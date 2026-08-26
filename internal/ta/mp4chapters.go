package ta

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// Chapter is a chapter marker read out of a media file: a start time in
// seconds and a title. The end time is derived by the caller, which knows the
// video duration.
type Chapter struct {
	Start float64
	Title string
}

const (
	// maxBoxDepth caps how deep the box walker descends into containers.
	maxBoxDepth = 8
	// maxBoxesPerLevel bounds how many sibling boxes are parsed at one level,
	// so garbage input cannot make the walker allocate without end.
	maxBoxesPerLevel = 1024
	// maxChapters caps how many chapters any parser returns.
	maxChapters = 2000
	// hundredNano is the unit Nero chpl start times are written in.
	hundredNano = 1e7
)

var (
	// ErrNoChapters means the file parsed fine but carries no chapters.
	ErrNoChapters = errors.New("no chapters in file")

	errBadBox       = errors.New("malformed mp4 box")
	errNoMoov       = errors.New("no moov box in head")
	errOutOfRange   = errors.New("mp4 samples outside the fetched head")
	errBadTimescale = errors.New("mp4 timescale is zero")
)

// ShortHeadError says the moov box extends past the buffer that was handed to
// the parser. Need is how many bytes from the start of the file are required.
type ShortHeadError struct{ Need int64 }

func (e *ShortHeadError) Error() string {
	return fmt.Sprintf("mp4 head too short: need %d bytes", e.Need)
}

// ChaptersFromMP4Head parses chapter markers out of the head of an mp4 file.
// head must start at file offset 0; these files are written faststart, so
// moov sits right behind ftyp and the sample offsets stored in the file are
// usable as indexes into head.
//
// The Nero chpl box is preferred; a QuickTime chapter text track referenced
// by tref/chap is the fallback. Returns ErrNoChapters when the file carries
// none, and *ShortHeadError when moov did not fit in head.
//
// Every failure mode is an error: the parser bounds-checks every read and
// never panics on truncated or garbage input.
func ChaptersFromMP4Head(head []byte) ([]Chapter, error) {
	moov, err := findMoov(head)
	if err != nil {
		return nil, err
	}
	if chs, err := chaptersFromChpl(moov); err == nil && len(chs) > 0 {
		return chs, nil
	}
	if chs, err := chaptersFromTextTrack(head, moov); err == nil && len(chs) > 0 {
		return chs, nil
	}
	return nil, ErrNoChapters
}

// ---- box walking ----

// mp4box is one parsed box: its type, its payload (the bytes after the
// header) and the file offset that payload starts at.
type mp4box struct {
	typ     string
	payload []byte
	offset  int64
}

// boxSpan is a box header read at a buffer offset, before the payload is
// sliced out — size may still overrun the buffer.
type boxSpan struct {
	typ  string
	off  int   // offset of the header in the buffer
	hdr  int   // header length: 8, or 16 for a 64-bit largesize
	size int64 // total box size including the header
}

func (b boxSpan) end() int64 { return int64(b.off) + b.size }

// readBoxSpan reads the box header at buf[off:]. size==1 means the real size
// follows as a 64-bit largesize; size==0 means "to the end of the buffer".
// Sizes smaller than their own header are rejected.
func readBoxSpan(buf []byte, off int) (boxSpan, error) {
	if off < 0 || off > len(buf)-8 {
		return boxSpan{}, errBadBox
	}
	size := uint64(binary.BigEndian.Uint32(buf[off : off+4]))
	b := boxSpan{typ: string(buf[off+4 : off+8]), off: off, hdr: 8}
	switch size {
	case 1:
		if off > len(buf)-16 {
			return boxSpan{}, errBadBox
		}
		size = binary.BigEndian.Uint64(buf[off+8 : off+16])
		b.hdr = 16
	case 0:
		// Runs to the end of the buffer. Non-negative: off <= len(buf)-8.
		size = uint64(len(buf) - off) //nolint:gosec // bounded above
	}
	if size > math.MaxInt64 {
		return boxSpan{}, errBadBox
	}
	b.size = int64(size)
	if b.size < int64(b.hdr) {
		return boxSpan{}, errBadBox
	}
	return b, nil
}

// boxesIn parses the child boxes packed into buf, whose first byte sits at
// file offset base. Parsing stops at the first malformed or overrunning box;
// whatever was read before it is returned, so a truncated container still
// yields its complete children.
func boxesIn(buf []byte, base int64) []mp4box {
	var out []mp4box
	for off := 0; off <= len(buf)-8; {
		b, err := readBoxSpan(buf, off)
		if err != nil || b.end() > int64(len(buf)) {
			break
		}
		// Safe: end() was just bounded by len(buf).
		end := int(b.end())
		out = append(out, mp4box{
			typ:     b.typ,
			payload: buf[off+b.hdr : end],
			offset:  base + int64(off+b.hdr),
		})
		if end == off || len(out) >= maxBoxesPerLevel {
			break
		}
		off = end
	}
	return out
}

// containerBoxes are the box types whose payload is a list of further boxes.
// Restricting the walk to known containers keeps it from parsing leaf data as
// boxes.
var containerBoxes = map[string]bool{
	"moov": true, "trak": true, "mdia": true, "minf": true, "stbl": true,
	"udta": true, "edts": true, "dinf": true, "tref": true,
}

func findBox(boxes []mp4box, typ string) (mp4box, bool) {
	for _, b := range boxes {
		if b.typ == typ {
			return b, true
		}
	}
	return mp4box{}, false
}

func boxesOfType(boxes []mp4box, typ string) []mp4box {
	var out []mp4box
	for _, b := range boxes {
		if b.typ == typ {
			out = append(out, b)
		}
	}
	return out
}

// findBoxDeep looks for a box type breadth-first through known containers,
// descending at most depth levels.
func findBoxDeep(boxes []mp4box, typ string, depth int) (mp4box, bool) {
	if depth <= 0 {
		return mp4box{}, false
	}
	if b, ok := findBox(boxes, typ); ok {
		return b, true
	}
	for _, b := range boxes {
		if !containerBoxes[b.typ] {
			continue
		}
		if got, ok := findBoxDeep(boxesIn(b.payload, b.offset), typ, depth-1); ok {
			return got, true
		}
	}
	return mp4box{}, false
}

// findMoov locates the moov box at the top level of head.
func findMoov(head []byte) (mp4box, error) {
	for off := 0; off <= len(head)-8; {
		b, err := readBoxSpan(head, off)
		if err != nil {
			return mp4box{}, err
		}
		if b.end() > int64(len(head)) {
			if b.typ == "moov" {
				return mp4box{}, &ShortHeadError{Need: b.end()}
			}
			// A box we don't care about (usually mdat) runs past the head, so
			// moov is not in front — nothing more to find here.
			return mp4box{}, errNoMoov
		}
		// Safe: end() was just bounded by len(head).
		end := int(b.end())
		if b.typ == "moov" {
			return mp4box{typ: b.typ, payload: head[off+b.hdr : end], offset: int64(off + b.hdr)}, nil
		}
		if end == off {
			break
		}
		off = end
	}
	return mp4box{}, errNoMoov
}

// ---- Nero chpl ----

// chaptersFromChpl reads the Nero chapter list, normally at moov/udta/chpl.
func chaptersFromChpl(moov mp4box) ([]Chapter, error) {
	box, ok := findBoxDeep(boxesIn(moov.payload, moov.offset), "chpl", maxBoxDepth)
	if !ok {
		return nil, ErrNoChapters
	}
	return parseChpl(box.payload)
}

// parseChpl decodes a chpl payload (the bytes after the box header).
//
// The layout is 1 byte version, 3 bytes flags, then the chapter count and the
// entries. ffmpeg's mov_write_chpl_tag writes four reserved zero bytes
// between the flags and the count; some other muxers omit them. Both layouts
// are tried and scored: a layout whose entries fit the box with valid UTF-8
// titles is a candidate, one whose start times also come out non-decreasing
// beats one that does not, and among equals the one leaving the fewest
// trailing bytes wins. On a full tie the ffmpeg layout is preferred, because
// that is what wrote these files. A file whose chapters really are out of
// order still parses — normalizeChapters sorts them.
//
// Each entry is an 8-byte big-endian start time in 100-nanosecond units, a
// 1-byte title length and that many UTF-8 bytes.
func parseChpl(payload []byte) ([]Chapter, error) {
	var best chplCandidate
	for _, reserved := range []int{4, 0} {
		off := 4 + reserved
		if off >= len(payload) {
			continue
		}
		count := int(payload[off])
		cand, ok := readChplEntries(payload, off+1, count)
		if !ok {
			continue
		}
		if !best.ok || cand.better(best) {
			best = cand
		}
	}
	if !best.ok {
		return nil, ErrNoChapters
	}
	out := normalizeChapters(best.chapters)
	if len(out) == 0 {
		return nil, ErrNoChapters
	}
	return out, nil
}

// chplCandidate is one decoded chpl layout and how well it fits the box.
type chplCandidate struct {
	chapters  []Chapter
	left      int // bytes left over after the last entry
	monotonic bool
	ok        bool
}

func (c chplCandidate) better(other chplCandidate) bool {
	if c.monotonic != other.monotonic {
		return c.monotonic
	}
	return c.left < other.left
}

// readChplEntries reads count entries starting at off, reporting false when
// they do not fit the box or carry invalid UTF-8 titles.
func readChplEntries(payload []byte, off, count int) (chplCandidate, bool) {
	if count <= 0 || count > maxChapters {
		return chplCandidate{}, false
	}
	chs := make([]Chapter, 0, count)
	prev := int64(-1)
	monotonic := true
	for range count {
		if off > len(payload)-9 {
			return chplCandidate{}, false
		}
		// Deliberate reinterpretation: a start time past MaxInt64 comes out
		// negative and is scored as non-monotonic below.
		start := int64(binary.BigEndian.Uint64(payload[off : off+8])) //nolint:gosec // checked below
		off += 8
		n := int(payload[off])
		off++
		if off > len(payload)-n {
			return chplCandidate{}, false
		}
		raw := payload[off : off+n]
		if !utf8.Valid(raw) {
			return chplCandidate{}, false
		}
		off += n
		if start < prev || start < 0 {
			monotonic = false
		}
		prev = start
		chs = append(chs, Chapter{Start: float64(start) / hundredNano, Title: string(raw)})
	}
	return chplCandidate{chapters: chs, left: len(payload) - off, monotonic: monotonic, ok: true}, true
}

// ---- QuickTime chapter text track ----

// chaptersFromTextTrack follows tref/chap to the chapter track and reads its
// text samples. The samples live in mdat, which may sit beyond the fetched
// head — then this returns errOutOfRange and the caller falls back.
func chaptersFromTextTrack(head []byte, moov mp4box) ([]Chapter, error) {
	traks := boxesOfType(boxesIn(moov.payload, moov.offset), "trak")
	var chapIDs []uint32
	for _, tr := range traks {
		tref, ok := findBox(boxesIn(tr.payload, tr.offset), "tref")
		if !ok {
			continue
		}
		chap, ok := findBox(boxesIn(tref.payload, tref.offset), "chap")
		if !ok {
			continue
		}
		for off := 0; off <= len(chap.payload)-4; off += 4 {
			chapIDs = append(chapIDs, binary.BigEndian.Uint32(chap.payload[off:off+4]))
		}
	}
	if len(chapIDs) == 0 {
		return nil, ErrNoChapters
	}
	lastErr := ErrNoChapters
	for _, tr := range traks {
		id, ok := trakID(tr)
		if !ok || !containsUint32(chapIDs, id) {
			continue
		}
		chs, err := chaptersFromTrak(head, tr)
		if err != nil {
			lastErr = err
			continue
		}
		if len(chs) > 0 {
			return chs, nil
		}
	}
	return nil, lastErr
}

func containsUint32(ids []uint32, id uint32) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

// trakID reads the track id out of tkhd (version 0 uses 32-bit times, version
// 1 uses 64-bit ones).
func trakID(trak mp4box) (uint32, bool) {
	tkhd, ok := findBox(boxesIn(trak.payload, trak.offset), "tkhd")
	if !ok || len(tkhd.payload) < 4 {
		return 0, false
	}
	off := 4
	if tkhd.payload[0] == 1 {
		off += 16
	} else {
		off += 8
	}
	if off > len(tkhd.payload)-4 {
		return 0, false
	}
	return binary.BigEndian.Uint32(tkhd.payload[off : off+4]), true
}

// mdhdTimescale reads the media timescale (units per second).
func mdhdTimescale(payload []byte) (uint32, error) {
	if len(payload) < 4 {
		return 0, errBadBox
	}
	off := 4
	if payload[0] == 1 {
		off += 16
	} else {
		off += 8
	}
	if off > len(payload)-4 {
		return 0, errBadBox
	}
	ts := binary.BigEndian.Uint32(payload[off : off+4])
	if ts == 0 {
		return 0, errBadTimescale
	}
	return ts, nil
}

func chaptersFromTrak(head []byte, trak mp4box) ([]Chapter, error) {
	mdia, ok := findBox(boxesIn(trak.payload, trak.offset), "mdia")
	if !ok {
		return nil, errBadBox
	}
	mdiaKids := boxesIn(mdia.payload, mdia.offset)
	mdhd, ok := findBox(mdiaKids, "mdhd")
	if !ok {
		return nil, errBadBox
	}
	timescale, err := mdhdTimescale(mdhd.payload)
	if err != nil {
		return nil, err
	}
	minf, ok := findBox(mdiaKids, "minf")
	if !ok {
		return nil, errBadBox
	}
	stbl, ok := findBox(boxesIn(minf.payload, minf.offset), "stbl")
	if !ok {
		return nil, errBadBox
	}
	kids := boxesIn(stbl.payload, stbl.offset)
	starts, err := sampleStarts(kids, timescale)
	if err != nil {
		return nil, err
	}
	offsets, sizes, err := sampleLocations(kids)
	if err != nil {
		return nil, err
	}
	n := min(len(starts), len(offsets))
	chs := make([]Chapter, 0, n)
	for i := range n {
		title, err := readTextSample(head, offsets[i], sizes[i])
		if err != nil {
			return nil, err
		}
		chs = append(chs, Chapter{Start: starts[i], Title: title})
	}
	out := normalizeChapters(chs)
	if len(out) == 0 {
		return nil, ErrNoChapters
	}
	return out, nil
}

// sampleStarts turns the stts duration table into cumulative start times in
// seconds.
func sampleStarts(stbl []mp4box, timescale uint32) ([]float64, error) {
	stts, ok := findBox(stbl, "stts")
	if !ok || len(stts.payload) < 8 {
		return nil, errBadBox
	}
	body := stts.payload[4:]
	entries := int(binary.BigEndian.Uint32(body[0:4]))
	body = body[4:]
	if entries < 0 || entries > len(body)/8 {
		return nil, errBadBox
	}
	var out []float64
	var cum uint64
	for i := range entries {
		e := body[i*8 : i*8+8]
		n := binary.BigEndian.Uint32(e[0:4])
		delta := binary.BigEndian.Uint32(e[4:8])
		for range n {
			if len(out) >= maxChapters {
				return out, nil
			}
			out = append(out, float64(cum)/float64(timescale))
			cum += uint64(delta)
		}
	}
	return out, nil
}

type stscEntry struct{ firstChunk, samplesPerChunk uint32 }

// sampleLocations resolves the file offset and size of every sample from
// stsz + stsc + stco/co64.
func sampleLocations(stbl []mp4box) ([]uint64, []uint32, error) {
	sizes, err := sampleSizes(stbl)
	if err != nil {
		return nil, nil, err
	}
	chunks, err := chunkOffsets(stbl)
	if err != nil {
		return nil, nil, err
	}
	runs, err := sampleToChunk(stbl)
	if err != nil {
		return nil, nil, err
	}
	offsets := make([]uint64, 0, len(sizes))
	idx := 0
	for i, run := range runs {
		if run.firstChunk < 1 || int(run.firstChunk) > len(chunks) {
			return nil, nil, errBadBox
		}
		last := len(chunks)
		if i+1 < len(runs) {
			next := int(runs[i+1].firstChunk) - 1
			if next < int(run.firstChunk) || next > last {
				return nil, nil, errBadBox
			}
			last = next
		}
		for c := int(run.firstChunk); c <= last; c++ {
			off := chunks[c-1]
			for range run.samplesPerChunk {
				if idx >= len(sizes) {
					return offsets, sizes, nil
				}
				offsets = append(offsets, off)
				off += uint64(sizes[idx])
				idx++
			}
		}
	}
	return offsets, sizes, nil
}

func sampleSizes(stbl []mp4box) ([]uint32, error) {
	stsz, ok := findBox(stbl, "stsz")
	if !ok || len(stsz.payload) < 12 {
		return nil, errBadBox
	}
	uniform := binary.BigEndian.Uint32(stsz.payload[4:8])
	count := int(binary.BigEndian.Uint32(stsz.payload[8:12]))
	if count < 0 || count > maxChapters {
		return nil, errBadBox
	}
	out := make([]uint32, 0, count)
	if uniform != 0 {
		for range count {
			out = append(out, uniform)
		}
		return out, nil
	}
	body := stsz.payload[12:]
	if count > len(body)/4 {
		return nil, errBadBox
	}
	for i := range count {
		out = append(out, binary.BigEndian.Uint32(body[i*4:i*4+4]))
	}
	return out, nil
}

func chunkOffsets(stbl []mp4box) ([]uint64, error) {
	if b, ok := findBox(stbl, "stco"); ok {
		return fixedEntries(b.payload, 4, func(e []byte) uint64 {
			return uint64(binary.BigEndian.Uint32(e))
		})
	}
	if b, ok := findBox(stbl, "co64"); ok {
		return fixedEntries(b.payload, 8, binary.BigEndian.Uint64)
	}
	return nil, errBadBox
}

// fixedEntries reads a full box holding entry_count followed by fixed-width
// entries.
func fixedEntries(payload []byte, width int, read func([]byte) uint64) ([]uint64, error) {
	if len(payload) < 8 {
		return nil, errBadBox
	}
	body := payload[4:]
	count := int(binary.BigEndian.Uint32(body[0:4]))
	body = body[4:]
	if count < 0 || count > len(body)/width {
		return nil, errBadBox
	}
	out := make([]uint64, 0, count)
	for i := range count {
		out = append(out, read(body[i*width:i*width+width]))
	}
	return out, nil
}

func sampleToChunk(stbl []mp4box) ([]stscEntry, error) {
	stsc, ok := findBox(stbl, "stsc")
	if !ok || len(stsc.payload) < 8 {
		return nil, errBadBox
	}
	body := stsc.payload[4:]
	count := int(binary.BigEndian.Uint32(body[0:4]))
	body = body[4:]
	if count < 1 || count > len(body)/12 {
		return nil, errBadBox
	}
	out := make([]stscEntry, 0, count)
	for i := range count {
		e := body[i*12 : i*12+12]
		out = append(out, stscEntry{
			firstChunk:      binary.BigEndian.Uint32(e[0:4]),
			samplesPerChunk: binary.BigEndian.Uint32(e[4:8]),
		})
	}
	return out, nil
}

// readTextSample decodes one QuickTime text sample: a uint16 length followed
// by the string (trailing atoms, if any, are ignored). A UTF-16 byte order
// mark is honoured.
func readTextSample(head []byte, off uint64, size uint32) (string, error) {
	end := off + uint64(size)
	if size < 2 || off > uint64(len(head)) || end > uint64(len(head)) {
		return "", errOutOfRange
	}
	b := head[off:end]
	n := int(binary.BigEndian.Uint16(b[0:2]))
	if n > len(b)-2 {
		return "", errBadBox
	}
	raw := b[2 : 2+n]
	if s, ok := decodeUTF16BOM(raw); ok {
		return s, nil
	}
	if !utf8.Valid(raw) {
		return "", errBadBox
	}
	return string(raw), nil
}

// decodeUTF16BOM decodes text that starts with a UTF-16 byte order mark.
func decodeUTF16BOM(raw []byte) (string, bool) {
	if len(raw) < 2 || len(raw)%2 != 0 {
		return "", false
	}
	var order binary.ByteOrder
	switch {
	case raw[0] == 0xFE && raw[1] == 0xFF:
		order = binary.BigEndian
	case raw[0] == 0xFF && raw[1] == 0xFE:
		order = binary.LittleEndian
	default:
		return "", false
	}
	units := make([]uint16, 0, len(raw)/2-1)
	for i := 2; i < len(raw); i += 2 {
		units = append(units, order.Uint16(raw[i:i+2]))
	}
	return string(utf16.Decode(units)), true
}

// ---- shared normalization ----

// normalizeChapters trims titles, drops empty and nonsensical entries, sorts
// by start time and removes non-increasing duplicates.
func normalizeChapters(in []Chapter) []Chapter {
	out := make([]Chapter, 0, len(in))
	for _, c := range in {
		c.Title = strings.TrimSpace(c.Title)
		if c.Title == "" || c.Start < 0 || math.IsNaN(c.Start) || math.IsInf(c.Start, 0) {
			continue
		}
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Start < out[j].Start })
	dedup := out[:0]
	for _, c := range out {
		if len(dedup) > 0 && c.Start <= dedup[len(dedup)-1].Start {
			continue
		}
		dedup = append(dedup, c)
	}
	if len(dedup) > maxChapters {
		dedup = dedup[:maxChapters]
	}
	return dedup
}
