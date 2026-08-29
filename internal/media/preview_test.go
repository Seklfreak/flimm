package media

import (
	"strings"
	"testing"
)

// The grid spreads over the whole video rather than sampling the start of it,
// and never gets finer than a drag can resolve.
func TestPlanPreviewSpreadsOverTheVideo(t *testing.T) {
	long := PlanPreview(7200) // two hours
	if long.Tiles != previewMaxTiles {
		t.Errorf("tiles = %d, want the cap", long.Tiles)
	}
	if long.Interval != 36 {
		t.Errorf("interval = %v, want 36s (7200/200)", long.Interval)
	}
	if long.Columns*long.Rows < long.Tiles {
		t.Errorf("grid %dx%d cannot hold %d tiles", long.Columns, long.Rows, long.Tiles)
	}

	short := PlanPreview(60)
	if short.Interval != previewMinInterval {
		t.Errorf("interval = %v, want the floor for a short video", short.Interval)
	}
	if short.Tiles != 30 {
		t.Errorf("tiles = %d, want 30", short.Tiles)
	}
}

func TestPlanPreviewRefusesANonVideo(t *testing.T) {
	if got := PlanPreview(0); got.Tiles != 0 {
		t.Errorf("plan = %+v, want nothing to derive", got)
	}
}

// Every cue points at its own rectangle of the one sheet: that is what makes a
// single image into a hundred thumbnails.
func TestPreviewTrackAddressesEachTile(t *testing.T) {
	plan := PlanPreview(60)
	track := PreviewTrack(plan, 60)
	if !strings.HasPrefix(track, "WEBVTT\n") {
		t.Fatalf("track does not start with the WEBVTT header:\n%s", track[:20])
	}
	if got := strings.Count(track, "#xywh="); got != plan.Tiles {
		t.Errorf("cues = %d, want one per tile (%d)", got, plan.Tiles)
	}
	// The first tile is the top-left corner, the eleventh starts the second row.
	if !strings.Contains(track, "00:00:00.000 --> 00:00:02.000\nsheet.jpg#xywh=0,0,160,90") {
		t.Errorf("first cue is wrong:\n%s", track[:120])
	}
	if !strings.Contains(track, "sheet.jpg#xywh=0,90,160,90") {
		t.Error("the eleventh tile should start the second row")
	}
	// Nothing runs past the end of the video.
	if strings.Contains(track, "--> 00:01:02") {
		t.Error("a cue runs past the end of the video")
	}
}

func TestVTTTimeFormatsAsWebVTTWants(t *testing.T) {
	cases := map[float64]string{
		0:       "00:00:00.000",
		61.5:    "00:01:01.500",
		3661.25: "01:01:01.250",
	}
	for in, want := range cases {
		if got := vttTime(in); got != want {
			t.Errorf("vttTime(%v) = %q, want %q", in, got, want)
		}
	}
}
