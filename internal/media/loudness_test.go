package media

import (
	"math"
	"testing"
)

// What the filter actually prints, tail and all: the report is the last thing
// in a run that has plenty else to say.
const loudnormStderr = `Input #0, matroska,webm, from 'http://127.0.0.1:1/x':
  Duration: 00:40:12.00, start: 0.000000, bitrate: 1200 kb/s
[Parsed_loudnorm_0 @ 0x600001f0c000]
{
	"input_i" : "-13.60",
	"input_tp" : "-0.50",
	"input_lra" : "5.20",
	"input_thresh" : "-23.66",
	"output_i" : "-18.03",
	"target_offset" : "0.03"
}
`

func TestParseLoudnormReadsTheReportAtTheEnd(t *testing.T) {
	got, err := parseLoudnorm(loudnormStderr)
	if err != nil {
		t.Fatal(err)
	}
	if got.MeasuredLUFS != -13.6 || got.PeakDBTP != -0.5 || got.RangeLU != 5.2 {
		t.Errorf("measurement = %+v", got)
	}
	if got.TargetLUFS != TargetLUFS {
		t.Errorf("target = %v, want %v", got.TargetLUFS, TargetLUFS)
	}
	// -13.6 is 4.4 dB above the target, but the peak is only 0.5 dB under the
	// ceiling — and attenuating never threatens the ceiling, so the target is
	// what binds.
	if math.Abs(got.GainDB-(-4.4)) > 0.001 {
		t.Errorf("gain = %v, want -4.4", got.GainDB)
	}
	if math.Abs(got.Scale()-0.6026) > 0.001 {
		t.Errorf("scale = %v, want about 0.6026", got.Scale())
	}
}

func TestParseLoudnormRefusesOutputWithNoReport(t *testing.T) {
	for _, in := range []string{"", "ffmpeg version 8.0\n", "{ not json }"} {
		if _, err := parseLoudnorm(in); err == nil {
			t.Errorf("parseLoudnorm(%q) succeeded, want an error", in)
		}
	}
}

// A silent track reports "-inf", which is not a number and not something to
// normalise.
func TestASilentTrackIsLeftAlone(t *testing.T) {
	if _, err := parseLoudnorm(`{"input_i":"-inf","input_tp":"-inf","input_lra":"0.00"}`); err == nil {
		t.Error("a silent measurement should be refused, not stored as a gain")
	}
	if got := GainFor(-70, -20); got != 0 {
		t.Errorf("GainFor(silence) = %v, want 0", got)
	}
}

// The gain is only ever a reduction, and only ever as far as the smaller of the
// two limits allows.
func TestGainIsTheSmallerLimitAndNeverABoost(t *testing.T) {
	cases := []struct {
		name           string
		measured, peak float64
		want           float64
	}{
		{"a loud video comes down to the target", -12, -3, -6},
		{"a quiet one is left alone rather than boosted", -25, -6, 0},
		{"one already at the target does nothing", TargetLUFS, -2, 0},
		{"the floor bounds something pathological", -0.5, -0.1, gainFloorDB},
	}
	for _, c := range cases {
		if got := GainFor(c.measured, c.peak); math.Abs(got-c.want) > 0.001 {
			t.Errorf("%s: GainFor(%v, %v) = %v, want %v", c.name, c.measured, c.peak, got, c.want)
		}
	}
}

// Applying the gain must never push a video past the ceiling — the check that
// matters if the target ever moves up.
func TestGainNeverExceedsThePeakCeiling(t *testing.T) {
	for _, measured := range []float64{-30, -20, -18, -10, -5} {
		for _, peak := range []float64{-6, -1, -0.2, 0.8} {
			gain := GainFor(measured, peak)
			if peak+gain > PeakCeilingDBTP+0.001 && gain != 0 {
				t.Errorf("measured %v peak %v: gain %v lands at %v dBTP", measured, peak, gain, peak+gain)
			}
			if gain > 0 {
				t.Errorf("measured %v peak %v: gain %v is a boost", measured, peak, gain)
			}
		}
	}
}
