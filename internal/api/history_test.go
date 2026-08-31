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

// A history entry names the feed its video most specifically belongs to: a
// playlist-source (series) match beats a channel match even when the channel
// feed sits higher in the sidebar, and no match means null.
func TestHistoryEntriesCarryTheirHomeFeed(t *testing.T) {
	client := ta.NewFake()
	client.AddVideo(video("a1", "A", "2026-08-01", 600, false))
	series := video("p1", "B", "2026-08-02", 600, false)
	series.Playlist = []string{"PL"}
	client.AddVideo(series)
	client.AddVideo(video("x1", "X", "2026-08-03", 600, false))

	channelFeed := sqlc.Feed{ID: uuid.New(), Name: "Making", Position: 0}
	seriesFeed := sqlc.Feed{ID: uuid.New(), Name: "Night sides", Position: 1}
	now := pgtype.Timestamptz{Time: time.Now(), Valid: true}
	events := []sqlc.WatchEvent{
		{ID: uuid.New(), VideoID: "a1", ChannelID: "A", Position: 10, Duration: 600, LastPlayedAt: now},
		{ID: uuid.New(), VideoID: "p1", ChannelID: "B", Position: 10, Duration: 600, LastPlayedAt: now},
		{ID: uuid.New(), VideoID: "x1", ChannelID: "X", Position: 10, Duration: 600, LastPlayedAt: now},
	}

	q := newEventStore().querier()
	q.ListHistoryFn = func(context.Context, sqlc.ListHistoryParams) ([]sqlc.WatchEvent, error) { return events, nil }
	q.CountHistoryFn = func(context.Context, sqlc.CountHistoryParams) (int64, error) { return 3, nil }
	q.ListFeedsFn = func(context.Context, uuid.UUID) ([]sqlc.Feed, error) {
		return []sqlc.Feed{channelFeed, seriesFeed}, nil
	}
	q.ListFeedChannelsForUserFn = func(context.Context, uuid.UUID) ([]sqlc.ListFeedChannelsForUserRow, error) {
		// Channel B is in Making too — the series feed must still win for p1.
		return []sqlc.ListFeedChannelsForUserRow{
			{FeedID: channelFeed.ID, ChannelID: "A", FeedName: "Making"},
			{FeedID: channelFeed.ID, ChannelID: "B", FeedName: "Making"},
		}, nil
	}
	q.ListFeedPlaylistsForUserFn = func(context.Context, uuid.UUID) ([]sqlc.ListFeedPlaylistsForUserRow, error) {
		return []sqlc.ListFeedPlaylistsForUserRow{{FeedID: seriesFeed.ID, PlaylistID: "PL", FeedName: "Night sides"}}, nil
	}
	h := newTestServer(client, q).Router()

	page := decode[Page[HistoryEntry]](t, do(t, h, http.MethodGet, "/api/v1/history", ""))
	if len(page.Items) != 3 {
		t.Fatalf("items = %d, want 3", len(page.Items))
	}
	byVideo := map[string]*FeedRef{}
	for _, it := range page.Items {
		byVideo[it.Video.ID] = it.Feed
	}
	if f := byVideo["a1"]; f == nil || f.Name != "Making" {
		t.Errorf("a1 feed = %+v, want Making (channel source)", f)
	}
	if f := byVideo["p1"]; f == nil || f.Name != "Night sides" {
		t.Errorf("p1 feed = %+v, want Night sides (series beats the channel feed above it)", f)
	}
	if f := byVideo["x1"]; f != nil {
		t.Errorf("x1 feed = %+v, want none", f)
	}
}
