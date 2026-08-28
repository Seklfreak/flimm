package api

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Seklfreak/flimm/internal/ta"
)

// countingTA reports how many TA list requests a compose actually made.
type countingTA struct {
	*ta.Fake
	lists atomic.Int64
	pings atomic.Int64
}

func (c *countingTA) Ping(ctx context.Context) error {
	c.pings.Add(1)
	return c.Fake.Ping(ctx)
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

// A cursor exists to make a deep page cost what the first one did. If it is
// only a prettier offset, the whole exercise is pointless — so this measures.
func TestCursorPagingDoesNotReplayThePrefix(t *testing.T) {
	client := bigArchive(t, 1200)
	h := newTestServer(client, newEventStore().querier()).Router()

	// Walk out to the same place twice: once by offset, once by cursor.
	byOffset := decode[Page[VideoSummary]](t, do(t, h, http.MethodGet, "/api/v1/channels/A/videos?page=20&page_size=30", ""))
	offsetCost := client.lists.Swap(0)

	cursor := ""
	for range 20 {
		url := "/api/v1/channels/A/videos?page_size=30"
		if cursor != "" {
			url += "&cursor=" + cursor
		}
		p := decode[Page[VideoSummary]](t, do(t, h, http.MethodGet, url, ""))
		cursor = p.NextCursor
		if cursor == "" {
			t.Fatalf("ran out of cursors early")
		}
	}
	client.lists.Store(0)
	byCursor := decode[Page[VideoSummary]](t, do(t, h, http.MethodGet, "/api/v1/channels/A/videos?page_size=30&cursor="+cursor, ""))
	cursorCost := client.lists.Load()

	if got, want := ids(byCursor.Items), ids(byOffset.Items); !reflect.DeepEqual(got, want) {
		t.Errorf("cursor page = %v, offset page = %v", got, want)
	}
	// The offset walk reads every page before it; the cursor reads its own.
	if cursorCost*4 > offsetCost {
		t.Errorf("cursor cost %d TA requests, offset cost %d — the cursor is not saving the walk", cursorCost, offsetCost)
	}
}

// Following cursors to the end must show every video exactly once, in the same
// order offset paging gives.
func TestCursorPagingCoversEveryVideoExactlyOnce(t *testing.T) {
	client := bigArchive(t, 300)
	h := newTestServer(client, newEventStore().querier()).Router()

	var order []string
	seen := map[string]int{}
	cursor := ""
	for range 40 {
		url := "/api/v1/channels/A/videos?page_size=16"
		if cursor != "" {
			url += "&cursor=" + cursor
		}
		p := decode[Page[VideoSummary]](t, do(t, h, http.MethodGet, url, ""))
		for _, it := range p.Items {
			seen[it.ID]++
			order = append(order, it.ID)
		}
		if !p.HasMore {
			break
		}
		cursor = p.NextCursor
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
			t.Fatalf("cursor order diverges at %d: %s vs %s", i, order[i], it.ID)
		}
	}
}

// A cursor is meaningless outside the list it came from — positions line up
// with that list's streams by index. Serving it anyway would repeat or skip
// silently, so it is refused.
func TestCursorFromAnotherListIsRefused(t *testing.T) {
	client := bigArchive(t, 60)
	client.AddVideo(video("other1", "B", "2026-01-01", 60, false))
	h := newTestServer(client, newEventStore().querier()).Router()

	first := decode[Page[VideoSummary]](t, do(t, h, http.MethodGet, "/api/v1/channels/A/videos?page_size=10", ""))
	if first.NextCursor == "" {
		t.Fatal("no cursor to test with")
	}
	for _, url := range []string{
		"/api/v1/channels/B/videos?page_size=10&cursor=" + first.NextCursor,
		"/api/v1/channels/A/videos?page_size=10&view=unseen&cursor=" + first.NextCursor,
		"/api/v1/channels/A/videos?page_size=10&cursor=not-a-cursor",
	} {
		if rec := do(t, h, http.MethodGet, url, ""); rec.Code != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", url, rec.Code)
		}
	}
}
