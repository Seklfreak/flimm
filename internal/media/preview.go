package media

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Scrub previews: the little picture that appears above the scrubber while you
// drag it.
//
// One sprite sheet of small stills plus a WebVTT track saying which tile
// belongs to which second — the format every web player and `AVPlayer`'s own
// trick-play understand, and the reason it is a sheet rather than a thousand
// files: a scrub drags through dozens of positions a second, and one image
// already in the browser's memory answers all of them.
//
// Derived once per video, on the first request, into the same cache as every
// other derivation. It is the most expensive thing in that cache per unit of
// use — a full decode of the file — which is why nothing derives it until a
// player actually asks.

const (
	// PreviewVariant names the cache directory.
	PreviewVariant = "preview"
	// PreviewSheetName and PreviewTrackName are what lands in it.
	PreviewSheetName = "sheet.jpg"
	PreviewTrackName = "preview.vtt"

	// previewTileWidth is each still's width. 160 is what a scrubber shows at
	// roughly life size on a phone and comfortably on a desktop; the sheet
	// grows with the square of this, so it is the number that decides whether
	// a two-hour video costs 300 KB or 3 MB.
	previewTileWidth = 160
	// previewColumns is the sheet's width in tiles. Ten keeps a sheet under
	// the 4096-pixel texture limit browsers and Metal are happiest with.
	previewColumns = 10
	// previewMaxTiles caps the sheet: 200 stills is a preview every 36 seconds
	// of a two-hour video, and a sheet of 1600×1800.
	previewMaxTiles = 200
	// previewMinInterval keeps a short video from being sampled absurdly
	// finely — two seconds apart is already finer than a drag can resolve.
	previewMinInterval = 2.0
)

// PreviewPlan is the grid for one video: how far apart the stills are, and how
// many there are.
type PreviewPlan struct {
	Interval float64
	Tiles    int
	Columns  int
	Rows     int
}

// PlanPreview chooses the grid for a video of this length.
//
// The interval is whatever spreads the cap over the whole video, never finer
// than `previewMinInterval`: a viewer dragging through an hour wants coverage,
// and one dragging through three minutes wants detail, and both get the same
// sheet size.
func PlanPreview(duration float64) PreviewPlan {
	if duration <= 0 {
		return PreviewPlan{}
	}
	interval := math.Max(previewMinInterval, duration/float64(previewMaxTiles))
	tiles := int(math.Ceil(duration / interval))
	if tiles < 1 {
		tiles = 1
	}
	if tiles > previewMaxTiles {
		tiles = previewMaxTiles
	}
	columns := min(previewColumns, tiles)
	rows := int(math.Ceil(float64(tiles) / float64(columns)))
	return PreviewPlan{Interval: interval, Tiles: tiles, Columns: columns, Rows: rows}
}

// Preview derives the sheet and its track.
//
// `-vf fps` decodes the whole file, which is the honest cost of a still every
// N seconds at a *regular* interval: sampling keyframes instead would be much
// faster and would put the stills wherever the encoder happened to leave one,
// which is not a grid a track can describe.
func Preview(ffmpegPath string, duration float64, log *slog.Logger, open RangeSourceFunc) DirDeriveFunc {
	return func(ctx context.Context, dir string) error {
		plan := PlanPreview(duration)
		if plan.Tiles == 0 {
			return fmt.Errorf("derive preview: duration %.3fs is not a video", duration)
		}
		lb, err := newLoopbackSource(log)
		if err != nil {
			return fmt.Errorf("derive preview: %w", err)
		}
		defer lb.close()
		src, release := lb.register(open)
		defer release()

		sheet := filepath.Join(dir, PreviewSheetName)
		filter := fmt.Sprintf("fps=1/%s,scale=%d:-2,tile=%dx%d",
			strconv.FormatFloat(plan.Interval, 'f', 3, 64), previewTileWidth, plan.Columns, plan.Rows)
		args := []string{
			"-hide_banner", "-loglevel", "error",
			"-i", src,
			"-an", "-sn",
			"-vf", filter,
			"-frames:v", "1",
			"-qscale:v", "5",
			"-y", sheet,
		}
		if err := runFFmpegIn(ctx, ffmpegPath, dir, args, log); err != nil {
			return fmt.Errorf("derive preview: %w", err)
		}
		// The track is written last, so its presence is what "ready" means:
		// a sheet on disk with no track is a job that died halfway.
		return os.WriteFile(filepath.Join(dir, PreviewTrackName), []byte(PreviewTrack(plan, duration)), 0o600)
	}
}

// PreviewTrack is the WebVTT track for a plan: one cue per tile, pointing at
// its rectangle of the sheet.
//
// The `#xywh=` fragment is the convention every player that does this speaks —
// it is how a single image can be a hundred thumbnails.
func PreviewTrack(plan PreviewPlan, duration float64) string {
	var b strings.Builder
	b.WriteString("WEBVTT\n\n")
	height := previewTileWidth * 9 / 16
	for i := range plan.Tiles {
		start := float64(i) * plan.Interval
		end := start + plan.Interval
		if end > duration {
			end = duration
		}
		if end <= start {
			break
		}
		x := (i % plan.Columns) * previewTileWidth
		y := (i / plan.Columns) * height
		fmt.Fprintf(&b, "%s --> %s\n%s#xywh=%d,%d,%d,%d\n\n",
			vttTime(start), vttTime(end), PreviewSheetName, x, y, previewTileWidth, height)
	}
	return b.String()
}

// vttTime formats seconds as WebVTT's hh:mm:ss.mmm.
func vttTime(seconds float64) string {
	if seconds < 0 {
		seconds = 0
	}
	ms := int64(math.Round(seconds * 1000))
	h := ms / 3_600_000
	m := (ms % 3_600_000) / 60_000
	s := (ms % 60_000) / 1000
	return fmt.Sprintf("%02d:%02d:%02d.%03d", h, m, s, ms%1000)
}

// PreviewReady reports whether a preview directory holds a finished pair.
func PreviewReady(dir string) bool {
	for _, name := range []string{PreviewSheetName, PreviewTrackName} {
		st, err := os.Stat(filepath.Join(dir, name))
		if err != nil || st.Size() == 0 {
			return false
		}
	}
	return true
}
