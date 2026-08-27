package media

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The loopback source: a tiny HTTP server on 127.0.0.1 that hands the archived
// file to ffmpeg *seekably*.
//
// ffmpeg's `-ss` on a pipe is not a seek. It decodes and discards everything
// before the seek point, so starting a transcode at 40:00 first costs 40
// minutes of decoding — which defeats the entire point of resuming there. Over
// HTTP, `-ss` before `-i` turns into byte-range requests and lands in
// milliseconds.
//
// The obvious way to give ffmpeg an HTTP source would be TubeArchivist's own
// URL, but that would put `TA_TOKEN` in argv (via `-headers`), in the child's
// environment, or in a log line — three places it must never be. So the source
// is proxied instead: this server holds the credentials, ffmpeg holds only
// `http://127.0.0.1:<ephemeral>/src/<nonce>`, and the nonce is a per-job
// 128-bit random token that stops existing when the job does.
//
// The listener is bound to 127.0.0.1 only, so nothing off the box can reach it
// even during the seconds the nonce is live.

// SourceStream is one upstream media response, with the metadata the loopback
// server has to mirror for ffmpeg to treat the input as seekable.
type SourceStream struct {
	Body io.ReadCloser
	// StatusCode is the upstream status: 200 for a whole file, 206 for a
	// range. 0 means 200.
	StatusCode int
	// ContentLength is the body's length, or -1 when it is unknown. ffmpeg
	// needs the length of the *unranged* response to know the file size, which
	// is what makes seeking possible at all.
	ContentLength int64
	ContentRange  string
	AcceptRanges  string
	ContentType   string
}

// RangeSourceFunc opens the source file, honouring an HTTP Range header
// ("" for the whole file). It is called once per request ffmpeg makes, so it
// must be safe to call repeatedly and concurrently.
type RangeSourceFunc func(ctx context.Context, rangeHeader string) (*SourceStream, error)

// loopbackPrefix is the only path the source server answers on.
const loopbackPrefix = "/src/"

// loopbackSource serves registered sources to a local ffmpeg. One is started
// per HLS job and closed when that job ends, so a nonce cannot outlive the
// work it was minted for.
type loopbackSource struct {
	ln  net.Listener
	srv *http.Server
	log *slog.Logger

	mu    sync.Mutex
	opens map[string]RangeSourceFunc
}

// newLoopbackSource binds an ephemeral port on the loopback interface and
// starts serving.
func newLoopbackSource(log *slog.Logger) (*loopbackSource, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("hls: loopback source: %w", err)
	}
	s := &loopbackSource{ln: ln, log: log, opens: map[string]RangeSourceFunc{}}
	s.srv = &http.Server{
		Handler:           http.HandlerFunc(s.serve),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() { _ = s.srv.Serve(ln) }()
	return s, nil
}

// register mints a nonce for open and returns the URL ffmpeg should read,
// together with the function that invalidates it. Every later request for that
// nonce is a 404, whether or not the server is still listening.
func (s *loopbackSource) register(open RangeSourceFunc) (string, func()) {
	var b [16]byte
	// crypto/rand.Read never returns an error; it panics on a broken system.
	_, _ = rand.Read(b[:])
	nonce := hex.EncodeToString(b[:])

	s.mu.Lock()
	s.opens[nonce] = open
	s.mu.Unlock()

	return "http://" + s.ln.Addr().String() + loopbackPrefix + nonce, func() {
		s.mu.Lock()
		delete(s.opens, nonce)
		s.mu.Unlock()
	}
}

// close stops the server, cutting any read still in flight. Close rather than
// Shutdown on purpose: the job is over, and a killed ffmpeg's half-read body
// is not something to wait politely for.
func (s *loopbackSource) close() { _ = s.srv.Close() }

func (s *loopbackSource) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	nonce, ok := strings.CutPrefix(r.URL.Path, loopbackPrefix)
	if !ok {
		http.NotFound(w, r)
		return
	}
	s.mu.Lock()
	open := s.opens[nonce]
	s.mu.Unlock()
	if open == nil {
		// Unknown or already-released nonce. Nothing distinguishes the two,
		// which is the point.
		http.NotFound(w, r)
		return
	}

	up, err := open(r.Context(), r.Header.Get("Range"))
	if err != nil {
		if s.log != nil && r.Context().Err() == nil {
			s.log.Warn("hls source", "err", scrubSecrets(err.Error()))
		}
		http.Error(w, "upstream unavailable", http.StatusBadGateway)
		return
	}
	defer up.Body.Close()

	h := w.Header()
	if up.ContentType != "" {
		h.Set("Content-Type", up.ContentType)
	}
	// Accept-Ranges and Content-Range are what tell ffmpeg the input can be
	// seeked; without them it falls back to reading the file from the top.
	if up.AcceptRanges != "" {
		h.Set("Accept-Ranges", up.AcceptRanges)
	}
	if up.ContentRange != "" {
		h.Set("Content-Range", up.ContentRange)
	}
	if up.ContentLength >= 0 {
		h.Set("Content-Length", strconv.FormatInt(up.ContentLength, 10))
	}
	status := up.StatusCode
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = io.Copy(w, up.Body)
}
