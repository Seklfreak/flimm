package api

import (
	"context"
	"time"

	"github.com/Seklfreak/flimm/internal/dearrow"
	"github.com/Seklfreak/flimm/internal/ta"
)

// The DeArrow half of the external cache: crowd titles for a whole page.
//
// It is the only source that is read for thirty videos at once, and the only
// one with nothing to fall back on — an archive title is not a crowd title. So
// it is also the only one that ever makes a request wait, and the only one that
// is swept ahead of time. See extcache.go for the shared rules.

const (
	// brandingInlineWait bounds the one case that blocks: a video nothing is
	// known about. Past this the page goes out with the archive's own title and
	// the lookup finishes in the background, so the next view is right.
	brandingInlineWait = 2500 * time.Millisecond
	// cacheJobsQueue is how many background lookups may be waiting, across all
	// sources. Full means dropping the newest, which costs a later refresh
	// rather than anything a viewer sees.
	cacheJobsQueue = 4096
	// cacheWorkers drain that queue. Deliberately few: this is somebody else's
	// service and nothing here is urgent.
	cacheWorkers = 2
	// cacheLookupGap paces each worker.
	cacheLookupGap = 250 * time.Millisecond
	// brandingSweepEvery is how often the archive is walked for videos with no
	// row or a stale one.
	brandingSweepEvery = 6 * time.Hour
)

// brandingPayload is a Branding as the cache stores it. The field names are
// also the JSON the 007 migration wrote when it carried the old table over, so
// they are a stored format rather than an implementation detail.
type brandingPayload struct {
	Title                string   `json:"title"`
	OriginalTitleWon     bool     `json:"original_title_won"`
	ThumbnailTime        *float64 `json:"thumbnail_time,omitempty"`
	OriginalThumbnailWon bool     `json:"original_thumb_won"`
	RandomTime           float64  `json:"random_time"`
}

func (p brandingPayload) branding() dearrow.Branding {
	return dearrow.Branding{
		Title:                p.Title,
		OriginalTitleWon:     p.OriginalTitleWon,
		ThumbnailTime:        p.ThumbnailTime,
		OriginalThumbnailWon: p.OriginalThumbnailWon,
		RandomTime:           p.RandomTime,
	}
}

func brandingPayloadOf(b dearrow.Branding) brandingPayload {
	return brandingPayload{
		Title:                b.Title,
		OriginalTitleWon:     b.OriginalTitleWon,
		ThumbnailTime:        b.ThumbnailTime,
		OriginalThumbnailWon: b.OriginalThumbnailWon,
		RandomTime:           b.RandomTime,
	}
}

// hasSubmission reports whether anyone has submitted a title or a thumbnail.
// DeArrow returns a suggested `RandomTime` for every video it has ever heard
// of, so that field alone does not count as data — treating it as data would
// put the whole archive on the short freshness window for nothing.
func hasSubmission(b dearrow.Branding) bool {
	return b.Title != "" || b.OriginalTitleWon || b.ThumbnailTime != nil || b.OriginalThumbnailWon
}

// fetchBranding asks DeArrow and records the answer, including "nothing".
func (s *Server) fetchBranding(ctx context.Context, id string) (dearrow.Branding, bool) {
	if s.dearrow == nil {
		return dearrow.Branding{}, false
	}
	b, err := s.dearrow.Branding(ctx, id)
	if err != nil {
		return dearrow.Branding{}, false
	}
	s.detachedSave(ctx, sourceDeArrow, id, brandingPayloadOf(b), hasSubmission(b))
	return b, true
}

// brandingSweep walks the archive and queues whatever has no row or a stale
// one, then does it again every few hours.
//
// Without it, every newly downloaded video would be paid for by whoever
// happened to open the page it first appears on. With it, the bill is usually
// settled before anyone looks.
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
		known := s.cacheLoad(ctx, sourceDeArrow, ids)
		for _, id := range ids {
			if row, ok := known[id]; ok && row.fresh(sourceDeArrow, now) {
				continue
			}
			s.cacheQueue(sourceDeArrow, id)
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
