package media

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// SourceFunc opens the muxed source file. It is called per attempt because a
// stream can only be consumed once, and the fallback below needs a second pass.
type SourceFunc func(ctx context.Context) (io.ReadCloser, error)

// AudioVariant is the cache key prefix and file extension for audio-only.
const (
	AudioVariant = "audio"
	AudioExt     = ".webm"
	AudioType    = "audio/webm"
)

// Audio returns a DeriveFunc that strips the video track.
//
// The archive stores Opus audio, so the first attempt copies the stream
// (`-c:a copy`): no re-encode, no quality loss, negligible CPU, and roughly
// 20–30× less data than the muxed source. WebM accepts only Opus and Vorbis,
// so a source with, say, AAC audio fails that copy — hence the re-encode
// fallback, which keeps mixed libraries working instead of 500ing.
func Audio(ffmpegPath string, source SourceFunc) DeriveFunc {
	return func(ctx context.Context, dst string) error {
		err := runFFmpeg(ctx, ffmpegPath, source, dst, "copy")
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return err
		}
		if reErr := runFFmpeg(ctx, ffmpegPath, source, dst, "libopus"); reErr != nil {
			return fmt.Errorf("derive audio: copy failed (%w) and re-encode failed: %w", err, reErr)
		}
		return nil
	}
}

func runFFmpeg(ctx context.Context, ffmpegPath string, source SourceFunc, dst, codec string) error {
	src, err := source(ctx)
	if err != nil {
		return fmt.Errorf("derive audio: open source: %w", err)
	}
	defer src.Close()

	args := []string{
		"-hide_banner", "-loglevel", "error",
		"-i", "pipe:0",
		"-vn", "-map", "0:a:0",
		"-c:a", codec,
	}
	if codec != "copy" {
		args = append(args, "-b:a", "128k")
	}
	// The source is faststart (moov up front), so a streamed, unseekable input
	// remuxes fine — which is what lets the file be piped in rather than the
	// API token being handed to ffmpeg on a command line.
	args = append(args, "-f", "webm", "-y", dst)

	// ffmpegPath comes from configuration, and every argument is either a
	// literal or a path this package generated — no request data reaches argv.
	cmd := exec.CommandContext(ctx, ffmpegPath, args...) //nolint:gosec // G204: operator-supplied binary, fixed args
	cmd.Stdin = src
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 500 {
			msg = msg[:500]
		}
		return fmt.Errorf("ffmpeg -c:a %s: %w: %s", codec, err, msg)
	}
	return nil
}
