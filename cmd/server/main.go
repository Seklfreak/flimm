// Command server runs the Archive backend: the /api/v1 JSON API, the /media
// proxy to TubeArchivist and the embedded web frontend.
package main

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"

	archive "github.com/Seklfreak/archive-client"
	"github.com/Seklfreak/archive-client/internal/api"
	"github.com/Seklfreak/archive-client/internal/config"
	"github.com/Seklfreak/archive-client/internal/db"
	"github.com/Seklfreak/archive-client/internal/media"
	"github.com/Seklfreak/archive-client/internal/obs"
	"github.com/Seklfreak/archive-client/internal/ta"
)

// version is the release version, injected at build time via
// -ldflags "-X main.version=...". "dev" for local builds.
var version = "dev"

func main() {
	// Sentry first (SENTRY_DSN unset = disabled, the local default) so the
	// logger below forwards error records as events. Only API request
	// transactions are traced; media streaming, health checks and static
	// paths are dropped.
	flush, sentryErr := obs.Init("archive-client@"+version, func(name string) bool {
		return strings.Contains(name, "/api/")
	})
	defer flush()
	log := obs.NewLogger()
	if sentryErr != nil {
		log.Error("sentry init", "err", sentryErr)
		os.Exit(1)
	}
	log.Info("starting", "version", version)
	api.BuildVersion = version

	cfg, err := config.Load()
	if err != nil {
		log.Error("config", "err", err)
		os.Exit(1)
	}
	slog.SetLogLoggerLevel(cfg.LogLevel)

	log.Info("running migrations")
	if err := db.Migrate(cfg.DatabaseURL); err != nil {
		log.Error("migrate", "err", err)
		os.Exit(1)
	}

	ctx := context.Background()
	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Error("db connect", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	var verifier *oidc.IDTokenVerifier
	if cfg.AuthDisabled {
		log.Warn("AUTH_DISABLED set — API is unauthenticated")
		if err := db.BootstrapDevUser(ctx, pool, api.DevUserID, api.DevUserSub); err != nil {
			log.Error("bootstrap dev user", "err", err)
			os.Exit(1)
		}
	} else {
		verifier, err = api.NewVerifier(ctx, cfg.OIDCIssuer, cfg.OIDCClientID)
		if err != nil {
			log.Error("oidc verifier", "err", err)
			os.Exit(1)
		}
		log.Info("oidc auth enabled", "issuer", cfg.OIDCIssuer)
	}

	client := ta.New(cfg.TAURL, cfg.TAToken)
	if err := client.Ping(ctx); err != nil {
		// Not fatal: TA may still be booting; /healthz reports it.
		log.Warn("tubearchivist not reachable at startup", "err", err)
	}
	proxy, err := ta.NewMediaProxy(cfg.TAURL, cfg.TAToken)
	if err != nil {
		log.Error("media proxy", "err", err)
		os.Exit(1)
	}

	dist, err := fs.Sub(archive.FrontendFS, "frontend/dist")
	if err != nil {
		log.Error("frontend fs", "err", err)
		os.Exit(1)
	}

	mediaCache, err := media.NewCache(cfg.MediaCacheDir, cfg.MediaCacheMaxBytes, log)
	if err != nil {
		// Derived media is one feature; the rest of the app still works, so
		// log and carry on with /media/audio disabled rather than exiting.
		log.Error("media cache disabled", "dir", cfg.MediaCacheDir, "err", err)
		mediaCache = nil
	}

	srv := api.NewServer(api.Options{
		Pool:           pool,
		TA:             client,
		MediaProxy:     proxy,
		Log:            log,
		Verifier:       verifier,
		AdminEmails:    cfg.AdminEmails,
		AppName:        cfg.AppName,
		OIDCIssuer:     cfg.OIDCIssuer,
		OIDCClientID:   cfg.OIDCClientID,
		MediaSecret:    cfg.MediaTokenSecret,
		MinPlaySeconds: cfg.MinPlaySeconds,
		MediaCache:     mediaCache,
		FFmpegPath:     cfg.FFmpegPath,
		SecureCookies:  cfg.SecureCookies(),
		CORSOrigins:    append([]string{cfg.PublicURL}, cfg.CORSOrigins...),
		Frontend:       dist,
	})

	// Header timeout only: /media streams hold the connection for as long as
	// the player reads, so a server-wide read/write timeout would cut them.
	httpServer := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           srv.Router(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Info("listening", "port", cfg.Port)
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("server", "err", err)
		os.Exit(1)
	}
}
