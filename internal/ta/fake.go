package ta

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Fake is an in-memory Client for handler tests. Seed Videos / Channels /
// Playlists; the list and search methods filter and sort them the way TA
// would, so feed-merging logic can be tested end to end. Every mutating call
// is recorded on Calls.
type Fake struct {
	mu        sync.Mutex
	Videos    map[string]*Video
	Channels  map[string]*Channel
	Playlists map[string]*Playlist
	// Progress mirrors what SetProgress wrote per video.
	Progress map[string]float64
	Calls    []string
	// Err, when set, is returned by every method (simulates TA down).
	Err error
	// PingErr fails only Ping.
	PingErr error
	// PingDelay makes Ping take this long, so a caller that time-boxes it can
	// be tested against a slow archive rather than only an absent one.
	PingDelay time.Duration
	// PageSizeCap makes ListVideos ignore the requested page size and use
	// this one, the way a real TubeArchivist does.
	PageSizeCap int
	// SearchFn overrides Search.
	SearchFn func(query string) (*SearchResult, error)
	// CommentsFn overrides Comments.
	CommentsFn func(id string) (Comments, error)
	// SimilarFn overrides SimilarVideos.
	SimilarFn func(id string) ([]Video, error)
	// Media holds raw file bytes keyed by the path FetchRange is called with
	// (the TA nginx path, e.g. "/media/UC1/v1.mp4").
	Media map[string][]byte
	// OpenMediaFn overrides OpenMedia.
	OpenMediaFn func(path string) (io.ReadCloser, error)
	// FetchRangeFn overrides FetchRange.
	FetchRangeFn func(path string, start, end int64) ([]byte, error)
	nextID       int
}

var _ Client = (*Fake)(nil)

// NewFake returns an empty Fake.
func NewFake() *Fake {
	return &Fake{
		Videos:    map[string]*Video{},
		Channels:  map[string]*Channel{},
		Playlists: map[string]*Playlist{},
		Progress:  map[string]float64{},
		Media:     map[string][]byte{},
	}
}

// AddVideo seeds a video (and its channel if unknown).
func (f *Fake) AddVideo(v Video) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if v.VidType == "" {
		v.VidType = "videos"
	}
	vv := v
	f.Videos[v.YoutubeID] = &vv
	if _, ok := f.Channels[v.Channel.ChannelID]; !ok && v.Channel.ChannelID != "" {
		ch := v.Channel
		f.Channels[ch.ChannelID] = &ch
	}
}

func (f *Fake) record(s string) {
	f.Calls = append(f.Calls, s)
}

func (f *Fake) Ping(ctx context.Context) error {
	if f.PingDelay > 0 {
		select {
		case <-time.After(f.PingDelay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if f.PingErr != nil {
		return f.PingErr
	}
	return f.Err
}

func (f *Fake) ListVideos(_ context.Context, q VideoQuery) (*VideoPage, error) {
	if f.Err != nil {
		return nil, f.Err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var all []Video
	for _, v := range f.Videos {
		if q.Channel != "" && v.Channel.ChannelID != q.Channel {
			continue
		}
		if q.Playlist != "" && !contains(v.Playlist, q.Playlist) {
			continue
		}
		switch q.Watch {
		case "watched":
			if !v.Player.Watched {
				continue
			}
		case "unwatched":
			if v.Player.Watched {
				continue
			}
		case "continue":
			if v.Player.Progress <= 0 || v.Player.Watched {
				continue
			}
		}
		if q.Type != "" && v.VidType != q.Type {
			continue
		}
		all = append(all, *v)
	}
	sortVideos(all, q.Sort, q.Order)
	// A real TubeArchivist decides its own page size and ignores the one on
	// the request; set PageSizeCap to reproduce that.
	size := q.PageSize
	if f.PageSizeCap > 0 {
		size = f.PageSizeCap
	}
	if size <= 0 {
		size = 12
	}
	page := q.Page
	if page < 1 {
		page = 1
	}
	from := (page - 1) * size
	to := from + size
	if from > len(all) {
		from = len(all)
	}
	if to > len(all) {
		to = len(all)
	}
	last := (len(all) + size - 1) / size
	return &VideoPage{
		Data:     append([]Video{}, all[from:to]...),
		Paginate: Paginate{PageSize: size, PageFrom: from, CurrentPage: page, LastPage: last, TotalHits: len(all)},
	}, nil
}

func sortVideos(vs []Video, field, order string) {
	less := func(a, b Video) bool { return a.Published < b.Published }
	switch field {
	case "duration":
		less = func(a, b Video) bool { return a.Player.Duration < b.Player.Duration }
	case "downloaded":
		less = func(a, b Video) bool { return a.DateDownloaded < b.DateDownloaded }
	case "views":
		less = func(a, b Video) bool { return a.Stats.ViewCount < b.Stats.ViewCount }
	}
	sort.SliceStable(vs, func(i, j int) bool {
		if order == "asc" {
			return less(vs[i], vs[j]) || (!less(vs[j], vs[i]) && vs[i].YoutubeID < vs[j].YoutubeID)
		}
		return less(vs[j], vs[i]) || (!less(vs[i], vs[j]) && vs[i].YoutubeID < vs[j].YoutubeID)
	})
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

func (f *Fake) GetVideo(_ context.Context, id string) (*Video, error) {
	if f.Err != nil {
		return nil, f.Err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.Videos[id]
	if !ok {
		return nil, ErrNotFound
	}
	vv := *v
	return &vv, nil
}

func (f *Fake) SimilarVideos(_ context.Context, id string) ([]Video, error) {
	if f.Err != nil {
		return nil, f.Err
	}
	if f.SimilarFn != nil {
		return f.SimilarFn(id)
	}
	return []Video{}, nil
}

func (f *Fake) Comments(_ context.Context, id string) (Comments, error) {
	if f.Err != nil {
		return nil, f.Err
	}
	if f.CommentsFn != nil {
		return f.CommentsFn(id)
	}
	return Comments{}, nil
}

func (f *Fake) SetProgress(_ context.Context, id string, position float64) error {
	if f.Err != nil {
		return f.Err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("progress:" + id)
	f.Progress[id] = position
	if v, ok := f.Videos[id]; ok {
		v.Player.Progress = position
	}
	return nil
}

func (f *Fake) DeleteProgress(_ context.Context, id string) error {
	if f.Err != nil {
		return f.Err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("delete-progress:" + id)
	delete(f.Progress, id)
	if v, ok := f.Videos[id]; ok {
		v.Player.Progress = 0
	}
	return nil
}

func (f *Fake) SetWatched(_ context.Context, id string, watched bool) error {
	if f.Err != nil {
		return f.Err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if watched {
		f.record("watched:" + id)
	} else {
		f.record("unwatched:" + id)
	}
	if v, ok := f.Videos[id]; ok {
		v.Player.Watched = watched
		return nil
	}
	// channel or playlist id: flag every member
	for _, v := range f.Videos {
		if v.Channel.ChannelID == id || contains(v.Playlist, id) {
			v.Player.Watched = watched
		}
	}
	return nil
}

func (f *Fake) ListChannels(context.Context) ([]Channel, error) {
	if f.Err != nil {
		return nil, f.Err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Channel, 0, len(f.Channels))
	for _, c := range f.Channels {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ChannelID < out[j].ChannelID })
	return out, nil
}

func (f *Fake) GetChannel(_ context.Context, id string) (*Channel, error) {
	if f.Err != nil {
		return nil, f.Err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.Channels[id]
	if !ok {
		return nil, ErrNotFound
	}
	cc := *c
	return &cc, nil
}

// ChannelCount mirrors TubeArchivist's channel aggregate.
func (f *Fake) ChannelCount(ctx context.Context) (int, error) {
	channels, err := f.ListChannels(ctx)
	if err != nil {
		return 0, err
	}
	return len(channels), nil
}

func (f *Fake) UnseenCount(ctx context.Context, channelID string) (int, error) {
	p, err := f.ListVideos(ctx, VideoQuery{Channel: channelID, Watch: "unwatched", PageSize: 1})
	if err != nil {
		return 0, err
	}
	return p.Paginate.TotalHits, nil
}

func (f *Fake) ChannelStats(ctx context.Context, channelID string) (*ChannelStats, error) {
	p, err := f.ListVideos(ctx, VideoQuery{Channel: channelID, Sort: "published", Order: "desc", PageSize: 1})
	if err != nil {
		return nil, err
	}
	s := &ChannelStats{VideoCount: p.Paginate.TotalHits}
	if len(p.Data) > 0 {
		s.LastUpload = p.Data[0].PublishedTime()
	}
	return s, nil
}

func (f *Fake) SetChannelSubscribed(_ context.Context, channelID string, subscribed bool) error {
	if f.Err != nil {
		return f.Err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.Channels[channelID]
	if !ok {
		return ErrNotFound
	}
	c.ChannelSubscribed = subscribed
	f.Calls = append(f.Calls, fmt.Sprintf("subscribe:%s:%t", channelID, subscribed))
	return nil
}

func (f *Fake) IndexChannelPlaylists(_ context.Context, channelID string) error {
	if f.Err != nil {
		return f.Err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.Channels[channelID]; !ok {
		return ErrNotFound
	}
	f.Calls = append(f.Calls, "index-playlists:"+channelID)
	return nil
}

func (f *Fake) ListPlaylists(_ context.Context, kind, channelID string) ([]Playlist, error) {
	if f.Err != nil {
		return nil, f.Err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []Playlist{}
	for _, p := range f.Playlists {
		if kind != "" && p.PlaylistType != kind {
			continue
		}
		if channelID != "" && p.PlaylistChannelID != channelID {
			continue
		}
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PlaylistID < out[j].PlaylistID })
	return out, nil
}

func (f *Fake) GetPlaylist(_ context.Context, id string) (*Playlist, error) {
	if f.Err != nil {
		return nil, f.Err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.Playlists[id]
	if !ok {
		return nil, ErrNotFound
	}
	pp := *p
	return &pp, nil
}

func (f *Fake) CreateCustomPlaylist(_ context.Context, name string) (*Playlist, error) {
	if f.Err != nil {
		return nil, f.Err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	id := "TA_playlist_" + strings.ReplaceAll(strings.ToLower(name), " ", "_") + "_" + itoa(f.nextID)
	p := &Playlist{PlaylistID: id, PlaylistName: name, PlaylistType: "custom", PlaylistActive: true, PlaylistEntries: []PlaylistEntry{}}
	f.Playlists[id] = p
	f.record("create-playlist:" + name)
	pp := *p
	return &pp, nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func (f *Fake) CustomPlaylistAction(_ context.Context, id, action, videoID string) error {
	if f.Err != nil {
		return f.Err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("playlist:" + id + ":" + action + ":" + videoID)
	p, ok := f.Playlists[id]
	if !ok {
		return ErrNotFound
	}
	idx := -1
	for i, e := range p.PlaylistEntries {
		if e.YoutubeID == videoID {
			idx = i
		}
	}
	switch action {
	case "create":
		if idx < 0 {
			title := ""
			if v, ok := f.Videos[videoID]; ok {
				title = v.Title
				v.Playlist = append(v.Playlist, id)
			}
			p.PlaylistEntries = append(p.PlaylistEntries, PlaylistEntry{YoutubeID: videoID, Title: title, Downloaded: true})
		}
	case "remove":
		if idx >= 0 {
			p.PlaylistEntries = append(p.PlaylistEntries[:idx], p.PlaylistEntries[idx+1:]...)
		}
	case "up", "down", "top", "bottom":
		if idx < 0 {
			return ErrNotFound
		}
		e := p.PlaylistEntries[idx]
		rest := append(append([]PlaylistEntry{}, p.PlaylistEntries[:idx]...), p.PlaylistEntries[idx+1:]...)
		to := idx
		switch action {
		case "up":
			to = max(idx-1, 0)
		case "down":
			to = min(idx+1, len(rest))
		case "top":
			to = 0
		case "bottom":
			to = len(rest)
		}
		p.PlaylistEntries = append(append(append([]PlaylistEntry{}, rest[:to]...), e), rest[to:]...)
	}
	for i := range p.PlaylistEntries {
		p.PlaylistEntries[i].Idx = i
	}
	return nil
}

func (f *Fake) DeletePlaylist(_ context.Context, id string) error {
	if f.Err != nil {
		return f.Err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("delete-playlist:" + id)
	if _, ok := f.Playlists[id]; !ok {
		return ErrNotFound
	}
	delete(f.Playlists, id)
	return nil
}

func (f *Fake) Search(_ context.Context, query string) (*SearchResult, error) {
	if f.Err != nil {
		return nil, f.Err
	}
	if f.SearchFn != nil {
		return f.SearchFn(query)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	res := &SearchResult{Videos: []Video{}, Channels: []Channel{}, Playlists: []Playlist{}, Fulltext: []SubtitleHit{}}
	prefix, term, _ := strings.Cut(query, ":")
	term = strings.ToLower(strings.TrimSpace(term))
	switch prefix {
	case "video":
		for _, v := range f.Videos {
			if strings.Contains(strings.ToLower(v.Title), term) {
				res.Videos = append(res.Videos, *v)
			}
		}
	case "channel":
		for _, c := range f.Channels {
			if strings.Contains(strings.ToLower(c.ChannelName), term) {
				res.Channels = append(res.Channels, *c)
			}
		}
	case "playlist":
		for _, p := range f.Playlists {
			if strings.Contains(strings.ToLower(p.PlaylistName), term) {
				res.Playlists = append(res.Playlists, *p)
			}
		}
	}
	return res, nil
}

// FetchRange serves a byte range out of Media (start and end inclusive).
func (f *Fake) FetchRange(_ context.Context, path string, start, end int64) ([]byte, error) {
	if f.FetchRangeFn != nil {
		return f.FetchRangeFn(path, start, end)
	}
	if f.Err != nil {
		return nil, f.Err
	}
	if start < 0 || end < start {
		return nil, ErrNotFound
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("fetch-range:" + path)
	data, ok := f.Media[path]
	if !ok {
		return nil, ErrNotFound
	}
	if start >= int64(len(data)) {
		return nil, ErrNotFound
	}
	to := min(end+1, int64(len(data)))
	return append([]byte{}, data[start:to]...), nil
}

func (f *Fake) OpenMedia(ctx context.Context, path string) (io.ReadCloser, error) {
	s, err := f.OpenMediaRange(ctx, path, "")
	if err != nil {
		return nil, err
	}
	return s.Body, nil
}

// OpenMediaRange serves Media with byte-range support, so a test exercises the
// same seekable path a real transcode reads through.
func (f *Fake) OpenMediaRange(_ context.Context, path, rangeHeader string) (*MediaStream, error) {
	if f.OpenMediaFn != nil {
		body, err := f.OpenMediaFn(path)
		if err != nil {
			return nil, err
		}
		return &MediaStream{Body: body, StatusCode: http.StatusOK, ContentLength: -1, AcceptRanges: "bytes"}, nil
	}
	if f.Err != nil {
		return nil, f.Err
	}
	f.mu.Lock()
	b, ok := f.Media[path]
	f.mu.Unlock()
	if !ok {
		return nil, ErrNotFound
	}
	total := int64(len(b))
	if rangeHeader != "" {
		start, end, err := parseByteRange(rangeHeader, total)
		if err != nil {
			return nil, err
		}
		return &MediaStream{
			Body:          io.NopCloser(bytes.NewReader(b[start : end+1])),
			StatusCode:    http.StatusPartialContent,
			ContentLength: end - start + 1,
			ContentRange:  fmt.Sprintf("bytes %d-%d/%d", start, end, total),
			AcceptRanges:  "bytes",
		}, nil
	}
	return &MediaStream{
		Body:          io.NopCloser(bytes.NewReader(b)),
		StatusCode:    http.StatusOK,
		ContentLength: total,
		AcceptRanges:  "bytes",
	}, nil
}

// parseByteRange understands the single-range forms ffmpeg sends:
// `bytes=a-b`, `bytes=a-` and `bytes=-n`.
func parseByteRange(header string, total int64) (int64, int64, error) {
	spec, ok := strings.CutPrefix(strings.TrimSpace(header), "bytes=")
	if !ok || strings.Contains(spec, ",") {
		return 0, 0, fmt.Errorf("fake: unsupported range %q", header)
	}
	from, to, ok := strings.Cut(spec, "-")
	if !ok {
		return 0, 0, fmt.Errorf("fake: unsupported range %q", header)
	}
	if from == "" {
		n, err := strconv.ParseInt(to, 10, 64)
		if err != nil || n <= 0 {
			return 0, 0, fmt.Errorf("fake: unsupported range %q", header)
		}
		return max(total-n, 0), total - 1, nil
	}
	start, err := strconv.ParseInt(from, 10, 64)
	if err != nil || start < 0 || start >= total {
		return 0, 0, fmt.Errorf("fake: range %q outside a %d-byte file", header, total)
	}
	end := total - 1
	if to != "" {
		if end, err = strconv.ParseInt(to, 10, 64); err != nil {
			return 0, 0, fmt.Errorf("fake: unsupported range %q", header)
		}
		end = min(end, total-1)
	}
	if end < start {
		return 0, 0, fmt.Errorf("fake: range %q outside a %d-byte file", header, total)
	}
	return start, end, nil
}
