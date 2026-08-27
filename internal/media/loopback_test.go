package media

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// loopbackReply is one response from the source server, read to the end and
// closed.
type loopbackReply struct {
	status        int
	contentLength int64
	header        http.Header
	body          []byte
}

func getLoopback(t *testing.T, url, rangeHeader string) loopbackReply {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rangeHeader != "" {
		req.Header.Set("Range", rangeHeader)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", url, err)
	}
	return loopbackReply{status: resp.StatusCode, contentLength: resp.ContentLength, header: resp.Header, body: body}
}

// The loopback source exists to make the input *seekable*, so what it has to
// get right is the range metadata: without Content-Length and a 206 with
// Content-Range, ffmpeg treats the input as a pipe and `-ss` goes back to
// decoding its way to the seek point.
func TestLoopbackSourcePassesRangesThrough(t *testing.T) {
	body := []byte("0123456789abcdefghij")
	lb, err := newLoopbackSource(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer lb.close()
	url, release := lb.register(testSource(body))
	defer release()

	whole := getLoopback(t, url, "")
	if whole.status != http.StatusOK {
		t.Fatalf("whole file = %d", whole.status)
	}
	if whole.contentLength != int64(len(body)) {
		t.Errorf("Content-Length = %d, want %d — without it ffmpeg cannot seek", whole.contentLength, len(body))
	}
	if got := whole.header.Get("Accept-Ranges"); got != "bytes" {
		t.Errorf("Accept-Ranges = %q, want bytes", got)
	}
	if !bytes.Equal(whole.body, body) {
		t.Errorf("body = %q, want %q", whole.body, body)
	}

	part := getLoopback(t, url, "bytes=5-9")
	if part.status != http.StatusPartialContent {
		t.Fatalf("range = %d, want 206", part.status)
	}
	if cr := part.header.Get("Content-Range"); cr != "bytes 5-9/20" {
		t.Errorf("Content-Range = %q", cr)
	}
	if string(part.body) != "56789" {
		t.Errorf("ranged body = %q, want %q", part.body, "56789")
	}

	// An open-ended range, which is what ffmpeg sends after a seek.
	tail := getLoopback(t, url, "bytes=15-")
	if tail.status != http.StatusPartialContent || string(tail.body) != "fghij" {
		t.Errorf("open-ended range = %d %q", tail.status, tail.body)
	}
}

// The nonce is the only thing standing between the archive and anything else
// on the box that can reach loopback, so it has to actually gate.
func TestLoopbackSourceNonceIsRequired(t *testing.T) {
	lb, err := newLoopbackSource(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer lb.close()
	url, release := lb.register(testSource([]byte("secret archive bytes")))

	base := url[:strings.LastIndex(url, "/")+1]
	for _, path := range []string{
		base,
		base + "0000000000000000000000000000000000",
		strings.TrimSuffix(base, "src/"),
		strings.TrimSuffix(base, "src/") + "etc/passwd",
	} {
		if resp := getLoopback(t, path, ""); resp.status != http.StatusNotFound {
			t.Errorf("%s = %d, want 404", path, resp.status)
		}
	}

	// The nonce itself is a 128-bit random token, not something guessable.
	nonce := url[strings.LastIndex(url, "/")+1:]
	if len(nonce) != 32 {
		t.Errorf("nonce = %q, want 32 hex characters (128 bits)", nonce)
	}
	second, _ := lb.register(testSource(nil))
	if second == url {
		t.Error("two jobs were given the same nonce")
	}

	// Once the job is over the nonce stops working, whether or not the
	// listener is still up.
	release()
	if resp := getLoopback(t, url, ""); resp.status != http.StatusNotFound {
		t.Errorf("a released nonce still serves the archive: %d", resp.status)
	}
}

// An upstream that is down is a 502 to ffmpeg, not a truncated file it would
// happily encode as the whole video.
func TestLoopbackSourceUpstreamFailureIs502(t *testing.T) {
	lb, err := newLoopbackSource(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer lb.close()
	url, release := lb.register(func(context.Context, string) (*SourceStream, error) {
		return nil, errors.New("tubearchivist unavailable")
	})
	defer release()

	if resp := getLoopback(t, url, ""); resp.status != http.StatusBadGateway {
		t.Errorf("upstream failure = %d, want 502", resp.status)
	}
}

// The reason the source is proxied at all: the TA token must never reach
// ffmpeg's command line, its environment or a log. What ffmpeg gets is a
// loopback URL with a nonce and nothing else.
func TestHLSNeverPutsTheTokenOnTheCommandLine(t *testing.T) {
	const token = "ta-token-abcdef0123456789"
	dir := t.TempDir()
	callLog := filepath.Join(dir, "calls.log")
	stub := writeStubHLSFFmpeg(t, dir, callLog, stubOptions{total: 2})
	out := filepath.Join(dir, "out")

	err := deriveHLS(t, HLSConfig{
		FFmpegPath: stub,
		Source:     HLSSource{VideoCodec: "av01", Height: 1080, AudioCodec: "opus", Duration: 8},
		Height:     1080,
		Open: func(_ context.Context, _ string) (*SourceStream, error) {
			// A real opener sets `Authorization: Token …` on its own request;
			// the point is that nothing about it reaches the child process.
			return &SourceStream{
				Body:          io.NopCloser(bytes.NewReader([]byte("source"))),
				StatusCode:    200,
				ContentLength: 6,
				AcceptRanges:  "bytes",
			}, nil
		},
	}, out)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}

	argv, err := os.ReadFile(callLog + ".argv") //nolint:gosec // test fixture path
	if err != nil {
		t.Fatal(err)
	}
	line := string(argv)
	if strings.Contains(line, token) {
		t.Errorf("the TA token reached ffmpeg's argv: %s", line)
	}
	for _, leak := range []string{"Authorization", "-headers", "Token "} {
		if strings.Contains(line, leak) {
			t.Errorf("ffmpeg's argv carries %q: %s", leak, line)
		}
	}
	// What it does carry is the loopback URL.
	if !strings.Contains(line, "http://127.0.0.1:") || !strings.Contains(line, "/src/") {
		t.Errorf("ffmpeg was not given a loopback source: %s", line)
	}
}
