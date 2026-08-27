// Command server runs the Flimm backend: the /api/v1 JSON API, the /media
// proxy to TubeArchivist and the embedded web frontend.
package main

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"

	"github.com/Seklfreak/flimm"
	"github.com/Seklfreak/flimm/internal/api"
	"github.com/Seklfreak/flimm/internal/config"
	"github.com/Seklfreak/flimm/internal/db"
	"github.com/Seklfreak/flimm/internal/media"
	"github.com/Seklfreak/flimm/internal/obs"
	"github.com/Seklfreak/flimm/internal/ta"
)

// version is the release version, injected at build time via
// -ldflags "-X main.version=...". "dev" for local builds.
var version = "dev"

func main() {
	// Sentry first (SENTRY_DSN unset = disabled, the local default) so the
	// logger below forwards error records as events. Only API request
	// transactions are traced; media streaming, health checks and static
	// paths are dropped.
	flush, sentryErr := obs.Init("flimm@"+version, func(name string) bool {
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

	// Cancelled on SIGINT/SIGTERM: the shutdown below stops accepting requests
	// and then kills any running transcode, so a rolling deploy leaves no
	// orphaned ffmpeg and no half-written cache entry.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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

	dist, err := fs.Sub(flimm.FrontendFS, "frontend/dist")
	if err != nil {
		log.Error("frontend fs", "err", err)
		os.Exit(1)
	}

	mediaCache, err := media.NewCache(cfg.MediaCacheDir, cfg.MediaCacheMaxBytes, cfg.MediaTranscodeJobs, log)
	if err != nil {
		// Derived media is one feature; the rest of the app still works, so
		// log and carry on with /media/audio and /media/hls disabled rather
		// than exiting.
		log.Error("media cache disabled", "dir", cfg.MediaCacheDir, "err", err)
		mediaCache = nil
	}

	// Decided once, here, rather than per transcode: the answer cannot change
	// while the process runs, and an operator who passed a GPU in wants to see
	// on the first line whether it was found.
	hwMode := media.ParseHWAccelMode(cfg.MediaHWAccel)
	hwaccel, hwReason := media.ResolveHWAccel(hwMode, cfg.MediaVAAPIDevice)
	log.Info("media hardware acceleration",
		"mode", string(hwMode), "vaapi", hwaccel.VAAPI, "device", hwaccel.Device, "reason", hwReason)

	srv := api.NewServer(api.Options{
		Pool:              pool,
		TA:                client,
		MediaProxy:        proxy,
		Log:               log,
		Verifier:          verifier,
		AdminEmails:       cfg.AdminEmails,
		AppName:           cfg.AppName,
		OIDCIssuer:        cfg.OIDCIssuer,
		OIDCClientID:      cfg.OIDCClientID,
		MediaSecret:       cfg.MediaTokenSecret,
		MinPlaySeconds:    cfg.MinPlaySeconds,
		MediaCache:        mediaCache,
		FFmpegPath:        cfg.FFmpegPath,
		HWAccel:           hwaccel,
		SegmentWait:       cfg.MediaSegmentWait,
		SeekAheadSegments: cfg.MediaSeekAheadSegments,
		SecureCookies:     cfg.SecureCookies(),
		CORSOrigins:       append([]string{cfg.PublicURL}, cfg.CORSOrigins...),
		Frontend:          dist,
	})

	// Header timeout only: /media streams hold the connection for as long as
	// the player reads, so a server-wide read/write timeout would cut them.
	httpServer := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           srv.Router(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Info("listening", "port", cfg.Port)
	serveErr := make(chan error, 1)
	go func() { serveErr <- httpServer.ListenAndServe() }()

	select {
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server", "err", err)
			os.Exit(1)
		}
	case <-ctx.Done():
		log.Info("shutting down")
		// Stop accepting first, give in-flight requests a moment, then cancel
		// the derivations — a transcode killed while a player is mid-segment
		// is fine (it restarts), an orphaned ffmpeg is not.
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			log.Error("shutdown", "err", err)
		}
	}
	if mediaCache != nil {
		mediaCache.Close()
	}
}
