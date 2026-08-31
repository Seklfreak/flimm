package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/Seklfreak/flimm/internal/ta"
)

// countingChannels counts the per-channel lookups a request makes. That count
// is the whole subject: the lookups themselves were never slow, there were just
// hundreds of them — one request to /channels was traced making 429.
type countingChannels struct {
	*ta.Fake
	lookups atomic.Int64
}

func (c *countingChannels) ChannelStats(ctx context.Context, id string) (*ta.ChannelStats, error) {
	c.lookups.Add(1)
	return c.Fake.ChannelStats(ctx, id)
}

func (c *countingChannels) UnseenCount(ctx context.Context, id string) (int, error) {
	c.lookups.Add(1)
	return c.Fake.UnseenCount(ctx, id)
}

// channelServer holds `channels` channels, each with one video.
func channelServer(t *testing.T, channels int) (*countingChannels, *Server) {
	t.Helper()
	fake := ta.NewFake()
	for i := range channels {
		id := "UC" + strconv.Itoa(i)
		v := video("v"+strconv.Itoa(i), id, "2026-08-01", 100, false)
		fake.AddVideo(v)
	}
	client := &countingChannels{Fake: fake}
	return client, NewServer(Options{
		Querier:     newEventStore().querier(),
		TA:          client,
		Log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		AppName:     "Flimm",
		MediaSecret: testSecret,
	})
}

// The bug this exists for: one request to /channels was traced making 429
// queries, because every channel in the archive was counted before the list was
// cut to a page of thirty.
func TestListingChannelsOnlyCountsThePageItShows(t *testing.T) {
	client, s := channelServer(t, 60)

	rec := do(t, s.Router(), http.MethodGet, "/api/v1/channels?page_size=10", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	page := decode[Page[ChannelSummary]](t, rec)
	if len(page.Items) != 10 {
		t.Fatalf("returned %d channels, want a page of 10", len(page.Items))
	}
	// Two lookups per channel shown — a count and an unseen count — not two per
	// channel in the archive.
	if n := client.lookups.Load(); n > 20 {
		t.Errorf("made %d lookups for a page of 10 channels, want at most 20", n)
	}
}

// Sorting by a count cannot be done without the counts, so that order still
// pays for them — the difference is that it is now the only order that does.
func TestSortingByACountStillCountsEverything(t *testing.T) {
	client, s := channelServer(t, 20)

	rec := do(t, s.Router(), http.MethodGet, "/api/v1/channels?sort=unseen&page_size=5", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if n := client.lookups.Load(); n < 20 {
		t.Errorf("made %d lookups, want every channel counted for a count-ordered list", n)
	}
}

// The second page of the same list costs nothing: the counts are in the cache
// by then, and a cache that dies with the process is what made a restart
// expensive.
func TestASecondPageReadsTheCountsFromTheCache(t *testing.T) {
	client, s := channelServer(t, 40)

	do(t, s.Router(), http.MethodGet, "/api/v1/channels?page_size=10", "")
	first := client.lookups.Load()
	do(t, s.Router(), http.MethodGet, "/api/v1/channels?page_size=10", "")
	second := client.lookups.Load() - first

	if second != 0 {
		t.Errorf("the second identical request made %d more lookups, want none", second)
	}
}
