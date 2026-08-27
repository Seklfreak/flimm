package sponsorblock

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"
	"time"
)

func TestHashPrefixHidesTheVideoID(t *testing.T) {
	// sha256("dQw4w9WgXcQ") starts 5f6b0b4e…; the lookup sends the first four.
	if got := HashPrefix("dQw4w9WgXcQ"); got != "5f6b" {
		t.Errorf("HashPrefix = %q, want %q", got, "5f6b")
	}
	if got := len(HashPrefix("anything")); got != hashPrefixLen {
		t.Errorf("prefix length = %d, want %d", got, hashPrefixLen)
	}
}

// serve runs a stub SponsorBlock server, recording the requests it saw.
func serve(t *testing.T, h http.HandlerFunc) (*httptest.Server, *[]*http.Request) {
	t.Helper()
	var got []*http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r)
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

func TestSegmentsPicksOurVideoOutOfThePrefix(t *testing.T) {
	body := `[
	  {"videoID":"other","segments":[{"category":"sponsor","actionType":"skip","segment":[0,10]}]},
	  {"videoID":"v1","segments":[
	    {"category":"outro","actionType":"skip","segment":[300,320]},
	    {"category":"sponsor","actionType":"skip","segment":[12.5,45.5]},
	    {"category":"music_offtopic","actionType":"mute","segment":[60,70]},
	    {"category":"poi_highlight","actionType":"poi","segment":[100,100]},
	    {"category":"chapter","actionType":"chapter","segment":[0,90],"description":"  Intro  "},
	    {"category":"selfpromo","actionType":"skip","segment":[500,400]},
	    {"category":"","actionType":"skip","segment":[1,2]},
	    {"category":"filler","actionType":"skip","segment":[80,80]}
	  ]}
	]`
	srv, reqs := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	})
	c := New(Options{BaseURL: srv.URL})

	got, err := c.Segments(context.Background(), "v1")
	if err != nil {
		t.Fatalf("Segments: %v", err)
	}
	want := []Segment{
		{Category: "chapter", ActionType: ActionChapter, Start: 0, End: 90, Description: "Intro"},
		{Category: "sponsor", ActionType: ActionSkip, Start: 12.5, End: 45.5},
		{Category: "music_offtopic", ActionType: ActionMute, Start: 60, End: 70},
		{Category: "poi_highlight", ActionType: ActionPOI, Start: 100, End: 100},
		{Category: "outro", ActionType: ActionSkip, Start: 300, End: 320},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("segments = %+v\nwant %+v", got, want)
	}

	// The request carries the hash prefix, never the video id.
	r := (*reqs)[0]
	if r.URL.Path != "/api/skipSegments/"+HashPrefix("v1") {
		t.Errorf("path = %q", r.URL.Path)
	}
	if raw := r.URL.String(); contains(raw, "v1") {
		t.Errorf("request leaks the video id: %q", raw)
	}
	q := r.URL.Query()
	if !contains(q.Get("categories"), "poi_highlight") || !contains(q.Get("categories"), CategoryChapter) {
		t.Errorf("categories = %q", q.Get("categories"))
	}
	// The API defaults to skip-only, so the other actions must be asked for.
	for _, a := range []string{ActionMute, ActionPOI, ActionFull, ActionChapter} {
		if !contains(q.Get("actionTypes"), a) {
			t.Errorf("actionTypes = %q, missing %q", q.Get("actionTypes"), a)
		}
	}
}

func TestSegmentsRequestsOnlyTheConfiguredCategories(t *testing.T) {
	srv, reqs := serve(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`[]`)) })
	c := New(Options{BaseURL: srv.URL, Categories: []string{"sponsor"}})
	if _, err := c.Segments(context.Background(), "v1"); err != nil {
		t.Fatalf("Segments: %v", err)
	}
	if got := (*reqs)[0].URL.Query().Get("categories"); got != `["sponsor"]` {
		t.Errorf("categories = %q", got)
	}
}

func TestSegmentsMissingActionTypeIsASkip(t *testing.T) {
	srv, _ := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"videoID":"v1","segments":[{"category":"sponsor","segment":[1,2]}]}]`))
	})
	got, err := New(Options{BaseURL: srv.URL}).Segments(context.Background(), "v1")
	if err != nil {
		t.Fatalf("Segments: %v", err)
	}
	if len(got) != 1 || got[0].ActionType != ActionSkip {
		t.Errorf("segments = %+v", got)
	}
}

func TestSegmentsNoEntryIsAnAnswerNotAFailure(t *testing.T) {
	for name, h := range map[string]http.HandlerFunc{
		"404": func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) },
		"other videos": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`[{"videoID":"zz","segments":[]}]`))
		},
		"empty response": func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`[]`)) },
	} {
		t.Run(name, func(t *testing.T) {
			srv, reqs := serve(t, h)
			c := New(Options{BaseURL: srv.URL})
			got, err := c.Segments(context.Background(), "v1")
			if err != nil || len(got) != 0 {
				t.Fatalf("segments = %+v, err = %v", got, err)
			}
			// Cached: "nobody submitted anything" is worth remembering too.
			if _, err := c.Segments(context.Background(), "v1"); err != nil {
				t.Fatalf("second call: %v", err)
			}
			if len(*reqs) != 1 {
				t.Errorf("requests = %d, want 1", len(*reqs))
			}
		})
	}
}

func TestSegmentsCachesTheAnswer(t *testing.T) {
	srv, reqs := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"videoID":"v1","segments":[{"category":"sponsor","actionType":"skip","segment":[1,2]}]}]`))
	})
	c := New(Options{BaseURL: srv.URL})
	for range 3 {
		if _, err := c.Segments(context.Background(), "v1"); err != nil {
			t.Fatalf("Segments: %v", err)
		}
	}
	if len(*reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(*reqs))
	}
	// Past the TTL it is fetched again.
	c.now = func() time.Time { return time.Now().Add(2 * defaultTTL) }
	if _, err := c.Segments(context.Background(), "v1"); err != nil {
		t.Fatalf("after ttl: %v", err)
	}
	if len(*reqs) != 2 {
		t.Errorf("requests after ttl = %d, want 2", len(*reqs))
	}
}

func TestSegmentsRemembersAFailureAsAFailure(t *testing.T) {
	srv, reqs := serve(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) })
	c := New(Options{BaseURL: srv.URL})
	if _, err := c.Segments(context.Background(), "v1"); err == nil {
		t.Fatal("want an error for a 500")
	}
	// The failure is remembered, but as an error — a caller must keep falling
	// back to its own source instead of reading it as "no segments".
	_, err := c.Segments(context.Background(), "v1")
	if !errors.Is(err, ErrUnavailable) {
		t.Errorf("second call err = %v, want ErrUnavailable", err)
	}
	if len(*reqs) != 1 {
		t.Errorf("requests = %d, want 1 (the failure is not retried at once)", len(*reqs))
	}
	c.now = func() time.Time { return time.Now().Add(2 * errTTL) }
	if _, err := c.Segments(context.Background(), "v1"); err == nil || errors.Is(err, ErrUnavailable) {
		t.Errorf("after errTTL the lookup runs again, err = %v", err)
	}
	if len(*reqs) != 2 {
		t.Errorf("requests after errTTL = %d, want 2", len(*reqs))
	}
}

func TestACancelledCallerDoesNotPoisonTheCache(t *testing.T) {
	srv, reqs := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"videoID":"v1","segments":[{"category":"sponsor","actionType":"skip","segment":[1,2]}]}]`))
	})
	c := New(Options{BaseURL: srv.URL})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Segments(ctx, "v1"); err == nil {
		t.Fatal("want an error for a cancelled context")
	}
	// The next caller gets a real lookup, not the remembered failure.
	got, err := c.Segments(context.Background(), "v1")
	if err != nil || len(got) != 1 {
		t.Fatalf("segments = %+v, err = %v", got, err)
	}
	if len(*reqs) != 1 {
		t.Errorf("requests = %d, want 1 (the cancelled one never reached the server)", len(*reqs))
	}
}

func TestSegmentsUnreachableServiceErrors(t *testing.T) {
	c := New(Options{BaseURL: "http://127.0.0.1:1", Timeout: 200 * time.Millisecond})
	if _, err := c.Segments(context.Background(), "v1"); err == nil {
		t.Error("want an error for an unreachable service")
	}
}

func TestSegmentsSendsAUserAgent(t *testing.T) {
	srv, reqs := serve(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`[]`)) })
	c := New(Options{BaseURL: srv.URL, UserAgent: "flimm/1.2.3"})
	if _, err := c.Segments(context.Background(), "v1"); err != nil {
		t.Fatalf("Segments: %v", err)
	}
	if got := (*reqs)[0].Header.Get("User-Agent"); got != "flimm/1.2.3" {
		t.Errorf("User-Agent = %q", got)
	}
}

func TestNewDefaultsToThePublicService(t *testing.T) {
	if c := New(Options{}); c.base != DefaultBaseURL {
		t.Errorf("base = %q, want %q", c.base, DefaultBaseURL)
	}
	if c := New(Options{BaseURL: "https://sb.example.com/"}); c.base != "https://sb.example.com" {
		t.Errorf("base = %q, trailing slash kept", c.base)
	}
}

func TestSegmentsEmptyVideoID(t *testing.T) {
	got, err := New(Options{BaseURL: "http://127.0.0.1:1"}).Segments(context.Background(), "")
	if err != nil || got != nil {
		t.Errorf("segments = %+v, err = %v", got, err)
	}
}

func contains(haystack, needle string) bool {
	unescaped, err := url.QueryUnescape(haystack)
	if err != nil {
		unescaped = haystack
	}
	return len(needle) > 0 && (indexOf(haystack, needle) >= 0 || indexOf(unescaped, needle) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
