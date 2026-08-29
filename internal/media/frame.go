package media

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
)

// FrameVariant and FrameExt name the derived-media entry for a single frame.
const (
	FrameVariant = "frame"
	FrameExt     = ".jpg"
	// FrameType is what the endpoint serves it as.
	FrameType = "image/jpeg"
	// frameHeight is what a thumbnail is scaled to. Every client draws these
	// at a fraction of that, and a card on an Apple TV is the largest of them.
	frameHeight = 720
	// frameQuality is ffmpeg's JPEG scale, 2..31, lower is better. 4 is
	// visually clean at this size and a fraction of the bytes of 2.
	frameQuality = "4"
)

// Frame extracts a single still from the video at `at` seconds, as a JPEG.
//
// This is what a DeArrow thumbnail *is*: the service returns a timestamp, not
// an image, so nothing is fetched from anyone — the frame is cut from the file
// the archive already holds. That is the whole reason the feature fits here
// rather than in a browser extension, and it is why a thumbnail keeps working
// with the archive offline.
//
// The seek is an input seek over the range-capable loopback (`-ss` before
// `-i`), so cutting a frame twenty minutes in reads a few hundred kilobytes
// rather than twenty minutes of video.
func Frame(ffmpegPath string, at float64, log *slog.Logger, open RangeSourceFunc) DeriveFunc {
	return func(ctx context.Context, dst string) error {
		lb, err := newLoopbackSource(log)
		if err != nil {
			return fmt.Errorf("derive frame: %w", err)
		}
		defer lb.close()
		src, release := lb.register(open)
		defer release()

		args := []string{
			"-hide_banner", "-loglevel", "error",
			// Before -i: a byte-range request rather than a decode of
			// everything up to `at`.
			"-ss", strconv.FormatFloat(max(at, 0), 'f', 3, 64),
			"-i", src,
			"-frames:v", "1",
			// A frame past the end of the file leaves ffmpeg with nothing to
			// write; -update makes the single-image output explicit either way.
			"-update", "1",
			"-vf", fmt.Sprintf("scale=-2:'min(%d,ih)'", frameHeight),
			"-qscale:v", frameQuality,
			"-f", "image2",
			"-y", dst,
		}
		// The working directory is irrelevant here — the output path is
		// absolute — but the runner is shared with the HLS path, which needs
		// one.
		if err := runFFmpegIn(ctx, ffmpegPath, "", args, log); err != nil {
			return fmt.Errorf("derive frame at %.3fs: %w", at, err)
		}
		return nil
	}
}
