package media

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// How many segments a video has is the number everything else hangs off: get it
// wrong by one and either the last few seconds are missing or the job waits
// forever for a segment no run will ever produce.
func TestHLSSegmentCount(t *testing.T) {
	for _, tc := range []struct {
		duration float64
		want     int
	}{
		{0, 0},
		{0.5, 1},
		{4, 1},
		{4.001, 2},
		{6, 2},
		{20, 5},
		{20.5, 6},
		{2520, 630}, // 42 minutes
		// A duration that is an exact multiple but for floating-point noise
		// must not grow a segment nothing can fill.
		{600.0000001, 150},
	} {
		if got := hlsSegmentCount(tc.duration); got != tc.want {
			t.Errorf("hlsSegmentCount(%v) = %d, want %d", tc.duration, got, tc.want)
		}
	}
}

// The playlist a player gets on its very first request. It describes the whole
// video — that is what lets it seek to a resume position before anything has
// been encoded.
func TestBuildHLSPlaylist(t *testing.T) {
	got := string(buildHLSPlaylist(hlsSegmentCount(20.5), 20.5, nil))

	for _, want := range []string{
		"#EXTM3U\n",
		"#EXT-X-VERSION:7\n",
		"#EXT-X-TARGETDURATION:4\n",
		"#EXT-X-PLAYLIST-TYPE:VOD\n",
		"#EXT-X-INDEPENDENT-SEGMENTS\n",
		`#EXT-X-MAP:URI="init.mp4"`,
		"#EXT-X-ENDLIST\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("playlist is missing %q:\n%s", want, got)
		}
	}
	// Six segments: five whole ones and the half-second remainder.
	if n := strings.Count(got, ".m4s"); n != 6 {
		t.Errorf("playlist names %d segments, want 6:\n%s", n, got)
	}
	for i, want := range []string{"4.000", "4.000", "4.000", "4.000", "4.000", "0.500"} {
		line := "#EXTINF:" + want + ",\n" + hlsSegmentName(i) + "\n"
		if !strings.Contains(got, line) {
			t.Errorf("segment %d is not %q:\n%s", i, want, got)
		}
	}
	// The last segment is last, and the list is closed after it.
	if !strings.HasSuffix(got, "seg00005.m4s\n#EXT-X-ENDLIST\n") {
		t.Errorf("playlist does not end with the last segment and ENDLIST:\n%s", got)
	}
}

// A video that divides evenly has a full-length last segment, not a zero one.
func TestBuildHLSPlaylistExactMultiple(t *testing.T) {
	got := string(buildHLSPlaylist(hlsSegmentCount(20), 20, nil))
	if n := strings.Count(got, ".m4s"); n != 5 {
		t.Errorf("playlist names %d segments, want 5:\n%s", n, got)
	}
	if strings.Contains(got, "#EXTINF:0.000") {
		t.Errorf("a zero-length segment crept in:\n%s", got)
	}
	if !strings.HasSuffix(got, "seg00004.m4s\n#EXT-X-ENDLIST\n") {
		t.Errorf("playlist does not end at segment 4:\n%s", got)
	}
}

// Once a run has finished, its real durations replace the nominal ones — and
// TARGETDURATION follows them up rather than staying a lie.
func TestBuildHLSPlaylistUsesRealDurations(t *testing.T) {
	got := string(buildHLSPlaylist(3, 12, map[int]float64{0: 4.004, 1: 4.6}))
	if !strings.Contains(got, "#EXTINF:4.004,\nseg00000.m4s") {
		t.Errorf("real duration not used:\n%s", got)
	}
	if !strings.Contains(got, "#EXTINF:4.600,\nseg00001.m4s") {
		t.Errorf("real duration not used:\n%s", got)
	}
	// Segment 2 has no run playlist entry yet, so it keeps the grid's value.
	if !strings.Contains(got, "#EXTINF:4.000,\nseg00002.m4s") {
		t.Errorf("unencoded segment not described by the grid:\n%s", got)
	}
	// 4.6 rounds to 5, and TARGETDURATION must be at least the longest EXTINF.
	if !strings.Contains(got, "#EXT-X-TARGETDURATION:5\n") {
		t.Errorf("TARGETDURATION does not cover the longest segment:\n%s", got)
	}
}

func TestHLSSegmentNameAndIndex(t *testing.T) {
	for i, want := range map[int]string{0: "seg00000.m4s", 3: "seg00003.m4s", 600: "seg00600.m4s", 12345: "seg12345.m4s"} {
		if got := HLSSegmentName(i); got != want {
			t.Errorf("HLSSegmentName(%d) = %q, want %q", i, got, want)
		}
		if got := HLSSegmentIndex(want); got != i {
			t.Errorf("HLSSegmentIndex(%q) = %d, want %d", want, got, i)
		}
	}
	// Anything that is not a segment, including the in-progress temp file the
	// muxer holds, must not read as one.
	for _, name := range []string{"index.m3u8", "init.mp4", "seg00003.m4s.tmp", "seg3.m4s", "segxxxxx.m4s", "run-00000.m3u8", ".complete", ""} {
		if got := HLSSegmentIndex(name); got != -1 {
			t.Errorf("HLSSegmentIndex(%q) = %d, want -1", name, got)
		}
	}
}

func TestHLSSegmentIndexAt(t *testing.T) {
	for seconds, want := range map[float64]int{0: 0, 3.9: 0, 4: 1, 7.999: 1, 8: 2, 2400: 600, -5: 0} {
		if got := HLSSegmentIndexAt(seconds); got != want {
			t.Errorf("HLSSegmentIndexAt(%v) = %d, want %d", seconds, got, want)
		}
	}
}

// The run playlists are the only record of how long each segment really came
// out, and they arrive one per run with their own numbering.
func TestReadRunDurations(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("run-00000.m3u8", "#EXTM3U\n#EXT-X-MAP:URI=\"init.mp4\"\n"+
		"#EXTINF:4.004000,\nseg00000.m4s\n#EXTINF:3.996000,\nseg00001.m4s\n#EXT-X-ENDLIST\n")
	write("run-00003.m3u8", "#EXTM3U\n#EXTINF:2.500000,\nseg00003.m4s\n#EXT-X-ENDLIST\n")
	// Not a run playlist; must not be read.
	write(HLSPlaylistName, "#EXTM3U\n#EXTINF:9.000,\nseg00000.m4s\n")

	got := readRunDurations(dir)
	for i, want := range map[int]float64{0: 4.004, 1: 3.996, 3: 2.5} {
		if got[i] != want {
			t.Errorf("segment %d duration = %v, want %v", i, got[i], want)
		}
	}
	if _, ok := got[2]; ok {
		t.Errorf("a segment no run produced has a duration: %v", got)
	}
}

// The playlist is published by rename, so a player re-reading it never gets
// half a file — and the temp it was built in does not linger in the entry.
func TestWriteHLSPlaylistPublishesWhole(t *testing.T) {
	dir := t.TempDir()
	if err := writeHLSPlaylist(dir, 3, 12, nil); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, HLSPlaylistName)) //nolint:gosec // test fixture path
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(b), "#EXT-X-ENDLIST\n") {
		t.Errorf("playlist is not complete: %q", b)
	}
	// Rewriting it (as the end of a job does) replaces it in place.
	if err := writeHLSPlaylist(dir, 3, 12, map[int]float64{0: 4.004}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != HLSPlaylistName {
			t.Errorf("writing the playlist left %s behind", e.Name())
		}
	}
}
