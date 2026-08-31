package api

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Seklfreak/flimm/internal/db/sqlc"
	"github.com/Seklfreak/flimm/internal/ta"
)

// A long playlist must be scrollable rather than cut off: up-next pages
// through everything after the current video instead of stopping at a fixed
// number of items.
func TestUpNextPagesThroughALongPlaylist(t *testing.T) {
	client := ta.NewFake()
	entries := make([]ta.PlaylistEntry, 0, 60)
	for i := range 60 {
		id := fmt.Sprintf("v%02d", i)
		client.Videos[id] = &ta.Video{YoutubeID: id, Title: id, Player: ta.Player{Duration: 600}, Playlist: []string{"PL1"}}
		entries = append(entries, ta.PlaylistEntry{YoutubeID: id, Idx: i, Downloaded: true})
	}
	client.Playlists["PL1"] = &ta.Playlist{PlaylistID: "PL1", PlaylistName: "Long", PlaylistType: "custom", PlaylistEntries: entries}
	h := newTestServer(client, newEventStore().querier()).Router()

	// From the first video, 59 follow — more than the old fixed cap of 20.
	first := decode[Page[VideoSummary]](t, do(t, h, http.MethodGet, "/api/v1/videos/v00/up-next?playlist=PL1&page_size=30", ""))
	if first.Total != 59 {
		t.Errorf("total = %d, want 59", first.Total)
	}
	if len(first.Items) != 30 {
		t.Fatalf("page 0 = %d items, want 30", len(first.Items))
	}
	if first.Items[0].ID != "v01" {
		t.Errorf("page 0 starts at %s, want v01", first.Items[0].ID)
	}

	// Scrolling on continues where the first page stopped, without repeats.
	second := decode[Page[VideoSummary]](t, do(t, h, http.MethodGet, "/api/v1/videos/v00/up-next?playlist=PL1&page_size=30&page=1", ""))
	if len(second.Items) != 29 {
		t.Fatalf("page 1 = %d items, want 29", len(second.Items))
	}
	if second.Items[0].ID != "v31" {
		t.Errorf("page 1 starts at %s, want v31", second.Items[0].ID)
	}
	seen := map[string]bool{}
	for _, v := range append(first.Items, second.Items...) {
		if seen[v.ID] {
			t.Fatalf("%s appeared on both pages", v.ID)
		}
		seen[v.ID] = true
	}
	if seen["v00"] {
		t.Error("the video being watched appeared in its own up-next list")
	}
}

// ?before=true is the other half of the panel: what already played in this
// context, closest first, and honestly empty when nothing did.
func TestUpNextBeforeListsPreviousVideosClosestFirst(t *testing.T) {
	client := ta.NewFake()
	entries := make([]ta.PlaylistEntry, 0, 5)
	for i := range 5 {
		id := fmt.Sprintf("v%02d", i)
		client.Videos[id] = &ta.Video{YoutubeID: id, Title: id, Player: ta.Player{Duration: 600}, Playlist: []string{"PL1"}}
		entries = append(entries, ta.PlaylistEntry{YoutubeID: id, Idx: i, Downloaded: true})
	}
	client.Playlists["PL1"] = &ta.Playlist{PlaylistID: "PL1", PlaylistName: "Short", PlaylistType: "custom", PlaylistEntries: entries}
	h := newTestServer(client, newEventStore().querier()).Router()

	page := decode[Page[VideoSummary]](t, do(t, h, http.MethodGet, "/api/v1/videos/v03/up-next?playlist=PL1&before=true", ""))
	if got := ids(page.Items); len(got) != 3 || got[0] != "v02" || got[2] != "v00" {
		t.Errorf("before = %v, want [v02 v01 v00] (closest first)", got)
	}

	// The first video has nothing before it — and no similar-videos fallback,
	// which would masquerade as history.
	if page := decode[Page[VideoSummary]](t, do(t, h, http.MethodGet, "/api/v1/videos/v00/up-next?playlist=PL1&before=true", "")); len(page.Items) != 0 {
		t.Errorf("before the first video = %v, want nothing", ids(page.Items))
	}

	// Without a context there is no history to unfold either.
	if page := decode[Page[VideoSummary]](t, do(t, h, http.MethodGet, "/api/v1/videos/v03/up-next?before=true", "")); len(page.Items) != 0 {
		t.Errorf("no context = %v, want nothing", ids(page.Items))
	}
}

// The end of a playlist is the end: what follows is offered as suggestions,
// said out loud so nothing autoplays into it or presents it under the
// playlist's name — and a video already watched is no suggestion at all.
func TestUpNextMarksSuggestionsAtTheEndOfAPlaylist(t *testing.T) {
	client := ta.NewFake()
	for _, id := range []string{"v0", "v1"} {
		client.Videos[id] = &ta.Video{YoutubeID: id, Title: id, Player: ta.Player{Duration: 600}, Playlist: []string{"PL1"}}
	}
	client.Playlists["PL1"] = &ta.Playlist{
		PlaylistID: "PL1", PlaylistName: "Two", PlaylistType: "custom",
		PlaylistEntries: []ta.PlaylistEntry{{YoutubeID: "v0", Idx: 0, Downloaded: true}, {YoutubeID: "v1", Idx: 1, Downloaded: true}},
	}
	// Two suggestions, one of them already watched.
	client.Videos["seen"] = &ta.Video{YoutubeID: "seen", Title: "Seen", Player: ta.Player{Duration: 600}}
	client.Videos["fresh"] = &ta.Video{YoutubeID: "fresh", Title: "Fresh", Player: ta.Player{Duration: 600}}
	client.SimilarFn = func(string) ([]ta.Video, error) {
		return []ta.Video{*client.Videos["seen"], *client.Videos["fresh"]}, nil
	}
	es := newEventStore()
	es.events["seen"] = sqlc.WatchEvent{
		VideoID: "seen", Position: 600, Duration: 600,
		CompletedAt:  pgtype.Timestamptz{Time: time.Now(), Valid: true},
		LastPlayedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}
	h := newTestServer(client, es.querier()).Router()

	// Mid-playlist: the real queue, and no flag.
	mid := decode[upNextPage](t, do(t, h, http.MethodGet, "/api/v1/videos/v0/up-next?playlist=PL1", ""))
	if mid.Suggestions {
		t.Error("the rest of a playlist was marked as suggestions")
	}
	if len(mid.Items) != 1 || mid.Items[0].ID != "v1" {
		t.Fatalf("queue = %+v, want [v1]", mid.Items)
	}

	// The last video: suggestions, marked, with the watched one dropped.
	end := decode[upNextPage](t, do(t, h, http.MethodGet, "/api/v1/videos/v1/up-next?playlist=PL1", ""))
	if !end.Suggestions {
		t.Error("suggestions at the end of a playlist were passed off as the queue")
	}
	if len(end.Items) != 1 || end.Items[0].ID != "fresh" {
		t.Fatalf("suggestions = %+v, want [fresh] (the watched one dropped)", end.Items)
	}
}
