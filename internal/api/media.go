package api

import (
	"net/http"
	"net/http/httputil"
	"strings"

	"github.com/go-chi/chi/v5"
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

// taMediaPath turns a TA media_url (relative, or already /media/-prefixed)
// into the nginx path.
func taMediaPath(mediaURL string) string {
	if strings.HasPrefix(mediaURL, "/media/") {
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
	s.proxyTo(w, r, s.thumbProxy, "/cache/videos/"+id[:2]+"/"+id+".jpg")
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
