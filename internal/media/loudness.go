package media

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Loudness normalisation: measuring how loud a video actually is, so a player
// can even out the difference between channels.
//
// One EBU R128 analysis pass per video (ffmpeg's `loudnorm` filter in its
// measure-only mode), the numbers written into the media cache, and the *gain*
// computed here rather than in four clients — a client is told how many
// decibels to apply and nothing else, which is the same rule the rendition
// ladder and the feed order follow.
//
// Nothing is re-encoded. The archived file is never touched: the analysis
// reads it and throws the decoded audio away.

const (
	// LoudnessVariant names the cache directory, and LoudnessName the one file
	// in it.
	LoudnessVariant = "loudness"
	LoudnessName    = "loudness.json"

	// TargetLUFS is the programme loudness clients normalise toward.
	//
	// YouTube itself attenuates anything above roughly -14 LUFS and boosts
	// nothing, so an archive sits mostly between -14 and -25. Pulling the loud
	// half down to -18 closes most of that gap while leaving enough level that
	// nobody has to reach for the volume; a lower target would even things out
	// further and make everything quiet.
	TargetLUFS = -18.0
	// PeakCeilingDBTP is the true peak no gain may push a video past. Gain is
	// only ever negative today, so this is a second lock rather than the
	// binding constraint — but it is what keeps that true if the target ever
	// moves.
	PeakCeilingDBTP = -1.0
	// gainFloorDB bounds the attenuation, so a measurement of something
	// pathological (a video that is one long clipped tone) cannot silence a
	// video outright.
	gainFloorDB = -15.0
)

// Loudness is one video's measurement, and what a player should do with it.
type Loudness struct {
	// MeasuredLUFS is the integrated programme loudness (`input_i`).
	MeasuredLUFS float64 `json:"measured_lufs"`
	// PeakDBTP is the measured true peak (`input_tp`).
	PeakDBTP float64 `json:"peak_dbtp"`
	// RangeLU is the loudness range (`input_lra`) — carried because it says
	// how compressed a video is, which is worth having when a measurement
	// looks wrong.
	RangeLU float64 `json:"range_lu"`
	// GainDB is what a player applies: the decibels to move this video by so
	// it sits at the target, never above the peak ceiling, and never a boost
	// (see GainFor).
	GainDB float64 `json:"gain_db"`
	// TargetLUFS is the target that gain was computed against, so a client
	// never has to know it and a stored measurement stays readable if the
	// target ever changes.
	TargetLUFS float64 `json:"target_lufs"`
}

// Scale is GainDB as a linear multiplier, which is what an audio API takes.
func (l Loudness) Scale() float64 { return math.Pow(10, l.GainDB/20) }

// GainFor is the gain to apply to a video measured at these numbers.
//
// It is the smaller of two limits — the distance to the target, and the
// headroom to the peak ceiling — and then clamped to zero, so a video is only
// ever turned *down*.
//
// The no-boost rule is a platform fact rather than a preference: `AVPlayer`'s
// volume tops out at 1.0 and its audio mixes do not apply to an HLS stream, so
// the Apple clients cannot amplify at all, and a web client that could would
// then be louder than the TV playing the same video. Attenuating alone still
// removes the jump between a loud channel and a quiet one, and it can never
// clip.
func GainFor(measuredLUFS, peakDBTP float64) float64 {
	if measuredLUFS <= -70 || math.IsNaN(measuredLUFS) {
		// -70 LUFS is loudnorm's floor for "silence": there is nothing here to
		// normalise, and treating it as very quiet would ask for a huge boost.
		return 0
	}
	gain := TargetLUFS - measuredLUFS
	if headroom := PeakCeilingDBTP - peakDBTP; headroom < gain {
		gain = headroom
	}
	return math.Max(math.Min(gain, 0), gainFloorDB)
}

// Measure runs the analysis pass and writes the result into the cache
// directory.
//
// `-vn` matters more than it looks: the whole file is read either way, but
// decoding only the audio is a fraction of the work, which is what makes this
// cheap enough to run on demand.
func Measure(ffmpegPath string, log *slog.Logger, open RangeSourceFunc) DirDeriveFunc {
	return func(ctx context.Context, dir string) error {
		lb, err := newLoopbackSource(log)
		if err != nil {
			return fmt.Errorf("measure loudness: %w", err)
		}
		defer lb.close()
		src, release := lb.register(open)
		defer release()

		args := []string{
			"-hide_banner", "-nostats",
			// One thread. This runs *while the viewer watches the same video*,
			// beside a transcode of it, and `loudnorm` is a software decode of
			// the whole audio track — on a long file it will happily take
			// every core it is given and slow down the encode the viewer is
			// actually waiting on. One core still analyses far faster than
			// realtime, which is all this has to be.
			"-threads", "1",
			"-i", src,
			"-vn",
			"-af", fmt.Sprintf("loudnorm=I=%s:TP=%s:print_format=json",
				strconv.FormatFloat(TargetLUFS, 'f', 1, 64),
				strconv.FormatFloat(PeakCeilingDBTP, 'f', 1, 64)),
			"-f", "null", "-",
		}
		// loudnorm prints its measurement to stderr, so this run is read
		// rather than only checked for an exit code.
		out, err := runFFmpegOutput(ctx, ffmpegPath, dir, args, log)
		if err != nil {
			return fmt.Errorf("measure loudness: %w", err)
		}
		measured, err := parseLoudnorm(out)
		if err != nil {
			return fmt.Errorf("measure loudness: %w", err)
		}
		body, err := json.Marshal(measured)
		if err != nil {
			return fmt.Errorf("measure loudness: %w", err)
		}
		return os.WriteFile(filepath.Join(dir, LoudnessName), body, 0o600)
	}
}

// ReadLoudness reads a finished measurement out of a cache directory.
func ReadLoudness(dir string) (Loudness, bool) {
	body, err := os.ReadFile(filepath.Join(dir, LoudnessName)) //nolint:gosec // dir is the cache, name is a literal
	if err != nil {
		return Loudness{}, false
	}
	var l Loudness
	if err := json.Unmarshal(body, &l); err != nil {
		return Loudness{}, false
	}
	return l, true
}

// LoudnessReady reports whether a directory holds a finished measurement.
func LoudnessReady(dir string) bool {
	_, ok := ReadLoudness(dir)
	return ok
}

// loudnormReport is what the filter prints. Every value is a *string* in that
// JSON, including the numbers, and "-inf" turns up for a silent track — which
// is why this is parsed by hand rather than into floats.
type loudnormReport struct {
	InputI   string `json:"input_i"`
	InputTP  string `json:"input_tp"`
	InputLRA string `json:"input_lra"`
}

// parseLoudnorm pulls the measurement out of ffmpeg's stderr.
//
// The filter prints a JSON object at the very end of the run, after whatever
// else ffmpeg had to say, so the last `{`…`}` in the output is the report.
func parseLoudnorm(stderr string) (Loudness, error) {
	start := strings.LastIndex(stderr, "{")
	end := strings.LastIndex(stderr, "}")
	if start < 0 || end < start {
		return Loudness{}, fmt.Errorf("loudnorm printed no measurement")
	}
	var report loudnormReport
	if err := json.Unmarshal([]byte(stderr[start:end+1]), &report); err != nil {
		return Loudness{}, fmt.Errorf("loudnorm measurement: %w", err)
	}
	measured, ok := loudnessNumber(report.InputI)
	if !ok {
		return Loudness{}, fmt.Errorf("loudnorm measurement has no integrated loudness")
	}
	peak, ok := loudnessNumber(report.InputTP)
	if !ok {
		// A track with no measurable peak is one with no audio worth
		// normalising; the ceiling then binds nothing.
		peak = PeakCeilingDBTP
	}
	rangeLU, _ := loudnessNumber(report.InputLRA)
	return Loudness{
		MeasuredLUFS: measured,
		PeakDBTP:     peak,
		RangeLU:      rangeLU,
		GainDB:       GainFor(measured, peak),
		TargetLUFS:   TargetLUFS,
	}, nil
}

// loudnessNumber reads one of the filter's string numbers. "-inf" (a silent
// track) is not a number, and saying so is the point.
func loudnessNumber(raw string) (float64, bool) {
	value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || math.IsInf(value, 0) || math.IsNaN(value) {
		return 0, false
	}
	return value, true
}
