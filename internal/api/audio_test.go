package api

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Seklfreak/flimm/internal/media"
	"github.com/Seklfreak/flimm/internal/ta"
)

// Serves an already-derived rendition, so the test needs no ffmpeg: it pins
// the wiring and the range behaviour the player depends on for seeking.
func audioServer(t *testing.T, body []byte) http.Handler {
	t.Helper()
	dir := t.TempDir()
	if body != nil {
		if err := os.WriteFile(filepath.Join(dir, "audio-v1.webm"), body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cache, err := media.NewCache(dir, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	client := ta.NewFake()
	client.Videos["v1"] = &ta.Video{YoutubeID: "v1", Title: "Video v1", MediaURL: "/youtube/UC1/v1.mp4", Player: ta.Player{Duration: 600}}
	return NewServer(Options{
		Querier:     newEventStore().querier(),
		TA:          client,
		Log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		AppName:     "Flimm",
		MediaSecret: testSecret,
		MediaCache:  cache,
	}).Router()
}

func TestAudioServesCachedRenditionWithRanges(t *testing.T) {
	body := make([]byte, 5000)
	for i := range body {
		body[i] = byte(i % 251)
	}
	h := audioServer(t, body)

	// Media routes take a Bearer header or the media cookie; the dev verifier
	// accepts any bearer.
	get := func(rangeHdr string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/media/audio/v1.webm", nil)
		req.Header.Set("Authorization", "Bearer test")
		if rangeHdr != "" {
			req.Header.Set("Range", rangeHdr)
		}
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr
	}
	rec := get("")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != media.AudioType {
		t.Errorf("Content-Type = %q, want %q", ct, media.AudioType)
	}
	if rec.Body.Len() != len(body) {
		t.Errorf("body = %d bytes, want %d", rec.Body.Len(), len(body))
	}

	// Seeking depends on ranges being honoured.
	rr := get("bytes=100-199")
	if rr.Code != http.StatusPartialContent {
		t.Fatalf("range status = %d, want 206", rr.Code)
	}
	if rr.Body.Len() != 100 {
		t.Errorf("range body = %d bytes, want 100", rr.Body.Len())
	}
	if cr := rr.Header().Get("Content-Range"); cr != "bytes 100-199/5000" {
		t.Errorf("Content-Range = %q", cr)
	}
}

// A path outside the id charset must never reach the filesystem or ffmpeg.
func TestAudioRejectsBadVideoID(t *testing.T) {
	h := audioServer(t, nil)
	for _, id := range []string{"..%2f..%2fetc%2fpasswd", "a/b", "a.b"} {
		req := httptest.NewRequest(http.MethodGet, "/media/audio/"+id+".webm", nil)
		req.Header.Set("Authorization", "Bearer test")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code == http.StatusOK {
			t.Errorf("id %q was accepted", id)
		}
	}
}
