package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/Seklfreak/flimm/internal/db/sqlc"
	"github.com/Seklfreak/flimm/internal/ta"
)

// listedPlaylistIDs is the ids GET /playlists (with the given query) returns.
func listedPlaylistIDs(t *testing.T, h http.Handler, query string) []string {
	t.Helper()
	rec := do(t, h, http.MethodGet, "/api/v1/playlists"+query, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	page := decode[Page[PlaylistSummary]](t, rec)
	ids := make([]string, 0, len(page.Items))
	for _, it := range page.Items {
		ids = append(ids, it.ID)
	}
	return ids
}

// A channel's own playlists belong to its channel page; the Playlists page
// lists one only once the viewer took it up. Otherwise an archive that indexes
// every playlist a prolific channel owns floods the page.
func TestListPlaylistsHidesChannelPlaylistsNobodyTookUp(t *testing.T) {
	client := ta.NewFake()
	client.Playlists["PL-custom"] = &ta.Playlist{PlaylistID: "PL-custom", PlaylistName: "Mine", PlaylistType: "custom"}
	for _, id := range []string{"PL-a", "PL-b", "PL-c"} {
		client.Playlists[id] = &ta.Playlist{PlaylistID: id, PlaylistName: "Series " + id, PlaylistType: "regular", PlaylistChannelID: "UC1", PlaylistChannel: "Channel UC1"}
	}
	h := newTestServer(client, newEventStore().querier()).Router()

	if got := listedPlaylistIDs(t, h, ""); len(got) != 1 || got[0] != "PL-custom" {
		t.Errorf("unadopted channel playlists listed: %v, want [PL-custom]", got)
	}

	// Pinning one and marking another as music is what puts them on the page.
	if rec := do(t, h, http.MethodPut, "/api/v1/playlists/PL-a/pinned", `{"pinned":true}`); rec.Code != http.StatusNoContent {
		t.Fatalf("pin status = %d", rec.Code)
	}
	if rec := do(t, h, http.MethodPut, "/api/v1/playlists/PL-b/music", `{"music":true}`); rec.Code != http.StatusNoContent {
		t.Fatalf("music status = %d", rec.Code)
	}
	got := listedPlaylistIDs(t, h, "")
	want := []string{"PL-custom", "PL-a", "PL-b"} // custom first, then by name
	if len(got) != 3 || got[0] != want[0] {
		t.Fatalf("playlists = %v, want %v", got, want)
	}
	for _, id := range want[1:] {
		found := false
		for _, g := range got {
			found = found || g == id
		}
		if !found {
			t.Errorf("taken-up playlist %s missing from %v", id, got)
		}
	}

	// The kind filter obeys the same rule.
	if got := listedPlaylistIDs(t, h, "?kind=channel"); len(got) != 2 {
		t.Errorf("kind=channel = %v, want the two taken-up ones", got)
	}
	if got := listedPlaylistIDs(t, h, "?kind=custom"); len(got) != 1 || got[0] != "PL-custom" {
		t.Errorf("kind=custom = %v, want [PL-custom]", got)
	}

	// The channel page still lists every playlist of the channel — that is
	// where one is found and taken up.
	rec := do(t, h, http.MethodGet, "/api/v1/channels/UC1/playlists", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("channel playlists status = %d: %s", rec.Code, rec.Body.String())
	}
	if lists := decode[[]PlaylistSummary](t, rec); len(lists) != 3 {
		t.Errorf("channel page lists %d playlists, want all 3", len(lists))
	}
}

// The playlist "In feeds:" control, mirroring the channel one — including the
// 404 (not 403) for a feed the user does not own.
func TestSetPlaylistFeedsAndBadges(t *testing.T) {
	client := ta.NewFake()
	client.Playlists["PL-a"] = &ta.Playlist{PlaylistID: "PL-a", PlaylistName: "Series A", PlaylistType: "regular", PlaylistChannelID: "UC1"}

	feedID := uuid.New()
	es := newEventStore()
	q := es.querier()
	q.GetFeedFn = func(_ context.Context, arg sqlc.GetFeedParams) (sqlc.Feed, error) {
		if arg.ID != feedID {
			return sqlc.Feed{}, errNoRows
		}
		return sqlc.Feed{ID: feedID, Name: "Series"}, nil
	}
	var rows []sqlc.ListFeedPlaylistsForUserRow
	q.ListFeedPlaylistsForUserFn = func(context.Context, uuid.UUID) ([]sqlc.ListFeedPlaylistsForUserRow, error) {
		return rows, nil
	}
	q.DeletePlaylistFromUserFeedsFn = func(_ context.Context, arg sqlc.DeletePlaylistFromUserFeedsParams) error {
		kept := rows[:0]
		for _, r := range rows {
			if r.PlaylistID != arg.PlaylistID {
				kept = append(kept, r)
			}
		}
		rows = kept
		return nil
	}
	q.NextFeedPlaylistPositionFn = func(context.Context, uuid.UUID) (int32, error) { return 0, nil }
	q.AddFeedPlaylistFn = func(_ context.Context, arg sqlc.AddFeedPlaylistParams) error {
		rows = append(rows, sqlc.ListFeedPlaylistsForUserRow{FeedID: arg.FeedID, PlaylistID: arg.PlaylistID, FeedName: "Series"})
		return nil
	}
	h := newTestServer(client, q).Router()

	rec := do(t, h, http.MethodPut, "/api/v1/playlists/PL-a/feeds", `{"feed_ids":["`+feedID.String()+`","everything"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	resp := decode[struct {
		Feeds []FeedRef `json:"feeds"`
	}](t, rec)
	if len(resp.Feeds) != 1 || resp.Feeds[0].ID != feedID.String() {
		t.Errorf("feeds = %+v, want the one feed (everything skipped)", resp.Feeds)
	}

	// The badge shows on the playlist detail and on the channel's list.
	detail := decode[PlaylistDetail](t, do(t, h, http.MethodGet, "/api/v1/playlists/PL-a", ""))
	if len(detail.Feeds) != 1 || detail.Feeds[0].Name != "Series" {
		t.Errorf("detail feeds = %+v", detail.Feeds)
	}
	chLists := decode[[]PlaylistSummary](t, do(t, h, http.MethodGet, "/api/v1/channels/UC1/playlists", ""))
	if len(chLists) != 1 || len(chLists[0].Feeds) != 1 {
		t.Errorf("channel playlists = %+v, want the badge on PL-a", chLists)
	}

	// A feed id that is not the user's own resolves like one that does not
	// exist at all.
	rec = do(t, h, http.MethodPut, "/api/v1/playlists/PL-a/feeds", `{"feed_ids":["`+uuid.NewString()+`"]}`)
	if rec.Code != http.StatusNotFound {
		t.Errorf("foreign feed: status = %d, want 404", rec.Code)
	}

	// Clearing works and empties the badge again.
	rec = do(t, h, http.MethodPut, "/api/v1/playlists/PL-a/feeds", `{"feed_ids":[]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("clear status = %d", rec.Code)
	}
	if detail := decode[PlaylistDetail](t, do(t, h, http.MethodGet, "/api/v1/playlists/PL-a", "")); len(detail.Feeds) != 0 {
		t.Errorf("feeds after clear = %+v", detail.Feeds)
	}
}
