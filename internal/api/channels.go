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
	out, err := s.channelPlaylistSummaries(r.Context(), currentUserID(r.Context()), chi.URLParam(r, "id"))
	if err != nil {
		s.writeTAError(w, "list channel playlists", err)
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
// know yet — a URL, @handle or UC… id — and holds the request until TA's
// own task has resolved and created it, so the caller gets the channel
// back rather than a promise. Admin-only for the same reason as the toggle.
//
// TA answers the subscribe with nothing to wait on, so the task is found
// by difference: the subscribe_to results stored before the call against
// those after it. A failed task (a handle that does not resolve, a URL off
// youtube.com) becomes a 502 carrying TA's reason, which used to vanish
// into the archive's logs. The channel is then read by id when the input
// carried one, or found as the one channel the archive did not have before,
// which is what a handle needs. Past subscribeWait the request answers 202
// and the channel appears in the directory whenever the task lands — the
// old contract, kept for the slow case (a busy TA queue) rather than a
// proxy timeout.
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
	ctx := r.Context()
	input := strings.TrimSpace(req.Channel)

	before, err := s.subscribeSnapshot(ctx)
	if err != nil {
		s.writeTAError(w, "subscribe channel", err)
		return
	}
	if err := s.ta.SetChannelSubscribed(ctx, input, true); err != nil {
		s.writeTAError(w, "subscribe channel", err)
		return
	}

	deadline := time.Now().Add(s.subscribeWait)
	for {
		outcome, err := s.subscribeOutcome(ctx, input, before)
		if err != nil {
			s.writeTAError(w, "subscribe channel", err)
			return
		}
		if outcome.failed != "" {
			s.log.Warn("subscribe channel: task failed", "channel", input, "reason", outcome.failed)
			writeError(w, http.StatusBadGateway, "TubeArchivist could not subscribe: "+outcome.failed)
			return
		}
		if outcome.channel != nil {
			summary, err := s.channelSummary(ctx, currentUserID(ctx), *outcome.channel)
			if err != nil {
				s.writeTAError(w, "subscribe channel", err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"status": "added", "channel": summary})
			return
		}
		if time.Now().After(deadline) {
			writeJSON(w, http.StatusAccepted, map[string]string{"status": "pending"})
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(s.subscribePoll):
		}
	}
}

// subscribeSnapshot is what the archive held before a subscribe: the task
// ids and channel ids the outcome is measured against.
type subscribeSnapshot struct {
	tasks    map[string]bool
	channels map[string]bool
}

func (s *Server) subscribeSnapshot(ctx context.Context) (subscribeSnapshot, error) {
	snap := subscribeSnapshot{channels: map[string]bool{}}
	var err error
	if snap.tasks, err = s.taskSnapshot(ctx, taskSubscribe); err != nil {
		return snap, err
	}
	s.ta.ForgetChannels()
	channels, err := s.ta.ListChannels(ctx)
	if err != nil {
		return snap, err
	}
	for _, c := range channels {
		snap.channels[c.ChannelID] = true
	}
	return snap, nil
}

// subscribeResult is one poll's reading: the channel once it is there, the
// reason if TA gave up, neither while the task is still running.
type subscribeResult struct {
	channel *ta.Channel
	failed  string
}

func (s *Server) subscribeOutcome(ctx context.Context, input string, before subscribeSnapshot) (subscribeResult, error) {
	state, err := s.taskState(ctx, taskSubscribe, before.tasks)
	if err != nil || !state.landed {
		return subscribeResult{failed: state.failed}, err
	}
	// The task is done; the channel may still be a moment behind it in the
	// index, in which case the next poll finds it.
	if id := ta.ChannelIDIn(input); id != "" {
		c, err := s.ta.GetChannel(ctx, id)
		if errors.Is(err, ta.ErrNotFound) {
			return subscribeResult{}, nil
		}
		return subscribeResult{channel: c}, err
	}
	s.ta.ForgetChannels()
	channels, err := s.ta.ListChannels(ctx)
	if err != nil {
		return subscribeResult{}, err
	}
	var added []ta.Channel
	for _, c := range channels {
		if !before.channels[c.ChannelID] {
			added = append(added, c)
		}
	}
	if len(added) == 1 {
		return subscribeResult{channel: &added[0]}, nil
	}
	// None: not indexed yet. Several: another admin was adding channels at
	// the same time and none of them can be told apart; the caller waits
	// out the window and gets "pending", and the directory has them all.
	return subscribeResult{}, nil
}

// TubeArchivist's names for the tasks behind its channel writes: the
// subscribe (POST /api/channel/) and the playlist discovery (the
// index_playlists overwrite).
const (
	taskSubscribe      = "subscribe_to"
	taskIndexPlaylists = "index_playlists"
)

// taskSnapshot is the set of TA's stored results for a task name — taken
// before queueing one, so the task that follows can be told apart from
// every earlier one by difference, which is all TA offers: the queue answers
// nothing to wait on, and a stored result names no channel.
func (s *Server) taskSnapshot(ctx context.Context, name string) (map[string]bool, error) {
	tasks, err := s.ta.ListTasks(ctx, name)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(tasks))
	for _, t := range tasks {
		seen[t.TaskID] = true
	}
	return seen, nil
}

// taskState is one reading of the tasks queued since a snapshot: landed
// once every new one has finished well, failed with TA's reason when one
// gave up, neither while one is still running or none has started.
type taskState struct {
	landed bool
	failed string
}

func (s *Server) taskState(ctx context.Context, name string, before map[string]bool) (taskState, error) {
	tasks, err := s.ta.ListTasks(ctx, name)
	if err != nil {
		return taskState{}, err
	}
	var state taskState
	for _, t := range tasks {
		if before[t.TaskID] {
			continue
		}
		if t.Failed() {
			return taskState{failed: t.Error()}, nil
		}
		if !t.Done() {
			return taskState{}, nil
		}
		state.landed = true
	}
	return state, nil
}

// indexChannelPlaylists asks TubeArchivist to index the channel's own
// playlists — the archive-side prerequisite for series feeds — and holds
// the request until TA's discovery task has run, answering with what it
// found, so a client shows the playlists rather than "check back later".
// Admin-only: the overwrite it flips is instance-wide TA state, shared by
// every user of the archive, and TA warns it slows the indexing of new
// videos.
//
// The task is found by difference, like the subscribe (see taskSnapshot).
// Discovery walks the channel's playlists with yt-dlp, which for a large
// channel takes longer than a request may hold: past subscribeWait the
// answer is 202 pending, and the client follows the status endpoint below
// until the task is gone. A failed task becomes a 502 with TA's reason.
func (s *Server) indexChannelPlaylists(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r.Context()) {
		writeError(w, http.StatusForbidden, "admin only")
		return
	}
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	before, err := s.taskSnapshot(ctx, taskIndexPlaylists)
	if err != nil {
		s.writeTAError(w, "index channel playlists", err)
		return
	}
	if err := s.ta.IndexChannelPlaylists(ctx, id); err != nil {
		s.writeTAError(w, "index channel playlists", err)
		return
	}
	deadline := time.Now().Add(s.subscribeWait)
	for {
		state, err := s.taskState(ctx, taskIndexPlaylists, before)
		if err != nil {
			s.writeTAError(w, "index channel playlists", err)
			return
		}
		if state.failed != "" {
			s.log.Warn("index channel playlists: task failed", "channel", id, "reason", state.failed)
			writeError(w, http.StatusBadGateway, "TubeArchivist could not index the playlists: "+state.failed)
			return
		}
		if state.landed {
			out, err := s.channelPlaylistSummaries(ctx, currentUserID(ctx), id)
			if err != nil {
				s.writeTAError(w, "index channel playlists", err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"status": "indexed", "playlists": out})
			return
		}
		if time.Now().After(deadline) {
			writeJSON(w, http.StatusAccepted, map[string]string{"status": "pending"})
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(s.subscribePoll):
		}
	}
}

// indexChannelPlaylistsStatus is what a client follows after a 202 from
// the POST: whether TubeArchivist is still running a playlist discovery.
// TA's stored results name no channel, so this is "any discovery", which
// is what a client waiting on one needs to know — when none is running,
// the channel's playlists are whatever they are going to be.
func (s *Server) indexChannelPlaylistsStatus(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r.Context()) {
		writeError(w, http.StatusForbidden, "admin only")
		return
	}
	tasks, err := s.ta.ListTasks(r.Context(), taskIndexPlaylists)
	if err != nil {
		s.writeTAError(w, "index channel playlists status", err)
		return
	}
	status := "idle"
	for _, t := range tasks {
		if !t.Done() {
			status = "running"
			break
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": status})
}

// channelPlaylistSummaries is a channel's indexed playlists as the client
// sees them, with the viewer's feed memberships attached.
func (s *Server) channelPlaylistSummaries(ctx context.Context, uid uuid.UUID, id string) ([]PlaylistSummary, error) {
	lists, err := s.ta.ListPlaylists(ctx, "regular", id)
	if err != nil {
		return nil, err
	}
	out, err := s.playlistSummaries(ctx, uid, lists)
	if err != nil {
		return nil, err
	}
	if err := s.attachPlaylistFeeds(ctx, uid, out); err != nil {
		return nil, err
	}
	return out, nil
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
