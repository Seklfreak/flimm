// Package dearrow reads crowd-sourced titles and thumbnails from DeArrow —
// the same project as SponsorBlock, and by default the same server.
//
// It exists for the same reason the sponsorblock package does: the archive
// carries whatever the uploader called the video and whatever frame YouTube
// chose, and a viewer may prefer what the crowd made of both. And it asks the
// same way — the first four hex characters of sha256(videoID), never the id
// itself — so the service learns that *someone* is watching one of a few
// hundred videos, and no more than that.
//
// What this package does *not* do is decide. It returns what the crowd said;
// which of it a viewer wants — crowd submissions only, or a machine's guess
// as well — is a preference, applied by the API layer.
package dearrow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Seklfreak/flimm/internal/obs"
)

// DefaultBaseURL is the public DeArrow server, which is also SponsorBlock's.
const DefaultBaseURL = "https://sponsor.ajay.app"

// ErrUnavailable is returned while a recent lookup failure is still
// remembered, so a caller keeps showing the archive's own title rather than
// reading "nobody has retitled this" out of an outage.
var ErrUnavailable = errors.New("dearrow unavailable")

const (
	hashPrefixLen  = 4
	maxBody        = 8 << 20
	defaultTTL     = 6 * time.Hour
	failureTTL     = 2 * time.Minute
	defaultTimeout = 5 * time.Second
	maxEntries     = 4096
)

// Branding is what the crowd has for one video.
//
// Both halves are independent: a video can have a title and no thumbnail, or
// the other way round, and a viewer can want one and not the other.
type Branding struct {
	// Title the crowd settled on. Empty when there is none, or when what won
	// was a vote for the original — which is a *decision*, not an absence, and
	// is why OriginalTitleWon exists.
	Title string
	// OriginalTitleWon reports that the submission with the most weight was
	// "the original title is fine". A viewer asking for crowd titles gets the
	// archive's own, and nothing should second-guess it.
	OriginalTitleWon bool
	// ThumbnailTime is the second of the video the crowd picked as a
	// thumbnail. Nil when there is no submission, or when the winning one was
	// a vote for the video's own thumbnail (OriginalThumbnailWon).
	ThumbnailTime        *float64
	OriginalThumbnailWon bool
	// RandomTime is DeArrow's own suggestion, as a fraction of the video: the
	// frame it would show when nobody has submitted one. This is the "auto"
	// half of thumbnails, and it is only ever used when a viewer asks for it.
	RandomTime float64
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

// Client looks branding up, with a cache in front.
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
	branding Branding
	// failed marks a remembered lookup failure, which is not the same as "no
	// one has retitled this video": one is a reason to keep asking later, the
	// other is an answer.
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
		// Traced, so a slow lookup shows up as a span on the request that
		// waited for it rather than as unexplained time (see obs.Transport).
		httpClient = &http.Client{Timeout: timeout, Transport: obs.Transport{}}
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

// HashPrefix is what the lookup sends instead of the video id.
func HashPrefix(videoID string) string {
	sum := sha256.Sum256([]byte(videoID))
	return hex.EncodeToString(sum[:])[:hashPrefixLen]
}

// Branding returns what the crowd has for a video. A video nobody has touched
// is a zero Branding and a nil error; only a lookup that actually failed
// returns one.
func (c *Client) Branding(ctx context.Context, videoID string) (Branding, error) {
	if videoID == "" {
		return Branding{}, nil
	}
	if e, ok := c.cached(videoID); ok {
		if e.failed {
			return Branding{}, ErrUnavailable
		}
		return e.branding, nil
	}
	b, err := c.fetch(ctx, videoID)
	if err != nil {
		c.put(videoID, entry{failed: true}, failureTTL)
		c.log.Debug("dearrow lookup failed", "err", err)
		return Branding{}, ErrUnavailable
	}
	c.put(videoID, entry{branding: b}, c.ttl)
	return b, nil
}

type apiVideo struct {
	Titles     []apiTitle     `json:"titles"`
	Thumbnails []apiThumbnail `json:"thumbnails"`
	RandomTime float64        `json:"randomTime"`
}

type apiTitle struct {
	Title    string `json:"title"`
	Original bool   `json:"original"`
	Votes    int    `json:"votes"`
	Locked   bool   `json:"locked"`
}

type apiThumbnail struct {
	Timestamp *float64 `json:"timestamp"`
	Original  bool     `json:"original"`
	Votes     int      `json:"votes"`
	Locked    bool     `json:"locked"`
}

func (c *Client) fetch(ctx context.Context, videoID string) (Branding, error) {
	endpoint := c.base + "/api/branding/" + HashPrefix(videoID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Branding{}, fmt.Errorf("dearrow request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	resp, err := c.http.Do(req)
	if err != nil {
		return Branding{}, fmt.Errorf("dearrow lookup: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		// Nothing in this prefix — an answer, not a failure.
		return Branding{}, nil
	case resp.StatusCode != http.StatusOK:
		return Branding{}, fmt.Errorf("dearrow lookup: unexpected status %d", resp.StatusCode)
	}
	// The response is keyed by video id: every video sharing the prefix, which
	// is what keeps the id itself off the wire.
	var byID map[string]apiVideo
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&byID); err != nil {
		return Branding{}, fmt.Errorf("dearrow decode: %w", err)
	}
	v, ok := byID[videoID]
	if !ok {
		return Branding{}, nil
	}
	return brandingOf(v), nil
}

// brandingOf picks the winning submission of each kind.
//
// DeArrow's own rule, and the only one that makes sense for crowd data: a
// locked submission (a moderator's decision) beats everything, and otherwise
// the most-voted one wins. A submission on negative votes has been rejected by
// the people who looked at it and is never shown.
func brandingOf(v apiVideo) Branding {
	out := Branding{RandomTime: v.RandomTime}

	if best, ok := bestOf(len(v.Titles), func(i int) (bool, int) {
		return v.Titles[i].Locked, v.Titles[i].Votes
	}); ok {
		title := v.Titles[best]
		if title.Original {
			out.OriginalTitleWon = true
		} else {
			out.Title = strings.TrimSpace(title.Title)
		}
	}

	if best, ok := bestOf(len(v.Thumbnails), func(i int) (bool, int) {
		return v.Thumbnails[i].Locked, v.Thumbnails[i].Votes
	}); ok {
		thumb := v.Thumbnails[best]
		switch {
		case thumb.Original || thumb.Timestamp == nil:
			out.OriginalThumbnailWon = true
		case *thumb.Timestamp >= 0 && *thumb.Timestamp < 1e6:
			t := *thumb.Timestamp
			out.ThumbnailTime = &t
		}
	}
	return out
}

// bestOf returns the index of the winner among n submissions: locked first,
// then most votes, and nothing at all when every one of them is in the
// negative.
func bestOf(n int, at func(i int) (locked bool, votes int)) (int, bool) {
	best, found := -1, false
	bestLocked, bestVotes := false, 0
	for i := range n {
		locked, votes := at(i)
		if votes < 0 && !locked {
			continue
		}
		switch {
		case !found, locked && !bestLocked, locked == bestLocked && votes > bestVotes:
			best, bestLocked, bestVotes, found = i, locked, votes, true
		}
	}
	return best, found
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
