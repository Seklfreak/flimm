package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Seklfreak/flimm/internal/ryd"
	"github.com/Seklfreak/flimm/internal/ta"
)

// videoWithVotes serves one video whose archived counts are 900 views and
// 40 likes, and a Return YouTube Dislike stub answering `body`.
func videoWithVotes(t *testing.T, body string, status int) http.Handler {
	t.Helper()
	client := ta.NewFake()
	v := video("v1", "A", "2026-08-01", 1000, false)
	v.Stats = ta.Stats{ViewCount: 900, LikeCount: 40}
	client.AddVideo(v)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	return NewServer(Options{
		Querier:     newEventStore().querier(),
		TA:          client,
		Log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		AppName:     "Flimm",
		MediaSecret: testSecret,
		RYD:         ryd.New(ryd.Options{BaseURL: srv.URL}),
	}).Router()
}

func detailStats(t *testing.T, h http.Handler) VideoStats {
	t.Helper()
	rec := do(t, h, http.MethodGet, "/api/v1/videos/v1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	return decode[VideoDetail](t, rec).Stats
}

// The whole point of the integration: the half of the vote YouTube stopped
// publishing, and with it the like count it was measured against.
func TestDislikesComeFromTheServiceAsAPair(t *testing.T) {
	h := videoWithVotes(t, `{"id":"v1","likes":45120,"dislikes":1183}`, http.StatusOK)
	stats := detailStats(t, h)
	if stats.Dislikes == nil || *stats.Dislikes != 1183 {
		t.Fatalf("dislikes = %v, want 1183", stats.Dislikes)
	}
	if stats.Likes != 45120 {
		t.Errorf("likes = %d, want the service's own, not the archive's 40", stats.Likes)
	}
	// Views are the archive's throughout; the service is not asked about them.
	if stats.Views != 900 {
		t.Errorf("views = %d, want the archive's", stats.Views)
	}
}

// "Unknown" and "zero" are different answers, and a client can only draw the
// control for one of them.
func TestAVideoTheServiceHasNeverSeenCarriesNoDislikeCount(t *testing.T) {
	h := videoWithVotes(t, `{"error":"not found"}`, http.StatusNotFound)
	stats := detailStats(t, h)
	if stats.Dislikes != nil {
		t.Errorf("dislikes = %v, want absent", *stats.Dislikes)
	}
	if stats.Likes != 40 {
		t.Errorf("likes = %d, want the archive's own kept", stats.Likes)
	}
}

// An outage must leave the video looking exactly as it did before the
// integration was switched on.
func TestAnOutageFallsBackToTheArchive(t *testing.T) {
	h := videoWithVotes(t, "", http.StatusInternalServerError)
	stats := detailStats(t, h)
	if stats.Dislikes != nil || stats.Likes != 40 {
		t.Errorf("stats = %+v, want the archive's counts and no dislikes", stats)
	}
}

// A record with no likes beside an archive that counted plenty is the service
// missing data, not the video losing its likes.
func TestAnEmptyLikeCountDoesNotOverwriteTheArchives(t *testing.T) {
	h := videoWithVotes(t, `{"id":"v1","likes":0,"dislikes":7}`, http.StatusOK)
	stats := detailStats(t, h)
	if stats.Likes != 40 {
		t.Errorf("likes = %d, want the archive's 40", stats.Likes)
	}
	if stats.Dislikes == nil || *stats.Dislikes != 7 {
		t.Errorf("dislikes = %v, want 7", stats.Dislikes)
	}
}

// Nothing is asked of anyone when RYD_URL is unset, which is the default.
func TestWithoutTheServiceNothingIsAskedAndNothingIsSent(t *testing.T) {
	client := ta.NewFake()
	v := video("v1", "A", "2026-08-01", 1000, false)
	v.Stats = ta.Stats{ViewCount: 900, LikeCount: 40}
	client.AddVideo(v)
	h := newTestServer(client, newEventStore().querier()).Router()

	rec := do(t, h, http.MethodGet, "/api/v1/videos/v1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var raw struct {
		Stats map[string]json.RawMessage `json:"stats"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw.Stats["dislikes"]; ok {
		t.Error("stats carries a dislikes key with no service configured")
	}
}
