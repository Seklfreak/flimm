package media

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The whole feature in one test: transcode something, build the playlist from
// the segments, then cut a byte range out of a segment and decode it. If the
// range is right a picture comes out; if it is a few bytes off, nothing does.
//
// This is the only check that matters. A playlist that parses but points at the
// wrong bytes looks perfect and shows a scrubber full of grey.
func TestAByteRangeFromThePlaylistDecodesToAPicture(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	src := makeSource(t, dir)
	out := filepath.Join(dir, "hls")
	if err := os.MkdirAll(out, 0o750); err != nil {
		t.Fatal(err)
	}

	// Two seconds at one-second segments: enough for several I-frames.
	//nolint:gosec // G204: fixture paths from t.TempDir()
	cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error",
		"-i", src, "-an", "-c:v", "libx264", "-g", "10", "-force_key_frames", "expr:gte(t,n_forced*1)",
		"-f", "hls", "-hls_time", "1", "-hls_playlist_type", "vod",
		"-hls_segment_type", "fmp4", "-hls_fmp4_init_filename", HLSInitName,
		"-hls_segment_filename", filepath.Join(out, "seg%05d.m4s"),
		"-y", filepath.Join(out, HLSPlaylistName))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Skipf("cannot build an fMP4 rendition: %v: %s", err, stderr.String())
	}

	playlist, err := BuildIFramePlaylist(out)
	if err != nil {
		t.Fatalf("BuildIFramePlaylist: %v", err)
	}
	text := string(playlist)
	for _, tag := range []string{"#EXT-X-I-FRAMES-ONLY", `#EXT-X-MAP:URI="init.mp4"`, "#EXT-X-ENDLIST"} {
		if !strings.Contains(text, tag) {
			t.Errorf("playlist is missing %s:\n%s", tag, text)
		}
	}

	name, length := firstRange(t, text)
	segment, err := os.ReadFile(filepath.Join(out, name)) //nolint:gosec // test fixture
	if err != nil {
		t.Fatal(err)
	}
	if length <= 0 || length > len(segment) {
		t.Fatalf("range %d is not inside a %d-byte segment", length, len(segment))
	}

	// init + exactly the advertised range, which is what a player fetches.
	frame := filepath.Join(dir, "frame.mp4")
	initBytes, err := os.ReadFile(filepath.Join(out, HLSInitName)) //nolint:gosec // test fixture
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(frame, append(initBytes, segment[:length]...), 0o600); err != nil {
		t.Fatal(err)
	}
	//nolint:gosec // G204: fixture paths from t.TempDir()
	decode := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error",
		"-i", frame, "-frames:v", "1", "-y", filepath.Join(dir, "frame.png"))
	decode.Stderr = &stderr
	if err := decode.Run(); err != nil {
		t.Fatalf("the advertised range does not decode: %v: %s", err, stderr.String())
	}
	info, err := os.Stat(filepath.Join(dir, "frame.png"))
	if err != nil || info.Size() == 0 {
		t.Fatalf("no picture came out of the range: %v", err)
	}
}

// firstRange reads the first segment name and byte-range length out of a
// rendered playlist.
func firstRange(t *testing.T, playlist string) (string, int) {
	t.Helper()
	var length int
	for _, line := range strings.Split(playlist, "\n") {
		switch {
		case strings.HasPrefix(line, "#EXT-X-BYTERANGE:"):
			value := strings.TrimPrefix(line, "#EXT-X-BYTERANGE:")
			n, err := strconv.Atoi(strings.Split(value, "@")[0])
			if err != nil {
				t.Fatalf("byte range %q: %v", line, err)
			}
			if !strings.HasSuffix(value, "@0") {
				t.Errorf("range %q does not start at the segment's first byte", line)
			}
			length = n
		case strings.HasPrefix(line, "#") || line == "":
		default:
			if length > 0 {
				return strings.TrimSpace(line), length
			}
		}
	}
	t.Fatal("playlist listed no segment with a byte range")
	return "", 0
}

// A fragment that does not say where its samples are gets left out rather than
// guessed at: a wrong range is a broken picture.
func TestAFragmentWithoutADataOffsetIsRefused(t *testing.T) {
	// trun with flags 0x000200 (sizes) but no 0x000001 (data offset).
	trun := box("trun", append([]byte{0x00, 0x00, 0x02, 0x00, 0, 0, 0, 1}, 0, 0, 0x10, 0))
	traf := box("traf", trun)
	moof := box("moof", traf)
	if _, err := firstSampleEnd(moof); err == nil {
		t.Error("a trun with no data offset was accepted")
	}
}

// The ordinary case, hand-built so the arithmetic is visible: a data offset of
// 100 and a first sample of 20 bytes ends at byte 120.
func TestTheRangeEndsAtTheFirstSample(t *testing.T) {
	trun := box("trun", []byte{
		0x00, 0x00, 0x03, 0x01, // flags: data offset + sample duration + sample size
		0, 0, 0, 2, // two samples
		0, 0, 0, 100, // data offset
		0, 0, 0, 33, // sample 0 duration
		0, 0, 0, 20, // sample 0 size
		0, 0, 0, 33,
		0, 0, 0, 7,
	})
	fragment := append(box("moof", box("traf", trun)), make([]byte, 200)...)
	end, err := firstSampleEnd(fragment)
	if err != nil {
		t.Fatalf("firstSampleEnd: %v", err)
	}
	if end != 120 {
		t.Errorf("end = %d, want 120", end)
	}
}

// Only the video codec: an I-frame playlist carries pictures and no audio, and
// claiming audio it does not have is a lie a player may act on.
func TestTheIFrameStreamInfNamesOnlyTheVideoCodec(t *testing.T) {
	line := BuildHLSIFrameStreamInf("avc1.640029,mp4a.40.2", 2_500_000, 1280, 720)
	if strings.Contains(line, "mp4a") {
		t.Errorf("stream inf claims audio: %s", line)
	}
	for _, want := range []string{`CODECS="avc1.640029"`, "RESOLUTION=1280x720", `URI="iframe.m3u8"`} {
		if !strings.Contains(line, want) {
			t.Errorf("stream inf is missing %s: %s", want, line)
		}
	}
}

// box wraps a payload in an MP4 box header.
func box(typ string, payload []byte) []byte {
	out := make([]byte, 8, 8+len(payload))
	size := uint32(8 + len(payload)) //nolint:gosec // test fixture sizes are small
	out[0], out[1], out[2], out[3] = byte(size>>24), byte(size>>16), byte(size>>8), byte(size)
	copy(out[4:], typ)
	return append(out, payload...)
}
