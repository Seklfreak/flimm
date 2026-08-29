package api

import (
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Seklfreak/flimm/internal/db/sqlc"
	"github.com/Seklfreak/flimm/internal/ta"
)

// A video opened by accident must leave no trace: no history entry and no
// resume position.
func TestProgressBelowMinPlayTimeIsNotRecorded(t *testing.T) {
	client := ta.NewFake()
	client.Videos["v1"] = &ta.Video{YoutubeID: "v1", Title: "Video v1", Player: ta.Player{Duration: 1000}}
	es := newEventStore()
	h := newTestServer(client, es.querier()).Router()

	rec := do(t, h, http.MethodPost, "/api/v1/videos/v1/progress", `{"position":9}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if _, ok := es.events["v1"]; ok {
		t.Error("a 9s play created a watch event; it should leave no trace")
	}
}

// Once past the threshold the video is recorded as normal.
func TestProgressAboveMinPlayTimeIsRecorded(t *testing.T) {
	client := ta.NewFake()
	client.Videos["v1"] = &ta.Video{YoutubeID: "v1", Title: "Video v1", Player: ta.Player{Duration: 1000}}
	es := newEventStore()
	h := newTestServer(client, es.querier()).Router()

	if rec := do(t, h, http.MethodPost, "/api/v1/videos/v1/progress", `{"position":20}`); rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if es.events["v1"].Position != 20 {
		t.Errorf("position = %v, want 20", es.events["v1"].Position)
	}
}

// Resuming an already-recorded video must keep updating even when the reported
// position is below the threshold — the threshold only gates creation.
func TestProgressBelowMinPlayTimeStillUpdatesExistingEvent(t *testing.T) {
	client := ta.NewFake()
	client.Videos["v1"] = &ta.Video{YoutubeID: "v1", Title: "Video v1", Player: ta.Player{Duration: 1000}}
	es := newEventStore()
	h := newTestServer(client, es.querier()).Router()

	if rec := do(t, h, http.MethodPost, "/api/v1/videos/v1/progress", `{"position":300}`); rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	// The viewer scrubs back to the start; the entry must follow, not vanish.
	if rec := do(t, h, http.MethodPost, "/api/v1/videos/v1/progress", `{"position":4}`); rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	if es.events["v1"].Position != 4 {
		t.Errorf("position = %v, want 4", es.events["v1"].Position)
	}
}

// A video shorter than the threshold must still be recordable, or it could
// never reach history at all.
func TestShortVideoCompletionIsRecordedBelowThreshold(t *testing.T) {
	client := ta.NewFake()
	client.Videos["s1"] = &ta.Video{YoutubeID: "s1", Title: "Short", Player: ta.Player{Duration: 10}}
	es := newEventStore()
	h := newTestServer(client, es.querier()).Router()

	if rec := do(t, h, http.MethodPost, "/api/v1/videos/s1/progress", `{"position":10}`); rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if !es.events["s1"].CompletedAt.Valid {
		t.Error("finishing a 10s video left no completed event")
	}
}

// Coming back to a video drops the viewer a little before where they stopped,
// because landing mid-sentence costs more than the seconds do. The server owns
// it so that every client resumes the same way and none has to implement it.
func TestResumeStartsShortOfWhereItStopped(t *testing.T) {
	client := ta.NewFake()
	client.AddVideo(video("v1", "A", "2026-08-01", 600, false))
	store := newEventStore()
	store.events["v1"] = sqlc.WatchEvent{VideoID: "v1", Position: 300, Duration: 600}
	h := newTestServer(client, store.querier()).Router()

	got := decode[VideoDetail](t, do(t, h, http.MethodGet, "/api/v1/videos/v1", ""))
	if got.Position != 285 {
		t.Errorf("position = %v, want 285 (300 less the rewind)", got.Position)
	}
	// The bar under the card still says how far the viewer really got.
	if got.Progress != 0.5 {
		t.Errorf("progress = %v, want 0.5 — the rewind must not move it", got.Progress)
	}
}

// The first seconds of a video have nothing to give back.
func TestResumeNeverGoesNegative(t *testing.T) {
	client := ta.NewFake()
	client.AddVideo(video("v1", "A", "2026-08-01", 600, false))
	store := newEventStore()
	store.events["v1"] = sqlc.WatchEvent{VideoID: "v1", Position: 9, Duration: 600}
	h := newTestServer(client, store.querier()).Router()

	if got := decode[VideoDetail](t, do(t, h, http.MethodGet, "/api/v1/videos/v1", "")); got.Position != 0 {
		t.Errorf("position = %v, want 0", got.Position)
	}
}

// A watched video is started over rather than resumed, so its recorded
// position is reported as it stands.
func TestAWatchedVideoKeepsItsPosition(t *testing.T) {
	client := ta.NewFake()
	client.AddVideo(video("v1", "A", "2026-08-01", 600, false))
	store := newEventStore()
	store.events["v1"] = sqlc.WatchEvent{
		VideoID:     "v1",
		Position:    590,
		Duration:    600,
		CompletedAt: pgtype.Timestamptz{Valid: true},
	}
	h := newTestServer(client, store.querier()).Router()

	got := decode[VideoDetail](t, do(t, h, http.MethodGet, "/api/v1/videos/v1", ""))
	if !got.Watched || got.Position != 590 {
		t.Errorf("watched = %v, position = %v", got.Watched, got.Position)
	}
}
