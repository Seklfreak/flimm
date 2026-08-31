package api

import (
	"context"
	"net/http"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/Seklfreak/flimm/internal/ta"
)

// countingDocs counts the video documents a request reads. That count is the
// subject: a playlist summary is six integers, and producing them used to cost
// every document in the playlist.
type countingDocs struct {
	*ta.Fake
	lists   atomic.Int64
	fetches atomic.Int64
}

func (c *countingDocs) ListVideos(ctx context.Context, q ta.VideoQuery) (*ta.VideoPage, error) {
	if q.Playlist != "" {
		c.lists.Add(1)
	}
	return c.Fake.ListVideos(ctx, q)
}

func (c *countingDocs) GetVideo(ctx context.Context, id string) (*ta.Video, error) {
	c.fetches.Add(1)
	return c.Fake.GetVideo(ctx, id)
}

func (c *countingDocs) docReads() int64 { return c.lists.Load() + c.fetches.Load() }

// playlistFixture builds one playlist holding `n` videos, each a minute long.
func playlistFixture(t *testing.T, n int) (*countingDocs, *Server) {
	t.Helper()
	fake := ta.NewFake()
	entries := make([]ta.PlaylistEntry, 0, n)
	for i := range n {
		id := "v" + strconv.Itoa(i)
		v := video(id, "UC1", "2026-08-01", 60, false)
		v.Playlist = []string{"PL1"}
		fake.AddVideo(v)
		entries = append(entries, ta.PlaylistEntry{YoutubeID: id, Idx: i, Downloaded: true})
	}
	fake.Playlists["PL1"] = &ta.Playlist{
		PlaylistID: "PL1", PlaylistName: "List PL1", PlaylistType: "custom",
		PlaylistEntries: entries,
	}
	client := &countingDocs{Fake: fake}
	return client, newTestServer(client, newEventStore().querier())
}

func summaryOf(t *testing.T, s *Server, path string) PlaylistSummary {
	t.Helper()
	rec := do(t, s.Router(), http.MethodGet, path, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	page := decode[Page[PlaylistSummary]](t, rec)
	if len(page.Items) != 1 {
		t.Fatalf("got %d playlists, want 1", len(page.Items))
	}
	return page.Items[0]
}

// The point of the row: a playlist that has been summarised before is
// summarised again without reading a single video document.
func TestASecondPlaylistSummaryReadsNoDocuments(t *testing.T) {
	client, s := playlistFixture(t, 40)

	first := summaryOf(t, s, "/api/v1/playlists")
	if first.VideoCount != 40 || first.TotalDuration != 40*60 {
		t.Fatalf("count/duration = %d/%d, want 40/2400", first.VideoCount, first.TotalDuration)
	}
	if client.docReads() == 0 {
		t.Fatal("the first summary read nothing, so this test proves nothing")
	}
	before := client.docReads()

	second := summaryOf(t, s, "/api/v1/playlists")
	if n := client.docReads() - before; n != 0 {
		t.Errorf("the second summary read %d video documents, want none", n)
	}
	if second.VideoCount != first.VideoCount || second.TotalDuration != first.TotalDuration {
		t.Errorf("cached summary = %d/%d, want %d/%d",
			second.VideoCount, second.TotalDuration, first.VideoCount, first.TotalDuration)
	}
}

// A cached playlist must still report the *user's* state, which is not in the
// row and is read fresh every time.
func TestACachedPlaylistStillTracksWatching(t *testing.T) {
	_, s := playlistFixture(t, 4)

	summaryOf(t, s, "/api/v1/playlists") // warm the row
	do(t, s.Router(), http.MethodPost, "/api/v1/videos/v0/progress", `{"position":60,"duration":60,"completed":true}`)

	after := summaryOf(t, s, "/api/v1/playlists")
	if after.SeenCount != 1 {
		t.Errorf("seen = %d, want 1 — watch state must not come from the cache", after.SeenCount)
	}
	if after.ResumeVideoID == nil || *after.ResumeVideoID != "v1" {
		t.Errorf("resume = %v, want v1", after.ResumeVideoID)
	}
}

// The one staleness a viewer would notice: a video finishes downloading and
// belongs in the count now, not when a timer says so. The downloaded-entry
// count is the row's validity token, so this does not wait.
func TestANewlyDownloadedVideoCountsImmediately(t *testing.T) {
	client, s := playlistFixture(t, 3)

	if got := summaryOf(t, s, "/api/v1/playlists").VideoCount; got != 3 {
		t.Fatalf("count = %d, want 3", got)
	}

	v := video("v3", "UC1", "2026-08-01", 60, false)
	v.Playlist = []string{"PL1"}
	client.AddVideo(v)
	p := client.Playlists["PL1"]
	p.PlaylistEntries = append(p.PlaylistEntries, ta.PlaylistEntry{YoutubeID: "v3", Idx: 3, Downloaded: true})

	got := summaryOf(t, s, "/api/v1/playlists")
	if got.VideoCount != 4 {
		t.Errorf("count = %d, want 4 — a new download must not wait for the freshness window", got.VideoCount)
	}
	if got.TotalDuration != 4*60 {
		t.Errorf("duration = %d, want 240", got.TotalDuration)
	}
}

// The detail view is the one caller that genuinely needs the documents, and it
// still gets them — with the items in playlist order.
func TestPlaylistDetailStillReturnsItems(t *testing.T) {
	_, s := playlistFixture(t, 3)

	rec := do(t, s.Router(), http.MethodGet, "/api/v1/playlists/PL1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	detail := decode[PlaylistDetail](t, rec)
	if len(detail.Items) != 3 {
		t.Fatalf("got %d items, want 3", len(detail.Items))
	}
	for i, it := range detail.Items {
		if it.Position != i || it.Video.ID != "v"+strconv.Itoa(i) {
			t.Errorf("item %d = %s at %d, want v%d at %d", i, it.Video.ID, it.Position, i, i)
		}
	}
	if detail.Items[0].Video.Title == "" {
		t.Error("the detail view lost its titles")
	}
}
