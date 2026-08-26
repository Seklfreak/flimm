package api

import (
	"fmt"
	"net/http"
	"testing"

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
