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
	"strings"
	"testing"
)

// The copy-vs-encode decision is what makes this variant affordable on an
// archive that is already H.264, and what stops it shipping a 4K x264 encode
// nobody asked for.
func TestHLSVideoCodecChoice(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  HLSSource
		want string
	}{
		{"h264 720p is copied", HLSSource{VideoCodec: "avc1", Height: 720}, "copy"},
		{"h264 with a profile suffix", HLSSource{VideoCodec: "avc1.640028", Height: 1080}, "copy"},
		{"ffprobe spelling", HLSSource{VideoCodec: "h264", Height: 1080}, "copy"},
		{"exactly the cap", HLSSource{VideoCodec: "avc1", Height: 1080}, "copy"},
		{"above the cap is scaled, so encoded", HLSSource{VideoCodec: "avc1", Height: 2160}, "libx264"},
		{"unknown height is not trusted", HLSSource{VideoCodec: "avc1"}, "libx264"},
		{"av1", HLSSource{VideoCodec: "av01.0.08M.08", Height: 1080}, "libx264"},
		{"vp9", HLSSource{VideoCodec: "vp09", Height: 720}, "libx264"},
		{"no metadata at all", HLSSource{}, "libx264"},
	} {
		if got := hlsVideoCodec(tc.src); got != tc.want {
			t.Errorf("%s: hlsVideoCodec(%+v) = %q, want %q", tc.name, tc.src, got, tc.want)
		}
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
	if got := hlsArgs("libx264", "aac"); !reflect.DeepEqual(got, want) {
		t.Errorf("encode args =\n%v\nwant\n%v", got, want)
	}
}

// A copy must carry none of the encoder settings: they would either be
// rejected or silently ignored, and either way they are a lie about what ran.
func TestHLSArgsCopyCarriesNoEncoderSettings(t *testing.T) {
	got := hlsArgs("copy", "copy")
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
	got := hlsArgs("copy", "aac")
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

	derive := HLS(stub, HLSSource{VideoCodec: "avc1", Height: 720, AudioCodec: "mp4a"}, nil, emptySource)
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

	derive := HLS(stub, HLSSource{VideoCodec: "avc1", Height: 720, AudioCodec: "mp4a"}, nil, emptySource)
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

	derive := HLS(stub, HLSSource{VideoCodec: "av01", Height: 1080, AudioCodec: "opus"}, nil, emptySource)
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

	derive := HLS(stub, HLSSource{VideoCodec: "av01", AudioCodec: "opus"}, nil, emptySource)
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
	derive := HLS("ffmpeg", HLSSource{VideoCodec: "av01", Height: 240, AudioCodec: "opus"}, nil,
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
