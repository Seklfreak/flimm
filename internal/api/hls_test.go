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
	playlist := "#EXTM3U\n#EXT-X-VERSION:7\n#EXT-X-TARGETDURATION:4\n#EXT-X-PLAYLIST-TYPE:VOD\n" +
		"#EXT-X-INDEPENDENT-SEGMENTS\n#EXT-X-MAP:URI=\"init.mp4\"\n#EXTINF:4.000,\nseg00000.m4s\n#EXT-X-ENDLIST\n"
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
	return hlsServerWith(t, dir, ffmpegPath, nil)
}

// hlsServerWith is hlsServer with the media options a test needs to change —
// the segment wait above all, which is a minute in production.
func hlsServerWith(t *testing.T, dir, ffmpegPath string, tweak func(*Options)) http.Handler {
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
	opts := Options{
		Querier:     newEventStore().querier(),
		TA:          client,
		Log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		AppName:     "Flimm",
		MediaSecret: testSecret,
		MediaCache:  cache,
		FFmpegPath:  ffmpegPath,
		SegmentWait: 2 * time.Second,
	}
	if tweak != nil {
		tweak(&opts)
	}
	return NewServer(opts).Router()
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
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexec sleep 30\n"), 0o700); err != nil { //nolint:gosec // test fixture must be executable
		t.Fatal(err)
	}
	return path
}

// The whole point of the resume-first rendition: the playlist describes the
// entire video from the very first request, before a single segment has been
// encoded, so a player can seek straight to where the viewer left off.
func TestHLSPlaylistIsCompleteBeforeAnySegmentExists(t *testing.T) {
	h := hlsServer(t, t.TempDir(), writeHangingFFmpeg(t))

	rec := getMedia(t, h, "/media/hls/v1/index.m3u8", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	// v1 is ten minutes long in the fixture: 150 four-second segments.
	if n := strings.Count(body, ".m4s"); n != 150 {
		t.Errorf("playlist names %d segments, want 150:\n%s", n, body)
	}
	for _, want := range []string{"#EXT-X-PLAYLIST-TYPE:VOD", "#EXT-X-TARGETDURATION:4", "#EXT-X-ENDLIST", `#EXT-X-MAP:URI="init.mp4"`} {
		if !strings.Contains(body, want) {
			t.Errorf("playlist is missing %q:\n%s", want, body)
		}
	}
	// It is rewritten once at the end with the real durations, so it must not
	// be cached while the job runs.
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
}

// writeHangingFFmpegAndProbe installs an ffmpeg stub whose ffprobe sibling
// hangs too, so even working out how long the video is never finishes. It is
// the only way the playlist itself can be late.
func writeHangingFFmpegAndProbe(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"ffmpeg", "ffprobe"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\nexec sleep 30\n"), 0o700); err != nil { //nolint:gosec // test fixture must be executable
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, "ffmpeg")
}

// A playlist that cannot be produced in time is not an error: the client is
// told to come back, and the job keeps going.
func TestHLSPlaylistTimesOutWith503(t *testing.T) {
	old := hlsPlaylistWait
	hlsPlaylistWait = 100 * time.Millisecond
	t.Cleanup(func() { hlsPlaylistWait = old })

	// A video TA reports no duration for, so the job has to probe the source
	// before it can write a playlist — and the probe hangs.
	h := hlsServerWith(t, t.TempDir(), writeHangingFFmpegAndProbe(t), func(o *Options) {
		o.TA.(*ta.Fake).Videos["v1"].Player.Duration = 0
	})
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

// A segment of a finished rendition that is not there was never part of it —
// the playlist stops at the end of the video — so it is a 404 rather than a
// wait for something no run will produce.
func TestHLSMissingSegmentIs404(t *testing.T) {
	h, _ := hlsFixture(t, []byte("segment"))
	if rec := getMedia(t, h, "/media/hls/v1/seg00042.m4s", ""); rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

// A segment the encoder has not reached blocks rather than 404ing: the playlist
// promised it, so the honest answer is "not yet" and not "no such thing".
func TestHLSSegmentWaitsThenTimesOutWith503(t *testing.T) {
	h := hlsServerWith(t, t.TempDir(), writeHangingFFmpeg(t), func(o *Options) {
		o.SegmentWait = 300 * time.Millisecond
	})
	// The playlist request is what starts the job.
	if rec := getMedia(t, h, "/media/hls/v1/index.m3u8", ""); rec.Code != http.StatusOK {
		t.Fatalf("playlist = %d: %s", rec.Code, rec.Body.String())
	}

	start := time.Now()
	rec := getMedia(t, h, "/media/hls/v1/seg00000.m4s", "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("segment = %d, want 503: %s", rec.Code, rec.Body.String())
	}
	if elapsed := time.Since(start); elapsed < 250*time.Millisecond {
		t.Errorf("the request gave up after %v; it should wait out MEDIA_SEGMENT_WAIT", elapsed)
	}
	if ra := rec.Header().Get("Retry-After"); ra != "2" {
		t.Errorf("Retry-After = %q, want 2", ra)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
}

// A segment past the end of the rendition is never coming, so it must not
// occupy a connection for a minute first.
func TestHLSSegmentPastTheEndIs404Immediately(t *testing.T) {
	h := hlsServerWith(t, t.TempDir(), writeHangingFFmpeg(t), func(o *Options) {
		o.SegmentWait = 10 * time.Second
	})
	if rec := getMedia(t, h, "/media/hls/v1/index.m3u8", ""); rec.Code != http.StatusOK {
		t.Fatalf("playlist = %d", rec.Code)
	}
	start := time.Now()
	// v1 has 150 segments.
	if rec := getMedia(t, h, "/media/hls/v1/seg00200.m4s", ""); rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("a segment past the end waited %v before 404ing", elapsed)
	}
}

// A job that failed is a 502 on its segments, not a wait: there is nothing
// coming.
func TestHLSSegmentOfAFailedJobIs502(t *testing.T) {
	failing := filepath.Join(t.TempDir(), "ffmpeg-fails")
	if err := os.WriteFile(failing, []byte("#!/bin/sh\necho 'Invalid data found' >&2\nexit 1\n"), 0o700); err != nil { //nolint:gosec // test fixture must be executable
		t.Fatal(err)
	}
	h := hlsServerWith(t, t.TempDir(), failing, func(o *Options) { o.SegmentWait = 5 * time.Second })
	if rec := do(t, h, http.MethodPost, "/api/v1/videos/v1/hls", ""); rec.Code != http.StatusOK {
		t.Fatalf("start = %d: %s", rec.Code, rec.Body.String())
	}
	waitForHLSState(t, h, "v1", string(media.StateFailed))

	rec := getMedia(t, h, "/media/hls/v1/seg00000.m4s", "")
	if rec.Code != http.StatusBadGateway {
		t.Errorf("segment of a failed job = %d, want 502: %s", rec.Code, rec.Body.String())
	}
}

// waitForHLSState polls the video detail until the default rendition reaches a
// state. The job runs behind the request that started it, so the state clients
// see lags the response by a moment.
func waitForHLSState(t *testing.T, h http.Handler, id, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if decode[VideoDetail](t, do(t, h, http.MethodGet, "/api/v1/videos/"+id, "")).HLSState == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("hls_state never reached %q", want)
}

// writeRecordingFFmpeg installs a stub that records its command line and stops.
// It is enough to see *where* a job aimed its first run, which is the whole
// contract behind `from`.
func writeRecordingFFmpeg(t *testing.T, argvLog string) string {
	t.Helper()
	return writeRecordingFFmpegHold(t, argvLog, 0)
}

// writeRecordingFFmpegHold is writeRecordingFFmpeg for a stub that must still
// be running when the test looks: it logs its argv, then holds for `hold`
// seconds before exiting. A stub that exits at once produces no segments, and
// the run fails a few milliseconds later — on a fast machine before the
// request under test has even read the playlist, which is a race, not a
// result.
func writeRecordingFFmpegHold(t *testing.T, argvLog string, hold int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ffmpeg-record")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + argvLog + "\nsleep " + strconv.Itoa(hold) + "\nexit 0\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil { //nolint:gosec // test fixture must be executable
		t.Fatal(err)
	}
	return path
}

// readArgv waits for the stub to be called and returns its first command line.
func readArgv(t *testing.T, path string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		b, err := os.ReadFile(path) //nolint:gosec // test fixture path
		if err == nil && len(b) > 0 {
			line, _, _ := strings.Cut(string(b), "\n")
			return line
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("ffmpeg was never called")
	return ""
}

// `from` is what makes resuming instant: the first run starts at the segment
// the viewer is on, instead of at 0:00 with the encoder grinding its way there.
func TestPostVideoHLSStartsAtTheResumePosition(t *testing.T) {
	argv := filepath.Join(t.TempDir(), "argv.log")
	h := hlsServer(t, t.TempDir(), writeRecordingFFmpeg(t, argv))

	// v1 is ten minutes long; 400 s into it is segment 100.
	rec := do(t, h, http.MethodPost, "/api/v1/videos/v1/hls?from=400", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	line := readArgv(t, argv)
	if !strings.Contains(line, "-ss 400 ") {
		t.Errorf("the first run does not seek to the resume position: %s", line)
	}
	if !strings.Contains(line, "-start_number 100 ") {
		t.Errorf("the first run does not number segments from the resume point: %s", line)
	}
	// The run's segments are written under a name the route will not serve
	// until their timestamps have been moved onto the rendition's timeline.
	if !strings.Contains(line, "-hls_segment_filename seg%05d.m4s.raw ") {
		t.Errorf("the run publishes segments before rebasing them: %s", line)
	}
	// Run A goes to the end of the video, so it is not cut short.
	if strings.Contains(line, "-t ") {
		t.Errorf("run A carries a duration limit: %s", line)
	}
}

// A client that cannot POST first passes `from` on the playlist instead, and
// gets the same behaviour.
func TestHLSPlaylistFromStartsAtTheResumePosition(t *testing.T) {
	argv := filepath.Join(t.TempDir(), "argv.log")
	h := hlsServer(t, t.TempDir(), writeRecordingFFmpegHold(t, argv, 3))

	rec := getMedia(t, h, "/media/hls/v1/1080/index.m3u8?from=120", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("playlist = %d: %s", rec.Code, rec.Body.String())
	}
	// The playlist is the whole video whatever `from` says — that is what lets
	// the player seek to it.
	if n := strings.Count(rec.Body.String(), ".m4s"); n != 150 {
		t.Errorf("playlist names %d segments, want 150", n)
	}
	if line := readArgv(t, argv); !strings.Contains(line, "-ss 120 ") || !strings.Contains(line, "-start_number 30 ") {
		t.Errorf("the transcode did not start at the resume position: %s", line)
	}
}

// A `from` that is not a position inside the video is not an error; it means
// "from the beginning", which is what a client that does not send one gets.
func TestHLSIgnoresAnUnusableFrom(t *testing.T) {
	// The last one is past the end of a ten-minute video.
	for _, q := range []string{"?from=", "?from=abc", "?from=-30", "?from=NaN", "?from=99999"} {
		argv := filepath.Join(t.TempDir(), "argv.log")
		h := hlsServer(t, t.TempDir(), writeRecordingFFmpeg(t, argv))
		if rec := do(t, h, http.MethodPost, "/api/v1/videos/v1/hls"+q, ""); rec.Code != http.StatusOK {
			t.Fatalf("%s = %d", q, rec.Code)
		}
		if line := readArgv(t, argv); strings.Contains(line, "-ss ") {
			t.Errorf("%s produced a seek: %s", q, line)
		}
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
		body := decode[HLSStartResponse](t, rec)
		if body.State != string(media.StateRunning) {
			t.Errorf("state = %q, want %q", body.State, media.StateRunning)
		}
		if body.Height != media.HLSDefaultHeight {
			t.Errorf("height = %d, want %d", body.Height, media.HLSDefaultHeight)
		}
		if body.Progress != 0 {
			t.Errorf("hls_progress = %v on a job that has just started, want 0", body.Progress)
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

// The whole feature through the real ffmpeg: a viewer resuming at 12 s of a
// 20-second video. The playlist covers the whole video on the very first
// request, and the segment they are actually on is encoded before the ones
// before it.
func TestHLSResumeEndToEndWithRealFFmpeg(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed; skipping derivation test")
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "src.mp4")
	//nolint:gosec // G204: fixture paths from t.TempDir(), no request data
	fixture := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc=duration=20:size=320x240:rate=24",
		"-f", "lavfi", "-i", "sine=duration=20",
		"-c:v", "libx264", "-c:a", "aac", "-movflags", "+faststart", "-y", source)
	if err := fixture.Run(); err != nil {
		t.Skipf("cannot build fixture: %v", err)
	}
	body, err := os.ReadFile(source) //nolint:gosec // test fixture path
	if err != nil {
		t.Fatal(err)
	}

	cacheDir := t.TempDir()
	cache, err := media.NewCache(cacheDir, 0, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cache.Close)
	client := ta.NewFake()
	// Claimed as AV1 so the real encode path runs — a stream copy would be a
	// single run and prove nothing about resuming.
	client.Videos["v1"] = &ta.Video{
		YoutubeID: "v1", Title: "Video v1", MediaURL: "/youtube/UC1/v1.mp4",
		Player:  ta.Player{Duration: 20},
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

	// The very first request, with the resume position on it.
	rec := getMedia(t, h, "/media/hls/v1/480/index.m3u8?from=12", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("playlist status = %d: %s", rec.Code, rec.Body.String())
	}
	playlist := rec.Body.String()
	// 20 seconds is five four-second segments, all of them there before
	// anything has been encoded — which is what lets the player seek to 12 s.
	if n := strings.Count(playlist, ".m4s"); n != 5 {
		t.Fatalf("the first playlist names %d segments, want 5:\n%s", n, playlist)
	}
	if !strings.Contains(playlist, "#EXT-X-PLAYLIST-TYPE:VOD") || !strings.Contains(playlist, "#EXT-X-ENDLIST") {
		t.Errorf("the first playlist is not a complete VOD list:\n%s", playlist)
	}

	// The segment at 12 s is the one the viewer needs; the request blocks until
	// it lands rather than 404ing.
	if seg := getMedia(t, h, "/media/hls/v1/480/seg00003.m4s", ""); seg.Code != http.StatusOK || seg.Body.Len() == 0 {
		t.Fatalf("the resume segment = %d, %d bytes", seg.Code, seg.Body.Len())
	}

	entry := cache.Dir(media.HLSName("v1", 480))
	deadline := time.Now().Add(120 * time.Second)
	for time.Now().Before(deadline) && cache.DirState(media.HLSName("v1", 480)) != media.StateDone {
		time.Sleep(20 * time.Millisecond)
	}
	if st := cache.DirState(media.HLSName("v1", 480)); st != media.StateDone {
		t.Fatalf("hls_state = %q, want done", st)
	}

	// Run A came first: the segment the viewer resumed on was written before
	// the one at the start of the video.
	resume, err := os.Stat(filepath.Join(entry, "seg00003.m4s"))
	if err != nil {
		t.Fatal(err)
	}
	first, err := os.Stat(filepath.Join(entry, "seg00000.m4s"))
	if err != nil {
		t.Fatal(err)
	}
	if !resume.ModTime().Before(first.ModTime()) {
		t.Errorf("seg00003 (%v) was not encoded before seg00000 (%v); the resume point did not go first",
			resume.ModTime(), first.ModTime())
	}

	// Every segment the playlist promised is on disk and servable.
	for i := range 5 {
		name := media.HLSSegmentName(i)
		seg := getMedia(t, h, "/media/hls/v1/480/"+name, "")
		if seg.Code != http.StatusOK || seg.Body.Len() == 0 {
			t.Errorf("segment %s = %d, %d bytes", name, seg.Code, seg.Body.Len())
		}
	}
	if init := getMedia(t, h, "/media/hls/v1/480/init.mp4", ""); init.Code != http.StatusOK || init.Body.Len() == 0 {
		t.Errorf("init segment = %d, %d bytes", init.Code, init.Body.Len())
	}
	final := getMedia(t, h, "/media/hls/v1/480/index.m3u8", "")
	if cc := final.Header().Get("Cache-Control"); cc != "private, max-age=3600" {
		t.Errorf("finished playlist Cache-Control = %q", cc)
	}
	detail := decode[VideoDetail](t, do(t, h, http.MethodGet, "/api/v1/videos/v1", ""))
	for _, v := range detail.HLSVariants {
		if v.Height == 480 && v.Progress != 1 {
			t.Errorf("hls_progress = %v for a finished rendition, want 1", v.Progress)
		}
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
	dir := t.TempDir()
	srv := hlsServer(t, dir, writeHangingFFmpeg(t))
	// v1 is 1080p in the fixture; ask through the alias and the job starts.
	if rec := getMedia(t, srv, "/media/hls/v1/index.m3u8", ""); rec.Code != http.StatusOK {
		t.Fatalf("alias = %d, want the playlist while the transcode runs", rec.Code)
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
	if got := decode[HLSStartResponse](t, rec); got.State != string(media.StateRunning) || got.Height != 720 {
		t.Errorf("height=720 response = %+v, want running at 720", got)
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
		{Height: 1080, URL: "/media/hls/v1/1080/index.m3u8", State: string(media.StatePending), Codec: "h264", Progress: 0},
		// A finished rendition is 100% transcoded, whether or not its job is
		// still around to say so.
		{Height: 720, URL: "/media/hls/v1/720/index.m3u8", State: string(media.StateDone), Codec: "h264", Progress: 1},
		{Height: 480, URL: "/media/hls/v1/480/index.m3u8", State: string(media.StatePending), Codec: "h264", Progress: 0},
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
	for _, key := range []string{"height", "url", "state", "codec", "hls_progress"} {
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
