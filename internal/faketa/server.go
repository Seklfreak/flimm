package faketa

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/Seklfreak/flimm/internal/ta"
	"time"
)

// PageSize is what TubeArchivist paginates video lists at by default, and
// what this stand-in uses regardless of the `page_size` on the request. It is
// deliberately smaller than anything Flimm asks for: a client that mistakes a
// short page for the last page must fail here, not only in production.
const PageSize = 12

// Server answers the subset of the TubeArchivist API that Flimm calls, plus
// the nginx-style /media/ paths its documents point at.
//
// Watch state lives here and only here: nothing is written to a real archive,
// which is the point of running against this instead of a live TA.
type Server struct {
	catalogue *Catalogue
	media     *Media
	log       *slog.Logger

	mu       sync.RWMutex
	watched  map[string]bool
	position map[string]float64
	// custom holds playlists created through the API, in creation order.
	custom []*ta.Playlist
	// reindexed overrides a video's date_downloaded. The real archive's
	// indexer writes date_downloaded and vid_last_refresh from one clock, so
	// a metadata refresh makes an old video read as downloaded just now —
	// the drift that fooled the feed notifier once. This door reproduces it.
	reindexed map[string]int64
	// arrived are videos added after start-up, modelled on a catalogue
	// video but with their own id: what a feed set to notify is waiting for.
	arrived []ta.Video
}

func NewServer(catalogue *Catalogue, media *Media, log *slog.Logger) *Server {
	return &Server{
		catalogue: catalogue,
		media:     media,
		log:       log,
		watched:   map[string]bool{},
		position:  map[string]float64{},
		reindexed: map[string]int64{},
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/ping/", s.ping)
	mux.HandleFunc("GET /api/video/", s.listVideos)
	mux.HandleFunc("GET /api/video/{id}/", s.getVideo)
	mux.HandleFunc("GET /api/video/{id}/similar/", s.similar)
	mux.HandleFunc("GET /api/video/{id}/comment/", s.comments)
	mux.HandleFunc("POST /api/video/{id}/progress/", s.setProgress)
	mux.HandleFunc("DELETE /api/video/{id}/progress/", s.deleteProgress)
	mux.HandleFunc("POST /api/watched/", s.setWatched)
	mux.HandleFunc("GET /api/channel/", s.listChannels)
	mux.HandleFunc("GET /api/channel/{id}/", s.getChannel)
	// The channel total the everything feed reads (see ta.ChannelCount);
	// without it GET /feeds 404s against the fake.
	mux.HandleFunc("GET /api/stats/channel/", s.channelStats)
	mux.HandleFunc("POST /api/channel/{id}/", s.updateChannel)
	mux.HandleFunc("POST /api/channel/", s.subscribeChannels)
	mux.HandleFunc("GET /api/playlist/", s.listPlaylists)
	mux.HandleFunc("POST /api/playlist/custom/", s.createPlaylist)
	mux.HandleFunc("POST /api/playlist/custom/{id}/", s.playlistAction)
	mux.HandleFunc("GET /api/playlist/{id}/", s.getPlaylist)
	mux.HandleFunc("DELETE /api/playlist/{id}/", s.deletePlaylist)
	mux.HandleFunc("GET /api/search/", s.search)
	// Not TubeArchivist routes. The catalogue is fixed, so nothing in it is
	// ever *new*: `arrive` adds a video modelled on one, with its own id,
	// indexed just now — what a feed set to notify is waiting for — and
	// `reindex` refreshes one the way the real archive does, which must
	// *not* count as new.
	mux.HandleFunc("POST /fake/arrive/{id}", s.arrive)
	mux.HandleFunc("POST /fake/reindex/{id}", s.reindex)
	mux.HandleFunc("GET /media/", s.serveMedia)
	// TA's thumbnail cache, which Flimm proxies /media/thumb/* to.
	mux.HandleFunc("GET /cache/", s.serveThumb)
	// Not TubeArchivist's: DeArrow's branding endpoint, so a dev stack can
	// exercise crowd-sourced titles and thumbnails without asking the real
	// service about videos that do not exist. Point DEARROW_URL here.
	mux.HandleFunc("GET /api/branding/{prefix}", s.branding)
	return s.logging(mux)
}

func (s *Server) logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.log.Debug("request", "method", r.Method, "path", r.URL.Path, "query", r.URL.RawQuery)
		next.ServeHTTP(w, r)
	})
}

// ---- videos ----

func (s *Server) ping(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"response": "pong"})
}

func (s *Server) listVideos(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	videos := s.videos()

	if channel := q.Get("channel"); channel != "" {
		videos = filter(videos, func(v ta.Video) bool { return v.Channel.ChannelID == channel })
	}
	if playlist := q.Get("playlist"); playlist != "" {
		ids := map[string]bool{}
		if p := s.playlist(playlist); p != nil {
			for _, e := range p.PlaylistEntries {
				ids[e.YoutubeID] = true
			}
		}
		videos = filter(videos, func(v ta.Video) bool { return ids[v.YoutubeID] })
	}
	switch q.Get("watch") {
	case "unwatched":
		videos = filter(videos, func(v ta.Video) bool { return !v.Player.Watched })
	case "watched":
		videos = filter(videos, func(v ta.Video) bool { return v.Player.Watched })
	}
	// TA's `type` filter is the list's own: no filter means videos only, the
	// way the real one behaves for a channel page.
	if kind := q.Get("type"); kind != "" {
		videos = filter(videos, func(v ta.Video) bool { return v.VidType == kind })
	}
	sortVideos(videos, q.Get("sort"), q.Get("order"))

	// A real TubeArchivist paginates at the size configured on *its* side and
	// ignores `page_size` on the request, so the stand-in must too — honouring
	// it here is what let a client bug (reading a short page as the last one)
	// look fine in local development while every list in production stopped at
	// twelve items.
	const pageSize = PageSize
	page := intParam(q.Get("page"), 1)
	total := len(videos)
	start := (page - 1) * pageSize
	end := start + pageSize
	if start > total {
		start = total
	}
	if end > total {
		end = total
	}
	last := 0
	if total > 0 {
		last = (total + pageSize - 1) / pageSize
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data": videos[start:end],
		"paginate": ta.Paginate{
			PageSize:    pageSize,
			PageFrom:    start,
			CurrentPage: page,
			LastPage:    last,
			TotalHits:   total,
		},
	})
}

func (s *Server) getVideo(w http.ResponseWriter, r *http.Request) {
	v, ok := s.video(r.PathValue("id"))
	if !ok {
		notFound(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": v})
}

// reindex answers POST /fake/reindex/{id}: the video's date_downloaded
// becomes now, exactly as a metadata refresh does in the real archive.
// Nothing else about it changes.
func (s *Server) reindex(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := s.video(id); !ok {
		notFound(w)
		return
	}
	s.mu.Lock()
	s.reindexed[id] = time.Now().Unix()
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

// arrive answers POST /fake/arrive/{id}: a new video on the same channel as
// {id}, with the same media, indexed just now. Its id is {id} with the last
// character replaced by a counter, so it stays eleven characters and unique
// for the run. Answers with the new document.
func (s *Server) arrive(w http.ResponseWriter, r *http.Request) {
	src, ok := s.video(r.PathValue("id"))
	if !ok {
		notFound(w)
		return
	}
	s.mu.Lock()
	n := len(s.arrived)
	v := src
	v.YoutubeID = src.YoutubeID[:len(src.YoutubeID)-1] + string(rune('A'+n%26))
	v.Title = src.Title + " (again)"
	v.DateDownloaded = time.Now().Unix()
	v.Player.Watched = false
	v.Player.Progress = 0
	s.arrived = append(s.arrived, v)
	s.mu.Unlock()
	writeJSON(w, http.StatusCreated, map[string]any{"data": v})
}

func (s *Server) similar(w http.ResponseWriter, r *http.Request) {
	v, ok := s.video(r.PathValue("id"))
	if !ok {
		notFound(w)
		return
	}
	out := filter(s.videos(), func(other ta.Video) bool {
		return other.Channel.ChannelID == v.Channel.ChannelID && other.YoutubeID != v.YoutubeID
	})
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}

// comments serves a small but *shaped* tree: replies, a hearted comment, one
// from the uploader, a timestamp and a comment that only has `time_text`. A
// flat list of two strings would have every client look right and none of them
// exercised.
func (s *Server) comments(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.video(r.PathValue("id")); !ok {
		notFound(w)
		return
	}
	// Fixed timestamps, so a screenshot taken today and one taken next month
	// show the same thing.
	base := time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC).Unix()
	writeJSON(w, http.StatusOK, map[string]any{"data": []map[string]any{
		{
			"comment_id":           "c1",
			"comment_text":         "The jig at 0:30 is the part I came for. Worth the wait.",
			"comment_author":       "@someone",
			"comment_author_id":    "UC-fake-someone",
			"comment_likecount":    128,
			"comment_time_text":    "1 week ago",
			"comment_timestamp":    base,
			"comment_is_favorited": true,
			"comment_replies": []map[string]any{
				{
					"comment_id":        "c1r1",
					"comment_text":      "Agreed — and the finish holds up.",
					"comment_author":    "@another-person",
					"comment_likecount": 4,
					"comment_timestamp": base + 3600,
				},
				{
					"comment_id":                 "c1r2",
					"comment_text":               "Thanks! The full cut is on the channel.",
					"comment_author":             "@the-workshop",
					"comment_likecount":          9,
					"comment_timestamp":          base + 7200,
					"comment_author_is_uploader": true,
				},
			},
		},
		{
			"comment_id":        "c2",
			"comment_text":      "The timer in the picture is the actual position, which is handy for checking a seek.",
			"comment_author":    "@someone-else",
			"comment_likecount": 12,
			"comment_timestamp": base + 86_400,
		},
		{
			// A long one, with a link and two timestamps — one inside the
			// video and one past its end, which must stay plain text. It is
			// what reaches a client's fold, and its wrapping of a long URL.
			"comment_id": "c5",
			"comment_text": "Watched this three times now. The part at 0:45 where the fence gets clamped is the one thing every other jig video skips, " +
				"and it is exactly where mine kept drifting.\n\nFor anyone building along: the plans linked in the description are worth it, " +
				"and the tail-board trick is also in https://example.com/forum/thread/dovetails-that-actually-close?page=3#post-17 (long thread, but read it).\n\n" +
				"One request: a follow-up on half-blinds, the way you did the through joint at 12:30 in the other video. That one is still the clearest explanation I have seen.",
			"comment_author":    "@builds-along",
			"comment_likecount": 41,
			"comment_timestamp": base + 2*86_400,
		},
		{
			// No timestamp at all: an older download that only kept the
			// relative wording. Clients have to fall back to it.
			"comment_id":        "c3",
			"comment_text":      "First. Also: this archive is not real.",
			"comment_author":    "@early-bird",
			"comment_likecount": 0,
			"comment_time_text": "2 days ago",
		},
		{
			// Nothing to say and no one to say it: dropped by the server, and
			// the reason the client never sees a blank row.
			"comment_id": "c4",
		},
	}})
}

func (s *Server) setProgress(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.video(r.PathValue("id")); !ok {
		notFound(w)
		return
	}
	var body struct {
		Position float64 `json:"position"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	s.mu.Lock()
	s.position[r.PathValue("id")] = body.Position
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"position": body.Position})
}

func (s *Server) deleteProgress(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	delete(s.position, r.PathValue("id"))
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

// setWatched takes a video, channel or playlist id, exactly as TA does.
func (s *Server) setWatched(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID        string `json:"id"`
		IsWatched bool   `json:"is_watched"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ID == "" {
		http.Error(w, `{"error":"bad request"}`, http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case strings.HasPrefix(body.ID, "UC"):
		for _, v := range s.catalogue.Videos {
			if v.Channel.ChannelID == body.ID {
				s.watched[v.YoutubeID] = body.IsWatched
			}
		}
	case strings.HasPrefix(body.ID, "PL"):
		if p := s.playlistLocked(body.ID); p != nil {
			for _, e := range p.PlaylistEntries {
				s.watched[e.YoutubeID] = body.IsWatched
			}
		}
	default:
		s.watched[body.ID] = body.IsWatched
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// ---- channels and playlists ----

func (s *Server) listChannels(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"data":     s.catalogue.Channels,
		"paginate": ta.Paginate{PageSize: len(s.catalogue.Channels), CurrentPage: 1, LastPage: 1, TotalHits: len(s.catalogue.Channels)},
	})
}

func (s *Server) getChannel(w http.ResponseWriter, r *http.Request) {
	for _, ch := range s.catalogue.Channels {
		if ch.ChannelID == r.PathValue("id") {
			writeJSON(w, http.StatusOK, map[string]any{"data": ch})
			return
		}
	}
	notFound(w)
}

func (s *Server) channelStats(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"doc_count": len(s.catalogue.Channels)})
}

// subscribeChannels is TA's subscribe toggle: {"data":[{"channel_id",
// "channel_subscribed"}]}. The fake flips the flag on channels it knows.
func (s *Server) subscribeChannels(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Data []struct {
			ChannelID  string `json:"channel_id"`
			Subscribed bool   `json:"channel_subscribed"`
		} `json:"data"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	for _, d := range body.Data {
		found := false
		for i := range s.catalogue.Channels {
			if s.catalogue.Channels[i].ChannelID == d.ChannelID {
				s.catalogue.Channels[i].ChannelSubscribed = d.Subscribed
				found = true
			}
		}
		if !found && d.Subscribed {
			// TA's subscribe task creates channels it does not know.
			s.catalogue.Channels = append(s.catalogue.Channels, ta.Channel{
				ChannelID: d.ChannelID, ChannelName: d.ChannelID,
				ChannelSubscribed: true, ChannelActive: true,
			})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": "subscription processed"})
}

// updateChannel accepts the channel_overwrites write behind Flimm's admin
// "index this channel's series" control. The fake's catalogue is fixed, so
// there is nothing to discover; acknowledging the write is what lets the
// flow be walked end to end against the dev stack.
func (s *Server) updateChannel(w http.ResponseWriter, r *http.Request) {
	known := false
	for _, ch := range s.catalogue.Channels {
		known = known || ch.ChannelID == r.PathValue("id")
	}
	if !known {
		notFound(w)
		return
	}
	var body struct {
		ChannelOverwrites map[string]any `json:"channel_overwrites"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"channel_overwrites": body.ChannelOverwrites})
}

func (s *Server) listPlaylists(w http.ResponseWriter, r *http.Request) {
	all := s.playlists()
	if kind := r.URL.Query().Get("playlist_type"); kind != "" {
		all = filter(all, func(p ta.Playlist) bool { return p.PlaylistType == kind })
	}
	if channel := r.URL.Query().Get("channel"); channel != "" {
		all = filter(all, func(p ta.Playlist) bool { return p.PlaylistChannelID == channel })
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data":     all,
		"paginate": ta.Paginate{PageSize: len(all), CurrentPage: 1, LastPage: 1, TotalHits: len(all)},
	})
}

func (s *Server) getPlaylist(w http.ResponseWriter, r *http.Request) {
	p := s.playlist(r.PathValue("id"))
	if p == nil {
		notFound(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": p})
}

func (s *Server) createPlaylist(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PlaylistName string `json:"playlist_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.PlaylistName == "" {
		http.Error(w, `{"error":"bad request"}`, http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	p := &ta.Playlist{
		PlaylistID:     "PL-fake-custom-" + strconv.Itoa(len(s.custom)+1),
		PlaylistName:   body.PlaylistName,
		PlaylistType:   "custom",
		PlaylistActive: true,
	}
	s.custom = append(s.custom, p)
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"data": p})
}

// playlistAction runs create|remove|up|down|top|bottom, the six TA supports.
func (s *Server) playlistAction(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Action  string `json:"action"`
		VideoID string `json:"video_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"bad request"}`, http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.playlistLocked(r.PathValue("id"))
	if p == nil {
		notFound(w)
		return
	}
	index := -1
	for i, e := range p.PlaylistEntries {
		if e.YoutubeID == body.VideoID {
			index = i
			break
		}
	}
	switch body.Action {
	case "create":
		if index < 0 {
			if v, ok := s.videoLocked(body.VideoID); ok {
				p.PlaylistEntries = append(p.PlaylistEntries, ta.PlaylistEntry{
					YoutubeID: v.YoutubeID, Title: v.Title, Uploader: v.Channel.ChannelName, Downloaded: true,
				})
			}
		}
	case "remove":
		if index >= 0 {
			p.PlaylistEntries = append(p.PlaylistEntries[:index], p.PlaylistEntries[index+1:]...)
		}
	case "up", "down", "top", "bottom":
		if index >= 0 {
			move(p, index, body.Action)
		}
	}
	for i := range p.PlaylistEntries {
		p.PlaylistEntries[i].Idx = i
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func move(p *ta.Playlist, index int, action string) {
	entry := p.PlaylistEntries[index]
	rest := append(append([]ta.PlaylistEntry{}, p.PlaylistEntries[:index]...), p.PlaylistEntries[index+1:]...)
	target := index
	switch action {
	case "up":
		target = max(0, index-1)
	case "down":
		target = min(len(rest), index+1)
	case "top":
		target = 0
	case "bottom":
		target = len(rest)
	}
	p.PlaylistEntries = append(rest[:target], append([]ta.PlaylistEntry{entry}, rest[target:]...)...)
}

func (s *Server) deletePlaylist(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, p := range s.custom {
		if p.PlaylistID == r.PathValue("id") {
			s.custom = append(s.custom[:i], s.custom[i+1:]...)
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	notFound(w)
}

// ---- search ----

func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("query")))
	// Real TA (v0.5.12) splits every word on ":" into exactly two parts and
	// crashes with a 500 on a third — which is what any word holding two
	// colons produces. Mirror that so a query the server forgot to sanitize
	// fails here the way it fails in production.
	for _, word := range strings.Fields(query) {
		if strings.Count(word, ":") >= 2 {
			http.Error(w, "ValueError: too many values to unpack", http.StatusInternalServerError)
			return
		}
	}
	// TA's prefixes narrow the search to one index.
	only := ""
	for _, prefix := range []string{"video:", "channel:", "playlist:", "full:"} {
		if strings.HasPrefix(query, prefix) {
			only = strings.TrimSuffix(prefix, ":")
			query = strings.TrimSpace(strings.TrimPrefix(query, prefix))
		}
	}
	out := map[string]any{
		"video_results":    []ta.Video{},
		"channel_results":  []ta.Channel{},
		"playlist_results": []ta.Playlist{},
		"fulltext_results": []ta.SubtitleHit{},
	}
	if query != "" {
		if only == "" || only == "video" {
			out["video_results"] = filter(s.videos(), func(v ta.Video) bool {
				return strings.Contains(strings.ToLower(v.Title), query)
			})
		}
		if only == "" || only == "channel" {
			out["channel_results"] = filter(s.catalogue.Channels, func(c ta.Channel) bool {
				return strings.Contains(strings.ToLower(c.ChannelName), query)
			})
		}
		if only == "" || only == "playlist" {
			out["playlist_results"] = filter(s.playlists(), func(p ta.Playlist) bool {
				return strings.Contains(strings.ToLower(p.PlaylistName), query)
			})
		}
		if only == "" || only == "full" {
			out["fulltext_results"] = s.subtitleHits(query)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": out})
}

// subtitleHits searches the generated WebVTT lines, which name their own
// timestamp — so a hit that says 0:20 really is at 0:20.
func (s *Server) subtitleHits(query string) []ta.SubtitleHit {
	hits := []ta.SubtitleHit{}
	for _, v := range s.videos() {
		for start := 0.0; start < v.Player.Duration; start += 5 {
			// Matched against the readable text, but indexed with the markup
			// the archive actually stores — that is the shape a client has to
			// cope with.
			if !strings.Contains(strings.ToLower(SubtitleLineText(start)), query) {
				continue
			}
			hits = append(hits, ta.SubtitleHit{
				YoutubeID: v.YoutubeID, Title: v.Title, SubtitleLine: SubtitleLine(start), SubtitleStart: start,
			})
			if len(hits) >= 20 {
				return hits
			}
		}
	}
	return hits
}

// ---- media ----

// serveMedia answers the nginx paths TA's documents point at: the video
// files, the WebVTT tracks and the thumbnails.
func (s *Server) serveMedia(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/media/")
	switch {
	case strings.HasSuffix(name, ".mp4"):
		id := strings.TrimSuffix(pathBase(name), ".mp4")
		path := s.media.Path(id)
		if path == "" {
			notFound(w)
			return
		}
		f, err := os.Open(path) //nolint:gosec // path comes from the generator, not the request
		if err != nil {
			notFound(w)
			return
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			notFound(w)
			return
		}
		w.Header().Set("Content-Type", "video/mp4")
		// ServeContent gives us Range support, which every player needs.
		http.ServeContent(w, r, path, info.ModTime(), f)
	case strings.HasSuffix(name, ".vtt"):
		id := strings.TrimSuffix(strings.TrimSuffix(pathBase(name), ".vtt"), ".en")
		v, ok := s.video(id)
		if !ok {
			notFound(w)
			return
		}
		w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
		_, _ = w.Write([]byte(Subtitles(v.Player.Duration)))
	case strings.HasSuffix(name, ".jpg"):
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(thumbnailJPEG(pathBase(name)))
	default:
		notFound(w)
	}
}

// serveThumb answers TA's /cache/… thumbnail paths: videos sharded by the
// first character of the id, channels with _thumb/_banner suffixes, and
// playlists. Every one of them gets a generated card.
func (s *Server) serveThumb(w http.ResponseWriter, r *http.Request) {
	if !strings.HasSuffix(r.URL.Path, ".jpg") {
		notFound(w)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	_, _ = w.Write(thumbnailJPEG(pathBase(r.URL.Path)))
}

func pathBase(name string) string {
	if i := strings.LastIndex(name, "/"); i >= 0 {
		return name[i+1:]
	}
	return name
}

// ---- state ----

// videos returns the catalogue with the live watch state folded in, which is
// what makes "mark seen" and resume behave like the real thing.
func (s *Server) videos() []ta.Video {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ta.Video, 0, len(s.catalogue.Videos)+len(s.arrived))
	for _, v := range s.catalogue.Videos {
		out = append(out, s.applyStateLocked(v))
	}
	for _, v := range s.arrived {
		out = append(out, s.applyStateLocked(v))
	}
	return out
}

func (s *Server) video(id string) (ta.Video, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.videoLocked(id)
}

func (s *Server) videoLocked(id string) (ta.Video, bool) {
	for _, v := range s.catalogue.Videos {
		if v.YoutubeID == id {
			return s.applyStateLocked(v), true
		}
	}
	for _, v := range s.arrived {
		if v.YoutubeID == id {
			return s.applyStateLocked(v), true
		}
	}
	return ta.Video{}, false
}

func (s *Server) applyStateLocked(v ta.Video) ta.Video {
	v.Player.Watched = s.watched[v.YoutubeID]
	if at, ok := s.reindexed[v.YoutubeID]; ok {
		v.DateDownloaded = at
	}
	if position, ok := s.position[v.YoutubeID]; ok && v.Player.Duration > 0 {
		v.Player.Progress = position / v.Player.Duration
	}
	return v
}

func (s *Server) playlists() []ta.Playlist {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := append([]ta.Playlist{}, s.catalogue.Playlists...)
	for _, p := range s.custom {
		out = append(out, *p)
	}
	return out
}

func (s *Server) playlist(id string) *ta.Playlist {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p := s.playlistLocked(id)
	if p == nil {
		return nil
	}
	copied := *p
	return &copied
}

func (s *Server) playlistLocked(id string) *ta.Playlist {
	for i := range s.catalogue.Playlists {
		if s.catalogue.Playlists[i].PlaylistID == id {
			return &s.catalogue.Playlists[i]
		}
	}
	for _, p := range s.custom {
		if p.PlaylistID == id {
			return p
		}
	}
	return nil
}

// ---- helpers ----

func sortVideos(videos []ta.Video, field, order string) {
	less := func(a, b ta.Video) bool { return a.Published > b.Published }
	switch field {
	case "duration":
		less = func(a, b ta.Video) bool { return a.Player.Duration < b.Player.Duration }
	case "downloaded":
		less = func(a, b ta.Video) bool { return a.DateDownloaded > b.DateDownloaded }
	}
	sort.SliceStable(videos, func(i, j int) bool {
		if order == "asc" {
			return less(videos[j], videos[i])
		}
		return less(videos[i], videos[j])
	})
}

func filter[T any](in []T, keep func(T) bool) []T {
	out := make([]T, 0, len(in))
	for _, v := range in {
		if keep(v) {
			out = append(out, v)
		}
	}
	return out
}

func intParam(raw string, fallback int) int {
	if n, err := strconv.Atoi(raw); err == nil && n > 0 {
		return n
	}
	return fallback
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func notFound(w http.ResponseWriter) {
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
}

// branding answers DeArrow's `/api/branding/{prefix}` for the fake catalogue.
//
// It speaks the real service's shape, including the part that makes the real
// one privacy-preserving: the request carries four characters of a hash, and
// the answer covers *every* video whose id starts with them. Here that is at
// most a handful, since the catalogue is small.
func (s *Server) branding(w http.ResponseWriter, r *http.Request) {
	prefix := strings.ToLower(r.PathValue("prefix"))
	out := map[string]any{}
	for _, v := range s.videos() {
		sum := sha256.Sum256([]byte(v.YoutubeID))
		if !strings.HasPrefix(hex.EncodeToString(sum[:]), prefix) {
			continue
		}
		b, ok := brandingFor(v.YoutubeID)
		if !ok {
			// In the prefix but with nothing said about it: the service still
			// answers, with an entry that has no submissions.
			out[v.YoutubeID] = map[string]any{"titles": []any{}, "thumbnails": []any{}, "randomTime": 0.5}
			continue
		}
		titles := []any{}
		if b.Title != "" {
			titles = append(titles, map[string]any{"title": b.Title, "original": false, "votes": 3, "locked": false})
		}
		if b.TitleOriginal {
			titles = append(titles, map[string]any{"title": "", "original": true, "votes": 4, "locked": false})
		}
		thumbnails := []any{}
		if b.ThumbnailAt != nil {
			thumbnails = append(thumbnails, map[string]any{
				"timestamp": *b.ThumbnailAt, "original": false, "votes": 2, "locked": false,
			})
		}
		out[v.YoutubeID] = map[string]any{"titles": titles, "thumbnails": thumbnails, "randomTime": b.RandomTime}
	}
	writeJSON(w, http.StatusOK, out)
}
