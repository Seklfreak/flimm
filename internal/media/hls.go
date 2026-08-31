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
	"time"
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
//
// The rendition is *resume-first*: the segment grid is fixed and known from the
// duration, so the playlist is complete from the first request (see
// hlsplaylist.go) and the encoder is pointed at wherever the viewer actually
// is (see hlsplan.go and hlsjob.go) rather than always starting at 0:00.
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
	// hlsSegmentSeconds is the segment length, and with it the grid every run
	// cuts on. Shorter segments reach the first frame sooner and cost more
	// requests; 4 s is the usual balance and matches Apple's own recommendation
	// (6 s max). It is a fixed number rather than a target because the whole
	// playlist is derived from it before anything is encoded.
	hlsSegmentSeconds = 4
	// hlsGOP forces a keyframe at least every 96 frames (4 s at 24 fps). The
	// segment boundaries themselves come from -force_key_frames on the 4 s
	// grid; this is the floor underneath it.
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
// video document. It decides copy vs re-encode and how many segments the
// rendition has; it is metadata, not a guarantee, which is why a failed copy
// falls back to encoding and a missing duration falls back to a probe.
type HLSSource struct {
	VideoCodec string
	Height     int
	AudioCodec string
	// Duration is the video's length in seconds (TA's `player.duration`), the
	// number the whole segment grid comes from. 0 means TA did not report one,
	// and the source is probed instead.
	Duration float64
}

// hlsAttempt is one ffmpeg configuration, named so a log line and an error can
// say which of them failed.
type hlsAttempt struct {
	name string
	// videoCodec and audioCodec are what this rung asks ffmpeg for.
	videoCodec, audioCodec string
	// vaapi routes the rung through the GPU command line.
	vaapi bool
	// device is the render node, for the vaapi rung.
	device string
	// singleRun forces one pass over the whole video regardless of what the
	// planner wants. Only a *video* copy sets it: a stream copy cannot cut on
	// the 4 s grid (it can only cut on the source's own keyframes), so a
	// partial range would produce segments that do not line up with the
	// playlist. It costs nothing to ignore the plan there — a copy runs at
	// remux speed, so the whole file is done in the time an encode needs for
	// the first minute.
	//
	// Copying only the *audio* is a different thing entirely: the video is
	// still being encoded, with keyframes forced onto the grid, so it cuts
	// where the planner says. Setting this for an audio copy is what made a
	// resumed video encode from the beginning — an hour of waiting to reach
	// the point the viewer had asked to start at.
	singleRun bool
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
		out = append(out, hlsAttempt{name: "copy", videoCodec: vc, audioCodec: ac, singleRun: vc == "copy"})
	}
	if hw.VAAPI {
		out = append(out, hlsAttempt{name: "vaapi", videoCodec: "vaapi", audioCodec: "aac", vaapi: true, device: hw.Device})
	}
	sw := hlsSoftwareEncoder(height)
	return append(out, hlsAttempt{name: sw, videoCodec: sw, audioCodec: "aac"})
}

// args builds this rung's ffmpeg command line for one run over the segment
// grid, reading from the loopback source URL.
func (a hlsAttempt) args(src string, height int, run hlsRun) []string {
	if a.vaapi {
		return hlsVAAPIArgs(a.device, a.audioCodec, height, src, run)
	}
	return hlsArgs(a.videoCodec, a.audioCodec, height, src, run)
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

// hlsForceKeyFrames pins a keyframe on the shared 4 s grid. Without it the
// encoder puts keyframes wherever the picture asks for them, the muxer cuts at
// the nearest one, and two runs of the same video disagree about where segment
// boundaries are — which is the one thing a stitched-together rendition cannot
// survive.
//
// `t` here is the run's own output time, before -output_ts_offset, so a run
// starting at a multiple of 4 s produces boundaries on the global grid.
var hlsForceKeyFrames = "expr:gte(t,n_forced*" + strconv.Itoa(hlsSegmentSeconds) + ")"

// hlsArgs builds the ffmpeg command line. The output names are relative
// because the command runs with the entry directory as its working directory —
// which is also what keeps them valid as playlist URLs.
//
// The source is the loopback URL, not stdin: `-ss` on a pipe decodes and throws
// away everything before the seek point, so resuming at 40:00 would first cost
// 40 minutes of decoding. Over HTTP it is a byte range. The URL carries a
// one-time nonce and no credential, so the TA token still never reaches argv or
// a log line — see loopback.go.
func hlsArgs(videoCodec, audioCodec string, height int, src string, run hlsRun) []string {
	args := []string{"-hide_banner", "-loglevel", "error"}
	args = append(args, hlsSeekArgs(run)...)
	args = append(args,
		"-i", src,
		"-map", "0:v:0", "-map", "0:a:0",
	)
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
			"-force_key_frames", hlsForceKeyFrames,
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
			"-force_key_frames", hlsForceKeyFrames,
		)
	}
	return hlsOutputArgs(args, audioCodec, run)
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
// -force_key_frames is honoured here as it is on the software path: it is
// applied by the generic encode layer, which flags the frame as a keyframe
// before handing it to h264_vaapi/hevc_vaapi, not by anything the fixed-function
// encoder chooses for itself.
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
func hlsVAAPIArgs(device, audioCodec string, height int, src string, run hlsRun) []string {
	args := []string{
		"-hide_banner", "-loglevel", "error",
		// Hardware decode, with the decoded frames left on the GPU. A source
		// the fixed-function decoder cannot take (10-bit AV1, say) fails here,
		// which is exactly the failure the software fallback exists for.
		"-hwaccel", "vaapi", "-hwaccel_device", device, "-hwaccel_output_format", "vaapi",
	}
	args = append(args, hlsSeekArgs(run)...)
	args = append(args,
		"-i", src,
		"-map", "0:v:0", "-map", "0:a:0",
		// The GPU-side hlsScaleFilter. w=-2 keeps the width even and the
		// aspect ratio; min() means a source shorter than the rung is not
		// upscaled.
		"-vf", "scale_vaapi=w=-2:h='min("+strconv.Itoa(height)+",ih)':format=nv12",
	)
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
	args = append(args, "-g", hlsGOP, "-keyint_min", hlsGOP, "-force_key_frames", hlsForceKeyFrames)
	return hlsOutputArgs(args, audioCodec, run)
}

// hlsSeekArgs is the input-side seek: `-ss` *before* `-i`, which over HTTP is a
// byte-range request and lands in milliseconds. After `-i` it would be an
// output-side seek — decode everything and throw it away — which is the cost
// this whole design exists to avoid.
func hlsSeekArgs(run hlsRun) []string {
	if run.startSeconds() <= 0 {
		return nil
	}
	return []string{"-ss", strconv.Itoa(run.startSeconds())}
}

// hlsOutputArgs appends the half every path shares: how much of the timeline
// the run covers, the audio track and the HLS muxer. Keeping it in one place is
// what stops the hardware path drifting into a subtly different rendition from
// the software one.
//
// Deliberately absent: -output_ts_offset. It does not move the segments — the
// muxer still numbers its fragments from zero — it writes an empty edit into
// the init segment instead, and that edit then applies to every segment in the
// rendition, including the ones another run wrote. The offset is applied to the
// finished segments instead; see hlsrebase.go.
func hlsOutputArgs(args []string, audioCodec string, run hlsRun) []string {
	if d := run.durationSeconds(); d > 0 {
		args = append(args, "-t", strconv.Itoa(d))
	}
	args = append(args, "-c:a", audioCodec)
	if audioCodec != "copy" {
		args = append(args, "-b:a", "160k", "-ac", "2")
	}
	return append(args,
		"-threads", "0",
		"-f", "hls",
		// With a forced keyframe on every 4 s boundary the muxer cuts exactly
		// there, so no split_by_time is needed: hls_time finds a keyframe
		// waiting for it at each boundary.
		"-hls_time", strconv.Itoa(hlsSegmentSeconds),
		// Each run covers a fixed stretch of a playlist Flimm writes itself
		// (index.m3u8), so ffmpeg's own list is a by-product read only for its
		// segment durations. vod keeps every entry in it.
		"-hls_playlist_type", "vod",
		"-hls_segment_type", "fmp4",
		// temp_file publishes each segment by rename, so a player never reads
		// one that is still being written — and a cancelled run leaves a .tmp
		// rather than a truncated segment.
		"-hls_flags", "independent_segments+temp_file",
		"-hls_fmp4_init_filename", run.initName,
		// A run that has to be put back on the timeline writes its segments
		// under a name the route will not serve, and each is published once it
		// has been. A run that starts at zero writes them directly.
		"-hls_segment_filename", run.segmentPattern(),
		// Segments are numbered by their place on the shared grid, not by
		// their place in this run.
		"-start_number", strconv.Itoa(run.seg.Start),
		"-y", run.playlist,
	)
}

// HLSPlaylistReady reports whether the playlist can be handed to a player:
// it exists and names at least one segment. Since Flimm writes the playlist
// itself, complete, before the first run starts, this is true within
// milliseconds of the job starting rather than after the first segment.
func HLSPlaylistReady(dir string) bool {
	b, err := readPlaylist(filepath.Join(dir, HLSPlaylistName))
	if err != nil {
		return false
	}
	return bytes.Contains(b, []byte(".m4s"))
}

// maxPlaylistBytes caps a playlist read. An hour of 4 s segments is ~30 KB, so
// anything near this is not a playlist.
const maxPlaylistBytes = 4 << 20

func readPlaylist(path string) ([]byte, error) {
	f, err := os.Open(path) //nolint:gosec // path built from the cache dir
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

// runFFmpegIn runs ffmpeg with dir as the working directory, so the muxer's
// relative output names land inside the cache entry. Nothing is piped in: the
// input is the loopback URL in args.
func runFFmpegIn(ctx context.Context, ffmpegPath, dir string, args []string, log *slog.Logger) error {
	_, err := runFFmpegOutput(ctx, ffmpegPath, dir, args, log)
	return err
}

// runFFmpegOutput is runFFmpegIn for a run whose *output* matters: ffmpeg
// writes everything it has to say — including a filter's measurements — to
// stderr, so an analysis pass reads what a transcode only checks the exit code
// of. The returned text is scrubbed like the logged one.
func runFFmpegOutput(ctx context.Context, ffmpegPath, dir string, args []string, log *slog.Logger) (string, error) {
	// ffmpegPath comes from configuration and every argument is a literal, a
	// number this package computed or a loopback URL with a random nonce — no
	// request data, and no token, reaches argv.
	cmd := exec.CommandContext(ctx, ffmpegPath, args...) //nolint:gosec // G204: operator-supplied binary, generated args
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	// Without this, killing a run waits for every process holding the stderr
	// pipe to let go of it — including anything the shutting-down ffmpeg
	// forked. Re-aiming a run cancels ffmpeg routinely now, so a job goroutine
	// pinned on a stray child is a real way to lose a transcode slot forever.
	cmd.WaitDelay = 10 * time.Second
	runErr := cmd.Run()

	out := scrubSecrets(strings.TrimSpace(stderr.String()))
	if out != "" && log != nil {
		log.Debug("ffmpeg", "entry", filepath.Base(dir), "stderr", out)
	}
	if runErr != nil {
		// A run the context ended is not an ffmpeg fault. Shutdown cancels
		// every derivation at once and the SIGKILL that follows arrives as an
		// *exec.ExitError ("signal: killed") that says nothing about why —
		// which is how an ordinary deploy filled Sentry with one report per
		// rendition. Wrapping the context's own error keeps the cause honest:
		// a cancelled run is dropped as the non-event it is, while a run that
		// outlived transcodeTimeout still reports as a real fault.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return out, fmt.Errorf("ffmpeg: %w", ctxErr)
		}
		msg := out
		if len(msg) > 500 {
			msg = msg[:500]
		}
		return out, fmt.Errorf("ffmpeg: %w: %s", runErr, msg)
	}
	return out, nil
}

// secretPattern matches the shapes a credential could take if one ever reached
// ffmpeg's stderr. Nothing should: the loopback source holds the token and
// hands ffmpeg a nonce. This is the second lock on the door — an error message
// is not a place to learn the TA token from.
var secretPattern = regexp.MustCompile(`(?i)(authorization:.*|\btoken[=:]\s*\S+|\btoken\s+[A-Za-z0-9._~+/-]{8,})`)

func scrubSecrets(s string) string { return secretPattern.ReplaceAllString(s, "[redacted]") }
