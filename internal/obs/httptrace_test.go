package obs

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/getsentry/sentry-go"
)

// captureTransport keeps the events a client would have sent, which is where
// the finished spans end up.
type captureTransport struct {
	events []*sentry.Event
}

func (c *captureTransport) Configure(sentry.ClientOptions)        {}
func (c *captureTransport) SendEvent(e *sentry.Event)             { c.events = append(c.events, e) }
func (c *captureTransport) Flush(time.Duration) bool              { return true }
func (c *captureTransport) Close()                                {}
func (c *captureTransport) FlushWithContext(context.Context) bool { return true }

// tracing runs fn inside a transaction and hands back the spans that reached
// the transport — the same list Sentry would receive.
func tracing(t *testing.T, fn func(ctx context.Context)) []*sentry.Span {
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

	tx := sentry.StartTransaction(context.Background(), "test")
	fn(tx.Context())
	tx.Finish()
	sentry.Flush(time.Second)

	if len(capture.events) != 1 {
		t.Fatalf("captured %d events, want the transaction", len(capture.events))
	}
	return capture.events[0].Spans
}

// The point of the whole file: a call to TubeArchivist or DeArrow shows up as
// its own span, so a slow request can be read as "waiting on that" rather than
// as unexplained time.
func TestAnOutgoingRequestBecomesASpan(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	spans := tracing(t, func(ctx context.Context) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/video/?channel=UC1", nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := TracedClient(nil).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
	})

	if len(spans) != 1 {
		t.Fatalf("recorded %d spans, want 1", len(spans))
	}
	span := spans[0]
	if span.Op != "http.client" {
		t.Errorf("op = %q", span.Op)
	}
	// The query carries video ids and media tokens, and a per-request
	// description groups into thousands of one-off rows.
	if want := "GET " + srv.Listener.Addr().String() + "/api/video/"; span.Description != want {
		t.Errorf("description = %q, want %q", span.Description, want)
	}
	if span.Status != sentry.SpanStatusOK {
		t.Errorf("status = %v", span.Status)
	}
}

// A failed call must read as failed. A 500 that looks like a fast success is
// how a broken dependency hides in a latency chart.
func TestAFailedCallIsAFailedSpan(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	spans := tracing(t, func(ctx context.Context) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
		resp, err := TracedClient(nil).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
	})
	if len(spans) != 1 || spans[0].Status != sentry.SpanStatusInternalError {
		t.Fatalf("spans = %+v, want one failed span", spans)
	}
}

// Requests outside a traced request — startup pings, the health check, a
// background refresh — record nothing and still work.
func TestARequestOutsideATransactionIsUntouched(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	defer srv.Close()

	resp, err := TracedClient(nil).Get(srv.URL)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTeapot {
		t.Errorf("status = %d", resp.StatusCode)
	}
}

// TracedClient keeps whatever transport it was handed — the streaming client
// has its own header timeout, and losing it would change how media behaves.
func TestTracedClientKeepsTheTransportItWraps(t *testing.T) {
	base := &http.Transport{ResponseHeaderTimeout: 42}
	client := TracedClient(&http.Client{Transport: base})
	wrapper, ok := client.Transport.(Transport)
	if !ok {
		t.Fatalf("transport = %T, want obs.Transport", client.Transport)
	}
	if wrapper.Base != http.RoundTripper(base) {
		t.Error("the wrapped transport was dropped")
	}
}
