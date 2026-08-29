package dearrow

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

const prefixResponse = `{
  "vid1": {
    "titles": [
      {"title": "A shouty title", "original": false, "votes": 5, "locked": false},
      {"title": "What the crowd settled on", "original": false, "votes": 1, "locked": true},
      {"title": "Rejected", "original": false, "votes": -3, "locked": false}
    ],
    "thumbnails": [
      {"timestamp": 12.5, "original": false, "votes": 2, "locked": false},
      {"timestamp": null, "original": true, "votes": 1, "locked": false}
    ],
    "randomTime": 0.25
  },
  "vid2": {"titles": [], "thumbnails": [], "randomTime": 0.5}
}`

func serve(t *testing.T, status int, body string) (*Client, *int) {
	t.Helper()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		// The id itself must never be on the wire — only a four-character
		// hash prefix, which is the whole reason this lookup is acceptable.
		if r.URL.Path != "/api/branding/"+HashPrefix("vid1") {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return New(Options{BaseURL: srv.URL}), &calls
}

func TestTheLockedSubmissionWinsOverVotes(t *testing.T) {
	c, _ := serve(t, http.StatusOK, prefixResponse)
	got, err := c.Branding(context.Background(), "vid1")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got.Title != "What the crowd settled on" {
		t.Errorf("title = %q, want the locked one", got.Title)
	}
	if got.ThumbnailTime == nil || *got.ThumbnailTime != 12.5 {
		t.Errorf("thumbnail = %v, want 12.5", got.ThumbnailTime)
	}
	if got.RandomTime != 0.25 {
		t.Errorf("randomTime = %v", got.RandomTime)
	}
}

// "Keep the original" is a decision the crowd can make, and it is not the same
// as having said nothing.
func TestAVoteForTheOriginalIsAnAnswer(t *testing.T) {
	body := `{"vid1": {"titles": [{"title": "x", "original": true, "votes": 9, "locked": false}],
	           "thumbnails": [{"timestamp": null, "original": true, "votes": 9, "locked": false}],
	           "randomTime": 0.1}}`
	c, _ := serve(t, http.StatusOK, body)
	got, _ := c.Branding(context.Background(), "vid1")
	if got.Title != "" || !got.OriginalTitleWon {
		t.Errorf("title = %q, originalWon = %v", got.Title, got.OriginalTitleWon)
	}
	if got.ThumbnailTime != nil || !got.OriginalThumbnailWon {
		t.Errorf("thumbnail = %v, originalWon = %v", got.ThumbnailTime, got.OriginalThumbnailWon)
	}
}

func TestAVideoNobodyHasTouchedIsNotAFailure(t *testing.T) {
	c, _ := serve(t, http.StatusOK, `{"vid2": {"titles": [], "thumbnails": [], "randomTime": 0.5}}`)
	got, err := c.Branding(context.Background(), "vid1")
	if err != nil {
		t.Fatalf("err = %v, want nil for a video with nothing", err)
	}
	if got.Title != "" || got.ThumbnailTime != nil {
		t.Errorf("branding = %+v, want empty", got)
	}
}

// An outage must not read as "nobody has retitled this": one is a reason to
// keep the archive's title *for now*, the other for good.
func TestAFailedLookupIsDistinctFromAnEmptyOne(t *testing.T) {
	c, calls := serve(t, http.StatusInternalServerError, "")
	if _, err := c.Branding(context.Background(), "vid1"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
	// ...and it is remembered, so a page of thirty videos does not make thirty
	// requests to a service that is down.
	if _, err := c.Branding(context.Background(), "vid1"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
	if *calls != 1 {
		t.Errorf("calls = %d, want the failure to be remembered", *calls)
	}
}

func TestAnAnswerIsCachedForItsTTL(t *testing.T) {
	c, calls := serve(t, http.StatusOK, prefixResponse)
	now := time.Now()
	c.now = func() time.Time { return now }

	for range 3 {
		if _, err := c.Branding(context.Background(), "vid1"); err != nil {
			t.Fatalf("err = %v", err)
		}
	}
	if *calls != 1 {
		t.Errorf("calls = %d, want 1", *calls)
	}
	now = now.Add(defaultTTL + time.Minute)
	if _, err := c.Branding(context.Background(), "vid1"); err != nil {
		t.Fatalf("err = %v", err)
	}
	if *calls != 2 {
		t.Errorf("calls = %d, want a refetch once the entry expired", *calls)
	}
}
