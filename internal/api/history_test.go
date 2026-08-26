package api

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Seklfreak/flimm/internal/db/sqlc"
	"github.com/Seklfreak/flimm/internal/ta"
)

func TestHistoryListAndDelete(t *testing.T) {
	client := ta.NewFake()
	client.AddVideo(video("v1", "A", "2026-08-01", 1000, false))
	now := time.Now()
	e1 := sqlc.WatchEvent{ID: uuid.New(), VideoID: "v1", Title: "Video v1", ChannelID: "A", Position: 100, Duration: 1000,
		LastPlayedAt: pgtype.Timestamptz{Time: now, Valid: true}}
	gone := sqlc.WatchEvent{ID: uuid.New(), VideoID: "deleted", Title: "Gone video", ChannelName: "Old Channel", Duration: 50,
		LastPlayedAt: pgtype.Timestamptz{Time: now.Add(-time.Hour), Valid: true}, CompletedAt: pgtype.Timestamptz{Time: now, Valid: true}}

	var gotParams sqlc.ListHistoryParams
	hidden := map[uuid.UUID]bool{}
	q := newEventStore().querier()
	q.ListHistoryFn = func(_ context.Context, arg sqlc.ListHistoryParams) ([]sqlc.WatchEvent, error) {
		gotParams = arg
		return []sqlc.WatchEvent{e1, gone}, nil
	}
	q.CountHistoryFn = func(context.Context, sqlc.CountHistoryParams) (int64, error) { return 2, nil }
	q.HideHistoryEntryFn = func(_ context.Context, arg sqlc.HideHistoryEntryParams) (int64, error) {
		if arg.ID != e1.ID {
			return 0, nil
		}
		hidden[arg.ID] = true
		return 1, nil
	}
	h := newTestServer(client, q).Router()

	rec := do(t, h, http.MethodGet, "/api/v1/history?filter=in_progress&q=vid&page=1&page_size=10", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if gotParams.Filter != "in_progress" || gotParams.Q != "vid" || gotParams.PageLimit != 10 || gotParams.PageOffset != 10 {
		t.Errorf("params = %+v", gotParams)
	}
	page := decode[Page[HistoryEntry]](t, rec)
	if page.Total != 2 || len(page.Items) != 2 {
		t.Fatalf("page = %+v", page)
	}
	if page.Items[0].State != "in_progress" || page.Items[0].Video.Progress != 0.1 || page.Items[0].Video.Title != "Video v1" {
		t.Errorf("entry 0 = %+v", page.Items[0])
	}
	// Video deleted from TA: snapshot fallback keeps the entry.
	if page.Items[1].State != "seen" || page.Items[1].Video.Title != "Gone video" || page.Items[1].Video.Channel.Name != "Old Channel" || !page.Items[1].Video.Watched {
		t.Errorf("entry 1 = %+v", page.Items[1])
	}

	if rec := do(t, h, http.MethodGet, "/api/v1/history?filter=bogus", ""); rec.Code != http.StatusBadRequest {
		t.Errorf("bad filter: %d", rec.Code)
	}
	if rec := do(t, h, http.MethodDelete, "/api/v1/history/"+e1.ID.String(), ""); rec.Code != http.StatusNoContent || !hidden[e1.ID] {
		t.Errorf("delete: %d", rec.Code)
	}
	if rec := do(t, h, http.MethodDelete, "/api/v1/history/"+uuid.NewString(), ""); rec.Code != http.StatusNotFound {
		t.Errorf("delete unknown: %d", rec.Code)
	}
}
