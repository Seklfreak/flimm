package api

import (
	"cmp"
	"context"
	"errors"
	"hash/fnv"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Seklfreak/flimm/internal/db/sqlc"
	"github.com/Seklfreak/flimm/internal/media"
	"github.com/Seklfreak/flimm/internal/ta"
)

// ---- listing core ----

// listOpts describes a merged video list (a feed, the everything feed or a
// single channel).
type listOpts struct {
	// ChannelIDs to fan out over; nil = no channel filter (everything).
	ChannelIDs    []string
	Sort          string // newest|oldest|shortest|longest
	IncludeShorts bool
	SubtitlesOnly bool
	// UnseenOnly asks TA for unwatched videos and drops completed events.
	UnseenOnly bool
	// DropDismissed removes videos the user took out of their feeds. Set for
	// every feed view and never for a channel, a playlist or search: those are
	// where a dismissed video is found again and put back.
	DropDismissed bool
	// ExcludeIDs are videos this list has already shown somewhere above it —
	// the in-progress head of an unseen feed. Deliberately *not* part of a
	// cursor's fingerprint: a video finishing mid-scroll changes this set, and
	// that must not invalidate the cursor a client is holding.
	ExcludeIDs map[string]bool
}

// taSort maps an API sort to TA's sort/order pair.
func taSort(sortKey string) (field, order string) {
	switch sortKey {
	case "oldest":
		return "published", "asc"
	case "shortest":
		return "duration", "asc"
	case "longest":
		return "duration", "desc"
	default:
		return "published", "desc"
	}
}

func sortSummaries(items []VideoSummary, sortKey string) {
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		switch sortKey {
		case "oldest":
			if !a.Published.Equal(b.Published) {
				return a.Published.Before(b.Published)
			}
		case "shortest":
			if a.Duration != b.Duration {
				return a.Duration < b.Duration
			}
		case "longest":
			if a.Duration != b.Duration {
				return a.Duration > b.Duration
			}
		default:
			if !a.Published.Equal(b.Published) {
				return a.Published.After(b.Published)
			}
		}
		if !a.Downloaded.Equal(b.Downloaded) {
			return a.Downloaded.After(b.Downloaded)
		}
		return a.ID < b.ID
	})
}

// fetchAll walks TA pages for one query up to maxListVideos.
//
// TubeArchivist chooses its own page size: `page_size` on the request is not
// honoured by every version, and the response reports the size TA actually
// used (12 by default). A page shorter than the one we asked for therefore
// says nothing about whether more exist — reading it as "last page" capped
// every list in Flimm at a single TA page, so a 369-video channel returned
// twelve videos and a feed over a 10,000-video archive returned twelve too.
//
// Trust TA's own pagination instead, and stop on a page that adds nothing new
// so a version that ignores `page` as well cannot spin.
func (s *Server) fetchAll(ctx context.Context, q ta.VideoQuery) ([]ta.Video, error) {
	q.PageSize = maxPageSize
	var out []ta.Video
	seen := make(map[string]bool)
	for page := 1; len(out) < maxListVideos; page++ {
		q.Page = page
		res, err := s.ta.ListVideos(ctx, q)
		if err != nil {
			return nil, err
		}
		added := 0
		for _, v := range res.Data {
			if seen[v.YoutubeID] {
				continue
			}
			seen[v.YoutubeID] = true
			out = append(out, v)
			added++
		}
		if added == 0 {
			break
		}
		if res.Paginate.LastPage > 0 && page >= res.Paginate.LastPage {
			break
		}
		if res.Paginate.TotalHits > 0 && len(out) >= res.Paginate.TotalHits {
			break
		}
	}
	return out, nil
}

// loadEvents fetches the user's watch_events for the given video ids.
func (s *Server) loadEvents(ctx context.Context, uid uuid.UUID, ids []string) (map[string]sqlc.WatchEvent, error) {
	out := map[string]sqlc.WatchEvent{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.q.ListWatchEventsForVideos(ctx, sqlc.ListWatchEventsForVideosParams{UserID: uid, VideoIds: ids})
	if err != nil {
		return nil, err
	}
	for _, ev := range rows {
		out[ev.VideoID] = ev
	}
	return out, nil
}

// overlay turns TA videos into per-user summaries.
func (s *Server) overlay(ctx context.Context, uid uuid.UUID, videos []ta.Video) ([]VideoSummary, error) {
	ids := make([]string, 0, len(videos))
	for _, v := range videos {
		ids = append(ids, v.YoutubeID)
	}
	events, err := s.loadEvents(ctx, uid, ids)
	if err != nil {
		return nil, err
	}
	dismissed, err := s.loadDismissed(ctx, uid, ids)
	if err != nil {
		return nil, err
	}
	out := make([]VideoSummary, 0, len(videos))
	for _, v := range videos {
		var ev *sqlc.WatchEvent
		if e, ok := events[v.YoutubeID]; ok {
			ev = &e
		}
		item := summarize(v, ev)
		item.Dismissed = dismissed[v.YoutubeID]
		out = append(out, item)
	}
	// Titles and thumbnails last, over the finished summaries: every list in
	// the API is built here, so this is the one place a video can be renamed
	// and the clients cannot disagree about what it is called.
	if s.dearrow != nil {
		prefs, err := s.prefsFor(ctx, uid)
		if err != nil {
			return nil, err
		}
		s.applyBranding(ctx, prefs, out)
	}
	return out, nil
}

// loadDismissed reports which of the given videos the user has taken out of
// their feeds.
func (s *Server) loadDismissed(ctx context.Context, uid uuid.UUID, ids []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.q.ListDismissedForVideos(ctx, sqlc.ListDismissedForVideosParams{UserID: uid, VideoIds: ids})
	if err != nil {
		return nil, err
	}
	for _, id := range rows {
		out[id] = true
	}
	return out, nil
}

// buildList fans out to TA (bounded), merges, filters, overlays and sorts.
func (s *Server) buildList(ctx context.Context, uid uuid.UUID, o listOpts) ([]VideoSummary, error) {
	field, order := taSort(o.Sort)
	watch := ""
	if o.UnseenOnly {
		watch = "unwatched"
	}
	queries := []ta.VideoQuery{{Watch: watch, Sort: field, Order: order}}
	if o.ChannelIDs != nil {
		queries = make([]ta.VideoQuery, 0, len(o.ChannelIDs))
		for _, ch := range o.ChannelIDs {
			queries = append(queries, ta.VideoQuery{Channel: ch, Watch: watch, Sort: field, Order: order})
		}
	}
	var mu sync.Mutex
	var merged []ta.Video
	seen := map[string]bool{}
	err := parallel(ctx, queries, func(ctx context.Context, _ int, q ta.VideoQuery) error {
		vids, err := s.fetchAll(ctx, q)
		if err != nil {
			return err
		}
		mu.Lock()
		defer mu.Unlock()
		for _, v := range vids {
			if seen[v.YoutubeID] {
				continue
			}
			seen[v.YoutubeID] = true
			merged = append(merged, v)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	filtered := merged[:0]
	for _, v := range merged {
		if !o.IncludeShorts && v.Kind() == "short" {
			continue
		}
		if o.SubtitlesOnly && len(v.Subtitles) == 0 {
			continue
		}
		filtered = append(filtered, v)
	}
	items, err := s.overlay(ctx, uid, filtered)
	if err != nil {
		return nil, err
	}
	if o.UnseenOnly {
		kept := items[:0]
		for _, it := range items {
			if !it.Watched {
				kept = append(kept, it)
			}
		}
		items = kept
	}
	if o.DropDismissed {
		kept := items[:0]
		for _, it := range items {
			if !it.Dismissed {
				kept = append(kept, it)
			}
		}
		items = kept
	}
	sortSummaries(items, o.Sort)
	if items == nil {
		items = []VideoSummary{}
	}
	return items, nil
}

// continueList is the "continue watching" view: in-progress events (newest
// first), restricted to channelIDs when given, resolved against TA.
func (s *Server) continueList(ctx context.Context, uid uuid.UUID, channelIDs []string, includeShorts bool) ([]VideoSummary, error) {
	events, err := s.q.ListInProgress(ctx, sqlc.ListInProgressParams{UserID: uid, Limit: maxListVideos})
	if err != nil {
		return nil, err
	}
	var allowed map[string]bool
	if channelIDs != nil {
		allowed = map[string]bool{}
		for _, id := range channelIDs {
			allowed[id] = true
		}
	}
	var picked []sqlc.WatchEvent
	for _, ev := range events {
		if allowed != nil && !allowed[ev.ChannelID] {
			continue
		}
		picked = append(picked, ev)
	}
	out := make([]VideoSummary, len(picked))
	found := make([]bool, len(picked))
	err = parallel(ctx, picked, func(ctx context.Context, i int, ev sqlc.WatchEvent) error {
		v, err := s.ta.GetVideo(ctx, ev.VideoID)
		if errors.Is(err, ta.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if !includeShorts && v.Kind() == "short" {
			return nil
		}
		out[i] = summarize(*v, &ev)
		found[i] = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	items := []VideoSummary{}
	for i := range out {
		if found[i] {
			items = append(items, out[i])
		}
	}
	// A dismissed video does not come back through the in-progress view
	// either: the viewer said they are not watching it, half-watched or not.
	// (`summarize` above knows nothing about dismissal, so this pass both
	// fills the flag in and drops the rows.)
	ids := make([]string, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.ID)
	}
	dismissed, err := s.loadDismissed(ctx, uid, ids)
	if err != nil {
		return nil, err
	}
	kept := items[:0]
	for _, it := range items {
		if dismissed[it.ID] {
			continue
		}
		kept = append(kept, it)
	}
	// This list is built from watch events rather than through `overlay`, so
	// the titles and thumbnails have to be asked for here too — otherwise the
	// in-progress head of a feed is the one row on the page still carrying
	// the archive's own name for a video.
	if err := s.brandList(ctx, uid, kept); err != nil {
		return nil, err
	}
	return kept, nil
}

// brandList applies a viewer's DeArrow preferences to summaries built outside
// `overlay` — the in-progress list and history, which start from the user's
// own events rather than from a TubeArchivist page.
func (s *Server) brandList(ctx context.Context, uid uuid.UUID, items []VideoSummary) error {
	if s.dearrow == nil || len(items) == 0 {
		return nil
	}
	prefs, err := s.prefsFor(ctx, uid)
	if err != nil {
		return err
	}
	s.applyBranding(ctx, prefs, items)
	return nil
}

// ---- video endpoints ----

func (s *Server) getVideo(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r.Context())
	id := chi.URLParam(r, "id")
	v, err := s.ta.GetVideo(r.Context(), id)
	if err != nil {
		s.writeTAError(w, "get video", err)
		return
	}
	// The SponsorBlock lookup is a network round trip of its own; run it
	// alongside the watch state and the channel summary rather than after
	// them. It never fails the request — it falls back to TA's snapshot.
	sponsorCh := make(chan []SponsorSegment, 1)
	go func() { sponsorCh <- s.sponsorSegments(r.Context(), v) }()
	items, err := s.overlay(r.Context(), uid, []ta.Video{*v})
	if err != nil {
		s.writeDBError(w, "load watch state", err)
		return
	}
	ch, err := s.channelSummary(r.Context(), uid, v.Channel)
	if err != nil {
		s.writeTAError(w, "channel summary", err)
		return
	}
	detail := VideoDetail{
		VideoSummary: items[0],
		Description:  v.Description,
		Height:       v.Height(),
		MediaURL:     "/media/video/" + v.YoutubeID + ".mp4",
		AudioURL:     "/media/audio/" + v.YoutubeID + media.AudioExt,
		AudioAACURL:  "/media/audio/" + v.YoutubeID + media.AudioAACExt,
		PreviewURL:   previewTrackURL(v.YoutubeID),
		HLSURL:       hlsURL(v.YoutubeID, media.HLSDefaultOffered(v.Height())),
		HLSState:     string(s.hlsState(v.YoutubeID, media.HLSDefaultOffered(v.Height()))),
		HLSVariants:  s.hlsVariants(v.YoutubeID, v.Height()),
		YoutubeURL:   "https://www.youtube.com/watch?v=" + v.YoutubeID,
		Streams:      []StreamInfo{},
		Subtitles:    []SubtitleTrack{},
		Sponsorblock: <-sponsorCh,
		Stats:        VideoStats{Views: v.Stats.ViewCount, Likes: v.Stats.LikeCount},
		Tags:         v.Tags,
		Playlists:    []VideoPlaylistRef{},
		Channel:      *ch,
	}
	if detail.Tags == nil {
		detail.Tags = []string{}
	}
	for _, st := range v.Streams {
		detail.Streams = append(detail.Streams, StreamInfo{
			Type:    st.Type,
			Codec:   st.Codec,
			Width:   st.Width,
			Height:  st.Height,
			Bitrate: st.Bitrate,
		})
	}
	for _, st := range v.Subtitles {
		if st.Lang == "" {
			continue
		}
		detail.Subtitles = append(detail.Subtitles, SubtitleTrack{Lang: st.Lang, Source: st.Source, URL: "/media/subtitles/" + v.YoutubeID + "/" + st.Lang + ".vtt"})
	}
	// Playlist membership: name/position/count need the playlist documents.
	refs := make([]VideoPlaylistRef, len(v.Playlist))
	ok := make([]bool, len(v.Playlist))
	err = parallel(r.Context(), v.Playlist, func(ctx context.Context, i int, pid string) error {
		p, err := s.ta.GetPlaylist(ctx, pid)
		if errors.Is(err, ta.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		ref := VideoPlaylistRef{ID: p.PlaylistID, Name: p.PlaylistName, Count: len(p.PlaylistEntries)}
		for idx, e := range p.PlaylistEntries {
			if e.YoutubeID == v.YoutubeID {
				ref.Position = idx
			}
		}
		refs[i], ok[i] = ref, true
		return nil
	})
	if err != nil {
		s.writeTAError(w, "video playlists", err)
		return
	}
	for i := range refs {
		if ok[i] {
			detail.Playlists = append(detail.Playlists, refs[i])
		}
	}
	writeJSON(w, http.StatusOK, detail)
}

func (s *Server) similarVideos(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r.Context())
	vids, err := s.ta.SimilarVideos(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.writeTAError(w, "similar videos", err)
		return
	}
	items, err := s.overlay(r.Context(), uid, vids)
	if err != nil {
		s.writeDBError(w, "load watch state", err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

// upNext returns what plays after the video in the given context (feed,
// playlist or channel), falling back to TA's similar videos.
// contextList resolves the ordered list the player is playing through — the
// playlist, feed or channel named in the query. Empty when the video was
// opened without a context.
func (s *Server) contextList(r *http.Request, uid uuid.UUID) ([]VideoSummary, error) {
	q := r.URL.Query()
	var (
		items []VideoSummary
		err   error
	)
	switch {
	case q.Get("feed") != "":
		items, err = s.upNextInFeed(r, uid, q.Get("feed"))
	case q.Get("playlist") != "":
		items, err = s.upNextInPlaylist(r, uid, q.Get("playlist"))
	case q.Get("channel") != "":
		items, err = s.buildList(r.Context(), uid, listOpts{ChannelIDs: []string{q.Get("channel")}, Sort: "newest", IncludeShorts: true})
	default:
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if seed := q.Get("shuffle"); seed != "" {
		shuffleBySeed(items, seed)
	}
	return items, nil
}

// shuffleBySeed orders items by hash(seed, video id) instead of permuting
// positions. Two properties matter: the same seed always yields the same
// order (so previous/next/autoplay agree across requests and reloads), and an
// item appearing or disappearing — a playlist edit, a video newly marked seen
// in a hide-seen feed — leaves the order of everything else untouched.
func shuffleBySeed(items []VideoSummary, seed string) {
	key := func(id string) uint64 {
		h := fnv.New64a()
		_, _ = h.Write([]byte(seed))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(id))
		return h.Sum64()
	}
	slices.SortStableFunc(items, func(a, b VideoSummary) int {
		if c := cmp.Compare(key(a.ID), key(b.ID)); c != 0 {
			return c
		}
		return cmp.Compare(a.ID, b.ID) // ties: keep it deterministic
	})
}

// videoNav gives the player its previous / next neighbours in the current
// context, so a playlist can be stepped through in both directions.
func (s *Server) videoNav(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r.Context())
	id := chi.URLParam(r, "id")
	items, err := s.contextList(r, uid)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		s.writeTAError(w, "video nav", err)
		return
	}
	out := NavResponse{Index: -1, Total: len(items)}
	if len(items) > 0 {
		first := items[0]
		out.First = &first
	}
	for i, it := range items {
		if it.ID != id {
			continue
		}
		out.Index = i
		if i > 0 {
			prev := items[i-1]
			out.Previous = &prev
		}
		if i+1 < len(items) {
			next := items[i+1]
			out.Next = &next
		}
		break
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) upNext(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r.Context())
	id := chi.URLParam(r, "id")
	items, err := s.contextList(r, uid)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		s.writeTAError(w, "up next", err)
		return
	}
	next := afterID(items, id)
	if len(next) == 0 {
		// No context, or the current video is last: suggest something rather
		// than ending on an empty panel. Similar videos are a flat list with
		// nothing after them, so they only ever fill the first page.
		vids, err := s.ta.SimilarVideos(r.Context(), id)
		if err != nil {
			s.writeTAError(w, "up next", err)
			return
		}
		if next, err = s.overlay(r.Context(), uid, vids); err != nil {
			s.writeDBError(w, "load watch state", err)
			return
		}
	}
	writeJSON(w, http.StatusOK, slicePage(next, parsePaging(r)))
}

// afterID returns every item following id; when id isn't in the list (already
// hidden as seen, say) the whole list minus id. The caller paginates — a long
// playlist should be scrollable, not truncated at an arbitrary cut-off.
func afterID(items []VideoSummary, id string) []VideoSummary {
	out := []VideoSummary{}
	idx := -1
	for i, it := range items {
		if it.ID == id {
			idx = i
			break
		}
	}
	for _, it := range items[idx+1:] {
		if it.ID == id {
			continue
		}
		out = append(out, it)
	}
	return out
}

func (s *Server) upNextInFeed(r *http.Request, uid uuid.UUID, feedID string) ([]VideoSummary, error) {
	o, err := s.feedListOpts(r, uid, feedID)
	if err != nil {
		return nil, err
	}
	return s.buildList(r.Context(), uid, o)
}

func (s *Server) upNextInPlaylist(r *http.Request, uid uuid.UUID, playlistID string) ([]VideoSummary, error) {
	p, err := s.ta.GetPlaylist(r.Context(), playlistID)
	if err != nil {
		return nil, err
	}
	items, err := s.playlistVideos(r.Context(), uid, p)
	if err != nil {
		return nil, err
	}
	out := make([]VideoSummary, 0, len(items))
	for _, it := range items {
		out = append(out, it.Video)
	}
	return out, nil
}

// ---- progress / watched ----

// isComplete applies the heartbeat rule: ≥90 % or ≤30 s remaining.
func isComplete(position float64, duration int) bool {
	if duration <= 0 {
		return false
	}
	d := float64(duration)
	return position/d >= 0.9 || d-position <= 30
}

func (s *Server) postProgress(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r.Context())
	id := chi.URLParam(r, "id")
	var req struct {
		Position float64 `json:"position"`
	}
	if err := decodeBody(r, &req); err != nil || req.Position < 0 {
		writeError(w, http.StatusBadRequest, "position is required")
		return
	}
	v, err := s.ta.GetVideo(r.Context(), id)
	if err != nil {
		s.writeTAError(w, "get video", err)
		return
	}
	duration := int(v.Player.Duration)
	if duration > 0 {
		req.Position = min(req.Position, float64(duration))
	}
	// Music carries no watch state: a song is replayed, so "seen" means
	// nothing, and recording it would fill history and continue-watching with
	// tracks. The client reports which playlist it is playing from and the
	// server decides, so every client behaves the same way.
	if playlistID := r.URL.Query().Get("playlist"); playlistID != "" {
		music, err := s.isMusicPlaylist(r.Context(), uid, playlistID)
		if err != nil {
			s.writeDBError(w, "load playlist settings", err)
			return
		}
		if music {
			writeJSON(w, http.StatusOK, map[string]any{"position": req.Position, "watched": false})
			return
		}
	}
	watched := isComplete(req.Position, duration)
	// Below the minimum play time nothing is recorded, so a video opened by
	// accident leaves no history entry and no resume position. An event that
	// already exists keeps updating, and completion always records.
	if !watched && req.Position < s.minPlaySeconds {
		_, err := s.q.GetWatchEvent(r.Context(), sqlc.GetWatchEventParams{UserID: uid, VideoID: id})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			writeJSON(w, http.StatusOK, map[string]any{"position": req.Position, "watched": false})
			return
		case err != nil:
			s.writeDBError(w, "load watch state", err)
			return
		}
	}
	ev, err := s.q.UpsertProgress(r.Context(), sqlc.UpsertProgressParams{
		UserID:      uid,
		VideoID:     id,
		ChannelID:   v.Channel.ChannelID,
		ChannelName: v.Channel.ChannelName,
		Title:       v.Title,
		Position:    req.Position,
		Duration:    int32(duration), //nolint:gosec // seconds; fits
		Completed:   watched,
	})
	if err != nil {
		s.writeDBError(w, "save progress", err)
		return
	}
	if err := s.ta.SetProgress(r.Context(), id, req.Position); err != nil {
		s.writeTAError(w, "write progress", err)
		return
	}
	if watched && !v.Player.Watched {
		if err := s.ta.SetWatched(r.Context(), id, true); err != nil {
			s.writeTAError(w, "mark watched", err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"position": ev.Position, "watched": ev.CompletedAt.Valid})
}

// isMusicPlaylist reports whether the user marked this playlist as music.
func (s *Server) isMusicPlaylist(ctx context.Context, uid uuid.UUID, playlistID string) (bool, error) {
	settings, err := s.playlistSettings(ctx, uid)
	if err != nil {
		return false, err
	}
	return settings[playlistID].Music, nil
}

func (s *Server) postWatched(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r.Context())
	id := chi.URLParam(r, "id")
	var req struct {
		Watched *bool `json:"watched"`
	}
	if err := decodeBody(r, &req); err != nil || req.Watched == nil {
		writeError(w, http.StatusBadRequest, "watched is required")
		return
	}
	v, err := s.ta.GetVideo(r.Context(), id)
	if err != nil {
		s.writeTAError(w, "get video", err)
		return
	}
	ev, err := s.q.SetWatched(r.Context(), sqlc.SetWatchedParams{
		UserID:      uid,
		VideoID:     id,
		ChannelID:   v.Channel.ChannelID,
		ChannelName: v.Channel.ChannelName,
		Title:       v.Title,
		Duration:    int32(v.Player.Duration), //nolint:gosec // seconds; fits
		Watched:     *req.Watched,
	})
	if err != nil {
		s.writeDBError(w, "save watched", err)
		return
	}
	if err := s.ta.SetWatched(r.Context(), id, *req.Watched); err != nil {
		s.writeTAError(w, "write watched", err)
		return
	}
	if !*req.Watched {
		if err := s.ta.DeleteProgress(r.Context(), id); err != nil {
			s.writeTAError(w, "clear progress", err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"position": ev.Position, "watched": ev.CompletedAt.Valid})
}

func (s *Server) deleteProgress(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r.Context())
	id := chi.URLParam(r, "id")
	if err := s.q.ResetPosition(r.Context(), sqlc.ResetPositionParams{UserID: uid, VideoID: id}); err != nil {
		s.writeDBError(w, "reset progress", err)
		return
	}
	if err := s.ta.DeleteProgress(r.Context(), id); err != nil {
		s.writeTAError(w, "clear progress", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// markAllSeen flags every unseen video of a list watched in TA and Flimm.
func (s *Server) markAllSeen(ctx context.Context, uid uuid.UUID, items []VideoSummary) error {
	return parallel(ctx, items, func(ctx context.Context, _ int, it VideoSummary) error {
		if it.Watched {
			return nil
		}
		if _, err := s.q.SetWatched(ctx, sqlc.SetWatchedParams{
			UserID: uid, VideoID: it.ID, ChannelID: it.Channel.ID, ChannelName: it.Channel.Name,
			Title: it.Title, Duration: int32(it.Duration), Watched: true, //nolint:gosec // seconds; fits
		}); err != nil {
			return err
		}
		return s.ta.SetWatched(ctx, it.ID, true)
	})
}

func lower(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
