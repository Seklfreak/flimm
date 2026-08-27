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
	"slices"
	"strconv"
	"strings"
)

// HLS variants: compatible video renditions, one per quality.
//
// The archive holds whatever yt-dlp downloaded, which today is usually VP9 or
// AV1 in WebM — codecs AVFoundation cannot decode on most Apple hardware. A
// variant transcodes the source to a codec Apple decodes in hardware and
// writes it as HLS with fMP4 segments, so a player can start on the first
// segment instead of waiting for a whole file. It is a real transcode: see
// docs/api.md "Compatible video renditions (HLS)" for the cost.
//
// There is one variant per offered height, each its own cache entry derived
// independently on demand — the client picks. Height also picks the codec:
// H.264 up to 1080p, HEVC above it, because a 4K x264 encode is enormous for
// what it delivers and every Apple device that can drive a 4K panel decodes
// HEVC in hardware.
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

	// HLSDefaultHeight is the rendition a client gets when it does not ask
	// for one: the best height that is neither a 4K transcode nor a
	// compromise on a phone. It is also what the legacy routes without a height
	// and `hls_url` point at.
	HLSDefaultHeight = 1080
	// hlsSegmentSeconds is the target segment length. Shorter segments reach
	// the first frame sooner and cost more requests; 4 s is the usual balance
	// and matches Apple's own recommendation (6 s max).
	hlsSegmentSeconds = "4"
	// hlsGOP forces a keyframe at least every 96 frames (4 s at 24 fps) so the
	// muxer can always cut a segment where it wants to.
	hlsGOP = "96"
	// hlsVAAPIQP is the constant quantiser for the hardware encoder, matched
	// to the software path's crf 23 so the two renditions look alike. QP and
	// CRF are not the same scale, but for H.264 at 1080p they land close
	// enough that a viewer cannot tell which path produced what they are
	// watching — which is the whole requirement here.
	hlsVAAPIQP = "23"
	// hlsVAAPIHEVCQP is the same idea for the HEVC rungs, matched to the
	// software path's crf 26. HEVC carries the same picture at a higher
	// quantiser than H.264 does, which is the entire reason the tall
	// renditions use it.
	hlsVAAPIHEVCQP = "25"
)

// The two codecs a rendition can be in, as reported to clients in
// `hls_variants[].codec`. They are part of the API.
const (
	HLSCodecH264 = "h264"
	HLSCodecHEVC = "hevc"
)

// The software encoders behind those codecs. They double as attempt names in
// the ladder, so a log line says which encoder ran.
const (
	hlsSoftwareH264 = "libx264"
	hlsSoftwareHEVC = "libx265"
)

// hlsHeights is every rendition height, tallest first. A video offers the ones
// its source can fill; see HLSOfferedHeights.
var hlsHeights = []int{2160, 1440, 1080, 720, 480}

// HLSHeights returns the full ladder, tallest first.
func HLSHeights() []int { return slices.Clone(hlsHeights) }

// ValidHLSHeight reports whether h is one of the rendition heights. Routes use
// it to reject anything else before it reaches the filesystem.
func ValidHLSHeight(h int) bool { return slices.Contains(hlsHeights, h) }

// HLSOfferedHeights is the ladder a video of this source height offers,
// tallest first: every height the source can fill, so nothing is upscaled into
// a bigger file with no more detail in it.
//
// An unknown source height (TA parsed no video stream) offers 1080 and below —
// the default rendition still has to be reachable, and the scaler clamps to the
// source anyway. A source shorter than the lowest rung still offers that rung,
// for the same reason: the point of the variant is a playable codec, and a
// 360p source transcoded at "480" is simply 360p H.264.
func HLSOfferedHeights(sourceHeight int) []int {
	limit := sourceHeight
	if limit <= 0 {
		limit = HLSDefaultHeight
	}
	var out []int
	for _, h := range hlsHeights {
		if h <= limit {
			out = append(out, h)
		}
	}
	if len(out) == 0 {
		out = []int{hlsHeights[len(hlsHeights)-1]}
	}
	return out
}

// HLSDefaultOffered is the height `hls_url` points at: the default when the
// video offers it, and the tallest it does offer when the source is smaller.
func HLSDefaultOffered(sourceHeight int) int {
	offered := HLSOfferedHeights(sourceHeight)
	if slices.Contains(offered, HLSDefaultHeight) {
		return HLSDefaultHeight
	}
	return offered[0]
}

// HLSCodecForHeight is the codec a rendition of that height is encoded in.
// Above 1080p that is HEVC: an H.264 encode of 4K is several times the file
// for a worse picture, and HEVC hardware decode is universal on Apple devices
// from the iPhone 7 and the Apple TV 4K on — which is everything that can show
// those heights anyway.
func HLSCodecForHeight(height int) string {
	if height > HLSDefaultHeight {
		return HLSCodecHEVC
	}
	return HLSCodecH264
}

// hlsSoftwareEncoder is the CPU encoder for a height's codec — the last rung
// of the ladder, the one that always works.
func hlsSoftwareEncoder(height int) string {
	if HLSCodecForHeight(height) == HLSCodecHEVC {
		return hlsSoftwareHEVC
	}
	return hlsSoftwareH264
}

// HLSName is the cache entry (a directory) for one video's rendition at one
// height. Every height is its own entry, derived, cached and evicted on its
// own — asking for 720p must not wait on someone else's 4K job.
func HLSName(videoID string, height int) string {
	return HLSVariant + "-" + strconv.Itoa(height) + "-" + videoID
}

// HLSSource is what the source file holds, as TubeArchivist reports it in the
// video document's `streams`. It decides copy vs re-encode; it is metadata,
// not a guarantee, which is why a failed copy falls back to encoding.
type HLSSource struct {
	VideoCodec string
	Height     int
	AudioCodec string
}

// HLS returns a DirDeriveFunc that writes index.m3u8, init.mp4 and the
// segments of the rendition at height into dir.
//
// A track is copied when the source already is what this rendition would
// encode (the same height in the height's codec, or AAC audio): segmenting a
// stream copy is nearly free, so a compatible archive costs no more than the
// audio variants do. Otherwise the track is encoded for real — on the GPU when
// hw says so, and on the CPU when it does not or when the GPU turns out not to
// manage this particular source.
func HLS(ffmpegPath string, src HLSSource, height int, hw HWAccel, log *slog.Logger, source SourceFunc) DirDeriveFunc {
	return func(ctx context.Context, dir string) error {
		if err := runHLSAttempts(ctx, ffmpegPath, dir, source, hlsAttempts(src, height, hw), log); err != nil {
			return err
		}
		return ensureEndList(filepath.Join(dir, HLSPlaylistName))
	}
}

// hlsAttempt is one ffmpeg run, named so a log line and an error can say which
// of them failed.
type hlsAttempt struct {
	name string
	args []string
}

// hlsAttempts is the fallback ladder, cheapest first. Each rung is strictly
// more likely to work than the one before it and strictly more expensive, and
// the last one — a plain software encode — is the one that always works. That
// ordering is what makes the failure of any earlier rung a non-event.
//
//   - copy, when TA's metadata says the tracks are already what this rendition
//     would produce. Metadata is not a guarantee, so a muxer that refuses it
//     falls through rather than failing the request.
//   - vaapi, when a GPU was resolved at start-up. It can fail for a source the
//     fixed-function decoder does not handle (10-bit AV1, most often) or
//     because the device went away; neither is the user's problem.
//   - libx264 or libx265, per the height's codec. If this fails, the source
//     really is broken.
func hlsAttempts(src HLSSource, height int, hw HWAccel) []hlsAttempt {
	vc, ac := hlsVideoCodec(src, height), aacCodec(src.AudioCodec)
	var out []hlsAttempt
	if vc == "copy" || ac == "copy" {
		out = append(out, hlsAttempt{name: "copy", args: hlsArgs(vc, ac, height)})
	}
	if hw.VAAPI {
		out = append(out, hlsAttempt{name: "vaapi", args: hlsVAAPIArgs(hw.Device, "aac", height)})
	}
	sw := hlsSoftwareEncoder(height)
	return append(out, hlsAttempt{name: sw, args: hlsArgs(sw, "aac", height)})
}

// runHLSAttempts walks the ladder until one rung produces a rendition.
//
// Between rungs the directory is emptied: the playlist is written incrementally
// and would otherwise carry the abandoned attempt's segments. The error it
// finally returns leads with the *last* failure — the software encode — because
// that is the one that means the source is unusable; the earlier failures ride
// along as context so a broken GPU is still visible in the log.
func runHLSAttempts(ctx context.Context, ffmpegPath, dir string, source SourceFunc, attempts []hlsAttempt, log *slog.Logger) error {
	var (
		lastName string
		lastErr  error
		earlier  []string
	)
	for i, a := range attempts {
		if i > 0 {
			if clearErr := clearDir(dir); clearErr != nil {
				return fmt.Errorf("derive hls: %s failed (%w) and clearing the partial output failed: %w", lastName, lastErr, clearErr)
			}
		}
		err := runFFmpegIn(ctx, ffmpegPath, dir, source, a.args, log)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			// Cancelled or out of time: nothing to learn from another attempt,
			// and the next one would only be killed too.
			return fmt.Errorf("derive hls: %s: %w", a.name, err)
		}
		if lastErr != nil {
			earlier = append(earlier, lastName+": "+lastErr.Error())
		}
		lastName, lastErr = a.name, err
		if i+1 < len(attempts) && log != nil {
			log.Warn("hls attempt failed, falling back",
				"entry", filepath.Base(dir), "attempt", a.name, "next", attempts[i+1].name, "err", err)
		}
	}
	if len(earlier) > 0 {
		return fmt.Errorf("derive hls: %s failed: %w (earlier attempts: %s)", lastName, lastErr, strings.Join(earlier, "; "))
	}
	return fmt.Errorf("derive hls: %s failed: %w", lastName, lastErr)
}

// hlsVideoCodec copies a source that already is this rendition — the same
// height, in the codec this height is encoded in — and encodes everything
// else. The height must match exactly: a 1080p source is not the 720p
// rendition, and copying it would hand a client a stream twice the size of the
// one it asked for. An unknown height means unknown dimensions, so it is
// encoded (and scaled) rather than trusted.
func hlsVideoCodec(src HLSSource, height int) string {
	if src.Height > 0 && src.Height == height && isSourceCodec(src.VideoCodec, HLSCodecForHeight(height)) {
		return "copy"
	}
	return hlsSoftwareEncoder(height)
}

// isSourceCodec reports whether the source track is already in the rendition's
// codec.
func isSourceCodec(sourceCodec, want string) bool {
	if want == HLSCodecHEVC {
		return isHEVC(sourceCodec)
	}
	return isH264(sourceCodec)
}

// isH264 recognises the ways an H.264 track is named. TA reports the ISO-BMFF
// sample entry ("avc1", sometimes with a profile suffix such as
// "avc1.640028"); ffprobe and yt-dlp say "h264".
func isH264(codec string) bool {
	c := strings.ToLower(strings.TrimSpace(codec))
	name, _, _ := strings.Cut(c, ".")
	return name == "avc1" || name == "h264"
}

// isHEVC recognises the ways an H.265 track is named: the ISO-BMFF sample
// entries TA reports ("hvc1"/"hev1", sometimes with a profile suffix such as
// "hvc1.1.6.L120.90") and ffprobe's and yt-dlp's "hevc"/"h265".
func isHEVC(codec string) bool {
	c := strings.ToLower(strings.TrimSpace(codec))
	name, _, _ := strings.Cut(c, ".")
	return name == "hvc1" || name == "hev1" || name == "hevc" || name == "h265"
}

// hlsArgs builds the ffmpeg command line. The output names are relative
// because the command runs with the entry directory as its working directory —
// which is also what keeps them valid as playlist URLs.
//
// The source is read from stdin, as the audio variants are, so the API token
// never reaches argv or a log line. A linear transcode never seeks backwards,
// so an unseekable input costs nothing here.
func hlsArgs(videoCodec, audioCodec string, height int) []string {
	args := []string{
		"-hide_banner", "-loglevel", "error",
		"-i", "pipe:0",
		"-map", "0:v:0", "-map", "0:a:0",
	}
	switch videoCodec {
	case "copy":
		args = append(args, "-c:v", "copy")
		if HLSCodecForHeight(height) == HLSCodecHEVC {
			// Even a copied HEVC track needs the hvc1 sample entry: ffmpeg
			// will happily write hev1 into fMP4, and AVFoundation refuses it.
			args = append(args, "-tag:v", "hvc1")
		}
	case hlsSoftwareHEVC:
		args = append(args,
			"-vf", hlsScaleFilter(height),
			"-c:v", hlsSoftwareHEVC,
			// crf 26 in HEVC is roughly crf 23 in H.264 — the same picture in
			// a smaller file, which is what makes a 4K rendition affordable at
			// all. veryfast for the same reason as the H.264 path: this
			// variant optimises time to first frame.
			"-preset", "veryfast", "-crf", "26",
			// Main (8-bit 4:2:0) with the hvc1 tag is what Apple hardware
			// decodes, from the iPhone 7 and the Apple TV 4K on.
			"-profile:v", "main", "-tag:v", "hvc1", "-pix_fmt", "yuv420p",
			"-g", hlsGOP, "-keyint_min", hlsGOP,
			// x265 writes its own banner and per-frame chatter to stderr,
			// which -loglevel does not reach. Left on, it would be the first
			// 500 characters of any error this encoder produces — that is,
			// instead of the reason it failed.
			"-x265-params", "log-level=error",
		)
	default:
		args = append(args,
			"-vf", hlsScaleFilter(height),
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
	return hlsOutputArgs(args, audioCodec)
}

// hlsScaleFilter scales to the rendition's height. -2 keeps the width even
// (every encoder here needs it) and the aspect ratio; min() means a source
// shorter than the rung is never upscaled — which only happens for a source
// whose height TA did not report, since a video offers no rung taller than
// itself.
func hlsScaleFilter(height int) string {
	return "scale=-2:'min(" + strconv.Itoa(height) + ",ih)'"
}

// hlsVAAPIArgs is hlsArgs' hardware twin: the same input, the same audio and
// the same HLS muxer, with the decode, the scale and the encode moved onto the
// GPU. The frames never leave VA surfaces between the three, which is where
// most of the win is — a round trip through system memory per frame would give
// much of it back.
//
// Deliberately absent, next to the software path:
//
//   - -pix_fmt yuv420p. The pixel format is the filter's format=nv12 (4:2:0,
//     which is what the encoder and every Apple decoder want); naming a
//     software format here would force a download off the GPU.
//   - -preset. h264_vaapi has no such knob: the encoder is fixed silicon, and
//     -quality (its nearest equivalent) is left at the driver's default.
//   - -sc_threshold. It is a generic AVCodecContext option, so ffmpeg accepts
//     it here and it does nothing — VAAPI has no scene-cut detection to turn
//     off. -g/-keyint_min already pin the GOP the segmenter needs, so it is
//     dropped rather than carried as a lie about what ran.
func hlsVAAPIArgs(device, audioCodec string, height int) []string {
	args := []string{
		"-hide_banner", "-loglevel", "error",
		// Hardware decode, with the decoded frames left on the GPU. A source
		// the fixed-function decoder cannot take (10-bit AV1, say) fails here,
		// which is exactly the failure the software fallback exists for.
		"-hwaccel", "vaapi", "-hwaccel_device", device, "-hwaccel_output_format", "vaapi",
		"-i", "pipe:0",
		"-map", "0:v:0", "-map", "0:a:0",
		// The GPU-side hlsScaleFilter. w=-2 keeps the width even and the
		// aspect ratio; min() means a source shorter than the rung is not
		// upscaled.
		"-vf", "scale_vaapi=w=-2:h='min(" + strconv.Itoa(height) + ",ih)':format=nv12",
	}
	if HLSCodecForHeight(height) == HLSCodecHEVC {
		args = append(args,
			"-c:v", "hevc_vaapi",
			// CQP as on the H.264 path, at the quantiser matching the software
			// path's crf 26.
			"-rc_mode", "CQP", "-qp", hlsVAAPIHEVCQP,
			// Main 4:2:0 with the hvc1 sample entry: Apple's decoders will not
			// touch hev1 in fMP4, and the encoder does not pick the tag.
			"-profile:v", "main", "-tag:v", "hvc1",
		)
	} else {
		args = append(args,
			"-c:v", "h264_vaapi",
			// Constant-quality, the closest thing to x264's crf: quality is
			// pinned and the bitrate goes where it must, instead of a bitrate
			// cap that would starve a busy 1080p scene. Intel's encoder is
			// less efficient than x264 at equal quality, so a QP matching the
			// software crf trades a somewhat larger rendition for the same
			// picture — the right way round for a cache that is thrown away
			// anyway.
			"-rc_mode", "CQP", "-qp", hlsVAAPIQP,
			// High@4.1 4:2:0, as on the software path: what every Apple device
			// since the 4S decodes in hardware.
			"-profile:v", "high", "-level", "4.1",
		)
	}
	args = append(args, "-g", hlsGOP, "-keyint_min", hlsGOP)
	return hlsOutputArgs(args, audioCodec)
}

// hlsOutputArgs appends the half both paths share: the audio track and the HLS
// muxer. Keeping it in one place is what stops the hardware path drifting into
// a subtly different rendition from the software one.
func hlsOutputArgs(args []string, audioCodec string) []string {
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
