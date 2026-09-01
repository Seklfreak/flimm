package api

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"

	"github.com/Seklfreak/flimm/internal/media"
	"github.com/Seklfreak/flimm/internal/sqlctest"
	"github.com/Seklfreak/flimm/internal/ta"
)

// cleanupServer is a server with a real media cache holding one entry per
// video named, and a fake querier the test drives.
func cleanupServer(t *testing.T, videos []string, finished, pinned []string, playlists map[string][]string) (*Server, *media.Cache) {
	t.Helper()
	cache, err := media.NewCache(t.TempDir(), 0, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cache.Close)
	for _, id := range videos {
		if _, err := cache.Get(t.Context(), media.HLSName(id, 720), func(_ context.Context, dst string) error {
			return os.WriteFile(dst, make([]byte, 100), 0o600)
		}); err != nil {
			t.Fatal(err)
		}
	}

	client := ta.NewFake()
	for id, entries := range playlists {
		p := &ta.Playlist{PlaylistID: id}
		for _, v := range entries {
			p.PlaylistEntries = append(p.PlaylistEntries, ta.PlaylistEntry{YoutubeID: v})
		}
		client.Playlists[id] = p
	}

	q := &sqlctest.FakeQuerier{
		ListFinishedVideosFn: func(_ context.Context, held []string) ([]string, error) {
			// Mirror the query: only ever answers about what it was asked.
			want := map[string]bool{}
			for _, id := range finished {
				want[id] = true
			}
			out := []string{}
			for _, id := range held {
				if want[id] {
					out = append(out, id)
				}
			}
			return out, nil
		},
		ListAllPinnedPlaylistsFn: func(context.Context) ([]string, error) { return pinned, nil },
	}
	srv := NewServer(Options{
		Querier:     q,
		TA:          client,
		Log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		AppName:     "Flimm",
		MediaSecret: testSecret,
		MediaCache:  cache,
	})
	return srv, cache
}

func held(t *testing.T, c *media.Cache) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, id := range c.Videos() {
		out[id] = true
	}
	return out
}

// The rule, in one test: a finished video's derivations go, an unfinished
// one's stay.
func TestCleanupRemovesFinishedVideosOnly(t *testing.T) {
	srv, cache := cleanupServer(t,
		[]string{"watched1111", "halfway2222", "untouched33"},
		[]string{"watched1111"}, nil, nil)

	srv.cleanupWatchedMedia(t.Context())

	got := held(t, cache)
	if got["watched1111"] {
		t.Error("a video everyone has finished kept its rendition")
	}
	if !got["halfway2222"] || !got["untouched33"] {
		t.Errorf("cleanup took a video nobody has finished: %v", got)
	}
}

// A pin says this list is worth keeping to hand, which is exactly the claim a
// rendition needs to survive being watched.
func TestCleanupKeepsPinnedPlaylists(t *testing.T) {
	srv, cache := cleanupServer(t,
		[]string{"pinned00001", "loose000002"},
		[]string{"pinned00001", "loose000002"},
		[]string{"PLpinned"},
		map[string][]string{"PLpinned": {"pinned00001"}},
	)

	srv.cleanupWatchedMedia(t.Context())

	got := held(t, cache)
	if !got["pinned00001"] {
		t.Error("a finished video in a pinned playlist lost its rendition")
	}
	if got["loose000002"] {
		t.Error("a finished video in no pinned playlist kept its rendition")
	}
}

// An unreachable TubeArchivist reads as "nothing is pinned", and acting on
// that would delete exactly what the pins existed to protect.
func TestCleanupDoesNothingWhenThePinsCannotBeRead(t *testing.T) {
	srv, cache := cleanupServer(t,
		[]string{"pinned00001"}, []string{"pinned00001"},
		[]string{"PLmissing"}, nil, // the playlist is not in the fake, so GetPlaylist fails
	)

	srv.cleanupWatchedMedia(t.Context())

	if !held(t, cache)["pinned00001"] {
		t.Error("cleanup deleted a rendition after failing to read the pins")
	}
}

// Nothing derived means nothing to ask the database about — the sweep runs on
// every server, including ones with an empty cache.
func TestCleanupAsksNothingWithAnEmptyCache(t *testing.T) {
	cache, err := media.NewCache(t.TempDir(), 0, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cache.Close)
	q := &sqlctest.FakeQuerier{
		ListFinishedVideosFn: func(context.Context, []string) ([]string, error) {
			t.Error("the database was asked about a cache holding nothing")
			return nil, nil
		},
	}
	srv := NewServer(Options{
		Querier: q, TA: ta.NewFake(), MediaCache: cache,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), AppName: "Flimm", MediaSecret: testSecret,
	})
	srv.cleanupWatchedMedia(t.Context())
}
