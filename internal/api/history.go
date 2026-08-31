package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Seklfreak/flimm/internal/db/sqlc"
	"github.com/Seklfreak/flimm/internal/ta"
)

func historyState(ev sqlc.WatchEvent) string {
	if ev.CompletedAt.Valid {
		return "seen"
	}
	return "in_progress"
}

func (s *Server) listHistory(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r.Context())
	filter := r.URL.Query().Get("filter")
	switch filter {
	case "", "all":
		filter = "all"
	case "in_progress", "seen":
	default:
		writeError(w, http.StatusBadRequest, "invalid filter")
		return
	}
	q := r.URL.Query().Get("q")
	p := parsePaging(r)
	rows, err := s.q.ListHistory(r.Context(), sqlc.ListHistoryParams{
		UserID: uid, Filter: filter, Q: q, MinPosition: s.minPlaySeconds,
		PageLimit: int32(p.Size), PageOffset: int32(p.offset()), //nolint:gosec // bounded by parsePaging
	})
	if err != nil {
		s.writeDBError(w, "list history", err)
		return
	}
	total, err := s.q.CountHistory(r.Context(), sqlc.CountHistoryParams{UserID: uid, Filter: filter, Q: q, MinPosition: s.minPlaySeconds})
	if err != nil {
		s.writeDBError(w, "count history", err)
		return
	}
	homes, err := s.feedHomes(r.Context(), uid)
	if err != nil {
		s.writeDBError(w, "list feeds", err)
		return
	}
	items := make([]HistoryEntry, len(rows))
	err = parallel(r.Context(), rows, func(ctx context.Context, i int, ev sqlc.WatchEvent) error {
		entry := HistoryEntry{ID: ev.ID.String(), PlayedAt: ts(ev.LastPlayedAt), State: historyState(ev)}
		v, err := s.ta.GetVideo(ctx, ev.VideoID)
		switch {
		case err == nil:
			entry.Video = summarize(*v, &ev)
			pid, feed := homes.find(v.Channel.ChannelID, v.Playlist)
			entry.Feed = feed
			if pid != "" {
				entry.PlaylistID = &pid
			}
		case errors.Is(err, ta.ErrNotFound):
			entry.Video = summaryFromEvent(ev)
			// The document is gone, so only the event's channel can match.
			_, entry.Feed = homes.find(ev.ChannelID, nil)
		default:
			return err
		}
		items[i] = entry
		return nil
	})
	if err != nil {
		s.writeTAError(w, "resolve history videos", err)
		return
	}
	// History is built from the viewer's own events, not from a TubeArchivist
	// page, so it misses `overlay` — and a video has to be called the same
	// thing here as in the feed it was played from.
	videos := make([]VideoSummary, len(items))
	for i, it := range items {
		videos[i] = it.Video
	}
	if err := s.brandList(r.Context(), uid, videos); err != nil {
		s.writeDBError(w, "load prefs", err)
		return
	}
	for i := range items {
		items[i].Video = videos[i]
	}
	writeJSON(w, http.StatusOK, Page[HistoryEntry]{Items: items, Page: p.Page, PageSize: p.Size, Total: total})
}

// feedHomes is the user's feeds with their source sets, in sidebar order, for
// answering "which feed does this video belong to" — the context a resume
// from history opens with.
type feedHomes []struct {
	ref   FeedRef
	chans map[string]bool
	pls   map[string]bool
}

// find is the home the video most specifically belongs to. When a feed holds
// the video through a playlist source, the *playlist itself* is the resume
// context — the series is the run being watched, so up next should be its
// next episode, not whatever else the feed carries. Only a plain channel
// match falls back to the feed. Sidebar order breaks ties within each kind.
func (h feedHomes) find(channelID string, playlists []string) (playlistID string, feed *FeedRef) {
	for _, f := range h {
		for _, pl := range playlists {
			if f.pls[pl] {
				ref := f.ref
				return pl, &ref
			}
		}
	}
	for _, f := range h {
		if f.chans[channelID] {
			ref := f.ref
			return "", &ref
		}
	}
	return "", nil
}

func (s *Server) feedHomes(ctx context.Context, uid uuid.UUID) (feedHomes, error) {
	feeds, err := s.q.ListFeeds(ctx, uid)
	if err != nil {
		return nil, err
	}
	chans, err := s.feedChannelMap(ctx, uid)
	if err != nil {
		return nil, err
	}
	pls, err := s.feedPlaylistMap(ctx, uid)
	if err != nil {
		return nil, err
	}
	out := make(feedHomes, 0, len(feeds))
	for _, f := range feeds {
		entry := struct {
			ref   FeedRef
			chans map[string]bool
			pls   map[string]bool
		}{ref: FeedRef{ID: f.ID.String(), Name: f.Name}, chans: map[string]bool{}, pls: map[string]bool{}}
		for _, c := range chans[f.ID] {
			entry.chans[c] = true
		}
		for _, pl := range pls[f.ID] {
			entry.pls[pl] = true
		}
		out = append(out, entry)
	}
	return out, nil
}

func (s *Server) deleteHistoryEntry(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	n, err := s.q.HideHistoryEntry(r.Context(), sqlc.HideHistoryEntryParams{ID: id, UserID: uid})
	if err != nil {
		s.writeDBError(w, "hide history entry", err)
		return
	}
	if n == 0 {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
