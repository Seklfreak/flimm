package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeSheetFFmpeg stands in for ffmpeg: it writes whatever the last argument
// names, which for a preview job is the sprite sheet.
func writeSheetFFmpeg(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ffmpeg")
	script := "#!/bin/sh\nfor last; do :; done\nprintf 'jpeg' > \"$last\"\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil { //nolint:gosec // test fixture must be executable
		t.Fatal(err)
	}
	return path
}

// getUntilReady asks for a derived file the way a client does: the first
// request starts the work and 404s, and the answer turns up on a later one.
func getUntilReady(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		rec := getMedia(t, h, path, "")
		if rec.Code != http.StatusNotFound || time.Now().After(deadline) {
			return rec
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// The pair a scrubbing player fetches: a track of cues, each naming its
// rectangle of one sheet.
func TestPreviewServesTrackAndSheet(t *testing.T) {
	h := hlsServer(t, t.TempDir(), writeSheetFFmpeg(t))

	// The first request is what starts the derivation, and says so.
	rec := getMedia(t, h, "/media/preview/v1/preview.vtt", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("first request = %d, want 404 while it is being made", rec.Code)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("a not-yet answer must not be cached: %q", rec.Header().Get("Cache-Control"))
	}

	track := getUntilReady(t, h, "/media/preview/v1/preview.vtt")
	if track.Code != http.StatusOK {
		t.Fatalf("track = %d, want 200: %s", track.Code, track.Body.String())
	}
	if ct := track.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/vtt") {
		t.Errorf("track content type = %q", ct)
	}
	body := track.Body.String()
	if !strings.HasPrefix(body, "WEBVTT") {
		t.Fatalf("track is not a WebVTT track:\n%s", body)
	}
	// v1 is ten minutes long in the fixture, which is 200 stills — the cap —
	// three seconds apart, ten to a row.
	if n := strings.Count(body, "sheet.jpg#xywh="); n != 200 {
		t.Errorf("track has %d cues, want 200", n)
	}
	for _, want := range []string{
		"00:00:00.000 --> 00:00:03.000\nsheet.jpg#xywh=0,0,160,90",
		"sheet.jpg#xywh=160,0,160,90",
		"sheet.jpg#xywh=0,90,160,90",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("track is missing %q:\n%s", want, body[:min(len(body), 400)])
		}
	}

	sheet := getMedia(t, h, "/media/preview/v1/sheet.jpg", "")
	if sheet.Code != http.StatusOK || sheet.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("sheet = %d %q", sheet.Code, sheet.Header().Get("Content-Type"))
	}
	if cc := sheet.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("a finished still never changes, so it should be immutable: %q", cc)
	}
}

// The path names a file inside the cache, so only the two files the job
// produces may be asked for.
func TestPreviewRefusesAnyOtherFile(t *testing.T) {
	h := hlsServer(t, t.TempDir(), writeSheetFFmpeg(t))
	for _, path := range []string{
		"/media/preview/v1/index.m3u8",
		"/media/preview/v1/.complete",
		"/media/preview/v1/..%2F..%2Fetc%2Fpasswd",
		"/media/preview/nosuchvideo/preview.vtt",
	} {
		if rec := getMedia(t, h, path, ""); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, rec.Code)
		}
	}
}
