package api

import (
	"net/http"
	"testing"

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
