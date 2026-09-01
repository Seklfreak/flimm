package media

import (
	"image/jpeg"
	"os"
	"path/filepath"
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

// The bug this exists for: the track can only address the sheet by arithmetic,
// so a cell that is not the size the track says is a cue pointing at the wrong
// pixels — and, past the row where the sheet runs out, at no pixels at all. A
// viewer some way into a 2.40:1 video saw a scrubber full of black.
//
// The fixture is 4:3, deliberately: 16:9 is the one ratio the old code got
// right, so a test that used it would have passed throughout.
func TestPreviewSheetIsTheGridTheTrackDescribes(t *testing.T) {
	dir := t.TempDir()
	body := buildFixture(t, dir, 8)

	out := filepath.Join(dir, "preview")
	if err := os.MkdirAll(out, 0o750); err != nil {
		t.Fatal(err)
	}
	const duration = 8
	if err := Preview("ffmpeg", duration, nil, testSource(body))(t.Context(), out); err != nil {
		t.Fatalf("derive: %v", err)
	}

	f, err := os.Open(filepath.Join(out, PreviewSheetName)) //nolint:gosec // test fixture path
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cfg, err := jpeg.DecodeConfig(f)
	if err != nil {
		t.Fatalf("the sheet is not a readable jpeg: %v", err)
	}

	plan := PlanPreview(duration)
	wantW, wantH := plan.Columns*previewTileWidth, plan.Rows*previewTileHeight
	if cfg.Width != wantW || cfg.Height != wantH {
		t.Errorf("sheet is %dx%d, but the track addresses a %dx%d grid — every cue past the first row points at the wrong pixels",
			cfg.Width, cfg.Height, wantW, wantH)
	}
}

// The cache entry carries the cell size, so a sheet built for one grid can
// never be found and served under another. Getting this wrong is silent: the
// old sheet is on disk, complete, and wrong.
func TestPreviewVariantNamesItsCell(t *testing.T) {
	if want := "preview-160x90"; PreviewVariant != want {
		t.Errorf("PreviewVariant = %q, want %q", PreviewVariant, want)
	}
	if previewTileWidth != 160 || previewTileHeight != 90 {
		t.Errorf("the cell is %dx%d but the cache entry is still called %q — change both together",
			previewTileWidth, previewTileHeight, PreviewVariant)
	}
}
