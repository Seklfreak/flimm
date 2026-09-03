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
func (s *Server) enrichChannel(ctx context.Context, c ta.Channel, feeds []FeedRef, pinned bool) (*ChannelSummary, error) {
	counts := s.channelAggregates(ctx, []string{c.ChannelID})
	out := channelSummaryOf(c, feeds, counts[c.ChannelID], pinned)
	return &out, nil
}

// channelSummaryOf is the DTO for one channel and its counts.
func channelSummaryOf(c ta.Channel, feeds []FeedRef, counts channelAggregate, pinned bool) ChannelSummary {
	if feeds == nil {
		feeds = []FeedRef{}
	}
	return ChannelSummary{
		ID:          c.ChannelID,
		Name:        c.ChannelName,
		ThumbURL:    channelThumbURL(c.ChannelID),
		BannerURL:   channelBannerURL(c.ChannelID),
		VideoCount:  counts.VideoCount,
		UnseenCount: counts.Unseen,
		LastUpload:  counts.lastUpload(),
		Subscribed:  c.ChannelSubscribed,
		Pinned:      pinned,
		Feeds:       feeds,
	}
}

// pinnedChannelSet is which channels the user pinned, for stamping summaries.
func (s *Server) pinnedChannelSet(ctx context.Context, uid uuid.UUID) (map[string]bool, error) {
	rows, err := s.q.ListPinnedChannels(ctx, uid)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(rows))
	for _, r := range rows {
		out[r.ChannelID] = true
	}
	return out, nil
}

func (s *Server) channelSummary(ctx context.Context, uid uuid.UUID, c ta.Channel) (*ChannelSummary, error) {
	refs, err := s.channelFeedRefs(ctx, uid)
	if err != nil {
		return nil, err
	}
	pins, err := s.pinnedChannelSet(ctx, uid)
	if err != nil {
		return nil, err
	}
	return s.enrichChannel(ctx, c, refs[c.ChannelID], pins[c.ChannelID])
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
	pins, err := s.pinnedChannelSet(r.Context(), uid)
	if err != nil {
		s.writeDBError(w, "list pinned channels", err)
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
	// Counts are only needed for the channels that will be *shown*, unless the
	// order depends on them. Enriching everything first is what made one request
	// to this route cost 429 queries: the archive has hundreds of channels and a
	// page holds thirty.
	sort := q.Get("sort")
	paging := parsePaging(r)
	if !sortNeedsCounts(sort) {
		sortChannelsByName(picked)
		window := slicePage(picked, paging)
		items := s.summarise(r.Context(), window.Items, refs, pins)
		writeJSON(w, http.StatusOK, Page[ChannelSummary]{
			Items: items, Page: paging.Page, PageSize: paging.Size,
			Total: window.Total, HasMore: window.HasMore,
		})
		return
	}
	items := s.summarise(r.Context(), picked, refs, pins)
	sortChannels(items, sort)
	writeJSON(w, http.StatusOK, slicePage(items, paging))
}

// sortNeedsCounts reports whether an order can only be decided once every
// channel's counts are known.
func sortNeedsCounts(key string) bool {
	switch key {
	case "videos", "unseen", "last_upload":
		return true
	}
	return false
}

// sortChannelsByName is the default order, and the one that needs nothing
// fetched to decide it.
func sortChannelsByName(channels []ta.Channel) {
	sort.SliceStable(channels, func(i, j int) bool {
		return strings.ToLower(channels[i].ChannelName) < strings.ToLower(channels[j].ChannelName)
	})
}

// summarise turns channels into summaries, reading every channel's counts in
// one pass (see channelAggregates).
func (s *Server) summarise(ctx context.Context, channels []ta.Channel, refs map[string][]FeedRef, pins map[string]bool) []ChannelSummary {
	ids := make([]string, 0, len(channels))
	for _, c := range channels {
		ids = append(ids, c.ChannelID)
	}
	counts := s.channelAggregates(ctx, ids)
	items := make([]ChannelSummary, len(channels))
	for i, c := range channels {
		items[i] = channelSummaryOf(c, refs[c.ChannelID], counts[c.ChannelID], pins[c.ChannelID])
	}
	return items
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
	page, err := s.listVideosPage(r.Context(), uid, listOpts{
		ChannelIDs:    []string{id},
		Sort:          sortKey,
		IncludeShorts: true,
		UnseenOnly:    r.URL.Query().Get("view") == "unseen",
	}, parsePaging(r))
	if err != nil {
		s.writeListError(w, "list channel videos", err)
		return
	}
	writeJSON(w, http.StatusOK, page)
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
	if err := s.attachPlaylistFeeds(r.Context(), uid, out); err != nil {
		s.writeDBError(w, "list feed playlists", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// listPinnedChannels is the sidebar's pinned-channel list: pin order, and a
// channel deleted in TubeArchivist simply drops out rather than failing the
// request (the same contract as pinned playlists).
func (s *Server) listPinnedChannels(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r.Context())
	rows, err := s.q.ListPinnedChannels(r.Context(), uid)
	if err != nil {
		s.writeDBError(w, "list pinned channels", err)
		return
	}
	all, err := s.ta.ListChannels(r.Context())
	if err != nil {
		s.writeTAError(w, "list channels", err)
		return
	}
	byID := make(map[string]ta.Channel, len(all))
	for _, c := range all {
		byID[c.ChannelID] = c
	}
	refs, err := s.channelFeedRefs(r.Context(), uid)
	if err != nil {
		s.writeDBError(w, "list feed channels", err)
		return
	}
	var picked []ta.Channel
	for _, row := range rows {
		if c, ok := byID[row.ChannelID]; ok {
			picked = append(picked, c)
		}
	}
	pins := make(map[string]bool, len(picked))
	for _, c := range picked {
		pins[c.ChannelID] = true
	}
	writeJSON(w, http.StatusOK, s.summarise(r.Context(), picked, refs, pins))
}

// setChannelPinned mirrors the playlist pin: per-user sidebar state. Pinning
// a channel TA does not know is refused, so the sidebar cannot accumulate
// references that never resolve.
func (s *Server) setChannelPinned(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r.Context())
	id := chi.URLParam(r, "id")
	var req struct {
		Pinned *bool `json:"pinned"`
	}
	if err := decodeBody(r, &req); err != nil || req.Pinned == nil {
		writeError(w, http.StatusBadRequest, "pinned is required")
		return
	}
	if *req.Pinned {
		if _, err := s.ta.GetChannel(r.Context(), id); err != nil {
			s.writeTAError(w, "get channel", err)
			return
		}
		if err := s.q.PinChannel(r.Context(), sqlc.PinChannelParams{UserID: uid, ChannelID: id}); err != nil {
			s.writeDBError(w, "pin channel", err)
			return
		}
	} else if err := s.q.UnpinChannel(r.Context(), sqlc.UnpinChannelParams{UserID: uid, ChannelID: id}); err != nil {
		s.writeDBError(w, "unpin channel", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// setChannelSubscribed flips TubeArchivist's own subscription — whether the
// archive keeps downloading the channel's new videos. Admin-only: it is
// instance-wide TA state that drives downloads and storage.
func (s *Server) setChannelSubscribed(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r.Context()) {
		writeError(w, http.StatusForbidden, "admin only")
		return
	}
	id := chi.URLParam(r, "id")
	var req struct {
		Subscribed *bool `json:"subscribed"`
	}
	if err := decodeBody(r, &req); err != nil || req.Subscribed == nil {
		writeError(w, http.StatusBadRequest, "subscribed is required")
		return
	}
	// Only channels the archive already knows; a brand-new channel goes
	// through subscribeNewChannel, which is a deliberate separate action.
	if _, err := s.ta.GetChannel(r.Context(), id); err != nil {
		s.writeTAError(w, "get channel", err)
		return
	}
	if err := s.ta.SetChannelSubscribed(r.Context(), id, *req.Subscribed); err != nil {
		s.writeTAError(w, "set channel subscribed", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// subscribeNewChannel asks TubeArchivist to subscribe a channel it may not
// know yet — a URL, @handle or UC… id; TA's own task resolves it, creates
// the channel and downloads from the next rescan on. Admin-only for the same
// reason as the toggle, and 202 because the resolution is TA's background
// work: the channel appears in the directory once the task lands. 204 like
// every other side-effect endpoint here (a 202 with an empty body trips
// JSON-parsing clients).
func (s *Server) subscribeNewChannel(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r.Context()) {
		writeError(w, http.StatusForbidden, "admin only")
		return
	}
	var req struct {
		Channel string `json:"channel"`
	}
	if err := decodeBody(r, &req); err != nil || strings.TrimSpace(req.Channel) == "" {
		writeError(w, http.StatusBadRequest, "channel is required")
		return
	}
	if err := s.ta.SetChannelSubscribed(r.Context(), strings.TrimSpace(req.Channel), true); err != nil {
		s.writeTAError(w, "subscribe channel", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// indexChannelPlaylists asks TubeArchivist to index the channel's own
// playlists — the archive-side prerequisite for series feeds. Admin-only:
// the overwrite it flips is instance-wide TA state, shared by every user of
// the archive, and TA warns it slows the indexing of new videos. 204 like
// every other side-effect endpoint here — the discovery runs as a TA task
// and lands whenever it lands.
func (s *Server) indexChannelPlaylists(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r.Context()) {
		writeError(w, http.StatusForbidden, "admin only")
		return
	}
	if err := s.ta.IndexChannelPlaylists(r.Context(), chi.URLParam(r, "id")); err != nil {
		s.writeTAError(w, "index channel playlists", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
