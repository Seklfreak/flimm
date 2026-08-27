package media

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The copy-vs-encode decision is what makes a rendition affordable on an
// archive that is already in the right codec, and what stops it shipping a 4K
// x264 encode nobody asked for.
func TestHLSVideoCodecChoice(t *testing.T) {
	for _, tc := range []struct {
		name   string
		src    HLSSource
		height int
		want   string
	}{
		{"h264 720p at 720", HLSSource{VideoCodec: "avc1", Height: 720}, 720, "copy"},
		{"h264 with a profile suffix", HLSSource{VideoCodec: "avc1.640028", Height: 1080}, 1080, "copy"},
		{"ffprobe spelling", HLSSource{VideoCodec: "h264", Height: 1080}, 1080, "copy"},
		{"a taller source is scaled, so encoded", HLSSource{VideoCodec: "avc1", Height: 1080}, 720, "libx264"},
		{"a shorter source is not the taller rung", HLSSource{VideoCodec: "avc1", Height: 720}, 1080, "libx264"},
		{"unknown height is not trusted", HLSSource{VideoCodec: "avc1"}, 1080, "libx264"},
		{"av1", HLSSource{VideoCodec: "av01.0.08M.08", Height: 1080}, 1080, "libx264"},
		{"vp9", HLSSource{VideoCodec: "vp09", Height: 720}, 720, "libx264"},
		{"no metadata at all", HLSSource{}, 1080, "libx264"},
		// Above 1080 the rendition is HEVC, so H.264 is no longer a copy and
		// HEVC is.
		{"hevc 2160 at 2160", HLSSource{VideoCodec: "hvc1", Height: 2160}, 2160, "copy"},
		{"hevc sample entry with a suffix", HLSSource{VideoCodec: "hvc1.1.6.L120.90", Height: 1440}, 1440, "copy"},
		{"ffprobe spelling for hevc", HLSSource{VideoCodec: "hevc", Height: 2160}, 2160, "copy"},
		{"hev1 is hevc too", HLSSource{VideoCodec: "hev1", Height: 1440}, 1440, "copy"},
		{"h264 at a hevc height is re-encoded", HLSSource{VideoCodec: "avc1", Height: 2160}, 2160, "libx265"},
		{"hevc at an h264 height is re-encoded", HLSSource{VideoCodec: "hvc1", Height: 1080}, 1080, "libx264"},
		{"av1 4k", HLSSource{VideoCodec: "av01.0.12M.08", Height: 2160}, 2160, "libx265"},
	} {
		if got := hlsVideoCodec(tc.src, tc.height); got != tc.want {
			t.Errorf("%s: hlsVideoCodec(%+v, %d) = %q, want %q", tc.name, tc.src, tc.height, got, tc.want)
		}
	}
}

// What a video offers is what its source can fill: a 4K rung on a 1080p source
// is an upscale — a bigger file with no more detail — and a client would have
// no way to know it was not getting what it asked for.
func TestHLSOfferedHeights(t *testing.T) {
	for _, tc := range []struct {
		name   string
		source int
		want   []int
	}{
		{"4k offers everything", 2160, []int{2160, 1440, 1080, 720, 480}},
		{"1440p stops there", 1440, []int{1440, 1080, 720, 480}},
		{"1080p", 1080, []int{1080, 720, 480}},
		{"720p", 720, []int{720, 480}},
		{"480p", 480, []int{480}},
		{"an odd height offers the rungs below it", 1200, []int{1080, 720, 480}},
		{"unknown height offers the default and below", 0, []int{1080, 720, 480}},
		{"a source shorter than the lowest rung still gets one", 360, []int{480}},
	} {
		if got := HLSOfferedHeights(tc.source); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: HLSOfferedHeights(%d) = %v, want %v", tc.name, tc.source, got, tc.want)
		}
	}
}

// hls_url points at the default rendition, which for a small source is the
// tallest it has.
func TestHLSDefaultOffered(t *testing.T) {
	for source, want := range map[int]int{2160: 1080, 1080: 1080, 720: 720, 480: 480, 360: 480, 0: 1080} {
		if got := HLSDefaultOffered(source); got != want {
			t.Errorf("HLSDefaultOffered(%d) = %d, want %d", source, got, want)
		}
	}
}

// The codec per height is part of the API (`hls_variants[].codec`) and decides
// which encoder every rung of the ladder uses.
func TestHLSCodecForHeight(t *testing.T) {
	for height, want := range map[int]string{2160: HLSCodecHEVC, 1440: HLSCodecHEVC, 1080: HLSCodecH264, 720: HLSCodecH264, 480: HLSCodecH264} {
		if got := HLSCodecForHeight(height); got != want {
			t.Errorf("HLSCodecForHeight(%d) = %q, want %q", height, got, want)
		}
		wantEncoder := "libx264"
		if want == HLSCodecHEVC {
			wantEncoder = "libx265"
		}
		if got := hlsSoftwareEncoder(height); got != wantEncoder {
			t.Errorf("hlsSoftwareEncoder(%d) = %q, want %q", height, got, wantEncoder)
		}
	}
	for _, h := range HLSHeights() {
		if !ValidHLSHeight(h) {
			t.Errorf("ValidHLSHeight(%d) = false for a listed height", h)
		}
	}
	for _, h := range []int{0, -1, 1081, 4320, 360} {
		if ValidHLSHeight(h) {
			t.Errorf("ValidHLSHeight(%d) = true, want false", h)
		}
	}
}

// Every height is its own cache entry, or one quality would evict, wait on, or
// be served in place of another.
func TestHLSNameIsPerHeight(t *testing.T) {
	seen := map[string]bool{}
	for _, h := range HLSHeights() {
		name := HLSName("v1", h)
		if seen[name] {
			t.Fatalf("two heights share the entry %q", name)
		}
		seen[name] = true
		if !strings.HasPrefix(name, HLSVariant+"-") || !strings.HasSuffix(name, "-v1") {
			t.Errorf("HLSName(v1, %d) = %q", h, name)
		}
	}
	if HLSName("v1", 1080) == HLSName("v2", 1080) {
		t.Error("two videos share an entry")
	}
}

func TestHLSArgs(t *testing.T) {
	want := []string{
		"-hide_banner", "-loglevel", "error",
		"-i", "pipe:0",
		"-map", "0:v:0", "-map", "0:a:0",
		"-vf", "scale=-2:'min(1080,ih)'",
		"-c:v", "libx264",
		"-preset", "veryfast", "-crf", "23",
		"-profile:v", "high", "-level", "4.1", "-pix_fmt", "yuv420p",
		"-g", "96", "-keyint_min", "96", "-sc_threshold", "0",
		"-c:a", "aac", "-b:a", "160k", "-ac", "2",
		"-threads", "0",
		"-f", "hls",
		"-hls_time", "4",
		"-hls_playlist_type", "event",
		"-hls_segment_type", "fmp4",
		"-hls_flags", "independent_segments+temp_file",
		"-hls_fmp4_init_filename", "init.mp4",
		"-hls_segment_filename", "seg%05d.m4s",
		"-y", "index.m3u8",
	}
	if got := hlsArgs("libx264", "aac", 1080); !reflect.DeepEqual(got, want) {
		t.Errorf("encode args =\n%v\nwant\n%v", got, want)
	}
}

// A copy must carry none of the encoder settings: they would either be
// rejected or silently ignored, and either way they are a lie about what ran.
func TestHLSArgsCopyCarriesNoEncoderSettings(t *testing.T) {
	got := hlsArgs("copy", "copy", 1080)
	for _, unwanted := range []string{"-vf", "-crf", "-preset", "-profile:v", "-b:a", "-ac", "-g"} {
		if slices.Contains(got, unwanted) {
			t.Errorf("copy args carry %s: %v", unwanted, got)
		}
	}
	for _, pair := range [][2]string{{"-c:v", "copy"}, {"-c:a", "copy"}, {"-f", "hls"}, {"-hls_segment_type", "fmp4"}} {
		if i := slices.Index(got, pair[0]); i < 0 || got[i+1] != pair[1] {
			t.Errorf("copy args missing %v: %v", pair, got)
		}
	}
	// Still HLS: a compatible source is segmented, not passed through whole.
	if got[len(got)-1] != HLSPlaylistName {
		t.Errorf("copy args do not end in the playlist: %v", got)
	}
}

// Mixing one copied and one encoded track is the common case (H.264 video with
// Opus audio), so it must not leak the other track's settings.
func TestHLSArgsMixedCopyAndEncode(t *testing.T) {
	got := hlsArgs("copy", "aac", 1080)
	if i := slices.Index(got, "-c:v"); got[i+1] != "copy" {
		t.Errorf("video should be copied: %v", got)
	}
	if slices.Contains(got, "-vf") {
		t.Errorf("copied video must not be scaled: %v", got)
	}
	if i := slices.Index(got, "-b:a"); i < 0 || got[i+1] != "160k" {
		t.Errorf("encoded audio should carry a bitrate: %v", got)
	}
}

func TestHLSPlaylistReady(t *testing.T) {
	dir := t.TempDir()
	if HLSPlaylistReady(dir) {
		t.Error("an empty directory is not ready")
	}
	playlist := filepath.Join(dir, HLSPlaylistName)
	// A header with no segments yet: handing this to a player is what the wait
	// exists to avoid.
	if err := os.WriteFile(playlist, []byte("#EXTM3U\n#EXT-X-VERSION:7\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if HLSPlaylistReady(dir) {
		t.Error("a playlist with no segments is not ready")
	}
	if err := os.WriteFile(playlist, []byte("#EXTM3U\n#EXTINF:4.0,\nseg00000.m4s\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !HLSPlaylistReady(dir) {
		t.Error("a playlist naming a segment is ready")
	}
}

// A player waits for #EXT-X-ENDLIST to know the video ended; without it it
// keeps polling a playlist that will never grow.
func TestEnsureEndList(t *testing.T) {
	dir := t.TempDir()
	playlist := filepath.Join(dir, HLSPlaylistName)
	if err := os.WriteFile(playlist, []byte("#EXTM3U\nseg00000.m4s\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for range 2 { // idempotent: a second pass must not append a second tag
		if err := ensureEndList(playlist); err != nil {
			t.Fatal(err)
		}
	}
	b, err := os.ReadFile(playlist) //nolint:gosec // test fixture path
	if err != nil {
		t.Fatal(err)
	}
	if n := bytes.Count(b, []byte("#EXT-X-ENDLIST")); n != 1 {
		t.Errorf("playlist carries %d end tags, want 1: %q", n, b)
	}
}

// Nothing should ever put a credential in ffmpeg's stderr — the source is
// piped in — but a log line is not a place to find out we were wrong.
func TestScrubSecrets(t *testing.T) {
	for _, in := range []string{
		"http error: Authorization: Token abcd1234secret",
		"GET /media/x.mp4?token=abcd1234secret failed",
		"header was Token abcd1234secret",
	} {
		if got := scrubSecrets(in); strings.Contains(got, "abcd1234secret") {
			t.Errorf("scrubSecrets(%q) = %q, still carries the secret", in, got)
		}
	}
	const clean = "Invalid data found when processing input"
	if got := scrubSecrets(clean); got != clean {
		t.Errorf("scrubSecrets mangled an ordinary message: %q", got)
	}
}

// writeStubHLSFFmpeg installs a script that records the -c:v of each call and
// writes a plausible rendition, so the derivation can be tested without a real
// ffmpeg. failCopy makes the copy attempt fail, as an unmuxable source would.
func writeStubHLSFFmpeg(t *testing.T, dir, callLog string, failCopy, writeEndList bool) string {
	t.Helper()
	path := filepath.Join(dir, "ffmpeg-hls-stub")
	end := ""
	if writeEndList {
		end = "#EXT-X-ENDLIST\\n"
	}
	script := "#!/bin/sh\n" +
		"codec=$(echo \"$@\" | sed -n 's/.*-c:v \\([^ ]*\\).*/\\1/p')\n" +
		"echo \"$codec\" >> " + callLog + "\n"
	if failCopy {
		script += "[ \"$codec\" = copy ] && { echo 'could not mux' >&2; exit 1; }\n"
	}
	script += "printf '#EXTM3U\\n#EXT-X-MAP:URI=\"init.mp4\"\\n#EXTINF:4.0,\\nseg00000.m4s\\n" + end + "' > " + HLSPlaylistName + "\n" +
		": > " + HLSInitName + "\n: > seg00000.m4s\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil { //nolint:gosec // test fixture must be executable
		t.Fatal(err)
	}
	return path
}

func emptySource(context.Context) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(nil)), nil
}

// A source TA calls H.264/AAC takes the copy path — the whole reason a
// compatible archive costs almost nothing here.
func TestHLSCopiesCompatibleSource(t *testing.T) {
	dir := t.TempDir()
	callLog := filepath.Join(dir, "calls.log")
	stub := writeStubHLSFFmpeg(t, dir, callLog, false, true)
	out := filepath.Join(dir, "out")
	if err := os.Mkdir(out, 0o750); err != nil {
		t.Fatal(err)
	}

	derive := HLS(stub, HLSSource{VideoCodec: "avc1", Height: 720, AudioCodec: "mp4a"}, 720, HWAccel{}, nil, emptySource)
	if err := derive(t.Context(), out); err != nil {
		t.Fatalf("derive: %v", err)
	}
	if got := readCalls(t, callLog); got != "copy" {
		t.Errorf("ffmpeg calls = %q, want a single copy call", got)
	}
	for _, name := range []string{HLSPlaylistName, HLSInitName, "seg00000.m4s"} {
		if _, err := os.Stat(filepath.Join(out, name)); err != nil {
			t.Errorf("derivation did not produce %s: %v", name, err)
		}
	}
}

// The codec strings are TA's metadata, not a guarantee. A copy that the muxer
// refuses must fall back to a real encode rather than failing the request.
func TestHLSFallsBackWhenCopyFails(t *testing.T) {
	dir := t.TempDir()
	callLog := filepath.Join(dir, "calls.log")
	stub := writeStubHLSFFmpeg(t, dir, callLog, true, true)
	out := filepath.Join(dir, "out")
	if err := os.Mkdir(out, 0o750); err != nil {
		t.Fatal(err)
	}

	derive := HLS(stub, HLSSource{VideoCodec: "avc1", Height: 720, AudioCodec: "mp4a"}, 720, HWAccel{}, nil, emptySource)
	if err := derive(t.Context(), out); err != nil {
		t.Fatalf("a failed copy should fall back to encoding, got: %v", err)
	}
	if got := readCalls(t, callLog); got != "copy\nlibx264" {
		t.Errorf("ffmpeg calls = %q, want copy then libx264", got)
	}
}

// An incompatible source is encoded straight away: no pointless copy attempt
// whose only result is a wasted pass over a multi-gigabyte file.
func TestHLSSkipsCopyForIncompatibleSource(t *testing.T) {
	dir := t.TempDir()
	callLog := filepath.Join(dir, "calls.log")
	stub := writeStubHLSFFmpeg(t, dir, callLog, true, true)
	out := filepath.Join(dir, "out")
	if err := os.Mkdir(out, 0o750); err != nil {
		t.Fatal(err)
	}

	derive := HLS(stub, HLSSource{VideoCodec: "av01", Height: 1080, AudioCodec: "opus"}, 1080, HWAccel{}, nil, emptySource)
	if err := derive(t.Context(), out); err != nil {
		t.Fatalf("derive: %v", err)
	}
	if got := readCalls(t, callLog); got != "libx264" {
		t.Errorf("ffmpeg calls = %q, want a single libx264 call", got)
	}
}

// Whatever ffmpeg does or does not write, the finished playlist must be closed
// or a player stalls at the end of the video forever.
func TestHLSClosesThePlaylist(t *testing.T) {
	dir := t.TempDir()
	stub := writeStubHLSFFmpeg(t, dir, filepath.Join(dir, "calls.log"), false, false)
	out := filepath.Join(dir, "out")
	if err := os.Mkdir(out, 0o750); err != nil {
		t.Fatal(err)
	}

	derive := HLS(stub, HLSSource{VideoCodec: "av01", AudioCodec: "opus"}, 1080, HWAccel{}, nil, emptySource)
	if err := derive(t.Context(), out); err != nil {
		t.Fatalf("derive: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(out, HLSPlaylistName)) //nolint:gosec // test fixture path
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte("#EXT-X-ENDLIST")) {
		t.Errorf("playlist was left open: %q", b)
	}
}

func readCalls(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // test fixture path
	if err != nil {
		t.Fatal(err)
	}
	return string(bytes.TrimSpace(b))
}

// The real thing: a source that must be transcoded, through the real ffmpeg,
// producing a playlist a player can actually load. The arguments are the part
// most likely to be wrong and no stub would catch it.
func TestHLSTranscodesToPlayableRendition(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mp4")
	//nolint:gosec // G204: fixture paths from t.TempDir(), no request data
	fixture := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc=duration=6:size=320x240:rate=24",
		"-f", "lavfi", "-i", "sine=duration=6",
		"-c:v", "libx264", "-c:a", "aac", "-movflags", "+faststart", "-y", src)
	var stderr bytes.Buffer
	fixture.Stderr = &stderr
	if err := fixture.Run(); err != nil {
		t.Skipf("cannot build fixture: %v: %s", err, stderr.String())
	}

	out := filepath.Join(dir, "out")
	if err := os.Mkdir(out, 0o750); err != nil {
		t.Fatal(err)
	}
	// Claim AV1 so the encode path runs, which is the case that matters.
	derive := HLS("ffmpeg", HLSSource{VideoCodec: "av01", Height: 240, AudioCodec: "opus"}, 480, HWAccel{}, nil,
		func(context.Context) (io.ReadCloser, error) {
			return os.Open(src) //nolint:gosec // test fixture path
		})
	if err := derive(t.Context(), out); err != nil {
		t.Fatalf("derive: %v", err)
	}

	b, err := os.ReadFile(filepath.Join(out, HLSPlaylistName)) //nolint:gosec // test fixture path
	if err != nil {
		t.Fatal(err)
	}
	playlist := string(b)
	if !strings.HasPrefix(playlist, "#EXTM3U") {
		t.Errorf("not an m3u8: %q", playlist)
	}
	// A finished rendition is closed; a player that never sees this tag never
	// reaches the end of the video.
	if !strings.Contains(strings.TrimSpace(playlist), "#EXT-X-ENDLIST") {
		t.Errorf("playlist does not end with #EXT-X-ENDLIST: %q", playlist)
	}
	if !strings.Contains(playlist, `#EXT-X-MAP:URI="`+HLSInitName+`"`) {
		t.Errorf("playlist does not reference the init segment: %q", playlist)
	}
	if !HLSPlaylistReady(out) {
		t.Error("finished rendition does not read as ready")
	}
	if st, err := os.Stat(filepath.Join(out, HLSInitName)); err != nil || st.Size() == 0 {
		t.Errorf("init segment missing or empty: %v", err)
	}
	segments, err := filepath.Glob(filepath.Join(out, "seg*.m4s"))
	if err != nil || len(segments) == 0 {
		t.Fatalf("no segments produced: %v", err)
	}
	// Every name in the playlist must match what the route will serve.
	for _, seg := range segments {
		if !strings.Contains(playlist, filepath.Base(seg)) {
			t.Errorf("segment %s is not in the playlist", filepath.Base(seg))
		}
	}
	assertH264AAC(t, segments[0], filepath.Join(out, HLSInitName))
}

// assertH264AAC checks the rendition really is what Apple hardware decodes.
func assertH264AAC(t *testing.T, segment, init string) {
	t.Helper()
	probe, err := exec.LookPath("ffprobe")
	if err != nil {
		return
	}
	// A media segment is only decodable together with its init segment.
	joined := filepath.Join(t.TempDir(), "joined.mp4")
	a, err := os.ReadFile(init) //nolint:gosec // test fixture path
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(segment) //nolint:gosec // test fixture path
	if err != nil {
		t.Fatal(err)
	}
	//nolint:gosec // G703: joined is t.TempDir() plus a literal name
	if err := os.WriteFile(joined, append(a, b...), 0o600); err != nil {
		t.Fatal(err)
	}
	//nolint:gosec // G204: probe path from LookPath, file from t.TempDir()
	out, err := exec.Command(probe, "-hide_banner", "-loglevel", "error",
		"-show_entries", "stream=codec_name", "-of", "csv=p=0", joined).Output()
	if err != nil {
		t.Fatalf("ffprobe: %v", err)
	}
	got := string(bytes.TrimSpace(out))
	for _, want := range []string{"h264", "aac"} {
		if !strings.Contains(got, want) {
			t.Errorf("rendition is not %s: %q", want, got)
		}
	}
}

// The hardware command line is the part no unit test can prove works — only a
// GPU can — so what is checked here is that it is the *same rendition* as the
// software path, built the way VAAPI needs: the acceleration flags before the
// input, the frames scaled and formatted on the GPU, and none of the
// software-encoder knobs carried along.
func TestHLSVAAPIArgs(t *testing.T) {
	want := []string{
		"-hide_banner", "-loglevel", "error",
		"-hwaccel", "vaapi", "-hwaccel_device", "/dev/dri/renderD128", "-hwaccel_output_format", "vaapi",
		"-i", "pipe:0",
		"-map", "0:v:0", "-map", "0:a:0",
		"-vf", "scale_vaapi=w=-2:h='min(1080,ih)':format=nv12",
		"-c:v", "h264_vaapi",
		"-rc_mode", "CQP", "-qp", "23",
		"-profile:v", "high", "-level", "4.1",
		"-g", "96", "-keyint_min", "96",
		"-c:a", "aac", "-b:a", "160k", "-ac", "2",
		"-threads", "0",
		"-f", "hls",
		"-hls_time", "4",
		"-hls_playlist_type", "event",
		"-hls_segment_type", "fmp4",
		"-hls_flags", "independent_segments+temp_file",
		"-hls_fmp4_init_filename", "init.mp4",
		"-hls_segment_filename", "seg%05d.m4s",
		"-y", "index.m3u8",
	}
	if got := hlsVAAPIArgs(DefaultVAAPIDevice, "aac", 1080); !reflect.DeepEqual(got, want) {
		t.Errorf("vaapi args =\n%v\nwant\n%v", got, want)
	}
}

func TestHLSVAAPIArgsShape(t *testing.T) {
	got := hlsVAAPIArgs("/dev/dri/renderD129", "aac", 1080)

	// -hwaccel* configure the *input*; after -i they would be ignored and the
	// decode would silently run on the CPU, which is the failure mode this
	// whole change exists to avoid.
	input := slices.Index(got, "-i")
	for _, flag := range []string{"-hwaccel", "-hwaccel_device", "-hwaccel_output_format"} {
		i := slices.Index(got, flag)
		if i < 0 || i > input {
			t.Errorf("%s must appear before -i: %v", flag, got)
		}
	}
	if i := slices.Index(got, "-hwaccel_device"); got[i+1] != "/dev/dri/renderD129" {
		t.Errorf("the configured device is not used: %v", got)
	}
	// Software-encoder settings: either rejected or ignored by h264_vaapi, and
	// -pix_fmt would drag every frame back off the GPU.
	for _, unwanted := range []string{"-preset", "-crf", "-pix_fmt", "-sc_threshold"} {
		if slices.Contains(got, unwanted) {
			t.Errorf("vaapi args carry the software knob %s: %v", unwanted, got)
		}
	}
	// The rendition still has to be the one clients are promised.
	for _, pair := range [][2]string{{"-c:v", "h264_vaapi"}, {"-profile:v", "high"}, {"-level", "4.1"}, {"-c:a", "aac"}} {
		if i := slices.Index(got, pair[0]); i < 0 || got[i+1] != pair[1] {
			t.Errorf("vaapi args missing %v: %v", pair, got)
		}
	}
	if got[len(got)-1] != HLSPlaylistName {
		t.Errorf("vaapi args do not end in the playlist: %v", got)
	}
}

// The ladder decides how much work a video costs. Getting the order or the
// membership wrong is either a pointless GPU attempt on a stream copy or a
// software encode on a box with a perfectly good GPU.
func TestHLSAttemptLadder(t *testing.T) {
	hw := HWAccel{VAAPI: true, Device: DefaultVAAPIDevice}
	for _, tc := range []struct {
		name   string
		src    HLSSource
		height int
		hw     HWAccel
		want   []string
	}{
		{"gpu off: encode in software", HLSSource{VideoCodec: "av01", AudioCodec: "opus"}, 1080, HWAccel{}, []string{"libx264"}},
		{"gpu on: try it, then software", HLSSource{VideoCodec: "av01", AudioCodec: "opus"}, 1080, hw, []string{"vaapi", "libx264"}},
		{"a copyable source is still copied first", HLSSource{VideoCodec: "avc1", Height: 720, AudioCodec: "mp4a"}, 720, hw, []string{"copy", "vaapi", "libx264"}},
		{"copied video, encoded audio", HLSSource{VideoCodec: "avc1", Height: 720, AudioCodec: "opus"}, 720, hw, []string{"copy", "vaapi", "libx264"}},
		{"the h264 source of a taller rung is not copied", HLSSource{VideoCodec: "avc1", Height: 2160, AudioCodec: "opus"}, 2160, hw, []string{"vaapi", "libx265"}},
		{"a hevc rung ends in libx265", HLSSource{VideoCodec: "av01", AudioCodec: "opus", Height: 2160}, 1440, HWAccel{}, []string{"libx265"}},
		{"a hevc source at its own height is copied", HLSSource{VideoCodec: "hvc1", Height: 2160, AudioCodec: "mp4a"}, 2160, hw, []string{"copy", "vaapi", "libx265"}},
		{"aac audio alone is enough for a copy attempt", HLSSource{VideoCodec: "av01", Height: 2160, AudioCodec: "mp4a"}, 2160, HWAccel{}, []string{"copy", "libx265"}},
	} {
		attempts := hlsAttempts(tc.src, tc.height, tc.hw)
		var got []string
		for _, a := range attempts {
			got = append(got, a.name)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: ladder = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// writeStubVAAPIFFmpeg installs a stub that fails the hardware attempt the way
// a GPU that cannot decode a source does — after writing part of a rendition —
// and succeeds on the software one. It records what each call was asked to do,
// and whether the hardware attempt's leftovers were still lying about.
func writeStubVAAPIFFmpeg(t *testing.T, dir, callLog string) string {
	t.Helper()
	path := filepath.Join(dir, "ffmpeg-vaapi-stub")
	script := "#!/bin/sh\n" +
		"case \"$*\" in\n" +
		"  *h264_vaapi*) codec=vaapi ;;\n" +
		"  *libx264*) codec=libx264 ;;\n" +
		"  *) codec=copy ;;\n" +
		"esac\n" +
		"echo \"$codec\" >> " + callLog + "\n" +
		"[ -e vaapi-leftover.m4s ] && echo 'dirty' >> " + callLog + "\n" +
		"if [ \"$codec\" = vaapi ]; then\n" +
		// A hardware attempt gets far enough to publish a segment and a
		// playlist naming it before the decoder gives up on a 10-bit frame.
		"  : > vaapi-leftover.m4s\n" +
		"  printf '#EXTM3U\\n#EXTINF:4.0,\\nvaapi-leftover.m4s\\n' > " + HLSPlaylistName + "\n" +
		"  echo 'Failed setup for format vaapi: hwaccel initialisation returned error' >&2\n" +
		"  exit 1\n" +
		"fi\n" +
		"printf '#EXTM3U\\n#EXT-X-MAP:URI=\"init.mp4\"\\n#EXTINF:4.0,\\nseg00000.m4s\\n#EXT-X-ENDLIST\\n' > " + HLSPlaylistName + "\n" +
		": > " + HLSInitName + "\n: > seg00000.m4s\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil { //nolint:gosec // test fixture must be executable
		t.Fatal(err)
	}
	return path
}

// A GPU that cannot manage a source is not the viewer's problem: the transcode
// falls back to the CPU and the request succeeds. What must not happen is the
// half-written hardware attempt surviving into the software one — the playlist
// would then name segments nothing wrote.
func TestHLSFallsBackFromVAAPIToSoftware(t *testing.T) {
	dir := t.TempDir()
	callLog := filepath.Join(dir, "calls.log")
	stub := writeStubVAAPIFFmpeg(t, dir, callLog)
	out := filepath.Join(dir, "out")
	if err := os.Mkdir(out, 0o750); err != nil {
		t.Fatal(err)
	}

	derive := HLS(stub, HLSSource{VideoCodec: "av01", Height: 2160, AudioCodec: "opus"}, 1080,
		HWAccel{VAAPI: true, Device: DefaultVAAPIDevice}, nil, emptySource)
	if err := derive(t.Context(), out); err != nil {
		t.Fatalf("a failed hardware attempt must fall back, not fail: %v", err)
	}
	if got := readCalls(t, callLog); got != "vaapi\nlibx264" {
		t.Errorf("ffmpeg calls = %q, want vaapi then libx264 with a cleared directory in between", got)
	}
	if _, err := os.Stat(filepath.Join(out, "vaapi-leftover.m4s")); err == nil {
		t.Error("the abandoned hardware attempt is still in the rendition")
	}
	b, err := os.ReadFile(filepath.Join(out, HLSPlaylistName)) //nolint:gosec // test fixture path
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "vaapi-leftover") {
		t.Errorf("the playlist still names the abandoned attempt: %q", b)
	}
}

// End to end through the cache, because "no user-visible failure" is a
// statement about the job state clients are told about, not about an error
// return: hls_state must read done, not failed.
func TestHLSVAAPIFallbackEndsUpDone(t *testing.T) {
	dir := t.TempDir()
	callLog := filepath.Join(dir, "calls.log")
	stub := writeStubVAAPIFFmpeg(t, dir, callLog)

	c, err := NewCache(filepath.Join(dir, "cache"), 0, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	name := HLSName("yt-id", 1080)
	derive := HLS(stub, HLSSource{VideoCodec: "av01", Height: 2160, AudioCodec: "opus"}, 1080,
		HWAccel{VAAPI: true, Device: DefaultVAAPIDevice}, nil, emptySource)
	if st := c.StartDir(name, derive); st != StateRunning {
		t.Fatalf("StartDir = %q, want running", st)
	}
	if _, err := c.WaitDir(t.Context(), name, HLSPlaylistReady, 10*time.Second); err != nil {
		t.Fatalf("wait: %v", err)
	}
	// The playlist can be ready a moment before the job marks the entry
	// complete; the state clients see is the one that has to settle on done.
	deadline := time.Now().Add(10 * time.Second)
	for c.DirState(name) != StateDone && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if st := c.DirState(name); st != StateDone {
		t.Errorf("hls_state = %q after a hardware failure and a software retry, want done", st)
	}
	if got := readCalls(t, callLog); got != "vaapi\nlibx264" {
		t.Errorf("ffmpeg calls = %q, want vaapi then libx264", got)
	}
}

// When the software encode fails too, the source really is unusable — and that
// is the failure the operator must see, with the hardware attempt kept as
// context rather than mistaken for the cause.
func TestHLSSoftwareFailureIsTheRealFailure(t *testing.T) {
	dir := t.TempDir()
	callLog := filepath.Join(dir, "calls.log")
	stub := filepath.Join(dir, "ffmpeg-always-fails")
	script := "#!/bin/sh\n" +
		"case \"$*\" in\n" +
		"  *h264_vaapi*) echo vaapi >> " + callLog + "; echo 'hwaccel initialisation returned error' >&2 ;;\n" +
		"  *) echo libx264 >> " + callLog + "; echo 'Invalid data found when processing input' >&2 ;;\n" +
		"esac\nexit 1\n"
	if err := os.WriteFile(stub, []byte(script), 0o700); err != nil { //nolint:gosec // test fixture must be executable
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out")
	if err := os.Mkdir(out, 0o750); err != nil {
		t.Fatal(err)
	}

	derive := HLS(stub, HLSSource{VideoCodec: "av01", Height: 1080, AudioCodec: "opus"}, 1080,
		HWAccel{VAAPI: true, Device: DefaultVAAPIDevice}, nil, emptySource)
	err := derive(t.Context(), out)
	if err == nil {
		t.Fatal("a source nothing can transcode must fail the derivation")
	}
	msg := err.Error()
	if !strings.Contains(msg, "libx264 failed") {
		t.Errorf("the error should lead with the software failure: %v", err)
	}
	if !strings.Contains(msg, "Invalid data found") {
		t.Errorf("the error should carry ffmpeg's reason: %v", err)
	}
	if !strings.Contains(msg, "vaapi") {
		t.Errorf("the earlier hardware attempt should still be visible: %v", err)
	}
	if got := readCalls(t, callLog); got != "vaapi\nlibx264" {
		t.Errorf("ffmpeg calls = %q, want both attempts", got)
	}
}

// argValue returns the value ffmpeg would take for a flag, or "" if the flag
// is absent.
func argValue(args []string, flag string) string {
	i := slices.Index(args, flag)
	if i < 0 || i+1 >= len(args) {
		return ""
	}
	return args[i+1]
}

// Every height on every rung, because the command line is the whole variant:
// a wrong encoder, a missing hvc1 tag or a scale filter pinned to 1080 is a
// rendition that either does not play on Apple hardware or is not the quality
// the client picked, and no unit short of this notices.
func TestHLSArgsPerHeightAndRung(t *testing.T) {
	for _, tc := range []struct {
		height  int
		encoder string
		vaapi   string
		crf, qp string
		profile string
		hevc    bool
	}{
		{height: 2160, encoder: "libx265", vaapi: "hevc_vaapi", crf: "26", qp: "25", profile: "main", hevc: true},
		{height: 1440, encoder: "libx265", vaapi: "hevc_vaapi", crf: "26", qp: "25", profile: "main", hevc: true},
		{height: 1080, encoder: "libx264", vaapi: "h264_vaapi", crf: "23", qp: "23", profile: "high"},
		{height: 720, encoder: "libx264", vaapi: "h264_vaapi", crf: "23", qp: "23", profile: "high"},
		{height: 480, encoder: "libx264", vaapi: "h264_vaapi", crf: "23", qp: "23", profile: "high"},
	} {
		scale := "scale=-2:'min(" + strconv.Itoa(tc.height) + ",ih)'"
		vaapiScale := "scale_vaapi=w=-2:h='min(" + strconv.Itoa(tc.height) + ",ih)':format=nv12"

		// Software rung.
		sw := hlsArgs(hlsSoftwareEncoder(tc.height), "aac", tc.height)
		if got := argValue(sw, "-c:v"); got != tc.encoder {
			t.Errorf("%dp software encoder = %q, want %q", tc.height, got, tc.encoder)
		}
		if got := argValue(sw, "-vf"); got != scale {
			t.Errorf("%dp software scale = %q, want %q", tc.height, got, scale)
		}
		if got := argValue(sw, "-crf"); got != tc.crf {
			t.Errorf("%dp crf = %q, want %q", tc.height, got, tc.crf)
		}
		if got := argValue(sw, "-profile:v"); got != tc.profile {
			t.Errorf("%dp software profile = %q, want %q", tc.height, got, tc.profile)
		}
		if got := argValue(sw, "-preset"); got != "veryfast" {
			t.Errorf("%dp preset = %q", tc.height, got)
		}
		if got := argValue(sw, "-pix_fmt"); got != "yuv420p" {
			t.Errorf("%dp pix_fmt = %q, want yuv420p (8-bit, what Apple decodes)", tc.height, got)
		}

		// Hardware rung: the same rendition, built the way VAAPI needs it.
		hwArgs := hlsVAAPIArgs(DefaultVAAPIDevice, "aac", tc.height)
		if got := argValue(hwArgs, "-c:v"); got != tc.vaapi {
			t.Errorf("%dp vaapi encoder = %q, want %q", tc.height, got, tc.vaapi)
		}
		if got := argValue(hwArgs, "-vf"); got != vaapiScale {
			t.Errorf("%dp vaapi scale = %q, want %q", tc.height, got, vaapiScale)
		}
		if got := argValue(hwArgs, "-qp"); got != tc.qp {
			t.Errorf("%dp vaapi qp = %q, want %q", tc.height, got, tc.qp)
		}
		if got := argValue(hwArgs, "-profile:v"); got != tc.profile {
			t.Errorf("%dp vaapi profile = %q, want %q", tc.height, got, tc.profile)
		}
		for _, unwanted := range []string{"-preset", "-crf", "-pix_fmt", "-sc_threshold"} {
			if slices.Contains(hwArgs, unwanted) {
				t.Errorf("%dp vaapi args carry the software knob %s", tc.height, unwanted)
			}
		}

		// Copy rung: no encoder settings on either path, whatever the height.
		cp := hlsArgs("copy", "copy", tc.height)
		if got := argValue(cp, "-c:v"); got != "copy" {
			t.Errorf("%dp copy = %q", tc.height, got)
		}
		if slices.Contains(cp, "-vf") || slices.Contains(cp, "-crf") {
			t.Errorf("%dp copy args carry encoder settings: %v", tc.height, cp)
		}

		// hvc1 rather than hev1 on every HEVC rung, copied or encoded:
		// AVFoundation refuses hev1 in fMP4, which is a rendition that plays
		// nowhere it was made for.
		for name, args := range map[string][]string{"software": sw, "vaapi": hwArgs, "copy": cp} {
			tag := argValue(args, "-tag:v")
			if tc.hevc && tag != "hvc1" {
				t.Errorf("%dp %s args tag = %q, want hvc1", tc.height, name, tag)
			}
			if !tc.hevc && tag != "" {
				t.Errorf("%dp %s args carry -tag:v %q; H.264 needs none", tc.height, name, tag)
			}
		}

		// The GOP and the muxer are the same everywhere, or the segmenter
		// cannot cut where it wants to.
		for name, args := range map[string][]string{"software": sw, "vaapi": hwArgs, "copy": cp} {
			if got := argValue(args, "-hls_segment_type"); got != "fmp4" {
				t.Errorf("%dp %s segment type = %q", tc.height, name, got)
			}
			if args[len(args)-1] != HLSPlaylistName {
				t.Errorf("%dp %s args do not end in the playlist", tc.height, name)
			}
		}
		for name, args := range map[string][]string{"software": sw, "vaapi": hwArgs} {
			if got := argValue(args, "-g"); got != hlsGOP {
				t.Errorf("%dp %s gop = %q, want %s", tc.height, name, got, hlsGOP)
			}
		}
	}
}

// The HEVC rungs through the real ffmpeg. The tag is the part that cannot be
// argued about: an HEVC track written as `hev1` plays on nothing Apple makes,
// and only a muxed file says which one came out.
func TestHLSTranscodesHEVCRendition(t *testing.T) {
	requireFFmpeg(t)
	if !hasEncoder(t, "libx265") {
		t.Skip("this ffmpeg has no libx265; skipping the HEVC derivation test")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mp4")
	//nolint:gosec // G204: fixture paths from t.TempDir(), no request data
	fixture := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc=duration=4:size=320x240:rate=24",
		"-f", "lavfi", "-i", "sine=duration=4",
		"-c:v", "libx264", "-c:a", "aac", "-movflags", "+faststart", "-y", src)
	var stderr bytes.Buffer
	fixture.Stderr = &stderr
	if err := fixture.Run(); err != nil {
		t.Skipf("cannot build fixture: %v: %s", err, stderr.String())
	}

	out := filepath.Join(dir, "out")
	if err := os.Mkdir(out, 0o750); err != nil {
		t.Fatal(err)
	}
	// A 1440 rendition of a small source: the scaler clamps to the source, so
	// this stays cheap while still taking the HEVC rung.
	derive := HLS("ffmpeg", HLSSource{VideoCodec: "avc1", Height: 240, AudioCodec: "aac"}, 1440, HWAccel{}, nil,
		func(context.Context) (io.ReadCloser, error) {
			return os.Open(src) //nolint:gosec // test fixture path
		})
	if err := derive(t.Context(), out); err != nil {
		t.Fatalf("derive: %v", err)
	}
	if !HLSPlaylistReady(out) {
		t.Fatal("no usable playlist")
	}
	segments, err := filepath.Glob(filepath.Join(out, "seg*.m4s"))
	if err != nil || len(segments) == 0 {
		t.Fatalf("no segments produced: %v", err)
	}
	assertHEVCTagged(t, segments[0], filepath.Join(out, HLSInitName))
}

// hasEncoder reports whether this ffmpeg can encode with the named encoder,
// so a machine without libx265 skips rather than fails.
func hasEncoder(t *testing.T, name string) bool {
	t.Helper()
	out, err := exec.Command("ffmpeg", "-hide_banner", "-encoders").Output()
	return err == nil && bytes.Contains(out, []byte(" "+name+" "))
}

// assertHEVCTagged checks the rendition is HEVC carrying the hvc1 sample entry.
func assertHEVCTagged(t *testing.T, segment, init string) {
	t.Helper()
	probe, err := exec.LookPath("ffprobe")
	if err != nil {
		return
	}
	joined := filepath.Join(t.TempDir(), "joined.mp4")
	a, err := os.ReadFile(init) //nolint:gosec // test fixture path
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(segment) //nolint:gosec // test fixture path
	if err != nil {
		t.Fatal(err)
	}
	//nolint:gosec // G703: joined is t.TempDir() plus a literal name
	if err := os.WriteFile(joined, append(a, b...), 0o600); err != nil {
		t.Fatal(err)
	}
	//nolint:gosec // G204: probe path from LookPath, file from t.TempDir()
	out, err := exec.Command(probe, "-hide_banner", "-loglevel", "error",
		"-select_streams", "v:0", "-show_entries", "stream=codec_name,codec_tag_string",
		"-of", "csv=p=0", joined).Output()
	if err != nil {
		t.Fatalf("ffprobe: %v", err)
	}
	got := strings.ToLower(string(bytes.TrimSpace(out)))
	if !strings.Contains(got, "hevc") {
		t.Errorf("rendition is not hevc: %q", got)
	}
	if !strings.Contains(got, "hvc1") {
		t.Errorf("rendition is not tagged hvc1 (AVFoundation will refuse it): %q", got)
	}
}
