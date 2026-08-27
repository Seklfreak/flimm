package media

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// HLS variant: a compatible video rendition.
//
// The archive holds whatever yt-dlp downloaded, which today is usually VP9 or
// AV1 in WebM — codecs AVFoundation cannot decode on most Apple hardware. This
// variant transcodes the source to H.264/AAC and writes it as HLS with fMP4
// segments, so a player can start on the first segment instead of waiting for
// a whole file. It is a real transcode: see docs/api.md "Compatible video
// rendition (HLS)" for the cost.
const (
	// HLSVariant is the cache key prefix; the entry is a *directory*, not a
	// file, because a rendition is a playlist plus its segments.
	HLSVariant = "hls"
	// HLSPlaylistName, HLSInitName and the seg%05d.m4s pattern are also the
	// URLs the playlist refers to, so they are fixed, relative names.
	HLSPlaylistName = "index.m3u8"
	HLSInitName     = "init.mp4"

	HLSPlaylistType = "application/vnd.apple.mpegurl"
	HLSInitType     = "video/mp4"
	HLSSegmentType  = "video/iso.segment"

	// hlsMaxHeight caps the rendition. Anything taller is scaled down: a 4K
	// x264 encode costs several times as much CPU for a rendition no Apple
	// client asks for.
	hlsMaxHeight = 1080
	// hlsSegmentSeconds is the target segment length. Shorter segments reach
	// the first frame sooner and cost more requests; 4 s is the usual balance
	// and matches Apple's own recommendation (6 s max).
	hlsSegmentSeconds = "4"
	// hlsGOP forces a keyframe at least every 96 frames (4 s at 24 fps) so the
	// muxer can always cut a segment where it wants to.
	hlsGOP = "96"
)

// HLSName is the cache entry (a directory) for a video's HLS rendition.
func HLSName(videoID string) string { return HLSVariant + "-" + videoID }

// HLSSource is what the source file holds, as TubeArchivist reports it in the
// video document's `streams`. It decides copy vs re-encode; it is metadata,
// not a guarantee, which is why a failed copy falls back to encoding.
type HLSSource struct {
	VideoCodec string
	Height     int
	AudioCodec string
}

// HLS returns a DirDeriveFunc that writes index.m3u8, init.mp4 and the
// segments into dir.
//
// Both tracks are copied when the source already matches what a player wants
// (H.264 at or below 1080p, AAC audio): segmenting a stream copy is nearly
// free, so a compatible archive costs no more than the audio variants do.
// Otherwise the track is encoded for real.
func HLS(ffmpegPath string, src HLSSource, log *slog.Logger, source SourceFunc) DirDeriveFunc {
	return func(ctx context.Context, dir string) error {
		vc, ac := hlsVideoCodec(src), aacCodec(src.AudioCodec)
		err := runFFmpegIn(ctx, ffmpegPath, dir, source, hlsArgs(vc, ac), log)
		if err != nil && ctx.Err() == nil && (vc == "copy" || ac == "copy") {
			// The codec strings come from TA's metadata; a container that
			// refuses the copied track must not fail the request when a real
			// encode would have worked. Clear the partial output first: the
			// playlist would otherwise carry the abandoned segments.
			if clearErr := clearDir(dir); clearErr != nil {
				return fmt.Errorf("derive hls: %w (and clearing the partial output failed: %w)", err, clearErr)
			}
			if reErr := runFFmpegIn(ctx, ffmpegPath, dir, source, hlsArgs("libx264", "aac"), log); reErr != nil {
				return fmt.Errorf("derive hls: copy failed (%w) and re-encode failed: %w", err, reErr)
			}
		} else if err != nil {
			return fmt.Errorf("derive hls: %w", err)
		}
		return ensureEndList(filepath.Join(dir, HLSPlaylistName))
	}
}

// hlsVideoCodec copies a source that is already what the rendition would
// encode to, and encodes everything else. An unknown height means unknown
// dimensions, so it is encoded (and scaled) rather than trusted.
func hlsVideoCodec(src HLSSource) string {
	if isH264(src.VideoCodec) && src.Height > 0 && src.Height <= hlsMaxHeight {
		return "copy"
	}
	return "libx264"
}

// isH264 recognises the ways an H.264 track is named. TA reports the ISO-BMFF
// sample entry ("avc1", sometimes with a profile suffix such as
// "avc1.640028"); ffprobe and yt-dlp say "h264".
func isH264(codec string) bool {
	c := strings.ToLower(strings.TrimSpace(codec))
	name, _, _ := strings.Cut(c, ".")
	return name == "avc1" || name == "h264"
}

// hlsArgs builds the ffmpeg command line. The output names are relative
// because the command runs with the entry directory as its working directory —
// which is also what keeps them valid as playlist URLs.
//
// The source is read from stdin, as the audio variants are, so the API token
// never reaches argv or a log line. A linear transcode never seeks backwards,
// so an unseekable input costs nothing here.
func hlsArgs(videoCodec, audioCodec string) []string {
	args := []string{
		"-hide_banner", "-loglevel", "error",
		"-i", "pipe:0",
		"-map", "0:v:0", "-map", "0:a:0",
	}
	if videoCodec == "copy" {
		args = append(args, "-c:v", "copy")
	} else {
		args = append(args,
			// -2 keeps the width even (x264 needs it) and the aspect ratio;
			// min() means a source below the cap is never upscaled.
			"-vf", "scale=-2:'min("+strconv.Itoa(hlsMaxHeight)+",ih)'",
			"-c:v", videoCodec,
			// veryfast is the fastest preset that still holds quality at
			// crf 23 — time to first frame is what this variant optimises.
			"-preset", "veryfast", "-crf", "23",
			// High@4.1 with 4:2:0 is what every Apple device since the 4S
			// decodes in hardware.
			"-profile:v", "high", "-level", "4.1", "-pix_fmt", "yuv420p",
			"-g", hlsGOP, "-keyint_min", hlsGOP, "-sc_threshold", "0",
		)
	}
	args = append(args, "-c:a", audioCodec)
	if audioCodec != "copy" {
		args = append(args, "-b:a", "160k", "-ac", "2")
	}
	return append(args,
		"-threads", "0",
		"-f", "hls",
		"-hls_time", hlsSegmentSeconds,
		// An event playlist grows as segments land and ffmpeg appends
		// #EXT-X-ENDLIST when it finishes, which is exactly the progressive
		// behaviour a viewer waiting on the first segment needs.
		"-hls_playlist_type", "event",
		"-hls_segment_type", "fmp4",
		// temp_file publishes each segment by rename, so a player never reads
		// one that is still being written.
		"-hls_flags", "independent_segments+temp_file",
		"-hls_fmp4_init_filename", HLSInitName,
		"-hls_segment_filename", "seg%05d.m4s",
		"-y", HLSPlaylistName,
	)
}

// HLSPlaylistReady reports whether the playlist can be handed to a player:
// it exists and names at least one segment. Serving it earlier gives the
// player an empty playlist, which some treat as a fatal error.
func HLSPlaylistReady(dir string) bool {
	b, err := readPlaylist(filepath.Join(dir, HLSPlaylistName))
	if err != nil {
		return false
	}
	return bytes.Contains(b, []byte(".m4s"))
}

// ensureEndList appends #EXT-X-ENDLIST when ffmpeg did not. It does with
// -hls_playlist_type event, but a player left waiting for the tag would stall
// forever at the end of the video, so this is not left to trust.
func ensureEndList(playlist string) error {
	b, err := readPlaylist(playlist)
	if err != nil {
		return fmt.Errorf("hls: read playlist: %w", err)
	}
	if bytes.Contains(b, []byte("#EXT-X-ENDLIST")) {
		return nil
	}
	f, err := os.OpenFile(playlist, os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec // path built from the cache dir
	if err != nil {
		return fmt.Errorf("hls: close playlist: %w", err)
	}
	defer f.Close()
	if _, err := f.WriteString("#EXT-X-ENDLIST\n"); err != nil {
		return fmt.Errorf("hls: close playlist: %w", err)
	}
	return nil
}

// maxPlaylistBytes caps a playlist read. An hour of 4 s segments is ~30 KB, so
// anything near this is not a playlist.
const maxPlaylistBytes = 4 << 20

func readPlaylist(path string) ([]byte, error) {
	f, err := os.Open(path) //nolint:gosec // path built from the cache dir and a fixed name
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, maxPlaylistBytes))
}

// clearDir empties dir without removing it, so a retry starts from nothing
// while the entry stays where concurrent readers expect it.
func clearDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// runFFmpegIn pipes the source through ffmpeg with dir as the working
// directory, so the muxer's relative output names land inside the cache entry.
func runFFmpegIn(ctx context.Context, ffmpegPath, dir string, source SourceFunc, args []string, log *slog.Logger) error {
	src, err := source(ctx)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer src.Close()

	// ffmpegPath comes from configuration and every argument is a literal —
	// no request data, and no token, reaches argv.
	cmd := exec.CommandContext(ctx, ffmpegPath, args...) //nolint:gosec // G204: operator-supplied binary, fixed args
	cmd.Dir = dir
	cmd.Stdin = src
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	runErr := cmd.Run()

	if msg := strings.TrimSpace(stderr.String()); msg != "" && log != nil {
		log.Debug("ffmpeg", "entry", filepath.Base(dir), "stderr", scrubSecrets(msg))
	}
	if runErr != nil {
		msg := scrubSecrets(strings.TrimSpace(stderr.String()))
		if len(msg) > 500 {
			msg = msg[:500]
		}
		return fmt.Errorf("ffmpeg: %w: %s", runErr, msg)
	}
	return nil
}

// secretPattern matches the shapes a credential could take if one ever reached
// ffmpeg's stderr. Nothing should: the source is piped in on stdin. This is the
// second lock on the door — an error message is not a place to learn the TA
// token from.
var secretPattern = regexp.MustCompile(`(?i)(authorization:.*|\btoken[=:]\s*\S+|\btoken\s+[A-Za-z0-9._~+/-]{8,})`)

func scrubSecrets(s string) string { return secretPattern.ReplaceAllString(s, "[redacted]") }
