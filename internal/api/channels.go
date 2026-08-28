package api

import (
	"context"
	"net/http"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Seklfreak/flimm/internal/db/sqlc"
	"github.com/Seklfreak/flimm/internal/ta"
)

// channelFeedRefs maps channel id → the user's feeds containing it.
func (s *Server) channelFeedRefs(ctx context.Context, uid uuid.UUID) (map[string][]FeedRef, error) {
	rows, err := s.q.ListFeedChannelsForUser(ctx, uid)
	if err != nil {
		return nil, err
	}
	out := map[string][]FeedRef{}
	for _, r := range rows {
		out[r.ChannelID] = append(out[r.ChannelID], FeedRef{ID: r.FeedID.String(), Name: r.FeedName})
	}
	return out, nil
}

// enrichChannel fills counts and last upload from TA (cached there).
func (s *Server) enrichChannel(ctx context.Context, c ta.Channel, feeds []FeedRef) (*ChannelSummary, error) {
	stats, err := s.ta.ChannelStats(ctx, c.ChannelID)
	if err != nil {
		return nil, err
	}
	unseen, err := s.ta.UnseenCount(ctx, c.ChannelID)
	if err != nil {
		return nil, err
	}
	if feeds == nil {
		feeds = []FeedRef{}
	}
	out := &ChannelSummary{
		ID:          c.ChannelID,
		Name:        c.ChannelName,
		ThumbURL:    channelThumbURL(c.ChannelID),
		BannerURL:   channelBannerURL(c.ChannelID),
		VideoCount:  stats.VideoCount,
		UnseenCount: unseen,
		Subscribed:  c.ChannelSubscribed,
		Feeds:       feeds,
	}
	if !stats.LastUpload.IsZero() {
		t := stats.LastUpload
		out.LastUpload = &t
	}
	return out, nil
}

func (s *Server) channelSummary(ctx context.Context, uid uuid.UUID, c ta.Channel) (*ChannelSummary, error) {
	refs, err := s.channelFeedRefs(ctx, uid)
	if err != nil {
		return nil, err
	}
	return s.enrichChannel(ctx, c, refs[c.ChannelID])
}

func (s *Server) listChannels(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r.Context())
	q := r.URL.Query()
	all, err := s.ta.ListChannels(r.Context())
	if err != nil {
		s.writeTAError(w, "list channels", err)
		return
	}
	refs, err := s.channelFeedRefs(r.Context(), uid)
	if err != nil {
		s.writeDBError(w, "list feed channels", err)
		return
	}
	needle := lower(q.Get("q"))
	unfeeded := q.Get("unfeeded") == "true"
	var picked []ta.Channel
	for _, c := range all {
		if needle != "" && !strings.Contains(strings.ToLower(c.ChannelName), needle) {
			continue
		}
		if unfeeded && len(refs[c.ChannelID]) > 0 {
			continue
		}
		picked = append(picked, c)
	}
	items := make([]ChannelSummary, len(picked))
	err = parallel(r.Context(), picked, func(ctx context.Context, i int, c ta.Channel) error {
		cs, err := s.enrichChannel(ctx, c, refs[c.ChannelID])
		if err != nil {
			return err
		}
		items[i] = *cs
		return nil
	})
	if err != nil {
		s.writeTAError(w, "channel stats", err)
		return
	}
	sortChannels(items, q.Get("sort"))
	writeJSON(w, http.StatusOK, slicePage(items, parsePaging(r)))
}

func sortChannels(items []ChannelSummary, key string) {
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		switch key {
		case "videos":
			if a.VideoCount != b.VideoCount {
				return a.VideoCount > b.VideoCount
			}
		case "unseen":
			if a.UnseenCount != b.UnseenCount {
				return a.UnseenCount > b.UnseenCount
			}
		case "last_upload":
			at, bt := timeOrZero(a.LastUpload), timeOrZero(b.LastUpload)
			if !at.Equal(bt) {
				return at.After(bt)
			}
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
}

func (s *Server) getChannel(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r.Context())
	c, err := s.ta.GetChannel(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.writeTAError(w, "get channel", err)
		return
	}
	cs, err := s.channelSummary(r.Context(), uid, *c)
	if err != nil {
		s.writeTAError(w, "channel summary", err)
		return
	}
	writeJSON(w, http.StatusOK, ChannelDetail{ChannelSummary: *cs, Description: c.ChannelDescription})
}

func (s *Server) listChannelVideos(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r.Context())
	id := chi.URLParam(r, "id")
	if _, err := s.ta.GetChannel(r.Context(), id); err != nil {
		s.writeTAError(w, "get channel", err)
		return
	}
	sortKey := r.URL.Query().Get("sort")
	if sortKey == "" {
		sortKey = "newest"
	}
	if !validSorts[sortKey] {
		writeError(w, http.StatusBadRequest, "invalid sort")
		return
	}
	p := parsePaging(r)
	prefix, more, err := s.buildWindow(r.Context(), uid, listOpts{
		ChannelIDs:    []string{id},
		Sort:          sortKey,
		IncludeShorts: true,
		UnseenOnly:    r.URL.Query().Get("view") == "unseen",
	}, p.offset()+p.Size+1)
	if err != nil {
		s.writeTAError(w, "list channel videos", err)
		return
	}
	writeJSON(w, http.StatusOK, windowPage(prefix, more, p))
}

func (s *Server) listChannelPlaylists(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r.Context())
	id := chi.URLParam(r, "id")
	lists, err := s.ta.ListPlaylists(r.Context(), "regular", id)
	if err != nil {
		s.writeTAError(w, "list channel playlists", err)
		return
	}
	out, err := s.playlistSummaries(r.Context(), uid, lists)
	if err != nil {
		s.writeTAError(w, "playlist summaries", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// setChannelFeeds is the "In feeds:" control: replaces the channel's feed
// memberships with the given set of the user's feeds.
func (s *Server) setChannelFeeds(w http.ResponseWriter, r *http.Request) {
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
		if err := q.DeleteChannelFromUserFeeds(r.Context(), sqlc.DeleteChannelFromUserFeedsParams{UserID: uid, ChannelID: id}); err != nil {
			return err
		}
		for _, fid := range feedIDs {
			pos, err := q.NextFeedChannelPosition(r.Context(), fid)
			if err != nil {
				return err
			}
			if err := q.AddFeedChannel(r.Context(), sqlc.AddFeedChannelParams{FeedID: fid, ChannelID: id, Position: pos}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		s.writeDBError(w, "set channel feeds", err)
		return
	}
	refs, err := s.channelFeedRefs(r.Context(), uid)
	if err != nil {
		s.writeDBError(w, "list feed channels", err)
		return
	}
	feeds := refs[id]
	if feeds == nil {
		feeds = []FeedRef{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"feeds": feeds})
}

func (s *Server) markChannelSeen(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r.Context())
	id := chi.URLParam(r, "id")
	items, err := s.buildList(r.Context(), uid, listOpts{ChannelIDs: []string{id}, Sort: "newest", IncludeShorts: true, UnseenOnly: true})
	if err != nil {
		s.writeTAError(w, "list channel videos", err)
		return
	}
	if err := s.markAllSeen(r.Context(), uid, items); err != nil {
		s.writeTAError(w, "mark seen", err)
		return
	}
	// Also flag the channel in TA so videos beyond our fetch cap are covered.
	if err := s.ta.SetWatched(r.Context(), id, true); err != nil {
		s.writeTAError(w, "mark channel watched", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
