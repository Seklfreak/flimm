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
	// PreviewVariant names the cache directory. The cell size is part of the
	// name on purpose: a sheet only means anything against the grid its track
	// describes, so a change to that grid must never be able to find an older
	// sheet on disk and serve it under the new arithmetic. Changing the cell
	// below without changing this is the bug this spells out.
	PreviewVariant = "preview-160x90"
	// PreviewSheetName and PreviewTrackName are what lands in it.
	PreviewSheetName = "sheet.jpg"
	PreviewTrackName = "preview.vtt"
	// previewStillPattern names the scratch stills the sampling pass writes,
	// which the tiling pass then reads. They never survive the job.
	previewStillPattern = "still-%04d.jpg"

	// previewTileWidth is each still's width. 160 is what a scrubber shows at
	// roughly life size on a phone and comfortably on a desktop; the sheet
	// grows with the square of this, so it is the number that decides whether
	// a two-hour video costs 300 KB or 3 MB.
	previewTileWidth = 160
	// previewTileHeight is the cell every still is fitted into, letterboxed.
	//
	// A *fixed* cell, not the source's own aspect, and that is the whole point:
	// a track can only address a sheet by arithmetic, and arithmetic needs a
	// cell it can rely on. Scaling to the source instead made every ratio but
	// 16:9 wrong — a 2.40:1 video produced 66px rows under a track that
	// addressed 90px ones, so the later rows fell off the bottom of the image
	// and a scrubber a viewer had got some way into showed nothing but black.
	// It also unbounded the sheet: a vertical video's twenty rows came to
	// 5680px, past the texture size browsers and Metal are happy with.
	previewTileHeight = previewTileWidth * 9 / 16
	// previewColumns is the sheet's width in tiles. Ten keeps a sheet under
	// the 4096-pixel texture limit browsers and Metal are happiest with —
	// which holds only because the cell above is pinned.
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
func Preview(ffmpegPath string, duration float64, log *slog.Logger, open RangeSourceFunc, report ProgressFunc) DirDeriveFunc {
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
		// Fit-and-pad rather than plain scale: every still comes out exactly
		// previewTileWidth×previewTileHeight whatever the source's shape, which
		// is what makes the track's arithmetic true. `setsar=1` keeps an
		// anamorphic source from carrying its pixel ratio into the sheet, where
		// nothing would honour it. The padding is black, and the tiling pass
		// needs equal-sized inputs anyway — a source that changes resolution
		// partway through used to be able to fail the whole job.
		filter := fmt.Sprintf("fps=1/%s,scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,setsar=1",
			strconv.FormatFloat(plan.Interval, 'f', 3, 64),
			previewTileWidth, previewTileHeight, previewTileWidth, previewTileHeight)
		// Two passes, and the split is what makes the wait measurable.
		//
		// Tiling inside the sampling run would make its output a single image,
		// and every counter ffmpeg reports is about the output — so a decode
		// that takes minutes would report nothing at all until it finished.
		// Writing the stills out one by one instead makes the frame count the
		// work, exactly: still 87 of 200 is 43% and means it.
		//
		// The second pass only reads a couple of hundred small JPEGs and lays
		// them out. It is not a decode of anything, and it costs nothing worth
		// reporting.
		if _, err := runFFmpegReporting(ctx, ffmpegPath, dir, withProgress([]string{
			"-hide_banner", "-loglevel", "error",
			"-i", src,
			"-an", "-sn",
			"-vf", filter,
			"-frames:v", strconv.Itoa(plan.Tiles),
			"-qscale:v", "3",
			"-y", previewStillPattern,
		}), log, byFrameCount(plan.Tiles), report); err != nil {
			return fmt.Errorf("derive preview: %w", err)
		}
		// The stills are scratch: they must not be in the entry when it is
		// marked complete, or the cache would account for them forever.
		defer removePreviewStills(dir)
		if err := runFFmpegIn(ctx, ffmpegPath, dir, []string{
			"-hide_banner", "-loglevel", "error",
			"-start_number", "1",
			"-i", previewStillPattern,
			"-vf", fmt.Sprintf("tile=%dx%d", plan.Columns, plan.Rows),
			"-frames:v", "1",
			"-qscale:v", "5",
			"-y", sheet,
		}, log); err != nil {
			return fmt.Errorf("derive preview sheet: %w", err)
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
		y := (i / plan.Columns) * previewTileHeight
		fmt.Fprintf(&b, "%s --> %s\n%s#xywh=%d,%d,%d,%d\n\n",
			vttTime(start), vttTime(end), PreviewSheetName, x, y, previewTileWidth, previewTileHeight)
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

// removePreviewStills clears the scratch stills. A failure to remove one is not
// worth failing the derivation over — the sheet and its track are what the
// entry is for, and the leftovers cost a few hundred KB until it is evicted.
func removePreviewStills(dir string) {
	matches, err := filepath.Glob(filepath.Join(dir, "still-*.jpg"))
	if err != nil {
		return
	}
	for _, m := range matches {
		_ = os.Remove(m)
	}
}
