// Package api wires the HTTP API (/api/v1), the media proxy (/media) and the
// embedded SPA.
package api

import (
	"cmp"
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	sentryhttp "github.com/getsentry/sentry-go/http"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Seklfreak/flimm/internal/db/sqlc"
	"github.com/Seklfreak/flimm/internal/dearrow"
	"github.com/Seklfreak/flimm/internal/media"
	"github.com/Seklfreak/flimm/internal/sponsorblock"
	"github.com/Seklfreak/flimm/internal/ta"
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
	// AnalyticsDisabled turns client-side usage analytics off for this
	// deployment. The endpoint is baked into each client at build time (see
	// the README), so this is the runtime opt-out an operator has: clients
	// read it from /api/v1/config and report nothing.
	AnalyticsDisabled bool
	// SecureCookies sets the Secure flag on the media cookie (https deploys).
	SecureCookies bool
	// MediaCache stores derived renditions; nil disables /media/audio/*.
	MediaCache *media.Cache
	// Sponsorblock fetches segments from a SponsorBlock server; nil falls
	// back to the snapshot TubeArchivist indexed at download time.
	Sponsorblock *sponsorblock.Client
	// DeArrow supplies crowd-sourced titles and thumbnails; nil disables them
	// for the deployment, whatever a viewer's preferences say.
	DeArrow *dearrow.Client
	// FFmpegPath is the ffmpeg binary used for derivations.
	FFmpegPath string
	// HWAccel is the hardware-transcode decision made at start-up; the zero
	// value keeps every transcode on the CPU.
	HWAccel media.HWAccel
	// SegmentWait is MEDIA_SEGMENT_WAIT: how long a request for an HLS segment
	// the transcode has not produced yet blocks before the client is told to
	// come back. 0 uses the default.
	SegmentWait time.Duration
	// SeekAheadSegments is MEDIA_SEEK_AHEAD_SEGMENTS: how far ahead of the
	// encoder a segment request has to be for the run to be re-aimed at it.
	// 0 uses the default.
	SeekAheadSegments int
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
	analyticsOff  bool
	mediaSecret   []byte
	secureCookies bool
	corsOrigins   []string
	frontend      fs.FS
	// chapters caches derived chapter lists per video id.
	chapters *chaptersCache
	// taHealth is the cached, time-boxed answer to "is TubeArchivist
	// reachable" that /healthz reports; see taStatus.
	taHealth taHealth
	// sponsorblock is the segment source; nil uses TA's snapshot.
	sponsorblock *sponsorblock.Client
	dearrow      *dearrow.Client
	// minPlaySeconds gates recording a watch event; see Options.
	minPlaySeconds float64
	mediaCache     *media.Cache
	ffmpegPath     string
	hwaccel        media.HWAccel
	// hlsJobs publishes running HLS jobs so a segment request can find the one
	// it is waiting on, steer it, and report its progress.
	hlsJobs           *media.HLSRegistry
	segmentWait       time.Duration
	seekAheadSegments int
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
		analyticsOff:   o.AnalyticsDisabled,
		mediaSecret:    []byte(o.MediaSecret),
		secureCookies:  o.SecureCookies,
		corsOrigins:    o.CORSOrigins,
		frontend:       o.Frontend,
		chapters:       newChaptersCache(),
		sponsorblock:   o.Sponsorblock,
		dearrow:        o.DeArrow,
		minPlaySeconds: cmp.Or(o.MinPlaySeconds, defaultMinPlaySeconds),
		mediaCache:     o.MediaCache,
		ffmpegPath:     cmp.Or(o.FFmpegPath, "ffmpeg"),
		hwaccel:        o.HWAccel,

		hlsJobs:           media.NewHLSRegistry(),
		segmentWait:       cmp.Or(o.SegmentWait, defaultSegmentWait),
		seekAheadSegments: cmp.Or(o.SeekAheadSegments, media.DefaultSeekAheadSegments),
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
	r.Get("/livez", s.livez)

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
		r.Get("/livez", s.livez)

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
			r.Get("/videos/{id}/nav", s.videoNav)
			r.Get("/videos/{id}/similar", s.similarVideos)
			r.Get("/videos/{id}/comments", s.videoComments)
			r.Get("/videos/{id}/chapters", s.getChapters)
			r.Get("/videos/{id}/loudness", s.getVideoLoudness)
			r.Post("/videos/{id}/progress", s.postProgress)
			r.Delete("/videos/{id}/progress", s.deleteProgress)
			r.Post("/videos/{id}/watched", s.postWatched)
			r.Post("/videos/{id}/dismiss", s.dismissVideo)
			r.Delete("/videos/{id}/dismiss", s.undismissVideo)
			r.Post("/videos/{id}/hls", s.postVideoHLS)

			r.Get("/playlists", s.listPlaylists)
			r.Get("/playlists/pinned", s.listPinnedPlaylists)
			r.Put("/playlists/{id}/music", s.setPlaylistMusic)
			r.Put("/playlists/{id}/pinned", s.setPlaylistPinned)
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
		r.Get("/frame/{id}/{ms}.jpg", s.mediaFrame)
		r.Get("/preview/{id}/{file}", s.mediaPreview)
		r.Get("/audio/{id}.webm", s.mediaAudio)
		r.Get("/audio/{id}.m4a", s.mediaAudioAAC)
		// One route for the playlist and every segment: AVPlayer re-sends the
		// media credentials on each one, so they must share the auth gate. The
		// path without a height is the alias older clients use; it serves the
		// default height's entry rather than an entry of its own.
		r.Get("/hls/{id}/{file}", s.mediaHLSDefault)
		r.Get("/hls/{id}/{height}/{file}", s.mediaHLS)
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

// ConfigResponse is GET /api/v1/config: everything a client needs beyond the
// server URL.
//
// AuthDisabled is said out loud rather than left to be inferred from empty
// OIDC fields, because the two cases are opposites for a client: a server
// deliberately running without auth is one to connect to, while a server that
// wants auth but publishes no issuer is broken and must not be.
type ConfigResponse struct {
	AppName      string `json:"app_name"`
	OIDCIssuer   string `json:"oidc_issuer"`
	OIDCClientID string `json:"oidc_client_id"`
	Version      string `json:"version"`
	AuthDisabled bool   `json:"auth_disabled"`
	// AnalyticsDisabled is the deployment's opt-out from the usage analytics
	// its clients were built with (ANALYTICS_DISABLED=true). Clients that were
	// built without an analytics endpoint report nothing either way.
	AnalyticsDisabled bool `json:"analytics_disabled"`
}

// getConfig is unauthenticated so native clients need only the server URL.
func (s *Server) getConfig(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, ConfigResponse{
		AppName:      s.appName,
		OIDCIssuer:   s.oidcIssuer,
		OIDCClientID: s.oidcClientID,
		Version:      BuildVersion,
		// AUTH_DISABLED is exactly "there is no verifier": every request is
		// the fixed dev user.
		AuthDisabled:      s.verifier == nil,
		AnalyticsDisabled: s.analyticsOff,
	})
}

// healthz answers "can this instance serve traffic": 200 when the database
// answers, 503 when it does not. `ta` reports TubeArchivist's reachability
// beside that verdict without deciding it — Flimm still serves its frontend,
// its auth and everything already cached when the archive is away. Admins (or
// dev mode) also see the TA error text.
//
// This is the readiness endpoint. Point liveness at /livez instead: a database
// outage is not something restarting the process can fix, and a probe that
// restarts on it only adds downtime to an outage.
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
	state, taErr := s.taStatus(ctx)
	out["ta"] = state
	if taErr != nil && (s.verifier == nil || s.isAdminEmail(currentEmailFromBearer(s, r))) {
		out["ta_error"] = taErr.Error()
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

// ---- health ----

const (
	// taHealthTimeout bounds the TubeArchivist check inside /healthz.
	//
	// A readiness probe gives this endpoint about a second before it counts as
	// a failure, and TA's reachability is reported beside `status` rather than
	// deciding it. A slow archive must therefore never be able to make the
	// probe late: after this long the answer is "slow", which is the honest
	// thing to report and costs the probe nothing.
	taHealthTimeout = 500 * time.Millisecond
	// taHealthTTL reuses the last answer between probes, so probing every ten
	// seconds does not mean asking TubeArchivist every ten seconds.
	taHealthTTL = 15 * time.Second
)

type taHealth struct {
	mu    sync.Mutex
	state string
	err   error
	exp   time.Time
}

// taStatus reports TubeArchivist's reachability for /healthz: cached, and
// time-boxed so a slow archive cannot hold the probe open. It returns the
// state to publish ("ok", "slow" or "unreachable") and the underlying error,
// which only an admin gets to see.
func (s *Server) taStatus(ctx context.Context) (string, error) {
	s.taHealth.mu.Lock()
	if time.Now().Before(s.taHealth.exp) {
		state, err := s.taHealth.state, s.taHealth.err
		s.taHealth.mu.Unlock()
		return state, err
	}
	s.taHealth.mu.Unlock()

	probe, cancel := context.WithTimeout(ctx, taHealthTimeout)
	defer cancel()
	err := s.ta.Ping(probe)

	state := "ok"
	switch {
	case err == nil:
	case ctx.Err() != nil:
		// The caller went away, not TA. Report it, but do not remember it.
		return "unknown", err
	case errors.Is(probe.Err(), context.DeadlineExceeded):
		state = "slow"
	default:
		state = "unreachable"
	}

	s.taHealth.mu.Lock()
	s.taHealth.state, s.taHealth.err, s.taHealth.exp = state, err, time.Now().Add(taHealthTTL)
	s.taHealth.mu.Unlock()
	return state, err
}

// livez is the liveness endpoint: 200 whenever the process is running and its
// router is answering. It deliberately touches nothing else.
//
// Liveness answers "should I be restarted", which is a different question from
// "can I serve traffic" — that one is /healthz. Pointing liveness at a check
// that reaches the database or TubeArchivist means an outage in either
// restarts the pod, which cannot fix the outage and adds downtime to it.
func (s *Server) livez(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "version": BuildVersion})
}
