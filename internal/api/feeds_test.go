package api

import (
	"context"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

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
	// Composition is lazy: the server walked one item past the window and
	// stopped, so total is a floor and has_more is what says "keep paging".
	if !page.HasMore || page.Total < 3 {
		t.Errorf("page 0 = total %d, has_more %v; want has_more with total >= 3", page.Total, page.HasMore)
	}
	a1 := page.Items[1]
	// Position is where playback resumes — the rewind off the 300 that was
	// stored — while progress still reports how far the viewer actually got.
	if a1.Position != 285 || a1.Progress != 0.5 || a1.Watched || a1.LastPlayedAt == nil {
		t.Errorf("a1 overlay = %+v", a1)
	}
	rec = do(t, h, http.MethodGet, "/api/v1/feeds/"+feed.ID.String()+"/videos?view=all&page_size=2&page=1", "")
	page = decode[Page[VideoSummary]](t, rec)
	if got := ids(page.Items); !reflect.DeepEqual(got, []string{"a2", "b2"}) {
		t.Errorf("page 1 = %v, want [a2 b2]", got)
	}
	// The last page walked the list out, so here total is exact.
	if page.HasMore || page.Total != 4 {
		t.Errorf("page 1 = total %d, has_more %v; want 4 and no more", page.Total, page.HasMore)
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
	var addedPlaylists []string
	q.DeleteFeedPlaylistsFn = func(context.Context, uuid.UUID) error { return nil }
	q.AddFeedPlaylistFn = func(_ context.Context, arg sqlc.AddFeedPlaylistParams) error {
		addedPlaylists = append(addedPlaylists, arg.PlaylistID)
		return nil
	}
	h := newTestServer(client, q).Router()

	rec := do(t, h, http.MethodPost, "/api/v1/feeds", `{"name":"Maker","channel_ids":["A","A","B"],"playlist_ids":["PL1","PL1"],"pinned":true,"sort":"oldest"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if !unpinned || !created.Pinned || created.Position != 2 || created.Sort != "oldest" || !created.HideSeen {
		t.Errorf("created = %+v unpinned=%v", created, unpinned)
	}
	if !reflect.DeepEqual(added, []string{"A", "B"}) {
		t.Errorf("channels = %v", added)
	}
	if !reflect.DeepEqual(addedPlaylists, []string{"PL1"}) {
		t.Errorf("playlists = %v", addedPlaylists)
	}
	f := decode[FeedDTO](t, rec)
	if f.ChannelCount != 2 || f.PlaylistCount != 1 || f.UnseenCount != 1 {
		t.Errorf("feed = %+v", f)
	}
	rec = do(t, h, http.MethodPost, "/api/v1/feeds", `{"name":"x","sort":"random"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("invalid sort: status = %d", rec.Code)
	}
}

// An unseen feed opens with what the viewer is part-way through: those are the
// videos they came back for, and there is no separate "Continue" filter to go
// and find them in any more.
func TestUnseenFeedOpensWithWhatIsInProgress(t *testing.T) {
	feed := sqlc.Feed{ID: uuid.New(), Name: "Home", Sort: "newest", HideSeen: true, IncludeShorts: true}
	_, es, h := feedFixture(t, feed, []string{"A", "B"})
	// a1 is the oldest video in the feed, so a plain unseen list would put it
	// last; half-watched, it belongs at the top.
	es.events["a1"] = sqlc.WatchEvent{
		VideoID: "a1", ChannelID: "A", Position: 120, Duration: 600,
		LastPlayedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}

	rec := do(t, h, http.MethodGet, "/api/v1/feeds/"+feed.ID.String()+"/videos?view=unseen&page_size=10", "")
	page := decode[Page[VideoSummary]](t, rec)
	got := ids(page.Items)
	if len(got) == 0 || got[0] != "a1" {
		t.Fatalf("items = %v, want a1 first (in progress)", got)
	}
	// ...and only once: the tail must not list it again further down.
	if count := strings.Count(strings.Join(got, ","), "a1"); count != 1 {
		t.Errorf("a1 appears %d times in %v", count, got)
	}
}

// The head is not a page of its own: whatever room is left goes to the rest of
// the unseen list, and paging carries on across the join.
func TestUnseenFeedPagesThroughTheHeadIntoTheTail(t *testing.T) {
	feed := sqlc.Feed{ID: uuid.New(), Name: "Home", Sort: "newest", HideSeen: true, IncludeShorts: true}
	_, es, h := feedFixture(t, feed, []string{"A", "B"})
	es.events["a1"] = sqlc.WatchEvent{
		VideoID: "a1", ChannelID: "A", Position: 120, Duration: 600,
		LastPlayedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}

	rec := do(t, h, http.MethodGet, "/api/v1/feeds/"+feed.ID.String()+"/videos?view=unseen&page_size=2", "")
	first := decode[Page[VideoSummary]](t, rec)
	if got := ids(first.Items); len(got) != 2 || got[0] != "a1" {
		t.Fatalf("page 0 = %v, want a1 and one more", got)
	}
	if !first.HasMore || first.NextCursor == "" {
		t.Fatalf("page 0 = %+v, want more to follow", first)
	}

	rec = do(t, h, http.MethodGet,
		"/api/v1/feeds/"+feed.ID.String()+"/videos?view=unseen&page_size=2&cursor="+url.QueryEscape(first.NextCursor), "")
	second := decode[Page[VideoSummary]](t, rec)
	for _, id := range ids(second.Items) {
		if id == "a1" {
			t.Errorf("page 1 = %v, showing the in-progress video twice", ids(second.Items))
		}
	}
	if len(second.Items) == 0 {
		t.Error("page 1 is empty; the tail should carry on from the head")
	}
}

// A feed showing everything is not reordered: "in progress first" is what the
// unseen view is for.
func TestTheAllViewIsNotReordered(t *testing.T) {
	feed := sqlc.Feed{ID: uuid.New(), Name: "Home", Sort: "newest", HideSeen: true, IncludeShorts: true}
	_, es, h := feedFixture(t, feed, []string{"A", "B"})
	es.events["a1"] = sqlc.WatchEvent{
		VideoID: "a1", ChannelID: "A", Position: 120, Duration: 600,
		LastPlayedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}

	rec := do(t, h, http.MethodGet, "/api/v1/feeds/"+feed.ID.String()+"/videos?view=all&page_size=10", "")
	page := decode[Page[VideoSummary]](t, rec)
	// Newest first, so the newest video leads and the half-watched oldest one
	// stays where the feed's own sort puts it.
	if got := ids(page.Items); len(got) == 0 || got[0] != "b2" {
		t.Errorf("items = %v, want the feed's own order (b2 newest first)", got)
	}
}

// A feed's videos are the union of its channels and its playlist sources —
// a series can sit in a feed without the rest of its channel — and a video
// reached through both kinds appears once.
func TestFeedMergesPlaylistAndChannelSources(t *testing.T) {
	client := ta.NewFake()
	inPL := video("a2", "A", "2026-08-03", 100, false)
	inPL.Playlist = []string{"PL"}
	series := video("b1", "B", "2026-08-02", 3000, false)
	series.Playlist = []string{"PL"}
	client.AddVideo(video("a1", "A", "2026-08-01", 600, false))
	client.AddVideo(inPL)
	client.AddVideo(series)
	client.AddVideo(video("c1", "C", "2026-08-05", 500, false))

	feed := sqlc.Feed{ID: uuid.New(), Name: "Series", Sort: "newest", HideSeen: false}
	es := newEventStore()
	q := es.querier()
	q.GetFeedFn = func(_ context.Context, arg sqlc.GetFeedParams) (sqlc.Feed, error) {
		if arg.ID != feed.ID {
			return sqlc.Feed{}, errNoRows
		}
		return feed, nil
	}
	q.ListFeedChannelsFn = func(context.Context, uuid.UUID) ([]string, error) { return []string{"A"}, nil }
	q.ListFeedPlaylistsFn = func(context.Context, uuid.UUID) ([]string, error) { return []string{"PL"}, nil }
	h := newTestServer(client, q).Router()

	rec := do(t, h, http.MethodGet, "/api/v1/feeds/"+feed.ID.String()+"/videos?view=all", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	page := decode[Page[VideoSummary]](t, rec)
	// a2 belongs to both the channel and the playlist: once, not twice.
	if got := ids(page.Items); !reflect.DeepEqual(got, []string{"a2", "b1", "a1"}) {
		t.Errorf("items = %v, want [a2 b1 a1] (union, deduped, newest first)", got)
	}

	// The unseen hint covers both source kinds (and double-counts overlap —
	// documented as a hint): channel A has 2 unseen + playlist PL has 2.
	rec = do(t, h, http.MethodGet, "/api/v1/feeds/"+feed.ID.String(), "")
	f := decode[FeedDTO](t, rec)
	if f.ChannelCount != 1 || f.PlaylistCount != 1 || f.UnseenCount != 4 {
		t.Errorf("feed = %+v, want 1 channel, 1 playlist, unseen hint 4", f)
	}
}

// An in-progress video that reached the feed through a playlist source has no
// channel membership to say so — the video document's playlist list does.
func TestUnseenFeedHeadIncludesPlaylistSourcedVideo(t *testing.T) {
	client := ta.NewFake()
	client.AddVideo(video("a1", "A", "2026-08-01", 600, false))
	series := video("p1", "B", "2026-08-02", 3000, false)
	series.Playlist = []string{"PL"}
	client.AddVideo(series)
	client.AddVideo(video("c1", "C", "2026-08-05", 500, false))

	feed := sqlc.Feed{ID: uuid.New(), Name: "Series", Sort: "newest", HideSeen: true}
	es := newEventStore()
	q := es.querier()
	q.GetFeedFn = func(context.Context, sqlc.GetFeedParams) (sqlc.Feed, error) { return feed, nil }
	q.ListFeedChannelsFn = func(context.Context, uuid.UUID) ([]string, error) { return []string{"A"}, nil }
	q.ListFeedPlaylistsFn = func(context.Context, uuid.UUID) ([]string, error) { return []string{"PL"}, nil }
	es.events["p1"] = sqlc.WatchEvent{
		VideoID: "p1", ChannelID: "B", Position: 100, Duration: 3000,
		LastPlayedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}
	es.events["c1"] = sqlc.WatchEvent{
		VideoID: "c1", ChannelID: "C", Position: 100, Duration: 500,
		LastPlayedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}
	h := newTestServer(client, q).Router()

	rec := do(t, h, http.MethodGet, "/api/v1/feeds/"+feed.ID.String()+"/videos?view=unseen", "")
	page := decode[Page[VideoSummary]](t, rec)
	got := ids(page.Items)
	if len(got) == 0 || got[0] != "p1" {
		t.Fatalf("items = %v, want p1 first (in progress via its playlist)", got)
	}
	for _, id := range got {
		if id == "c1" {
			t.Errorf("c1 listed although it is in no source: %v", got)
		}
	}
}

// A PUT without playlist_ids leaves them alone — an older client's full
// update must not wipe a feed's series — while an explicit empty list clears.
func TestUpdateFeedPlaylistsAbsentMeansUnchanged(t *testing.T) {
	feed := sqlc.Feed{ID: uuid.New(), Name: "Home", Sort: "newest", HideSeen: true}
	es := newEventStore()
	q := es.querier()
	q.GetFeedFn = func(context.Context, sqlc.GetFeedParams) (sqlc.Feed, error) { return feed, nil }
	q.UpdateFeedFn = func(_ context.Context, arg sqlc.UpdateFeedParams) (sqlc.Feed, error) {
		feed.Name = arg.Name
		return feed, nil
	}
	q.ListFeedChannelsFn = func(context.Context, uuid.UUID) ([]string, error) { return nil, nil }
	q.ListFeedPlaylistsFn = func(context.Context, uuid.UUID) ([]string, error) { return []string{"PL"}, nil }
	cleared := false
	q.DeleteFeedPlaylistsFn = func(context.Context, uuid.UUID) error { cleared = true; return nil }
	q.DeleteFeedChannelsFn = func(context.Context, uuid.UUID) error { return nil }
	h := newTestServer(ta.NewFake(), q).Router()

	rec := do(t, h, http.MethodPut, "/api/v1/feeds/"+feed.ID.String(), `{"name":"Still home"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if cleared {
		t.Error("PUT without playlist_ids rewrote the playlist sources")
	}
	if f := decode[FeedDTO](t, rec); f.PlaylistCount != 1 {
		t.Errorf("feed = %+v, want the kept playlist reported", f)
	}

	rec = do(t, h, http.MethodPut, "/api/v1/feeds/"+feed.ID.String(), `{"name":"Still home","playlist_ids":[]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if !cleared {
		t.Error("PUT with an explicit empty playlist_ids did not clear them")
	}
}
