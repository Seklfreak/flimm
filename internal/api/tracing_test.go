package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/getsentry/sentry-go"
	sentryhttp "github.com/getsentry/sentry-go/http"
	"github.com/go-chi/chi/v5"

	"github.com/Seklfreak/flimm/internal/obs"
)

// captureTransport keeps what a client would have sent.
type captureTransport struct {
	events []*sentry.Event
}

func (c *captureTransport) Configure(sentry.ClientOptions)        {}
func (c *captureTransport) SendEvent(e *sentry.Event)             { c.events = append(c.events, e) }
func (c *captureTransport) Flush(time.Duration) bool              { return true }
func (c *captureTransport) FlushWithContext(context.Context) bool { return true }
func (c *captureTransport) Close()                                {}

// traced serves one request through sentryhttp and the naming middleware, and
// returns the transaction that came out.
func traced(t *testing.T, method, path string, route func(chi.Router)) *sentry.Event {
	t.Helper()
	capture := &captureTransport{}
	if err := sentry.Init(sentry.ClientOptions{
		Dsn:              "https://key@example.invalid/1",
		EnableTracing:    true,
		TracesSampleRate: 1,
		Transport:        capture,
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sentry.Init(sentry.ClientOptions{}) })

	r := chi.NewRouter()
	r.Use(sentryhttp.New(sentryhttp.Options{}).Handle)
	r.Use(traceRouteName)
	route(r)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	sentry.Flush(time.Second)

	if len(capture.events) != 1 {
		t.Fatalf("captured %d events, want one transaction", len(capture.events))
	}
	return capture.events[0]
}

// The whole point: one row per endpoint. Left to sentryhttp, the name is the
// pattern chi matched at the outer mux — `/api/v1/*` for every route in the
// app — so the tag this leaves is what carries the real one to send time.
func TestATransactionCarriesItsRoute(t *testing.T) {
	event := traced(t, http.MethodGet, "/api/v1/feeds/6f1e/videos", func(r chi.Router) {
		r.Route("/api/v1", func(r chi.Router) {
			r.Get("/feeds/{id}/videos", func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			})
		})
	})
	if want := "GET /api/v1/feeds/{id}/videos"; event.Tags[obs.RouteKey] != want {
		t.Errorf("route tag = %q, want %q", event.Tags[obs.RouteKey], want)
	}
	// And what sentryhttp left behind is exactly the name this replaces.
	if event.Transaction != "GET /api/v1/*" {
		t.Logf("sentryhttp named it %q", event.Transaction)
	}
}

// Most requests are not traced at all — media streaming, the health check, and
// anything the sampler dropped. The middleware runs on all of them and must be
// nothing but a pass-through there.
func TestTheMiddlewareIsHarmlessWithoutATransaction(t *testing.T) {
	r := chi.NewRouter()
	r.Use(traceRouteName)
	r.Get("/media/video/{id}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusPartialContent)
	})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/media/video/yt-id", nil))
	if rec.Code != http.StatusPartialContent {
		t.Errorf("status = %d, want the handler's own", rec.Code)
	}
}

// routeName is what the middleware asks; a request that never reached the
// router has nothing to say.
func TestRouteNameIsEmptyWithoutAMatch(t *testing.T) {
	if got := routeName(httptest.NewRequest(http.MethodGet, "/whatever", nil)); got != "" {
		t.Errorf("routeName = %q, want empty", got)
	}
}
