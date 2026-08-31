package api

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Seklfreak/flimm/internal/db/sqlc"
	"github.com/Seklfreak/flimm/internal/ta"
)

func timeOrZero(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

func playlistKind(p ta.Playlist) string {
	if p.PlaylistType == "custom" {
		return "custom"
	}
	return "channel"
}

// playlistVideoDocs resolves a playlist's entries to TA video documents in
// playlist order. TA's playlist filter on the video list is tried first (one
// round trip per 100 entries); entries missing there are fetched by id.
//
// This is the expensive half of a playlist — a page of documents per hundred
// videos — and the only caller that needs the documents themselves is the
// detail view. A summary wants six integers, and gets them from the cached
// aggregate instead; see playlistcache.go.
func (s *Server) playlistVideoDocs(ctx context.Context, p *ta.Playlist) ([]ta.Video, error) {
	byID := map[string]ta.Video{}
	if len(p.PlaylistEntries) > 0 {
		vids, err := s.fetchAll(ctx, ta.VideoQuery{Playlist: p.PlaylistID})
		if err != nil {
			return nil, err
		}
		for _, v := range vids {
			byID[v.YoutubeID] = v
		}
	}
	var missing []string
	for _, e := range p.PlaylistEntries {
		if _, ok := byID[e.YoutubeID]; !ok && e.Downloaded {
			missing = append(missing, e.YoutubeID)
		}
	}
	if len(missing) > 0 && len(missing) <= maxListVideos {
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
	}
	ordered := make([]ta.Video, 0, len(p.PlaylistEntries))
	for _, e := range p.PlaylistEntries {
		if v, ok := byID[e.YoutubeID]; ok {
			ordered = append(ordered, v)
		}
	}
	return ordered, nil
}

// playlistVideos resolves a playlist's entries to per-user summaries in
// playlist order.
func (s *Server) playlistVideos(ctx context.Context, uid uuid.UUID, p *ta.Playlist) ([]PlaylistItem, error) {
	ordered, err := s.playlistVideoDocs(ctx, p)
	if err != nil {
		return nil, err
	}
	// Having paid for the documents, record what a summary would have needed,
	// so the next one does not have to.
	s.savePlaylistAggregate(ctx, p, ordered)
	items, err := s.overlay(ctx, uid, ordered)
	if err != nil {
		return nil, err
	}
	out := make([]PlaylistItem, 0, len(items))
	for i, it := range items {
		out = append(out, PlaylistItem{Position: i, Video: it})
	}
	return out, nil
}

// playlistShell is the part of a summary that needs nothing but the playlist
// itself.
func playlistShell(p *ta.Playlist) *PlaylistSummary {
	out := &PlaylistSummary{
		ID:       p.PlaylistID,
		Name:     p.PlaylistName,
		Kind:     playlistKind(*p),
		ThumbURL: playlistThumbURL(p.PlaylistID),
		Feeds:    []FeedRef{},
	}
	if p.PlaylistChannelID != "" {
		out.Channel = &PlaylistChannelRef{ID: p.PlaylistChannelID, Name: p.PlaylistChannel}
	}
	return out
}

// tallyPlaylist counts a playlist's videos into its summary: how long it is,
// how much of it is seen, and where to resume.
//
// Both paths that build a summary run this same function over the same
// per-user VideoSummary values — the cheap one over videos rebuilt from the
// cache, the detail one over the real documents — so the two cannot report a
// playlist differently.
func tallyPlaylist(out *PlaylistSummary, videos []VideoSummary) {
	out.VideoCount = len(videos)
	var firstInProgress, firstUnseen *string
	for i := range videos {
		v := videos[i]
		out.TotalDuration += v.Duration
		switch {
		case v.Watched:
			out.SeenCount++
		case v.Position > 0:
			out.InProgressCount++
			if firstInProgress == nil {
				id := v.ID
				firstInProgress = &id
			}
		default:
			if firstUnseen == nil {
				id := v.ID
				firstUnseen = &id
			}
		}
	}
	if out.VideoCount > 0 {
		out.Progress = float64(out.SeenCount) / float64(out.VideoCount)
	}
	out.ResumeVideoID = firstInProgress
	if out.ResumeVideoID == nil {
		out.ResumeVideoID = firstUnseen
	}
}

// playlistSummary builds a summary and the items behind it. Only the detail
// view wants the items; everything else should call playlistSummaryOnly, which
// does not fetch them.
func (s *Server) playlistSummary(ctx context.Context, uid uuid.UUID, p *ta.Playlist) (*PlaylistSummary, []PlaylistItem, error) {
	items, err := s.playlistVideos(ctx, uid, p)
	if err != nil {
		return nil, nil, err
	}
	videos := make([]VideoSummary, len(items))
	for i := range items {
		videos[i] = items[i].Video
	}
	out := playlistShell(p)
	tallyPlaylist(out, videos)
	return out, items, nil
}

func (s *Server) playlistSummaries(ctx context.Context, uid uuid.UUID, lists []ta.Playlist) ([]PlaylistSummary, error) {
	out := make([]PlaylistSummary, len(lists))
	err := parallel(ctx, lists, func(ctx context.Context, i int, p ta.Playlist) error {
		sum, err := s.playlistSummaryOnly(ctx, uid, &p)
		if err != nil {
			return err
		}
		out[i] = *sum
		return nil
	})
	if out == nil {
		out = []PlaylistSummary{}
	}
	return out, err
}

// playlistFeedRefs maps playlist id → feeds holding it as a source, the same
// badge channels carry.
func (s *Server) playlistFeedRefs(ctx context.Context, uid uuid.UUID) (map[string][]FeedRef, error) {
	rows, err := s.q.ListFeedPlaylistsForUser(ctx, uid)
	if err != nil {
		return nil, err
	}
	out := map[string][]FeedRef{}
	for _, r := range rows {
		out[r.PlaylistID] = append(out[r.PlaylistID], FeedRef{ID: r.FeedID.String(), Name: r.FeedName})
	}
	return out, nil
}

// attachPlaylistFeeds stamps summaries with the feeds that hold them.
func (s *Server) attachPlaylistFeeds(ctx context.Context, uid uuid.UUID, items []PlaylistSummary) error {
	refs, err := s.playlistFeedRefs(ctx, uid)
	if err != nil {
		return err
	}
	for i := range items {
		if r := refs[items[i].ID]; r != nil {
			items[i].Feeds = r
		}
	}
	return nil
}

// playlistSettings is the user's per-playlist state, for stamping onto
// summaries. Absent means every setting is off.
func (s *Server) playlistSettings(ctx context.Context, uid uuid.UUID) (map[string]sqlc.PlaylistSetting, error) {
	rows, err := s.q.ListPlaylistSettings(ctx, uid)
	if err != nil {
		return nil, err
	}
	out := make(map[string]sqlc.PlaylistSetting, len(rows))
	for _, r := range rows {
		out[r.PlaylistID] = r
	}
	return out, nil
}

// setPlaylistFlag applies one boolean setting, then drops the row if nothing
// is set any more so the table only ever holds real intent.
func (s *Server) setPlaylistFlag(w http.ResponseWriter, r *http.Request, field string, want *bool) {
	uid := currentUserID(r.Context())
	id := chi.URLParam(r, "id")
	if *want {
		// Refuse a setting for a playlist TA doesn't have, so the sidebar and
		// audio mode can't accumulate references that never resolve.
		if _, err := s.ta.GetPlaylist(r.Context(), id); err != nil {
			s.writeTAError(w, "get playlist", err)
			return
		}
	}
	var err error
	switch field {
	case "pinned":
		err = s.q.SetPlaylistPinned(r.Context(), sqlc.SetPlaylistPinnedParams{UserID: uid, PlaylistID: id, Pinned: *want})
	case "music":
		err = s.q.SetPlaylistMusic(r.Context(), sqlc.SetPlaylistMusicParams{UserID: uid, PlaylistID: id, Music: *want})
	}
	if err != nil {
		s.writeDBError(w, "save playlist setting", err)
		return
	}
	if !*want {
		if err := s.q.PruneEmptyPlaylistSettings(r.Context(), uid); err != nil {
			s.writeDBError(w, "prune playlist settings", err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// listPinnedPlaylists backs the sidebar. Pins name TubeArchivist ids that TA
// owns, so one that no longer resolves is skipped rather than surfaced as an
// error — a playlist deleted in TA must not wedge the sidebar.
func (s *Server) listPinnedPlaylists(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r.Context())
	rows, err := s.q.ListPinnedPlaylists(r.Context(), uid)
	if err != nil {
		s.writeDBError(w, "list pinned playlists", err)
		return
	}
	out := make([]PlaylistSummary, 0, len(rows))
	for _, row := range rows {
		p, err := s.ta.GetPlaylist(r.Context(), row.PlaylistID)
		if errors.Is(err, ta.ErrNotFound) {
			continue
		}
		if err != nil {
			s.writeTAError(w, "get playlist", err)
			return
		}
		sum, err := s.playlistSummaryOnly(r.Context(), uid, p)
		if err != nil {
			s.writeTAError(w, "playlist summary", err)
			return
		}
		sum.Pinned = true
		sum.Music = row.Music
		out = append(out, *sum)
	}
	if err := s.attachPlaylistFeeds(r.Context(), uid, out); err != nil {
		s.writeDBError(w, "list feed playlists", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// setPlaylistFeeds is the playlist's "In feeds:" control: replaces the
// playlist's feed-source memberships with the given set of the user's feeds.
// The mirror of setChannelFeeds, with the same 404-not-403 scoping.
func (s *Server) setPlaylistFeeds(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r.Context())
	id := chi.URLParam(r, "id")
	var req struct {
		FeedIDs []string `json:"feed_ids"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	feedIDs := make([]uuid.UUID, 0, len(req.FeedIDs))
	for _, raw := range dedupe(req.FeedIDs) {
		if raw == everythingFeedID {
			continue
		}
		fid, err := uuid.Parse(raw)
		if err != nil {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		if _, err := s.q.GetFeed(r.Context(), sqlc.GetFeedParams{ID: fid, UserID: uid}); err != nil {
			s.writeDBError(w, "get feed", err)
			return
		}
		feedIDs = append(feedIDs, fid)
	}
	err := s.withTx(r.Context(), func(q sqlc.Querier) error {
		if err := q.DeletePlaylistFromUserFeeds(r.Context(), sqlc.DeletePlaylistFromUserFeedsParams{UserID: uid, PlaylistID: id}); err != nil {
			return err
		}
		for _, fid := range feedIDs {
			pos, err := q.NextFeedPlaylistPosition(r.Context(), fid)
			if err != nil {
				return err
			}
			if err := q.AddFeedPlaylist(r.Context(), sqlc.AddFeedPlaylistParams{FeedID: fid, PlaylistID: id, Position: pos}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		s.writeDBError(w, "set playlist feeds", err)
		return
	}
	refs, err := s.playlistFeedRefs(r.Context(), uid)
	if err != nil {
		s.writeDBError(w, "list feed playlists", err)
		return
	}
	feeds := refs[id]
	if feeds == nil {
		feeds = []FeedRef{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"feeds": feeds})
}

func (s *Server) setPlaylistPinned(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Pinned *bool `json:"pinned"`
	}
	if err := decodeBody(r, &req); err != nil || req.Pinned == nil {
		writeError(w, http.StatusBadRequest, "pinned is required")
		return
	}
	s.setPlaylistFlag(w, r, "pinned", req.Pinned)
}

func (s *Server) setPlaylistMusic(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Music *bool `json:"music"`
	}
	if err := decodeBody(r, &req); err != nil || req.Music == nil {
		writeError(w, http.StatusBadRequest, "music is required")
		return
	}
	s.setPlaylistFlag(w, r, "music", req.Music)
}

// clearWatchState strips watch-derived fields from a music playlist and its
// items. Songs are replayed, so seen counts, progress and a resume point are
// noise — reporting them would force every client to special-case music.
func clearWatchState(sum *PlaylistSummary, items []PlaylistItem) {
	sum.SeenCount, sum.InProgressCount, sum.Progress, sum.ResumeVideoID = 0, 0, 0, nil
	for i := range items {
		items[i].Video.Watched = false
		items[i].Video.Position = 0
		items[i].Video.Progress = 0
		items[i].Video.LastPlayedAt = nil
	}
}

func (s *Server) listPlaylists(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r.Context())
	kind := ""
	switch r.URL.Query().Get("kind") {
	case "custom":
		kind = "custom"
	case "channel":
		kind = "regular"
	}
	lists, err := s.ta.ListPlaylists(r.Context(), kind, "")
	if err != nil {
		s.writeTAError(w, "list playlists", err)
		return
	}
	settings, err := s.playlistSettings(r.Context(), uid)
	if err != nil {
		s.writeDBError(w, "list playlist settings", err)
		return
	}
	// A channel's own playlists belong to its channel page. One shows up here
	// only once the viewer has taken it up — pinned it, or marked it music —
	// so an archive that indexes every playlist a prolific channel owns cannot
	// flood this page with lists nobody asked for.
	kept := lists[:0]
	for _, p := range lists {
		st := settings[p.PlaylistID]
		if p.PlaylistType == "custom" || st.Pinned || st.Music {
			kept = append(kept, p)
		}
	}
	lists = kept
	sort.SliceStable(lists, func(i, j int) bool {
		ci, cj := lists[i].PlaylistType == "custom", lists[j].PlaylistType == "custom"
		if ci != cj {
			return ci
		}
		return strings.ToLower(lists[i].PlaylistName) < strings.ToLower(lists[j].PlaylistName)
	})
	p := parsePaging(r)
	window := slicePage(lists, p)
	items, err := s.playlistSummaries(r.Context(), uid, window.Items)
	if err != nil {
		s.writeTAError(w, "playlist summaries", err)
		return
	}
	for i := range items {
		st := settings[items[i].ID]
		items[i].Pinned, items[i].Music = st.Pinned, st.Music
	}
	if err := s.attachPlaylistFeeds(r.Context(), uid, items); err != nil {
		s.writeDBError(w, "list feed playlists", err)
		return
	}
	writeJSON(w, http.StatusOK, Page[PlaylistSummary]{Items: items, Page: p.Page, PageSize: p.Size, Total: window.Total})
}

func (s *Server) createPlaylist(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r.Context())
	var req struct {
		Name string `json:"name"`
	}
	if err := decodeBody(r, &req); err != nil || strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	p, err := s.ta.CreateCustomPlaylist(r.Context(), strings.TrimSpace(req.Name))
	if err != nil {
		s.writeTAError(w, "create playlist", err)
		return
	}
	sum, err := s.playlistSummaryOnly(r.Context(), uid, p)
	if err != nil {
		s.writeTAError(w, "playlist summary", err)
		return
	}
	writeJSON(w, http.StatusCreated, sum)
}

func (s *Server) getPlaylist(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r.Context())
	p, err := s.ta.GetPlaylist(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.writeTAError(w, "get playlist", err)
		return
	}
	sum, items, err := s.playlistSummary(r.Context(), uid, p)
	if err != nil {
		s.writeTAError(w, "playlist summary", err)
		return
	}
	settings, err := s.playlistSettings(r.Context(), uid)
	if err != nil {
		s.writeDBError(w, "list playlist settings", err)
		return
	}
	sum.Pinned, sum.Music = settings[sum.ID].Pinned, settings[sum.ID].Music
	refs, err := s.playlistFeedRefs(r.Context(), uid)
	if err != nil {
		s.writeDBError(w, "list feed playlists", err)
		return
	}
	if r := refs[sum.ID]; r != nil {
		sum.Feeds = r
	}
	if sum.Music {
		clearWatchState(sum, items)
	}
	writeJSON(w, http.StatusOK, PlaylistDetail{PlaylistSummary: *sum, Items: items})
}

// renamePlaylist: TA has no rename for custom playlists, so the playlist is
// re-created under the new name with the same videos in order and the old
// one deleted. The id changes — the response carries the new one.
func (s *Server) renamePlaylist(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r.Context())
	id := chi.URLParam(r, "id")
	var req struct {
		Name string `json:"name"`
	}
	if err := decodeBody(r, &req); err != nil || strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	old, err := s.ta.GetPlaylist(r.Context(), id)
	if err != nil {
		s.writeTAError(w, "get playlist", err)
		return
	}
	if old.PlaylistType != "custom" {
		writeError(w, http.StatusBadRequest, "only custom playlists can be renamed")
		return
	}
	created, err := s.ta.CreateCustomPlaylist(r.Context(), strings.TrimSpace(req.Name))
	if err != nil {
		s.writeTAError(w, "create playlist", err)
		return
	}
	for _, e := range old.PlaylistEntries {
		if err := s.ta.CustomPlaylistAction(r.Context(), created.PlaylistID, "create", e.YoutubeID); err != nil {
			s.writeTAError(w, "copy playlist entry", err)
			return
		}
	}
	if err := s.ta.DeletePlaylist(r.Context(), old.PlaylistID); err != nil {
		s.writeTAError(w, "delete old playlist", err)
		return
	}
	fresh, err := s.ta.GetPlaylist(r.Context(), created.PlaylistID)
	if err != nil {
		s.writeTAError(w, "get playlist", err)
		return
	}
	sum, err := s.playlistSummaryOnly(r.Context(), uid, fresh)
	if err != nil {
		s.writeTAError(w, "playlist summary", err)
		return
	}
	writeJSON(w, http.StatusOK, sum)
}

func (s *Server) deletePlaylist(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, err := s.ta.GetPlaylist(r.Context(), id)
	if err != nil {
		s.writeTAError(w, "get playlist", err)
		return
	}
	if p.PlaylistType != "custom" {
		writeError(w, http.StatusBadRequest, "only custom playlists can be deleted")
		return
	}
	if err := s.ta.DeletePlaylist(r.Context(), id); err != nil {
		s.writeTAError(w, "delete playlist", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

var playlistActions = map[string]string{
	"add": "create", "remove": "remove", "up": "up", "down": "down", "top": "top", "bottom": "bottom",
}

func (s *Server) playlistVideoAction(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r.Context())
	id := chi.URLParam(r, "id")
	var req struct {
		VideoID string `json:"video_id"`
		Action  string `json:"action"`
	}
	if err := decodeBody(r, &req); err != nil || req.VideoID == "" {
		writeError(w, http.StatusBadRequest, "video_id and action are required")
		return
	}
	taAction, ok := playlistActions[req.Action]
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid action")
		return
	}
	p, err := s.ta.GetPlaylist(r.Context(), id)
	if err != nil {
		s.writeTAError(w, "get playlist", err)
		return
	}
	if p.PlaylistType != "custom" {
		writeError(w, http.StatusBadRequest, "only custom playlists can be edited")
		return
	}
	if err := s.ta.CustomPlaylistAction(r.Context(), id, taAction, req.VideoID); err != nil {
		s.writeTAError(w, "playlist action", err)
		return
	}
	fresh, err := s.ta.GetPlaylist(r.Context(), id)
	if err != nil {
		s.writeTAError(w, "get playlist", err)
		return
	}
	sum, items, err := s.playlistSummary(r.Context(), uid, fresh)
	if err != nil {
		s.writeTAError(w, "playlist summary", err)
		return
	}
	writeJSON(w, http.StatusOK, PlaylistDetail{PlaylistSummary: *sum, Items: items})
}
