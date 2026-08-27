package media

import (
	"bufio"
	"bytes"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// The playlist Flimm writes itself.
//
// ffmpeg's own playlist grows as it encodes, which is exactly wrong for a
// viewer resuming at 40:00: a player cannot seek past the end of the playlist
// it holds, so it starts at 0:00 and waits for the encoder to catch up. Since
// the segment grid is fixed (4 s, aligned to the timeline) the *whole* playlist
// is known from the video's duration alone — so it is generated up front, as a
// complete `EXT-X-PLAYLIST-TYPE:VOD` list of N segments, and the segments
// themselves are filled in behind it in whatever order the viewer needs.
//
// A player therefore sees a normal, finished VOD stream from the first request
// and may seek anywhere in it. A segment that is not encoded yet is not a
// missing segment, it is a slow one: the request blocks until it lands (see
// MEDIA_SEGMENT_WAIT), and a seek far ahead steers the encoder there.

// hlsSegmentCount is how many segments a video of this duration has.
func hlsSegmentCount(duration float64) int {
	if duration <= 0 {
		return 0
	}
	n := int(math.Ceil(duration/float64(hlsSegmentSeconds) - hlsDurationEpsilon))
	if n < 1 {
		n = 1
	}
	return n
}

// hlsDurationEpsilon absorbs the last decimal of a reported duration, so a
// video TA calls 600.0000001 s long is 150 segments and not 151 — the extra
// one would be a segment no run can ever produce, and the job would never
// finish.
const hlsDurationEpsilon = 1e-6

// hlsSegmentDuration is how long segment i of a video of this duration is:
// four seconds, except the last, which is the remainder.
func hlsSegmentDuration(i, n int, duration float64) float64 {
	if i < n-1 {
		return float64(hlsSegmentSeconds)
	}
	last := duration - float64(hlsSegmentSeconds*(n-1))
	if last <= 0 || last > float64(hlsSegmentSeconds) {
		return float64(hlsSegmentSeconds)
	}
	return last
}

// hlsSegmentName is the file (and playlist URI) of segment i. It has to agree
// with ffmpeg's -hls_segment_filename pattern and with the route's
// validHLSFile, so it lives here rather than being spelled out three times.
func hlsSegmentName(i int) string { return fmt.Sprintf("seg%05d.m4s", i) }

// hlsSegmentIndex is hlsSegmentName in reverse: the index a segment file name
// refers to, or -1 if the name is not one.
func hlsSegmentIndex(name string) int {
	digits, ok := strings.CutPrefix(name, "seg")
	if !ok {
		return -1
	}
	digits, ok = strings.CutSuffix(digits, ".m4s")
	if !ok || len(digits) != 5 {
		return -1
	}
	i, err := strconv.Atoi(digits)
	if err != nil || i < 0 {
		return -1
	}
	return i
}

// buildHLSPlaylist renders the complete VOD playlist for n segments of a video
// of this duration. measured carries the durations ffmpeg actually produced,
// per segment index; every segment not in it is described by the nominal grid,
// which is what it will be to within a frame.
func buildHLSPlaylist(n int, duration float64, measured map[int]float64) []byte {
	var b bytes.Buffer
	target := hlsSegmentSeconds
	durations := make([]float64, n)
	for i := range n {
		d, ok := measured[i]
		if !ok || d <= 0 {
			d = hlsSegmentDuration(i, n, duration)
		}
		durations[i] = d
		// EXT-X-TARGETDURATION must be at least the longest EXTINF rounded to
		// the nearest integer. It is 4 for every rendition this produces; the
		// max() is there so a source that makes ffmpeg overshoot cannot turn
		// the playlist into a spec violation.
		if r := int(math.Round(d)); r > target {
			target = r
		}
	}

	b.WriteString("#EXTM3U\n")
	b.WriteString("#EXT-X-VERSION:7\n")
	fmt.Fprintf(&b, "#EXT-X-TARGETDURATION:%d\n", target)
	b.WriteString("#EXT-X-PLAYLIST-TYPE:VOD\n")
	b.WriteString("#EXT-X-INDEPENDENT-SEGMENTS\n")
	fmt.Fprintf(&b, "#EXT-X-MAP:URI=%q\n", HLSInitName)
	for i, d := range durations {
		fmt.Fprintf(&b, "#EXTINF:%.3f,\n%s\n", d, hlsSegmentName(i))
	}
	b.WriteString("#EXT-X-ENDLIST\n")
	return b.Bytes()
}

// hlsSecondsParam formats a resume position for the EXT-X-START offset and the
// master's `?from=` query, so both name the same point the same way: seconds
// with three decimals, matching the EXTINF grid's precision.
func hlsSecondsParam(t float64) string {
	return strconv.FormatFloat(t, 'f', 3, 64)
}

// InsertHLSStart returns the media playlist with an `#EXT-X-START` tag added to
// its header, so a resuming player begins at from and fetches the segment there
// first rather than blocking on segment 0. That matters because the transcode is
// resume-first — it encodes [from→end] before [0→from], so segment 0 is produced
// last — while every HLS player (hls.js and AVPlayer alike) fetches segment 0
// first to lay out the timeline before honouring a seek. EXT-X-START makes the
// player's own start position the resume point, so the first segment it asks for
// is the one the transcode produces first.
//
// The tag is inserted after `#EXT-X-PLAYLIST-TYPE` (conventionally in the header;
// the spec allows it anywhere). from must be inside (0, duration), where duration
// is the sum of the playlist's own EXTINF values; a from at or past the end, or a
// non-positive one, leaves the playlist untouched, so the player starts at 0 as
// it did before from existed. The segment list is never changed — this is a pure
// header addition, and the body is still the complete VOD list.
func InsertHLSStart(playlist []byte, from float64) []byte {
	if from <= 0 {
		return playlist
	}
	if total := hlsPlaylistDuration(playlist); total <= 0 || from >= total {
		return playlist
	}
	tag := fmt.Sprintf("#EXT-X-START:TIME-OFFSET=%s,PRECISE=YES\n", hlsSecondsParam(from))

	// Insert just after the EXT-X-PLAYLIST-TYPE line; fall back to before the
	// first segment (its #EXTINF) if that tag is somehow absent.
	at := -1
	if i := bytes.Index(playlist, []byte("#EXT-X-PLAYLIST-TYPE")); i >= 0 {
		if nl := bytes.IndexByte(playlist[i:], '\n'); nl >= 0 {
			at = i + nl + 1
		}
	}
	if at < 0 {
		if i := bytes.Index(playlist, []byte("#EXTINF:")); i >= 0 {
			at = i
		}
	}
	if at < 0 {
		return playlist
	}
	out := make([]byte, 0, len(playlist)+len(tag))
	out = append(out, playlist[:at]...)
	out = append(out, tag...)
	out = append(out, playlist[at:]...)
	return out
}

// hlsPlaylistDuration is the video's length as the playlist itself reports it:
// the sum of its EXTINF segment durations. The playlist is the complete VOD list
// from the first request, so this is the true duration without a second source.
func hlsPlaylistDuration(playlist []byte) float64 {
	var total float64
	sc := bufio.NewScanner(bytes.NewReader(playlist))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		rest, ok := strings.CutPrefix(line, "#EXTINF:")
		if !ok {
			continue
		}
		value, _, _ := strings.Cut(rest, ",")
		if d, err := strconv.ParseFloat(strings.TrimSpace(value), 64); err == nil && d > 0 {
			total += d
		}
	}
	return total
}

// writeHLSPlaylist publishes the playlist into dir by rename, so a player
// re-reading it never gets half a file.
func writeHLSPlaylist(dir string, n int, duration float64, measured map[int]float64) error {
	tmp, err := os.CreateTemp(dir, ".index-*.m3u8")
	if err != nil {
		return fmt.Errorf("hls: write playlist: %w", err)
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := tmp.Write(buildHLSPlaylist(n, duration, measured)); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("hls: write playlist: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("hls: write playlist: %w", err)
	}
	if err := os.Chmod(name, 0o600); err != nil {
		return fmt.Errorf("hls: write playlist: %w", err)
	}
	if err := os.Rename(name, filepath.Join(dir, HLSPlaylistName)); err != nil {
		return fmt.Errorf("hls: publish playlist: %w", err)
	}
	return nil
}

// readRunDurations collects the real segment durations out of the per-run
// playlists ffmpeg wrote (`run-NNNNN.m3u8`). Those playlists are otherwise
// ignored — they describe one run, not the rendition — but they are the only
// record of how long each segment actually came out.
func readRunDurations(dir string) map[int]float64 {
	out := map[int]float64{}
	runs, err := filepath.Glob(filepath.Join(dir, hlsRunPlaylistGlob))
	if err != nil {
		return out
	}
	for _, run := range runs {
		b, err := readPlaylist(run)
		if err != nil {
			continue
		}
		var pending float64
		sc := bufio.NewScanner(bytes.NewReader(b))
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if rest, ok := strings.CutPrefix(line, "#EXTINF:"); ok {
				value, _, _ := strings.Cut(rest, ",")
				d, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
				if err == nil && d > 0 {
					pending = d
				}
				continue
			}
			// A run that has to be put back on the timeline names its
			// segments with the `.raw` suffix they are written under.
			if i := hlsSegmentIndex(strings.TrimSuffix(line, hlsRawSuffix)); i >= 0 && pending > 0 {
				out[i] = pending
			}
			if !strings.HasPrefix(line, "#") {
				pending = 0
			}
		}
	}
	return out
}
