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
// every few minutes, for every feed that asked, ask the archive what it
// downloaded for the feed's sources since the last look. The question is
// *downloaded*, not *published*: a channel backfill fetches old uploads
// today, and those are news to the archive even if not to YouTube. One
// notification per feed per pass, however many arrived, and the feed's
// high-water mark moves to the newest of them.
//
// The mark is set to *now* when a feed's flag is switched on (see the
// CreateFeed/UpdateFeed queries), so switching it on for a channel with a
// thousand videos announces nothing until the next one lands — the same
// baseline rule series watches follow. It also moves when the user has no
// device to reach: a phone registered next week should not get last week's
// downloads in one burst.

const (
	// notifyEvery is the poll interval. It reads one page per source per
	// notifying feed — nothing is derived, nothing is written to the archive
	// — so it can afford to be frequent without the pause the prepare job
	// takes for playback.
	notifyEvery = 5 * time.Minute
	// notifyDelay keeps the first pass off the boot path.
	notifyDelay = time.Minute
	// notifyPageSize is how far down each source's newest downloads one pass
	// looks. More than this arriving for one source in one interval is a
	// backfill, and a backfill gets one notification, not one per video.
	notifyPageSize = 25
	// notifyTitles is how many titles a digest names before "and N more".
	notifyTitles = 3
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

// notifyFeed announces what one feed received since its mark, and moves the
// mark. A feed whose flag is on but whose mark is unset (a row from before
// the column had a value) starts now, like a fresh switch-on.
func (s *Server) notifyFeed(ctx context.Context, f sqlc.Feed, devices []sqlc.PushDevice) error {
	now := time.Now()
	if !f.NotifiedAt.Valid {
		return s.q.SetFeedNotifiedAt(ctx, sqlc.SetFeedNotifiedAtParams{ID: f.ID, NotifiedAt: pgtype.Timestamptz{Time: now, Valid: true}})
	}
	fresh, err := s.newlyDownloaded(ctx, f, f.NotifiedAt.Time)
	if err != nil {
		return err
	}
	s.log.Debug("notify: feed", "feed", f.ID, "since", f.NotifiedAt.Time, "fresh", len(fresh), "devices", len(devices))
	if len(fresh) == 0 {
		return nil
	}
	newest := f.NotifiedAt.Time
	for _, v := range fresh {
		if v.Downloaded.After(newest) {
			newest = v.Downloaded
		}
	}
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
				return err
			}
			delivered = true
		default:
			s.log.Warn("notify: send", "feed", f.ID, "err", err)
		}
	}
	if !delivered {
		// Nothing reached anyone and the failure was Apple's or the
		// network's, not the token's: leave the mark, try again next pass.
		return nil
	}
	return s.q.SetFeedNotifiedAt(ctx, sqlc.SetFeedNotifiedAtParams{ID: f.ID, NotifiedAt: pgtype.Timestamptz{Time: newest, Valid: true}})
}

// newlyDownloaded is what the feed's sources received after `since` that the
// viewer has not already watched or dismissed, newest download first. One
// page per source, deliberately: the feed's full listing walks every page
// of every channel, and this runs every five minutes.
func (s *Server) newlyDownloaded(ctx context.Context, f sqlc.Feed, since time.Time) ([]VideoSummary, error) {
	chans, err := s.q.ListFeedChannels(ctx, f.ID)
	if err != nil {
		return nil, err
	}
	pls, err := s.q.ListFeedPlaylists(ctx, f.ID)
	if err != nil {
		return nil, err
	}
	field, order := taSort(sortDownloaded)
	queries := make([]ta.VideoQuery, 0, len(chans)+len(pls))
	for _, ch := range chans {
		queries = append(queries, ta.VideoQuery{Channel: ch, Watch: "unwatched", Sort: field, Order: order, PageSize: notifyPageSize, Page: 1})
	}
	for _, pl := range pls {
		queries = append(queries, ta.VideoQuery{Playlist: pl, Watch: "unwatched", Sort: field, Order: order, PageSize: notifyPageSize, Page: 1})
	}
	var mu sync.Mutex
	var merged []ta.Video
	seen := map[string]bool{}
	err = parallel(ctx, queries, func(ctx context.Context, _ int, q ta.VideoQuery) error {
		page, err := s.ta.ListVideos(ctx, q)
		if err != nil {
			return err
		}
		mu.Lock()
		defer mu.Unlock()
		for _, v := range page.Data {
			if seen[v.YoutubeID] || !v.DownloadedTime().After(since) {
				continue
			}
			if !f.IncludeShorts && v.Kind() == "short" {
				continue
			}
			if f.SubtitlesOnly && len(v.Subtitles) == 0 {
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
	items, err := s.overlay(ctx, f.UserID, merged)
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
