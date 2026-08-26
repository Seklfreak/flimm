package ta

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	// ErrNotFound is returned for TA 404s.
	ErrNotFound = errors.New("not found")
	// ErrUnavailable wraps connection failures and 5xx responses; handlers
	// map it to 502 "tubearchivist unavailable".
	ErrUnavailable = errors.New("tubearchivist unavailable")
	errBadDate     = errors.New("bad date")
)

// Client is what the handlers depend on. HTTP is the real implementation,
// Fake the test double.
type Client interface {
	Ping(ctx context.Context) error

	ListVideos(ctx context.Context, q VideoQuery) (*VideoPage, error)
	GetVideo(ctx context.Context, id string) (*Video, error)
	SimilarVideos(ctx context.Context, id string) ([]Video, error)
	Comments(ctx context.Context, id string) (Comments, error)
	SetProgress(ctx context.Context, id string, position float64) error
	DeleteProgress(ctx context.Context, id string) error
	// SetWatched flags a video, channel or playlist id watched/unwatched.
	SetWatched(ctx context.Context, id string, watched bool) error

	// ListChannels returns every channel TA knows (cached).
	ListChannels(ctx context.Context) ([]Channel, error)
	GetChannel(ctx context.Context, id string) (*Channel, error)
	// UnseenCount is the number of unwatched videos in a channel (cached).
	UnseenCount(ctx context.Context, channelID string) (int, error)
	// ChannelStats is the channel's video count and newest upload (cached).
	ChannelStats(ctx context.Context, channelID string) (*ChannelStats, error)

	ListPlaylists(ctx context.Context, kind, channelID string) ([]Playlist, error)
	GetPlaylist(ctx context.Context, id string) (*Playlist, error)
	CreateCustomPlaylist(ctx context.Context, name string) (*Playlist, error)
	CustomPlaylistAction(ctx context.Context, id, action, videoID string) error
	DeletePlaylist(ctx context.Context, id string) error

	Search(ctx context.Context, query string) (*SearchResult, error)
}

const (
	cacheChannels = 60 * time.Second
	cacheCounts   = 60 * time.Second
	cacheVideos   = 30 * time.Second
	// maxPageSize is what we ask TA for when walking a full list.
	maxPageSize = 100
)

// HTTP talks to a TubeArchivist instance with a server-side API token.
type HTTP struct {
	base  string
	token string
	http  *http.Client

	mu       sync.Mutex
	channels *cached[[]Channel]
	counts   map[string]*cached[int]
	stats    map[string]*cached[ChannelStats]
	videos   map[string]*cached[Video]
	lists    map[string]*cached[VideoPage]
}

type cached[T any] struct {
	val T
	exp time.Time
}

// New builds an HTTP client for baseURL (no trailing slash) using the Token
// auth header.
func New(baseURL, token string) *HTTP {
	return &HTTP{
		base:   strings.TrimRight(baseURL, "/"),
		token:  token,
		http:   &http.Client{Timeout: 30 * time.Second},
		counts: map[string]*cached[int]{},
		stats:  map[string]*cached[ChannelStats]{},
		videos: map[string]*cached[Video]{},
		lists:  map[string]*cached[VideoPage]{},
	}
}

var _ Client = (*HTTP)(nil)

// do performs a JSON request. A 404 is ErrNotFound; connection errors and 5xx
// are ErrUnavailable; other non-2xx statuses are plain errors with the body.
func (c *HTTP) do(ctx context.Context, method, path string, query url.Values, body any, out any) error {
	u := c.base + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Token "+c.token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return fmt.Errorf("%w: read body: %w", ErrUnavailable, err)
	}
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return ErrNotFound
	case resp.StatusCode >= 500:
		return fmt.Errorf("%w: %s %s: status %d", ErrUnavailable, method, path, resp.StatusCode)
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("%w: %s %s: status %d (check TA_TOKEN)", ErrUnavailable, method, path, resp.StatusCode)
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return fmt.Errorf("tubearchivist %s %s: status %d: %s", method, path, resp.StatusCode, truncate(string(data), 200))
	}
	if out == nil || len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("tubearchivist %s %s: decode: %w", method, path, err)
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

// envelope is TA's optional {"data": …} wrapper: some versions return the
// document directly, others wrap it. Decode both.
type envelope struct {
	Data     json.RawMessage `json:"data"`
	Paginate *Paginate       `json:"paginate"`
}

func decodeDoc(raw json.RawMessage, out any) error {
	var env envelope
	if err := json.Unmarshal(raw, &env); err == nil && len(env.Data) > 0 && !bytes.Equal(env.Data, []byte("null")) {
		return json.Unmarshal(env.Data, out)
	}
	return json.Unmarshal(raw, out)
}

func (c *HTTP) getDoc(ctx context.Context, path string, query url.Values, out any) error {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, path, query, nil, &raw); err != nil {
		return err
	}
	return decodeDoc(raw, out)
}

// Ping checks TA reachability and token validity.
func (c *HTTP) Ping(ctx context.Context) error {
	return c.do(ctx, http.MethodGet, "/api/ping/", nil, nil, nil)
}

func (q VideoQuery) values() url.Values {
	v := url.Values{}
	if q.Channel != "" {
		v.Set("channel", q.Channel)
	}
	if q.Playlist != "" {
		v.Set("playlist", q.Playlist)
	}
	if q.Watch != "" {
		v.Set("watch", q.Watch)
	}
	if q.Sort != "" {
		v.Set("sort", q.Sort)
	}
	if q.Order != "" {
		v.Set("order", q.Order)
	}
	if q.Type != "" {
		v.Set("type", q.Type)
	}
	if q.Page > 1 {
		v.Set("page", strconv.Itoa(q.Page))
	}
	if q.PageSize > 0 {
		v.Set("page_size", strconv.Itoa(q.PageSize))
	}
	return v
}

// ListVideos fetches one page of /api/video/. Pages are cached for 30 s so
// a feed fan-out repeated by pagination doesn't hammer TA.
func (c *HTTP) ListVideos(ctx context.Context, q VideoQuery) (*VideoPage, error) {
	key := q.values().Encode()
	c.mu.Lock()
	if e, ok := c.lists[key]; ok && time.Now().Before(e.exp) {
		c.mu.Unlock()
		p := e.val
		return &p, nil
	}
	c.mu.Unlock()

	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, "/api/video/", q.values(), nil, &raw); err != nil {
		if errors.Is(err, ErrNotFound) {
			// TA answers 404 for an empty result set on some versions.
			return &VideoPage{Data: []Video{}}, nil
		}
		return nil, err
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("decode video list: %w", err)
	}
	page := VideoPage{Data: []Video{}}
	if len(env.Data) > 0 && !bytes.Equal(env.Data, []byte("null")) {
		if err := json.Unmarshal(env.Data, &page.Data); err != nil {
			return nil, fmt.Errorf("decode video list: %w", err)
		}
	}
	if env.Paginate != nil {
		page.Paginate = *env.Paginate
	}
	c.mu.Lock()
	c.lists[key] = &cached[VideoPage]{val: page, exp: time.Now().Add(cacheVideos)}
	c.mu.Unlock()
	return &page, nil
}

// GetVideo fetches a video document (cached 30 s; heartbeats read it for the
// duration on every tick).
func (c *HTTP) GetVideo(ctx context.Context, id string) (*Video, error) {
	c.mu.Lock()
	if e, ok := c.videos[id]; ok && time.Now().Before(e.exp) {
		c.mu.Unlock()
		v := e.val
		return &v, nil
	}
	c.mu.Unlock()
	var v Video
	if err := c.getDoc(ctx, "/api/video/"+url.PathEscape(id)+"/", nil, &v); err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.videos[id] = &cached[Video]{val: v, exp: time.Now().Add(cacheVideos)}
	c.mu.Unlock()
	return &v, nil
}

func (c *HTTP) SimilarVideos(ctx context.Context, id string) ([]Video, error) {
	var out []Video
	if err := c.getDoc(ctx, "/api/video/"+url.PathEscape(id)+"/similar/", nil, &out); err != nil {
		if errors.Is(err, ErrNotFound) {
			return []Video{}, nil
		}
		return nil, err
	}
	if out == nil {
		out = []Video{}
	}
	return out, nil
}

func (c *HTTP) Comments(ctx context.Context, id string) (Comments, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, "/api/video/"+url.PathEscape(id)+"/comment/", nil, nil, &raw); err != nil {
		if errors.Is(err, ErrNotFound) {
			return Comments("[]"), nil
		}
		return nil, err
	}
	var out json.RawMessage
	if err := decodeDoc(raw, &out); err != nil || len(out) == 0 {
		return raw, nil //nolint:nilerr // raw passthrough when the wrapper isn't there
	}
	return out, nil
}

func (c *HTTP) SetProgress(ctx context.Context, id string, position float64) error {
	c.invalidateVideo(id)
	return c.do(ctx, http.MethodPost, "/api/video/"+url.PathEscape(id)+"/progress/", nil, map[string]any{"position": position}, nil)
}

func (c *HTTP) DeleteProgress(ctx context.Context, id string) error {
	c.invalidateVideo(id)
	err := c.do(ctx, http.MethodDelete, "/api/video/"+url.PathEscape(id)+"/progress/", nil, nil, nil)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

func (c *HTTP) SetWatched(ctx context.Context, id string, watched bool) error {
	c.invalidateWatch(id)
	return c.do(ctx, http.MethodPost, "/api/watched/", nil, map[string]any{"id": id, "is_watched": watched}, nil)
}

// invalidateVideo drops the cached document of one video.
func (c *HTTP) invalidateVideo(id string) {
	c.mu.Lock()
	delete(c.videos, id)
	c.mu.Unlock()
}

// invalidateWatch drops everything a watched-flag change can affect: unseen
// counts, cached list pages and the video itself.
func (c *HTTP) invalidateWatch(id string) {
	c.mu.Lock()
	delete(c.videos, id)
	c.counts = map[string]*cached[int]{}
	c.lists = map[string]*cached[VideoPage]{}
	c.mu.Unlock()
}

// ListChannels walks every page of /api/channel/, cached 60 s.
func (c *HTTP) ListChannels(ctx context.Context) ([]Channel, error) {
	c.mu.Lock()
	if c.channels != nil && time.Now().Before(c.channels.exp) {
		out := c.channels.val
		c.mu.Unlock()
		return out, nil
	}
	c.mu.Unlock()

	all := []Channel{}
	for page := 1; page <= 100; page++ {
		q := url.Values{"page_size": {strconv.Itoa(maxPageSize)}}
		if page > 1 {
			q.Set("page", strconv.Itoa(page))
		}
		var raw json.RawMessage
		if err := c.do(ctx, http.MethodGet, "/api/channel/", q, nil, &raw); err != nil {
			if errors.Is(err, ErrNotFound) {
				break
			}
			return nil, err
		}
		var env envelope
		if err := json.Unmarshal(raw, &env); err != nil {
			return nil, fmt.Errorf("decode channel list: %w", err)
		}
		var chunk []Channel
		if len(env.Data) > 0 {
			if err := json.Unmarshal(env.Data, &chunk); err != nil {
				return nil, fmt.Errorf("decode channel list: %w", err)
			}
		}
		all = append(all, chunk...)
		if len(chunk) == 0 || env.Paginate == nil || env.Paginate.LastPage == 0 || page >= env.Paginate.LastPage {
			break
		}
	}
	c.mu.Lock()
	c.channels = &cached[[]Channel]{val: all, exp: time.Now().Add(cacheChannels)}
	c.mu.Unlock()
	return all, nil
}

func (c *HTTP) GetChannel(ctx context.Context, id string) (*Channel, error) {
	var ch Channel
	if err := c.getDoc(ctx, "/api/channel/"+url.PathEscape(id)+"/", nil, &ch); err != nil {
		return nil, err
	}
	return &ch, nil
}

// UnseenCount asks for one unwatched video and reads total_hits (cached 60 s).
func (c *HTTP) UnseenCount(ctx context.Context, channelID string) (int, error) {
	c.mu.Lock()
	if e, ok := c.counts[channelID]; ok && time.Now().Before(e.exp) {
		c.mu.Unlock()
		return e.val, nil
	}
	c.mu.Unlock()
	page, err := c.ListVideos(ctx, VideoQuery{Channel: channelID, Watch: "unwatched", PageSize: 1})
	if err != nil {
		return 0, err
	}
	n := page.Paginate.TotalHits
	c.mu.Lock()
	c.counts[channelID] = &cached[int]{val: n, exp: time.Now().Add(cacheCounts)}
	c.mu.Unlock()
	return n, nil
}

// ChannelStats reads video count + newest upload from a one-item newest-first
// list (cached 60 s).
func (c *HTTP) ChannelStats(ctx context.Context, channelID string) (*ChannelStats, error) {
	c.mu.Lock()
	if e, ok := c.stats[channelID]; ok && time.Now().Before(e.exp) {
		c.mu.Unlock()
		s := e.val
		return &s, nil
	}
	c.mu.Unlock()
	page, err := c.ListVideos(ctx, VideoQuery{Channel: channelID, Sort: "published", Order: "desc", PageSize: 1})
	if err != nil {
		return nil, err
	}
	s := ChannelStats{VideoCount: page.Paginate.TotalHits}
	if len(page.Data) > 0 {
		s.LastUpload = page.Data[0].PublishedTime()
	}
	c.mu.Lock()
	c.stats[channelID] = &cached[ChannelStats]{val: s, exp: time.Now().Add(cacheCounts)}
	c.mu.Unlock()
	return &s, nil
}

// ListPlaylists lists playlists of a kind (custom|regular|"" for all),
// optionally restricted to a channel.
func (c *HTTP) ListPlaylists(ctx context.Context, kind, channelID string) ([]Playlist, error) {
	all := []Playlist{}
	for page := 1; page <= 100; page++ {
		q := url.Values{"page_size": {strconv.Itoa(maxPageSize)}}
		if kind != "" {
			q.Set("type", kind)
		}
		if channelID != "" {
			q.Set("channel", channelID)
		}
		if page > 1 {
			q.Set("page", strconv.Itoa(page))
		}
		var raw json.RawMessage
		if err := c.do(ctx, http.MethodGet, "/api/playlist/", q, nil, &raw); err != nil {
			if errors.Is(err, ErrNotFound) {
				break
			}
			return nil, err
		}
		var env envelope
		if err := json.Unmarshal(raw, &env); err != nil {
			return nil, fmt.Errorf("decode playlist list: %w", err)
		}
		var chunk []Playlist
		if len(env.Data) > 0 {
			if err := json.Unmarshal(env.Data, &chunk); err != nil {
				return nil, fmt.Errorf("decode playlist list: %w", err)
			}
		}
		all = append(all, chunk...)
		if len(chunk) == 0 || env.Paginate == nil || env.Paginate.LastPage == 0 || page >= env.Paginate.LastPage {
			break
		}
	}
	return all, nil
}

func (c *HTTP) GetPlaylist(ctx context.Context, id string) (*Playlist, error) {
	var p Playlist
	if err := c.getDoc(ctx, "/api/playlist/"+url.PathEscape(id)+"/", nil, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// CreateCustomPlaylist creates a TA custom playlist and returns it.
func (c *HTTP) CreateCustomPlaylist(ctx context.Context, name string) (*Playlist, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodPost, "/api/playlist/custom/", nil, map[string]string{"playlist_name": name}, &raw); err != nil {
		return nil, err
	}
	var p Playlist
	if err := decodeDoc(raw, &p); err != nil || p.PlaylistID == "" {
		// Older TA returns only a message; find the playlist by name.
		lists, lerr := c.ListPlaylists(ctx, "custom", "")
		if lerr != nil {
			return nil, lerr
		}
		for i := range lists {
			if lists[i].PlaylistName == name {
				return &lists[i], nil
			}
		}
		return nil, fmt.Errorf("create playlist: TA returned no playlist for %q", name)
	}
	return &p, nil
}

// CustomPlaylistAction runs one of create|remove|up|down|top|bottom for a
// video on a custom playlist.
func (c *HTTP) CustomPlaylistAction(ctx context.Context, id, action, videoID string) error {
	return c.do(ctx, http.MethodPost, "/api/playlist/custom/"+url.PathEscape(id)+"/", nil, map[string]string{"action": action, "video_id": videoID}, nil)
}

// DeletePlaylist removes the playlist only, never its videos.
func (c *HTTP) DeletePlaylist(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/api/playlist/"+url.PathEscape(id)+"/", url.Values{"delete-videos": {"false"}}, nil, nil)
}

// Search runs a TA search query (with its video:/channel:/playlist:/full:
// prefixes) and buckets the results.
func (c *HTTP) Search(ctx context.Context, query string) (*SearchResult, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, "/api/search/", url.Values{"query": {query}}, nil, &raw); err != nil {
		return nil, err
	}
	// {"results": {...buckets...}} on current TA; buckets at top level on older.
	var wrapped struct {
		Results json.RawMessage `json:"results"`
	}
	body := raw
	if err := json.Unmarshal(raw, &wrapped); err == nil && len(wrapped.Results) > 0 {
		body = wrapped.Results
	}
	var buckets struct {
		VideoResults    []Video       `json:"video_results"`
		ChannelResults  []Channel     `json:"channel_results"`
		PlaylistResults []Playlist    `json:"playlist_results"`
		FulltextResults []SubtitleHit `json:"fulltext_results"`
	}
	if err := json.Unmarshal(body, &buckets); err != nil {
		return nil, fmt.Errorf("decode search: %w", err)
	}
	return &SearchResult{
		Videos:    orEmpty(buckets.VideoResults),
		Channels:  orEmpty(buckets.ChannelResults),
		Playlists: orEmpty(buckets.PlaylistResults),
		Fulltext:  orEmpty(buckets.FulltextResults),
	}, nil
}

func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
