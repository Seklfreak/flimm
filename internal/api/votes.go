package api

import (
	"context"

	"github.com/Seklfreak/flimm/internal/ta"
)

// videoStats resolves a video's vote counts.
//
// TubeArchivist indexes what YouTube publishes, which since 2021 is views and
// likes and no dislikes at all. Return YouTube Dislike has the other half —
// archived while it was still public, estimated from its own users since — and
// is asked for it when a deployment has turned it on (`RYD_URL`; see the ryd
// package for why that is off by default).
//
// **The pair comes from one source or the counts are not a ratio.** Both of
// that service's numbers are estimates of the same kind, measured against each
// other; pairing its dislike count with the archive's like count from download
// day would put two different vintages either side of a slash and invite
// arithmetic on them. So when the service knows the video, both counts are
// its own. Anything else — no service, an outage, a video it has never seen —
// leaves the archive's like count exactly as it was, and no dislike count at
// all, because "unknown" and "zero" are different answers and a client must be
// able to tell them apart.
func (s *Server) videoStats(ctx context.Context, v *ta.Video) VideoStats {
	stats := VideoStats{Views: v.Stats.ViewCount, Likes: v.Stats.LikeCount}
	// The archive's own dislike count, when it has one. TubeArchivist asks the
	// same service at index time if `integrate_ryd` is on, so most deployments
	// already hold this number and never need Flimm to ask anyone — and it
	// refreshes with the rest of the metadata whenever TA reindexes the video.
	//
	// A zero is dropped rather than reported: TA stores 0 both for a video with
	// no dislikes and for one indexed while that setting was off, and those are
	// not the same claim. The service below can still supply a true zero.
	if v.Stats.DislikeCount > 0 {
		archived := v.Stats.DislikeCount
		stats.Dislikes = &archived
	}
	if s.ryd == nil {
		return stats
	}
	votes, err := s.ryd.Votes(ctx, v.YoutubeID)
	if err != nil {
		s.log.Debug("return youtube dislike: lookup failed, showing the archive's counts",
			"video", v.YoutubeID, "err", err)
		return stats
	}
	if !votes.Found {
		return stats
	}
	// One exception to taking the pair whole: a record with no likes at all
	// beside an archive that counted plenty is the service missing data, not
	// the video losing its likes.
	if votes.Likes > 0 || stats.Likes == 0 {
		stats.Likes = votes.Likes
	}
	dislikes := votes.Dislikes
	stats.Dislikes = &dislikes
	// Views take the larger of the two, which is the only comparison either
	// number supports: a view count only ever goes up, so the bigger one is
	// simply the more recently read. The archive's was true the day the file
	// was downloaded and the service's the day it last looked, and neither
	// knows which of those was later.
	stats.Views = max(stats.Views, votes.Views)
	return stats
}
