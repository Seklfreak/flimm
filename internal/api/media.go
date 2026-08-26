package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httputil"
	"os"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Seklfreak/flimm/internal/media"
)

// proxyTo rewrites the request path to the TA path and streams the upstream
// response through the given proxy.
func (s *Server) proxyTo(w http.ResponseWriter, r *http.Request, p *httputil.ReverseProxy, path string) {
	if p == nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	r2 := r.Clone(r.Context())
	r2.URL.Path = path
	r2.URL.RawPath = ""
	r2.URL.RawQuery = ""
	r2.RequestURI = ""
	p.ServeHTTP(w, r2)
}

// taMediaPath turns a TA media_url into the nginx path. TA stores the path
// as seen inside its container ("/youtube/<channel>/<file>"); nginx serves
// that directory as /media/. Accept the already-mapped and the bare relative
// forms too.
func taMediaPath(mediaURL string) string {
	switch {
	case strings.HasPrefix(mediaURL, "/youtube/"):
		return "/media/" + strings.TrimPrefix(mediaURL, "/youtube/")
	case strings.HasPrefix(mediaURL, "/media/"):
		return mediaURL
	}
	return "/media/" + strings.TrimPrefix(mediaURL, "/")
}

func (s *Server) mediaVideo(w http.ResponseWriter, r *http.Request) {
	v, err := s.ta.GetVideo(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.writeTAError(w, "get video", err)
		return
	}
	if v.MediaURL == "" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	s.proxyTo(w, r, s.mediaProxy, taMediaPath(v.MediaURL))
}

func (s *Server) mediaSubtitles(w http.ResponseWriter, r *http.Request) {
	v, err := s.ta.GetVideo(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.writeTAError(w, "get video", err)
		return
	}
	lang := chi.URLParam(r, "lang")
	for _, st := range v.Subtitles {
		if st.Lang == lang && st.MediaURL != "" {
			s.proxyTo(w, r, s.mediaProxy, taMediaPath(st.MediaURL))
			return
		}
	}
	writeError(w, http.StatusNotFound, "not found")
}

func (s *Server) mediaVideoThumb(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if len(id) < 2 || strings.ContainsAny(id, "/.") {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	// TA shards video thumbnails by the first character of the id, lowercased.
	s.proxyTo(w, r, s.thumbProxy, "/cache/videos/"+strings.ToLower(id[:1])+"/"+id+".jpg")
}

func (s *Server) mediaChannelThumb(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" || strings.ContainsAny(id, "/.") {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	s.proxyTo(w, r, s.thumbProxy, "/cache/channels/"+id+"_thumb.jpg")
}

func (s *Server) mediaChannelBanner(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" || strings.ContainsAny(id, "/.") {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	s.proxyTo(w, r, s.thumbProxy, "/cache/channels/"+id+"_banner.jpg")
}

func (s *Server) mediaPlaylistThumb(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" || strings.ContainsAny(id, "/.") {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	s.proxyTo(w, r, s.thumbProxy, "/cache/playlists/"+id+".jpg")
}

// validMediaID guards every path built from a video id. TA ids are YouTube
// ids, so anything outside this set is refused rather than reaching the
// filesystem or ffmpeg.
var validMediaID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// mediaAudio serves the audio-only rendition, deriving it on first request.
// Once on disk it is served with http.ServeContent, which handles Range,
// If-Range and 206 — so seeking and resume behave as they do for video.
func (s *Server) mediaAudio(w http.ResponseWriter, r *http.Request) {
	if s.mediaCache == nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	id := chi.URLParam(r, "id")
	if !validMediaID.MatchString(id) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	v, err := s.ta.GetVideo(r.Context(), id)
	if err != nil {
		s.writeTAError(w, "get video", err)
		return
	}
	if v.MediaURL == "" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	src := taMediaPath(v.MediaURL)
	path, err := s.mediaCache.Get(r.Context(), media.AudioVariant+"-"+id+media.AudioExt,
		media.Audio(s.ffmpegPath, func(ctx context.Context) (io.ReadCloser, error) {
			return s.ta.OpenMedia(ctx, src)
		}))
	if err != nil {
		s.log.Error("derive audio", "video", id, "err", err)
		writeError(w, http.StatusBadGateway, "could not prepare audio")
		return
	}
	f, err := os.Open(path) //nolint:gosec // path is the cache dir plus a validated id
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	w.Header().Set("Content-Type", media.AudioType)
	w.Header().Set("Cache-Control", "private, max-age=3600")
	http.ServeContent(w, r, id+media.AudioExt, st.ModTime(), f)
}
