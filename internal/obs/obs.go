// Package obs wires Sentry error reporting and tracing for the server:
// MCP binaries: a DSN-gated Init, and a slog logger that forwards
// error-level records to Sentry so existing log.Error call sites become
// captured events without changing them.
package obs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/getsentry/sentry-go"
)

// Init configures Sentry from SENTRY_DSN — unset (local runs) disables it,
// and every capture in the codebase becomes a no-op. release names the
// deploy (e.g. "archive@1.2.3"); keepTx selects which transaction names
// are traced, everything else is dropped (health checks, static assets).
// The returned flush is to be deferred by main.
func Init(release string, keepTx func(name string) bool) (flush func(), err error) {
	dsn := os.Getenv("SENTRY_DSN")
	if dsn == "" {
		return func() {}, nil
	}
	env := os.Getenv("SENTRY_ENVIRONMENT")
	if env == "" {
		env = "production"
	}
	err = sentry.Init(sentry.ClientOptions{
		Dsn:         dsn,
		Release:     release,
		Environment: env,
		// Handled errors are captured as plain errors; the stack at the
		// capture site is what locates them.
		AttachStacktrace: true,
		EnableTracing:    true,
		BeforeSend:       dropAbortedRequests,
		TracesSampler: func(sctx sentry.SamplingContext) float64 {
			if keepTx(sctx.Span.Name) {
				return 1
			}
			return 0
		},
	})
	return func() { sentry.Flush(2 * time.Second) }, err
}

// dropAbortedRequests discards the panic net/http uses to tear down a response
// the client stopped reading. ReverseProxy raises http.ErrAbortHandler when a
// browser walks away from a media stream — a seek, a pause, a closed tab —
// which for a video app is ordinary traffic rather than a fault. chi's
// Recoverer already re-panics it unlogged, but sentryhttp recovers every panic
// alike, so the event has to be dropped here. The panic still propagates, so
// the connection is aborted exactly as before.
func dropAbortedRequests(event *sentry.Event, hint *sentry.EventHint) *sentry.Event {
	if hint == nil {
		return event
	}
	if err, ok := hint.RecoveredException.(error); ok && errors.Is(err, http.ErrAbortHandler) {
		return nil
	}
	return event
}

// NewLogger returns the process logger: text on stdout as before, with
// error-level records also captured as Sentry events. The record's "err"
// attribute (when present) becomes the captured error's cause.
func NewLogger() *slog.Logger {
	return slog.New(fanout{
		slog.NewTextHandler(os.Stdout, nil),
		sentryHandler{},
	})
}

// sentryHandler captures error-level records. Groups are not used by this
// codebase, so WithGroup flattens.
type sentryHandler struct {
	attrs []slog.Attr
}

func (sentryHandler) Enabled(_ context.Context, lvl slog.Level) bool {
	return lvl >= slog.LevelError
}

func (h sentryHandler) Handle(ctx context.Context, rec slog.Record) error {
	var cause error
	var parts []string
	collect := func(a slog.Attr) {
		if e, ok := a.Value.Resolve().Any().(error); ok && cause == nil {
			cause = e
			return
		}
		parts = append(parts, a.Key+"="+a.Value.String())
	}
	for _, a := range h.attrs {
		collect(a)
	}
	rec.Attrs(func(a slog.Attr) bool { collect(a); return true })

	msg := rec.Message
	if len(parts) > 0 {
		msg += " (" + strings.Join(parts, " ") + ")"
	}
	hub := sentry.GetHubFromContext(ctx)
	if hub == nil {
		hub = sentry.CurrentHub()
	}
	if cause != nil {
		// A canceled request context means the client went away mid-request
		// — logged, but not an app error.
		if errors.Is(cause, context.Canceled) {
			return nil
		}
		hub.CaptureException(fmt.Errorf("%s: %w", msg, cause))
	} else {
		hub.CaptureMessage(msg)
	}
	return nil
}

func (h sentryHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	h.attrs = append(append([]slog.Attr(nil), h.attrs...), attrs...)
	return h
}

func (h sentryHandler) WithGroup(string) slog.Handler { return h }

// fanout delivers each record to every handler that wants its level.
type fanout []slog.Handler

func (f fanout) Enabled(ctx context.Context, lvl slog.Level) bool {
	for _, h := range f {
		if h.Enabled(ctx, lvl) {
			return true
		}
	}
	return false
}

func (f fanout) Handle(ctx context.Context, rec slog.Record) error {
	var err error
	for _, h := range f {
		if h.Enabled(ctx, rec.Level) {
			err = errors.Join(err, h.Handle(ctx, rec.Clone()))
		}
	}
	return err
}

func (f fanout) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := make(fanout, len(f))
	for i, h := range f {
		out[i] = h.WithAttrs(attrs)
	}
	return out
}

func (f fanout) WithGroup(name string) slog.Handler {
	out := make(fanout, len(f))
	for i, h := range f {
		out[i] = h.WithGroup(name)
	}
	return out
}
