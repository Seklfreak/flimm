package api

import (
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Seklfreak/flimm/internal/db/sqlc"
	"github.com/Seklfreak/flimm/internal/ta"
)

// watchedEvent is a completed watch event, as the heartbeat would leave one.
func watchedEvent(videoID string) sqlc.WatchEvent {
	now := pgtype.Timestamptz{Time: time.Now(), Valid: true}
	return sqlc.WatchEvent{
		ID: uuid.New(), VideoID: videoID, Duration: 200, Position: 200,
		FirstPlayedAt: now, LastPlayedAt: now, CompletedAt: now,
	}
}

func musicFixture(t *testing.T) (http.Handler, *eventStore) {
	t.Helper()
	client := ta.NewFake()
	entries := make([]ta.PlaylistEntry, 0, 2)
	for i, id := range []string{"s1", "s2"} {
		client.Videos[id] = &ta.Video{YoutubeID: id, Title: "Song " + id, Player: ta.Player{Duration: 200}, Playlist: []string{"PLM"}}
		entries = append(entries, ta.PlaylistEntry{YoutubeID: id, Idx: i, Downloaded: true})
	}
	client.Playlists["PLM"] = &ta.Playlist{PlaylistID: "PLM", PlaylistName: "Music", PlaylistType: "custom", PlaylistEntries: entries}
	es := newEventStore()
	return newTestServer(client, es.querier()).Router(), es
}

// Playing from a music playlist must leave no watch state: songs are replayed,
// so history and continue-watching would fill with tracks.
func TestProgressInMusicPlaylistRecordsNothing(t *testing.T) {
	h, es := musicFixture(t)
	if rec := do(t, h, http.MethodPut, "/api/v1/playlists/PLM/music", `{"music":true}`); rec.Code != http.StatusNoContent {
		t.Fatalf("set music: %d %s", rec.Code, rec.Body.String())
	}
	// Well past both the minimum play time and the watched threshold.
	if rec := do(t, h, http.MethodPost, "/api/v1/videos/s1/progress?playlist=PLM", `{"position":190}`); rec.Code != http.StatusOK {
		t.Fatalf("progress: %d %s", rec.Code, rec.Body.String())
	}
	if _, ok := es.events["s1"]; ok {
		t.Error("playing a song recorded a watch event")
	}
}

// The same video played outside that playlist is ordinary viewing again.
func TestProgressOutsideMusicPlaylistStillRecords(t *testing.T) {
	h, es := musicFixture(t)
	if rec := do(t, h, http.MethodPut, "/api/v1/playlists/PLM/music", `{"music":true}`); rec.Code != http.StatusNoContent {
		t.Fatal(rec.Body.String())
	}
	if rec := do(t, h, http.MethodPost, "/api/v1/videos/s1/progress", `{"position":100}`); rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	if es.events["s1"].Position != 100 {
		t.Errorf("position = %v, want 100", es.events["s1"].Position)
	}
}

// A playlist that is not music keeps recording as before.
func TestProgressInOrdinaryPlaylistRecords(t *testing.T) {
	h, es := musicFixture(t)
	if rec := do(t, h, http.MethodPost, "/api/v1/videos/s1/progress?playlist=PLM", `{"position":100}`); rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	if es.events["s1"].Position != 100 {
		t.Errorf("position = %v, want 100", es.events["s1"].Position)
	}
}

// A music playlist reports no watch-derived fields, so clients need no
// special-casing to hide seen counts and resume points.
func TestMusicPlaylistReportsNoWatchState(t *testing.T) {
	h, es := musicFixture(t)
	es.events["s1"] = watchedEvent("s1")

	before := decode[PlaylistDetail](t, do(t, h, http.MethodGet, "/api/v1/playlists/PLM", ""))
	if before.SeenCount != 1 {
		t.Fatalf("precondition: seen_count = %d, want 1 before marking as music", before.SeenCount)
	}

	if rec := do(t, h, http.MethodPut, "/api/v1/playlists/PLM/music", `{"music":true}`); rec.Code != http.StatusNoContent {
		t.Fatal(rec.Body.String())
	}
	got := decode[PlaylistDetail](t, do(t, h, http.MethodGet, "/api/v1/playlists/PLM", ""))
	if !got.Music {
		t.Error("playlist does not report music")
	}
	if got.SeenCount != 0 || got.InProgressCount != 0 || got.Progress != 0 || got.ResumeVideoID != nil {
		t.Errorf("watch state leaked: seen=%d inprogress=%d progress=%v resume=%v",
			got.SeenCount, got.InProgressCount, got.Progress, got.ResumeVideoID)
	}
	for _, it := range got.Items {
		if it.Video.Watched || it.Video.Position != 0 || it.Video.LastPlayedAt != nil {
			t.Errorf("item %s still carries watch state: %+v", it.Video.ID, it.Video)
		}
	}
}
