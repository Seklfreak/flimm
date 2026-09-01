package api

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Seklfreak/flimm/internal/media"
	"github.com/Seklfreak/flimm/internal/sqlctest"
	"github.com/Seklfreak/flimm/internal/ta"
)

// prepareTestServer is a server whose ffmpeg writes whatever it is told to,
// with one video and one user.
func prepareTestServer(t *testing.T) (*Server, *media.Cache) {
	t.Helper()
	cache, err := media.NewCache(t.TempDir(), 0, 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cache.Close)

	client := ta.NewFake()
	client.Videos["v1"] = &ta.Video{
		YoutubeID: "v1", Title: "A video", MediaURL: "/youtube/UC1/v1.mp4",
		Channel: ta.Channel{ChannelID: "UC1"},
		Player:  ta.Player{Duration: 60},
		Streams: []ta.Stream{{Type: "video", Codec: "avc1", Height: 1080}},
	}
	client.Media = map[string][]byte{"/media/UC1/v1.mp4": []byte("source")}

	uid := uuid.New()
	q := &sqlctest.FakeQuerier{
		ListUserIDsFn: func(context.Context) ([]uuid.UUID, error) { return []uuid.UUID{uid}, nil },
	}
	srv := NewServer(Options{
		Querier: q, TA: client, MediaCache: cache, FFmpegPath: writeSheetFFmpeg(t),
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), AppName: "Flimm", MediaSecret: testSecret,
	})
	return srv, cache
}

// The whole point of the pause: a viewer's playback comes first, and the job
// only ever declines to *start* new work.
func TestPrepareStandsAsideWhilePlaying(t *testing.T) {
	srv, _ := prepareTestServer(t)

	if srv.playbackRecent() {
		t.Error("a server nobody has played anything on reads as busy")
	}
	srv.notePlayback()
	if !srv.playbackRecent() {
		t.Error("a heartbeat that just arrived does not read as playback")
	}

	// waitForQuiet must not return while playback is recent.
	done := make(chan bool, 1)
	go func() { done <- srv.waitForQuiet(t.Context()) }()
	select {
	case <-done:
		t.Error("the job carried on while a video was playing")
	case <-time.After(150 * time.Millisecond):
	}

	// A cancelled context releases it rather than leaving a goroutine waiting
	// out a video on a server that is shutting down.
	ctx, cancel := context.WithCancel(context.Background())
	go func() { done <- srv.waitForQuiet(ctx) }()
	cancel()
	select {
	case ok := <-done:
		if ok {
			t.Error("waitForQuiet said carry on after its context ended")
		}
	case <-time.After(2 * time.Second):
		t.Error("waitForQuiet ignored its context")
	}
}

// The status is what the UI shows, and "paused" has to be visible as its own
// thing: a bar that simply stops moving is the thing people file bugs about.
func TestPrepareStatusSaysPausedWhilePlaying(t *testing.T) {
	srv, _ := prepareTestServer(t)
	srv.prepare.set(func(st *PrepareStatus) { st.State, st.Done, st.Total = "running", 3, 10 })

	if got := srv.PrepareStatusOf().State; got != "running" {
		t.Errorf("state = %q, want running", got)
	}
	srv.notePlayback()
	if got := srv.PrepareStatusOf().State; got != "paused" {
		t.Errorf("state while playing = %q, want paused", got)
	}
	if got := srv.PrepareStatusOf().Total; got != 10 {
		t.Errorf("a paused pass lost its total: %d", got)
	}
}

// Preparing derives the two cheap entries and nothing else. A rendition is a
// thousand times the disk of a preview sheet, which is the entire reason this
// job is affordable.
func TestPrepareDerivesTheCheapEntriesOnly(t *testing.T) {
	srv, cache := prepareTestServer(t)
	v := ta.Video{
		YoutubeID: "v1", MediaURL: "/youtube/UC1/v1.mp4",
		Player: ta.Player{Duration: 60},
	}
	srv.prepareVideo(t.Context(), v)

	held := map[string]bool{}
	for _, name := range cacheEntryNames(t, cache) {
		variant, _ := media.EntryOf(name)
		held[variant] = true
	}
	if held[media.HLSVariant] {
		t.Error("preparing started a transcode; renditions are what this job exists not to make")
	}
	if !held[media.PreviewVariant] {
		t.Error("preparing did not derive the scrub-preview sheet")
	}
}

// A video already derived is not derived again: the pass runs every couple of
// hours over the same feed heads, and re-deriving would be a full decode per
// video per pass forever.
func TestPrepareSkipsWhatIsAlreadyThere(t *testing.T) {
	srv, cache := prepareTestServer(t)
	v := ta.Video{YoutubeID: "v1", MediaURL: "/youtube/UC1/v1.mp4", Player: ta.Player{Duration: 60}}

	srv.prepareVideo(t.Context(), v)
	first := entryModTime(t, cache, previewName("v1"))

	time.Sleep(20 * time.Millisecond)
	srv.prepareVideo(t.Context(), v)
	if again := entryModTime(t, cache, previewName("v1")); !again.Equal(first) {
		t.Error("the sheet was derived a second time; a full decode per pass is not a cache")
	}
}

// cacheEntryNames lists what the cache holds, for tests that care which
// variants a code path produced.
func cacheEntryNames(t *testing.T, c *media.Cache) []string {
	t.Helper()
	entries, err := os.ReadDir(c.Dir(""))
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func entryModTime(t *testing.T, c *media.Cache, name string) time.Time {
	t.Helper()
	st, err := os.Stat(filepath.Join(c.Dir(name), media.PreviewSheetName))
	if err != nil {
		t.Fatal(err)
	}
	return st.ModTime()
}
