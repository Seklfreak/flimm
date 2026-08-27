package media

import "sort"

// Which part of a rendition to encode next.
//
// A rendition is N fixed 4-second segments on a timeline shared by every run,
// so "what is done" is a set of segment indices and "what to do next" is a
// choice between the holes in it. Keeping that choice here — pure, with no
// filesystem and no ffmpeg — is what makes the resume behaviour testable: the
// interesting cases (a client resuming at 40:00, then seeking to 5:00, then
// jumping ahead again) are three calls to planNextRun.

// segRange is a half-open interval of segment indices, [Start, End).
type segRange struct{ Start, End int }

// Len is the number of segments in the range.
func (r segRange) Len() int { return r.End - r.Start }

// rangesFromSet turns the set of produced segment indices into sorted,
// coalesced ranges, dropping anything outside [0, n).
func rangesFromSet(set map[int]bool, n int) []segRange {
	idx := make([]int, 0, len(set))
	for i, ok := range set {
		if ok && i >= 0 && i < n {
			idx = append(idx, i)
		}
	}
	sort.Ints(idx)

	var out []segRange
	for _, i := range idx {
		if len(out) > 0 && out[len(out)-1].End == i {
			out[len(out)-1].End = i + 1
			continue
		}
		out = append(out, segRange{Start: i, End: i + 1})
	}
	return out
}

// mergeRanges sorts and coalesces ranges, clamped to [0, n). Overlapping and
// touching ranges become one.
func mergeRanges(in []segRange, n int) []segRange {
	clamped := make([]segRange, 0, len(in))
	for _, r := range in {
		if r.Start < 0 {
			r.Start = 0
		}
		if r.End > n {
			r.End = n
		}
		if r.Len() > 0 {
			clamped = append(clamped, r)
		}
	}
	sort.Slice(clamped, func(i, j int) bool { return clamped[i].Start < clamped[j].Start })

	var out []segRange
	for _, r := range clamped {
		if len(out) > 0 && r.Start <= out[len(out)-1].End {
			if r.End > out[len(out)-1].End {
				out[len(out)-1].End = r.End
			}
			continue
		}
		out = append(out, r)
	}
	return out
}

// gapsIn returns the complement of produced within [0, n): the ranges still to
// encode, earliest first.
func gapsIn(produced []segRange, n int) []segRange {
	merged := mergeRanges(produced, n)
	var out []segRange
	at := 0
	for _, r := range merged {
		if r.Start > at {
			out = append(out, segRange{Start: at, End: r.Start})
		}
		at = r.End
	}
	if at < n {
		out = append(out, segRange{Start: at, End: n})
	}
	return out
}

// planNextRun picks the next ffmpeg run for a rendition of n segments, given
// what is already produced and the segment index a client asked for most
// recently (-1 when none has). ok is false once nothing is left.
//
// The rules, in order:
//
//  1. If the requested index falls in a gap, encode **from that index** to the
//     end of the gap. This is what makes resume work: a viewer resuming at
//     40:00 gets segment 600 first, not segment 0, and the part before it is
//     left for a later run. It is also what a mid-playback seek does, because
//     a seek is just a new requested index.
//  2. Otherwise fill the earliest gap whole. Once the viewer's position is
//     covered, the cheapest thing to be doing is completing the rendition from
//     the front, so a later seek backwards finds it already there.
//
// Deliberately *not* a rule: "encode the gap nearest the requested index".
// After run A the only gap left is usually the one before the resume point, and
// preferring the earliest keeps the order predictable — which matters more here
// than shaving a run, because every run's boundary is already on the segment
// grid and costs nothing to stitch.
func planNextRun(produced []segRange, requested, n int) (segRange, bool) {
	if n <= 0 {
		return segRange{}, false
	}
	gaps := gapsIn(produced, n)
	if len(gaps) == 0 {
		return segRange{}, false
	}
	if requested >= 0 && requested < n {
		for _, g := range gaps {
			if requested >= g.Start && requested < g.End {
				return segRange{Start: requested, End: g.End}, true
			}
		}
	}
	return gaps[0], true
}
