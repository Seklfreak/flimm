package api

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Seklfreak/flimm/internal/db/sqlc"
	"github.com/Seklfreak/flimm/internal/dearrow"
	"github.com/Seklfreak/flimm/internal/ta"
)

// The DeArrow cache: how a page gets crowd titles without waiting for them.
//
// The rule is that a viewer waits at most once per video, ever. A row in
// `dearrow_branding` is served immediately however old it is; if it is past its
// freshness window a refresh is queued behind the response. Only a video **no
// row exists for** is fetched inside the request, and even then under a
// deadline, because the service that answers in 300ms most of the time has been
// measured at fifteen seconds.
//
// So the cost falls on the day a video is downloaded and never again — and the
// sweep below usually gets there first.

const (
	// brandingFreshSubmission is how long a row that carries an actual
	// submission is served without refreshing. Votes move; a day is close
	// enough for a title.
	brandingFreshSubmission = 24 * time.Hour
	// brandingFreshEmpty is the same for "nobody has submitted anything",
	// which is around nine rows in ten and the least likely to change. A week
	// late on a title that has never existed costs nothing.
	brandingFreshEmpty = 7 * 24 * time.Hour
	// brandingInlineWait bounds the one case that blocks: a video nothing is
	// known about. Past this the page goes out with the archive's own title and
	// the lookup finishes in the background, so the next view is right.
	brandingInlineWait = 2500 * time.Millisecond
	// brandingQueue is how many videos may be waiting to be fetched in the
	// background. Full means dropping the newest request, which costs a later
	// refresh rather than anything a viewer sees.
	brandingQueue = 4096
	// brandingWorkers fetch from the queue. Deliberately small: this is
	// somebody else's service, and nothing here is urgent.
	brandingWorkers = 2
	// brandingSweepEvery is how often the archive is walked for videos with no
	// row or a stale one.
	brandingSweepEvery = 6 * time.Hour
)

// brandingRecord is what the cache knows about one video.
type brandingRecord struct {
	branding dearrow.Branding
	// submission reports that somebody has actually submitted something, which
	// decides how long this stays fresh.
	submission bool
	fetchedAt  time.Time
}

// fresh reports whether this row can be served without queueing a refresh.
func (r brandingRecord) fresh(now time.Time) bool {
	window := brandingFreshEmpty
	if r.submission {
		window = brandingFreshSubmission
	}
	return now.Sub(r.fetchedAt) < window
}

// hasSubmission reports whether anyone has submitted a title or a thumbnail.
// DeArrow returns a suggested `RandomTime` for every video it has ever heard
// of, so that field alone does not count as data — treating it as data would
// make the whole archive look "submitted" and halve the freshness window for
// nothing.
func hasSubmission(b dearrow.Branding) bool {
	return b.Title != "" || b.OriginalTitleWon || b.ThumbnailTime != nil || b.OriginalThumbnailWon
}

// loadBranding reads what is known about these videos.
func (s *Server) loadBranding(ctx context.Context, ids []string) map[string]brandingRecord {
	if s.q == nil || len(ids) == 0 {
		return nil
	}
	rows, err := s.q.ListBranding(ctx, ids)
	if err != nil {
		// A cache that cannot be read is a slow page, not a broken one.
		s.log.Debug("dearrow cache read failed", "err", err)
		return nil
	}
	out := make(map[string]brandingRecord, len(rows))
	for _, row := range rows {
		b := dearrow.Branding{
			Title:                row.Title,
			OriginalTitleWon:     row.OriginalTitleWon,
			OriginalThumbnailWon: row.OriginalThumbWon,
			RandomTime:           row.RandomTime,
		}
		if row.ThumbnailTime.Valid {
			t := row.ThumbnailTime.Float64
			b.ThumbnailTime = &t
		}
		out[row.VideoID] = brandingRecord{
			branding:   b,
			submission: row.HasSubmission,
			fetchedAt:  row.FetchedAt.Time,
		}
	}
	return out
}

// saveBranding records what the service said, including that it said nothing.
func (s *Server) saveBranding(ctx context.Context, id string, b dearrow.Branding) {
	if s.q == nil {
		return
	}
	arg := sqlc.UpsertBrandingParams{
		VideoID:          id,
		Title:            b.Title,
		OriginalTitleWon: b.OriginalTitleWon,
		OriginalThumbWon: b.OriginalThumbnailWon,
		RandomTime:       b.RandomTime,
		HasSubmission:    hasSubmission(b),
	}
	if b.ThumbnailTime != nil {
		arg.ThumbnailTime = pgtype.Float8{Float64: *b.ThumbnailTime, Valid: true}
	}
	if err := s.q.UpsertBranding(ctx, arg); err != nil {
		s.log.Debug("dearrow cache write failed", "video", id, "err", err)
	}
}

// fetchBranding asks the service and records the answer.
func (s *Server) fetchBranding(ctx context.Context, id string) (dearrow.Branding, bool) {
	b, err := s.dearrow.Branding(ctx, id)
	if err != nil {
		return dearrow.Branding{}, false
	}
	// Saved with a context of its own: the request that triggered this may be
	// finished, and the answer is still worth keeping.
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	s.saveBranding(saveCtx, id, b)
	return b, true
}

// queueBranding asks for a video to be looked up in the background. It never
// blocks: a full queue means the refresh happens on some later page instead.
func (s *Server) queueBranding(ids ...string) {
	if s.brandingQueue == nil {
		return
	}
	for _, id := range ids {
		select {
		case s.brandingQueue <- id:
		default:
			return
		}
	}
}

// StartBrandingWarmer runs the background half of the cache: the workers that
// drain the queue, and the sweep that fills it from the archive.
//
// Without the sweep every new video would be paid for by whoever happened to
// open the page it first appears on. With it, the bill is usually settled
// before anyone looks.
func (s *Server) StartBrandingWarmer(ctx context.Context) {
	if s.dearrow == nil || s.q == nil || s.brandingQueue == nil {
		return
	}
	for range brandingWorkers {
		go s.brandingWorker(ctx)
	}
	go s.brandingSweep(ctx)
}

func (s *Server) brandingWorker(ctx context.Context) {
	// A gap between lookups: this is somebody else's service, and a warm cache
	// an hour from now is worth exactly as much as one right now.
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case id := <-s.brandingQueue:
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
			s.fetchBranding(ctx, id)
		}
	}
}

// brandingSweep walks the archive and queues whatever has no row or a stale
// one, then does it again every few hours.
func (s *Server) brandingSweep(ctx context.Context) {
	// Not immediately on boot: a deploy should not be followed by a burst of
	// outbound requests while the app is also serving its first pages.
	select {
	case <-ctx.Done():
		return
	case <-time.After(2 * time.Minute):
	}
	for {
		s.sweepOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(brandingSweepEvery):
		}
	}
}

// sweepOnce walks every video the archive holds once.
func (s *Server) sweepOnce(ctx context.Context) {
	now := time.Now()
	queued, seen := 0, 0
	for page := 1; ; page++ {
		res, err := s.ta.ListVideos(ctx, ta.VideoQuery{Page: page, PageSize: maxPageSize})
		if err != nil {
			s.log.Debug("dearrow sweep: list videos", "page", page, "err", err)
			return
		}
		if len(res.Data) == 0 {
			break
		}
		ids := make([]string, 0, len(res.Data))
		for _, v := range res.Data {
			ids = append(ids, v.YoutubeID)
		}
		seen += len(ids)
		known := s.loadBranding(ctx, ids)
		for _, id := range ids {
			if row, ok := known[id]; ok && row.fresh(now) {
				continue
			}
			s.queueBranding(id)
			queued++
		}
		if res.Paginate.LastPage > 0 && page >= res.Paginate.LastPage {
			break
		}
		if ctx.Err() != nil {
			return
		}
	}
	s.log.Info("dearrow sweep", "videos", seen, "queued", queued)
}
