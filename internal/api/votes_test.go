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
	_, h := votesServer(t, body, status)
	return h
}

// votesServer is videoWithVotes with the server, for warming the cache.
func votesServer(t *testing.T, body string, status int) (*Server, http.Handler) {
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

	s := NewServer(Options{
		Querier:     newEventStore().querier(),
		TA:          client,
		Log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		AppName:     "Flimm",
		MediaSecret: testSecret,
		RYD:         ryd.New(ryd.Options{BaseURL: srv.URL}),
	})
	return s, s.Router()
}

// warmVotes fetches once, the way the background worker does. The first view
// of a video never waits for the service, so every test that asserts live
// counts is asserting the state after it has answered.
func warmVotes(t *testing.T, s *Server) {
	t.Helper()
	s.fetchVotes(t.Context(), "v1")
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
	s, h := votesServer(t, `{"id":"v1","likes":45120,"dislikes":1183,"viewCount":1200000}`, http.StatusOK)
	warmVotes(t, s)
	stats := detailStats(t, h)
	if stats.Dislikes == nil || *stats.Dislikes != 1183 {
		t.Fatalf("dislikes = %v, want 1183", stats.Dislikes)
	}
	if stats.Likes != 45120 {
		t.Errorf("likes = %d, want the service's own, not the archive's 40", stats.Likes)
	}
	// A view count only goes up, so the larger of the two is simply the more
	// recently read.
	if stats.Views != 1_200_000 {
		t.Errorf("views = %d, want the service's newer count", stats.Views)
	}
}

// The first view of a video does not wait for the service. The archive's own
// counts go out and the lookup is queued, so the wait falls on nobody.
func TestTheFirstViewDoesNotWaitForTheVoteService(t *testing.T) {
	s, h := votesServer(t, `{"id":"v1","likes":45120,"dislikes":1183}`, http.StatusOK)

	stats := detailStats(t, h)
	if stats.Likes != 40 {
		t.Errorf("likes = %d, want the archive's own on a cold cache", stats.Likes)
	}
	if len(s.cacheJobs) != 1 {
		t.Errorf("queued %d lookups, want the one this view could not answer", len(s.cacheJobs))
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

// The other direction of the same rule: a service record older than the
// download keeps the archive's larger count.
func TestTheLargerViewCountWins(t *testing.T) {
	h := videoWithVotes(t, `{"id":"v1","likes":40,"dislikes":3,"viewCount":12}`, http.StatusOK)
	if views := detailStats(t, h).Views; views != 900 {
		t.Errorf("views = %d, want the archive's 900", views)
	}
}

// A record with no likes beside an archive that counted plenty is the service
// missing data, not the video losing its likes.
func TestAnEmptyLikeCountDoesNotOverwriteTheArchives(t *testing.T) {
	s, h := votesServer(t, `{"id":"v1","likes":0,"dislikes":7}`, http.StatusOK)
	warmVotes(t, s)
	stats := detailStats(t, h)
	if stats.Likes != 40 {
		t.Errorf("likes = %d, want the archive's 40", stats.Likes)
	}
	if stats.Dislikes == nil || *stats.Dislikes != 7 {
		t.Errorf("dislikes = %v, want 7", stats.Dislikes)
	}
}

// Most archives already hold a dislike count: TubeArchivist asks the same
// service at index time. Flimm shows it without asking anyone.
func TestTheArchivesOwnDislikeCountIsUsed(t *testing.T) {
	client := ta.NewFake()
	v := video("v1", "A", "2026-08-01", 1000, false)
	v.Stats = ta.Stats{ViewCount: 900, LikeCount: 40, DislikeCount: 6}
	client.AddVideo(v)
	h := newTestServer(client, newEventStore().querier()).Router()

	stats := detailStats(t, h)
	if stats.Dislikes == nil || *stats.Dislikes != 6 {
		t.Errorf("dislikes = %v, want the archive's 6 with no service configured", stats.Dislikes)
	}
}

// A live answer is newer than the one indexed at download time, so it wins.
func TestTheServiceOverridesTheArchivedDislikeCount(t *testing.T) {
	client := ta.NewFake()
	v := video("v1", "A", "2026-08-01", 1000, false)
	v.Stats = ta.Stats{ViewCount: 900, LikeCount: 40, DislikeCount: 6}
	client.AddVideo(v)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id":"v1","likes":45120,"dislikes":1183,"viewCount":1200000}`))
	}))
	t.Cleanup(srv.Close)
	s := NewServer(Options{
		Querier:     newEventStore().querier(),
		TA:          client,
		Log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		AppName:     "Flimm",
		MediaSecret: testSecret,
		RYD:         ryd.New(ryd.Options{BaseURL: srv.URL}),
	})
	warmVotes(t, s)

	if got := detailStats(t, s.Router()).Dislikes; got == nil || *got != 1183 {
		t.Errorf("dislikes = %v, want the service's 1183", got)
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
	// This video's archive record has no dislike count either, so there is
	// nothing to report and the key stays off.
	if _, ok := raw.Stats["dislikes"]; ok {
		t.Error("stats carries a dislikes key when nothing knows one")
	}
}
