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
