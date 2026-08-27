package media

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// The multivariant (master) playlist.
//
// hls.js (Chrome/Firefox/Edge, i.e. every browser without native HLS) will not
// schedule fMP4 fragments from a *media* playlist that carries no CODECS
// attribute: it parses the playlist, sees the segment count, and then never
// requests fragment 0 — no MSE SourceBuffer is created because it does not know
// what to create it for. A media playlist has nowhere to put CODECS; a
// multivariant playlist does, in EXT-X-STREAM-INF. So the URL a client loads is
// a one-entry master that names the codecs up front and points at the existing
// media playlist (index.m3u8) with a relative URI. Native AVPlayer and Safari
// play a master playlist just as happily, so this is the URL for every client.
//
// The CODECS string must be truthful: MediaSource creates the SourceBuffer from
// it, and a wrong profile makes the appended fMP4 fail to decode — a different
// way to the same stall. So the strings are parsed out of the init segment the
// job actually produced (see ParseInitCodecs); only when the init is not on
// disk yet does the master fall back to the height-derived default, which is
// correct for the fixed encoder settings the transcode always uses.

const (
	// HLSMasterName is the multivariant playlist a client loads. It lives at
	// the same path as the media playlist and references it by a relative URI,
	// so it adds no new cache file — it is rendered on the fly.
	HLSMasterName = "master.m3u8"
	// hlsCodecsMarker caches the CODECS string parsed from the init segment, so
	// the init is parsed once per rendition rather than on every master
	// request. Never served (validHLSFile does not match it).
	hlsCodecsMarker = ".codecs"
)

// HLSCodecsInfo is what a rendition's streams turn out to be: the RFC 6381
// CODECS string for its EXT-X-STREAM-INF, and the encoded frame size for its
// RESOLUTION. Width/Height are 0 when unknown (the init has not been parsed).
type HLSCodecsInfo struct {
	Codecs string
	Width  int
	Height int
}

// BuildHLSMaster renders the one-entry multivariant playlist. width/height are
// the encoded frame size; either being 0 omits RESOLUTION (a truthful omission
// beats a guessed resolution). The single variant URI is the media playlist,
// relative, so it resolves against the master's own URL.
func BuildHLSMaster(codecs string, bandwidth, width, height int) []byte {
	var b bytes.Buffer
	b.WriteString("#EXTM3U\n")
	b.WriteString("#EXT-X-VERSION:7\n")
	b.WriteString("#EXT-X-INDEPENDENT-SEGMENTS\n")
	fmt.Fprintf(&b, "#EXT-X-STREAM-INF:BANDWIDTH=%d,CODECS=%q", bandwidth, codecs)
	if width > 0 && height > 0 {
		fmt.Fprintf(&b, ",RESOLUTION=%dx%d", width, height)
	}
	b.WriteByte('\n')
	b.WriteString(HLSPlaylistName + "\n")
	return b.Bytes()
}

// HLSBandwidth is the BANDWIDTH a rendition of this height advertises. It is an
// estimate — the encode is constant-quality, so the real bitrate varies with
// the content — and BANDWIDTH is advisory in a single-variant master anyway; a
// plausible per-height number is all a player wants.
func HLSBandwidth(height int) int {
	switch {
	case height >= 2160:
		return 20_000_000
	case height >= 1440:
		return 10_000_000
	case height >= 1080:
		return 5_000_000
	case height >= 720:
		return 2_500_000
	default:
		return 1_200_000
	}
}

// DefaultHLSCodecs is the CODECS string for a height when the init segment has
// not been parsed yet. It matches the fixed encoder settings every rendition
// uses — H.264 High@4.1 (avc1.640829) up to 1080p, HEVC Main above it — plus
// AAC-LC stereo. A copied source can differ, which is why the parsed value is
// preferred; this is only the value served before the first init lands.
func DefaultHLSCodecs(height int) string {
	if HLSCodecForHeight(height) == HLSCodecHEVC {
		return "hvc1.1.6." + defaultHEVCLevel(height) + ".90,mp4a.40.2"
	}
	// High profile (0x64), constraint byte 0x08, level 4.1 (0x29): avc1.640829.
	return "avc1.640829,mp4a.40.2"
}

// defaultHEVCLevel is the HEVC level string a height's default codecs claims: a
// level high enough to cover the frame size. Level does not gate MSE support,
// so erring high is harmless.
func defaultHEVCLevel(height int) string {
	if height > 1440 {
		return "L153" // 5.1, covers 2160p
	}
	return "L123" // 4.1, covers 1440p
}

// EnsureHLSCodecs returns the CODECS string and encoded size for a rendition,
// preferring the truth parsed from its init segment. It reads a cached marker
// first, then parses init.mp4 and caches the result, and only if neither is
// available falls back to the height-derived default (exact=false). exact=false
// with an init already present means the init could not be parsed — the caller
// should not keep waiting for it.
func EnsureHLSCodecs(dir string, renditionHeight int) (info HLSCodecsInfo, exact bool) {
	if info, ok := readCodecsMarker(dir); ok {
		return info, true
	}
	initPath := filepath.Join(dir, HLSInitName)
	if b, err := os.ReadFile(initPath); err == nil && len(b) > 0 { //nolint:gosec // cache dir + a fixed name
		if info, err := ParseInitCodecs(b); err == nil && info.Codecs != "" {
			writeCodecsMarker(dir, info)
			return info, true
		}
	}
	return HLSCodecsInfo{Codecs: DefaultHLSCodecs(renditionHeight)}, false
}

func readCodecsMarker(dir string) (HLSCodecsInfo, bool) {
	b, err := os.ReadFile(filepath.Join(dir, hlsCodecsMarker)) //nolint:gosec // cache dir + a fixed name
	if err != nil {
		return HLSCodecsInfo{}, false
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) == "" {
		return HLSCodecsInfo{}, false
	}
	info := HLSCodecsInfo{Codecs: strings.TrimSpace(lines[0])}
	if len(lines) >= 3 {
		info.Width, _ = strconv.Atoi(strings.TrimSpace(lines[1]))
		info.Height, _ = strconv.Atoi(strings.TrimSpace(lines[2]))
	}
	return info, true
}

func writeCodecsMarker(dir string, info HLSCodecsInfo) {
	body := fmt.Sprintf("%s\n%d\n%d\n", info.Codecs, info.Width, info.Height)
	// Best-effort: a rendition whose codecs cannot be cached simply parses its
	// init again next time. Write by rename so a concurrent reader never sees
	// half a line.
	tmp, err := os.CreateTemp(dir, ".codecs-*")
	if err != nil {
		return
	}
	name := tmp.Name()
	if _, err := tmp.WriteString(body); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return
	}
	if err := os.Rename(name, filepath.Join(dir, hlsCodecsMarker)); err != nil {
		_ = os.Remove(name)
	}
}

// ParseInitCodecs extracts the RFC 6381 CODECS string and the encoded frame
// size from an fMP4 initialisation segment. It walks the box tree to the sample
// entries under each track's stsd and reads the codec configuration record —
// avcC for H.264, hvcC for HEVC — and the audio object type from esds.
func ParseInitCodecs(b []byte) (HLSCodecsInfo, error) {
	moov := findBox(b, "moov")
	if moov == nil {
		return HLSCodecsInfo{}, errors.New("hls: init segment has no moov box")
	}
	var video, audio string
	var width, height int
	for _, trak := range findBoxes(moov, "trak") {
		stsd := descend(trak, "mdia", "minf", "stbl", "stsd")
		if len(stsd) < 8 {
			continue
		}
		// stsd is a FullBox: 1 byte version + 3 flags + 4 byte entry_count,
		// then the sample entry boxes.
		for _, se := range iterBoxes(stsd[8:]) {
			switch se.typ {
			case "avc1", "avc3":
				width, height = visualSize(se.payload)
				if c := avcCodec(findBox(se.payload[visualSampleEntryHeader:], "avcC")); c != "" {
					video = c
				}
			case "hvc1", "hev1":
				width, height = visualSize(se.payload)
				if c := hevcCodec(findBox(se.payload[visualSampleEntryHeader:], "hvcC")); c != "" {
					video = c
				}
			case "mp4a":
				audio = audioCodecFromESDS(findBox(se.payload[audioSampleEntryHeader:], "esds"))
			}
		}
	}
	codecs := make([]string, 0, 2)
	if video != "" {
		codecs = append(codecs, video)
	}
	if audio != "" {
		codecs = append(codecs, audio)
	}
	if len(codecs) == 0 {
		return HLSCodecsInfo{}, errors.New("hls: init segment declares no known codecs")
	}
	return HLSCodecsInfo{Codecs: strings.Join(codecs, ","), Width: width, Height: height}, nil
}

// The fixed prefixes before the child boxes of a sample entry, past the box
// header iterBoxes already strips.
const (
	// VisualSampleEntry: 6 reserved + 2 data_ref_index + 2 pre_defined +
	// 2 reserved + 12 pre_defined + 2 width + 2 height + 4 horiz + 4 vert +
	// 4 reserved + 2 frame_count + 32 compressorname + 2 depth + 2 pre_defined.
	visualSampleEntryHeader = 78
	// AudioSampleEntry: 6 reserved + 2 data_ref_index + 8 reserved +
	// 2 channelcount + 2 samplesize + 2 pre_defined + 2 reserved + 4 samplerate.
	audioSampleEntryHeader = 28
)

// visualSize reads the width and height out of a visual sample entry payload
// (after the box header). They sit at fixed offsets 24 and 26.
func visualSize(payload []byte) (int, int) {
	if len(payload) < 28 {
		return 0, 0
	}
	return int(binary.BigEndian.Uint16(payload[24:26])), int(binary.BigEndian.Uint16(payload[26:28]))
}

// avcCodec builds the avc1 codec string from an AVCDecoderConfigurationRecord
// (the avcC box payload): avc1.PPCCLL from AVCProfileIndication,
// profile_compatibility and AVCLevelIndication. Bytes 01 64 08 29 →
// avc1.640829 (High profile, constraint 0x08, level 4.1).
func avcCodec(avcC []byte) string {
	if len(avcC) < 4 {
		return ""
	}
	return fmt.Sprintf("avc1.%02x%02x%02x", avcC[1], avcC[2], avcC[3])
}

// hevcCodec builds the hvc1 codec string from an HEVCDecoderConfigurationRecord
// (the hvcC box payload), per RFC 6381: profile space + profile idc, the
// compatibility flags (bit-reversed, trimmed), the tier + level, and the
// significant constraint bytes. A Main@L4.0 record yields hvc1.1.6.L120.90.
func hevcCodec(hvcC []byte) string {
	if len(hvcC) < 13 {
		return ""
	}
	profileSpace := (hvcC[1] >> 6) & 0x03
	tierFlag := (hvcC[1] >> 5) & 0x01
	profileIDC := hvcC[1] & 0x1f
	compat := binary.BigEndian.Uint32(hvcC[2:6])
	constraints := hvcC[6:12]
	levelIDC := hvcC[12]

	var sb strings.Builder
	sb.WriteString("hvc1.")
	switch profileSpace {
	case 1:
		sb.WriteByte('A')
	case 2:
		sb.WriteByte('B')
	case 3:
		sb.WriteByte('C')
	}
	sb.WriteString(strconv.Itoa(int(profileIDC)))
	fmt.Fprintf(&sb, ".%x", reverseBits32(compat))
	if tierFlag == 0 {
		sb.WriteString(".L")
	} else {
		sb.WriteString(".H")
	}
	sb.WriteString(strconv.Itoa(int(levelIDC)))
	// The constraint bytes, most significant first, with trailing zero bytes
	// dropped — a Main-profile record is 90 00 00 00 00 00, so just "90".
	end := len(constraints)
	for end > 0 && constraints[end-1] == 0 {
		end--
	}
	for i := 0; i < end; i++ {
		fmt.Fprintf(&sb, ".%02x", constraints[i])
	}
	return sb.String()
}

// reverseBits32 reverses the bit order of a 32-bit value, as the HEVC
// compatibility-flags field is written in the codec string.
func reverseBits32(v uint32) uint32 {
	var r uint32
	for i := 0; i < 32; i++ {
		r = (r << 1) | (v & 1)
		v >>= 1
	}
	return r
}

// audioCodecFromESDS reads the audio codec string out of an esds box payload.
// For AAC it is mp4a.40.<audioObjectType> (mp4a.40.2 for AAC-LC). It falls back
// to mp4a.40.2 — the object type the transcode always produces — when the
// descriptor cannot be read.
func audioCodecFromESDS(esds []byte) string {
	const fallback = "mp4a.40.2"
	if len(esds) < 5 {
		return fallback
	}
	// esds is a FullBox: skip 1 version + 3 flags.
	i := 4
	// ES_Descriptor (tag 0x03).
	if i >= len(esds) || esds[i] != 0x03 {
		return fallback
	}
	i++
	i = skipDescriptorLen(esds, i)
	// ES_ID (2) + flags (1).
	if i+3 > len(esds) {
		return fallback
	}
	flags := esds[i+2]
	i += 3
	if flags&0x80 != 0 { // streamDependenceFlag → dependsOn_ES_ID
		i += 2
	}
	if flags&0x40 != 0 { // URL_Flag → URLlength + URLstring
		if i >= len(esds) {
			return fallback
		}
		i += 1 + int(esds[i])
	}
	if flags&0x20 != 0 { // OCRstreamFlag → OCR_ES_ID
		i += 2
	}
	// DecoderConfigDescriptor (tag 0x04).
	if i >= len(esds) || esds[i] != 0x04 {
		return fallback
	}
	i++
	i = skipDescriptorLen(esds, i)
	if i >= len(esds) {
		return fallback
	}
	oti := esds[i] // objectTypeIndication
	i++
	// streamType (1) + bufferSizeDB (3) + maxBitrate (4) + avgBitrate (4).
	i += 12
	if oti != 0x40 { // not the MPEG-4 audio family
		return fmt.Sprintf("mp4a.%02x", oti)
	}
	// DecoderSpecificInfo (tag 0x05): the AudioSpecificConfig.
	if i >= len(esds) || esds[i] != 0x05 {
		return fallback
	}
	i++
	i = skipDescriptorLen(esds, i)
	if i >= len(esds) {
		return fallback
	}
	// audioObjectType is the top 5 bits of the first byte (the >=31 escape is
	// not used by anything this pipeline produces).
	aot := int(esds[i] >> 3)
	if aot <= 0 || aot >= 31 {
		return fallback
	}
	return fmt.Sprintf("mp4a.40.%d", aot)
}

// skipDescriptorLen advances past an MPEG-4 descriptor's expandable size field
// (1–4 bytes, 7 bits each, high bit continues).
func skipDescriptorLen(b []byte, i int) int {
	for n := 0; n < 4 && i < len(b); n++ {
		c := b[i]
		i++
		if c&0x80 == 0 {
			break
		}
	}
	return i
}

// mp4box is one box: its four-character type and its payload (past the header).
type mp4box struct {
	typ     string
	payload []byte
}

// iterBoxes lists the top-level boxes in buf. It handles the 64-bit largesize
// and the to-end size, and stops rather than reading past a malformed length.
func iterBoxes(buf []byte) []mp4box {
	var out []mp4box
	for o := 0; o+8 <= len(buf); {
		size := int(binary.BigEndian.Uint32(buf[o : o+4]))
		typ := string(buf[o+4 : o+8])
		hdr := 8
		switch size {
		case 1:
			// 64-bit largesize. It never appears in an init segment, but a box
			// bigger than the buffer is malformed either way, so bound it
			// before narrowing to int.
			if o+16 > len(buf) {
				return out
			}
			large := binary.BigEndian.Uint64(buf[o+8 : o+16])
			if large > uint64(len(buf)) {
				return out
			}
			size = int(large) //nolint:gosec // bounded by len(buf) just above, so it fits an int
			hdr = 16
		case 0:
			size = len(buf) - o
		}
		if size < hdr || o+size > len(buf) {
			return out
		}
		out = append(out, mp4box{typ: typ, payload: buf[o+hdr : o+size]})
		o += size
	}
	return out
}

// findBox returns the payload of the first box of type typ in buf, or nil.
func findBox(buf []byte, typ string) []byte {
	for _, b := range iterBoxes(buf) {
		if b.typ == typ {
			return b.payload
		}
	}
	return nil
}

// findBoxes returns the payloads of every box of type typ in buf.
func findBoxes(buf []byte, typ string) [][]byte {
	var out [][]byte
	for _, b := range iterBoxes(buf) {
		if b.typ == typ {
			out = append(out, b.payload)
		}
	}
	return out
}

// descend walks a chain of nested container boxes, returning the payload of the
// last one, or nil if any link is missing.
func descend(buf []byte, path ...string) []byte {
	for _, typ := range path {
		buf = findBox(buf, typ)
		if buf == nil {
			return nil
		}
	}
	return buf
}
