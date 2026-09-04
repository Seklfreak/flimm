package api

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"sync"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Seklfreak/flimm/internal/db/sqlc"
	"github.com/Seklfreak/flimm/internal/ta"
)

type feedBody struct {
	Name       string   `json:"name"`
	ChannelIDs []string `json:"channel_ids"`
	// PlaylistIDs are the feed's playlist sources — single series next to
	// whole channels. Nil on PUT means "leave them as they are", so a client
	// built before playlist sources existed cannot wipe them with a full
	// update; an explicit empty list clears them.
	PlaylistIDs []string `json:"playlist_ids"`
	// SeriesWatchChannelIDs: channels whose *new* series are announced in
	// this feed. Same nil-on-PUT-means-unchanged contract as PlaylistIDs.
	SeriesWatchChannelIDs []string `json:"series_watch_channel_ids"`
	Sort                  string   `json:"sort"`
	HideSeen              *bool    `json:"hide_seen"`
	IncludeShorts         *bool    `json:"include_shorts"`
	SubtitlesOnly         *bool    `json:"subtitles_only"`
	Pinned                *bool    `json:"pinned"`
	// Notify: push new downloads to the user's devices. Nil on PUT keeps
	// the current setting, like every other option here.
	Notify *bool `json:"notify"`
}

func boolOr(p *bool, def bool) bool {
	if p == nil {
		return def
	}
	return *p
}

// feedChannelMap groups the user's feed memberships by feed id.
func (s *Server) feedChannelMap(ctx context.Context, uid uuid.UUID) (map[uuid.UUID][]string, error) {
	rows, err := s.q.ListFeedChannelsForUser(ctx, uid)
	if err != nil {
		return nil, err
	}
	out := map[uuid.UUID][]string{}
	for _, r := range rows {
		out[r.FeedID] = append(out[r.FeedID], r.ChannelID)
	}
	return out, nil
}

// feedPlaylistMap groups the user's playlist-source memberships by feed id.
func (s *Server) feedPlaylistMap(ctx context.Context, uid uuid.UUID) (map[uuid.UUID][]string, error) {
	rows, err := s.q.ListFeedPlaylistsForUser(ctx, uid)
	if err != nil {
		return nil, err
	}
	out := map[uuid.UUID][]string{}
	for _, r := range rows {
		out[r.FeedID] = append(out[r.FeedID], r.PlaylistID)
	}
	return out, nil
}

// unseenForPlaylists sums TA's unwatched totals across playlist sources, one
// single-row query per playlist (the TA client caches them).
func (s *Server) unseenForPlaylists(ctx context.Context, ids []string) (int, error) {
	counts := make([]int, len(ids))
	err := parallel(ctx, ids, func(ctx context.Context, i int, id string) error {
		p, err := s.ta.ListVideos(ctx, ta.VideoQuery{Playlist: id, Watch: "unwatched", PageSize: 1})
		if err != nil {
			return err
		}
		counts[i] = p.Paginate.TotalHits
		return nil
	})
	if err != nil {
		return 0, err
	}
	total := 0
	for _, c := range counts {
		total += c
	}
	return total, nil
}

// unseenForFeed is the feed's unseen hint across both source kinds. A video
// that is in a member channel *and* a member playlist is counted twice — the
// number was already a hint (see docs/api.md), and staying one keeps it to
// cached counts instead of a walk of both lists.
func (s *Server) unseenForFeed(ctx context.Context, channelIDs, playlistIDs []string) (int, error) {
	unseen, err := s.unseenForChannels(ctx, channelIDs)
	if err != nil {
		return 0, err
	}
	fromPlaylists, err := s.unseenForPlaylists(ctx, playlistIDs)
	if err != nil {
		return 0, err
	}
	return unseen + fromPlaylists, nil
}

func (s *Server) everythingFeed(ctx context.Context, uid uuid.UUID, position int) (FeedDTO, error) {
	raw, err := s.q.GetPrefs(ctx, uid)
	prefs := defaultPrefs()
	if err == nil {
		prefs = parsePrefs(raw)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return FeedDTO{}, err
	}
	unseen, err := s.unseenForChannels(ctx, nil)
	if err != nil {
		return FeedDTO{}, err
	}
	// The count, not the channels: reading it as the length of the list walks
	// every page of channel documents (see ta.ChannelCount).
	channelCount, err := s.ta.ChannelCount(ctx)
	if err != nil {
		return FeedDTO{}, err
	}
	return FeedDTO{
		ID:                    everythingFeedID,
		Name:                  "Everything",
		ChannelIDs:            []string{},
		ChannelCount:          channelCount,
		PlaylistIDs:           []string{},
		SeriesWatchChannelIDs: []string{},
		UnseenCount:           unseen,
		Sort:                  prefs.EverythingSort,
		HideSeen:              prefs.EverythingHideSeen,
		IncludeShorts:         prefs.EverythingIncludeShorts,
		Position:              position,
	}, nil
}

func (s *Server) listFeeds(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r.Context())
	feeds, err := s.q.ListFeeds(r.Context(), uid)
	if err != nil {
		s.writeDBError(w, "list feeds", err)
		return
	}
	channels, err := s.feedChannelMap(r.Context(), uid)
	if err != nil {
		s.writeDBError(w, "list feed channels", err)
		return
	}
	playlists, err := s.feedPlaylistMap(r.Context(), uid)
	if err != nil {
		s.writeDBError(w, "list feed playlists", err)
		return
	}
	watchRows, err := s.q.ListSeriesWatchesForUser(r.Context(), uid)
	if err != nil {
		s.writeDBError(w, "list series watches", err)
		return
	}
	watches := map[uuid.UUID][]string{}
	for _, row := range watchRows {
		watches[row.FeedID] = append(watches[row.FeedID], row.ChannelID)
	}
	out := make([]FeedDTO, len(feeds)+1)
	err = parallel(r.Context(), feeds, func(ctx context.Context, i int, f sqlc.Feed) error {
		unseen, err := s.unseenForFeed(ctx, orEmptyIDs(channels[f.ID]), playlists[f.ID])
		if err != nil {
			return err
		}
		out[i] = feedDTO(f, channels[f.ID], playlists[f.ID], watches[f.ID], unseen)
		return nil
	})
	if err != nil {
		s.writeTAError(w, "feed unseen counts", err)
		return
	}
	every, err := s.everythingFeed(r.Context(), uid, len(feeds))
	if err != nil {
		s.writeTAError(w, "everything feed", err)
		return
	}
	out[len(feeds)] = every
	writeJSON(w, http.StatusOK, out)
}

func orEmptyIDs(ids []string) []string {
	if ids == nil {
		return []string{}
	}
	return ids
}

func (s *Server) createFeed(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r.Context())
	var req feedBody
	if err := decodeBody(r, &req); err != nil || req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.Sort == "" {
		req.Sort = "newest"
	}
	if !validSorts[req.Sort] {
		writeError(w, http.StatusBadRequest, "invalid sort")
		return
	}
	// The snapshot of a watched channel's *current* playlists happens before
	// the transaction: it reads TubeArchivist, and only playlists indexed
	// after the watch should ever announce.
	watchIDs := dedupe(req.SeriesWatchChannelIDs)
	baseline, err := s.seriesBaseline(r.Context(), watchIDs)
	if err != nil {
		s.writeTAError(w, "snapshot series", err)
		return
	}
	var feed sqlc.Feed
	err = s.withTx(r.Context(), func(q sqlc.Querier) error {
		pos, err := q.NextFeedPosition(r.Context(), uid)
		if err != nil {
			return err
		}
		if boolOr(req.Pinned, false) {
			if err := q.UnpinFeeds(r.Context(), uid); err != nil {
				return err
			}
		}
		feed, err = q.CreateFeed(r.Context(), sqlc.CreateFeedParams{
			UserID:        uid,
			Name:          req.Name,
			Sort:          req.Sort,
			HideSeen:      boolOr(req.HideSeen, true),
			IncludeShorts: boolOr(req.IncludeShorts, false),
			SubtitlesOnly: boolOr(req.SubtitlesOnly, false),
			Pinned:        boolOr(req.Pinned, false),
			Notify:        boolOr(req.Notify, false),
			Position:      pos,
		})
		if err != nil {
			return err
		}
		if err := setFeedChannels(r.Context(), q, feed.ID, req.ChannelIDs); err != nil {
			return err
		}
		if err := setFeedPlaylists(r.Context(), q, feed.ID, req.PlaylistIDs); err != nil {
			return err
		}
		return setSeriesWatches(r.Context(), q, uid, feed.ID, watchIDs, baseline)
	})
	if err != nil {
		s.writeDBError(w, "create feed", err)
		return
	}
	s.ackSeries(r.Context(), uid, dedupe(req.PlaylistIDs))
	chans, pls := dedupe(req.ChannelIDs), dedupe(req.PlaylistIDs)
	unseen, err := s.unseenForFeed(r.Context(), chans, pls)
	if err != nil {
		s.writeTAError(w, "feed unseen count", err)
		return
	}
	writeJSON(w, http.StatusCreated, feedDTO(feed, chans, pls, watchIDs, unseen))
}

func dedupe(ids []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

func setFeedChannels(ctx context.Context, q sqlc.Querier, feedID uuid.UUID, ids []string) error {
	if err := q.DeleteFeedChannels(ctx, feedID); err != nil {
		return err
	}
	for i, id := range dedupe(ids) {
		if err := q.AddFeedChannel(ctx, sqlc.AddFeedChannelParams{FeedID: feedID, ChannelID: id, Position: int32(i)}); err != nil { //nolint:gosec // small
			return err
		}
	}
	return nil
}

func setFeedPlaylists(ctx context.Context, q sqlc.Querier, feedID uuid.UUID, ids []string) error {
	if err := q.DeleteFeedPlaylists(ctx, feedID); err != nil {
		return err
	}
	for i, id := range dedupe(ids) {
		if err := q.AddFeedPlaylist(ctx, sqlc.AddFeedPlaylistParams{FeedID: feedID, PlaylistID: id, Position: int32(i)}); err != nil { //nolint:gosec // small
			return err
		}
	}
	return nil
}

// seriesBaseline is every playlist TubeArchivist currently holds for the
// given channels — what a fresh watch marks as already seen.
func (s *Server) seriesBaseline(ctx context.Context, channels []string) (map[string][]string, error) {
	out := make(map[string][]string, len(channels))
	var mu sync.Mutex
	err := parallel(ctx, channels, func(ctx context.Context, _ int, ch string) error {
		lists, err := s.ta.ListPlaylists(ctx, "regular", ch)
		if err != nil {
			return err
		}
		ids := make([]string, 0, len(lists))
		for _, p := range lists {
			ids = append(ids, p.PlaylistID)
		}
		mu.Lock()
		out[ch] = ids
		mu.Unlock()
		return nil
	})
	return out, err
}

// setSeriesWatches replaces a feed's watched channels. `baseline` carries the
// snapshot for channels that are newly watched; channels the user already
// watched keep their seen-state (MarkSeriesSeen is an upsert, so re-marking
// is harmless and pending announcements survive an unrelated feed edit only
// when their channel's baseline isn't in the map).
func setSeriesWatches(ctx context.Context, q sqlc.Querier, uid, feedID uuid.UUID, ids []string, baseline map[string][]string) error {
	if err := q.DeleteSeriesWatches(ctx, feedID); err != nil {
		return err
	}
	for _, ch := range ids {
		if err := q.AddSeriesWatch(ctx, sqlc.AddSeriesWatchParams{FeedID: feedID, ChannelID: ch}); err != nil {
			return err
		}
		for _, pid := range baseline[ch] {
			if err := q.MarkSeriesSeen(ctx, sqlc.MarkSeriesSeenParams{UserID: uid, ChannelID: ch, PlaylistID: pid}); err != nil {
				return err
			}
		}
	}
	return nil
}

// ackSeries marks playlists as known the moment they become a feed source
// anywhere — a subscribed series must not keep announcing. Best-effort: a
// playlist TA no longer knows just cannot announce anyway.
func (s *Server) ackSeries(ctx context.Context, uid uuid.UUID, playlistIDs []string) {
	for _, pid := range playlistIDs {
		p, err := s.ta.GetPlaylist(ctx, pid)
		if err != nil {
			continue
		}
		_ = s.q.MarkSeriesSeen(ctx, sqlc.MarkSeriesSeenParams{UserID: uid, ChannelID: p.PlaylistChannelID, PlaylistID: pid})
	}
}

// loadFeed resolves a feed id for the user — the feed row plus its channel
// and playlist sources; "everything" is never a row.
func (s *Server) loadFeed(ctx context.Context, uid uuid.UUID, id string) (sqlc.Feed, []string, []string, error) {
	fid, err := uuid.Parse(id)
	if err != nil {
		return sqlc.Feed{}, nil, nil, pgx.ErrNoRows
	}
	feed, err := s.q.GetFeed(ctx, sqlc.GetFeedParams{ID: fid, UserID: uid})
	if err != nil {
		return sqlc.Feed{}, nil, nil, err
	}
	chans, err := s.q.ListFeedChannels(ctx, fid)
	if err != nil {
		return sqlc.Feed{}, nil, nil, err
	}
	pls, err := s.q.ListFeedPlaylists(ctx, fid)
	if err != nil {
		return sqlc.Feed{}, nil, nil, err
	}
	return feed, orEmptyIDs(chans), orEmptyIDs(pls), nil
}

func (s *Server) getFeed(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r.Context())
	id := chi.URLParam(r, "id")
	if id == everythingFeedID {
		feeds, err := s.q.ListFeeds(r.Context(), uid)
		if err != nil {
			s.writeDBError(w, "list feeds", err)
			return
		}
		every, err := s.everythingFeed(r.Context(), uid, len(feeds))
		if err != nil {
			s.writeTAError(w, "everything feed", err)
			return
		}
		writeJSON(w, http.StatusOK, every)
		return
	}
	feed, chans, pls, err := s.loadFeed(r.Context(), uid, id)
	if err != nil {
		s.writeDBError(w, "get feed", err)
		return
	}
	watches, err := s.q.ListSeriesWatches(r.Context(), feed.ID)
	if err != nil {
		s.writeDBError(w, "list series watches", err)
		return
	}
	unseen, err := s.unseenForFeed(r.Context(), chans, pls)
	if err != nil {
		s.writeTAError(w, "feed unseen count", err)
		return
	}
	writeJSON(w, http.StatusOK, feedDTO(feed, chans, pls, watches, unseen))
}

func (s *Server) updateFeed(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r.Context())
	id := chi.URLParam(r, "id")
	var req feedBody
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Sort != "" && !validSorts[req.Sort] {
		writeError(w, http.StatusBadRequest, "invalid sort")
		return
	}
	if id == everythingFeedID {
		// Read-only except the listing options, which live in prefs.
		prefs, err := s.loadPrefs(r, uid)
		if err != nil {
			s.writeDBError(w, "load prefs", err)
			return
		}
		if req.Sort != "" {
			prefs.EverythingSort = req.Sort
		}
		prefs.EverythingHideSeen = boolOr(req.HideSeen, prefs.EverythingHideSeen)
		prefs.EverythingIncludeShorts = boolOr(req.IncludeShorts, prefs.EverythingIncludeShorts)
		if err := s.savePrefs(r, uid, prefs); err != nil {
			s.writeDBError(w, "save prefs", err)
			return
		}
		s.getFeed(w, r)
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	fid, err := uuid.Parse(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	// Baseline snapshots for newly watched channels, read outside the tx.
	var updWatchIDs []string
	updBaseline := map[string][]string{}
	if req.SeriesWatchChannelIDs != nil {
		updWatchIDs = dedupe(req.SeriesWatchChannelIDs)
		existing, err := s.q.ListSeriesWatches(r.Context(), fid)
		if err != nil {
			s.writeDBError(w, "list series watches", err)
			return
		}
		known := map[string]bool{}
		for _, ch := range existing {
			known[ch] = true
		}
		var added []string
		for _, ch := range updWatchIDs {
			if !known[ch] {
				added = append(added, ch)
			}
		}
		updBaseline, err = s.seriesBaseline(r.Context(), added)
		if err != nil {
			s.writeTAError(w, "snapshot series", err)
			return
		}
	}
	var feed sqlc.Feed
	err = s.withTx(r.Context(), func(q sqlc.Querier) error {
		cur, err := q.GetFeed(r.Context(), sqlc.GetFeedParams{ID: fid, UserID: uid})
		if err != nil {
			return err
		}
		if req.Sort == "" {
			req.Sort = cur.Sort
		}
		pinned := boolOr(req.Pinned, cur.Pinned)
		if pinned && !cur.Pinned {
			if err := q.UnpinFeeds(r.Context(), uid); err != nil {
				return err
			}
		}
		feed, err = q.UpdateFeed(r.Context(), sqlc.UpdateFeedParams{
			ID:            fid,
			UserID:        uid,
			Name:          req.Name,
			Sort:          req.Sort,
			HideSeen:      boolOr(req.HideSeen, cur.HideSeen),
			IncludeShorts: boolOr(req.IncludeShorts, cur.IncludeShorts),
			SubtitlesOnly: boolOr(req.SubtitlesOnly, cur.SubtitlesOnly),
			Pinned:        pinned,
			Notify:        boolOr(req.Notify, cur.Notify),
		})
		if err != nil {
			return err
		}
		if req.ChannelIDs != nil {
			if err := setFeedChannels(r.Context(), q, fid, req.ChannelIDs); err != nil {
				return err
			}
		}
		if req.PlaylistIDs != nil {
			if err := setFeedPlaylists(r.Context(), q, fid, req.PlaylistIDs); err != nil {
				return err
			}
		}
		if feed.Notify && (req.ChannelIDs != nil || req.PlaylistIDs != nil) {
			// New sources bring their whole back catalogue, and none of it
			// is news: the notifier seeds the feed again before it speaks.
			if err := q.SetFeedNotifySeeded(r.Context(), sqlc.SetFeedNotifySeededParams{ID: fid, NotifySeeded: false}); err != nil {
				return err
			}
		}
		if req.SeriesWatchChannelIDs != nil {
			return setSeriesWatches(r.Context(), q, uid, fid, updWatchIDs, updBaseline)
		}
		return nil
	})
	if err != nil {
		s.writeDBError(w, "update feed", err)
		return
	}
	if req.PlaylistIDs != nil {
		s.ackSeries(r.Context(), uid, dedupe(req.PlaylistIDs))
	}
	chans, err := s.q.ListFeedChannels(r.Context(), fid)
	if err != nil {
		s.writeDBError(w, "list feed channels", err)
		return
	}
	pls, err := s.q.ListFeedPlaylists(r.Context(), fid)
	if err != nil {
		s.writeDBError(w, "list feed playlists", err)
		return
	}
	watches, err := s.q.ListSeriesWatches(r.Context(), fid)
	if err != nil {
		s.writeDBError(w, "list series watches", err)
		return
	}
	chans, pls = orEmptyIDs(chans), orEmptyIDs(pls)
	unseen, err := s.unseenForFeed(r.Context(), chans, pls)
	if err != nil {
		s.writeTAError(w, "feed unseen count", err)
		return
	}
	writeJSON(w, http.StatusOK, feedDTO(feed, chans, pls, watches, unseen))
}

func (s *Server) deleteFeed(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r.Context())
	fid, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	n, err := s.q.DeleteFeed(r.Context(), sqlc.DeleteFeedParams{ID: fid, UserID: uid})
	if err != nil {
		s.writeDBError(w, "delete feed", err)
		return
	}
	if n == 0 {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) reorderFeeds(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r.Context())
	var req struct {
		IDs []string `json:"ids"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	err := s.withTx(r.Context(), func(q sqlc.Querier) error {
		pos := 0
		for _, id := range req.IDs {
			if id == everythingFeedID {
				continue // always last, not stored
			}
			fid, err := uuid.Parse(id)
			if err != nil {
				continue
			}
			if err := q.SetFeedPosition(r.Context(), sqlc.SetFeedPositionParams{ID: fid, UserID: uid, Position: int32(pos)}); err != nil { //nolint:gosec // small
				return err
			}
			pos++
		}
		return nil
	})
	if err != nil {
		s.writeDBError(w, "reorder feeds", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// feedListOpts resolves a feed (or "everything") into listing options using
// the feed's own hide_seen as the default view.
func (s *Server) feedListOpts(r *http.Request, uid uuid.UUID, id string) (listOpts, error) {
	var o listOpts
	if id == everythingFeedID {
		prefs, err := s.loadPrefs(r, uid)
		if err != nil {
			return o, err
		}
		o = listOpts{Sort: prefs.EverythingSort, IncludeShorts: prefs.EverythingIncludeShorts, UnseenOnly: prefs.EverythingHideSeen, DropDismissed: true}
	} else {
		feed, chans, pls, err := s.loadFeed(r.Context(), uid, id)
		if err != nil {
			return o, err
		}
		o = listOpts{ChannelIDs: chans, PlaylistIDs: pls, Sort: feed.Sort, IncludeShorts: feed.IncludeShorts, SubtitlesOnly: feed.SubtitlesOnly, UnseenOnly: feed.HideSeen, DropDismissed: true}
	}
	switch r.URL.Query().Get("view") {
	case "unseen":
		o.UnseenOnly = true
	case "all":
		o.UnseenOnly = false
	}
	return o, nil
}

func (s *Server) listFeedVideos(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r.Context())
	o, err := s.feedListOpts(r, uid, chi.URLParam(r, "id"))
	if err != nil {
		s.writeDBError(w, "load feed", err)
		return
	}
	p := parsePaging(r)
	// `view=continue` was its own filter once. The videos it listed now open
	// the unseen view instead, so the parameter is kept only for clients built
	// before that — it answers with the same in-progress videos, which are the
	// head of the list those clients would get from `view=unseen` today.
	if r.URL.Query().Get("view") == "continue" {
		items, err := s.continueList(r.Context(), uid, o)
		if err != nil {
			s.writeTAError(w, "list feed videos", err)
			return
		}
		writeJSON(w, http.StatusOK, slicePage(items, p))
		return
	}
	page, err := s.listFeedPage(r.Context(), uid, o, p)
	if err != nil {
		s.writeListError(w, "list feed videos", err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

// listFeedPage is one page of a feed.
//
// An unseen feed opens with what the viewer is part-way through, most recently
// played first, and continues into the rest of the unseen videos. Half-watched
// videos are the ones a viewer came back for; making them the top of the list
// is why there is no longer a separate "Continue" filter to go and find them
// in. Every other view is the plain lazily-composed list.
//
// The in-progress head is bounded (it comes from the viewer's own events) and
// is composed eagerly; the tail is the same lazy walk as before, minus the
// videos the head already showed. How much of the head a page has served rides
// the cursor, so a head longer than one page still pages properly.
func (s *Server) listFeedPage(ctx context.Context, uid uuid.UUID, o listOpts, p paging) (Page[VideoSummary], error) {
	if !o.UnseenOnly {
		return s.listVideosPage(ctx, uid, o, p)
	}
	head, err := s.continueList(ctx, uid, o)
	if err != nil {
		return Page[VideoSummary]{}, err
	}
	if len(head) == 0 {
		return s.listVideosPage(ctx, uid, o, p)
	}
	o.ExcludeIDs = make(map[string]bool, len(head))
	for _, it := range head {
		o.ExcludeIDs[it.ID] = true
	}

	served, err := s.headServed(p, o, len(head))
	if err != nil {
		return Page[VideoSummary]{}, err
	}
	from := head[min(served, len(head)):]
	if len(from) >= p.Size {
		// This page is head all the way. The cursor it hands back has no
		// stream positions, so the next one starts the tail at its beginning.
		items := from[:p.Size]
		return Page[VideoSummary]{
			Items:      items,
			Page:       p.Page,
			PageSize:   p.Size,
			Total:      int64(len(head)),
			HasMore:    true,
			NextCursor: encodeCursor(o, nil, 0, served+p.Size),
		}, nil
	}

	// The head runs out inside this page; the rest of it comes from the tail.
	tail := p
	tail.Size = p.Size - len(from)
	if p.Cursor == "" {
		tail.Page = max(0, (p.offset()-len(head)+tail.Size-1)/max(tail.Size, 1))
	}
	page, err := s.listVideosPageAfterHead(ctx, uid, o, tail, len(head), len(head))
	if err != nil {
		return Page[VideoSummary]{}, err
	}
	page.Items = append(slices.Clone(from), page.Items...)
	page.Page, page.PageSize = p.Page, p.Size
	return page, nil
}

// headServed is how many in-progress videos the pages before this one showed.
func (s *Server) headServed(p paging, o listOpts, head int) (int, error) {
	if p.Cursor == "" {
		return min(p.offset(), head), nil
	}
	c, err := decodeCursor(p.Cursor, o)
	if err != nil {
		return 0, err
	}
	return min(c.Head, head), nil
}

func (s *Server) markFeedSeen(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r.Context())
	o, err := s.feedListOpts(r, uid, chi.URLParam(r, "id"))
	if err != nil {
		s.writeDBError(w, "load feed", err)
		return
	}
	o.UnseenOnly = true
	items, err := s.buildList(r.Context(), uid, o)
	if err != nil {
		s.writeTAError(w, "list feed videos", err)
		return
	}
	if err := s.markAllSeen(r.Context(), uid, items); err != nil {
		s.writeTAError(w, "mark seen", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// listNewSeries is the feed's announcements: playlists TubeArchivist has
// indexed for the feed's watched channels that the user has not seen yet —
// not baselined at watch creation, not dismissed, not subscribed anywhere.
func (s *Server) listNewSeries(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r.Context())
	fid, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if _, err := s.q.GetFeed(r.Context(), sqlc.GetFeedParams{ID: fid, UserID: uid}); err != nil {
		s.writeDBError(w, "get feed", err)
		return
	}
	channels, err := s.q.ListSeriesWatches(r.Context(), fid)
	if err != nil {
		s.writeDBError(w, "list series watches", err)
		return
	}
	out := []PlaylistSummary{}
	if len(channels) == 0 {
		writeJSON(w, http.StatusOK, out)
		return
	}
	seenIDs, err := s.q.ListSeriesSeen(r.Context(), sqlc.ListSeriesSeenParams{UserID: uid, ChannelIds: channels})
	if err != nil {
		s.writeDBError(w, "list series seen", err)
		return
	}
	seen := make(map[string]bool, len(seenIDs))
	for _, id := range seenIDs {
		seen[id] = true
	}
	var mu sync.Mutex
	var fresh []ta.Playlist
	err = parallel(r.Context(), channels, func(ctx context.Context, _ int, ch string) error {
		lists, err := s.ta.ListPlaylists(ctx, "regular", ch)
		if err != nil {
			return err
		}
		mu.Lock()
		defer mu.Unlock()
		for _, p := range lists {
			if !seen[p.PlaylistID] {
				fresh = append(fresh, p)
			}
		}
		return nil
	})
	if err != nil {
		s.writeTAError(w, "list new series", err)
		return
	}
	sums, err := s.playlistSummaries(r.Context(), uid, fresh)
	if err != nil {
		s.writeTAError(w, "playlist summaries", err)
		return
	}
	if err := s.attachPlaylistFeeds(r.Context(), uid, sums); err != nil {
		s.writeDBError(w, "list feed playlists", err)
		return
	}
	writeJSON(w, http.StatusOK, sums)
}

// dismissNewSeries: "not a series I want" — the announcement never comes
// back, in any feed.
func (s *Server) dismissNewSeries(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r.Context())
	fid, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if _, err := s.q.GetFeed(r.Context(), sqlc.GetFeedParams{ID: fid, UserID: uid}); err != nil {
		s.writeDBError(w, "get feed", err)
		return
	}
	pid := chi.URLParam(r, "playlistID")
	p, err := s.ta.GetPlaylist(r.Context(), pid)
	if err != nil {
		s.writeTAError(w, "get playlist", err)
		return
	}
	if err := s.q.MarkSeriesSeen(r.Context(), sqlc.MarkSeriesSeenParams{UserID: uid, ChannelID: p.PlaylistChannelID, PlaylistID: pid}); err != nil {
		s.writeDBError(w, "mark series seen", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
