// Package sponsorblock reads crowd-sourced segments from a SponsorBlock
// server (sponsor.ajay.app by default) instead of relying on the snapshot
// TubeArchivist stored at download time.
//
// The lookup never tells the service which video is playing: it sends the
// first four hex characters of sha256(videoID), gets every video sharing that
// prefix back, and picks ours out locally. Results (including "this video has
// no segments") are cached, so a browsing session makes one request per video
// at most per TTL.
package sponsorblock

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
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

// DefaultBaseURL is the public SponsorBlock server.
const DefaultBaseURL = "https://sponsor.ajay.app"

// ErrUnavailable is returned while a recent lookup failure is still
// remembered, so a caller keeps falling back for as long as the service is
// down instead of reading "no segments" out of an outage.
var ErrUnavailable = errors.New("sponsorblock unavailable")

// Action types. A client may only *skip* ActionSkip segments; ActionMute is
// muted instead, ActionPOI is a single point of interest ("the highlight")
// and ActionFull labels the whole video rather than a range of it.
const (
	ActionSkip    = "skip"
	ActionMute    = "mute"
	ActionPOI     = "poi"
	ActionFull    = "full"
	ActionChapter = "chapter"
)

// CategoryChapter carries a crowd-sourced chapter name in Segment.Description.
const CategoryChapter = "chapter"

// The categories that mark a *range* of a video, which is to say the ones a
// viewer can have an opinion about: skip it, be offered it, or leave it alone.
// `poi_highlight` is deliberately absent — it marks an instant, and is only
// ever offered — as is `chapter`, which is a name rather than an action.
const (
	CategorySponsor     = "sponsor"
	CategorySelfPromo   = "selfpromo"
	CategoryInteraction = "interaction"
	CategoryIntro       = "intro"
	CategoryOutro       = "outro"
	CategoryPreview     = "preview"
	CategoryMusicOff    = "music_offtopic"
	CategoryFiller      = "filler"
	CategoryExclusive   = "exclusive_access"
)

// DefaultCategories is everything the service offers. Flimm asks for all of
// it and lets each client decide what to do with a category: the alternative
// is a server-side allowlist that clients cannot see past.
var DefaultCategories = []string{
	"sponsor",
	"selfpromo",
	"interaction",
	"intro",
	"outro",
	"preview",
	"music_offtopic",
	"filler",
	"poi_highlight",
	"exclusive_access",
	CategoryChapter,
}

// actionTypes must be sent explicitly: the API defaults to skip-only, which
// would drop mute segments, the highlight and chapter names.
var actionTypes = []string{ActionSkip, ActionMute, ActionPOI, ActionFull, ActionChapter}

const (
	// hashPrefixLen is the number of leading hex characters of the video id's
	// sha256 the lookup sends. Four is what the service documents: enough to
	// keep the response small, short enough that the id stays hidden in a
	// crowd of videos.
	hashPrefixLen = 4
	// maxBody caps the response read: the prefix matches a slice of the whole
	// database, so a broken or hostile server cannot stream us to death.
	maxBody = 8 << 20
	// maxSegments bounds what one video contributes to a player's timeline.
	maxSegments = 256

	defaultTimeout = 5 * time.Second
	// defaultTTL is short next to the chapters cache: unlike a downloaded
	// file's chapters, segments keep being submitted and voted on.
	defaultTTL = 6 * time.Hour
	// errTTL keeps a failing (or unreachable, or offline) service from being
	// retried on every video detail; until it expires the caller falls back to
	// the TubeArchivist snapshot.
	errTTL = 10 * time.Minute
	// cacheMax bounds the map so a crawl over a big archive cannot grow it
	// without end.
	cacheMax = 4096
)

// Segment is one crowd-sourced range. Description carries the chapter name
// for ActionChapter segments and is empty otherwise.
type Segment struct {
	Category    string
	ActionType  string
	Start       float64
	End         float64
	Description string
}

// Options configures a Client. The zero value of every field is usable.
type Options struct {
	// BaseURL of the SponsorBlock server; "" uses DefaultBaseURL.
	BaseURL string
	// Categories to ask for; nil uses DefaultCategories.
	Categories []string
	// HTTPClient overrides the default (a client with Timeout).
	HTTPClient *http.Client
	// Timeout bounds one lookup; 0 uses defaultTimeout.
	Timeout time.Duration
	// TTL caches a successful answer; 0 uses defaultTTL.
	TTL time.Duration
	// UserAgent identifies Flimm to the service.
	UserAgent string
	Log       *slog.Logger
}

// Client fetches and caches segments. It is safe for concurrent use.
type Client struct {
	base       string
	categories string // pre-encoded JSON array
	actions    string
	http       *http.Client
	ttl        time.Duration
	userAgent  string
	log        *slog.Logger

	mu sync.Mutex
	m  map[string]entry
	// now is time.Now; replaced in tests.
	now func() time.Time
}

type entry struct {
	segments []Segment
	// failed marks a remembered lookup failure. It is not an empty result:
	// "the service is down" and "this video has no segments" lead callers to
	// opposite decisions.
	failed bool
	exp    time.Time
}

// New builds a Client. It performs no I/O.
func New(o Options) *Client {
	base := strings.TrimRight(o.BaseURL, "/")
	if base == "" {
		base = DefaultBaseURL
	}
	cats := o.Categories
	if len(cats) == 0 {
		cats = DefaultCategories
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
		base:       base,
		categories: jsonArray(cats),
		actions:    jsonArray(actionTypes),
		http:       httpClient,
		ttl:        ttl,
		userAgent:  ua,
		log:        log,
		m:          map[string]entry{},
		now:        time.Now,
	}
}

func jsonArray(v []string) string {
	b, err := json.Marshal(v)
	if err != nil { // a []string never fails to marshal
		return "[]"
	}
	return string(b)
}

// Segments returns the segments for a video, newest data first-hand. A video
// the service has nothing for is an empty slice and a nil error — only a
// lookup that actually failed returns one, and the caller then has to decide
// what to fall back to.
func (c *Client) Segments(ctx context.Context, videoID string) ([]Segment, error) {
	if videoID == "" {
		return nil, nil
	}
	if e, ok := c.cached(videoID); ok {
		if e.failed {
			return nil, ErrUnavailable
		}
		return e.segments, nil
	}
	segs, err := c.fetch(ctx, videoID)
	if err != nil {
		// Remember the failure briefly so an unreachable service costs one
		// timeout per errTTL rather than one per request — but only when it
		// was the service's failure. A caller that went away (a viewer who
		// closed the tab mid-request) says nothing about the service, and
		// must not poison the next ten minutes of lookups for that video.
		if ctx.Err() == nil {
			c.put(videoID, entry{failed: true}, errTTL)
		}
		return nil, err
	}
	c.put(videoID, entry{segments: segs}, c.ttl)
	return segs, nil
}

// HashPrefix is the lookup key sent to the service: the first hashPrefixLen
// hex characters of sha256(videoID).
func HashPrefix(videoID string) string {
	sum := sha256.Sum256([]byte(videoID))
	return hex.EncodeToString(sum[:])[:hashPrefixLen]
}

// apiVideo is one entry of the hash-prefix response.
type apiVideo struct {
	VideoID  string       `json:"videoID"`
	Segments []apiSegment `json:"segments"`
}

type apiSegment struct {
	Category    string     `json:"category"`
	ActionType  string     `json:"actionType"`
	Segment     [2]float64 `json:"segment"`
	Description string     `json:"description"`
}

func (c *Client) fetch(ctx context.Context, videoID string) ([]Segment, error) {
	q := url.Values{
		"categories":  {c.categories},
		"actionTypes": {c.actions},
	}
	endpoint := c.base + "/api/skipSegments/" + HashPrefix(videoID) + "?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("sponsorblock request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sponsorblock lookup: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		// No video in this prefix has segments — an answer, not a failure.
		return nil, nil
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("sponsorblock lookup: unexpected status %d", resp.StatusCode)
	}
	var videos []apiVideo
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&videos); err != nil {
		return nil, fmt.Errorf("sponsorblock decode: %w", err)
	}
	for _, v := range videos {
		if v.VideoID == videoID {
			return clean(v.Segments), nil
		}
	}
	return nil, nil
}

// clean drops what a player cannot use and orders what is left. A zero-length
// range is only meaningful for the point-of-interest and whole-video actions.
func clean(in []apiSegment) []Segment {
	out := make([]Segment, 0, len(in))
	for _, s := range in {
		start, end := s.Segment[0], s.Segment[1]
		if isBadFloat(start) || isBadFloat(end) || start < 0 || end < start {
			continue
		}
		action := s.ActionType
		if action == "" {
			action = ActionSkip
		}
		if end == start && action != ActionPOI && action != ActionFull {
			continue
		}
		if s.Category == "" {
			continue
		}
		out = append(out, Segment{
			Category:    s.Category,
			ActionType:  action,
			Start:       start,
			End:         end,
			Description: strings.TrimSpace(s.Description),
		})
		if len(out) == maxSegments {
			break
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Start < out[j].Start })
	return out
}

func isBadFloat(f float64) bool {
	return f != f || f > 1e12 || f < -1e12
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
	if len(c.m) >= cacheMax {
		c.evictLocked()
	}
	e.exp = c.now().Add(ttl)
	c.m[videoID] = e
}

// evictLocked drops expired entries, and the entry closest to expiry when
// that freed nothing.
func (c *Client) evictLocked() {
	now := c.now()
	oldest, oldestExp := "", time.Time{}
	for id, e := range c.m {
		if now.After(e.exp) {
			delete(c.m, id)
			continue
		}
		if oldest == "" || e.exp.Before(oldestExp) {
			oldest, oldestExp = id, e.exp
		}
	}
	if len(c.m) >= cacheMax && oldest != "" {
		delete(c.m, oldest)
	}
}
