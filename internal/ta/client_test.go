package ta

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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
