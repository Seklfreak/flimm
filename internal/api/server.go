// Package api wires the HTTP API (/api/v1), the media proxy (/media) and the
// embedded SPA.
package api

import (
	"cmp"
	"context"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	sentryhttp "github.com/getsentry/sentry-go/http"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Seklfreak/archive-client/internal/db/sqlc"
	"github.com/Seklfreak/archive-client/internal/ta"
)

// BuildVersion is the running server's release version, set from main via
// ldflags (-X) and surfaced at GET /api/v1/config. "dev" in local builds.
var BuildVersion = "dev"

// Options configures a Server.
// defaultMinPlaySeconds mirrors config.MinPlaySeconds' default so tests and
// embedders that leave Options.MinPlaySeconds at zero behave like production.
const defaultMinPlaySeconds = 15

type Options struct {
	Pool         *pgxpool.Pool // nil in tests (Querier used directly)
	Querier      sqlc.Querier  // defaults to sqlc.New(Pool)
	TA           ta.Client
	MediaProxy   *httputil.ReverseProxy // nil disables /media/video and subtitles
	Log          *slog.Logger
	Verifier     *oidc.IDTokenVerifier // nil when AUTH_DISABLED
	AdminEmails  []string
	AppName      string
	OIDCIssuer   string
	OIDCClientID string
	MediaSecret  string
	// SecureCookies sets the Secure flag on the media cookie (https deploys).
	SecureCookies bool
	// MinPlaySeconds is how long a video must be played before it is recorded;
	// 0 uses the default.
	MinPlaySeconds float64
	// CORSOrigins are the browser origins allowed to call the API.
	CORSOrigins []string
	// Frontend is the built SPA (frontend/dist); nil serves no static files.
	Frontend fs.FS
}

type Server struct {
	pool          *pgxpool.Pool
	q             sqlc.Querier
	ta            ta.Client
	mediaProxy    *httputil.ReverseProxy
	thumbProxy    *httputil.ReverseProxy
	log           *slog.Logger
	verifier      *oidc.IDTokenVerifier
	adminEmails   map[string]bool
	appName       string
	oidcIssuer    string
	oidcClientID  string
	mediaSecret   []byte
	secureCookies bool
	corsOrigins   []string
	frontend      fs.FS
	// chapters caches derived chapter lists per video id.
	chapters *chaptersCache
	// minPlaySeconds gates recording a watch event; see Options.
	minPlaySeconds float64
}

func NewServer(o Options) *Server {
	admins := make(map[string]bool, len(o.AdminEmails))
	for _, e := range o.AdminEmails {
		admins[strings.ToLower(strings.TrimSpace(e))] = true
	}
	q := o.Querier
	if q == nil && o.Pool != nil {
		q = sqlc.New(o.Pool)
	}
	log := o.Log
	if log == nil {
		log = slog.Default()
	}
	s := &Server{
		pool:           o.Pool,
		q:              q,
		ta:             o.TA,
		mediaProxy:     o.MediaProxy,
		log:            log,
		verifier:       o.Verifier,
		adminEmails:    admins,
		appName:        o.AppName,
		oidcIssuer:     o.OIDCIssuer,
		oidcClientID:   o.OIDCClientID,
		mediaSecret:    []byte(o.MediaSecret),
		secureCookies:  o.SecureCookies,
		corsOrigins:    o.CORSOrigins,
		frontend:       o.Frontend,
		chapters:       newChaptersCache(),
		minPlaySeconds: cmp.Or(o.MinPlaySeconds, defaultMinPlaySeconds),
	}
	if o.MediaProxy != nil {
		// Thumbnails are immutable per id; let the browser keep them a day.
		thumbs := *o.MediaProxy
		thumbs.ModifyResponse = func(resp *http.Response) error {
			if resp.StatusCode == http.StatusOK {
				resp.Header.Set("Cache-Control", "private, max-age=86400")
			}
			return nil
		}
		s.thumbProxy = &thumbs
	}
	return s
}

func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	// RealIP is deprecated for spoofing reasons when directly exposed; the app
	// sits behind a trusted reverse proxy and the IP is only logged.
	r.Use(middleware.RealIP) //nolint:staticcheck // trusted-proxy-only; logging
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	// Inside Recoverer on purpose: a panic is captured to Sentry first, then
	// re-panicked for Recoverer to turn into a 500.
	r.Use(sentryhttp.New(sentryhttp.Options{Repanic: true}).Handle)
	if len(s.corsOrigins) > 0 {
		r.Use(cors.Handler(cors.Options{
			AllowedOrigins:   s.corsOrigins,
			AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
			AllowedHeaders:   []string{"Accept", "Content-Type", "Authorization", "Range", "If-Range"},
			ExposedHeaders:   []string{"Content-Range", "Accept-Ranges", "Content-Length", "Content-Type"},
			AllowCredentials: true,
			MaxAge:           300,
		}))
	}

	r.Get("/healthz", s.healthz)

	r.Route("/api/v1", func(r chi.Router) {
		// Per-user data; never let a browser cache it.
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Cache-Control", "no-store")
				next.ServeHTTP(w, r)
			})
		})
		r.Use(middleware.Timeout(120 * time.Second))

		r.Get("/config", s.getConfig)
		r.Get("/healthz", s.healthz)

		r.Group(func(r chi.Router) {
			r.Use(s.authMiddleware)

			r.Get("/me", s.getMe)
			r.Patch("/me/prefs", s.patchPrefs)
			r.Post("/session/media", s.setMediaCookie)

			r.Get("/feeds", s.listFeeds)
			r.Post("/feeds", s.createFeed)
			r.Post("/feeds/reorder", s.reorderFeeds)
			r.Get("/feeds/{id}", s.getFeed)
			r.Put("/feeds/{id}", s.updateFeed)
			r.Delete("/feeds/{id}", s.deleteFeed)
			r.Get("/feeds/{id}/videos", s.listFeedVideos)
			r.Post("/feeds/{id}/mark-seen", s.markFeedSeen)

			r.Get("/channels", s.listChannels)
			r.Get("/channels/{id}", s.getChannel)
			r.Get("/channels/{id}/videos", s.listChannelVideos)
			r.Get("/channels/{id}/playlists", s.listChannelPlaylists)
			r.Put("/channels/{id}/feeds", s.setChannelFeeds)
			r.Post("/channels/{id}/mark-seen", s.markChannelSeen)

			r.Get("/videos/{id}", s.getVideo)
			r.Get("/videos/{id}/up-next", s.upNext)
			r.Get("/videos/{id}/similar", s.similarVideos)
			r.Get("/videos/{id}/comments", s.videoComments)
			r.Get("/videos/{id}/chapters", s.getChapters)
			r.Post("/videos/{id}/progress", s.postProgress)
			r.Delete("/videos/{id}/progress", s.deleteProgress)
			r.Post("/videos/{id}/watched", s.postWatched)

			r.Get("/playlists", s.listPlaylists)
			r.Post("/playlists", s.createPlaylist)
			r.Get("/playlists/{id}", s.getPlaylist)
			r.Patch("/playlists/{id}", s.renamePlaylist)
			r.Delete("/playlists/{id}", s.deletePlaylist)
			r.Post("/playlists/{id}/videos", s.playlistVideoAction)

			r.Get("/history", s.listHistory)
			r.Delete("/history/{id}", s.deleteHistoryEntry)

			r.Get("/search", s.search)
		})
	})

	r.Route("/media", func(r chi.Router) {
		r.Use(s.mediaAuthMiddleware)
		r.Get("/video/{id}.mp4", s.mediaVideo)
		r.Get("/subtitles/{id}/{lang}.vtt", s.mediaSubtitles)
		r.Get("/thumb/video/{id}", s.mediaVideoThumb)
		r.Get("/thumb/channel/{id}", s.mediaChannelThumb)
		r.Get("/thumb/channel/{id}/banner", s.mediaChannelBanner)
		r.Get("/thumb/playlist/{id}", s.mediaPlaylistThumb)
	})

	if s.frontend != nil {
		r.Handle("/*", spaHandler(s.frontend))
	}
	return r
}

// getConfig is unauthenticated so native clients need only the server URL.
func (s *Server) getConfig(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"app_name":       s.appName,
		"oidc_issuer":    s.oidcIssuer,
		"oidc_client_id": s.oidcClientID,
		"version":        BuildVersion,
	})
}

// healthz is 200 when the DB answers; `ta` reports TA reachability. Admins
// (or dev mode) also see the TA error text.
func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	out := map[string]any{"status": "ok", "version": BuildVersion}
	status := http.StatusOK
	if s.pool != nil {
		if err := s.pool.Ping(ctx); err != nil {
			out["status"] = "degraded"
			out["db"] = "unreachable"
			status = http.StatusServiceUnavailable
		} else {
			out["db"] = "ok"
		}
	}
	if err := s.ta.Ping(ctx); err != nil {
		out["ta"] = "unreachable"
		if s.verifier == nil || s.isAdminEmail(currentEmailFromBearer(s, r)) {
			out["ta_error"] = err.Error()
		}
	} else {
		out["ta"] = "ok"
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, status, out)
}

// currentEmailFromBearer resolves an optional Bearer token on an
// unauthenticated route; "" when absent or invalid.
func currentEmailFromBearer(s *Server, r *http.Request) string {
	if bearerToken(r) == "" {
		return ""
	}
	u, _, _ := s.authenticate(r)
	if u == nil {
		return ""
	}
	return u.email
}

// spaHandler serves the built frontend, falling back to index.html for
// client-side routes. Hashed assets are immutable; everything else
// revalidates.
func spaHandler(dist fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(dist))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if strings.HasPrefix(path, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		if path != "/" {
			if _, err := fs.Stat(dist, strings.TrimPrefix(path, "/")); err != nil {
				r.URL.Path = "/"
			}
		}
		fileServer.ServeHTTP(w, r)
	})
}
