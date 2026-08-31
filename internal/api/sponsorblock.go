package api

import (
	"context"
	"time"

	"github.com/Seklfreak/flimm/internal/sponsorblock"
	"github.com/Seklfreak/flimm/internal/ta"
)

// sponsorSegments resolves a video's SponsorBlock segments.
//
// The service is the authoritative source when one is configured — segments
// keep being submitted, corrected and downvoted long after a download, so an
// answer of "none" from it wins over a stale snapshot. The snapshot
// TubeArchivist stored at download time is the fallback: no service
// configured, or a lookup that failed (offline deploy, outage, timeout).
// Chapter segments are left out; they reach clients through
// GET /videos/{id}/chapters instead of the player's tint overlay.
func (s *Server) sponsorSegments(ctx context.Context, v *ta.Video) []SponsorSegment {
	if s.sponsorblock == nil {
		return taSponsorSegments(v)
	}
	// Cached first, and served however old it is: the alternative is making a
	// viewer wait on a service that has been measured at five seconds, for a
	// list of segments that barely changes after a video's first week.
	if row, ok := s.cacheLoad(ctx, sourceSponsorBlock, []string{v.YoutubeID})[v.YoutubeID]; ok {
		var payload []sponsorblock.Segment
		if row.decode(&payload) {
			if !row.fresh(sourceSponsorBlock, time.Now()) {
				s.cacheQueue(sourceSponsorBlock, v.YoutubeID)
			}
			return apiSponsorSegments(payload, v.Player.Duration)
		}
	}
	// Nothing known yet. Unlike crowd titles this has a fallback worth having —
	// the snapshot TubeArchivist stored when it downloaded the video — so the
	// service is asked in the background and nobody waits at all. The next play
	// of this video gets the live list.
	s.cacheQueue(sourceSponsorBlock, v.YoutubeID)
	return taSponsorSegments(v)
}

// fetchSponsorSegments asks the service and records the answer, including an
// empty one: "this video has no segments" is the common case and the one worth
// not asking twice.
func (s *Server) fetchSponsorSegments(ctx context.Context, id string) {
	if s.sponsorblock == nil {
		return
	}
	segs, err := s.sponsorblock.Segments(ctx, id)
	if err != nil {
		s.log.Debug("sponsorblock lookup failed", "video", id, "err", err)
		return
	}
	if segs == nil {
		segs = []sponsorblock.Segment{}
	}
	s.detachedSave(ctx, sourceSponsorBlock, id, segs, len(segs) > 0)
}

// apiSponsorSegments maps fetched segments to the API shape, dropping what
// does not fit this copy of the video: a segment submitted against a
// different cut can start past the end of ours, and one that overruns it is
// clamped rather than left to seek the player past the last frame.
func apiSponsorSegments(in []sponsorblock.Segment, duration float64) []SponsorSegment {
	out := make([]SponsorSegment, 0, len(in))
	for _, s := range in {
		if s.ActionType == sponsorblock.ActionChapter || s.Category == sponsorblock.CategoryChapter {
			continue
		}
		start, end := s.Start, s.End
		if duration > 0 {
			if start >= duration {
				continue
			}
			if end > duration {
				end = duration
			}
		}
		out = append(out, SponsorSegment{
			Category:   s.Category,
			ActionType: s.ActionType,
			Start:      start,
			End:        end,
		})
	}
	return out
}

// taSponsorSegments reads the segments TubeArchivist indexed at download
// time. It stores no action type, so every one of them is a skip.
func taSponsorSegments(v *ta.Video) []SponsorSegment {
	out := make([]SponsorSegment, 0, len(v.Sponsorblock.Segments))
	for _, seg := range v.Sponsorblock.Segments {
		out = append(out, SponsorSegment{
			Category:   seg.Category,
			ActionType: sponsorblock.ActionSkip,
			Start:      seg.Segment[0],
			End:        seg.Segment[1],
		})
	}
	return out
}

// sponsorblockChapters returns crowd-sourced chapter markers for a video, or
// nil when there is no service, the lookup failed or nobody has submitted
// any. Every failure is a miss, not an error: the caller falls back to the
// description.
func (s *Server) sponsorblockChapters(ctx context.Context, v *ta.Video) []ta.Chapter {
	if s.sponsorblock == nil {
		return nil
	}
	segs, err := s.sponsorblock.Segments(ctx, v.YoutubeID)
	if err != nil {
		s.log.Debug("sponsorblock: chapter lookup failed", "video", v.YoutubeID, "err", err)
		return nil
	}
	var out []ta.Chapter
	for _, seg := range segs {
		if seg.ActionType != sponsorblock.ActionChapter || seg.Description == "" {
			continue
		}
		out = append(out, ta.Chapter{Start: seg.Start, Title: seg.Description})
	}
	return out
}
