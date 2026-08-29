package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeMeasuringFFmpeg stands in for ffmpeg's loudnorm pass: it prints a
// report on stderr, which is where the real filter puts it.
func writeMeasuringFFmpeg(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ffmpeg")
	script := "#!/bin/sh\n" +
		"cat >&2 <<'EOF'\n" +
		"[Parsed_loudnorm_0 @ 0x0]\n" +
		"{\n\t\"input_i\" : \"-11.20\",\n\t\"input_tp\" : \"-1.50\",\n\t\"input_lra\" : \"6.10\"\n}\n" +
		"EOF\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil { //nolint:gosec // test fixture must be executable
		t.Fatal(err)
	}
	return path
}

func loudnessOf(t *testing.T, h http.Handler, id string) LoudnessInfo {
	t.Helper()
	rec := do(t, h, http.MethodGet, "/api/v1/videos/"+id+"/loudness", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var out LoudnessInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// The measurement is a real decode, so the first call starts it and says so
// rather than holding the request open.
func TestLoudnessIsMeasuredInTheBackgroundAndThenReported(t *testing.T) {
	h := hlsServer(t, t.TempDir(), writeMeasuringFFmpeg(t))

	first := loudnessOf(t, h, "v1")
	if first.State == "done" {
		t.Fatalf("the first call should start the pass, not finish it: %+v", first)
	}
	if first.GainDB != 0 {
		t.Errorf("nothing is known yet, so the gain must be 0, got %v", first.GainDB)
	}
	if first.TargetLUFS == 0 {
		t.Error("the target belongs in every answer, so no client hardcodes it")
	}

	var done LoudnessInfo
	deadline := time.Now().Add(5 * time.Second)
	for {
		done = loudnessOf(t, h, "v1")
		if done.State == "done" || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if done.State != "done" {
		t.Fatalf("measurement never finished: %+v", done)
	}
	if done.MeasuredLUFS != -11.2 || done.PeakDBTP != -1.5 {
		t.Errorf("measurement = %+v", done)
	}
	// -11.2 is 6.8 dB above the -18 target; the peak leaves 0.5 dB of
	// headroom, which attenuation never needs.
	if done.GainDB > -6.79 || done.GainDB < -6.81 {
		t.Errorf("gain = %v, want about -6.8", done.GainDB)
	}
}

func TestLoudnessRefusesAVideoThatIsNotOne(t *testing.T) {
	h := hlsServer(t, t.TempDir(), writeMeasuringFFmpeg(t))
	rec := do(t, h, http.MethodGet, "/api/v1/videos/nosuchvideo/loudness", "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

// Normalisation is on unless a viewer turns it off — it asks nobody anything
// and only ever turns a video down.
func TestNormalisationIsOnByDefault(t *testing.T) {
	if !defaultPrefs().NormalizeLoudness {
		t.Error("normalize_loudness should default to on")
	}
}
