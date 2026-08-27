package media

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// Minimal fMP4 fixtures. The rebase only ever touches four fixed-size fields,
// so a segment built from the boxes that carry them exercises it exactly as a
// real one does — and lets the derivation tests run without ffmpeg.

// mp4Box wraps body in a box header.
func mp4Box(typ string, body ...[]byte) []byte {
	var inner []byte
	for _, b := range body {
		inner = append(inner, b...)
	}
	out := make([]byte, 8, 8+len(inner))
	binary.BigEndian.PutUint32(out, uint32(8+len(inner))) //nolint:gosec // G115: test fixtures are a few hundred bytes
	copy(out[4:], typ)
	return append(out, inner...)
}

func be32(v uint32) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, v)
	return b
}

// fakeInitSegment is an init segment naming tracks with the given timescales,
// keyed by track id. Only what mp4Timescales reads is filled in.
func fakeInitSegment(timescales map[uint32]uint32) []byte {
	var traks []byte
	for id, scale := range timescales {
		tkhd := mp4Box("tkhd", []byte{0, 0, 0, 0}, be32(0), be32(0), be32(id), be32(0))
		mdhd := mp4Box("mdhd", []byte{0, 0, 0, 0}, be32(0), be32(0), be32(scale), be32(0))
		traks = append(traks, mp4Box("trak", tkhd, mp4Box("mdia", mdhd))...)
	}
	return append(mp4Box("ftyp", []byte("iso6")), mp4Box("moov", traks)...)
}

// fakeSegment is a media segment whose decode times are all zero, as ffmpeg's
// HLS muxer writes them however far into the video the run started.
func fakeSegment(trackIDs []uint32, sidxTimescale uint32) []byte {
	out := mp4Box("styp", []byte("msdh"))
	out = append(out, mp4Box("sidx",
		[]byte{0, 0, 0, 0}, be32(trackIDs[0]), be32(sidxTimescale),
		be32(0) /* earliest_presentation_time */, be32(0), []byte{0, 0, 0, 0})...)
	var trafs []byte
	for _, id := range trackIDs {
		tfhd := mp4Box("tfhd", []byte{0, 0, 0, 0}, be32(id))
		tfdt := mp4Box("tfdt", []byte{0, 0, 0, 0}, be32(0))
		trafs = append(trafs, mp4Box("traf", tfhd, tfdt)...)
	}
	out = append(out, mp4Box("moof", mp4Box("mfhd", []byte{0, 0, 0, 0}, be32(1)), trafs)...)
	return append(out, mp4Box("mdat", bytes.Repeat([]byte{0x42}, 64))...)
}

// tfdtValues reads back every track fragment decode time, in order.
func tfdtValues(b []byte) []uint32 {
	var out []uint32
	mp4Walk(b, 0, len(b), func(typ string, body, end int) bool {
		if typ != "moof" {
			return true
		}
		mp4Walk(b, body, end, func(t string, tb, te int) bool {
			if t != "traf" {
				return true
			}
			mp4Walk(b, tb, te, func(t2 string, b2, _ int) bool {
				if t2 == "tfdt" {
					out = append(out, binary.BigEndian.Uint32(b[b2+4:]))
				}
				return true
			})
			return true
		})
		return true
	})
	return out
}

func sidxEarliest(b []byte) uint32 {
	var out uint32
	mp4Walk(b, 0, len(b), func(typ string, body, _ int) bool {
		if typ == "sidx" {
			out = binary.BigEndian.Uint32(b[body+12:])
			return false
		}
		return true
	})
	return out
}

func TestMP4Timescales(t *testing.T) {
	got := mp4Timescales(fakeInitSegment(map[uint32]uint32{1: 12288, 2: 44100}))
	if len(got) != 2 || got[1] != 12288 || got[2] != 44100 {
		t.Errorf("mp4Timescales = %v, want video 12288 and audio 44100", got)
	}
	if n := len(mp4Timescales([]byte("not an mp4 at all"))); n != 0 {
		t.Errorf("a non-MP4 named %d tracks", n)
	}
}

// Each track's decode time is in *its own* timescale, so the same twelve
// seconds is a different number per track. Getting that wrong puts the audio
// somewhere else entirely.
func TestRebaseSegmentPerTrackTimescale(t *testing.T) {
	scales := map[uint32]uint32{1: 12288, 2: 44100}
	seg := fakeSegment([]uint32{1, 2}, 12288)

	if err := rebaseSegment(seg, 12, scales); err != nil {
		t.Fatalf("rebase: %v", err)
	}
	want := []uint32{12 * 12288, 12 * 44100}
	got := tfdtValues(seg)
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("tfdt = %v, want %v", got, want)
	}
	if e := sidxEarliest(seg); e != 12*12288 {
		t.Errorf("sidx earliest presentation time = %d, want %d", e, 12*12288)
	}
}

// A run that starts at the beginning has nothing to move, and must not be
// touched.
func TestRebaseSegmentZeroOffsetIsANoOp(t *testing.T) {
	seg := fakeSegment([]uint32{1}, 12288)
	before := append([]byte(nil), seg...)
	if err := rebaseSegment(seg, 0, map[uint32]uint32{1: 12288}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(seg, before) {
		t.Error("a zero offset rewrote the segment")
	}
}

// A segment naming a track the init segment does not is not something to guess
// at: an unshifted track would land twelve seconds from the rest of the stream.
func TestRebaseSegmentRejectsAnUnknownTrack(t *testing.T) {
	seg := fakeSegment([]uint32{7}, 12288)
	if err := rebaseSegment(seg, 12, map[uint32]uint32{1: 12288}); err == nil {
		t.Error("a segment with an unknown track was rebased anyway")
	}
}

// A shift that does not fit the field would silently produce a stream whose
// timestamps wrap; saying so is the only safe answer.
func TestRebaseSegmentRejectsAnOverflowingShift(t *testing.T) {
	seg := fakeSegment([]uint32{1}, 48000)
	// 2^32 / 48000 Hz is about 24 hours.
	if err := rebaseSegment(seg, 100000, map[uint32]uint32{1: 48000}); err == nil {
		t.Error("an overflowing shift was applied")
	}
}

// Publishing is what makes a rebased segment visible: a player must never be
// able to fetch one whose timestamps have not been fixed up.
func TestPublishRawSegments(t *testing.T) {
	dir := t.TempDir()
	scales := map[uint32]uint32{1: 12288}
	if err := os.WriteFile(filepath.Join(dir, HLSInitName), fakeInitSegment(scales), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, i := range []int{3, 4} {
		name := HLSSegmentName(i) + hlsRawSuffix
		if err := os.WriteFile(filepath.Join(dir, name), fakeSegment([]uint32{1}, 12288), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	n, err := publishRawSegments(dir, HLSInitName, 12)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if n != 2 {
		t.Errorf("published %d segments, want 2", n)
	}
	for _, i := range []int{3, 4} {
		if _, err := os.Stat(filepath.Join(dir, HLSSegmentName(i)+hlsRawSuffix)); err == nil {
			t.Errorf("segment %d is still unpublished", i)
		}
		b, err := os.ReadFile(filepath.Join(dir, HLSSegmentName(i))) //nolint:gosec // test fixture path
		if err != nil {
			t.Fatalf("segment %d was not published: %v", i, err)
		}
		if got := tfdtValues(b); len(got) != 1 || got[0] != 12*12288 {
			t.Errorf("published segment %d tfdt = %v, want %d", i, got, 12*12288)
		}
	}
	// Nothing left to do the second time round.
	if n, err := publishRawSegments(dir, HLSInitName, 12); err != nil || n != 0 {
		t.Errorf("second pass published %d (%v), want 0", n, err)
	}
}

// A malformed box must end the walk rather than panic: these files come from a
// subprocess.
func TestMP4WalkSurvivesGarbage(_ *testing.T) {
	for _, b := range [][]byte{
		nil,
		[]byte("abc"),
		{0, 0, 0, 0},
		{0xff, 0xff, 0xff, 0xff, 'm', 'o', 'o', 'v'},
		{0, 0, 0, 1, 'm', 'o', 'o', 'v'},
		{0, 0, 0, 4, 'm', 'o', 'o', 'v'},
	} {
		mp4Walk(b, 0, len(b), func(string, int, int) bool { return true })
		mp4Timescales(b)
		_ = rebaseSegment(append([]byte(nil), b...), 12, map[uint32]uint32{1: 1000})
	}
}
