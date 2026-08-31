package obs

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/getsentry/sentry-go"
)

// A client that walks away mid-stream is the common case for a video app, not
// a fault: ReverseProxy turns it into a panic(http.ErrAbortHandler), and
// sentryhttp captures every panic alike. Without this filter a single evening
// of seeking fills the project with fatal events nobody can act on.
func TestDropAbortedRequestsDropsAbortHandlerPanics(t *testing.T) {
	hint := &sentry.EventHint{RecoveredException: http.ErrAbortHandler}
	if got := dropAbortedRequests(&sentry.Event{}, hint); got != nil {
		t.Errorf("event = %v, want nil (dropped)", got)
	}
}

// The panic travels wrapped in some middleware chains, so identity alone would
// let it through — the filter has to unwrap.
func TestDropAbortedRequestsDropsWrappedAbortHandlerPanics(t *testing.T) {
	hint := &sentry.EventHint{
		RecoveredException: fmt.Errorf("proxy media: %w", http.ErrAbortHandler),
	}
	if got := dropAbortedRequests(&sentry.Event{}, hint); got != nil {
		t.Errorf("event = %v, want nil (dropped)", got)
	}
}

// Everything else still has to reach Sentry — a filter that swallows real
// panics is worse than no filter at all.
func TestDropAbortedRequestsKeepsOtherPanics(t *testing.T) {
	cases := []struct {
		name string
		hint *sentry.EventHint
	}{
		{"nil hint", nil},
		{"no recovered value", &sentry.EventHint{}},
		{"an unrelated error", &sentry.EventHint{RecoveredException: errors.New("boom")}},
		{"a non-error panic", &sentry.EventHint{RecoveredException: "boom"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			event := &sentry.Event{}
			if got := dropAbortedRequests(event, tc.hint); got != event {
				t.Errorf("event = %v, want it kept", got)
			}
		})
	}
}

// The route a handler matched becomes the transaction's name. Without this the
// name is whatever sentryhttp derived on the way out — for a mounted router,
// the mount pattern, which is the same string for every endpoint in the app.
func TestNameByRoutePromotesTheRouteTag(t *testing.T) {
	event := &sentry.Event{
		Transaction: "GET /api/v1/*",
		Tags:        map[string]string{RouteKey: "GET /api/v1/feeds/{id}/videos", "other": "kept"},
	}
	got := nameByRoute(event, nil)
	if got.Transaction != "GET /api/v1/feeds/{id}/videos" {
		t.Errorf("transaction = %q", got.Transaction)
	}
	// `route` is what stops Sentry clustering the name back into a wildcard.
	if got.TransactionInfo == nil || got.TransactionInfo.Source != sentry.SourceRoute {
		t.Errorf("source = %+v", got.TransactionInfo)
	}
	// The tag has done its job; leaving it on adds a second copy of the name
	// to every transaction.
	if _, ok := got.Tags[RouteKey]; ok {
		t.Error("the route tag was left behind")
	}
	if got.Tags["other"] != "kept" {
		t.Error("an unrelated tag was dropped")
	}
}

// A transaction with no route — a 404, or anything that never reached the
// router — keeps the name it arrived with rather than being renamed to nothing.
func TestNameByRouteLeavesUntaggedTransactionsAlone(t *testing.T) {
	event := &sentry.Event{Transaction: "GET /nope"}
	if got := nameByRoute(event, nil); got.Transaction != "GET /nope" {
		t.Errorf("transaction = %q, want it untouched", got.Transaction)
	}
}
