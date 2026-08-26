package api

import (
	"context"
	"net/http"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Seklfreak/flimm/internal/db/sqlc"
	"github.com/Seklfreak/flimm/internal/ta"
)

// feedFixture wires one feed (channels A + B) with a seeded TA.
func feedFixture(t *testing.T, feed sqlc.Feed, chans []string) (*ta.Fake, *eventStore, http.Handler) {
	t.Helper()
	client := ta.NewFake()
	client.AddVideo(video("a1", "A", "2026-08-01", 600, false))
	client.AddVideo(video("a2", "A", "2026-08-03", 100, true))
	client.AddVideo(video("b1", "B", "2026-08-02", 3000, false))
	short := video("b2", "B", "2026-08-04", 40, false)
	short.VidType = "shorts"
	client.AddVideo(short)
	client.AddVideo(video("c1", "C", "2026-08-05", 500, false))

	es := newEventStore()
	q := es.querier()
	q.GetFeedFn = func(_ context.Context, arg sqlc.GetFeedParams) (sqlc.Feed, error) {
		if arg.ID != feed.ID {
			return sqlc.Feed{}, errNoRows
		}
		return feed, nil
	}
	q.ListFeedChannelsFn = func(context.Context, uuid.UUID) ([]string, error) { return chans, nil }
	return client, es, newTestServer(client, q).Router()
}

func TestFeedVideosUnseenMergesAndHidesSeen(t *testing.T) {
	feed := sqlc.Feed{ID: uuid.New(), Name: "Home", Sort: "newest", HideSeen: true}
	client, es, h := feedFixture(t, feed, []string{"A", "B"})
	// b1 completed in Flimm but TA doesn't know (should still be hidden).
	es.events["b1"] = sqlc.WatchEvent{VideoID: "b1", CompletedAt: pgtype.Timestamptz{Valid: true}}
	_ = client

	rec := do(t, h, http.MethodGet, "/api/v1/feeds/"+feed.ID.String()+"/videos", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	page := decode[Page[VideoSummary]](t, rec)
	// a2 is TA-watched, b1 is Flimm-completed, b2 is a short (excluded), c1 not in feed.
	if got := ids(page.Items); !reflect.DeepEqual(got, []string{"a1"}) {
		t.Errorf("items = %v, want [a1]", got)
	}
	if page.Total != 1 || page.PageSize != 30 {
		t.Errorf("page = %+v", page)
	}
}

func TestFeedVideosAllViewSortsAndOverlaysProgress(t *testing.T) {
	feed := sqlc.Feed{ID: uuid.New(), Name: "Home", Sort: "longest", HideSeen: true, IncludeShorts: true}
	_, es, h := feedFixture(t, feed, []string{"A", "B"})
	es.events["a1"] = sqlc.WatchEvent{VideoID: "a1", Position: 300, Duration: 600, LastPlayedAt: pgtype.Timestamptz{Valid: true}}

	rec := do(t, h, http.MethodGet, "/api/v1/feeds/"+feed.ID.String()+"/videos?view=all&page_size=2", "")
	page := decode[Page[VideoSummary]](t, rec)
	if got := ids(page.Items); !reflect.DeepEqual(got, []string{"b1", "a1"}) {
		t.Errorf("items = %v, want [b1 a1] (longest first)", got)
	}
	if page.Total != 4 {
		t.Errorf("total = %d, want 4", page.Total)
	}
	a1 := page.Items[1]
	if a1.Position != 300 || a1.Progress != 0.5 || a1.Watched || a1.LastPlayedAt == nil {
		t.Errorf("a1 overlay = %+v", a1)
	}
	rec = do(t, h, http.MethodGet, "/api/v1/feeds/"+feed.ID.String()+"/videos?view=all&page_size=2&page=1", "")
	page = decode[Page[VideoSummary]](t, rec)
	if got := ids(page.Items); !reflect.DeepEqual(got, []string{"a2", "b2"}) {
		t.Errorf("page 1 = %v, want [a2 b2]", got)
	}
}

func TestFeedVideosContinueView(t *testing.T) {
	feed := sqlc.Feed{ID: uuid.New(), Name: "Home", Sort: "newest", HideSeen: true}
	_, es, h := feedFixture(t, feed, []string{"A", "B"})
	es.events["a1"] = sqlc.WatchEvent{VideoID: "a1", ChannelID: "A", Position: 10, Duration: 600}
	es.events["c1"] = sqlc.WatchEvent{VideoID: "c1", ChannelID: "C", Position: 10, Duration: 500}

	rec := do(t, h, http.MethodGet, "/api/v1/feeds/"+feed.ID.String()+"/videos?view=continue", "")
	page := decode[Page[VideoSummary]](t, rec)
	if got := ids(page.Items); !reflect.DeepEqual(got, []string{"a1"}) {
		t.Errorf("continue = %v, want [a1] (c1 is outside the feed)", got)
	}
}

func TestEverythingFeedUsesPrefsAndNoChannelFilter(t *testing.T) {
	feed := sqlc.Feed{ID: uuid.New(), Name: "Home", Sort: "newest", HideSeen: true}
	_, _, h := feedFixture(t, feed, []string{"A"})
	rec := do(t, h, http.MethodGet, "/api/v1/feeds/everything/videos", "")
	page := decode[Page[VideoSummary]](t, rec)
	// default prefs: newest, hide seen, no shorts → c1, b1, a1
	if got := ids(page.Items); !reflect.DeepEqual(got, []string{"c1", "b1", "a1"}) {
		t.Errorf("everything = %v", got)
	}
}

func TestListFeedsIncludesEverythingLast(t *testing.T) {
	feed := sqlc.Feed{ID: uuid.New(), Name: "Home", Sort: "newest", HideSeen: true, Pinned: true}
	client, _, h := feedFixture(t, feed, []string{"A", "B"})
	_ = client
	srv := newTestServer(client, func() sqlc.Querier {
		es := newEventStore()
		q := es.querier()
		q.ListFeedsFn = func(context.Context, uuid.UUID) ([]sqlc.Feed, error) { return []sqlc.Feed{feed}, nil }
		q.ListFeedChannelsForUserFn = func(context.Context, uuid.UUID) ([]sqlc.ListFeedChannelsForUserRow, error) {
			return []sqlc.ListFeedChannelsForUserRow{{FeedID: feed.ID, ChannelID: "A", FeedName: "Home"}, {FeedID: feed.ID, ChannelID: "B", FeedName: "Home"}}, nil
		}
		return q
	}())
	_ = h
	rec := do(t, srv.Router(), http.MethodGet, "/api/v1/feeds", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	feeds := decode[[]FeedDTO](t, rec)
	if len(feeds) != 2 || feeds[0].Name != "Home" || feeds[1].ID != "everything" {
		t.Fatalf("feeds = %+v", feeds)
	}
	// unseen: a1 + b1 + b2 (TA counts shorts too) = 3; everything = 4 (c1 too)
	if feeds[0].UnseenCount != 3 || feeds[0].ChannelCount != 2 || !feeds[0].Pinned {
		t.Errorf("home = %+v", feeds[0])
	}
	if feeds[1].UnseenCount != 4 || feeds[1].Position != 1 {
		t.Errorf("everything = %+v", feeds[1])
	}
}

func TestMarkFeedSeen(t *testing.T) {
	feed := sqlc.Feed{ID: uuid.New(), Name: "Home", Sort: "newest", HideSeen: true}
	client, es, h := feedFixture(t, feed, []string{"A", "B"})
	rec := do(t, h, http.MethodPost, "/api/v1/feeds/"+feed.ID.String()+"/mark-seen", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	for _, id := range []string{"a1", "b1"} {
		if !client.Videos[id].Player.Watched {
			t.Errorf("%s not watched in TA", id)
		}
		if !es.events[id].CompletedAt.Valid {
			t.Errorf("%s not completed in Flimm", id)
		}
	}
	if client.Videos["b2"].Player.Watched {
		t.Error("short b2 marked watched although feed excludes shorts")
	}
}

func TestCreateFeedPinsAndStoresChannels(t *testing.T) {
	client := ta.NewFake()
	client.AddVideo(video("a1", "A", "2026-08-01", 600, false))
	es := newEventStore()
	q := es.querier()
	var created sqlc.CreateFeedParams
	var added []string
	unpinned := false
	q.NextFeedPositionFn = func(context.Context, uuid.UUID) (int32, error) { return 2, nil }
	q.UnpinFeedsFn = func(context.Context, uuid.UUID) error { unpinned = true; return nil }
	q.CreateFeedFn = func(_ context.Context, arg sqlc.CreateFeedParams) (sqlc.Feed, error) {
		created = arg
		return sqlc.Feed{ID: uuid.New(), Name: arg.Name, Sort: arg.Sort, HideSeen: arg.HideSeen, Pinned: arg.Pinned, Position: arg.Position}, nil
	}
	q.DeleteFeedChannelsFn = func(context.Context, uuid.UUID) error { return nil }
	q.AddFeedChannelFn = func(_ context.Context, arg sqlc.AddFeedChannelParams) error {
		added = append(added, arg.ChannelID)
		return nil
	}
	h := newTestServer(client, q).Router()

	rec := do(t, h, http.MethodPost, "/api/v1/feeds", `{"name":"Maker","channel_ids":["A","A","B"],"pinned":true,"sort":"oldest"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if !unpinned || !created.Pinned || created.Position != 2 || created.Sort != "oldest" || !created.HideSeen {
		t.Errorf("created = %+v unpinned=%v", created, unpinned)
	}
	if !reflect.DeepEqual(added, []string{"A", "B"}) {
		t.Errorf("channels = %v", added)
	}
	f := decode[FeedDTO](t, rec)
	if f.ChannelCount != 2 || f.UnseenCount != 1 {
		t.Errorf("feed = %+v", f)
	}
	rec = do(t, h, http.MethodPost, "/api/v1/feeds", `{"name":"x","sort":"random"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("invalid sort: status = %d", rec.Code)
	}
}
