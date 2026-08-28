package api

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Seklfreak/flimm/internal/db/sqlc"
	"github.com/Seklfreak/flimm/internal/sqlctest"
	"github.com/Seklfreak/flimm/internal/ta"
)

// dismissStore is an in-memory dismissed_videos table.
type dismissStore struct {
	ids map[string]bool
}

func newDismissStore() *dismissStore { return &dismissStore{ids: map[string]bool{}} }

// wire adds the dismissal queries to a querier a test already has.
func (d *dismissStore) wire(q *sqlctest.FakeQuerier) *sqlctest.FakeQuerier {
	q.DismissVideoFn = func(_ context.Context, arg sqlc.DismissVideoParams) error {
		d.ids[arg.VideoID] = true
		return nil
	}
	q.UndismissVideoFn = func(_ context.Context, arg sqlc.UndismissVideoParams) error {
		delete(d.ids, arg.VideoID)
		return nil
	}
	q.ListDismissedForVideosFn = func(_ context.Context, arg sqlc.ListDismissedForVideosParams) ([]string, error) {
		var out []string
		for _, id := range arg.VideoIds {
			if d.ids[id] {
				out = append(out, id)
			}
		}
		return out, nil
	}
	return q
}

func dismissFixture(t *testing.T) (*ta.Fake, *eventStore, *dismissStore, http.Handler) {
	t.Helper()
	client := ta.NewFake()
	client.AddVideo(video("v1", "A", "2026-08-01", 600, false))
	client.AddVideo(video("v2", "A", "2026-08-02", 600, false))
	es := newEventStore()
	ds := newDismissStore()
	return client, es, ds, newTestServer(client, ds.wire(es.querier())).Router()
}

func TestDismissTakesAVideoOutOfTheFeeds(t *testing.T) {
	client, _, _, h := dismissFixture(t)
	_ = client

	before := decode[Page[VideoSummary]](t, do(t, h, http.MethodGet, "/api/v1/feeds/everything/videos?view=all", ""))
	if len(before.Items) != 2 {
		t.Fatalf("feed starts with %d videos, want 2", len(before.Items))
	}

	if rec := do(t, h, http.MethodPost, "/api/v1/videos/v1/dismiss", ""); rec.Code != http.StatusOK {
		t.Fatalf("dismiss: %d %s", rec.Code, rec.Body.String())
	}

	after := decode[Page[VideoSummary]](t, do(t, h, http.MethodGet, "/api/v1/feeds/everything/videos?view=all", ""))
	if len(after.Items) != 1 || after.Items[0].ID != "v2" {
		t.Errorf("feed after dismissal = %+v, want only v2", ids(after.Items))
	}
}

// The whole point of the feature: it must not pretend the video was watched,
// because Flimm writes watch state back to TubeArchivist.
func TestDismissLeavesWatchStateAlone(t *testing.T) {
	client, _, _, h := dismissFixture(t)

	if rec := do(t, h, http.MethodPost, "/api/v1/videos/v1/dismiss", ""); rec.Code != http.StatusOK {
		t.Fatalf("dismiss: %d", rec.Code)
	}
	for _, call := range client.Calls {
		if call == "SetWatched v1 true" {
			t.Fatal("dismissing a video marked it watched in TubeArchivist")
		}
	}
	v := decode[VideoDetail](t, do(t, h, http.MethodGet, "/api/v1/videos/v1", ""))
	if v.Watched {
		t.Error("dismissed video came back watched")
	}
	if !v.Dismissed {
		t.Error("dismissed video does not report dismissed")
	}
}

// A dismissed video is still reachable where a viewer would look for it —
// that is where it gets put back.
func TestDismissedVideoStaysOnTheChannel(t *testing.T) {
	_, _, _, h := dismissFixture(t)
	if rec := do(t, h, http.MethodPost, "/api/v1/videos/v1/dismiss", ""); rec.Code != http.StatusOK {
		t.Fatalf("dismiss: %d", rec.Code)
	}

	channel := decode[Page[VideoSummary]](t, do(t, h, http.MethodGet, "/api/v1/channels/A/videos", ""))
	found := false
	for _, item := range channel.Items {
		if item.ID == "v1" {
			found = true
			if !item.Dismissed {
				t.Error("the channel page does not mark it dismissed")
			}
		}
	}
	if !found {
		t.Error("a dismissed video vanished from its channel page too")
	}
}

func TestUndismissPutsItBack(t *testing.T) {
	_, _, _, h := dismissFixture(t)
	do(t, h, http.MethodPost, "/api/v1/videos/v1/dismiss", "")

	if rec := do(t, h, http.MethodDelete, "/api/v1/videos/v1/dismiss", ""); rec.Code != http.StatusOK {
		t.Fatalf("undismiss: %d %s", rec.Code, rec.Body.String())
	}
	after := decode[Page[VideoSummary]](t, do(t, h, http.MethodGet, "/api/v1/feeds/everything/videos?view=all", ""))
	if len(after.Items) != 2 {
		t.Errorf("feed after undismiss = %v, want both back", ids(after.Items))
	}
	// Undoing twice is a success, so an undo button cannot fail on a double tap.
	if rec := do(t, h, http.MethodDelete, "/api/v1/videos/v1/dismiss", ""); rec.Code != http.StatusOK {
		t.Errorf("second undismiss: %d", rec.Code)
	}
}

// A half-watched video that gets dismissed must not come back through the
// "Continue watching" view — the viewer said they are not watching it.
func TestDismissedVideoLeavesTheContinueView(t *testing.T) {
	client, es, ds, _ := dismissFixture(t)
	es.events["v1"] = sqlc.WatchEvent{
		VideoID: "v1", ChannelID: "A", Position: 120, Duration: 600,
		LastPlayedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}
	h := newTestServer(client, ds.wire(es.querier())).Router()

	before := decode[Page[VideoSummary]](t, do(t, h, http.MethodGet, "/api/v1/feeds/everything/videos?view=continue", ""))
	if len(before.Items) != 1 {
		t.Fatalf("continue view starts with %v, want v1", ids(before.Items))
	}
	if rec := do(t, h, http.MethodPost, "/api/v1/videos/v1/dismiss", ""); rec.Code != http.StatusOK {
		t.Fatalf("dismiss: %d", rec.Code)
	}
	after := decode[Page[VideoSummary]](t, do(t, h, http.MethodGet, "/api/v1/feeds/everything/videos?view=continue", ""))
	if len(after.Items) != 0 {
		t.Errorf("continue view after dismissal = %v, want empty", ids(after.Items))
	}
}

func TestDismissUnknownVideoIs404(t *testing.T) {
	_, _, _, h := dismissFixture(t)
	if rec := do(t, h, http.MethodPost, "/api/v1/videos/nope/dismiss", ""); rec.Code != http.StatusNotFound {
		t.Errorf("dismissing an unknown video = %d, want 404", rec.Code)
	}
}
