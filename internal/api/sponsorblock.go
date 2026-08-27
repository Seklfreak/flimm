package api

import (
	"context"

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
	if s.sponsorblock != nil {
		segs, err := s.sponsorblock.Segments(ctx, v.YoutubeID)
		if err == nil {
			return apiSponsorSegments(segs, v.Player.Duration)
		}
		s.log.Debug("sponsorblock: lookup failed, using the TubeArchivist snapshot",
			"video", v.YoutubeID, "err", err)
	}
	return taSponsorSegments(v)
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
