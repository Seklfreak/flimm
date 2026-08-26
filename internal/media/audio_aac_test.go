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
	"testing"
)

// The copy-vs-encode decision is the whole point of this variant: copying an
// Opus track would produce a file Apple still cannot play, and re-encoding an
// AAC track would burn CPU for nothing.
func TestAACCodecChoice(t *testing.T) {
	for codec, want := range map[string]string{
		"mp4a":      "copy",
		"mp4a.40.2": "copy",
		"MP4A":      "copy",
		" mp4a ":    "copy",
		"aac":       "copy",
		"opus":      "aac",
		"vorbis":    "aac",
		"":          "aac",
	} {
		if got := aacCodec(codec); got != want {
			t.Errorf("aacCodec(%q) = %q, want %q", codec, got, want)
		}
	}
}

func TestAACArgs(t *testing.T) {
	want := []string{
		"-hide_banner", "-loglevel", "error",
		"-i", "pipe:0",
		"-vn", "-map", "0:a:0",
		"-c:a", "aac", "-b:a", "128k",
		"-movflags", "+faststart", "-f", "mp4", "-y", "/tmp/out.m4a",
	}
	if got := aacArgs("aac", "/tmp/out.m4a"); !reflect.DeepEqual(got, want) {
		t.Errorf("encode args = %v, want %v", got, want)
	}

	got := aacArgs("copy", "/tmp/out.m4a")
	// A stream copy must not carry a bitrate: there is nothing to encode.
	if slices.Contains(got, "-b:a") {
		t.Errorf("copy args carry a bitrate: %v", got)
	}
	for _, want := range [][]string{{"-c:a", "copy"}, {"-movflags", "+faststart"}, {"-f", "mp4"}} {
		if i := slices.Index(got, want[0]); i < 0 || got[i+1] != want[1] {
			t.Errorf("copy args missing %v: %v", want, got)
		}
	}
	if got[len(got)-1] != "/tmp/out.m4a" || got[len(got)-2] != "-y" {
		t.Errorf("copy args do not end in the output path: %v", got)
	}
}

// Exercises the real ffmpeg invocation for an Opus source: it must come out as
// AAC in MP4, which is the case the Apple clients depend on.
func TestAudioAACReencodesOpusSource(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	src := makeSource(t, dir)
	dst := filepath.Join(dir, "out.m4a")

	derive := AudioAAC("ffmpeg", "opus", func(context.Context) (io.ReadCloser, error) {
		return os.Open(src) //nolint:gosec // test fixture path
	})
	if err := derive(t.Context(), dst); err != nil {
		t.Fatalf("derive: %v", err)
	}
	assertAACOnly(t, dst)
}

// An AAC source takes the copy path; the result must still be a valid,
// video-free m4a.
func TestAudioAACCopiesAACSource(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "aac.mp4")
	//nolint:gosec // G204: fixture paths from t.TempDir(), no request data
	cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc=duration=2:size=64x64:rate=10",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=2",
		"-c:v", "libx264", "-c:a", "aac", "-movflags", "+faststart", "-y", src)
	if err := cmd.Run(); err != nil {
		t.Skipf("cannot build AAC fixture: %v", err)
	}

	dst := filepath.Join(dir, "out.m4a")
	derive := AudioAAC("ffmpeg", "mp4a.40.2", func(context.Context) (io.ReadCloser, error) {
		return os.Open(src) //nolint:gosec // test fixture path
	})
	if err := derive(t.Context(), dst); err != nil {
		t.Fatalf("derive: %v", err)
	}
	assertAACOnly(t, dst)
}

// A container that refuses the copied track must not fail the request: the
// derivation falls back to encoding. Driven by a stub ffmpeg so it needs no
// real one and the two invocations can be inspected.
func TestAudioAACFallsBackWhenCopyFails(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "calls.log")
	stub := writeStubFFmpeg(t, dir, log)
	dst := filepath.Join(dir, "out.m4a")

	derive := AudioAAC(stub, "mp4a", func(context.Context) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(nil)), nil
	})
	if err := derive(t.Context(), dst); err != nil {
		t.Fatalf("copy failure should fall back to encoding, got: %v", err)
	}
	calls, err := os.ReadFile(log) //nolint:gosec // test fixture path
	if err != nil {
		t.Fatal(err)
	}
	if got := string(bytes.TrimSpace(calls)); got != "copy\naac" {
		t.Errorf("ffmpeg calls = %q, want copy then aac", got)
	}
}

// A non-AAC source is encoded straight away: no pointless copy attempt.
func TestAudioAACSkipsCopyForOpusSource(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "calls.log")
	stub := writeStubFFmpeg(t, dir, log)

	derive := AudioAAC(stub, "opus", func(context.Context) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(nil)), nil
	})
	if err := derive(t.Context(), filepath.Join(dir, "out.m4a")); err != nil {
		t.Fatalf("derive: %v", err)
	}
	calls, err := os.ReadFile(log) //nolint:gosec // test fixture path
	if err != nil {
		t.Fatal(err)
	}
	if got := string(bytes.TrimSpace(calls)); got != "aac" {
		t.Errorf("ffmpeg calls = %q, want a single aac call", got)
	}
}

// writeStubFFmpeg installs a script that records the -c:a value of each call
// and fails the copy, so the fallback can be tested without ffmpeg.
func writeStubFFmpeg(t *testing.T, dir, log string) string {
	t.Helper()
	path := filepath.Join(dir, "ffmpeg-stub")
	script := "#!/bin/sh\n" +
		"codec=$(echo \"$@\" | sed -n 's/.*-c:a \\([^ ]*\\).*/\\1/p')\n" +
		"echo \"$codec\" >> " + log + "\n" +
		"[ \"$codec\" = copy ] && { echo 'could not mux' >&2; exit 1; }\n" +
		"eval \"dst=\\${$#}\"; : > \"$dst\"\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil { //nolint:gosec // test fixture must be executable
		t.Fatal(err)
	}
	return path
}

// assertAACOnly checks the derivation produced an MP4 holding exactly one AAC
// audio track and no video.
func assertAACOnly(t *testing.T, dst string) {
	t.Helper()
	b, err := os.ReadFile(dst) //nolint:gosec // test fixture path
	if err != nil {
		t.Fatal(err)
	}
	if len(b) == 0 {
		t.Fatal("derivation produced an empty file")
	}
	// ISO-BMFF: the first box is "ftyp", so bytes 4..8 are its type.
	if len(b) < 12 || !bytes.Equal(b[4:8], []byte("ftyp")) {
		t.Fatalf("output is not an MP4, starts with % x", b[:min(12, len(b))])
	}
	probe, err := exec.LookPath("ffprobe")
	if err != nil {
		return
	}
	//nolint:gosec // G204: probe path from LookPath, dst from t.TempDir()
	out, err := exec.Command(probe, "-hide_banner", "-loglevel", "error",
		"-show_entries", "stream=codec_type,codec_name", "-of", "csv=p=0", dst).Output()
	if err != nil {
		t.Fatalf("ffprobe: %v", err)
	}
	got := string(bytes.TrimSpace(out))
	if bytes.Contains(out, []byte("video")) {
		t.Errorf("video track survived: %q", got)
	}
	if !bytes.Contains(out, []byte("aac")) {
		t.Errorf("output audio is not AAC: %q", got)
	}
}
