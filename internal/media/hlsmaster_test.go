package media

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The avc1 codec string is derived straight from the avcC record's profile,
// compatibility and level bytes. 01 64 08 29 is High@4.1 with constraint 0x08 —
// the deployed init — and must render as avc1.640829, not avc1.640028.
func TestAVCCodecString(t *testing.T) {
	cases := []struct {
		avcC []byte
		want string
	}{
		{[]byte{0x01, 0x64, 0x08, 0x29}, "avc1.640829"}, // High@4.1, constraint 0x08
		{[]byte{0x01, 0x64, 0x00, 0x29}, "avc1.640029"}, // High@4.1, no constraint
		{[]byte{0x01, 0x42, 0xc0, 0x1e}, "avc1.42c01e"}, // Baseline@3.0
		{[]byte{0x01, 0x4d, 0x40, 0x1f}, "avc1.4d401f"}, // Main@3.1
	}
	for _, c := range cases {
		if got := avcCodec(c.avcC); got != c.want {
			t.Errorf("avcCodec(% x) = %q, want %q", c.avcC, got, c.want)
		}
	}
	if got := avcCodec([]byte{0x01, 0x64}); got != "" {
		t.Errorf("avcCodec(short) = %q, want empty", got)
	}
}

// The hvc1 codec string follows RFC 6381: profile idc, the bit-reversed
// compatibility flags, the tier+level, and the significant constraint bytes.
func TestHEVCCodecString(t *testing.T) {
	// A Main-profile record: version, profile byte (space 0, tier 0, idc 1),
	// compat flags 0x60000000 (reversed → 6), constraints 90 00 00 00 00 00,
	// then the level.
	main := func(level byte) []byte {
		return []byte{0x01, 0x01, 0x60, 0x00, 0x00, 0x00, 0x90, 0x00, 0x00, 0x00, 0x00, 0x00, level}
	}
	cases := []struct {
		hvcC []byte
		want string
	}{
		{main(120), "hvc1.1.6.L120.90"}, // Main@L4.0
		{main(153), "hvc1.1.6.L153.90"}, // Main@L5.1 (2160p)
		// High tier (tier flag set) → H, and a Main10 profile idc of 2.
		{[]byte{0x01, 0x22, 0x60, 0x00, 0x00, 0x00, 0x90, 0x00, 0x00, 0x00, 0x00, 0x00, 150}, "hvc1.2.6.H150.90"},
	}
	for _, c := range cases {
		if got := hevcCodec(c.hvcC); got != c.want {
			t.Errorf("hevcCodec(% x) = %q, want %q", c.hvcC, got, c.want)
		}
	}
	if got := hevcCodec([]byte{0x01, 0x01}); got != "" {
		t.Errorf("hevcCodec(short) = %q, want empty", got)
	}
}

func TestAudioCodecFromESDS(t *testing.T) {
	if got := audioCodecFromESDS(buildESDS(0x12)); got != "mp4a.40.2" { // AOT 2 = AAC-LC
		t.Errorf("audioCodecFromESDS = %q, want mp4a.40.2", got)
	}
	if got := audioCodecFromESDS(buildESDS(0x28)); got != "mp4a.40.5" { // AOT 5 = HE-AAC
		t.Errorf("audioCodecFromESDS(HE-AAC) = %q, want mp4a.40.5", got)
	}
	// A garbage descriptor still yields the AAC-LC the pipeline always produces.
	if got := audioCodecFromESDS([]byte{0, 0, 0, 0}); got != "mp4a.40.2" {
		t.Errorf("audioCodecFromESDS(garbage) = %q, want mp4a.40.2", got)
	}
}

// buildESDS assembles a minimal esds payload whose AudioSpecificConfig has the
// given first byte (the top 5 bits are the audio object type).
func buildESDS(ascFirst byte) []byte {
	// Lengths are constant: the DecoderSpecificInfo is 2 bytes, so the
	// DecoderConfigDescriptor content is 0x11 (oti + 12 + 4) and the
	// ES_Descriptor content is 0x16 (ES_ID + flags + the 21-byte DCD).
	dsi := []byte{0x05, 0x02, ascFirst, 0x10} // DecoderSpecificInfo, 2 bytes
	dcd := append([]byte{0x04, 0x11, 0x40,    // DecoderConfigDescriptor, oti 0x40 (AAC)
		0x15, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}, dsi...) // streamType..bitrates
	es := append([]byte{0x03, 0x16, 0x00, 0x00, 0x00}, dcd...) // ES_Descriptor
	return append([]byte{0x00, 0x00, 0x00, 0x00}, es...)       // FullBox header + ES_Descriptor
}

func TestBuildHLSMaster(t *testing.T) {
	got := string(BuildHLSMaster("avc1.640829,mp4a.40.2", 5_000_000, 1920, 1080, 0))
	want := "#EXTM3U\n" +
		"#EXT-X-VERSION:7\n" +
		"#EXT-X-INDEPENDENT-SEGMENTS\n" +
		"#EXT-X-STREAM-INF:BANDWIDTH=5000000,CODECS=\"avc1.640829,mp4a.40.2\",CLOSED-CAPTIONS=NONE,RESOLUTION=1920x1080\n" +
		"index.m3u8\n"
	if got != want {
		t.Errorf("1080p master:\n%q\nwant\n%q", got, want)
	}

	got4k := string(BuildHLSMaster("hvc1.1.6.L153.90,mp4a.40.2", 20_000_000, 3840, 2160, 0))
	want4k := "#EXTM3U\n" +
		"#EXT-X-VERSION:7\n" +
		"#EXT-X-INDEPENDENT-SEGMENTS\n" +
		"#EXT-X-STREAM-INF:BANDWIDTH=20000000,CODECS=\"hvc1.1.6.L153.90,mp4a.40.2\",CLOSED-CAPTIONS=NONE,RESOLUTION=3840x2160\n" +
		"index.m3u8\n"
	if got4k != want4k {
		t.Errorf("2160p master:\n%q\nwant\n%q", got4k, want4k)
	}

	// Unknown frame size omits RESOLUTION rather than guessing one.
	noRes := string(BuildHLSMaster("avc1.640829,mp4a.40.2", 5_000_000, 0, 0, 0))
	if want := "#EXT-X-STREAM-INF:BANDWIDTH=5000000,CODECS=\"avc1.640829,mp4a.40.2\",CLOSED-CAPTIONS=NONE\nindex.m3u8\n"; noRes[len(noRes)-len(want):] != want {
		t.Errorf("master without resolution = %q", noRes)
	}

	// A resume position carries through to the media playlist as `?from=`, so
	// the player follows the master to a playlist that starts there.
	// A player that is told nothing about captions invents a "CC" option and
	// shows it in its own menu — a subtitle control that selects nothing. See
	// BuildHLSMaster.
	for _, master := range []string{got, got4k, noRes} {
		if !strings.Contains(master, "CLOSED-CAPTIONS=NONE") {
			t.Errorf("master does not rule captions out:\n%s", master)
		}
	}

	resume := string(BuildHLSMaster("avc1.640829,mp4a.40.2", 5_000_000, 1920, 1080, 1408))
	if want := "\nindex.m3u8?from=1408.000\n"; resume[len(resume)-len(want):] != want {
		t.Errorf("resume master variant URI = %q", resume)
	}
}

func TestDefaultHLSCodecsAndBandwidth(t *testing.T) {
	if got := DefaultHLSCodecs(1080); got != "avc1.640829,mp4a.40.2" {
		t.Errorf("DefaultHLSCodecs(1080) = %q", got)
	}
	if got := DefaultHLSCodecs(2160); got != "hvc1.1.6.L153.90,mp4a.40.2" {
		t.Errorf("DefaultHLSCodecs(2160) = %q", got)
	}
	if got := DefaultHLSCodecs(1440); got != "hvc1.1.6.L123.90,mp4a.40.2" {
		t.Errorf("DefaultHLSCodecs(1440) = %q", got)
	}
	for _, c := range []struct {
		height int
		want   int
	}{{2160, 20_000_000}, {1440, 10_000_000}, {1080, 5_000_000}, {720, 2_500_000}, {480, 1_200_000}} {
		if got := HLSBandwidth(c.height); got != c.want {
			t.Errorf("HLSBandwidth(%d) = %d, want %d", c.height, got, c.want)
		}
	}
}

// ParseInitCodecs walks the box tree to the sample entries. This builds a
// minimal but well-formed init with an H.264 video track and an AAC audio track
// and asserts both the codec string and the encoded frame size.
func TestParseInitCodecs(t *testing.T) {
	avcC := mp4Box("avcC", []byte{0x01, 0x64, 0x08, 0x29, 0xff, 0xe1, 0x00, 0x00})
	avc1 := mp4Box("avc1", append(visualPrefix(1920, 1080), avcC...))
	esds := mp4Box("esds", buildESDS(0x12))
	mp4a := mp4Box("mp4a", append(audioPrefix(), esds...))

	videoStbl := mp4Box("stbl", stsd(avc1))
	audioStbl := mp4Box("stbl", stsd(mp4a))
	videoTrak := mp4Box("trak", mp4Box("mdia", mp4Box("minf", videoStbl)))
	audioTrak := mp4Box("trak", mp4Box("mdia", mp4Box("minf", audioStbl)))
	moov := mp4Box("moov", append(videoTrak, audioTrak...))
	init := append(mp4Box("ftyp", []byte("iso5")), moov...)

	info, err := ParseInitCodecs(init)
	if err != nil {
		t.Fatal(err)
	}
	if info.Codecs != "avc1.640829,mp4a.40.2" {
		t.Errorf("codecs = %q, want avc1.640829,mp4a.40.2", info.Codecs)
	}
	if info.Width != 1920 || info.Height != 1080 {
		t.Errorf("size = %dx%d, want 1920x1080", info.Width, info.Height)
	}

	if _, err := ParseInitCodecs([]byte("not an mp4")); err == nil {
		t.Error("ParseInitCodecs on junk: want error")
	}
}

func TestEnsureHLSCodecs(t *testing.T) {
	// A cached marker is authoritative and read back with its dimensions.
	dir := t.TempDir()
	writeCodecsMarker(dir, HLSCodecsInfo{Codecs: "avc1.640829,mp4a.40.2", Width: 1920, Height: 1080})
	if info, exact := EnsureHLSCodecs(dir, 1080); !exact || info.Codecs != "avc1.640829,mp4a.40.2" || info.Width != 1920 {
		t.Errorf("from marker: %+v exact=%v", info, exact)
	}

	// No marker and no init: the height's default, not exact.
	empty := t.TempDir()
	if info, exact := EnsureHLSCodecs(empty, 2160); exact || info.Codecs != "hvc1.1.6.L153.90,mp4a.40.2" {
		t.Errorf("fallback: %+v exact=%v", info, exact)
	}

	// An init on disk is parsed and its codecs cached for next time.
	withInit := t.TempDir()
	avc1 := mp4Box("avc1", append(visualPrefix(1280, 720), mp4Box("avcC", []byte{0x01, 0x64, 0x08, 0x29})...))
	moov := mp4Box("moov", mp4Box("trak", mp4Box("mdia", mp4Box("minf", mp4Box("stbl", stsd(avc1))))))
	if err := os.WriteFile(filepath.Join(withInit, HLSInitName), moov, 0o600); err != nil {
		t.Fatal(err)
	}
	info, exact := EnsureHLSCodecs(withInit, 720)
	if !exact || info.Codecs != "avc1.640829" || info.Width != 1280 || info.Height != 720 {
		t.Errorf("from init: %+v exact=%v", info, exact)
	}
	if _, err := os.Stat(filepath.Join(withInit, hlsCodecsMarker)); err != nil {
		t.Errorf("codecs marker not cached: %v", err)
	}
}

// stsd wraps sample entries in an stsd FullBox (version+flags+entry_count).
func stsd(entries ...[]byte) []byte {
	head := []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, byte(len(entries) & 0xff)}
	for _, e := range entries {
		head = append(head, e...)
	}
	return mp4Box("stsd", head)
}

// visualPrefix is the 78-byte VisualSampleEntry header with the width and
// height set, before the child boxes.
func visualPrefix(width, height int) []byte {
	p := make([]byte, visualSampleEntryHeader)
	binary.BigEndian.PutUint16(p[24:26], uint16(width&0xffff))
	binary.BigEndian.PutUint16(p[26:28], uint16(height&0xffff))
	return p
}

// audioPrefix is the 28-byte AudioSampleEntry header before the child boxes.
func audioPrefix() []byte { return make([]byte, audioSampleEntryHeader) }
