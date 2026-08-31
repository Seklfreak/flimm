package api

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Seklfreak/flimm/internal/ta"
)

type subtitleHit struct {
	Lang  string  `json:"lang"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

type searchVideo struct {
	Video        VideoSummary  `json:"video"`
	SubtitleHits []subtitleHit `json:"subtitle_hits"`
}

type searchChannel struct {
	ChannelSummary
	MatchCount int `json:"match_count"`
}

type searchPlaylist struct {
	PlaylistSummary
	MatchCount int `json:"match_count"`
}

type searchBucket[T any] struct {
	Total int `json:"total"`
	Items []T `json:"items"`
}

type searchResponse struct {
	TookMS    int64                        `json:"took_ms"`
	Videos    searchBucket[searchVideo]    `json:"videos"`
	Channels  searchBucket[searchChannel]  `json:"channels"`
	Playlists searchBucket[searchPlaylist] `json:"playlists"`
}

var searchScopes = map[string]bool{"all": true, "titles": true, "subtitles": true, "channels": true, "playlists": true}

func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	uid := currentUserID(r.Context())
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeError(w, http.StatusBadRequest, "q is required")
		return
	}
	scope := r.URL.Query().Get("scope")
	if scope == "" {
		scope = "all"
	}
	if !searchScopes[scope] {
		writeError(w, http.StatusBadRequest, "invalid scope")
		return
	}
	unseen := r.URL.Query().Get("unseen") == "true"

	// TA's query parser splits every whitespace-separated word on ":" into
	// exactly two parts, so a colon in the first word (which carries our
	// index prefix) or two colons in any word ("1:23:45") crash it with a
	// 500. Colons are not tokens ES indexes, so folding them to spaces keeps
	// the same matches. An empty remainder must not reach TA either: a bare
	// prefix turns into a match-all that returns arbitrary documents.
	term := strings.Join(strings.Fields(strings.ReplaceAll(q, ":", " ")), " ")
	if term == "" {
		writeJSON(w, http.StatusOK, searchResponse{
			TookMS:    time.Since(start).Milliseconds(),
			Videos:    searchBucket[searchVideo]{Items: []searchVideo{}},
			Channels:  searchBucket[searchChannel]{Items: []searchChannel{}},
			Playlists: searchBucket[searchPlaylist]{Items: []searchPlaylist{}},
		})
		return
	}

	// Feed restriction: nil = no restriction, empty = everything feed. A feed
	// admits a video through either source kind — its channels, or playlist
	// membership.
	var allowedChannels, allowedPlaylists map[string]bool
	if feedID := r.URL.Query().Get("feed"); feedID != "" && feedID != everythingFeedID {
		_, chans, pls, err := s.loadFeed(r.Context(), uid, feedID)
		if err != nil {
			s.writeDBError(w, "load feed", err)
			return
		}
		allowedChannels = map[string]bool{}
		for _, c := range chans {
			allowedChannels[c] = true
		}
		allowedPlaylists = map[string]bool{}
		for _, p := range pls {
			allowedPlaylists[p] = true
		}
	}

	// One TA query per index, in parallel.
	var queries []string
	want := func(k string) bool { return scope == "all" || scope == k }
	if want("titles") {
		queries = append(queries, "video:"+term)
	}
	if want("subtitles") {
		queries = append(queries, "full:"+term)
	}
	if want("channels") {
		queries = append(queries, "channel:"+term)
	}
	if want("playlists") {
		queries = append(queries, "playlist:"+term)
	}
	var mu sync.Mutex
	merged := ta.SearchResult{Videos: []ta.Video{}, Channels: []ta.Channel{}, Playlists: []ta.Playlist{}, Fulltext: []ta.SubtitleHit{}}
	err := parallel(r.Context(), queries, func(ctx context.Context, _ int, query string) error {
		res, err := s.ta.Search(ctx, query)
		if err != nil {
			return err
		}
		mu.Lock()
		defer mu.Unlock()
		merged.Videos = append(merged.Videos, res.Videos...)
		merged.Channels = append(merged.Channels, res.Channels...)
		merged.Playlists = append(merged.Playlists, res.Playlists...)
		merged.Fulltext = append(merged.Fulltext, res.Fulltext...)
		return nil
	})
	if err != nil {
		s.writeTAError(w, "search", err)
		return
	}

	videos, err := s.searchVideos(r.Context(), uid, merged, allowedChannels, allowedPlaylists, unseen)
	if err != nil {
		s.writeTAError(w, "search videos", err)
		return
	}
	hitsByChannel := map[string]int{}
	hitsByVideo := map[string]bool{}
	for _, v := range videos {
		hitsByChannel[v.Video.Channel.ID]++
		hitsByVideo[v.Video.ID] = true
	}

	channels := []searchChannel{}
	if len(merged.Channels) > 0 {
		refs, err := s.channelFeedRefs(r.Context(), uid)
		if err != nil {
			s.writeDBError(w, "list feed channels", err)
			return
		}
		pins, err := s.pinnedChannelSet(r.Context(), uid)
		if err != nil {
			s.writeDBError(w, "list pinned channels", err)
			return
		}
		out := make([]searchChannel, len(merged.Channels))
		err = parallel(r.Context(), merged.Channels, func(ctx context.Context, i int, c ta.Channel) error {
			cs, err := s.enrichChannel(ctx, c, refs[c.ChannelID], pins[c.ChannelID])
			if err != nil {
				return err
			}
			out[i] = searchChannel{ChannelSummary: *cs, MatchCount: hitsByChannel[c.ChannelID]}
			return nil
		})
		if err != nil {
			s.writeTAError(w, "channel stats", err)
			return
		}
		channels = out
	}

	playlists := []searchPlaylist{}
	if len(merged.Playlists) > 0 {
		sums, err := s.playlistSummaries(r.Context(), uid, merged.Playlists)
		if err != nil {
			s.writeTAError(w, "playlist summaries", err)
			return
		}
		for i, p := range merged.Playlists {
			n := 0
			for _, e := range p.PlaylistEntries {
				if hitsByVideo[e.YoutubeID] {
					n++
				}
			}
			playlists = append(playlists, searchPlaylist{PlaylistSummary: sums[i], MatchCount: n})
		}
	}

	writeJSON(w, http.StatusOK, searchResponse{
		TookMS:    time.Since(start).Milliseconds(),
		Videos:    searchBucket[searchVideo]{Total: len(videos), Items: videos},
		Channels:  searchBucket[searchChannel]{Total: len(channels), Items: channels},
		Playlists: searchBucket[searchPlaylist]{Total: len(playlists), Items: playlists},
	})
}

// searchVideos merges title hits and subtitle hits (grouped per video),
// resolving videos only known from subtitle hits, then applies the unseen
// and feed filters.
func (s *Server) searchVideos(ctx context.Context, uid uuid.UUID, res ta.SearchResult, allowed, allowedPlaylists map[string]bool, unseen bool) ([]searchVideo, error) {
	hits := map[string][]subtitleHit{}
	var order []string
	byID := map[string]ta.Video{}
	for _, v := range res.Videos {
		if _, ok := byID[v.YoutubeID]; !ok {
			order = append(order, v.YoutubeID)
		}
		byID[v.YoutubeID] = v
	}
	for _, h := range res.Fulltext {
		if _, ok := byID[h.YoutubeID]; !ok && !containsStr(order, h.YoutubeID) {
			order = append(order, h.YoutubeID)
		}
		hits[h.YoutubeID] = append(hits[h.YoutubeID], subtitleHit{Lang: h.SubtitleLang, Start: h.SubtitleStart, End: h.SubtitleEnd, Text: h.SubtitleLine})
	}
	var missing []string
	for _, id := range order {
		if _, ok := byID[id]; !ok {
			missing = append(missing, id)
		}
	}
	fetched := make([]*ta.Video, len(missing))
	err := parallel(ctx, missing, func(ctx context.Context, i int, id string) error {
		v, err := s.ta.GetVideo(ctx, id)
		if errors.Is(err, ta.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		fetched[i] = v
		return nil
	})
	if err != nil {
		return nil, err
	}
	for _, v := range fetched {
		if v != nil {
			byID[v.YoutubeID] = *v
		}
	}
	var vids []ta.Video
	for _, id := range order {
		v, ok := byID[id]
		if !ok {
			continue
		}
		if allowed != nil && !allowed[v.Channel.ChannelID] && !inAnyPlaylist(v, allowedPlaylists) {
			continue
		}
		vids = append(vids, v)
	}
	items, err := s.overlay(ctx, uid, vids)
	if err != nil {
		return nil, err
	}
	out := []searchVideo{}
	for _, it := range items {
		if unseen && it.Watched {
			continue
		}
		h := hits[it.ID]
		if h == nil {
			h = []subtitleHit{}
		}
		sort.SliceStable(h, func(i, j int) bool { return h[i].Start < h[j].Start })
		out = append(out, searchVideo{Video: it, SubtitleHits: h})
	}
	return out, nil
}

// inAnyPlaylist reports whether the video belongs to one of the playlists.
func inAnyPlaylist(v ta.Video, playlists map[string]bool) bool {
	for _, pid := range v.Playlist {
		if playlists[pid] {
			return true
		}
	}
	return false
}

func containsStr(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
