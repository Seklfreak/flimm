package media

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// makeSource builds a tiny file shaped like the archive's: a video track plus
// an Opus audio track in an mp4, faststart so it remuxes from a stream.
func makeSource(t *testing.T, dir string) string {
	t.Helper()
	src := filepath.Join(dir, "src.mp4")
	//nolint:gosec // G204: fixture paths from t.TempDir(), no request data
	cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc=duration=2:size=64x64:rate=10",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=2",
		"-c:v", "libx264", "-c:a", "libopus", "-movflags", "+faststart",
		"-y", src)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Skipf("cannot build fixture (ffmpeg lacks libx264/libopus?): %v: %s", err, stderr.String())
	}
	return src
}

func requireFFmpeg(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed; skipping derivation test")
	}
}

// Exercises the real ffmpeg invocation: the arguments are the part most likely
// to be wrong, and no unit test of the cache would catch it.
func TestAudioRemuxesOpusWithoutReencoding(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	src := makeSource(t, dir)
	dst := filepath.Join(dir, "out.webm")

	derive := Audio("ffmpeg", func(context.Context) (io.ReadCloser, error) {
		return os.Open(src) //nolint:gosec // test fixture path
	})
	if err := derive(t.Context(), dst); err != nil {
		t.Fatalf("derive: %v", err)
	}

	b, err := os.ReadFile(dst) //nolint:gosec // test fixture path
	if err != nil {
		t.Fatal(err)
	}
	if len(b) == 0 {
		t.Fatal("derivation produced an empty file")
	}
	// EBML magic: the output really is a Matroska/WebM container.
	if !bytes.HasPrefix(b, []byte{0x1A, 0x45, 0xDF, 0xA3}) {
		t.Errorf("output is not WebM, starts with % x", b[:4])
	}
	// The point of the feature: the video track must be gone.
	if probe, err := exec.LookPath("ffprobe"); err == nil {
		//nolint:gosec // G204: probe path from LookPath, dst from t.TempDir()
		out, err := exec.Command(probe, "-hide_banner", "-loglevel", "error",
			"-show_entries", "stream=codec_type", "-of", "csv=p=0", dst).Output()
		if err != nil {
			t.Fatalf("ffprobe: %v", err)
		}
		got := string(bytes.TrimSpace(out))
		if bytes.Contains(out, []byte("video")) {
			t.Errorf("video track survived: %q", got)
		}
		if !bytes.Contains(out, []byte("audio")) {
			t.Errorf("no audio track in output: %q", got)
		}
	}
}

// A source whose audio WebM cannot hold (AAC) must still derive, via the
// re-encode fallback, rather than failing the request.
func TestAudioFallsBackToReencodeForNonOpusSource(t *testing.T) {
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

	dst := filepath.Join(dir, "out.webm")
	derive := Audio("ffmpeg", func(context.Context) (io.ReadCloser, error) {
		return os.Open(src) //nolint:gosec // test fixture path
	})
	if err := derive(t.Context(), dst); err != nil {
		t.Fatalf("AAC source should fall back to re-encoding, got: %v", err)
	}
	b, err := os.ReadFile(dst) //nolint:gosec // test fixture path
	if err != nil || len(b) == 0 {
		t.Fatalf("fallback produced no output: %v", err)
	}
	if !bytes.HasPrefix(b, []byte{0x1A, 0x45, 0xDF, 0xA3}) {
		t.Error("fallback output is not WebM")
	}
}
