package api

import (
	"net/http"
	"testing"

	"github.com/Seklfreak/flimm/internal/ta"
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

// With a seed the endpoint walks the shuffled order, and reports its head so a
// client can start a shuffled run without knowing the order itself.
func TestVideoNavShuffled(t *testing.T) {
	h := navFixture(t)
	const seed = "?playlist=PL1&shuffle=abc123"

	first := decode[NavResponse](t, do(t, h, http.MethodGet, "/api/v1/videos/p1/nav"+seed, ""))
	if first.First == nil {
		t.Fatal("no first item reported")
	}
	// Walking forward from the head must reach every item exactly once.
	seen := []string{first.First.ID}
	cur := *first.First
	for range 5 {
		got := decode[NavResponse](t, do(t, h, http.MethodGet, "/api/v1/videos/"+cur.ID+"/nav"+seed, ""))
		if got.Next == nil {
			break
		}
		seen = append(seen, got.Next.ID)
		cur = *got.Next
	}
	if len(seen) != 3 {
		t.Fatalf("walked %v, want all 3 items once", seen)
	}
	// Stepping back from the end must retrace the same order.
	back := decode[NavResponse](t, do(t, h, http.MethodGet, "/api/v1/videos/"+seen[2]+"/nav"+seed, ""))
	if back.Previous == nil || back.Previous.ID != seen[1] {
		t.Errorf("previous = %+v, want %s", back.Previous, seen[1])
	}
}
