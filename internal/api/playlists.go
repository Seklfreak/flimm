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

	"github.com/Seklfreak/archive-client/internal/ta"
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

// playlistVideos resolves a playlist's entries to per-user summaries in
// playlist order. TA's playlist filter on the video list is tried first (one
// round trip per 100 entries); entries missing there are fetched by id.
func (s *Server) playlistVideos(ctx context.Context, uid uuid.UUID, p *ta.Playlist) ([]PlaylistItem, error) {
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

func (s *Server) playlistSummary(ctx context.Context, uid uuid.UUID, p *ta.Playlist) (*PlaylistSummary, []PlaylistItem, error) {
	items, err := s.playlistVideos(ctx, uid, p)
	if err != nil {
		return nil, nil, err
	}
	out := &PlaylistSummary{
		ID:         p.PlaylistID,
		Name:       p.PlaylistName,
		Kind:       playlistKind(*p),
		ThumbURL:   playlistThumbURL(p.PlaylistID),
		VideoCount: len(items),
	}
	if p.PlaylistChannelID != "" {
		out.Channel = &PlaylistChannelRef{ID: p.PlaylistChannelID, Name: p.PlaylistChannel}
	}
	var firstInProgress, firstUnseen *string
	for i := range items {
		v := items[i].Video
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
	return out, items, nil
}

func (s *Server) playlistSummaries(ctx context.Context, uid uuid.UUID, lists []ta.Playlist) ([]PlaylistSummary, error) {
	out := make([]PlaylistSummary, len(lists))
	err := parallel(ctx, lists, func(ctx context.Context, i int, p ta.Playlist) error {
		sum, _, err := s.playlistSummary(ctx, uid, &p)
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
	sum, _, err := s.playlistSummary(r.Context(), uid, p)
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
	sum, _, err := s.playlistSummary(r.Context(), uid, fresh)
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
