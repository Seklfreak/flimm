package media

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// The I-frame playlist: what a player scrubs with.
//
// Dragging the scrubber over an HLS stream shows pictures only if the stream
// says where its I-frames are. Without one of these playlists a player has
// nothing to draw and shows a bare timeline — which is what the Apple TV does
// on the compatible-rendition path, where the archived file is not what is
// playing and AVKit cannot go and read frames out of it itself.
//
// Nothing is encoded for this. The rendition is already cut on a keyframe grid,
// so every segment *begins* with an I-frame; all a player needs is the byte
// range that holds it. That range is read out of the fragment itself: the
// `moof` box, then the first sample's bytes in `mdat`, both of which the
// fragment describes exactly. So the playlist costs one small read per segment
// and no CPU at all.

// HLSIFrameName is the I-frame playlist, rendered on the fly like the master.
const HLSIFrameName = "iframe.m3u8"

// iframeHeadBytes is how much of a segment is read to find its first sample.
// A `moof` for a four-second GOP is a couple of kilobytes; this is generous.
const iframeHeadBytes = 64 << 10

// BuildIFramePlaylist renders the I-frame playlist for a rendition, mirroring
// the media playlist beside it.
//
// It is built *from* `index.m3u8` rather than recomputed: that playlist already
// names every segment and its exact length, including the short last one, and
// two lists of segments that can disagree is a class of bug worth not having.
//
// A segment whose first sample cannot be located is left out rather than
// guessed at — a wrong byte range is a broken picture, a missing one only a
// coarser scrub.
func BuildIFramePlaylist(dir string) ([]byte, error) {
	playlist, err := readPlaylist(filepath.Join(dir, HLSPlaylistName))
	if err != nil {
		return nil, fmt.Errorf("iframe playlist: %w", err)
	}
	segments := playlistSegments(playlist)
	if len(segments) == 0 {
		return nil, fmt.Errorf("iframe playlist: the media playlist names no segments")
	}

	var b strings.Builder
	b.WriteString("#EXTM3U\n")
	b.WriteString("#EXT-X-VERSION:7\n")
	b.WriteString("#EXT-X-INDEPENDENT-SEGMENTS\n")
	b.WriteString("#EXT-X-PLAYLIST-TYPE:VOD\n")
	b.WriteString("#EXT-X-I-FRAMES-ONLY\n")
	fmt.Fprintf(&b, "#EXT-X-TARGETDURATION:%d\n", hlsSegmentSeconds)
	b.WriteString("#EXT-X-MEDIA-SEQUENCE:0\n")
	fmt.Fprintf(&b, "#EXT-X-MAP:URI=%q\n", HLSInitName)

	written := 0
	for _, seg := range segments {
		length, err := iframeRange(filepath.Join(dir, seg.name))
		if err != nil {
			continue
		}
		fmt.Fprintf(&b, "#EXTINF:%s,\n", hlsSecondsParam(seg.seconds))
		fmt.Fprintf(&b, "#EXT-X-BYTERANGE:%d@0\n", length)
		b.WriteString(seg.name + "\n")
		written++
	}
	if written == 0 {
		return nil, fmt.Errorf("iframe playlist: no segment held a locatable I-frame")
	}
	b.WriteString("#EXT-X-ENDLIST\n")
	return []byte(b.String()), nil
}

// playlistSegment is one `#EXTINF` and the segment it introduces.
type playlistSegment struct {
	name    string
	seconds float64
}

// playlistSegments reads the segment list out of a media playlist.
func playlistSegments(playlist []byte) []playlistSegment {
	var out []playlistSegment
	var pending float64
	for _, raw := range strings.Split(string(playlist), "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case strings.HasPrefix(line, "#EXTINF:"):
			value := strings.TrimSuffix(strings.TrimPrefix(line, "#EXTINF:"), ",")
			pending, _ = strconv.ParseFloat(strings.TrimSpace(value), 64)
		case line == "" || strings.HasPrefix(line, "#"):
			continue
		default:
			if pending > 0 {
				out = append(out, playlistSegment{name: line, seconds: pending})
			}
			pending = 0
		}
	}
	return out
}

// iframeRange is the byte range at the start of a segment that holds its first
// sample: everything from byte 0 through the end of that sample.
func iframeRange(path string) (int, error) {
	f, err := os.Open(path) //nolint:gosec // the cache dir plus a generated segment name
	if err != nil {
		return 0, err
	}
	defer f.Close()
	head := make([]byte, iframeHeadBytes)
	n, err := io.ReadFull(f, head)
	if err != nil && n == 0 {
		return 0, fmt.Errorf("read segment: %w", err)
	}
	return firstSampleEnd(head[:n])
}

// firstSampleEnd finds where a fragment's first sample ends, counted from the
// start of the fragment.
//
// The fragment says it twice over: `trun` carries a data offset from the start
// of the enclosing `moof` to the first sample's bytes, and the sample sizes
// follow. Adding the first size to that offset is the end of exactly one
// sample — which, on a keyframe-aligned rendition, is exactly one I-frame.
func firstSampleEnd(fragment []byte) (int, error) {
	moof, ok := findBoxAt(fragment, "moof")
	if !ok {
		return 0, fmt.Errorf("fragment has no moof")
	}
	traf, ok := findBoxAt(moof.payload, "traf")
	if !ok {
		return 0, fmt.Errorf("fragment has no traf")
	}
	trun, ok := findBoxAt(traf.payload, "trun")
	if !ok {
		return 0, fmt.Errorf("fragment has no trun")
	}
	offset, size, err := trunFirstSample(trun.payload, defaultSampleSize(traf.payload))
	if err != nil {
		return 0, err
	}
	// The data offset is measured from the start of the moof, which is where
	// the fragment starts. Nothing here assumes an mdat header length.
	end := moof.offset + offset + size
	if end <= 0 || end > len(fragment) {
		return 0, fmt.Errorf("first sample ends outside the fragment")
	}
	return end, nil
}

// trunFirstSample reads the data offset and the first sample's size out of a
// `trun` payload. `fallbackSize` is the track fragment's default sample size,
// used when the run does not carry per-sample sizes.
func trunFirstSample(payload []byte, fallbackSize uint32) (offset, size int, err error) {
	if len(payload) < 8 {
		return 0, 0, fmt.Errorf("trun too short")
	}
	flags := binary.BigEndian.Uint32(payload[0:4]) & 0x00FFFFFF
	count := binary.BigEndian.Uint32(payload[4:8])
	if count == 0 {
		return 0, 0, fmt.Errorf("trun has no samples")
	}
	pos := 8
	if flags&0x000001 == 0 {
		// Without a data offset the sample data's position is not stated here,
		// and guessing it is how a scrubber ends up showing garbage.
		return 0, 0, fmt.Errorf("trun has no data offset")
	}
	if len(payload) < pos+4 {
		return 0, 0, fmt.Errorf("trun truncated at its data offset")
	}
	dataOffset := int(int32(binary.BigEndian.Uint32(payload[pos : pos+4]))) //nolint:gosec // a signed offset by definition
	pos += 4
	if flags&0x000004 != 0 {
		pos += 4 // first-sample flags, which say the sample is a sync sample
	}
	// The first sample's size, if the run carries sizes at all.
	if flags&0x000200 != 0 {
		if flags&0x000100 != 0 {
			pos += 4 // this sample's duration comes first
		}
		if len(payload) < pos+4 {
			return 0, 0, fmt.Errorf("trun truncated at its first sample")
		}
		return dataOffset, int(binary.BigEndian.Uint32(payload[pos : pos+4])), nil
	}
	if fallbackSize == 0 {
		return 0, 0, fmt.Errorf("trun states no sample size")
	}
	return dataOffset, int(fallbackSize), nil
}

// defaultSampleSize reads `tfhd`'s default sample size, which a run without
// per-sample sizes falls back to. 0 means it was not stated.
func defaultSampleSize(traf []byte) uint32 {
	tfhd := findBox(traf, "tfhd")
	if len(tfhd) < 8 {
		return 0
	}
	flags := binary.BigEndian.Uint32(tfhd[0:4]) & 0x00FFFFFF
	pos := 8 // version+flags, track_ID
	if flags&0x000001 != 0 {
		pos += 8 // base data offset
	}
	if flags&0x000002 != 0 {
		pos += 4 // sample description index
	}
	if flags&0x000008 != 0 {
		pos += 4 // default sample duration
	}
	if flags&0x000010 == 0 || len(tfhd) < pos+4 {
		return 0
	}
	return binary.BigEndian.Uint32(tfhd[pos : pos+4])
}

// findBoxAt is findBox with the box's position, which a byte range needs.
func findBoxAt(buf []byte, typ string) (mp4box, bool) {
	for _, box := range iterBoxes(buf) {
		if box.typ == typ {
			return box, true
		}
	}
	return mp4box{}, false
}

// BuildHLSIFrameStreamInf is the line the master carries to point at the
// playlist above. Bandwidth is a fraction of the video's: an I-frame stream is
// a handful of frames per second, and a player uses this only to decide it can
// afford to scrub.
func BuildHLSIFrameStreamInf(codecs string, bandwidth, width, height int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "#EXT-X-I-FRAME-STREAM-INF:BANDWIDTH=%d,CODECS=%q", max(bandwidth/10, 50_000), videoCodecOnly(codecs))
	if width > 0 && height > 0 {
		fmt.Fprintf(&b, ",RESOLUTION=%dx%d", width, height)
	}
	fmt.Fprintf(&b, ",URI=%q\n", HLSIFrameName)
	return b.String()
}

// videoCodecOnly drops the audio codec: an I-frame playlist carries pictures
// and nothing else, and claiming audio it does not have is a lie a player may
// act on.
func videoCodecOnly(codecs string) string {
	if i := strings.IndexByte(codecs, ','); i >= 0 {
		return codecs[:i]
	}
	return codecs
}
