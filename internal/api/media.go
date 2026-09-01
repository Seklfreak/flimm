package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httputil"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Seklfreak/flimm/internal/media"
	"github.com/Seklfreak/flimm/internal/ta"
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

// rangeSource hands a derivation a way to read the archived file over the
// loopback: byte ranges, and the TA token kept on this side of it.
func (s *Server) rangeSource(src string) media.RangeSourceFunc {
	return func(ctx context.Context, rangeHeader string) (*media.SourceStream, error) {
		st, err := s.ta.OpenMediaRange(ctx, src, rangeHeader)
		if err != nil {
			return nil, err
		}
		return &media.SourceStream{
			Body:          st.Body,
			StatusCode:    st.StatusCode,
			ContentLength: st.ContentLength,
			ContentRange:  st.ContentRange,
			AcceptRanges:  st.AcceptRanges,
			ContentType:   st.ContentType,
		}, nil
	}
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

// mediaAudio serves the WebM (Opus) audio-only rendition.
func (s *Server) mediaAudio(w http.ResponseWriter, r *http.Request) {
	s.serveDerivedAudio(w, r, media.AudioVariant, media.AudioExt, media.AudioType,
		func(_ *ta.Video, open media.SourceFunc) media.DeriveFunc {
			return media.Audio(s.ffmpegPath, open)
		})
}

// mediaAudioAAC serves the AAC/MP4 audio-only rendition, the one Apple clients
// can decode. The source audio codec decides copy vs re-encode, so it is read
// off the video document rather than probed.
func (s *Server) mediaAudioAAC(w http.ResponseWriter, r *http.Request) {
	s.serveDerivedAudio(w, r, media.AudioAACVariant, media.AudioAACExt, media.AudioAACType,
		func(v *ta.Video, open media.SourceFunc) media.DeriveFunc {
			return media.AudioAAC(s.ffmpegPath, sourceAudioCodec(v), open)
		})
}

// sourceAudioCodec is the codec of the video's first audio stream, "" when TA
// reports none.
func sourceAudioCodec(v *ta.Video) string {
	for _, st := range v.Streams {
		if st.Type == "audio" {
			return st.Codec
		}
	}
	return ""
}

// serveDerivedAudio serves an audio-only rendition, deriving it on first
// request. Once on disk it is served with http.ServeContent, which handles
// Range, If-Range and 206 — so seeking and resume behave as they do for video.
func (s *Server) serveDerivedAudio(w http.ResponseWriter, r *http.Request, variant, ext, contentType string,
	derive func(v *ta.Video, open media.SourceFunc) media.DeriveFunc,
) {
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
	path, err := s.mediaCache.Get(r.Context(), variant+"-"+id+ext,
		derive(v, func(ctx context.Context) (io.ReadCloser, error) {
			return s.ta.OpenMedia(ctx, src)
		}))
	if err != nil {
		s.log.Error("derive audio", "video", id, "variant", variant, "err", err)
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
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "private, max-age=3600")
	http.ServeContent(w, r, id+ext, st.ModTime(), f)
}

// mediaFrame serves one still of a video, cut on first request and cached.
//
// This is what a DeArrow thumbnail resolves to: the service returns a
// *timestamp*, and the frame is taken from the archive's own copy of the
// video. No third party is asked for an image, nothing is fetched at render
// time, and a thumbnail keeps working with the archive offline — which is why
// this belongs in Flimm rather than in a browser extension.
//
// The path carries milliseconds so an entry is keyed by an integer: a cache
// keyed on a float is a cache that misses on rounding.
func (s *Server) mediaFrame(w http.ResponseWriter, r *http.Request) {
	if s.mediaCache == nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	id := chi.URLParam(r, "id")
	if !validMediaID.MatchString(id) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	ms, err := strconv.ParseInt(chi.URLParam(r, "ms"), 10, 64)
	if err != nil || ms < 0 || ms > maxFrameMillis {
		writeError(w, http.StatusBadRequest, "ms must be a position in the video")
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
	name := media.FrameVariant + "-" + id + "-" + strconv.FormatInt(ms, 10) + media.FrameExt
	path, err := s.mediaCache.Get(r.Context(), name,
		media.Frame(s.ffmpegPath, float64(ms)/1000, s.log, s.rangeSource(src)))
	if err != nil {
		// A frame that cannot be cut is not worth an error page: the client
		// asked for a thumbnail, and the archive has one of its own. Serve
		// that instead of leaving a hole in the grid.
		s.log.Debug("derive frame", "video", id, "ms", ms, "err", err)
		s.mediaVideoThumb(w, r)
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
	w.Header().Set("Content-Type", media.FrameType)
	// A frame of a fixed timestamp of an archived file never changes.
	w.Header().Set("Cache-Control", "private, max-age=86400, immutable")
	http.ServeContent(w, r, id+media.FrameExt, st.ModTime(), f)
}

// maxFrameMillis is a day, which is longer than anything TubeArchivist holds
// and short enough that a silly `ms` is refused rather than handed to ffmpeg.
const maxFrameMillis = 24 * 60 * 60 * 1000

// previewName is the cache directory holding a video's scrub-preview sheet and
// its track.
func previewName(id string) string { return media.PreviewVariant + "-" + id }

func previewTrackURL(id string) string {
	return "/media/preview/" + id + "/" + media.PreviewTrackName
}

// previewPending is the 404 a client gets while the sheet is being made. It
// carries the job's own state and how far it has got: a scrubber with no
// pictures is otherwise indistinguishable from one whose derivation failed,
// and a wait with no number behind it is indistinguishable from a wedge.
type previewPending struct {
	Error string `json:"error"`
	State string `json:"state"`
	// Progress is 0–1 through the source. A decode of the whole file is the
	// honest cost of stills at a regular interval, and on a long video that is
	// minutes.
	Progress float64 `json:"progress"`
}

// mediaPreview serves the scrub-preview track and its sheet, and starts the
// derivation on the first request for either.
//
// A player asking for the track is the signal that someone is watching this
// video: nothing derives a preview for an archive nobody has opened, which is
// the only reason a full decode per video is affordable at all.
//
// While it is being made the answer is 404. That is deliberate: a scrubber
// without pictures is a scrubber, and a player that waited on this would be a
// player that opened slowly.
func (s *Server) mediaPreview(w http.ResponseWriter, r *http.Request) {
	if s.mediaCache == nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	id := chi.URLParam(r, "id")
	file := chi.URLParam(r, "file")
	if !validMediaID.MatchString(id) || (file != media.PreviewTrackName && file != media.PreviewSheetName) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	v, err := s.ta.GetVideo(r.Context(), id)
	if err != nil {
		s.writeTAError(w, "get video", err)
		return
	}
	if v.MediaURL == "" || v.Player.Duration <= 0 {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	name := previewName(id)
	src := taMediaPath(v.MediaURL)
	state := s.mediaCache.StartScan(name, media.Preview(s.ffmpegPath, v.Player.Duration, s.log,
		s.rangeSource(src), s.mediaCache.ReportDirProgress(name)))
	dir := s.mediaCache.Dir(name)
	if !media.PreviewReady(dir) {
		// Being made, or it failed. Either way there is nothing to show yet —
		// but which of the two it is is the whole question when a scrubber has
		// no pictures, so the 404 carries the job's state rather than making
		// the client guess from how long it has been asking.
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusNotFound, previewPending{
			Error:    "preview not ready",
			State:    string(state),
			Progress: s.mediaCache.DirProgress(name),
		})
		return
	}
	s.mediaCache.TouchDir(name)
	f, err := os.Open(filepath.Join(dir, file)) //nolint:gosec // dir is the cache, file is one of two literals
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
	if file == media.PreviewTrackName {
		w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
	} else {
		w.Header().Set("Content-Type", media.FrameType)
	}
	// The stills of an archived file never change.
	w.Header().Set("Cache-Control", "private, max-age=86400, immutable")
	http.ServeContent(w, r, file, st.ModTime(), f)
}
