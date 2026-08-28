package api

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Seklfreak/flimm/internal/ta"
)

// countingTA reports how many TA list requests a compose actually made.
type countingTA struct {
	*ta.Fake
	lists atomic.Int64
}

func (c *countingTA) ListVideos(ctx context.Context, q ta.VideoQuery) (*ta.VideoPage, error) {
	c.lists.Add(1)
	return c.Fake.ListVideos(ctx, q)
}

func bigArchive(t *testing.T, n int) *countingTA {
	t.Helper()
	fake := ta.NewFake()
	fake.PageSizeCap = 12 // what a real TubeArchivist does
	day := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range n {
		// Strictly descending dates, so "newest" order is exactly v0000, v0001, …
		fake.AddVideo(video(fmt.Sprintf("v%04d", i), "A", day.AddDate(0, 0, -i).Format("2006-01-02"), float64(600+i), false))
	}
	return &countingTA{Fake: fake}
}

// A list must not be bounded by how much the server is willing to hold in
// memory. Composition is lazy: a page reads the TA pages it needs and stops.
func TestListsPageBeyondTheOldComposeCap(t *testing.T) {
	client := bigArchive(t, 1200)
	h := newTestServer(client, newEventStore().querier()).Router()

	first := decode[Page[VideoSummary]](t, do(t, h, http.MethodGet, "/api/v1/channels/A/videos?page=0&page_size=30", ""))
	if len(first.Items) != 30 || !first.HasMore {
		t.Fatalf("page 0 = %d items, has_more %v", len(first.Items), first.HasMore)
	}
	// 31 items at 12 rows per TA page is three requests; anything close to
	// 1200/12 means the whole archive was walked to serve one page.
	if n := client.lists.Load(); n > 6 {
		t.Errorf("page 0 made %d TA requests, want a handful", n)
	}

	// Well past the old 500-video ceiling.
	deep := decode[Page[VideoSummary]](t, do(t, h, http.MethodGet, "/api/v1/channels/A/videos?page=25&page_size=30", ""))
	if len(deep.Items) != 30 || !deep.HasMore {
		t.Fatalf("page 25 = %d items, has_more %v", len(deep.Items), deep.HasMore)
	}
	if got, want := deep.Items[0].ID, "v0750"; got != want {
		t.Errorf("page 25 starts at %s, want %s", got, want)
	}

	// And the end of the archive is reachable, with an exact total there.
	last := decode[Page[VideoSummary]](t, do(t, h, http.MethodGet, "/api/v1/channels/A/videos?page=39&page_size=30", ""))
	if last.HasMore || last.Total != 1200 || len(last.Items) != 30 {
		t.Errorf("last page = %d items, total %d, has_more %v", len(last.Items), last.Total, last.HasMore)
	}
}

// Every page must be a clean cut of one order: the lazy merge and the final
// sort have to agree, or a window boundary drops or repeats a video.
func TestPagingCoversEveryVideoExactlyOnce(t *testing.T) {
	client := bigArchive(t, 300)
	h := newTestServer(client, newEventStore().querier()).Router()

	seen := map[string]int{}
	var order []string
	for page := 0; page < 20; page++ {
		p := decode[Page[VideoSummary]](t, do(t, h,
			http.MethodGet, fmt.Sprintf("/api/v1/channels/A/videos?page=%d&page_size=16", page), ""))
		for _, it := range p.Items {
			seen[it.ID]++
			order = append(order, it.ID)
		}
		if !p.HasMore {
			break
		}
	}
	if len(seen) != 300 {
		t.Errorf("saw %d distinct videos, want 300", len(seen))
	}
	for id, n := range seen {
		if n != 1 {
			t.Errorf("%s appeared %d times", id, n)
		}
	}
	whole := decode[Page[VideoSummary]](t, do(t, h, http.MethodGet, "/api/v1/channels/A/videos?page=0&page_size=100", ""))
	for i, it := range whole.Items {
		if order[i] != it.ID {
			t.Fatalf("paged order diverges at %d: %s vs %s", i, order[i], it.ID)
		}
	}
}
