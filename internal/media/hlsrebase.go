package media

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
)

// Putting a run's segments back on the rendition's timeline.
//
// ffmpeg's HLS muxer numbers every fragment it writes from zero: the first
// segment of a run gets `tfdt` 0 whatever `-ss` asked for. `-output_ts_offset`
// does not move it either — it only writes an empty edit into the init segment,
// which then applies to *every* segment in the rendition and puts the other
// run's output in the wrong place. Verified against ffmpeg 8: a rendition
// stitched from two runs that way decodes as 16 seconds of overlapping video.
//
// So the offset is applied afterwards, here: a run starting at segment k has
// 4k seconds added to each segment's `tfdt` (and to the `earliest_presentation_
// time` of its segment index), which is exactly what a single run over the whole
// video would have written. Nothing else in the segment changes, and the two
// runs' init segments are then byte-identical — which they are not when
// `-output_ts_offset` is used.
//
// The fields are fixed-size, so this is a patch rather than a rewrite. A run
// that needs it writes its segments under a `.raw` suffix, invisible to the
// route, and each one is patched and renamed into place: a player never sees a
// segment whose timestamps have not been fixed up.

// hlsRawSuffix marks a segment that still has to be put on the timeline. It is
// not a name validHLSFile accepts, so such a file cannot be served.
const hlsRawSuffix = ".raw"

// maxSegmentBytes caps a segment read. Four seconds of 4K HEVC is a few tens of
// megabytes; anything near this is not a segment.
const maxSegmentBytes = 256 << 20

// publishRawSegments patches every finished `.raw` segment in dir onto the
// timeline and renames it into place, returning how many it published.
//
// initName is the init segment this run wrote, which is where the per-track
// timescales come from — a track's `tfdt` is in its own media timescale, and
// video and audio do not share one.
func publishRawSegments(dir, initName string, offsetSeconds int) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	var raw []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".m4s"+hlsRawSuffix) {
			raw = append(raw, e.Name())
		}
	}
	if len(raw) == 0 {
		return 0, nil
	}

	init, err := os.ReadFile(filepath.Join(dir, initName)) //nolint:gosec // cache dir plus a generated name
	if err != nil {
		return 0, fmt.Errorf("hls: read init segment: %w", err)
	}
	timescales := mp4Timescales(init)
	if len(timescales) == 0 {
		return 0, fmt.Errorf("hls: %s names no tracks", initName)
	}

	published := 0
	for _, name := range raw {
		src := filepath.Join(dir, name)
		b, err := readSegment(src)
		if err != nil {
			return published, err
		}
		if err := rebaseSegment(b, offsetSeconds, timescales); err != nil {
			return published, fmt.Errorf("hls: %s: %w", name, err)
		}
		// Written back under the raw name and renamed, so the segment becomes
		// visible to the route only once it is on the right timeline.
		if err := os.WriteFile(src, b, 0o600); err != nil {
			return published, fmt.Errorf("hls: write segment: %w", err)
		}
		if err := os.Rename(src, filepath.Join(dir, strings.TrimSuffix(name, hlsRawSuffix))); err != nil {
			return published, fmt.Errorf("hls: publish segment: %w", err)
		}
		published++
	}
	return published, nil
}

func readSegment(path string) ([]byte, error) {
	f, err := os.Open(path) //nolint:gosec // cache dir plus a generated name
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxSegmentBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxSegmentBytes {
		return nil, fmt.Errorf("hls: %s is larger than %d bytes", filepath.Base(path), maxSegmentBytes)
	}
	return b, nil
}

// rebaseSegment adds offsetSeconds to every decode time in an fMP4 segment.
func rebaseSegment(b []byte, offsetSeconds int, timescales map[uint32]uint32) error {
	if offsetSeconds < 0 {
		return fmt.Errorf("hls: cannot move a segment back by %ds", -offsetSeconds)
	}
	if offsetSeconds == 0 {
		return nil
	}
	var firstErr error
	fail := func(err error) {
		if firstErr == nil {
			firstErr = err
		}
	}
	mp4Walk(b, 0, len(b), func(typ string, body, end int) bool {
		switch typ {
		case "sidx":
			if err := shiftSidx(b, body, end, offsetSeconds); err != nil {
				fail(err)
			}
		case "moof":
			mp4Walk(b, body, end, func(inner string, ibody, iend int) bool {
				if inner == "traf" {
					if err := shiftTraf(b, ibody, iend, offsetSeconds, timescales); err != nil {
						fail(err)
					}
				}
				return true
			})
		}
		return true
	})
	return firstErr
}

// shiftSidx moves a segment index's earliest presentation time. The box carries
// its own timescale, so no lookup is needed.
func shiftSidx(b []byte, body, end, offsetSeconds int) error {
	if body+12 > end {
		return nil
	}
	version := b[body]
	scale := binary.BigEndian.Uint32(b[body+8:])
	return shiftTime(b, body+12, end, version, scale, offsetSeconds)
}

// shiftTraf moves one track fragment's base decode time, in that track's own
// media timescale.
func shiftTraf(b []byte, body, end, offsetSeconds int, timescales map[uint32]uint32) error {
	var scale uint32
	mp4Walk(b, body, end, func(typ string, ibody, iend int) bool {
		if typ == "tfhd" && ibody+8 <= iend {
			scale = timescales[binary.BigEndian.Uint32(b[ibody+4:])]
			return false
		}
		return true
	})
	if scale == 0 {
		return fmt.Errorf("track fragment names a track the init segment does not")
	}
	var err error
	mp4Walk(b, body, end, func(typ string, ibody, iend int) bool {
		if typ != "tfdt" || ibody+4 > iend {
			return true
		}
		err = shiftTime(b, ibody+4, iend, b[ibody], scale, offsetSeconds)
		return false
	})
	return err
}

// shiftTime adds offsetSeconds, converted to the given timescale, to the 32- or
// 64-bit time at p. A shift that would not fit is an error rather than a
// silently wrong timeline.
func shiftTime(b []byte, p, end int, version byte, scale uint32, offsetSeconds int) error {
	//nolint:gosec // G115: offsetSeconds is a segment index times four, never negative
	delta := uint64(offsetSeconds) * uint64(scale)
	if version == 0 {
		if p+4 > end {
			return nil
		}
		v := uint64(binary.BigEndian.Uint32(b[p:])) + delta
		if v > math.MaxUint32 {
			return fmt.Errorf("a %ds offset does not fit a 32-bit timestamp at %d Hz", offsetSeconds, scale)
		}
		binary.BigEndian.PutUint32(b[p:], uint32(v))
		return nil
	}
	if p+8 > end {
		return nil
	}
	binary.BigEndian.PutUint64(b[p:], binary.BigEndian.Uint64(b[p:])+delta)
	return nil
}

// mp4Timescales maps each track id in an init segment to its media timescale.
func mp4Timescales(init []byte) map[uint32]uint32 {
	out := map[uint32]uint32{}
	mp4Walk(init, 0, len(init), func(typ string, body, end int) bool {
		if typ != "moov" {
			return true
		}
		mp4Walk(init, body, end, func(t string, tbody, tend int) bool {
			if t != "trak" {
				return true
			}
			var id, scale uint32
			mp4Walk(init, tbody, tend, func(t2 string, b2, e2 int) bool {
				switch t2 {
				case "tkhd":
					// version, flags, creation, modification, track_id
					if off := b2 + 4 + fullBoxTimeFields(init[b2]); off+4 <= e2 {
						id = binary.BigEndian.Uint32(init[off:])
					}
				case "mdia":
					mp4Walk(init, b2, e2, func(t3 string, b3, e3 int) bool {
						if t3 != "mdhd" {
							return true
						}
						if off := b3 + 4 + fullBoxTimeFields(init[b3]); off+4 <= e3 {
							scale = binary.BigEndian.Uint32(init[off:])
						}
						return false
					})
				}
				return true
			})
			if id != 0 && scale != 0 {
				out[id] = scale
			}
			return true
		})
		return false
	})
	return out
}

// fullBoxTimeFields is how many bytes the creation and modification times take
// in tkhd and mdhd: 4 each in version 0, 8 each in version 1.
func fullBoxTimeFields(version byte) int {
	if version == 1 {
		return 16
	}
	return 8
}

// mp4Walk calls fn for each box in b[from:to], with the offsets of the box's
// body and of the byte after it. Returning false stops the walk. A malformed
// box ends it rather than panicking: this reads files a subprocess wrote.
func mp4Walk(b []byte, from, to int, fn func(typ string, body, end int) bool) {
	if to > len(b) {
		to = len(b)
	}
	for i := from; i >= 0 && i+8 <= to; {
		size := int(binary.BigEndian.Uint32(b[i:]))
		typ := string(b[i+4 : i+8])
		header := 8
		switch size {
		case 1:
			if i+16 > to {
				return
			}
			large := binary.BigEndian.Uint64(b[i+8:])
			// i+16 <= to above, so to-i is positive, and this comparison is what
			// stops an oversized box reaching the conversion below.
			if large > uint64(to-i) { //nolint:gosec // G115: to-i is positive here
				return
			}
			size, header = int(large), 16 //nolint:gosec // G115: bounded by to-i just above
		case 0:
			size = to - i
		}
		if size < header || i+size > to {
			return
		}
		if !fn(typ, i+header, i+size) {
			return
		}
		i += size
	}
}
