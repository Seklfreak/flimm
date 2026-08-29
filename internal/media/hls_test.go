package media

import (
	"bytes"
	"context"
	"fmt"
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
	} {
		if got := hlsVideoCodec(tc.src, tc.height); got != tc.want {
			t.Errorf("%s: hlsVideoCodec = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// A video offers the rungs its source can fill and nothing above them: an
// upscale is a bigger file with no more detail in it.
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

// testSrcURL stands in for the loopback source URL in argument tests. What
// matters about it is that it is a URL and carries no credential.
const testSrcURL = "http://127.0.0.1:54321/src/0123456789abcdef0123456789abcdef"

// testRun describes one pass over the grid, the way the job would.
func testRun(start, end, total int) hlsRun {
	init := HLSInitName
	if start > 0 {
		init = "init-" + fmt.Sprintf("%05d", start) + ".mp4"
	}
	return hlsRun{
		seg:      segRange{Start: start, End: end},
		total:    total,
		initName: init,
		playlist: fmt.Sprintf("run-%05d.m3u8", start),
	}
}

func TestHLSArgs(t *testing.T) {
	want := []string{
		"-hide_banner", "-loglevel", "error",
		"-i", testSrcURL,
		"-map", "0:v:0", "-map", "0:a:0",
		"-vf", "scale=-2:'min(1080,ih)'",
		"-c:v", "libx264",
		"-preset", "veryfast", "-crf", "23",
		"-profile:v", "high", "-level", "4.1", "-pix_fmt", "yuv420p",
		"-g", "96", "-keyint_min", "96", "-sc_threshold", "0",
		"-force_key_frames", "expr:gte(t,n_forced*4)",
		"-c:a", "aac", "-b:a", "160k", "-ac", "2",
		"-threads", "0",
		"-f", "hls",
		"-hls_time", "4",
		"-hls_playlist_type", "vod",
		"-hls_segment_type", "fmp4",
		"-hls_flags", "independent_segments+temp_file",
		"-hls_fmp4_init_filename", "init.mp4",
		"-hls_segment_filename", "seg%05d.m4s",
		"-start_number", "0",
		"-y", "run-00000.m3u8",
	}
	if got := hlsArgs("libx264", "aac", 1080, testSrcURL, testRun(0, 5, 5)); !reflect.DeepEqual(got, want) {
		t.Errorf("encode args =\n%v\nwant\n%v", got, want)
	}
}

// Run A: the resume-first pass. `-ss` must come *before* `-i` (an input seek,
// which over HTTP is a byte range) and the timestamps must be put back where
// the playlist says they are, or the segments decode at the wrong time.
func TestHLSArgsRunA(t *testing.T) {
	// A viewer resuming at 40:00 of a 42-minute video: segment 600 onwards.
	got := hlsArgs("libx264", "aac", 1080, testSrcURL, testRun(600, 630, 630))

	input := slices.Index(got, "-i")
	ss := slices.Index(got, "-ss")
	if ss < 0 || ss > input {
		t.Fatalf("-ss must come before -i, or the seek decodes its way there: %v", got)
	}
	if got[ss+1] != "2400" {
		t.Errorf("-ss = %q, want 2400 (600 segments × 4 s)", got[ss+1])
	}
	// -output_ts_offset is deliberately absent: with the HLS muxer it does not
	// move the segments at all, it writes an empty edit into the init segment
	// that would then misplace every *other* run's output. The offset is
	// applied to the finished segments instead, which is why they are written
	// under a name the route will not serve until it has been.
	if slices.Contains(got, "-output_ts_offset") {
		t.Errorf("run A carries -output_ts_offset: %v", got)
	}
	if v := argValue(got, "-hls_segment_filename"); v != "seg%05d.m4s.raw" {
		t.Errorf("run A segment pattern = %q, want the unpublished one", v)
	}
	if v := argValue(got, "-start_number"); v != "600" {
		t.Errorf("-start_number = %q, want 600", v)
	}
	// It runs to the end of the video, so there is nothing to cut short.
	if slices.Contains(got, "-t") {
		t.Errorf("a run to the end of the video must carry no -t: %v", got)
	}
	if got[len(got)-1] != "run-00600.m3u8" {
		t.Errorf("run playlist = %q", got[len(got)-1])
	}
	if v := argValue(got, "-hls_fmp4_init_filename"); v != "init-00600.mp4" {
		t.Errorf("init file = %q; a later run must not overwrite init.mp4 under a player", v)
	}
}

// Run B: the part before the resume point, cut off exactly at it so it meets
// run A on a segment boundary.
func TestHLSArgsRunB(t *testing.T) {
	got := hlsArgs("libx264", "aac", 1080, testSrcURL, testRun(0, 600, 630))

	if slices.Contains(got, "-ss") {
		t.Errorf("a run from the start needs no seek: %v", got)
	}
	if v := argValue(got, "-t"); v != "2400" {
		t.Errorf("-t = %q, want 2400 (600 segments × 4 s)", v)
	}
	if v := argValue(got, "-hls_segment_filename"); v != "seg%05d.m4s" {
		t.Errorf("a run from the start writes segments directly: %q", v)
	}
	if v := argValue(got, "-start_number"); v != "0" {
		t.Errorf("-start_number = %q, want 0", v)
	}
	if v := argValue(got, "-hls_fmp4_init_filename"); v != HLSInitName {
		t.Errorf("the first run writes %q, want %q", v, HLSInitName)
	}
}

// Every run cuts on the same 4 s grid, or two runs of the same video disagree
// about where segment boundaries are and the stitched rendition is unplayable.
func TestHLSForcesKeyFramesOnTheGrid(t *testing.T) {
	const want = "expr:gte(t,n_forced*4)"
	for name, args := range map[string][]string{
		"software h264": hlsArgs("libx264", "aac", 1080, testSrcURL, testRun(0, 5, 5)),
		"software hevc": hlsArgs("libx265", "aac", 2160, testSrcURL, testRun(0, 5, 5)),
		"vaapi h264":    hlsVAAPIArgs(DefaultVAAPIDevice, "aac", 1080, testSrcURL, testRun(0, 5, 5)),
		"vaapi hevc":    hlsVAAPIArgs(DefaultVAAPIDevice, "aac", 2160, testSrcURL, testRun(0, 5, 5)),
	} {
		if got := argValue(args, "-force_key_frames"); got != want {
			t.Errorf("%s: -force_key_frames = %q, want %q", name, got, want)
		}
	}
	// A stream copy has no encoder to force anything on; it is also the one
	// rung that always runs over the whole video.
	if cp := hlsArgs("copy", "copy", 1080, testSrcURL, testRun(0, 5, 5)); slices.Contains(cp, "-force_key_frames") {
		t.Errorf("a copy cannot force keyframes: %v", cp)
	}
}

// A copy must carry none of the encoder settings: they would either be
// rejected or silently ignored, and either way they are a lie about what ran.
func TestHLSArgsCopyCarriesNoEncoderSettings(t *testing.T) {
	got := hlsArgs("copy", "copy", 1080, testSrcURL, testRun(0, 5, 5))
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
	if !strings.HasPrefix(got[len(got)-1], "run-") {
		t.Errorf("copy args do not end in a run playlist: %v", got)
	}
}

// Mixing one copied and one encoded track is the common case (H.264 video with
// Opus audio), so it must not leak the other track's settings.
func TestHLSArgsMixedCopyAndEncode(t *testing.T) {
	got := hlsArgs("copy", "aac", 1080, testSrcURL, testRun(0, 5, 5))
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

// A *video* copy can only ever produce the whole rendition: a stream copy cuts
// on the source's own keyframes, not on the 4 s grid, so a partial range would
// produce segments the playlist does not describe.
func TestAVideoCopyIsASingleRun(t *testing.T) {
	attempts := hlsAttempts(HLSSource{VideoCodec: "avc1", Height: 720, AudioCodec: "mp4a"}, 720,
		HWAccel{VAAPI: true, Device: DefaultVAAPIDevice})
	for _, a := range attempts {
		if (a.name == "copy") != a.singleRun {
			t.Errorf("attempt %q: singleRun = %v", a.name, a.singleRun)
		}
	}
}

// ...but copying only the audio does not make it one. The video is still being
// encoded, onto the same grid, so the run starts where the planner asked —
// which for a resumed video is the part the viewer is waiting for. Treating
// this as a single run encoded the whole video from zero first.
func TestAnAudioOnlyCopyStillHonoursThePlan(t *testing.T) {
	// VP9 at 720p: the video has to be encoded, the AAC audio can be copied.
	attempts := hlsAttempts(HLSSource{VideoCodec: "vp09", Height: 720, AudioCodec: "mp4a"}, 720, HWAccel{})
	if len(attempts) == 0 || attempts[0].name != "copy" {
		t.Fatalf("attempts = %+v, want the copy rung first", attempts)
	}
	if attempts[0].audioCodec != "copy" || attempts[0].videoCodec == "copy" {
		t.Fatalf("attempt = %+v, want an audio-only copy", attempts[0])
	}
	if attempts[0].singleRun {
		t.Error("an audio-only copy must not force a pass over the whole video")
	}
}

func TestHLSPlaylistReady(t *testing.T) {
	dir := t.TempDir()
	if HLSPlaylistReady(dir) {
		t.Error("an empty directory is not ready")
	}
	playlist := filepath.Join(dir, HLSPlaylistName)
	// A header with no segments: handing this to a player is what the wait
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

// Nothing should ever put a credential in ffmpeg's stderr — the loopback source
// holds the token and hands ffmpeg a nonce — but a log line is not a place to
// find out we were wrong.
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

// stubOptions shapes the fake ffmpeg the derivation tests run.
type stubOptions struct {
	// total is how many segments the rendition has, so a run with no -t knows
	// where to stop.
	total int
	// failCopy makes the copy attempt fail, as an unmuxable source would.
	failCopy bool
	// failVAAPI makes the hardware attempt fail after publishing part of a
	// rendition, as a GPU that cannot decode a source does.
	failVAAPI bool
	// segmentDelay sleeps between segments, so a test can catch a run in
	// progress.
	segmentDelay string
}

// writeStubHLSFFmpeg installs a script that stands in for ffmpeg: it reads the
// run's -start_number, -t, -hls_fmp4_init_filename and -hls_segment_filename
// off its own command line and writes exactly the segments that run would
// produce, logging what it was asked to do. What it writes is minimal but real
// fMP4, so the timeline rebase runs for real too — everything about the two-run
// scheme except the encoding itself is exercised without a transcode.
func writeStubHLSFFmpeg(t *testing.T, dir, callLog string, opt stubOptions) string {
	t.Helper()
	path := filepath.Join(dir, "ffmpeg-stub")
	initFixture := filepath.Join(dir, "fixture-init.mp4")
	segFixture := filepath.Join(dir, "fixture-seg.m4s")
	if err := os.WriteFile(initFixture, fakeInitSegment(map[uint32]uint32{1: 12288}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(segFixture, fakeSegment([]uint32{1}, 12288), 0o600); err != nil {
		t.Fatal(err)
	}
	delay := ""
	if opt.segmentDelay != "" {
		delay = "  sleep " + opt.segmentDelay + "\n"
	}
	script := "#!/bin/sh\n" +
		"TOTAL=" + strconv.Itoa(opt.total) + "\n" +
		"case \"$*\" in\n" +
		"  *h264_vaapi*|*hevc_vaapi*) codec=vaapi ;;\n" +
		"  *libx264*) codec=libx264 ;;\n" +
		"  *libx265*) codec=libx265 ;;\n" +
		"  *) codec=copy ;;\n" +
		"esac\n" +
		"start=0; dur=0; init=" + HLSInitName + "; pat='seg%05d.m4s'; out=\n" +
		"prev=\n" +
		"for a in \"$@\"; do\n" +
		"  case \"$prev\" in\n" +
		"    -start_number) start=$a ;;\n" +
		"    -t) dur=$a ;;\n" +
		"    -hls_fmp4_init_filename) init=$a ;;\n" +
		"    -hls_segment_filename) pat=$a ;;\n" +
		"  esac\n" +
		"  prev=$a; out=$a\n" +
		"done\n" +
		"echo \"$codec $start $dur\" >> " + callLog + "\n" +
		"printf '%s\\n' \"$*\" >> " + callLog + ".argv\n"
	if opt.failCopy {
		script += "[ \"$codec\" = copy ] && { echo 'could not mux' >&2; exit 1; }\n"
	}
	if opt.failVAAPI {
		// A hardware attempt gets far enough to publish a segment before the
		// decoder gives up on a 10-bit frame.
		script += "if [ \"$codec\" = vaapi ]; then\n" +
			"  : > vaapi-leftover.m4s\n" +
			"  cp " + segFixture + " \"$(printf \"$pat\" \"$start\")\"\n" +
			"  echo 'Failed setup for format vaapi: hwaccel initialisation returned error' >&2\n" +
			"  exit 1\n" +
			"fi\n"
	}
	script += "cp " + initFixture + " \"$init\"\n" +
		"printf '#EXTM3U\\n' > \"$out\"\n" +
		"if [ \"$dur\" -gt 0 ]; then n=$((dur/4)); else n=$((TOTAL-start)); fi\n" +
		"i=0\n" +
		"while [ $i -lt $n ]; do\n" +
		delay +
		"  seg=$(printf \"$pat\" $((start+i)))\n" +
		"  cp " + segFixture + " \"$seg\"\n" +
		"  printf '#EXTINF:4.000000,\\n%s\\n' \"$seg\" >> \"$out\"\n" +
		"  i=$((i+1))\n" +
		"done\n" +
		"printf '#EXT-X-ENDLIST\\n' >> \"$out\"\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil { //nolint:gosec // test fixture must be executable
		t.Fatal(err)
	}
	return path
}

// testSource serves body over the loopback source, honouring Range the way TA
// does.
func testSource(body []byte) RangeSourceFunc {
	return func(_ context.Context, rangeHeader string) (*SourceStream, error) {
		total := int64(len(body))
		if rangeHeader == "" {
			return &SourceStream{
				Body:          io.NopCloser(bytes.NewReader(body)),
				StatusCode:    200,
				ContentLength: total,
				AcceptRanges:  "bytes",
			}, nil
		}
		start, end, err := parseTestRange(rangeHeader, total)
		if err != nil {
			return nil, err
		}
		return &SourceStream{
			Body:          io.NopCloser(bytes.NewReader(body[start : end+1])),
			StatusCode:    206,
			ContentLength: end - start + 1,
			ContentRange:  fmt.Sprintf("bytes %d-%d/%d", start, end, total),
			AcceptRanges:  "bytes",
		}, nil
	}
}

func parseTestRange(header string, total int64) (int64, int64, error) {
	spec, ok := strings.CutPrefix(header, "bytes=")
	if !ok {
		return 0, 0, fmt.Errorf("bad range %q", header)
	}
	from, to, _ := strings.Cut(spec, "-")
	start, err := strconv.ParseInt(from, 10, 64)
	if err != nil || start >= total {
		return 0, 0, fmt.Errorf("bad range %q", header)
	}
	end := total - 1
	if to != "" {
		if end, err = strconv.ParseInt(to, 10, 64); err != nil {
			return 0, 0, fmt.Errorf("bad range %q", header)
		}
		end = min(end, total-1)
	}
	return start, end, nil
}

var emptySource = testSource([]byte("source"))

// deriveHLS runs a whole job into a fresh directory, the way the cache would.
func deriveHLS(t *testing.T, cfg HLSConfig, dir string) error {
	t.Helper()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	prepare, derive := HLS(cfg)
	if err := prepare(t.Context(), dir); err != nil {
		return err
	}
	return derive(t.Context(), dir)
}

// A source TA calls H.264/AAC takes the copy path — the whole reason a
// compatible archive costs almost nothing here.
func TestHLSCopiesCompatibleSource(t *testing.T) {
	dir := t.TempDir()
	callLog := filepath.Join(dir, "calls.log")
	stub := writeStubHLSFFmpeg(t, dir, callLog, stubOptions{total: 3})

	err := deriveHLS(t, HLSConfig{
		FFmpegPath: stub,
		Source:     HLSSource{VideoCodec: "avc1", Height: 720, AudioCodec: "mp4a", Duration: 12},
		Height:     720,
		Open:       emptySource,
	}, filepath.Join(dir, "out"))
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	if got := readCalls(t, callLog); got != "copy 0 0" {
		t.Errorf("ffmpeg calls = %q, want a single copy call over the whole video", got)
	}
	out := filepath.Join(dir, "out")
	for _, name := range []string{HLSPlaylistName, HLSInitName, "seg00000.m4s", "seg00002.m4s"} {
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
	stub := writeStubHLSFFmpeg(t, dir, callLog, stubOptions{total: 3, failCopy: true})

	err := deriveHLS(t, HLSConfig{
		FFmpegPath: stub,
		Source:     HLSSource{VideoCodec: "avc1", Height: 720, AudioCodec: "mp4a", Duration: 12},
		Height:     720,
		Open:       emptySource,
	}, filepath.Join(dir, "out"))
	if err != nil {
		t.Fatalf("a failed copy should fall back to encoding, got: %v", err)
	}
	if got := readCalls(t, callLog); got != "copy 0 0\nlibx264 0 0" {
		t.Errorf("ffmpeg calls = %q, want copy then libx264", got)
	}
}

// An incompatible source is encoded straight away: no pointless copy attempt
// whose only result is a wasted pass over a multi-gigabyte file.
func TestHLSSkipsCopyForIncompatibleSource(t *testing.T) {
	dir := t.TempDir()
	callLog := filepath.Join(dir, "calls.log")
	stub := writeStubHLSFFmpeg(t, dir, callLog, stubOptions{total: 2})

	err := deriveHLS(t, HLSConfig{
		FFmpegPath: stub,
		Source:     HLSSource{VideoCodec: "av01", Height: 1080, AudioCodec: "opus", Duration: 8},
		Height:     1080,
		Open:       emptySource,
	}, filepath.Join(dir, "out"))
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	if got := readCalls(t, callLog); got != "libx264 0 0" {
		t.Errorf("ffmpeg calls = %q, want a single libx264 call", got)
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
	body := buildFixture(t, dir, 6)

	out := filepath.Join(dir, "out")
	// Claim AV1 so the encode path runs, which is the case that matters.
	err := deriveHLS(t, HLSConfig{
		FFmpegPath: "ffmpeg",
		Source:     HLSSource{VideoCodec: "av01", Height: 240, AudioCodec: "opus", Duration: 6},
		Height:     480,
		Open:       testSource(body),
	}, out)
	if err != nil {
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
	// Every name in the playlist must match what the route will serve, and
	// every segment the playlist names must exist by the time the job is done.
	for _, seg := range segments {
		if !strings.Contains(playlist, filepath.Base(seg)) {
			t.Errorf("segment %s is not in the playlist", filepath.Base(seg))
		}
	}
	for _, line := range strings.Split(playlist, "\n") {
		name := strings.TrimSpace(line)
		if !strings.HasSuffix(name, ".m4s") {
			continue
		}
		st, err := os.Stat(filepath.Join(out, name)) //nolint:gosec // test fixture path
		if err != nil || st.Size() == 0 {
			t.Errorf("the finished playlist names %s, which is not on disk", name)
		}
	}
	assertH264AAC(t, segments[0], filepath.Join(out, HLSInitName))
}

// buildFixture writes a short, real video and returns its bytes.
func buildFixture(t *testing.T, dir string, seconds int) []byte {
	t.Helper()
	src := filepath.Join(dir, "src.mp4")
	d := strconv.Itoa(seconds)
	//nolint:gosec // G204: fixture paths from t.TempDir(), no request data
	fixture := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc=duration="+d+":size=320x240:rate=24",
		"-f", "lavfi", "-i", "sine=duration="+d,
		"-c:v", "libx264", "-c:a", "aac", "-movflags", "+faststart", "-y", src)
	var stderr bytes.Buffer
	fixture.Stderr = &stderr
	if err := fixture.Run(); err != nil {
		t.Skipf("cannot build fixture: %v: %s", err, stderr.String())
	}
	b, err := os.ReadFile(src) //nolint:gosec // test fixture path
	if err != nil {
		t.Fatal(err)
	}
	return b
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
		"-i", testSrcURL,
		"-map", "0:v:0", "-map", "0:a:0",
		"-vf", "scale_vaapi=w=-2:h='min(1080,ih)':format=nv12",
		"-c:v", "h264_vaapi",
		"-rc_mode", "CQP", "-qp", "23",
		"-profile:v", "high", "-level", "4.1",
		"-g", "96", "-keyint_min", "96",
		"-force_key_frames", "expr:gte(t,n_forced*4)",
		"-c:a", "aac", "-b:a", "160k", "-ac", "2",
		"-threads", "0",
		"-f", "hls",
		"-hls_time", "4",
		"-hls_playlist_type", "vod",
		"-hls_segment_type", "fmp4",
		"-hls_flags", "independent_segments+temp_file",
		"-hls_fmp4_init_filename", "init.mp4",
		"-hls_segment_filename", "seg%05d.m4s",
		"-start_number", "0",
		"-y", "run-00000.m3u8",
	}
	if got := hlsVAAPIArgs(DefaultVAAPIDevice, "aac", 1080, testSrcURL, testRun(0, 5, 5)); !reflect.DeepEqual(got, want) {
		t.Errorf("vaapi args =\n%v\nwant\n%v", got, want)
	}
}

func TestHLSVAAPIArgsShape(t *testing.T) {
	got := hlsVAAPIArgs("/dev/dri/renderD129", "aac", 1080, testSrcURL, testRun(600, 630, 630))

	// -hwaccel* configure the *input*; after -i they would be ignored and the
	// decode would silently run on the CPU, which is the failure mode this
	// whole change exists to avoid. -ss has to be there too, for the same
	// reason it does on the software path.
	input := slices.Index(got, "-i")
	for _, flag := range []string{"-hwaccel", "-hwaccel_device", "-hwaccel_output_format", "-ss"} {
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
	if got[len(got)-1] != "run-00600.m3u8" {
		t.Errorf("vaapi args do not end in the run playlist: %v", got)
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

// A GPU that cannot manage a source is not the viewer's problem: the transcode
// falls back to the CPU and the request succeeds. What must not happen is the
// half-written hardware attempt surviving into the software one — the playlist
// would then name segments nothing wrote, and the two encoders' bitstreams
// would sit under one init segment.
func TestHLSFallsBackFromVAAPIToSoftware(t *testing.T) {
	dir := t.TempDir()
	callLog := filepath.Join(dir, "calls.log")
	stub := writeStubHLSFFmpeg(t, dir, callLog, stubOptions{total: 3, failVAAPI: true})
	out := filepath.Join(dir, "out")

	err := deriveHLS(t, HLSConfig{
		FFmpegPath: stub,
		Source:     HLSSource{VideoCodec: "av01", Height: 2160, AudioCodec: "opus", Duration: 12},
		Height:     1080,
		HW:         HWAccel{VAAPI: true, Device: DefaultVAAPIDevice},
		Open:       emptySource,
	}, out)
	if err != nil {
		t.Fatalf("a failed hardware attempt must fall back, not fail: %v", err)
	}
	if got := readCalls(t, callLog); got != "vaapi 0 0\nlibx264 0 0" {
		t.Errorf("ffmpeg calls = %q, want vaapi then libx264 with a cleared directory in between", got)
	}
	if _, err := os.Stat(filepath.Join(out, "vaapi-leftover.m4s")); err == nil {
		t.Error("the abandoned hardware attempt is still in the rendition")
	}
}

// End to end through the cache, because "no user-visible failure" is a
// statement about the job state clients are told about, not about an error
// return: hls_state must read done, not failed.
func TestHLSVAAPIFallbackEndsUpDone(t *testing.T) {
	dir := t.TempDir()
	callLog := filepath.Join(dir, "calls.log")
	stub := writeStubHLSFFmpeg(t, dir, callLog, stubOptions{total: 3, failVAAPI: true})

	c, err := NewCache(filepath.Join(dir, "cache"), 0, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	name := HLSName("yt-id", 1080)
	prepare, derive := HLS(HLSConfig{
		FFmpegPath: stub,
		Source:     HLSSource{VideoCodec: "av01", Height: 2160, AudioCodec: "opus", Duration: 12},
		Height:     1080,
		HW:         HWAccel{VAAPI: true, Device: DefaultVAAPIDevice},
		Open:       emptySource,
	})
	if st := c.StartDirJob(name, prepare, derive); st != StateRunning {
		t.Fatalf("StartDirJob = %q, want running", st)
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
	if got := readCalls(t, callLog); got != "vaapi 0 0\nlibx264 0 0" {
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

	err := deriveHLS(t, HLSConfig{
		FFmpegPath: stub,
		Source:     HLSSource{VideoCodec: "av01", Height: 1080, AudioCodec: "opus", Duration: 8},
		Height:     1080,
		HW:         HWAccel{VAAPI: true, Device: DefaultVAAPIDevice},
		Open:       emptySource,
	}, filepath.Join(dir, "out"))
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
	run := testRun(0, 5, 5)
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
		sw := hlsArgs(hlsSoftwareEncoder(tc.height), "aac", tc.height, testSrcURL, run)
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
		hwArgs := hlsVAAPIArgs(DefaultVAAPIDevice, "aac", tc.height, testSrcURL, run)
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
		cp := hlsArgs("copy", "copy", tc.height, testSrcURL, run)
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
			if !strings.HasPrefix(args[len(args)-1], "run-") {
				t.Errorf("%dp %s args do not end in a run playlist", tc.height, name)
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
	body := buildFixture(t, dir, 4)

	out := filepath.Join(dir, "out")
	// A 1440 rendition of a small source: the scaler clamps to the source, so
	// this stays cheap while still taking the HEVC rung.
	err := deriveHLS(t, HLSConfig{
		FFmpegPath: "ffmpeg",
		Source:     HLSSource{VideoCodec: "avc1", Height: 240, AudioCodec: "aac", Duration: 4},
		Height:     1440,
		Open:       testSource(body),
	}, out)
	if err != nil {
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
