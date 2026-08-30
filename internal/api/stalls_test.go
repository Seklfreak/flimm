package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/Seklfreak/flimm/internal/media"
)

func stallReport(t *testing.T, h http.Handler, body string) {
	t.Helper()
	rec := do(t, h, http.MethodPost, "/api/v1/videos/v1/stall", body)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
}

// The whole point of reporting a stall to the server: the client knows the
// picture stopped, and only the server knows whether the bytes were there.
func TestAStallIsAttributedToTheEncoderOrToDelivery(t *testing.T) {
	dir := t.TempDir()
	h := hlsServer(t, dir, writeHangingFFmpeg(t))

	// The playlist request starts the job, so there is one to ask about.
	if rec := getMedia(t, h, "/media/hls/v1/1080/index.m3u8", ""); rec.Code != http.StatusOK {
		t.Fatalf("playlist: %d", rec.Code)
	}

	// Nothing is encoded (the stub hangs), so a stall there is the encoder's.
	stallReport(t, h, `{"position":120,"seconds":3,"height":1080,"client":"tvos"}`)
	recent := lastStall(t, h)
	if recent.Reason != stallEncoder {
		t.Errorf("reason = %q, want %q", recent.Reason, stallEncoder)
	}
	if recent.Segment != 30 {
		t.Errorf("segment = %d, want the one 120s falls in", recent.Segment)
	}

	// Playing the archived file itself involves no rendition at all.
	stallReport(t, h, `{"position":10,"seconds":2,"height":0,"client":"web"}`)
	if got := lastStall(t, h).Reason; got != stallSource {
		t.Errorf("reason = %q, want %q for a direct play", got, stallSource)
	}
}

// The ordinary case for a video watched twice: the rendition was finished
// before playback began, so there is no run to ask — but the segment file
// settles it, and settles it certainly.
func TestAFinishedRenditionIsAttributedFromDisk(t *testing.T) {
	dir := t.TempDir()
	h := hlsServer(t, dir, writeHangingFFmpeg(t))

	// No playlist request, so nothing is running: only what is on disk.
	out := filepath.Join(dir, media.HLSName("v1", 1080))
	if err := os.MkdirAll(out, 0o750); err != nil {
		t.Fatal(err)
	}
	segment := media.HLSSegmentIndexAt(120)
	if err := os.WriteFile(filepath.Join(out, media.HLSSegmentName(segment)), []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}

	stallReport(t, h, `{"position":120,"seconds":3,"height":1080,"client":"web"}`)
	if got := lastStall(t, h).Reason; got != stallDelivery {
		t.Errorf("reason = %q, want %q: the segment was on disk", got, stallDelivery)
	}

	// A position the rendition never held stays unattributed rather than being
	// blamed on an encoder that is not running.
	stallReport(t, h, `{"position":500,"seconds":3,"height":1080,"client":"web"}`)
	if got := lastStall(t, h).Reason; got != stallUnknown {
		t.Errorf("reason = %q, want %q", got, stallUnknown)
	}
}

// A gap of a few hundred milliseconds is what every player does between
// segments; a log line for each would bury the ones that matter.
func TestATinyGapIsNotAStall(t *testing.T) {
	h := hlsServer(t, t.TempDir(), writeHangingFFmpeg(t))
	stallReport(t, h, `{"position":10,"seconds":0.2,"height":1080,"client":"web"}`)
	if stalls := allStalls(t, h); len(stalls) != 0 {
		t.Errorf("recorded %d stalls, want none for a 200ms gap", len(stalls))
	}
}

// Only so many are kept: enough to see a pattern, few enough to hold.
func TestOnlyTheRecentStallsAreKept(t *testing.T) {
	h := hlsServer(t, t.TempDir(), writeHangingFFmpeg(t))
	for i := range stallsKept + 10 {
		stallReport(t, h, fmt.Sprintf(`{"position":%d,"seconds":1,"height":0,"client":"web"}`, i))
	}
	stalls := allStalls(t, h)
	if len(stalls) != stallsKept {
		t.Fatalf("kept %d, want %d", len(stalls), stallsKept)
	}
	if stalls[len(stalls)-1].Position != float64(stallsKept+9) {
		t.Errorf("the newest kept stall is at %v, want the last one reported", stalls[len(stalls)-1].Position)
	}
}

func allStalls(t *testing.T, h http.Handler) []Stall {
	t.Helper()
	rec := do(t, h, http.MethodGet, "/healthz", "")
	var out struct {
		Stalls []Stall `json:"stalls"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.Stalls
}

func lastStall(t *testing.T, h http.Handler) Stall {
	t.Helper()
	stalls := allStalls(t, h)
	if len(stalls) == 0 {
		t.Fatal("no stall was recorded")
	}
	return stalls[len(stalls)-1]
}
