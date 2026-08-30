// Package ryd reads a video's vote counts from Return YouTube Dislike.
//
// YouTube stopped publishing dislike counts in 2021, and TubeArchivist indexes
// what YouTube publishes: `view_count` and `like_count`, never a dislike. The
// number still exists — Return YouTube Dislike kept archiving it, and estimates
// it from its extension's users afterwards — so a video's second half is here
// or nowhere.
//
// # It is off by default, and that is not caution for its own sake
//
// SponsorBlock and DeArrow are asked for a *hash prefix* of the video id: they
// learn that someone is watching one of a few hundred videos, and no more. This
// service has no such endpoint. Its API takes the bare id, so with it on, every
// video detail view tells a third party exactly what is being watched, from the
// server's address. That is a real cost, and it belongs to whoever runs the
// deployment rather than to a default — so `RYD_URL` is empty unless someone
// sets it (see deploy.md), and nothing here is reached until they do.
//
// # What it does not do
//
// It reports what the service said. Whether that beats the archive's own like
// count, and what a client shows, is the API layer's decision.
package ryd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// DefaultBaseURL is the public Return YouTube Dislike API. It is a default for
// the *value* of RYD_URL, never for whether the integration runs at all.
const DefaultBaseURL = "https://returnyoutubedislikeapi.com"

// ErrUnavailable is returned while a recent lookup failure is still remembered,
// so a caller shows the archive's own numbers rather than reading "this video
// has no dislikes" out of an outage.
var ErrUnavailable = errors.New("return youtube dislike unavailable")

const (
	maxBody        = 1 << 20
	defaultTTL     = 6 * time.Hour
	failureTTL     = 2 * time.Minute
	defaultTimeout = 5 * time.Second
	maxEntries     = 4096
)

// Votes is what the service has for one video.
//
// Both counts are estimates after 2021 — the service extrapolates from its own
// users once YouTube stops answering — which is why they travel together: a
// dislike count is only meaningful beside the like count it was measured
// against.
type Votes struct {
	Likes    int64
	Dislikes int64
	// Views the service last recorded. Its vintage is its own — see the API
	// layer, which takes the larger of this and the archive's.
	Views int64
	// Found reports that the service knows this video at all. A video it has
	// never seen is zero votes and Found false, which is not the same as a
	// video that genuinely has none.
	Found bool
}

// Options configure a Client.
type Options struct {
	BaseURL    string
	HTTPClient *http.Client
	Timeout    time.Duration
	TTL        time.Duration
	UserAgent  string
	Log        *slog.Logger
}

// Client looks votes up, with a cache in front.
type Client struct {
	base      string
	http      *http.Client
	ttl       time.Duration
	userAgent string
	log       *slog.Logger

	mu sync.Mutex
	m  map[string]entry
	// now is time.Now; replaced in tests.
	now func() time.Time
}

type entry struct {
	votes Votes
	// failed marks a remembered lookup failure, which is not the same as "the
	// service does not know this video": one is a reason to keep asking later,
	// the other is an answer.
	failed bool
	exp    time.Time
}

// New builds a Client. It performs no I/O.
func New(o Options) *Client {
	base := strings.TrimRight(o.BaseURL, "/")
	if base == "" {
		base = DefaultBaseURL
	}
	httpClient := o.HTTPClient
	if httpClient == nil {
		timeout := o.Timeout
		if timeout <= 0 {
			timeout = defaultTimeout
		}
		httpClient = &http.Client{Timeout: timeout}
	}
	ttl := o.TTL
	if ttl <= 0 {
		ttl = defaultTTL
	}
	log := o.Log
	if log == nil {
		log = slog.Default()
	}
	ua := o.UserAgent
	if ua == "" {
		ua = "flimm"
	}
	return &Client{
		base:      base,
		http:      httpClient,
		ttl:       ttl,
		userAgent: ua,
		log:       log,
		m:         map[string]entry{},
		now:       time.Now,
	}
}

// Votes returns what the service has for a video. A video it has never seen is
// a zero Votes with Found false and a nil error; only a lookup that actually
// failed returns one.
func (c *Client) Votes(ctx context.Context, videoID string) (Votes, error) {
	if videoID == "" {
		return Votes{}, nil
	}
	if e, ok := c.cached(videoID); ok {
		if e.failed {
			return Votes{}, ErrUnavailable
		}
		return e.votes, nil
	}
	v, err := c.fetch(ctx, videoID)
	if err != nil {
		c.put(videoID, entry{failed: true}, failureTTL)
		c.log.Debug("return youtube dislike lookup failed", "err", err)
		return Votes{}, ErrUnavailable
	}
	c.put(videoID, entry{votes: v}, c.ttl)
	return v, nil
}

type apiVotes struct {
	ID        string `json:"id"`
	Likes     int64  `json:"likes"`
	Dislikes  int64  `json:"dislikes"`
	ViewCount int64  `json:"viewCount"`
	// Deleted marks a video YouTube no longer has. Its last known counts are
	// still the right ones to show for an archived copy.
	Deleted bool `json:"deleted"`
}

func (c *Client) fetch(ctx context.Context, videoID string) (Votes, error) {
	endpoint := c.base + "/votes?videoId=" + url.QueryEscape(videoID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Votes{}, fmt.Errorf("ryd request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	resp, err := c.http.Do(req)
	if err != nil {
		return Votes{}, fmt.Errorf("ryd lookup: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		// A video the service has never seen — an answer, not a failure, and
		// one worth caching for as long as any other.
		return Votes{}, nil
	case resp.StatusCode != http.StatusOK:
		return Votes{}, fmt.Errorf("ryd lookup: unexpected status %d", resp.StatusCode)
	}
	var out apiVotes
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&out); err != nil {
		return Votes{}, fmt.Errorf("ryd decode: %w", err)
	}
	// Negative counts are not a thing; a service having a bad day does not get
	// to render as -1 dislikes on a TV.
	return Votes{
		Likes:    max(out.Likes, 0),
		Dislikes: max(out.Dislikes, 0),
		Views:    max(out.ViewCount, 0),
		Found:    true,
	}, nil
}

// ---- cache ----

func (c *Client) cached(videoID string) (entry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[videoID]
	if !ok || c.now().After(e.exp) {
		return entry{}, false
	}
	return e, true
}

func (c *Client) put(videoID string, e entry, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e.exp = c.now().Add(ttl)
	c.m[videoID] = e
	c.evictLocked()
}

// evictLocked keeps the cache bounded. Expiry is checked on read, so this only
// has to stop an unbounded archive from filling memory; dropping the whole map
// is cheaper than tracking an order for something this cheap to refetch.
func (c *Client) evictLocked() {
	if len(c.m) <= maxEntries {
		return
	}
	c.m = map[string]entry{}
}
