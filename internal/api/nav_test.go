package api

import (
	"net/http"
	"testing"

	"github.com/Seklfreak/archive-client/internal/ta"
)

func navFixture(t *testing.T) http.Handler {
	t.Helper()
	client := ta.NewFake()
	entries := make([]ta.PlaylistEntry, 0, 3)
	for i, id := range []string{"p1", "p2", "p3"} {
		client.Videos[id] = &ta.Video{
			YoutubeID: id, Title: "Video " + id,
			Player:   ta.Player{Duration: 600},
			Playlist: []string{"PL1"},
		}
		entries = append(entries, ta.PlaylistEntry{YoutubeID: id, Idx: i, Downloaded: true})
	}
	// Order comes from the playlist entries, not from the video map.
	client.Playlists["PL1"] = &ta.Playlist{
		PlaylistID: "PL1", PlaylistName: "List", PlaylistType: "custom",
		PlaylistEntries: entries,
	}
	return newTestServer(client, newEventStore().querier()).Router()
}

func TestVideoNavInPlaylist(t *testing.T) {
	h := navFixture(t)

	mid := decode[NavResponse](t, do(t, h, http.MethodGet, "/api/v1/videos/p2/nav?playlist=PL1", ""))
	if mid.Index != 1 || mid.Total != 3 {
		t.Fatalf("index/total = %d/%d, want 1/3", mid.Index, mid.Total)
	}
	if mid.Previous == nil || mid.Previous.ID != "p1" {
		t.Errorf("previous = %+v, want p1", mid.Previous)
	}
	if mid.Next == nil || mid.Next.ID != "p3" {
		t.Errorf("next = %+v, want p3", mid.Next)
	}

	// The ends must report no neighbour rather than wrapping around.
	first := decode[NavResponse](t, do(t, h, http.MethodGet, "/api/v1/videos/p1/nav?playlist=PL1", ""))
	if first.Previous != nil {
		t.Errorf("first item has a previous: %+v", first.Previous)
	}
	if first.Next == nil || first.Next.ID != "p2" {
		t.Errorf("next = %+v, want p2", first.Next)
	}
	last := decode[NavResponse](t, do(t, h, http.MethodGet, "/api/v1/videos/p3/nav?playlist=PL1", ""))
	if last.Next != nil {
		t.Errorf("last item has a next: %+v", last.Next)
	}
	if last.Previous == nil || last.Previous.ID != "p2" {
		t.Errorf("previous = %+v, want p2", last.Previous)
	}
}

// Without a context there is nothing to step through; the player hides the
// buttons rather than showing dead ones.
func TestVideoNavWithoutContext(t *testing.T) {
	h := navFixture(t)
	got := decode[NavResponse](t, do(t, h, http.MethodGet, "/api/v1/videos/p2/nav", ""))
	if got.Index != -1 || got.Total != 0 || got.Previous != nil || got.Next != nil {
		t.Errorf("nav without context = %+v, want empty", got)
	}
}
