package api

import (
	"net/http"

	"github.com/getsentry/sentry-go"
	"github.com/go-chi/chi/v5"

	"github.com/Seklfreak/flimm/internal/obs"
)

// Naming transactions after the route rather than the URL.
//
// A transaction's name is what Sentry groups by, and every latency number is
// per name. sentryhttp derives it from `http.Request.Pattern`, which chi fills
// in with the pattern matched at the *outermost* mux — for this app that is the
// mount, `/api/v1/*`. So every read the API does arrived under one name, and
// the p95 on the board was an average over the whole API: a number about
// nothing, and the reason a seven-second request could not be traced to an
// endpoint.
//
// The route pattern is the name that was wanted: one row per endpoint,
// `GET /api/v1/feeds/{id}/videos`, comparable with itself over time. chi knows
// it only after matching, so it is read on the way out — and it cannot simply
// be assigned, because sentryhttp overwrites the name in a deferred call after
// every handler returns. The handler leaves it in a tag instead, and
// `obs.nameByRoute` promotes that to the name at send time, which is later
// still.

// traceRouteName leaves the matched route where obs.nameByRoute can find it.
//
// Setting `tx.Name` here would be simpler and does not work: sentryhttp
// overwrites the name in a deferred call after this returns. See obs.RouteKey.
func traceRouteName(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
		tx := sentry.TransactionFromContext(r.Context())
		if tx == nil {
			return
		}
		if name := routeName(r); name != "" {
			tx.SetTag(obs.RouteKey, name)
		}
	})
}

// routeName is the matched route as a transaction name, or "" when nothing
// matched — a 404 has no route to name, and calling it one would invent an
// endpoint that does not exist.
func routeName(r *http.Request) string {
	rc := chi.RouteContext(r.Context())
	if rc == nil {
		return ""
	}
	pattern := rc.RoutePattern()
	if pattern == "" || pattern == "/*" {
		return ""
	}
	return r.Method + " " + pattern
}
