package media

import (
	"context"
	"fmt"
	"strings"
)

// AudioAACVariant is the cache key prefix and file extension for the
// Apple-compatible audio-only rendition.
const (
	AudioAACVariant = "audio-aac"
	AudioAACExt     = ".m4a"
	AudioAACType    = "audio/mp4"
)

// AudioAAC returns a DeriveFunc producing AAC audio in an MP4 container.
//
// This exists because AVFoundation cannot decode Opus in WebM, which is what
// the `audio` variant is for most of the archive — so Apple clients have
// nothing to play there. sourceCodec is the codec of the source's audio track
// as TubeArchivist reports it: when it is already AAC the track is copied and
// this costs the same as the WebM variant, and otherwise it is a genuine
// re-encode, so the first listener waits and the CPU is spent for real.
//
// A copy can still fail — the codec string is metadata, not a guarantee — so a
// failed copy falls back to encoding rather than failing the request.
func AudioAAC(ffmpegPath, sourceCodec string, source SourceFunc) DeriveFunc {
	return func(ctx context.Context, dst string) error {
		codec := aacCodec(sourceCodec)
		err := runFFmpeg(ctx, ffmpegPath, source, codec, aacArgs(codec, dst))
		if err == nil {
			return nil
		}
		if codec != "copy" {
			return fmt.Errorf("derive audio-aac: %w", err)
		}
		if ctx.Err() != nil {
			return err
		}
		if reErr := runFFmpeg(ctx, ffmpegPath, source, "aac", aacArgs("aac", dst)); reErr != nil {
			return fmt.Errorf("derive audio-aac: copy failed (%w) and re-encode failed: %w", err, reErr)
		}
		return nil
	}
}

// aacCodec picks stream copy over re-encoding when the source is already AAC.
func aacCodec(sourceCodec string) string {
	if isAAC(sourceCodec) {
		return "copy"
	}
	return "aac"
}

// isAAC recognises the ways an AAC track is named. TA reports the ISO-BMFF
// sample entry ("mp4a", sometimes with a profile suffix such as
// "mp4a.40.2"); ffprobe and yt-dlp say "aac".
func isAAC(codec string) bool {
	c := strings.ToLower(strings.TrimSpace(codec))
	name, _, _ := strings.Cut(c, ".")
	return name == "mp4a" || name == "aac"
}

// aacArgs builds the ffmpeg command line for the `audio-aac` variant.
// +faststart moves the moov atom to the front of the finished file, which is
// what makes byte-range seeking work for a player that has only the first few
// hundred kilobytes.
func aacArgs(codec, dst string) []string {
	args := audioInputArgs(codec)
	return append(args, "-movflags", "+faststart", "-f", "mp4", "-y", dst)
}
