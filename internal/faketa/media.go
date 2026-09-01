package faketa

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// Media generates and serves the archive's files. Everything is derived from
// the catalogue with ffmpeg on first run and cached on disk, so a second start
// is instant.
type Media struct {
	dir    string
	ffmpeg string
	log    *slog.Logger

	mu   sync.Mutex
	made map[string]string // video id -> file path
}

func NewMedia(dir, ffmpeg string, log *slog.Logger) *Media {
	return &Media{dir: dir, ffmpeg: ffmpeg, log: log, made: map[string]string{}}
}

// Generate builds every missing file. It is safe to call on every start: a
// file that is already there is left alone.
func (m *Media) Generate(ctx context.Context, catalogue *Catalogue) error {
	if err := os.MkdirAll(m.dir, 0o750); err != nil {
		return fmt.Errorf("media dir: %w", err)
	}
	for _, v := range catalogue.Videos {
		s, _, ok := specFor(v.YoutubeID)
		if !ok {
			continue
		}
		path := filepath.Join(m.dir, v.YoutubeID+".mp4")
		if _, err := os.Stat(path); err == nil {
			m.remember(v.YoutubeID, path)
			continue
		}
		m.log.Info("generating", "video", v.YoutubeID, "title", v.Title, "seconds", s.seconds, "codec", s.codec)
		if err := m.encode(ctx, path, v.Title, s); err != nil {
			return fmt.Errorf("generate %s: %w", v.YoutubeID, err)
		}
		m.remember(v.YoutubeID, path)
	}
	return nil
}

func (m *Media) remember(id, path string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.made[id] = path
}

// Path is the generated file for a video id, or "" when there is none.
func (m *Media) Path(id string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.made[id]
}

// encode writes one video. testsrc2 carries a running timer, so a viewer can
// see at a glance that a seek, a resume or a chapter jump landed where it
// claimed to.
func (m *Media) encode(ctx context.Context, path, title string, s spec) error {
	// The extension has to stay .mp4: ffmpeg picks the muxer from it, and a
	// ".partial" suffix makes it give up rather than guess.
	tmp := strings.TrimSuffix(path, ".mp4") + ".partial.mp4"
	args := []string{
		"-y", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", fmt.Sprintf("testsrc2=size=%dx%d:rate=30:duration=%.0f", s.codedWidth(), s.height, s.seconds),
		"-f", "lavfi", "-i", fmt.Sprintf("sine=frequency=220:duration=%.0f", s.seconds),
	}
	// Chapters ride in through a metadata input, which is how the embedded
	// chapter path gets exercised at all.
	if s.chapters {
		meta, err := m.writeChapterMetadata(path, title, s)
		if err != nil {
			return err
		}
		defer func() { _ = os.Remove(meta) }()
		args = append(args, "-i", meta, "-map_metadata", "2")
	}
	args = append(args, "-map", "0:v", "-map", "1:a")
	// Each video is generated at its own level, so the archive has quiet ones
	// and loud ones — which is the only way the loudness measurement, and the
	// gain a player applies from it, mean anything here.
	if s.levelDB != 0 {
		args = append(args, "-af", fmt.Sprintf("volume=%.1fdB", s.levelDB))
	}
	// Rate-capped on purpose. `ultrafast` with no rate control encodes a test
	// pattern at ~15 Mbit/s, which made a 45-second clip 88 MB and the whole
	// fixture a gigabyte — slow to generate, slow to serve, and pointless for
	// something whose only job is to be recognisably moving video.
	if s.codec == "vp09" {
		args = append(args,
			"-c:v", "libvpx-vp9", "-deadline", "realtime", "-cpu-used", "8", "-b:v", "600k",
			"-c:a", "aac", "-b:a", "96k", "-shortest", "-movflags", "+faststart", tmp)
	} else {
		args = append(args,
			"-c:v", "libx264", "-preset", "veryfast", "-crf", "32",
			"-maxrate", "2M", "-bufsize", "4M", "-pix_fmt", "yuv420p", "-g", "60",
			"-c:a", "aac", "-b:a", "96k", "-shortest", "-movflags", "+faststart", tmp)
	}
	cmd := exec.CommandContext(ctx, m.ffmpeg, args...) //nolint:gosec // dev tool, fixed args
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return os.Rename(tmp, path)
}

// writeChapterMetadata emits an FFMETADATA file with the same three marks the
// description carries, so the embedded and description sources agree.
func (m *Media) writeChapterMetadata(path, title string, s spec) (string, error) {
	var b strings.Builder
	b.WriteString(";FFMETADATA1\ntitle=" + title + "\n")
	marks := chapterMarks(s.seconds)
	names := []string{"Intro", "The middle bit", "Wrapping up"}
	for i, start := range marks {
		end := s.seconds
		if i+1 < len(marks) {
			end = marks[i+1]
		}
		fmt.Fprintf(&b, "\n[CHAPTER]\nTIMEBASE=1/1000\nSTART=%d\nEND=%d\ntitle=%s\n",
			int64(start*1000), int64(end*1000), names[i])
	}
	meta := path + ".ffmeta"
	if err := os.WriteFile(meta, []byte(b.String()), 0o600); err != nil {
		return "", fmt.Errorf("chapter metadata: %w", err)
	}
	return meta, nil
}

// Subtitles is a WebVTT track whose lines name their own timestamp, so a
// search hit that claims 0:20 can be checked by eye.
func Subtitles(seconds float64) string {
	var b strings.Builder
	b.WriteString("WEBVTT\n\n")
	for start := 0.0; start < seconds; start += 5 {
		end := start + 5
		if end > seconds {
			end = seconds
		}
		fmt.Fprintf(&b, "%s --> %s\nThis line starts at %s.\n\n", vttClock(start), vttClock(end), clock(start))
	}
	return b.String()
}

func vttClock(seconds float64) string {
	total := int(seconds)
	return fmt.Sprintf("00:%02d:%02d.000", total/60, total%60)
}

// ---- thumbnails ----

var (
	thumbMu    sync.Mutex
	thumbCache = map[string][]byte{}
)

// thumbnailJPEG draws a plain two-tone card, its colour derived from the
// name. Real thumbnails are the difference between a grid you can navigate
// and a wall of identical grey, and this is the cheapest way to get one.
func thumbnailJPEG(name string) []byte {
	thumbMu.Lock()
	defer thumbMu.Unlock()
	if cached, ok := thumbCache[name]; ok {
		return cached
	}
	const width, height = 640, 360
	// FNV-1a rather than a rolling sum: names in this catalogue differ by a
	// character or two, and a weak hash gives them all the same blue.
	var hash uint32 = 2166136261
	for _, b := range []byte(name) {
		hash = (hash ^ uint32(b)) * 16777619
	}
	hue := int(hash % 360)
	base := hueColor(hue)
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		// A vertical fade, so the card reads as an image rather than a block.
		shade := 0.55 + 0.45*float64(y)/float64(height)
		row := color.RGBA{
			R: uint8(float64(base.R) * shade),
			G: uint8(float64(base.G) * shade),
			B: uint8(float64(base.B) * shade),
			A: 255,
		}
		for x := range width {
			img.SetRGBA(x, y, row)
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 70}); err != nil {
		return nil
	}
	out := buf.Bytes()
	thumbCache[name] = out
	return out
}

// hueColor is a coarse HSV-to-RGB at full saturation and value: enough for
// "give me a different colour per name".
func hueColor(hue int) color.RGBA {
	section := hue / 60 % 6
	rising := uint8(255 * (hue % 60) / 60) //nolint:gosec // bounded by the modulo
	falling := 255 - rising
	switch section {
	case 0:
		return color.RGBA{R: 255, G: rising, A: 255}
	case 1:
		return color.RGBA{R: falling, G: 255, A: 255}
	case 2:
		return color.RGBA{G: 255, B: rising, A: 255}
	case 3:
		return color.RGBA{G: falling, B: 255, A: 255}
	case 4:
		return color.RGBA{R: rising, B: 255, A: 255}
	default:
		return color.RGBA{R: 255, B: falling, A: 255}
	}
}
