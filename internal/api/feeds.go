package api

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Seklfreak/flimm/internal/db/sqlc"
	"github.com/Seklfreak/flimm/internal/ta"
)

type feedBody struct {
	Name          string   `json:"name"`
	ChannelIDs    []string `json:"channel_ids"`
	Sort          string   `json:"sort"`
	HideSeen      *bool    `json:"hide_seen"`
	IncludeShorts *bool    `json:"include_shorts"`
	SubtitlesOnly *bool    `json:"subtitles_only"`
	Pinned        *bool    `json:"pinned"`
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

// unseenForChannels sums TA's per-channel unwatched counts; nil channels =
// the whole library.
func (s *Server) unseenForChannels(ctx context.Context, channelIDs []string) (int, error) {
	if channelIDs == nil {
		p, err := s.ta.ListVideos(ctx, ta.VideoQuery{Watch: "unwatched", PageSize: 1})
		if err != nil {
			return 0, err
		}
		return p.Paginate.TotalHits, nil
	}
	var total atomic.Int64
	err := parallel(ctx, channelIDs, func(ctx context.Context, _ int, ch string) error {
		n, err := s.ta.UnseenCount(ctx, ch)
		if err != nil {
			return err
		}
		total.Add(int64(n))
		return nil
	})
	return int(total.Load()), err
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
	channels, err := s.ta.ListChannels(ctx)
	if err != nil {
		return FeedDTO{}, err
	}
	return FeedDTO{
		ID:            everythingFeedID,
		Name:          "Everything",
		ChannelIDs:    []string{},
		ChannelCount:  len(channels),
		UnseenCount:   unseen,
		Sort:          prefs.EverythingSort,
		HideSeen:      prefs.EverythingHideSeen,
		IncludeShorts: prefs.EverythingIncludeShorts,
		Position:      position,
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
	out := make([]FeedDTO, len(feeds)+1)
	err = parallel(r.Context(), feeds, func(ctx context.Context, i int, f sqlc.Feed) error {
		unseen, err := s.unseenForChannels(ctx, orEmptyIDs(channels[f.ID]))
		if err != nil {
			return err
		}
		out[i] = feedDTO(f, channels[f.ID], unseen)
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
	var feed sqlc.Feed
	err := s.withTx(r.Context(), func(q sqlc.Querier) error {
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
			Position:      pos,
		})
		if err != nil {
			return err
		}
		return setFeedChannels(r.Context(), q, feed.ID, req.ChannelIDs)
	})
	if err != nil {
		s.writeDBError(w, "create feed", err)
		return
	}
	chans := dedupe(req.ChannelIDs)
	unseen, err := s.unseenForChannels(r.Context(), chans)
	if err != nil {
		s.writeTAError(w, "feed unseen count", err)
		return
	}
	writeJSON(w, http.StatusCreated, feedDTO(feed, chans, unseen))
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

// loadFeed resolves a feed id for the user; "everything" is never a row.
func (s *Server) loadFeed(ctx context.Context, uid uuid.UUID, id string) (sqlc.Feed, []string, error) {
	fid, err := uuid.Parse(id)
	if err != nil {
		return sqlc.Feed{}, nil, pgx.ErrNoRows
	}
	feed, err := s.q.GetFeed(ctx, sqlc.GetFeedParams{ID: fid, UserID: uid})
	if err != nil {
		return sqlc.Feed{}, nil, err
	}
	chans, err := s.q.ListFeedChannels(ctx, fid)
	if err != nil {
		return sqlc.Feed{}, nil, err
	}
	return feed, orEmptyIDs(chans), nil
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
	feed, chans, err := s.loadFeed(r.Context(), uid, id)
	if err != nil {
		s.writeDBError(w, "get feed", err)
		return
	}
	unseen, err := s.unseenForChannels(r.Context(), chans)
	if err != nil {
		s.writeTAError(w, "feed unseen count", err)
		return
	}
	writeJSON(w, http.StatusOK, feedDTO(feed, chans, unseen))
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
		})
		if err != nil {
			return err
		}
		if req.ChannelIDs != nil {
			return setFeedChannels(r.Context(), q, fid, req.ChannelIDs)
		}
		return nil
	})
	if err != nil {
		s.writeDBError(w, "update feed", err)
		return
	}
	chans, err := s.q.ListFeedChannels(r.Context(), fid)
	if err != nil {
		s.writeDBError(w, "list feed channels", err)
		return
	}
	chans = orEmptyIDs(chans)
	unseen, err := s.unseenForChannels(r.Context(), chans)
	if err != nil {
		s.writeTAError(w, "feed unseen count", err)
		return
	}
	writeJSON(w, http.StatusOK, feedDTO(feed, chans, unseen))
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
		o = listOpts{Sort: prefs.EverythingSort, IncludeShorts: prefs.EverythingIncludeShorts, UnseenOnly: prefs.EverythingHideSeen}
	} else {
		feed, chans, err := s.loadFeed(r.Context(), uid, id)
		if err != nil {
			return o, err
		}
		o = listOpts{ChannelIDs: chans, Sort: feed.Sort, IncludeShorts: feed.IncludeShorts, SubtitlesOnly: feed.SubtitlesOnly, UnseenOnly: feed.HideSeen}
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
	var items []VideoSummary
	if r.URL.Query().Get("view") == "continue" {
		items, err = s.continueList(r.Context(), uid, o.ChannelIDs, o.IncludeShorts)
	} else {
		items, err = s.buildList(r.Context(), uid, o)
	}
	if err != nil {
		s.writeTAError(w, "list feed videos", err)
		return
	}
	writeJSON(w, http.StatusOK, slicePage(items, parsePaging(r)))
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
