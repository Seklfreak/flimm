package api

import (
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/Seklfreak/flimm/internal/ta"
)

// TA's query parser crashes on a word with two colons, and our prefix means a
// colon in the user's first word becomes exactly that. The handler must never
// let a colon through.
func TestSearchSanitizesColons(t *testing.T) {
	var mu sync.Mutex
	var queries []string
	fake := &ta.Fake{
		SearchFn: func(query string) (*ta.SearchResult, error) {
			mu.Lock()
			queries = append(queries, query)
			mu.Unlock()
			return &ta.SearchResult{
				Videos: []ta.Video{}, Channels: []ta.Channel{}, Playlists: []ta.Playlist{}, Fulltext: []ta.SubtitleHit{},
			}, nil
		},
	}
	h := newTestServer(fake, newEventStore().querier()).Router()

	rec := do(t, h, http.MethodGet, "/api/v1/search?q="+"re%3Azero+at+1%3A23%3A45", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	if len(queries) != 4 {
		t.Fatalf("TA queries = %d, want 4 (one per index)", len(queries))
	}
	for _, q := range queries {
		prefix, term, ok := strings.Cut(q, ":")
		if !ok || prefix == "" {
			t.Fatalf("query %q lost its index prefix", q)
		}
		if strings.Contains(term, ":") {
			t.Fatalf("query %q carries a colon into TA", q)
		}
		if want := "re zero at 1 23 45"; term != want {
			t.Fatalf("term = %q, want %q", term, want)
		}
	}
}

// A query that is only colons must not reach TA at all: a bare index prefix
// is a match-all there and returns arbitrary documents.
func TestSearchAllColonsIsEmpty(t *testing.T) {
	fake := &ta.Fake{
		SearchFn: func(query string) (*ta.SearchResult, error) {
			t.Fatalf("TA searched with %q, want no call", query)
			return nil, nil
		},
	}
	h := newTestServer(fake, newEventStore().querier()).Router()

	rec := do(t, h, http.MethodGet, "/api/v1/search?q=%3A%3A", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	res := decode[map[string]any](t, rec)
	for _, bucket := range []string{"videos", "channels", "playlists"} {
		b, _ := res[bucket].(map[string]any)
		if n, _ := b["total"].(float64); n != 0 {
			t.Fatalf("%s total = %v, want 0", bucket, n)
		}
	}
}

// A subtitle hit is a WebVTT cue, so it arrives with karaoke markup, styling
// and entities. None of that belongs in a search result.
func TestSubtitleTextStripsMarkup(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"<c>and then</c><00:00:12.345><c> we left</c>", "and then we left"},
		{"<i>whispering</i> &amp; <b>shouting</b>", "whispering & shouting"},
		{`<font color="#ffffff">plain</font>`, "plain"},
		{"nothing&nbsp;to   strip", "nothing to strip"},
		{"already clean", "already clean"},
		{"<unclosed", ""},
	} {
		if got := subtitleText(tc.in); got != tc.want {
			t.Errorf("subtitleText(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
