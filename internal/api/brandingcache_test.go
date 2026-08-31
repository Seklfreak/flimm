package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Seklfreak/flimm/internal/db/sqlc"
	"github.com/Seklfreak/flimm/internal/dearrow"
	"github.com/Seklfreak/flimm/internal/sqlctest"
	"github.com/Seklfreak/flimm/internal/ta"
)

// brandingStore is the cache table, with a count of the lookups the service
// was actually asked for — which is the number this whole file exists to keep
// at zero on a warm page.
type brandingStore struct {
	rows     map[string]sqlc.DearrowBranding
	lookups  atomic.Int64
	upserted atomic.Int64
}

func (b *brandingStore) querier() *sqlctest.FakeQuerier {
	return &sqlctest.FakeQuerier{
		ListBrandingFn: func(_ context.Context, ids []string) ([]sqlc.DearrowBranding, error) {
			var out []sqlc.DearrowBranding
			for _, id := range ids {
				if row, ok := b.rows[id]; ok {
					out = append(out, row)
				}
			}
			return out, nil
		},
		UpsertBrandingFn: func(_ context.Context, arg sqlc.UpsertBrandingParams) error {
			b.upserted.Add(1)
			b.rows[arg.VideoID] = sqlc.DearrowBranding{
				VideoID: arg.VideoID, Title: arg.Title, HasSubmission: arg.HasSubmission,
				FetchedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
			}
			return nil
		},
	}
}

// brandingServer is a server whose DeArrow client answers from a stub, so the
// test can count what reached the network.
func brandingServer(t *testing.T, store *brandingStore, title string, delay time.Duration) *Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		store.lookups.Add(1)
		time.Sleep(delay)
		// The response is keyed by video id; ids are unknown here, so answer
		// for every id the test uses.
		_, _ = w.Write([]byte(`{"v1":{"titles":[{"title":"` + title + `","original":false,"votes":3,"locked":false}],"thumbnails":[],"randomTime":0.25},` +
			`"v2":{"titles":[{"title":"` + title + `","original":false,"votes":3,"locked":false}],"thumbnails":[],"randomTime":0.25}}`))
	}))
	t.Cleanup(srv.Close)

	return NewServer(Options{
		Querier:     store.querier(),
		TA:          ta.NewFake(),
		Log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		AppName:     "Flimm",
		MediaSecret: testSecret,
		DeArrow:     dearrow.New(dearrow.Options{BaseURL: srv.URL}),
	})
}

func brandingPrefs() Prefs { return Prefs{DeArrowTitles: dearrowManual} }

// The point of the table: a page that has been served before touches nothing.
func TestAKnownVideoIsBrandedWithoutAskingAnyone(t *testing.T) {
	store := &brandingStore{rows: map[string]sqlc.DearrowBranding{
		"v1": {
			VideoID: "v1", Title: "What it is actually about", HasSubmission: true,
			FetchedAt: pgtype.Timestamptz{Time: time.Now().Add(-time.Hour), Valid: true},
		},
	}}
	s := brandingServer(t, store, "from the service", 0)

	items := []VideoSummary{{ID: "v1", Title: "WATCH THIS NOW"}}
	s.applyBranding(context.Background(), brandingPrefs(), items)

	if items[0].Title != "What it is actually about" {
		t.Errorf("title = %q, want the cached one", items[0].Title)
	}
	if n := store.lookups.Load(); n != 0 {
		t.Errorf("made %d lookups for a cached video, want none", n)
	}
}

// A row past its freshness window is still served immediately; the refresh
// happens behind the response. The viewer waits for nothing.
func TestAStaleRowIsServedNowAndRefreshedAfter(t *testing.T) {
	store := &brandingStore{rows: map[string]sqlc.DearrowBranding{
		"v1": {
			VideoID: "v1", Title: "an old crowd title", HasSubmission: true,
			FetchedAt: pgtype.Timestamptz{Time: time.Now().Add(-30 * 24 * time.Hour), Valid: true},
		},
	}}
	s := brandingServer(t, store, "from the service", 0)

	items := []VideoSummary{{ID: "v1", Title: "WATCH THIS NOW"}}
	s.applyBranding(context.Background(), brandingPrefs(), items)

	if items[0].Title != "an old crowd title" {
		t.Errorf("title = %q, want the stale row served as-is", items[0].Title)
	}
	if n := store.lookups.Load(); n != 0 {
		t.Errorf("a stale row cost %d lookups inside the request, want none", n)
	}
	if len(s.brandingQueue) != 1 {
		t.Errorf("queued %d refreshes, want 1", len(s.brandingQueue))
	}
}

// The one case that waits: a video nothing is known about. It is also the only
// case that writes a row, which is what stops it happening twice.
func TestAnUnknownVideoIsFetchedOnceAndRemembered(t *testing.T) {
	store := &brandingStore{rows: map[string]sqlc.DearrowBranding{}}
	s := brandingServer(t, store, "What it is actually about", 0)

	items := []VideoSummary{{ID: "v1", Title: "WATCH THIS NOW"}}
	s.applyBranding(context.Background(), brandingPrefs(), items)

	if items[0].Title != "What it is actually about" {
		t.Errorf("title = %q, want the fetched one", items[0].Title)
	}
	if n := store.lookups.Load(); n != 1 {
		t.Fatalf("made %d lookups, want 1", n)
	}
	if store.upserted.Load() == 0 {
		t.Fatal("the answer was not written to the cache")
	}

	// Second time round: cached, so nothing is asked of anyone.
	again := []VideoSummary{{ID: "v1", Title: "WATCH THIS NOW"}}
	s.applyBranding(context.Background(), brandingPrefs(), again)
	if n := store.lookups.Load(); n != 1 {
		t.Errorf("made %d lookups in total, want the second page served from cache", n)
	}
}

// A service having a bad minute must not hold a page. The archive's own title
// goes out and the lookup is left to the background.
func TestASlowServiceDoesNotHoldThePage(t *testing.T) {
	store := &brandingStore{rows: map[string]sqlc.DearrowBranding{}}
	s := brandingServer(t, store, "too late", 2*time.Second)
	// The real deadline is seconds; this test's patience is shorter.
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	items := []VideoSummary{{ID: "v1", Title: "WATCH THIS NOW"}}
	start := time.Now()
	s.applyBranding(ctx, brandingPrefs(), items)

	if took := time.Since(start); took > time.Second {
		t.Errorf("the page waited %v", took)
	}
	if items[0].Title != "WATCH THIS NOW" {
		t.Errorf("title = %q, want the archive's own", items[0].Title)
	}
	if len(s.brandingQueue) != 1 {
		t.Errorf("queued %d background lookups, want the missed one", len(s.brandingQueue))
	}
}

// The freshness windows differ by answer, and by an order of magnitude: "nobody
// submitted anything" is most of the table and the least likely to change.
func TestNothingSubmittedStaysFreshLonger(t *testing.T) {
	old := time.Now().Add(-3 * 24 * time.Hour)
	submitted := brandingRecord{submission: true, fetchedAt: old}
	empty := brandingRecord{submission: false, fetchedAt: old}

	if submitted.fresh(time.Now()) {
		t.Error("a three-day-old submission should be refreshed")
	}
	if !empty.fresh(time.Now()) {
		t.Error("a three-day-old empty answer should still be served")
	}
}

// DeArrow suggests a frame for every video it has heard of, so that alone is
// not a submission — counting it would put the whole archive on the short
// freshness window for nothing.
func TestAGeneratedSuggestionIsNotASubmission(t *testing.T) {
	if hasSubmission(dearrow.Branding{RandomTime: 0.25}) {
		t.Error("a random-time suggestion counted as a submission")
	}
	if !hasSubmission(dearrow.Branding{Title: "something"}) {
		t.Error("a submitted title did not count")
	}
	if !hasSubmission(dearrow.Branding{OriginalTitleWon: true}) {
		t.Error("a vote for the original is a decision, and counts")
	}
}
