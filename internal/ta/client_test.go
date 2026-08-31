package ta

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientDecodesEnvelopeAndDirectDocs(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Token tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/video/wrapped/":
			_, _ = w.Write([]byte(`{"data":{"youtube_id":"wrapped","title":"W"}}`))
		case "/api/video/direct/":
			_, _ = w.Write([]byte(`{"youtube_id":"direct","title":"D","vid_type":"shorts"}`))
		case "/api/video/":
			if r.URL.Query().Get("channel") != "UC1" || r.URL.Query().Get("watch") != "unwatched" || r.URL.Query().Get("page_size") != "1" {
				t.Errorf("unexpected query %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"data":[{"youtube_id":"a"}],"paginate":{"total_hits":7,"last_page":7}}`))
		case "/api/search/":
			_, _ = w.Write([]byte(`{"results":{"video_results":[{"youtube_id":"a"}],"fulltext_results":[{"youtube_id":"b","subtitle_line":"hi","subtitle_start":1.5}]},"queryType":"video"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := New(srv.URL, "tok")
	ctx := context.Background()

	v, err := c.GetVideo(ctx, "wrapped")
	if err != nil || v.Title != "W" {
		t.Fatalf("wrapped: %+v %v", v, err)
	}
	v, err = c.GetVideo(ctx, "direct")
	if err != nil || v.Title != "D" || v.Kind() != "short" {
		t.Fatalf("direct: %+v %v", v, err)
	}
	if _, err := c.GetVideo(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing: %v", err)
	}

	n, err := c.UnseenCount(ctx, "UC1")
	if err != nil || n != 7 {
		t.Fatalf("unseen = %d %v", n, err)
	}
	before := calls
	if _, err := c.UnseenCount(ctx, "UC1"); err != nil || calls != before {
		t.Errorf("unseen count not cached (calls %d → %d)", before, calls)
	}
	if err := c.SetWatched(ctx, "a", true); err == nil {
		t.Error("expected 404 from fake TA on /api/watched/")
	}
	if _, err := c.UnseenCount(ctx, "UC1"); err != nil || calls == before+1 {
		t.Errorf("cache should be invalidated after a watched write")
	}

	res, err := c.Search(ctx, "video:x")
	if err != nil || len(res.Videos) != 1 || len(res.Fulltext) != 1 || res.Fulltext[0].SubtitleStart != 1.5 {
		t.Fatalf("search = %+v %v", res, err)
	}
	if res.Channels == nil || res.Playlists == nil {
		t.Error("empty buckets should be non-nil")
	}
}

func TestClientUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()
	c := New(srv.URL, "tok")
	if err := c.Ping(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Errorf("5xx: %v", err)
	}
	c = New("http://127.0.0.1:1", "tok")
	if err := c.Ping(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Errorf("refused: %v", err)
	}
}

func TestVideoHelpers(t *testing.T) {
	var v Video
	if err := json.Unmarshal([]byte(`{"published":"2026-08-23","date_downloaded":1755921120,"streams":[{"type":"audio"},{"type":"video","height":1080}]}`), &v); err != nil {
		t.Fatal(err)
	}
	if v.PublishedTime().Format("2006-01-02") != "2026-08-23" || v.DownloadedTime().IsZero() || v.Height() != 1080 {
		t.Errorf("helpers: %v %v %d", v.PublishedTime(), v.DownloadedTime(), v.Height())
	}
}

func TestFetchRange(t *testing.T) {
	body := []byte("0123456789abcdef")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Token tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/media/a/v.mp4":
			if got := r.Header.Get("Range"); got != "bytes=0-7" {
				t.Errorf("Range = %q", got)
			}
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(body[:8])
		case "/media/a/ignores-range.mp4":
			// A server that ignores Range answers 200 with the whole file.
			_, _ = w.Write(body)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := New(srv.URL, "tok")
	ctx := context.Background()

	got, err := c.FetchRange(ctx, "/media/a/v.mp4", 0, 7)
	if err != nil || string(got) != "01234567" {
		t.Fatalf("FetchRange = %q %v", got, err)
	}

	// A 200 is accepted, but never more than the range asked for.
	got, err = c.FetchRange(ctx, "/media/a/ignores-range.mp4", 0, 3)
	if err != nil || string(got) != "0123" {
		t.Fatalf("unranged response = %q %v", got, err)
	}

	if _, err := c.FetchRange(ctx, "/media/a/missing.mp4", 0, 7); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing: %v", err)
	}
	if _, err := c.FetchRange(ctx, "/media/a/v.mp4", 8, 2); err == nil {
		t.Error("expected an error for an inverted range")
	}
	if _, err := c.FetchRange(ctx, "/media/a/v.mp4", -1, 7); err == nil {
		t.Error("expected an error for a negative start")
	}
}

// The count comes from one aggregate rather than from walking every page of
// channel documents — which was nineteen requests against a real archive, to
// produce one integer, on a route the app loads with every screen.
func TestChannelCountReadsTheAggregate(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		_, _ = w.Write([]byte(`{"doc_count":218,"subscribed_true":67,"subscribed_false":151}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "tok")
	n, err := c.ChannelCount(context.Background())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if n != 218 {
		t.Errorf("count = %d, want 218", n)
	}
	if len(paths) != 1 || paths[0] != "/api/stats/channel/" {
		t.Errorf("requested %v, want one call to the aggregate", paths)
	}

	// Cached like the other counts: a sidebar that loads on every screen must
	// not ask again each time.
	if _, err := c.ChannelCount(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 {
		t.Errorf("made %d requests, want the second answered from the cache", len(paths))
	}
}

// Real TA indexes subtitle_start/subtitle_end as the VTT timestamp strings
// its indexer formats ("HH:MM:SS.mmm"); the fulltext bucket must decode them
// (and tolerate plain numbers).
func TestSearchDecodesSubtitleTimestampStrings(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"results":{"fulltext_results":[
			{"youtube_id":"v1","subtitle_line":"a","subtitle_start":"00:01:05.500","subtitle_end":"00:01:20.000"},
			{"youtube_id":"v2","subtitle_line":"b","subtitle_start":42.5,"subtitle_end":50}
		]}}`))
	}))
	defer srv.Close()

	res, err := New(srv.URL, "tok").Search(context.Background(), "full:test")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(res.Fulltext) != 2 {
		t.Fatalf("hits = %d, want 2", len(res.Fulltext))
	}
	if got := res.Fulltext[0].SubtitleStart; got != 65.5 {
		t.Errorf("string start = %v, want 65.5", got)
	}
	if got := res.Fulltext[0].SubtitleEnd; got != 80 {
		t.Errorf("string end = %v, want 80", got)
	}
	if got := res.Fulltext[1].SubtitleStart; got != 42.5 {
		t.Errorf("numeric start = %v, want 42.5", got)
	}
}

// The fake archive must speak the same wire format as real TA, so marshaling
// a hit produces the timestamp string that unmarshaling reads back.
func TestSubtitleHitRoundTripsVTTStamps(t *testing.T) {
	in := SubtitleHit{YoutubeID: "v", SubtitleStart: 3725.25, SubtitleEnd: 3730}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if want := `"subtitle_start":"01:02:05.250"`; !strings.Contains(string(b), want) {
		t.Fatalf("marshal = %s, want it to carry %s", b, want)
	}
	var out SubtitleHit
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.SubtitleStart != in.SubtitleStart || out.SubtitleEnd != in.SubtitleEnd {
		t.Errorf("round trip = %+v, want %+v", out, in)
	}
}
