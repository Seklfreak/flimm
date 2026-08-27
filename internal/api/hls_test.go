package api

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Seklfreak/flimm/internal/media"
	"github.com/Seklfreak/flimm/internal/ta"
)

// completeMarker mirrors the unexported marker internal/media writes when a
// directory entry finishes. Fixtures need it so a test entry reads as done and
// no ffmpeg is started; if it ever changes, these tests fail loudly by trying
// to transcode.
const completeMarker = ".complete"

// hlsFixture builds a server whose cache already holds a finished rendition
// for v1, so the routes can be tested without ffmpeg.
func hlsFixture(t *testing.T, segment []byte) (http.Handler, string) {
	t.Helper()
	dir := t.TempDir()
	entry := filepath.Join(dir, media.HLSName("v1"))
	if err := os.MkdirAll(entry, 0o750); err != nil {
		t.Fatal(err)
	}
	playlist := "#EXTM3U\n#EXT-X-VERSION:7\n#EXT-X-PLAYLIST-TYPE:EVENT\n" +
		"#EXT-X-MAP:URI=\"init.mp4\"\n#EXTINF:4.000000,\nseg00000.m4s\n#EXT-X-ENDLIST\n"
	for name, body := range map[string][]byte{
		media.HLSPlaylistName: []byte(playlist),
		media.HLSInitName:     []byte("init-bytes"),
		"seg00000.m4s":        segment,
		completeMarker:        {},
	} {
		if err := os.WriteFile(filepath.Join(entry, name), body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return hlsServer(t, dir, "ffmpeg"), dir
}

func hlsServer(t *testing.T, dir, ffmpegPath string) http.Handler {
	t.Helper()
	cache, err := media.NewCache(dir, 0, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cache.Close)
	client := ta.NewFake()
	client.Videos["v1"] = &ta.Video{
		YoutubeID: "v1", Title: "Video v1", MediaURL: "/youtube/UC1/v1.mp4",
		Player:  ta.Player{Duration: 600},
		Streams: []ta.Stream{{Type: "video", Codec: "av01", Height: 1080}, {Type: "audio", Codec: "opus"}},
	}
	client.Media = map[string][]byte{"/media/UC1/v1.mp4": []byte("source")}
	return NewServer(Options{
		Querier:     newEventStore().querier(),
		TA:          client,
		Log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		AppName:     "Flimm",
		MediaSecret: testSecret,
		MediaCache:  cache,
		FFmpegPath:  ffmpegPath,
	}).Router()
}

func getMedia(t *testing.T, h http.Handler, path, rangeHdr string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer test")
	if rangeHdr != "" {
		req.Header.Set("Range", rangeHdr)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// The three file kinds a player fetches, each with the content type
// AVFoundation expects — a wrong one on the playlist is an instant failure.
func TestHLSServesPlaylistInitAndSegments(t *testing.T) {
	segment := make([]byte, 5000)
	for i := range segment {
		segment[i] = byte(i % 251)
	}
	h, _ := hlsFixture(t, segment)

	rec := getMedia(t, h, "/media/hls/v1/index.m3u8", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("playlist status = %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != media.HLSPlaylistType {
		t.Errorf("playlist Content-Type = %q, want %q", ct, media.HLSPlaylistType)
	}
	// A finished rendition never changes, so it may be cached.
	if cc := rec.Header().Get("Cache-Control"); cc != "private, max-age=3600" {
		t.Errorf("finished playlist Cache-Control = %q", cc)
	}
	if body := rec.Body.String(); !strings.Contains(body, "#EXT-X-ENDLIST") || !strings.Contains(body, "seg00000.m4s") {
		t.Errorf("playlist body = %q", body)
	}

	init := getMedia(t, h, "/media/hls/v1/init.mp4", "")
	if init.Code != http.StatusOK || init.Header().Get("Content-Type") != media.HLSInitType {
		t.Errorf("init = %d %q", init.Code, init.Header().Get("Content-Type"))
	}

	seg := getMedia(t, h, "/media/hls/v1/seg00000.m4s", "")
	if seg.Code != http.StatusOK {
		t.Fatalf("segment status = %d", seg.Code)
	}
	if ct := seg.Header().Get("Content-Type"); ct != media.HLSSegmentType {
		t.Errorf("segment Content-Type = %q, want %q", ct, media.HLSSegmentType)
	}
	if seg.Body.Len() != len(segment) {
		t.Errorf("segment body = %d bytes, want %d", seg.Body.Len(), len(segment))
	}

	// Ranges work on segments exactly as they do for the audio variants.
	partial := getMedia(t, h, "/media/hls/v1/seg00000.m4s", "bytes=100-199")
	if partial.Code != http.StatusPartialContent {
		t.Fatalf("range status = %d, want 206", partial.Code)
	}
	if cr := partial.Header().Get("Content-Range"); cr != "bytes 100-199/5000" {
		t.Errorf("Content-Range = %q", cr)
	}
}

// Only the names the rendition actually contains are served: no traversal, no
// stray temp file, no completion marker.
func TestHLSRejectsAnythingButItsOwnFiles(t *testing.T) {
	h, dir := hlsFixture(t, []byte("segment"))
	if err := os.WriteFile(filepath.Join(dir, "secret.txt"), []byte("nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"/media/hls/v1/" + completeMarker,
		"/media/hls/v1/seg00000.m4s.tmp",
		"/media/hls/v1/index.m3u",
		"/media/hls/v1/..%2f..%2fsecret.txt",
		"/media/hls/v1/%2e%2e%2fsecret.txt",
		"/media/hls/a.b/index.m3u8",
		"/media/hls/..%2fetc%2fpasswd/index.m3u8",
	} {
		if rec := getMedia(t, h, path, ""); rec.Code == http.StatusOK {
			t.Errorf("%s was served: %s", path, rec.Body.String())
		}
	}
}

// Media routes are gated the same way for every segment, because AVPlayer
// re-sends the credentials on each one.
func TestHLSRequiresMediaAuth(t *testing.T) {
	h, _ := hlsFixture(t, []byte("segment"))
	for _, path := range []string{"/media/hls/v1/index.m3u8", "/media/hls/v1/seg00000.m4s"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s without credentials = %d, want 401", path, rec.Code)
		}
	}
}

// writeHangingFFmpeg installs a stub that never produces anything, standing in
// for a transcode that has not reached its first segment yet.
func writeHangingFFmpeg(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ffmpeg-hang")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nsleep 30\n"), 0o700); err != nil { //nolint:gosec // test fixture must be executable
		t.Fatal(err)
	}
	return path
}

// A rendition that is not ready in time is not an error: the client is told to
// come back, and the transcode keeps running.
func TestHLSPlaylistTimesOutWith503(t *testing.T) {
	old := hlsPlaylistWait
	hlsPlaylistWait = 100 * time.Millisecond
	t.Cleanup(func() { hlsPlaylistWait = old })

	h := hlsServer(t, t.TempDir(), writeHangingFFmpeg(t))
	rec := getMedia(t, h, "/media/hls/v1/index.m3u8", "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503: %s", rec.Code, rec.Body.String())
	}
	if ra := rec.Header().Get("Retry-After"); ra != "5" {
		t.Errorf("Retry-After = %q, want 5", ra)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
}

// A segment the transcode has not written yet is a 404 the player retries,
// never a hang.
func TestHLSMissingSegmentIs404(t *testing.T) {
	h, _ := hlsFixture(t, []byte("segment"))
	if rec := getMedia(t, h, "/media/hls/v1/seg00042.m4s", ""); rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

// Prefetch: the endpoint starts the job and returns at once, so a client can
// warm the next video up instead of making the viewer wait at play time.
func TestPostVideoHLSStartsWithoutWaiting(t *testing.T) {
	h := hlsServer(t, t.TempDir(), writeHangingFFmpeg(t))
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- do(t, h, http.MethodPost, "/api/v1/videos/v1/hls", "") }()
	select {
	case rec := <-done:
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
		}
		if got := decode[map[string]string](t, rec)["state"]; got != string(media.StateRunning) {
			t.Errorf("state = %q, want %q", got, media.StateRunning)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the prefetch endpoint waited for the transcode")
	}

	if rec := do(t, h, http.MethodPost, "/api/v1/videos/nope/hls", ""); rec.Code != http.StatusNotFound {
		t.Errorf("unknown video = %d, want 404", rec.Code)
	}
}

// The detail always carries the URL — a client needs to know the rendition can
// exist — and the state says whether asking for it will mean waiting.
func TestVideoDetailCarriesHLSURLAndState(t *testing.T) {
	h, _ := hlsFixture(t, []byte("segment"))
	detail := decode[VideoDetail](t, do(t, h, http.MethodGet, "/api/v1/videos/v1", ""))
	if detail.HLSURL != "/media/hls/v1/index.m3u8" {
		t.Errorf("hls_url = %q", detail.HLSURL)
	}
	if detail.HLSState != string(media.StateDone) {
		t.Errorf("hls_state = %q, want %q", detail.HLSState, media.StateDone)
	}

	// A video nobody has asked for is pending, and so is every video on a
	// server with no media cache at all.
	fake := ta.NewFake()
	fake.Videos["v2"] = &ta.Video{YoutubeID: "v2", Title: "Video v2", MediaURL: "/youtube/UC1/v2.mp4"}
	plain := newTestServer(fake, newEventStore().querier()).Router()
	d2 := decode[VideoDetail](t, do(t, plain, http.MethodGet, "/api/v1/videos/v2", ""))
	if d2.HLSURL != "/media/hls/v2/index.m3u8" || d2.HLSState != string(media.StatePending) {
		t.Errorf("without a cache: hls_url = %q, hls_state = %q", d2.HLSURL, d2.HLSState)
	}
}

// The copy-vs-encode inputs come off the TA document, not a probe.
func TestHLSSourceFromVideo(t *testing.T) {
	v := &ta.Video{Streams: []ta.Stream{
		{Type: "video", Codec: "avc1", Height: 720},
		{Type: "audio", Codec: "mp4a"},
	}}
	got := hlsSource(v)
	if got.VideoCodec != "avc1" || got.AudioCodec != "mp4a" || got.Height != 720 {
		t.Errorf("hlsSource = %+v", got)
	}
	if empty := hlsSource(&ta.Video{}); empty.VideoCodec != "" || empty.Height != 0 {
		t.Errorf("no streams = %+v", empty)
	}
}

// End to end through the routes with a real ffmpeg: the playlist request
// starts the transcode and comes back with something a player can follow, and
// every segment it names is actually servable. Skipped where ffmpeg is not
// installed, like the other derivation tests.
func TestHLSEndToEndWithRealFFmpeg(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed; skipping derivation test")
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "src.mp4")
	//nolint:gosec // G204: fixture paths from t.TempDir(), no request data
	fixture := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc=duration=6:size=320x240:rate=24",
		"-f", "lavfi", "-i", "sine=duration=6",
		"-c:v", "libx264", "-c:a", "aac", "-movflags", "+faststart", "-y", source)
	if err := fixture.Run(); err != nil {
		t.Skipf("cannot build fixture: %v", err)
	}
	body, err := os.ReadFile(source) //nolint:gosec // test fixture path
	if err != nil {
		t.Fatal(err)
	}

	cache, err := media.NewCache(t.TempDir(), 0, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cache.Close)
	client := ta.NewFake()
	// Claimed as AV1 so the real encode path runs — the case the whole variant
	// exists for.
	client.Videos["v1"] = &ta.Video{
		YoutubeID: "v1", Title: "Video v1", MediaURL: "/youtube/UC1/v1.mp4",
		Streams: []ta.Stream{{Type: "video", Codec: "av01", Height: 240}, {Type: "audio", Codec: "opus"}},
	}
	client.Media = map[string][]byte{"/media/UC1/v1.mp4": body}
	h := NewServer(Options{
		Querier:     newEventStore().querier(),
		TA:          client,
		Log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		AppName:     "Flimm",
		MediaSecret: testSecret,
		MediaCache:  cache,
	}).Router()

	rec := getMedia(t, h, "/media/hls/v1/index.m3u8", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("playlist status = %d: %s", rec.Code, rec.Body.String())
	}
	playlist := rec.Body.String()
	if !strings.Contains(playlist, "#EXTM3U") {
		t.Fatalf("not a playlist: %q", playlist)
	}

	// Init and every segment the playlist names must be servable by the route.
	if init := getMedia(t, h, "/media/hls/v1/init.mp4", ""); init.Code != http.StatusOK || init.Body.Len() == 0 {
		t.Errorf("init segment = %d, %d bytes", init.Code, init.Body.Len())
	}
	segments := 0
	for _, line := range strings.Split(playlist, "\n") {
		name := strings.TrimSpace(line)
		if !strings.HasSuffix(name, ".m4s") {
			continue
		}
		segments++
		seg := getMedia(t, h, "/media/hls/v1/"+name, "")
		if seg.Code != http.StatusOK || seg.Body.Len() == 0 {
			t.Errorf("segment %s = %d, %d bytes", name, seg.Code, seg.Body.Len())
		}
	}
	if segments == 0 {
		t.Fatalf("playlist names no segments: %q", playlist)
	}

	// The transcode finishes behind the request; the detail then says so.
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if cache.DirState(media.HLSName("v1")) == media.StateDone {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	detail := decode[VideoDetail](t, do(t, h, http.MethodGet, "/api/v1/videos/v1", ""))
	if detail.HLSState != string(media.StateDone) {
		t.Fatalf("hls_state = %q, want %q", detail.HLSState, media.StateDone)
	}
	// A finished rendition is closed, or a player never reaches the end.
	final := getMedia(t, h, "/media/hls/v1/index.m3u8", "")
	if !strings.Contains(final.Body.String(), "#EXT-X-ENDLIST") {
		t.Errorf("finished playlist is not closed: %q", final.Body.String())
	}
	if cc := final.Header().Get("Cache-Control"); cc != "private, max-age=3600" {
		t.Errorf("finished playlist Cache-Control = %q", cc)
	}
}
