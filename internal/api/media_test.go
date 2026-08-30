package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Seklfreak/flimm/internal/ta"
)

// testCookie builds an flimm_media cookie the way the server issues it.
func testCookie(value string) *http.Cookie {
	return &http.Cookie{Name: "flimm_media", Value: value, Path: "/media", HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode}
}

func mediaServer(t *testing.T) (*Server, *httptest.Server, *[]*http.Request) {
	t.Helper()
	var seen []*http.Request
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Clone(r.Context()))
		if r.Header.Get("Authorization") != "Token secret-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case strings.HasPrefix(r.URL.Path, "/cache/"):
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte("jpeg"))
		case r.URL.Path == "/media/A/v1.mp4":
			w.Header().Set("Content-Type", "video/mp4")
			w.Header().Set("Accept-Ranges", "bytes")
			if rng := r.Header.Get("Range"); rng == "bytes=100-199" {
				w.Header().Set("Content-Range", "bytes 100-199/1000")
				w.Header().Set("Content-Length", "100")
				w.WriteHeader(http.StatusPartialContent)
				_, _ = w.Write([]byte(strings.Repeat("x", 100)))
				return
			}
			w.Header().Set("Content-Length", "1000")
			_, _ = w.Write([]byte(strings.Repeat("x", 1000)))
		case r.URL.Path == "/media/A/v1.en.vtt":
			w.Header().Set("Content-Type", "text/vtt")
			_, _ = w.Write([]byte("WEBVTT"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(up.Close)
	proxy, err := ta.NewMediaProxy(up.URL, "secret-token")
	if err != nil {
		t.Fatal(err)
	}
	client := ta.NewFake()
	v := video("v1", "A", "2026-08-01", 1000, false)
	v.Subtitles = []ta.Subtitle{{Lang: "en", Source: "user", MediaURL: "A/v1.en.vtt"}}
	client.AddVideo(v)
	s := newTestServer(client, newEventStore().querier())
	s.mediaProxy = proxy
	thumbs := *proxy
	thumbs.ModifyResponse = NewServer(Options{TA: client, MediaProxy: proxy, MediaSecret: testSecret}).thumbProxy.ModifyResponse
	s.thumbProxy = &thumbs
	return s, up, &seen
}

func TestMediaAuthCookieVsBearer(t *testing.T) {
	s, _, _ := mediaServer(t)
	h := s.Router()

	// No credentials → 401.
	rec := do(t, h, http.MethodGet, "/media/thumb/video/v1", "")
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("no auth: %d", rec.Code)
	}

	// Bearer (dev verifier accepts any) → 200.
	req := httptest.NewRequest(http.MethodGet, "/media/thumb/video/v1", nil)
	req.Header.Set("Authorization", "Bearer whatever")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "private, max-age=86400" {
		t.Errorf("bearer: %d %v", rec.Code, rec.Header())
	}

	// Cookie issued by POST /session/media → 200. It answers 200 with the
	// token in the body rather than a bare 204: a native client has no cookie
	// jar, and the Apple TV's top shelf needs the token in a URL.
	rec = do(t, h, http.MethodPost, "/api/v1/session/media", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("session/media: %d", rec.Code)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "flimm_media" || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode || cookies[0].Path != "/media" {
		t.Fatalf("cookie = %+v", cookies)
	}
	// The cookie lives as long as the token it carries, whatever that is
	// configured to be — thirty days by default, because the Apple TV's top
	// shelf holds URLs the system fetches long after the app last ran.
	if cookies[0].MaxAge != int(defaultMediaTokenTTL.Seconds()) {
		t.Errorf("max-age = %d, want the token's own lifetime", cookies[0].MaxAge)
	}
	req = httptest.NewRequest(http.MethodGet, "/media/thumb/channel/A", nil)
	req.AddCookie(cookies[0])
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("cookie: %d %s", rec.Code, rec.Body.String())
	}

	// Tampered cookie → 401.
	req = httptest.NewRequest(http.MethodGet, "/media/thumb/channel/A", nil)
	req.AddCookie(testCookie(cookies[0].Value[:len(cookies[0].Value)-2] + "zz"))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("tampered: %d", rec.Code)
	}

	// Expired token → 401.
	req = httptest.NewRequest(http.MethodGet, "/media/thumb/channel/A", nil)
	req.AddCookie(testCookie(s.mediaToken(uuid.New(), time.Now().Add(-defaultMediaTokenTTL-time.Hour))))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expired: %d", rec.Code)
	}
}

func TestMediaProxyForwardsRange(t *testing.T) {
	s, _, seen := mediaServer(t)
	h := s.Router()
	cookie := testCookie(s.mediaToken(DevUserID, time.Now()))

	req := httptest.NewRequest(http.MethodGet, "/media/video/v1.mp4", nil)
	req.Header.Set("Range", "bytes=100-199")
	req.Header.Set("If-Range", `"etag"`)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusPartialContent {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	for k, want := range map[string]string{
		"Content-Range":  "bytes 100-199/1000",
		"Accept-Ranges":  "bytes",
		"Content-Length": "100",
		"Content-Type":   "video/mp4",
	} {
		if got := rec.Header().Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if rec.Body.Len() != 100 {
		t.Errorf("body len = %d", rec.Body.Len())
	}
	up := (*seen)[len(*seen)-1]
	if up.URL.Path != "/media/A/v1.mp4" || up.Header.Get("Range") != "bytes=100-199" || up.Header.Get("If-Range") != `"etag"` {
		t.Errorf("upstream request = %s %v", up.URL.Path, up.Header)
	}
	if up.Header.Get("Cookie") != "" {
		t.Error("browser cookie leaked upstream")
	}

	// Subtitles and a full (non-range) read.
	req = httptest.NewRequest(http.MethodGet, "/media/subtitles/v1/en.vtt", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "WEBVTT" {
		t.Errorf("subtitles: %d %s", rec.Code, rec.Body.String())
	}
	req = httptest.NewRequest(http.MethodGet, "/media/subtitles/v1/de.vtt", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown subtitle lang: %d", rec.Code)
	}
	req = httptest.NewRequest(http.MethodGet, "/media/video/v1.mp4", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	body, _ := io.ReadAll(rec.Body)
	if rec.Code != http.StatusOK || len(body) != 1000 || rec.Header().Get("Content-Length") != "1000" {
		t.Errorf("full read: %d len=%d", rec.Code, len(body))
	}
}

func TestMediaProxyStreamsWithoutBuffering(t *testing.T) {
	// The upstream writes a first chunk, then blocks until released; the
	// proxy must deliver that chunk before the response completes.
	release := make(chan struct{})
	first := make(chan struct{})
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("chunk1"))
		w.(http.Flusher).Flush()
		close(first)
		<-release
		_, _ = w.Write([]byte("chunk2"))
	}))
	defer up.Close()
	proxy, err := ta.NewMediaProxy(up.URL, "tok")
	if err != nil {
		t.Fatal(err)
	}
	client := ta.NewFake()
	client.AddVideo(video("v1", "A", "2026-08-01", 10, false))
	s := newTestServer(client, newEventStore().querier())
	s.mediaProxy = proxy
	front := httptest.NewServer(s.Router())
	defer front.Close()

	req, _ := http.NewRequest(http.MethodGet, front.URL+"/media/video/v1.mp4", nil)
	req.AddCookie(testCookie(s.mediaToken(DevUserID, time.Now())))
	resp, err := http.DefaultClient.Do(req) //nolint:bodyclose // closed below; the body is read from a goroutine first
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	<-first
	buf := make([]byte, 6)
	done := make(chan error, 1)
	go func() { _, err := io.ReadFull(resp.Body, buf); done <- err }()
	select {
	case err := <-done:
		if err != nil || string(buf) != "chunk1" {
			t.Fatalf("first chunk: %q %v", buf, err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("first chunk was buffered until upstream finished")
	}
	close(release)
	rest, _ := io.ReadAll(resp.Body)
	if string(rest) != "chunk2" {
		t.Errorf("rest = %q", rest)
	}
}

func TestSPAFallback(t *testing.T) {
	s := newTestServer(ta.NewFake(), newEventStore().querier())
	s.frontend = newFS(fstestMap{"index.html": "<!doctype html>app", "assets/app.js": "js"})
	h := s.Router()
	if rec := do(t, h, http.MethodGet, "/feeds/abc", ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "app") || rec.Header().Get("Cache-Control") != "no-cache" {
		t.Errorf("fallback: %d %q %v", rec.Code, rec.Body.String(), rec.Header())
	}
	if rec := do(t, h, http.MethodGet, "/assets/app.js", ""); rec.Code != http.StatusOK || rec.Body.String() != "js" || !strings.Contains(rec.Header().Get("Cache-Control"), "immutable") {
		t.Errorf("asset: %d %q", rec.Code, rec.Body.String())
	}
	if rec := do(t, h, http.MethodGet, "/api/v1/nope", ""); rec.Code == http.StatusOK {
		t.Errorf("api route fell through to SPA")
	}
}

func TestTAMediaPath(t *testing.T) {
	cases := map[string]string{
		"/youtube/UCabc/vid.mp4":    "/media/UCabc/vid.mp4",
		"/youtube/UCabc/vid.en.vtt": "/media/UCabc/vid.en.vtt",
		"/media/UCabc/vid.mp4":      "/media/UCabc/vid.mp4",
		"UCabc/vid.mp4":             "/media/UCabc/vid.mp4",
	}
	for in, want := range cases {
		if got := taMediaPath(in); got != want {
			t.Errorf("taMediaPath(%q) = %q, want %q", in, got, want)
		}
	}
}

// The Apple TV's top shelf is drawn by the system from URLs an extension hands
// it: a process with no header and no cookie of ours. The same signed token in
// the query is the only way that artwork can be authenticated.
func TestMediaTokenInTheQueryAuthenticates(t *testing.T) {
	s, _, _ := mediaServer(t)
	h := s.Router()

	rec := do(t, h, http.MethodPost, "/api/v1/session/media", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("session/media: %d", rec.Code)
	}
	var session MediaSession
	if err := json.Unmarshal(rec.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	if session.Token == "" || session.ExpiresIn == 0 {
		t.Fatalf("session = %+v, want a token a native client can put in a URL", session)
	}
	// The cookie is still set, because browsers use it.
	if len(rec.Result().Cookies()) != 1 {
		t.Error("the cookie went away; the web client depends on it")
	}

	req := httptest.NewRequest(http.MethodGet, "/media/thumb/video/v1?media_token="+session.Token, nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("token in the query: %d, want 200", rec.Code)
	}

	// And it is a real check, not a wave-through.
	req = httptest.NewRequest(http.MethodGet, "/media/thumb/video/v1?media_token=nonsense", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("a bad token: %d, want 401", rec.Code)
	}
}

// A credential in a log line is a credential given away, and chi's logger
// formats straight from the request URL.
func TestMediaTokenIsRedactedBeforeLogging(t *testing.T) {
	s, _, _ := mediaServer(t)
	var logged string
	probe := redactMediaToken(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		logged = r.URL.String()
	}))
	req := httptest.NewRequest(http.MethodGet, "/media/thumb/video/v1?media_token=secret-value", nil)
	probe.ServeHTTP(httptest.NewRecorder(), req)
	if strings.Contains(logged, "secret-value") {
		t.Errorf("the URL a logger would print still holds the token: %q", logged)
	}
	if !strings.Contains(logged, "redacted") {
		t.Errorf("URL = %q, want the parameter kept but redacted", logged)
	}
	_ = s
}
