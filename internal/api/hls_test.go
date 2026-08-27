package api

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
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
// for v1 at the default height, so the routes can be tested without ffmpeg.
func hlsFixture(t *testing.T, segment []byte) (http.Handler, string) {
	t.Helper()
	dir := t.TempDir()
	writeHLSEntry(t, dir, "v1", media.HLSDefaultHeight, segment)
	return hlsServer(t, dir, "ffmpeg"), dir
}

// writeHLSEntry lays down a finished rendition of one video at one height.
func writeHLSEntry(t *testing.T, dir, id string, height int, segment []byte) {
	t.Helper()
	entry := filepath.Join(dir, media.HLSName(id, height))
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
	if detail.HLSURL != "/media/hls/v1/1080/index.m3u8" {
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
	if d2.HLSURL != "/media/hls/v2/1080/index.m3u8" || d2.HLSState != string(media.StatePending) {
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

	// A 240p source offers only the lowest rung, so that is the rendition its
	// detail points at and the one a client asks for.
	rec := getMedia(t, h, "/media/hls/v1/480/index.m3u8", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("playlist status = %d: %s", rec.Code, rec.Body.String())
	}
	playlist := rec.Body.String()
	if !strings.Contains(playlist, "#EXTM3U") {
		t.Fatalf("not a playlist: %q", playlist)
	}

	// Init and every segment the playlist names must be servable by the route.
	if init := getMedia(t, h, "/media/hls/v1/480/init.mp4", ""); init.Code != http.StatusOK || init.Body.Len() == 0 {
		t.Errorf("init segment = %d, %d bytes", init.Code, init.Body.Len())
	}
	segments := 0
	for _, line := range strings.Split(playlist, "\n") {
		name := strings.TrimSpace(line)
		if !strings.HasSuffix(name, ".m4s") {
			continue
		}
		segments++
		seg := getMedia(t, h, "/media/hls/v1/480/"+name, "")
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
		if cache.DirState(media.HLSName("v1", 480)) == media.StateDone {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	detail := decode[VideoDetail](t, do(t, h, http.MethodGet, "/api/v1/videos/v1", ""))
	if detail.HLSState != string(media.StateDone) {
		t.Fatalf("hls_state = %q, want %q", detail.HLSState, media.StateDone)
	}
	// A finished rendition is closed, or a player never reaches the end.
	final := getMedia(t, h, "/media/hls/v1/480/index.m3u8", "")
	if !strings.Contains(final.Body.String(), "#EXT-X-ENDLIST") {
		t.Errorf("finished playlist is not closed: %q", final.Body.String())
	}
	if cc := final.Header().Get("Cache-Control"); cc != "private, max-age=3600" {
		t.Errorf("finished playlist Cache-Control = %q", cc)
	}
}

// Each height is served from its own entry, under its own path.
func TestHLSVariantRoutesServeEachHeight(t *testing.T) {
	dir := t.TempDir()
	for _, h := range []int{1080, 720, 480} {
		writeHLSEntry(t, dir, "v1", h, []byte("segment-"+strconv.Itoa(h)))
	}
	srv := hlsServer(t, dir, "ffmpeg")

	for _, height := range []int{1080, 720, 480} {
		base := "/media/hls/v1/" + strconv.Itoa(height) + "/"
		rec := getMedia(t, srv, base+"index.m3u8", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("%dp playlist = %d: %s", height, rec.Code, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); ct != media.HLSPlaylistType {
			t.Errorf("%dp playlist Content-Type = %q", height, ct)
		}
		if init := getMedia(t, srv, base+"init.mp4", ""); init.Code != http.StatusOK {
			t.Errorf("%dp init = %d", height, init.Code)
		}
		seg := getMedia(t, srv, base+"seg00000.m4s", "")
		if seg.Code != http.StatusOK {
			t.Fatalf("%dp segment = %d", height, seg.Code)
		}
		// The right entry, not merely an entry: each height's segment carries
		// its own bytes.
		if got := seg.Body.String(); got != "segment-"+strconv.Itoa(height) {
			t.Errorf("%dp segment body = %q", height, got)
		}
	}
}

// The path without a height that older clients use must keep working, and must not
// become a sixth rendition: it is the 1080 entry under another name.
func TestHLSLegacyRouteServesTheDefaultVariant(t *testing.T) {
	dir := t.TempDir()
	writeHLSEntry(t, dir, "v1", media.HLSDefaultHeight, []byte("the-1080-segment"))
	srv := hlsServer(t, dir, "ffmpeg")

	for _, file := range []string{"index.m3u8", "init.mp4", "seg00000.m4s"} {
		alias := getMedia(t, srv, "/media/hls/v1/"+file, "")
		explicit := getMedia(t, srv, "/media/hls/v1/1080/"+file, "")
		if alias.Code != http.StatusOK || explicit.Code != http.StatusOK {
			t.Fatalf("%s: alias = %d, explicit = %d", file, alias.Code, explicit.Code)
		}
		if alias.Body.String() != explicit.Body.String() {
			t.Errorf("%s: the alias serves different bytes from /1080/", file)
		}
	}
	// No entry of its own was created for the alias.
	if _, err := os.Stat(filepath.Join(dir, "hls-v1")); err == nil {
		t.Error("the alias created a duplicate cache entry")
	}
}

// A source below the default still answers the alias — an old client has no
// other URL to fall back to — while the heights it does not offer are 404 on
// the explicit routes.
func TestHLSLegacyRouteWorksForASmallSource(t *testing.T) {
	old := hlsPlaylistWait
	hlsPlaylistWait = 100 * time.Millisecond
	t.Cleanup(func() { hlsPlaylistWait = old })

	dir := t.TempDir()
	srv := hlsServer(t, dir, writeHangingFFmpeg(t))
	// v1 is 1080p in the fixture; ask through the alias and the job starts.
	if rec := getMedia(t, srv, "/media/hls/v1/index.m3u8", ""); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("alias = %d, want 503 while the transcode runs", rec.Code)
	}
	waitForEntry(t, dir, media.HLSName("v1", media.HLSDefaultHeight))
}

// A height that is not one of the five, or one this source cannot fill, never
// reaches the filesystem and never starts a transcode.
func TestHLSRejectsBadHeights(t *testing.T) {
	dir := t.TempDir()
	srv := hlsServer(t, dir, writeHangingFFmpeg(t))
	for _, path := range []string{
		"/media/hls/v1/999/index.m3u8",
		"/media/hls/v1/1081/index.m3u8",
		"/media/hls/v1/0/index.m3u8",
		"/media/hls/v1/-720/index.m3u8",
		"/media/hls/v1/abc/index.m3u8",
		"/media/hls/v1/1080p/seg00000.m4s",
		// One of the five, but taller than the 1080p source: offering it would
		// be an upscale, so it is not offered and not served.
		"/media/hls/v1/2160/index.m3u8",
		"/media/hls/v1/1440/index.m3u8",
	} {
		if rec := getMedia(t, srv, path, ""); rec.Code != http.StatusNotFound {
			t.Errorf("%s = %d, want 404", path, rec.Code)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		t.Errorf("a rejected height started a transcode: %s", e.Name())
	}
}

// waitForEntry waits for a cache entry to appear. StartDir returns before the
// job has created anything — that is the point of it — so the filesystem lags
// the response by a moment.
func waitForEntry(t *testing.T, dir, name string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("cache entry %s was never created", name)
}

// Prefetch picks a height too, and refuses one the video does not offer rather
// than quietly starting a different transcode from the one asked for.
//
// Each case gets its own server: one transcode slot means a second job would
// queue behind the first hanging one and touch nothing on disk.
func TestPostVideoHLSHeight(t *testing.T) {
	dir := t.TempDir()
	rec := do(t, hlsServer(t, dir, writeHangingFFmpeg(t)), http.MethodPost, "/api/v1/videos/v1/hls?height=720", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("height=720 = %d: %s", rec.Code, rec.Body.String())
	}
	if got := decode[map[string]string](t, rec)["state"]; got != string(media.StateRunning) {
		t.Errorf("state = %q, want running", got)
	}
	waitForEntry(t, dir, media.HLSName("v1", 720))

	// No height at all starts the default one, the same the detail's hls_url
	// points at.
	defaultDir := t.TempDir()
	if rec := do(t, hlsServer(t, defaultDir, writeHangingFFmpeg(t)), http.MethodPost, "/api/v1/videos/v1/hls", ""); rec.Code != http.StatusOK {
		t.Fatalf("no height = %d: %s", rec.Code, rec.Body.String())
	}
	waitForEntry(t, defaultDir, media.HLSName("v1", media.HLSDefaultHeight))

	// A height this 1080p source cannot fill, or no height at all, is a 400 —
	// the client is working from a stale hls_variants.
	rejectDir := t.TempDir()
	reject := hlsServer(t, rejectDir, writeHangingFFmpeg(t))
	for _, q := range []string{"?height=2160", "?height=1440", "?height=999", "?height=abc", "?height=1080p", "?height=-720"} {
		if rec := do(t, reject, http.MethodPost, "/api/v1/videos/v1/hls"+q, ""); rec.Code != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", q, rec.Code)
		}
	}
	entries, err := os.ReadDir(rejectDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		t.Errorf("a rejected height started a transcode: %s", e.Name())
	}
}

// The detail's ladder is the contract clients pick from: tallest first, only
// what the source can fill, each with its own URL, codec and state.
func TestVideoDetailCarriesHLSVariants(t *testing.T) {
	dir := t.TempDir()
	writeHLSEntry(t, dir, "v1", 720, []byte("segment"))
	srv := hlsServer(t, dir, "ffmpeg")

	detail := decode[VideoDetail](t, do(t, srv, http.MethodGet, "/api/v1/videos/v1", ""))
	want := []HLSVariantInfo{
		{Height: 1080, URL: "/media/hls/v1/1080/index.m3u8", State: string(media.StatePending), Codec: "h264"},
		{Height: 720, URL: "/media/hls/v1/720/index.m3u8", State: string(media.StateDone), Codec: "h264"},
		{Height: 480, URL: "/media/hls/v1/480/index.m3u8", State: string(media.StatePending), Codec: "h264"},
	}
	if !reflect.DeepEqual(detail.HLSVariants, want) {
		t.Errorf("hls_variants =\n%+v\nwant\n%+v", detail.HLSVariants, want)
	}
	// hls_url stays, pointing at the default height.
	if detail.HLSURL != "/media/hls/v1/1080/index.m3u8" || detail.HLSState != string(media.StatePending) {
		t.Errorf("hls_url = %q, hls_state = %q", detail.HLSURL, detail.HLSState)
	}
}

// A 4K source offers the tall rungs, and they are HEVC — which is the whole
// reason `codec` is in the payload: a client that cannot decode it picks 1080
// or below.
func TestVideoDetailHLSVariantsForA4KSource(t *testing.T) {
	fake := ta.NewFake()
	fake.Videos["v4k"] = &ta.Video{
		YoutubeID: "v4k", Title: "4K", MediaURL: "/youtube/UC1/v4k.mp4",
		Streams: []ta.Stream{{Type: "video", Codec: "av01", Height: 2160}, {Type: "audio", Codec: "opus"}},
	}
	// A 480p source, to check the ladder stops at what it can fill.
	fake.Videos["vsmall"] = &ta.Video{
		YoutubeID: "vsmall", Title: "small", MediaURL: "/youtube/UC1/vsmall.mp4",
		Streams: []ta.Stream{{Type: "video", Codec: "vp09", Height: 480}, {Type: "audio", Codec: "opus"}},
	}
	srv := newTestServer(fake, newEventStore().querier()).Router()

	detail := decode[VideoDetail](t, do(t, srv, http.MethodGet, "/api/v1/videos/v4k", ""))
	var heights []int
	codecs := map[int]string{}
	for _, v := range detail.HLSVariants {
		heights = append(heights, v.Height)
		codecs[v.Height] = v.Codec
	}
	if !reflect.DeepEqual(heights, []int{2160, 1440, 1080, 720, 480}) {
		t.Errorf("4K heights = %v", heights)
	}
	for height, want := range map[int]string{2160: "hevc", 1440: "hevc", 1080: "h264", 720: "h264", 480: "h264"} {
		if codecs[height] != want {
			t.Errorf("%dp codec = %q, want %q", height, codecs[height], want)
		}
	}
	// Even on a 4K source the default rendition is 1080p H.264: nothing starts
	// a 4K transcode unless a client asks for one.
	if detail.HLSURL != "/media/hls/v4k/1080/index.m3u8" {
		t.Errorf("hls_url = %q", detail.HLSURL)
	}

	small := decode[VideoDetail](t, do(t, srv, http.MethodGet, "/api/v1/videos/vsmall", ""))
	if len(small.HLSVariants) != 1 || small.HLSVariants[0].Height != 480 {
		t.Errorf("480p source variants = %+v", small.HLSVariants)
	}
	if small.HLSURL != "/media/hls/vsmall/480/index.m3u8" {
		t.Errorf("hls_url for a 480p source = %q", small.HLSURL)
	}
}

// The JSON keys are the contract; a Go struct rename must not quietly move
// them.
func TestVideoDetailHLSVariantsJSONShape(t *testing.T) {
	h, _ := hlsFixture(t, []byte("segment"))
	body := decode[map[string]any](t, do(t, h, http.MethodGet, "/api/v1/videos/v1", ""))
	raw, ok := body["hls_variants"].([]any)
	if !ok || len(raw) != 3 {
		t.Fatalf("hls_variants = %#v", body["hls_variants"])
	}
	first, ok := raw[0].(map[string]any)
	if !ok {
		t.Fatalf("hls_variants[0] = %#v", raw[0])
	}
	for _, key := range []string{"height", "url", "state", "codec"} {
		if _, ok := first[key]; !ok {
			t.Errorf("hls_variants[0] has no %q: %#v", key, first)
		}
	}
	if first["height"] != float64(1080) || first["codec"] != "h264" {
		t.Errorf("hls_variants[0] = %#v", first)
	}
	for _, key := range []string{"hls_url", "hls_state"} {
		if _, ok := body[key]; !ok {
			t.Errorf("the detail lost %q", key)
		}
	}
}
