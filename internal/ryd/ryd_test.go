package ryd

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

const votesResponse = `{
  "id": "vid1",
  "dateCreated": "2022-01-31T12:00:00.000Z",
  "likes": 45120,
  "dislikes": 1183,
  "rating": 4.8,
  "viewCount": 1200000,
  "deleted": false
}`

func serve(t *testing.T, status int, body string) (*Client, *int) {
	t.Helper()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/votes" {
			t.Errorf("path = %q", r.URL.Path)
		}
		// Unlike SponsorBlock and DeArrow, this service is asked about a video
		// by name. The test states it because it is the reason the whole
		// integration is off by default.
		if got := r.URL.Query().Get("videoId"); got != "vid1" {
			t.Errorf("videoId = %q", got)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return New(Options{BaseURL: srv.URL}), &calls
}

func TestVotesComeBackAsAPair(t *testing.T) {
	c, _ := serve(t, http.StatusOK, votesResponse)
	got, err := c.Votes(context.Background(), "vid1")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got.Likes != 45120 || got.Dislikes != 1183 {
		t.Errorf("votes = %+v, want 45120/1183", got)
	}
	if got.Views != 1_200_000 {
		t.Errorf("views = %d, want 1200000", got.Views)
	}
	if !got.Found {
		t.Error("Found = false for a video the service knows")
	}
}

// A video the service has never seen is an answer. Nothing should show up as
// "0 dislikes" for it, which is what Found is for.
func TestAnUnknownVideoIsAnAnswerNotAFailure(t *testing.T) {
	c, calls := serve(t, http.StatusNotFound, `{"error":"not found"}`)
	got, err := c.Votes(context.Background(), "vid1")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got.Found {
		t.Errorf("Found = true for %+v", got)
	}
	// Cached like any other answer, rather than asked again on every view.
	if _, err := c.Votes(context.Background(), "vid1"); err != nil {
		t.Fatalf("err = %v", err)
	}
	if *calls != 1 {
		t.Errorf("calls = %d, want the answer cached", *calls)
	}
}

func TestAnAnswerIsCachedUntilItExpires(t *testing.T) {
	c, calls := serve(t, http.StatusOK, votesResponse)
	now := time.Now()
	c.now = func() time.Time { return now }

	for range 3 {
		if _, err := c.Votes(context.Background(), "vid1"); err != nil {
			t.Fatalf("err = %v", err)
		}
	}
	if *calls != 1 {
		t.Fatalf("calls = %d, want 1", *calls)
	}

	now = now.Add(defaultTTL + time.Minute)
	if _, err := c.Votes(context.Background(), "vid1"); err != nil {
		t.Fatalf("err = %v", err)
	}
	if *calls != 2 {
		t.Errorf("calls = %d, want the entry to have expired", *calls)
	}
}

// An outage must not read as "this video has no dislikes", and must not put a
// failing third party in front of every video detail either.
func TestAFailureIsRememberedBriefly(t *testing.T) {
	c, calls := serve(t, http.StatusInternalServerError, "")
	now := time.Now()
	c.now = func() time.Time { return now }

	if _, err := c.Votes(context.Background(), "vid1"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
	if _, err := c.Votes(context.Background(), "vid1"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
	if *calls != 1 {
		t.Errorf("calls = %d, want the failure remembered", *calls)
	}

	// Briefly: the service coming back must not wait for the six-hour TTL.
	now = now.Add(failureTTL + time.Second)
	if _, err := c.Votes(context.Background(), "vid1"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
	if *calls != 2 {
		t.Errorf("calls = %d, want a retry after the failure TTL", *calls)
	}
}

// A service having a bad day does not get to render as -1 dislikes on a TV.
func TestNegativeCountsAreFloored(t *testing.T) {
	c, _ := serve(t, http.StatusOK, `{"id":"vid1","likes":-2,"dislikes":-7}`)
	got, err := c.Votes(context.Background(), "vid1")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got.Likes != 0 || got.Dislikes != 0 {
		t.Errorf("votes = %+v, want both floored to 0", got)
	}
}
