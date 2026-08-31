package obs

import (
	"net/http"

	"github.com/getsentry/sentry-go"
)

// Transport records every outgoing request as a span on the surrounding
// request transaction.
//
// Flimm's own work is cheap; almost all of a slow request is spent waiting on
// something else — TubeArchivist, SponsorBlock, DeArrow — and without this a
// trace shows a seven-second request made of one database query and a great
// deal of nothing. Which of those services, how many calls, and how long each
// took is the whole question, and it is only answerable from here.
//
// A request made outside a sampled transaction (startup, a health check, a
// background refresh) records nothing and pays a map lookup.
type Transport struct {
	// Base is the transport underneath. nil means http.DefaultTransport.
	Base http.RoundTripper
}

func (t Transport) RoundTrip(r *http.Request) (*http.Response, error) {
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	parent := sentry.SpanFromContext(r.Context())
	if parent == nil {
		return base.RoundTrip(r)
	}
	// The query string is left out on purpose: it carries video ids, channel
	// ids and media tokens, and a description that differs per request groups
	// into thousands of one-off rows instead of one line per endpoint.
	span := parent.StartChild("http.client", sentry.WithDescription(r.Method+" "+r.URL.Host+r.URL.Path))
	span.SetData("http.request.method", r.Method)
	span.SetData("server.address", r.URL.Host)
	span.SetData("url.path", r.URL.Path)
	defer span.Finish()

	resp, err := base.RoundTrip(r)
	switch {
	case err != nil:
		span.Status = sentry.SpanStatusInternalError
	default:
		span.SetData("http.response.status_code", resp.StatusCode)
		span.Status = spanStatusFor(resp.StatusCode)
	}
	return resp, err
}

// TracedClient is an http.Client whose requests become spans. It is what every
// outbound integration here is built on.
func TracedClient(base *http.Client) *http.Client {
	if base == nil {
		return &http.Client{Transport: Transport{}}
	}
	base.Transport = Transport{Base: base.Transport}
	return base
}

// spanStatusFor maps a status code to a span status, so a trace shows a failed
// call as failed rather than as a fast one.
func spanStatusFor(code int) sentry.SpanStatus {
	switch {
	case code >= 500:
		return sentry.SpanStatusInternalError
	case code == http.StatusNotFound:
		return sentry.SpanStatusNotFound
	case code == http.StatusTooManyRequests:
		return sentry.SpanStatusResourceExhausted
	case code >= 400:
		return sentry.SpanStatusInvalidArgument
	default:
		return sentry.SpanStatusOK
	}
}
