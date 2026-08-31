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
	"github.com/Seklfreak/flimm/internal/dearrow"
	"github.com/Seklfreak/flimm/internal/media"
	"github.com/Seklfreak/flimm/internal/obs"
	"github.com/Seklfreak/flimm/internal/ryd"
	"github.com/Seklfreak/flimm/internal/sponsorblock"
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

	// Segments are fetched by a hash prefix of the video id, so enabling this
	// by default costs the deployment no privacy; an offline install turns it
	// off with an empty SPONSORBLOCK_URL and keeps TA's snapshot.
	var sbClient *sponsorblock.Client
	if cfg.SponsorblockURL != "" {
		sbClient = sponsorblock.New(sponsorblock.Options{
			BaseURL:    cfg.SponsorblockURL,
			Categories: cfg.SponsorblockCategories,
			UserAgent:  "flimm/" + version,
			Log:        log,
		})
		log.Info("sponsorblock", "url", cfg.SponsorblockURL, "categories", cfg.SponsorblockCategories)
	} else {
		log.Info("sponsorblock disabled; using the TubeArchivist snapshot")
	}

	// Titles and thumbnails from the same project, asked for the same way (a
	// hash prefix, never the id). Nothing is looked up until a viewer turns
	// one of them on in their preferences.
	var deClient *dearrow.Client
	if cfg.DeArrowURL != "" {
		deClient = dearrow.New(dearrow.Options{
			BaseURL:   cfg.DeArrowURL,
			UserAgent: "flimm/" + version,
			Log:       log,
		})
		log.Info("dearrow", "url", cfg.DeArrowURL)
	} else {
		log.Info("dearrow disabled; titles and thumbnails come from the archive")
	}

	// Dislike counts. Off unless someone set RYD_URL: this service is asked
	// about a video by name rather than by hash prefix, so it is the one
	// integration here that tells a third party what is being watched.
	var rydClient *ryd.Client
	if cfg.RYDURL != "" {
		rydClient = ryd.New(ryd.Options{
			BaseURL:   cfg.RYDURL,
			UserAgent: "flimm/" + version,
			Log:       log,
		})
		log.Info("return youtube dislike", "url", cfg.RYDURL)
	} else {
		log.Info("return youtube dislike disabled; videos carry no dislike count")
	}

	srv := api.NewServer(api.Options{
		Pool:              pool,
		TA:                client,
		MediaProxy:        proxy,
		Log:               log,
		Verifier:          verifier,
		AdminEmails:       cfg.AdminEmails,
		AppName:           cfg.AppName,
		AnalyticsDisabled: cfg.AnalyticsDisabled,
		OIDCIssuer:        cfg.OIDCIssuer,
		OIDCClientID:      cfg.OIDCClientID,
		MediaSecret:       cfg.MediaTokenSecret,
		MinPlaySeconds:    cfg.MinPlaySeconds,
		MediaCache:        mediaCache,
		Sponsorblock:      sbClient,
		DeArrow:           deClient,
		RYD:               rydClient,
		FFmpegPath:        cfg.FFmpegPath,
		HWAccel:           hwaccel,
		SegmentWait:       cfg.MediaSegmentWait,
		SeekAheadSegments: cfg.MediaSeekAheadSegments,
		MediaTokenTTL:     cfg.MediaTokenTTL,
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
	// Everything Flimm asks a third party is cached and refreshed behind the
	// response; this starts the workers that do the refreshing, and the sweep
	// that keeps crowd titles ahead of new downloads. Stops with the process's
	// context.
	srv.StartCacheWarmer(ctx)

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
			// /media holds a connection open for as long as the player reads,
			// so reaching the deadline with streams still attached is how a
			// shutdown ordinarily ends rather than a fault — the process exits
			// a moment later and players reconnect, which is what they do
			// across a deploy anyway. Only an unexpected failure is an error.
			if errors.Is(err, context.DeadlineExceeded) {
				log.Info("shutdown: deadline passed with streams still open")
			} else {
				log.Error("shutdown", "err", err)
			}
		}
	}
	if mediaCache != nil {
		mediaCache.Close()
	}
}
