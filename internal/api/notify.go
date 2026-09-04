package api

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Seklfreak/flimm/internal/apns"
	"github.com/Seklfreak/flimm/internal/db/sqlc"
	"github.com/Seklfreak/flimm/internal/ta"
)

// The feed notifier: "tell me when this feed has something new."
//
// TubeArchivist has no webhook and no per-channel event, so this is a poll —
// every few minutes, for every feed that asked, look at what the archive
// indexed for the feed's sources since the last look and announce the ids
// the feed has never seen. One notification per feed per pass, however many
// arrived.
//
// *Never seen*, not *downloaded after*: TubeArchivist's date_downloaded is
// written by the indexer from the same clock as vid_last_refresh, so every
// metadata refresh of an old video makes it read as downloaded just now, and
// a pass keyed on that timestamp announced February's uploads all morning.
// The timestamp still bounds the scan — only what was indexed since the last
// pass is fetched — but the decision is the id against notify_seen.
//
// A feed switched on is *seeded* first: every video its sources hold is
// marked seen without a word, so only what arrives afterwards is news — the
// same baseline rule series watches follow, and it holds when the user has
// no device yet, so a phone registered next week does not get last week's
// downloads in one burst.

const (
	// notifyEvery is the poll interval. A pass reads the archive from the
	// newest index time down to the last pass and writes nothing to it, so it
	// can afford to be frequent without the pause the prepare job takes for
	// playback.
	notifyEvery = 5 * time.Minute
	// notifyDelay keeps the first pass off the boot path.
	notifyDelay = time.Minute
	// notifyScanPages bounds one source's scan in a pass. TubeArchivist
	// pages at its own size (twelve, typically); more than this many pages
	// indexed in one interval is a mass refresh, and what it pushed past the
	// bound is caught by the next pass's overlap or was a refresh anyway.
	notifyScanPages = 20
	// notifyOverlap is subtracted from the mark when scanning, because
	// date_downloaded has whole-second precision and the mark does not. The
	// seen set makes an overlap free.
	notifyOverlap = time.Minute
	// notifyTitles is how many titles a digest names before "and N more".
	notifyTitles = 3
	// notifySeenBatch is how many ids one MarkNotifySeen insert carries.
	notifySeenBatch = 500
)

// StartFeedNotifier runs the poll until ctx ends. A server with no APNs
// client never starts it: the flag is stored, nothing is sent.
func (s *Server) StartFeedNotifier(ctx context.Context) {
	if s.q == nil || s.push == nil {
		return
	}
	go func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(notifyDelay):
		}
		for {
			s.notifyOnce(ctx)
			select {
			case <-ctx.Done():
				return
			case <-time.After(notifyEvery):
			}
		}
	}()
}

// notifyOnce is one pass over every notifying feed of every user.
func (s *Server) notifyOnce(ctx context.Context) {
	feeds, err := s.q.ListNotifyFeeds(ctx)
	if err != nil {
		s.log.Warn("notify: list feeds", "err", err)
		return
	}
	s.log.Debug("notify: pass", "feeds", len(feeds))
	// Devices are per user, and a user with several notifying feeds is the
	// common case; read them once per pass.
	devices := map[uuid.UUID][]sqlc.PushDevice{}
	for _, f := range feeds {
		if _, ok := devices[f.UserID]; !ok {
			d, err := s.q.ListPushDevices(ctx, f.UserID)
			if err != nil {
				s.log.Warn("notify: list devices", "user", f.UserID, "err", err)
				return
			}
			devices[f.UserID] = d
		}
		// One unreadable feed is not a reason to skip the rest.
		if err := s.notifyFeed(ctx, f, devices[f.UserID]); err != nil {
			s.log.Warn("notify: feed", "feed", f.ID, "err", err)
		}
	}
}

// notifyFeed seeds a feed that needs it, or announces what one received
// since its mark and moves the mark to the start of this pass.
func (s *Server) notifyFeed(ctx context.Context, f sqlc.Feed, devices []sqlc.PushDevice) error {
	start := time.Now()
	chans, err := s.q.ListFeedChannels(ctx, f.ID)
	if err != nil {
		return err
	}
	pls, err := s.q.ListFeedPlaylists(ctx, f.ID)
	if err != nil {
		return err
	}
	if !f.NotifySeeded || !f.NotifiedAt.Valid {
		return s.seedFeed(ctx, f, chans, pls, start)
	}
	since := f.NotifiedAt.Time.Add(-notifyOverlap)
	candidates, err := s.indexedSince(ctx, chans, pls, since)
	if err != nil {
		return err
	}
	fresh, err := s.unseenOf(ctx, f, candidates)
	if err != nil {
		return err
	}
	s.log.Debug("notify: feed", "feed", f.ID, "since", since, "indexed", len(candidates), "fresh", len(fresh), "devices", len(devices))
	if len(fresh) > 0 && !s.deliver(ctx, f, fresh, devices) {
		// Nothing reached anyone and the failure was Apple's or the
		// network's, not the token's: leave everything, try again next pass.
		return nil
	}
	// Everything indexed in this window is now known — the announced, the
	// filtered-out, and the refreshed — so none of it comes back.
	if err := s.markSeen(ctx, f.UserID, videoIDs(candidates)); err != nil {
		return err
	}
	return s.q.SetFeedNotifiedAt(ctx, sqlc.SetFeedNotifiedAtParams{ID: f.ID, NotifiedAt: pgtype.Timestamptz{Time: start, Valid: true}})
}

// seedFeed marks everything the feed's sources hold as seen and starts the
// mark, announcing nothing: the archive's contents at switch-on are not
// news. Walks every page of every source — the one time the notifier does.
func (s *Server) seedFeed(ctx context.Context, f sqlc.Feed, chans, pls []string, start time.Time) error {
	var mu sync.Mutex
	var ids []string
	err := parallel(ctx, sourceQueries(chans, pls, ta.VideoQuery{}), func(ctx context.Context, _ int, q ta.VideoQuery) error {
		vids, err := s.fetchEvery(ctx, q)
		if err != nil {
			return err
		}
		mu.Lock()
		defer mu.Unlock()
		for _, v := range vids {
			ids = append(ids, v.YoutubeID)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := s.markSeen(ctx, f.UserID, ids); err != nil {
		return err
	}
	s.log.Info("notify: seeded feed", "feed", f.ID, "videos", len(ids))
	if err := s.q.SetFeedNotifySeeded(ctx, sqlc.SetFeedNotifySeededParams{ID: f.ID, NotifySeeded: true}); err != nil {
		return err
	}
	return s.q.SetFeedNotifiedAt(ctx, sqlc.SetFeedNotifiedAtParams{ID: f.ID, NotifiedAt: pgtype.Timestamptz{Time: start, Valid: true}})
}

// sourceQueries is one query per source with the given options.
func sourceQueries(chans, pls []string, base ta.VideoQuery) []ta.VideoQuery {
	out := make([]ta.VideoQuery, 0, len(chans)+len(pls))
	for _, ch := range chans {
		q := base
		q.Channel = ch
		out = append(out, q)
	}
	for _, pl := range pls {
		q := base
		q.Playlist = pl
		out = append(out, q)
	}
	return out
}

// seedWalks is how many times a source is walked when a walk comes up short
// of the archive's own count before the seed gives up on it for this pass.
const seedWalks = 3

// fetchEvery is fetchAll without its cap: a seed has to know every video, or
// the ones past the cap announce on their next refresh.
//
// Walked oldest-published first, and checked against the archive's count.
// The archive's default order is whatever the operator sorts their own
// pages by — date_downloaded for some — and that order is rewritten under
// the walk while a reindex sweep runs, so pages shift and a video slips
// between two of them. The seed that missed one that way announced it at
// its next refresh. Publish dates do not move on a refresh, ascending puts
// new arrivals at the end where nothing shifts, and the count says whether
// the walk was whole.
func (s *Server) fetchEvery(ctx context.Context, q ta.VideoQuery) ([]ta.Video, error) {
	q.PageSize = maxPageSize
	q.Sort, q.Order = "published", "asc"
	var out []ta.Video
	for attempt := 1; attempt <= seedWalks; attempt++ {
		out = out[:0]
		seen := map[string]bool{}
		total := 0
		for page := 1; ; page++ {
			q.Page = page
			res, err := s.ta.ListVideos(ctx, q)
			if err != nil {
				return nil, err
			}
			if page == 1 {
				total = res.Paginate.TotalHits
			}
			if len(res.Data) == 0 {
				break
			}
			for _, v := range res.Data {
				if !seen[v.YoutubeID] {
					seen[v.YoutubeID] = true
					out = append(out, v)
				}
			}
			if res.Paginate.LastPage > 0 && page >= res.Paginate.LastPage {
				break
			}
			if total > 0 && len(out) >= total {
				break
			}
		}
		if len(out) >= total {
			return out, nil
		}
		s.log.Warn("notify: seed walk came up short", "channel", q.Channel, "playlist", q.Playlist, "got", len(out), "total", total, "attempt", attempt)
	}
	return nil, fmt.Errorf("seed: %s%s: %d of its videos could not all be read", q.Channel, q.Playlist, len(out))
}

// indexedSince is everything the sources indexed after `since`, newest
// first: each source is read from the top of its date_downloaded order down
// to the mark, and no further.
func (s *Server) indexedSince(ctx context.Context, chans, pls []string, since time.Time) ([]ta.Video, error) {
	field, order := taSort(sortDownloaded)
	base := ta.VideoQuery{Sort: field, Order: order, PageSize: maxPageSize}
	var mu sync.Mutex
	var merged []ta.Video
	seen := map[string]bool{}
	err := parallel(ctx, sourceQueries(chans, pls, base), func(ctx context.Context, _ int, q ta.VideoQuery) error {
		for page := 1; page <= notifyScanPages; page++ {
			q.Page = page
			res, err := s.ta.ListVideos(ctx, q)
			if err != nil {
				return err
			}
			older := len(res.Data) == 0
			mu.Lock()
			for _, v := range res.Data {
				if !v.DownloadedTime().After(since) {
					older = true
					break
				}
				if !seen[v.YoutubeID] {
					seen[v.YoutubeID] = true
					merged = append(merged, v)
				}
			}
			mu.Unlock()
			if older || (res.Paginate.LastPage > 0 && page >= res.Paginate.LastPage) {
				break
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return merged, nil
}

// unseenOf is the candidates the user has never seen that the feed would
// show: not a refreshed old video, not a Short in a feed without them, not
// watched, not dismissed. Newest index first.
func (s *Server) unseenOf(ctx context.Context, f sqlc.Feed, candidates []ta.Video) ([]VideoSummary, error) {
	if len(candidates) == 0 {
		return nil, nil
	}
	known, err := s.q.ListNotifySeen(ctx, sqlc.ListNotifySeenParams{UserID: f.UserID, VideoIds: videoIDs(candidates)})
	if err != nil {
		return nil, err
	}
	isKnown := make(map[string]bool, len(known))
	for _, id := range known {
		isKnown[id] = true
	}
	var unseen []ta.Video
	for _, v := range candidates {
		if isKnown[v.YoutubeID] {
			continue
		}
		if !f.IncludeShorts && v.Kind() == "short" {
			continue
		}
		if f.SubtitlesOnly && len(v.Subtitles) == 0 {
			continue
		}
		unseen = append(unseen, v)
	}
	items, err := s.overlay(ctx, f.UserID, unseen)
	if err != nil {
		return nil, err
	}
	kept := items[:0]
	for _, it := range items {
		if !it.Watched && !it.Dismissed {
			kept = append(kept, it)
		}
	}
	sortSummaries(kept, sortDownloaded)
	return kept, nil
}

// markSeen records ids as known, in batches.
func (s *Server) markSeen(ctx context.Context, uid uuid.UUID, ids []string) error {
	for start := 0; start < len(ids); start += notifySeenBatch {
		end := min(start+notifySeenBatch, len(ids))
		if err := s.q.MarkNotifySeen(ctx, sqlc.MarkNotifySeenParams{UserID: uid, VideoIds: ids[start:end]}); err != nil {
			return err
		}
	}
	return nil
}

// deliver sends the feed's alert to every device and reports whether it
// counts as delivered: it reached someone, or there was nobody to reach, or
// every failure was a dead token (which is forgotten here).
func (s *Server) deliver(ctx context.Context, f sqlc.Feed, fresh []VideoSummary, devices []sqlc.PushDevice) bool {
	delivered := len(devices) == 0
	for _, d := range devices {
		env, _ := apns.ParseEnvironment(d.Environment)
		err := s.push.Send(ctx, notificationFor(f, fresh, d.Token, env))
		switch {
		case err == nil:
			delivered = true
		case errors.Is(err, apns.ErrBadToken):
			// The phone is gone, or the token belongs to the other APNs.
			// Forgetting it is the only thing that stops the same failure
			// every five minutes for ever.
			s.log.Info("notify: forgetting device", "user", f.UserID, "reason", err)
			if err := s.q.ForgetPushDevice(ctx, d.Token); err != nil {
				s.log.Warn("notify: forget device", "err", err)
			}
			delivered = true
		default:
			s.log.Warn("notify: send", "feed", f.ID, "err", err)
		}
	}
	return delivered
}

// notificationFor is the one alert a pass sends for a feed. A single video
// is announced by name and channel and opens itself when tapped; several
// are a digest that opens the feed.
func notificationFor(f sqlc.Feed, fresh []VideoSummary, token string, env apns.Environment) apns.Notification {
	n := apns.Notification{
		Token: token, Environment: env,
		ThreadID: f.ID.String(),
		Data:     map[string]any{"feed": f.ID.String()},
	}
	if len(fresh) == 1 {
		v := fresh[0]
		n.Title = v.Channel.Name
		n.Subtitle = f.Name
		n.Body = v.Title
		n.Data["video"] = v.ID
		return n
	}
	n.Title = f.Name
	n.Body = digest(fresh)
	return n
}

// digest reads "3 new videos: A, B and C" or "5 new videos: A, B, C and 2
// more" — enough to decide whether to look, short enough to fit a banner.
func digest(fresh []VideoSummary) string {
	titles := make([]string, 0, notifyTitles)
	for i, v := range fresh {
		if i == notifyTitles {
			break
		}
		titles = append(titles, v.Title)
	}
	rest := len(fresh) - len(titles)
	var list string
	switch {
	case rest > 0:
		list = strings.Join(titles, ", ") + fmt.Sprintf(" and %d more", rest)
	case len(titles) > 1:
		list = strings.Join(titles[:len(titles)-1], ", ") + " and " + titles[len(titles)-1]
	default:
		list = strings.Join(titles, "")
	}
	return fmt.Sprintf("%d new videos: %s", len(fresh), list)
}
