package media

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// swConfig is a job that always takes the software rung, so the tests below are
// about the runs and not about the ladder.
func swConfig(stub string, duration float64, from float64) HLSConfig {
	return HLSConfig{
		FFmpegPath: stub,
		Source:     HLSSource{VideoCodec: "av01", Height: 1080, AudioCodec: "opus", Duration: duration},
		Height:     1080,
		From:       from,
		Open:       emptySource,
	}
}

func readSegments(t *testing.T, dir string) []int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []int
	for _, e := range entries {
		if i := HLSSegmentIndex(e.Name()); i >= 0 {
			out = append(out, i)
		}
	}
	return out
}

// The point of the whole change: a viewer resuming at 20 s of a 40 s video gets
// the segment they are on encoded *first*, and the part before it afterwards.
func TestHLSResumeEncodesTheResumePointFirst(t *testing.T) {
	dir := t.TempDir()
	callLog := filepath.Join(dir, "calls.log")
	stub := writeStubHLSFFmpeg(t, dir, callLog, stubOptions{total: 10})
	out := filepath.Join(dir, "out")

	if err := deriveHLS(t, swConfig(stub, 40, 20), out); err != nil {
		t.Fatalf("derive: %v", err)
	}
	// Run A covers [5,10) — from the resume point to the end, no -t needed.
	// Run B covers [0,5) — 20 seconds, cut off exactly where run A begins.
	if got := readCalls(t, callLog); got != "libx264 5 0\nlibx264 0 20" {
		t.Errorf("ffmpeg calls =\n%s\nwant run A from segment 5 then run B for the first 20 s", got)
	}
	if got := len(readSegments(t, out)); got != 10 {
		t.Errorf("the finished rendition has %d segments, want 10", got)
	}
}

// Without a resume position nothing changes: one run from the start, as before.
func TestHLSWithoutFromIsASingleRun(t *testing.T) {
	dir := t.TempDir()
	callLog := filepath.Join(dir, "calls.log")
	stub := writeStubHLSFFmpeg(t, dir, callLog, stubOptions{total: 10})

	if err := deriveHLS(t, swConfig(stub, 40, 0), filepath.Join(dir, "out")); err != nil {
		t.Fatalf("derive: %v", err)
	}
	if got := readCalls(t, callLog); got != "libx264 0 0" {
		t.Errorf("ffmpeg calls = %q, want one run from the start", got)
	}
}

// A resume position inside the first segment is the beginning, so it is still
// one run — not a zero-length run B.
func TestHLSResumeInsideTheFirstSegmentIsASingleRun(t *testing.T) {
	dir := t.TempDir()
	callLog := filepath.Join(dir, "calls.log")
	stub := writeStubHLSFFmpeg(t, dir, callLog, stubOptions{total: 10})

	if err := deriveHLS(t, swConfig(stub, 40, 3), filepath.Join(dir, "out")); err != nil {
		t.Fatalf("derive: %v", err)
	}
	if got := readCalls(t, callLog); got != "libx264 0 0" {
		t.Errorf("ffmpeg calls = %q, want one run from the start", got)
	}
}

// The playlist is complete before a single frame is encoded — that is what lets
// a player seek to the resume position instead of starting at 0:00.
func TestHLSPlaylistIsCompleteBeforeEncoding(t *testing.T) {
	dir := t.TempDir()
	// A stub that never returns: preparation must not depend on it.
	stub := filepath.Join(dir, "ffmpeg-hang")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\nsleep 30\n"), 0o700); err != nil { //nolint:gosec // test fixture must be executable
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out")
	if err := os.MkdirAll(out, 0o750); err != nil {
		t.Fatal(err)
	}

	prepare, _ := HLS(swConfig(stub, 40, 20))
	if err := prepare(t.Context(), out); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if !HLSPlaylistReady(out) {
		t.Fatal("the playlist is not servable before the transcode starts")
	}
	b, err := os.ReadFile(filepath.Join(out, HLSPlaylistName)) //nolint:gosec // test fixture path
	if err != nil {
		t.Fatal(err)
	}
	playlist := string(b)
	if n := strings.Count(playlist, ".m4s"); n != 10 {
		t.Errorf("playlist names %d segments before encoding, want all 10:\n%s", n, playlist)
	}
	if !strings.Contains(playlist, "#EXT-X-PLAYLIST-TYPE:VOD") || !strings.Contains(playlist, "#EXT-X-ENDLIST") {
		t.Errorf("the playlist is not a complete VOD list:\n%s", playlist)
	}
	// Nothing has been encoded yet, so a player seeking to 20 s asks for a
	// segment that does not exist — which is what the segment wait is for.
	if segs := readSegments(t, out); len(segs) != 0 {
		t.Errorf("preparation encoded something: %v", segs)
	}
}

// A seek far ahead of the encoder re-aims the run rather than making the viewer
// wait out everything in between. What is already produced stays.
func TestHLSSeekAheadReaimsTheRun(t *testing.T) {
	dir := t.TempDir()
	callLog := filepath.Join(dir, "calls.log")
	stub := writeStubHLSFFmpeg(t, dir, callLog, stubOptions{total: 30, segmentDelay: "0.1"})
	out := filepath.Join(dir, "out")
	if err := os.MkdirAll(out, 0o750); err != nil {
		t.Fatal(err)
	}

	reg := NewHLSRegistry()
	cfg := swConfig(stub, 120, 0)
	cfg.Registry = reg
	cfg.SeekAheadSegments = 5
	prepare, derive := HLS(cfg)
	if err := prepare(t.Context(), out); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { done <- derive(t.Context(), out) }()

	// Wait for the first run to get going, then jump to the far end.
	job := reg.Get(filepath.Base(out))
	if job == nil {
		t.Fatal("the job was not published for a segment request to find")
	}
	waitFor(t, 5*time.Second, func() bool { return job.Progress() > 0 })
	job.Request(25)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("derive: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the job never finished")
	}

	calls := strings.Split(readCalls(t, callLog), "\n")
	if len(calls) < 3 {
		t.Fatalf("ffmpeg calls =\n%s\nwant the first run cut short and re-aimed", strings.Join(calls, "\n"))
	}
	if !strings.HasPrefix(calls[0], "libx264 0 ") {
		t.Errorf("the first run did not start at the beginning: %q", calls[0])
	}
	if !strings.HasPrefix(calls[1], "libx264 25 ") {
		t.Errorf("the run was not re-aimed at the requested segment: %q", calls[1])
	}
	// Nothing already encoded was thrown away, and the rendition still ends up
	// whole.
	if got := len(readSegments(t, out)); got != 30 {
		t.Errorf("the finished rendition has %d segments, want 30", got)
	}
	if p := job.Progress(); p != 1 {
		t.Errorf("progress = %v at the end, want 1", p)
	}
}

// A request only a little ahead of the encoder is read-ahead, not a seek:
// restarting for it would encode nothing at all.
func TestHLSNearbyRequestDoesNotReaimTheRun(t *testing.T) {
	dir := t.TempDir()
	callLog := filepath.Join(dir, "calls.log")
	stub := writeStubHLSFFmpeg(t, dir, callLog, stubOptions{total: 12, segmentDelay: "0.1"})
	out := filepath.Join(dir, "out")
	if err := os.MkdirAll(out, 0o750); err != nil {
		t.Fatal(err)
	}

	reg := NewHLSRegistry()
	cfg := swConfig(stub, 48, 0)
	cfg.Registry = reg
	cfg.SeekAheadSegments = 30
	prepare, derive := HLS(cfg)
	if err := prepare(t.Context(), out); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- derive(t.Context(), out) }()

	job := reg.Get(filepath.Base(out))
	waitFor(t, 5*time.Second, func() bool { return job.Progress() > 0 })
	job.Request(4)

	if err := <-done; err != nil {
		t.Fatalf("derive: %v", err)
	}
	if got := readCalls(t, callLog); got != "libx264 0 0" {
		t.Errorf("ffmpeg calls = %q; a read-ahead request restarted the run", got)
	}
}

// A process killed mid-job leaves a directory with some segments in it. The
// next job picks up from there rather than encoding it all over again.
func TestHLSResumesAPartialDirectoryAfterARestart(t *testing.T) {
	dir := t.TempDir()
	callLog := filepath.Join(dir, "calls.log")
	stub := writeStubHLSFFmpeg(t, dir, callLog, stubOptions{total: 10})
	out := filepath.Join(dir, "out")
	if err := os.MkdirAll(out, 0o750); err != nil {
		t.Fatal(err)
	}
	// What the killed process left: five segments, the init header, and the
	// note saying which encoder wrote them.
	for i := range 5 {
		if err := os.WriteFile(filepath.Join(out, HLSSegmentName(i)), []byte("payload"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range map[string]string{
		HLSInitName:      "header",
		hlsEncoderMarker: "libx264",
		// An in-progress segment the muxer was still holding: not a segment.
		"seg00005.m4s.tmp": "half",
	} {
		if err := os.WriteFile(filepath.Join(out, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	prepare, derive := HLS(swConfig(stub, 40, 0))
	if err := prepare(t.Context(), out); err != nil {
		t.Fatal(err)
	}
	if err := derive(t.Context(), out); err != nil {
		t.Fatalf("derive: %v", err)
	}
	// Only the gap is encoded, and the ladder is not walked again: the marker
	// says which rung wrote what is already there.
	if got := readCalls(t, callLog); got != "libx264 5 0" {
		t.Errorf("ffmpeg calls = %q, want one run filling segments 5-9", got)
	}
	if got := len(readSegments(t, out)); got != 10 {
		t.Errorf("the finished rendition has %d segments, want 10", got)
	}
	if _, err := os.Stat(filepath.Join(out, "seg00005.m4s.tmp")); err == nil {
		t.Error("the muxer's in-progress temp file survived into the rendition")
	}
}

// Segments of unknown provenance are not resumed: two encoders' bitstreams
// under one init segment decode to garbage, so the entry starts over.
func TestHLSDoesNotResumeSegmentsFromAnotherEncoder(t *testing.T) {
	dir := t.TempDir()
	callLog := filepath.Join(dir, "calls.log")
	stub := writeStubHLSFFmpeg(t, dir, callLog, stubOptions{total: 6})
	out := filepath.Join(dir, "out")
	if err := os.MkdirAll(out, 0o750); err != nil {
		t.Fatal(err)
	}
	for i := range 3 {
		if err := os.WriteFile(filepath.Join(out, HLSSegmentName(i)), []byte("payload"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// The GPU that wrote these is not on this ladder any more.
	if err := os.WriteFile(filepath.Join(out, hlsEncoderMarker), []byte("vaapi"), 0o600); err != nil {
		t.Fatal(err)
	}

	prepare, derive := HLS(swConfig(stub, 24, 0))
	if err := prepare(t.Context(), out); err != nil {
		t.Fatal(err)
	}
	if err := derive(t.Context(), out); err != nil {
		t.Fatalf("derive: %v", err)
	}
	if got := readCalls(t, callLog); got != "libx264 0 0" {
		t.Errorf("ffmpeg calls = %q, want the whole rendition encoded again", got)
	}
	if got := readEncoderMarker(t, out); got != "libx264" {
		t.Errorf("encoder marker = %q, want libx264", got)
	}
}

func readEncoderMarker(t *testing.T, dir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, hlsEncoderMarker)) //nolint:gosec // test fixture path
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

// A later run must not overwrite init.mp4: a player may be part-way through
// downloading it, and a truncated init segment is a dead stream. The duplicate
// is written elsewhere and removed.
func TestHLSLaterRunsDoNotOverwriteTheInitSegment(t *testing.T) {
	dir := t.TempDir()
	callLog := filepath.Join(dir, "calls.log")
	stub := writeStubHLSFFmpeg(t, dir, callLog, stubOptions{total: 10})
	out := filepath.Join(dir, "out")

	if err := deriveHLS(t, swConfig(stub, 40, 20), out); err != nil {
		t.Fatalf("derive: %v", err)
	}
	// Run A wrote init.mp4; run B wrote init-00000.mp4, which is gone again.
	entries, err := os.ReadDir(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "init-") {
			t.Errorf("a duplicate init segment was left behind: %s", e.Name())
		}
	}
	if st, err := os.Stat(filepath.Join(out, HLSInitName)); err != nil || st.Size() == 0 {
		t.Errorf("init.mp4 missing or empty: %v", err)
	}
}

// The durations in the finished playlist are the ones ffmpeg really produced,
// read back out of the per-run playlists.
func TestHLSFinishedPlaylistCarriesRealDurations(t *testing.T) {
	dir := t.TempDir()
	stub := writeStubHLSFFmpeg(t, dir, filepath.Join(dir, "calls.log"), stubOptions{total: 3})
	out := filepath.Join(dir, "out")

	if err := deriveHLS(t, swConfig(stub, 12, 0), out); err != nil {
		t.Fatalf("derive: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(out, HLSPlaylistName)) //nolint:gosec // test fixture path
	if err != nil {
		t.Fatal(err)
	}
	// The stub reports 4.000000 per segment, which is what should come back.
	if n := strings.Count(string(b), "#EXTINF:4.000,"); n != 3 {
		t.Errorf("playlist does not carry the run's durations:\n%s", b)
	}
}

// The one that matters, through the real ffmpeg: two runs, one rendition, and a
// player has to see a single continuous twenty seconds.
//
// This is where the timeline rebase earns its keep. ffmpeg's HLS muxer numbers
// every run's fragments from zero, so without it the resumed run's segments sit
// on top of the other run's and the rendition decodes as eight seconds of
// overlapping video — which is exactly what this test caught.
func TestHLSResumeProducesOneContinuousTimeline(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	body := buildFixture(t, dir, 20)
	out := filepath.Join(dir, "out")

	// Resuming at 12 s: run A covers segments 3-4, run B covers 0-2.
	err := deriveHLS(t, HLSConfig{
		FFmpegPath: "ffmpeg",
		Source:     HLSSource{VideoCodec: "av01", Height: 240, AudioCodec: "opus", Duration: 20},
		Height:     480,
		From:       12,
		Open:       testSource(body),
	}, out)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	for i := range 5 {
		if st, statErr := os.Stat(filepath.Join(out, HLSSegmentName(i))); statErr != nil || st.Size() == 0 {
			t.Fatalf("segment %d missing: %v", i, statErr)
		}
	}
	// Nothing unpublished may be left lying about.
	entries, err := os.ReadDir(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), hlsRawSuffix) {
			t.Errorf("an unrebased segment survived: %s", e.Name())
		}
	}

	playlist := filepath.Join(out, HLSPlaylistName)
	if got := probeFloat(t, playlist, "format=duration"); got < 19.5 || got > 20.5 {
		t.Errorf("the rendition is %.3fs long, want about 20", got)
	}
	first, last, frames := probeFrameSpan(t, playlist)
	if frames < 460 {
		t.Errorf("the rendition decodes %d video frames, want about 480 — the runs overlap", frames)
	}
	if first > 0.2 {
		t.Errorf("the rendition starts at %.3fs, want the beginning of the video", first)
	}
	if last < 19.5 {
		t.Errorf("the rendition ends at %.3fs, want about 20 — the resumed run is in the wrong place", last)
	}
}

// probeFloat reads one numeric field out of ffprobe.
func probeFloat(t *testing.T, path, entries string) float64 {
	t.Helper()
	//nolint:gosec // G204: fixture path from t.TempDir()
	out, err := exec.Command("ffprobe", "-hide_banner", "-v", "error",
		"-show_entries", entries, "-of", "default=nw=1:nk=1", path).Output()
	if err != nil {
		t.Fatalf("ffprobe %s: %v", entries, err)
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil {
		t.Fatalf("ffprobe %s = %q: %v", entries, out, err)
	}
	return v
}

// probeFrameSpan decodes the rendition and reports the first and last video
// timestamps and how many frames came out.
func probeFrameSpan(t *testing.T, path string) (first, last float64, frames int) {
	t.Helper()
	//nolint:gosec // G204: fixture path from t.TempDir()
	out, err := exec.Command("ffprobe", "-hide_banner", "-v", "error",
		"-select_streams", "v:0", "-show_entries", "frame=pts_time", "-of", "csv=p=0", path).Output()
	if err != nil {
		t.Fatalf("ffprobe frames: %v", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		v, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(line), ","), 64)
		if err != nil {
			continue
		}
		if frames == 0 || v < first {
			first = v
		}
		if v > last {
			last = v
		}
		frames++
	}
	return first, last, frames
}

// The duration is what the grid comes from. When TA has none, the source is
// probed rather than the job guessing.
func TestHLSProbesTheDurationWhenTAHasNone(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	body := buildFixture(t, dir, 6)
	out := filepath.Join(dir, "out")
	if err := os.MkdirAll(out, 0o750); err != nil {
		t.Fatal(err)
	}

	prepare, _ := HLS(HLSConfig{
		FFmpegPath: "ffmpeg",
		Source:     HLSSource{VideoCodec: "av01", Height: 240, AudioCodec: "opus"}, // no Duration
		Height:     480,
		Open:       testSource(body),
	})
	if err := prepare(t.Context(), out); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(out, HLSPlaylistName)) //nolint:gosec // test fixture path
	if err != nil {
		t.Fatal(err)
	}
	// Six seconds is two segments.
	if n := strings.Count(string(b), ".m4s"); n != 2 {
		t.Errorf("probed playlist names %d segments, want 2:\n%s", n, b)
	}
}

// A video whose length nothing can establish cannot be gridded, and saying so
// is better than producing a playlist that is wrong.
func TestHLSFailsWithoutADuration(t *testing.T) {
	dir := t.TempDir()
	stub := filepath.Join(dir, "ffprobe-fails")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\nexit 1\n"), 0o700); err != nil { //nolint:gosec // test fixture must be executable
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out")
	if err := os.MkdirAll(out, 0o750); err != nil {
		t.Fatal(err)
	}
	// The job derives ffprobe's path from ffmpeg's, so name the stub so that
	// happens.
	ffmpeg := filepath.Join(dir, "ffmpeg")
	if err := os.Rename(stub, filepath.Join(dir, "ffprobe")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ffmpeg, []byte("#!/bin/sh\nexit 1\n"), 0o700); err != nil { //nolint:gosec // test fixture must be executable
		t.Fatal(err)
	}

	prepare, _ := HLS(HLSConfig{
		FFmpegPath: ffmpeg,
		Source:     HLSSource{VideoCodec: "av01", Height: 1080, AudioCodec: "opus"},
		Height:     1080,
		Open:       emptySource,
	})
	err := prepare(t.Context(), out)
	if err == nil {
		t.Fatal("a video with no duration must fail rather than get a made-up playlist")
	}
	if !strings.Contains(err.Error(), "duration") {
		t.Errorf("the error should say what is missing: %v", err)
	}
}

func TestFFprobePath(t *testing.T) {
	// An operator who set FFMPEG_PATH should not have to set a second variable
	// for its sibling: ffprobe ships next to ffmpeg.
	for in, want := range map[string]string{
		"ffmpeg":          "ffprobe",
		"/usr/bin/ffmpeg": "/usr/bin/ffprobe",
		"/opt/ff/ffmpeg7": "/opt/ff/ffprobe", // not an "ffmpeg" suffix: the sibling
		"/opt/transcoder": "/opt/ffprobe",
	} {
		if got := ffprobePath(in); got != want {
			t.Errorf("ffprobePath(%q) = %q, want %q", in, got, want)
		}
	}
}

// The registry is how a segment request finds the job it is waiting on. A job
// that has ended must not be found, or a request would steer a dead run.
func TestHLSRegistry(t *testing.T) {
	var nilReg *HLSRegistry
	if nilReg.Get("anything") != nil {
		t.Error("a nil registry returned a job")
	}
	// Every accessor is nil-safe, because a handler asks before checking.
	var nilJob *HLSJob
	if nilJob.Segments() != 0 || nilJob.Progress() != 0 || nilJob.Has(3) {
		t.Error("a nil job is not inert")
	}
	nilJob.Request(3)
	nilJob.RequestSeconds(12)

	reg := NewHLSRegistry()
	job := &HLSJob{total: 4, produced: map[int]bool{}}
	drop := reg.put("hls-1080-v1", job)
	if reg.Get("hls-1080-v1") != job {
		t.Error("the job was not published")
	}
	drop()
	if reg.Get("hls-1080-v1") != nil {
		t.Error("a finished job is still published")
	}
}

func TestHLSJobProgress(t *testing.T) {
	job := &HLSJob{total: 8, produced: map[int]bool{}}
	if got := job.Progress(); got != 0 {
		t.Errorf("progress with nothing produced = %v", got)
	}
	for i := range 2 {
		job.produced[i] = true
	}
	if got := job.Progress(); got != 0.25 {
		t.Errorf("progress = %v, want 0.25", got)
	}
	if !job.Has(1) || job.Has(5) {
		t.Error("Has does not report the produced set")
	}
	for i := range 8 {
		job.produced[i] = true
	}
	if got := job.Progress(); got != 1 {
		t.Errorf("progress = %v, want 1", got)
	}
}

// waitFor polls cond until it holds or the deadline passes.
func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for the job to make progress")
}
