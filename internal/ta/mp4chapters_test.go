package ta

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
)

// ---- synthetic mp4 builders ----
//
// The builders below work with small, known-good sizes, so the narrowing
// conversions they need are safe by construction.

func u32(n int) uint32 { return uint32(n) } //nolint:gosec // test fixture sizes are small
func u16(n int) uint16 { return uint16(n) } //nolint:gosec // test fixture sizes are small
func u8(n int) byte    { return byte(n) }   //nolint:gosec // test fixture sizes are small

// mbox emits a box with a 32-bit size header.
func mbox(typ string, parts ...[]byte) []byte {
	body := bytes.Join(parts, nil)
	out := make([]byte, 0, 8+len(body))
	out = binary.BigEndian.AppendUint32(out, u32(8+len(body)))
	out = append(out, typ...)
	return append(out, body...)
}

// mbox64 emits the same box with size==1 and a 64-bit largesize.
func mbox64(typ string, parts ...[]byte) []byte {
	body := bytes.Join(parts, nil)
	out := make([]byte, 0, 16+len(body))
	out = binary.BigEndian.AppendUint32(out, 1)
	out = append(out, typ...)
	out = binary.BigEndian.AppendUint64(out, uint64(16+len(body))) //nolint:gosec // test fixture sizes are small
	return append(out, body...)
}

// mboxToEnd emits a box with size==0, meaning "runs to the end of the file".
func mboxToEnd(typ string, parts ...[]byte) []byte {
	body := bytes.Join(parts, nil)
	out := make([]byte, 0, 8+len(body))
	out = binary.BigEndian.AppendUint32(out, 0)
	out = append(out, typ...)
	return append(out, body...)
}

func ftyp() []byte { return mbox("ftyp", []byte("isom\x00\x00\x02\x00isomiso2mp41")) }

type chplEntry struct {
	start float64 // seconds
	title string
	// titleLen overrides the encoded length byte (to build an overrun).
	titleLen int
}

// chplBox builds a Nero chapter list. reserved mirrors ffmpeg's four zero
// bytes between the flags and the count.
func chplBox(reserved bool, entries ...chplEntry) []byte {
	body := []byte{1, 0, 0, 0} // version + flags
	if reserved {
		body = append(body, 0, 0, 0, 0)
	}
	body = append(body, u8(len(entries)))
	for _, e := range entries {
		body = binary.BigEndian.AppendUint64(body, uint64(int64(e.start*hundredNano))) //nolint:gosec // test fixture sizes are small
		n := len(e.title)
		if e.titleLen != 0 {
			n = e.titleLen
		}
		body = append(body, u8(n))
		body = append(body, e.title...)
	}
	return mbox("chpl", body)
}

func chplFile(reserved bool, entries ...chplEntry) []byte {
	return bytes.Join([][]byte{
		ftyp(),
		mbox("moov", mbox("mvhd", make([]byte, 100)), mbox("udta", chplBox(reserved, entries...))),
		mbox("mdat", make([]byte, 64)),
	}, nil)
}

func titles(chs []Chapter) []string {
	out := make([]string, 0, len(chs))
	for _, c := range chs {
		out = append(out, c.Title)
	}
	return out
}

func starts(chs []Chapter) []float64 {
	out := make([]float64, 0, len(chs))
	for _, c := range chs {
		out = append(out, c.Start)
	}
	return out
}

// ---- chpl ----

func TestChaptersFromChpl(t *testing.T) {
	good := []chplEntry{{0, "Intro", 0}, {12.5, "Body", 0}, {130, "Outro", 0}}
	tests := []struct {
		name       string
		head       []byte
		wantTitles []string
		wantStarts []float64
	}{
		{
			name:       "ffmpeg layout with reserved bytes",
			head:       chplFile(true, good...),
			wantTitles: []string{"Intro", "Body", "Outro"},
			wantStarts: []float64{0, 12.5, 130},
		},
		{
			name:       "layout without reserved bytes",
			head:       chplFile(false, good...),
			wantTitles: []string{"Intro", "Body", "Outro"},
			wantStarts: []float64{0, 12.5, 130},
		},
		{
			name:       "out of order chapters are sorted",
			head:       chplFile(true, chplEntry{30, "Third", 0}, chplEntry{0, "First", 0}, chplEntry{10, "Second", 0}),
			wantTitles: []string{"First", "Second", "Third"},
			wantStarts: []float64{0, 10, 30},
		},
		{
			name:       "empty and whitespace titles are dropped",
			head:       chplFile(true, chplEntry{0, "Intro", 0}, chplEntry{5, "   ", 0}, chplEntry{9, "", 0}, chplEntry{20, " Outro ", 0}),
			wantTitles: []string{"Intro", "Outro"},
			wantStarts: []float64{0, 20},
		},
		{
			name:       "duplicate start times collapse",
			head:       chplFile(true, chplEntry{0, "A", 0}, chplEntry{0, "B", 0}, chplEntry{7, "C", 0}),
			wantTitles: []string{"A", "C"},
			wantStarts: []float64{0, 7},
		},
		{
			name:       "64-bit largesize boxes",
			head:       bytes.Join([][]byte{ftyp(), mbox64("moov", mbox64("udta", chplBox(true, good...)))}, nil),
			wantTitles: []string{"Intro", "Body", "Outro"},
			wantStarts: []float64{0, 12.5, 130},
		},
		{
			name:       "trailing box with size 0",
			head:       bytes.Join([][]byte{ftyp(), mboxToEnd("moov", mbox("udta", chplBox(true, good...)))}, nil),
			wantTitles: []string{"Intro", "Body", "Outro"},
			wantStarts: []float64{0, 12.5, 130},
		},
		{
			name:       "utf-8 titles survive",
			head:       chplFile(true, chplEntry{0, "Einführung", 0}, chplEntry{4, "日本語", 0}),
			wantTitles: []string{"Einführung", "日本語"},
			wantStarts: []float64{0, 4},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ChaptersFromMP4Head(tc.head)
			if err != nil {
				t.Fatalf("ChaptersFromMP4Head: %v", err)
			}
			if !reflect.DeepEqual(titles(got), tc.wantTitles) {
				t.Errorf("titles = %q, want %q", titles(got), tc.wantTitles)
			}
			if !reflect.DeepEqual(starts(got), tc.wantStarts) {
				t.Errorf("starts = %v, want %v", starts(got), tc.wantStarts)
			}
		})
	}
}

func TestChaptersFromMP4HeadRejects(t *testing.T) {
	// A moov whose declared size runs past the buffer.
	full := chplFile(true, chplEntry{0, "Intro", 0}, chplEntry{5, "Body", 0})
	truncated := full[:len(ftyp())+20]

	tests := []struct {
		name string
		head []byte
	}{
		{"empty", nil},
		{"one byte", []byte{7}},
		{"all zeroes", make([]byte, 64)},
		{"all ones", bytes.Repeat([]byte{0xFF}, 64)},
		{"box size below header", append([]byte{0, 0, 0, 4}, "moov"...)},
		{"largesize below its header", bytes.Join([][]byte{{0, 0, 0, 1}, []byte("moov"), {0, 0, 0, 0, 0, 0, 0, 9}}, nil)},
		{"largesize header cut short", append([]byte{0, 0, 0, 1}, "moov"...)},
		{"no moov", bytes.Join([][]byte{ftyp(), mbox("mdat", make([]byte, 32))}, nil)},
		{"moov without chapters", bytes.Join([][]byte{ftyp(), mbox("moov", mbox("mvhd", make([]byte, 100)))}, nil)},
		{"title length overruns the box", chplFile(true, chplEntry{0, "Intro", 200})},
		{"count larger than the entries present", bytes.Join([][]byte{
			ftyp(),
			mbox("moov", mbox("udta", mbox("chpl", []byte{1, 0, 0, 0, 0, 0, 0, 0, 9, 0, 0, 0, 0}))),
		}, nil)},
		{"chpl too short for a header", bytes.Join([][]byte{
			ftyp(), mbox("moov", mbox("udta", mbox("chpl", []byte{1, 0}))),
		}, nil)},
		{"invalid utf-8 title", bytes.Join([][]byte{
			ftyp(),
			mbox("moov", mbox("udta", mbox("chpl", append([]byte{1, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 2}, 0xFF, 0xFE)))),
		}, nil)},
		{"child box declaring more than its container holds", bytes.Join([][]byte{
			ftyp(),
			mbox("moov", mbox("udta", []byte{0, 0, 1, 0}, []byte("chpl"), []byte{1, 0, 0, 0})),
		}, nil)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ChaptersFromMP4Head(tc.head)
			if err == nil {
				t.Fatalf("expected an error, got %d chapters", len(got))
			}
			var short *ShortHeadError
			if errors.As(err, &short) {
				t.Fatalf("unexpected ShortHeadError: %v", err)
			}
		})
	}

	t.Run("truncated moov reports how much is needed", func(t *testing.T) {
		_, err := ChaptersFromMP4Head(truncated)
		var short *ShortHeadError
		if !errors.As(err, &short) {
			t.Fatalf("err = %v, want *ShortHeadError", err)
		}
		wantNeed := int64(len(full) - len(mbox("mdat", make([]byte, 64))))
		if short.Need != wantNeed {
			t.Errorf("Need = %d, want %d", short.Need, wantNeed)
		}
		// The full buffer parses.
		if chs, err := ChaptersFromMP4Head(full); err != nil || len(chs) != 2 {
			t.Errorf("full buffer: %v %d chapters", err, len(chs))
		}
	})
}

// TestChaptersFromMP4HeadNeverPanics feeds mutated and random buffers through
// the parser: it may fail, it may not crash.
func TestChaptersFromMP4HeadNeverPanics(t *testing.T) {
	seed := chplFile(true, chplEntry{0, "Intro", 0}, chplEntry{12.5, "Body", 0}, chplEntry{130, "Outro", 0})
	seed = append(seed, buildChapterTrackFile([]string{"One", "Two"}, []uint32{1000, 2000})...)
	rng := rand.New(rand.NewPCG(1, 2)) //nolint:gosec // deterministic fuzzing, not cryptography

	for i := range 3000 {
		buf := append([]byte{}, seed...)
		switch i % 4 {
		case 0: // truncate
			buf = buf[:rng.IntN(len(buf))]
		case 1: // flip bytes
			for range 1 + rng.IntN(8) {
				buf[rng.IntN(len(buf))] = u8(rng.IntN(256))
			}
		case 2: // random noise
			buf = make([]byte, rng.IntN(512))
			for j := range buf {
				buf[j] = u8(rng.IntN(256))
			}
		case 3: // truncate then flip
			buf = buf[:rng.IntN(len(buf))]
			if len(buf) > 0 {
				buf[rng.IntN(len(buf))] = u8(rng.IntN(256))
			}
		}
		func() {
			defer func() {
				if p := recover(); p != nil {
					t.Fatalf("panic on iteration %d (%d bytes): %v", i, len(buf), p)
				}
			}()
			chs, err := ChaptersFromMP4Head(buf)
			if err == nil {
				for _, c := range chs {
					if c.Start < 0 || strings.TrimSpace(c.Title) == "" {
						t.Fatalf("iteration %d yielded a bogus chapter %+v", i, c)
					}
				}
			}
		}()
	}
}

// ---- QuickTime chapter text track ----

// buildChapterTrackFile writes a faststart file with a video track that
// references a chapter text track through tref/chap. deltas are the sample
// durations in the chapter track's timescale (1000).
func buildChapterTrackFile(chapterTitles []string, deltas []uint32) []byte {
	const timescale = 1000

	var samples []byte
	sizes := make([]uint32, 0, len(chapterTitles))
	for _, title := range chapterTitles {
		s := binary.BigEndian.AppendUint16(nil, u16(len(title)))
		s = append(s, title...)
		samples = append(samples, s...)
		sizes = append(sizes, u32(len(s)))
	}

	tkhd := func(id uint32) []byte {
		body := []byte{0, 0, 0, 0}                     // version 0 + flags
		body = append(body, make([]byte, 8)...)        // creation + modification
		body = binary.BigEndian.AppendUint32(body, id) // track id
		body = append(body, make([]byte, 60)...)       // the rest
		return mbox("tkhd", body)
	}
	mdhd := func() []byte {
		body := []byte{0, 0, 0, 0}
		body = append(body, make([]byte, 8)...)
		body = binary.BigEndian.AppendUint32(body, timescale)
		body = binary.BigEndian.AppendUint32(body, 10000) // duration
		body = append(body, 0x55, 0xC4, 0, 0)
		return mbox("mdhd", body)
	}
	stts := func() []byte {
		body := []byte{0, 0, 0, 0}
		body = binary.BigEndian.AppendUint32(body, u32(len(deltas)))
		for _, d := range deltas {
			body = binary.BigEndian.AppendUint32(body, 1) // sample_count
			body = binary.BigEndian.AppendUint32(body, d) // sample_delta
		}
		return mbox("stts", body)
	}
	stsz := func() []byte {
		body := []byte{0, 0, 0, 0}
		body = binary.BigEndian.AppendUint32(body, 0) // per-sample sizes follow
		body = binary.BigEndian.AppendUint32(body, u32(len(sizes)))
		for _, s := range sizes {
			body = binary.BigEndian.AppendUint32(body, s)
		}
		return mbox("stsz", body)
	}
	stsc := func() []byte {
		body := []byte{0, 0, 0, 0}
		body = binary.BigEndian.AppendUint32(body, 1)
		body = binary.BigEndian.AppendUint32(body, 1)               // first_chunk
		body = binary.BigEndian.AppendUint32(body, u32(len(sizes))) // samples_per_chunk
		body = binary.BigEndian.AppendUint32(body, 1)               // description index
		return mbox("stsc", body)
	}
	stco := func(offset uint32) []byte {
		body := []byte{0, 0, 0, 0}
		body = binary.BigEndian.AppendUint32(body, 1)
		body = binary.BigEndian.AppendUint32(body, offset)
		return mbox("stco", body)
	}

	moovFor := func(chunkOffset uint32) []byte {
		videoTrak := mbox("trak",
			tkhd(1),
			mbox("tref", mbox("chap", binary.BigEndian.AppendUint32(nil, 2))),
			mbox("mdia", mdhd(), mbox("minf", mbox("stbl", mbox("stsd", make([]byte, 16))))),
		)
		chapTrak := mbox("trak",
			tkhd(2),
			mbox("mdia", mdhd(), mbox("minf", mbox("stbl",
				mbox("stsd", make([]byte, 16)), stts(), stsc(), stsz(), stco(chunkOffset),
			))),
		)
		return mbox("moov", mbox("mvhd", make([]byte, 100)), videoTrak, chapTrak)
	}

	// The moov length does not depend on the offset value, so one dry run is
	// enough to learn where mdat's payload lands.
	head := ftyp()
	chunkOffset := u32(len(head) + len(moovFor(0)) + 8)
	return bytes.Join([][]byte{head, moovFor(chunkOffset), mbox("mdat", samples)}, nil)
}

func TestChaptersFromTextTrack(t *testing.T) {
	file := buildChapterTrackFile([]string{"Intro", "Middle", "End"}, []uint32{2500, 7500, 5000})

	got, err := ChaptersFromMP4Head(file)
	if err != nil {
		t.Fatalf("ChaptersFromMP4Head: %v", err)
	}
	if want := []string{"Intro", "Middle", "End"}; !reflect.DeepEqual(titles(got), want) {
		t.Errorf("titles = %q, want %q", titles(got), want)
	}
	if want := []float64{0, 2.5, 10}; !reflect.DeepEqual(starts(got), want) {
		t.Errorf("starts = %v, want %v", starts(got), want)
	}
}

func TestChplWinsOverTextTrack(t *testing.T) {
	// Same file with a chpl box bolted into moov/udta: chpl is authoritative.
	file := buildChapterTrackFile([]string{"Track A", "Track B"}, []uint32{1000, 1000})
	moovStart := len(ftyp())
	udta := mbox("udta", chplBox(true, chplEntry{0, "Chpl A", 0}, chplEntry{3, "Chpl B", 0}))

	moovSize := int(binary.BigEndian.Uint32(file[moovStart : moovStart+4]))
	moovEnd := moovStart + moovSize
	patched := append([]byte{}, file[:moovEnd]...)
	patched = append(patched, udta...)
	binary.BigEndian.PutUint32(patched[moovStart:moovStart+4], u32(moovSize+len(udta)))
	patched = append(patched, file[moovEnd:]...)

	got, err := ChaptersFromMP4Head(patched)
	if err != nil {
		t.Fatalf("ChaptersFromMP4Head: %v", err)
	}
	if want := []string{"Chpl A", "Chpl B"}; !reflect.DeepEqual(titles(got), want) {
		t.Errorf("titles = %q, want %q", titles(got), want)
	}
}

// TestTextTrackSamplesOutOfRange covers the documented limitation: when mdat
// is not in the fetched head the text track path reports not-found instead of
// reading past the buffer.
func TestTextTrackSamplesOutOfRange(t *testing.T) {
	file := buildChapterTrackFile([]string{"Intro", "Outro"}, []uint32{1000, 1000})
	// Keep moov intact, cut mdat's payload away.
	head := file[:len(file)-12]
	if _, err := ChaptersFromMP4Head(head); err == nil {
		t.Fatal("expected an error when the samples fall outside the head")
	}
}
