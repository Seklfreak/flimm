package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Seklfreak/flimm/internal/media"
	"github.com/Seklfreak/flimm/internal/ta"
)

// The compatible video renditions: HLS with fMP4 segments, one per offered
// height, derived on first request. See docs/api.md "Compatible video
// renditions (HLS)".
//
// The playlist request is the one that waits. It starts the transcode, blocks
// until the first segments exist and then serves the playlist as it stands —
// so a viewer starts watching after a few seconds rather than after the whole
// video has been transcoded. Segment requests never wait and never touch
// TubeArchivist: by the time one arrives the job is running and the file is
// either there or not yet, and which height it belongs to is in the path.
// hlsRetryAfter is what a client is told to wait before asking again.
const hlsRetryAfter = "5"

// hlsPlaylistWait bounds the block on the first segments. Long enough for a
// slow box to encode four seconds of video, short enough that a client is told
// to come back rather than sitting on a dead connection. A variable so tests
// need not wait out the real one.
var hlsPlaylistWait = 45 * time.Second

// validHLSFile is the complete set of names the rendition contains. Anything
// else — a traversal, a stray temp file, the completion marker — is a 404
// before it reaches the filesystem.
var validHLSFile = regexp.MustCompile(`^(index\.m3u8|init\.mp4|seg[0-9]{5}\.m4s)$`)

// errHLSHeightNotOffered is what starting a rendition the video cannot fill
// returns: a 4K rendition of a 1080p source is a transcode nobody asked for.
var errHLSHeightNotOffered = errors.New("hls: height not offered for this video")

// hlsURL is the playlist a client loads to play the rendition at height.
func hlsURL(id string, height int) string {
	return "/media/hls/" + id + "/" + strconv.Itoa(height) + "/" + media.HLSPlaylistName
}

// mediaHLS serves one file of a video's rendition at a given height. Both the
// playlist and the segments go through the same route because AVPlayer
// re-sends the media credentials on every segment request, and they must all
// be gated the same way.
func (s *Server) mediaHLS(w http.ResponseWriter, r *http.Request) {
	height, err := strconv.Atoi(chi.URLParam(r, "height"))
	if err != nil || !media.ValidHLSHeight(height) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	s.serveHLSVariant(w, r, height, true)
}

// mediaHLSDefault is the route without a height clients written before the ladder
// existed still use. It is an alias, not a variant of its own: it serves the
// default height's directory, so an old client and a new one asking for 1080p
// share one transcode.
//
// It does not check the height against what the video offers, because for a
// source below 1080p there is nothing else it could serve — the scaler clamps
// to the source and the client gets the same pixels either way.
func (s *Server) mediaHLSDefault(w http.ResponseWriter, r *http.Request) {
	s.serveHLSVariant(w, r, media.HLSDefaultHeight, false)
}

// serveHLSVariant is the body both routes share, once the height is known.
func (s *Server) serveHLSVariant(w http.ResponseWriter, r *http.Request, height int, enforceOffered bool) {
	id, file := chi.URLParam(r, "id"), chi.URLParam(r, "file")
	if s.mediaCache == nil || !validMediaID.MatchString(id) || !validHLSFile.MatchString(file) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if file == media.HLSPlaylistName {
		s.serveHLSPlaylist(w, r, id, height, enforceOffered)
		return
	}
	s.serveHLSFile(w, r, id, height, file)
}

// serveHLSPlaylist starts the transcode if it is not already running and
// serves the playlist once it names a segment.
func (s *Server) serveHLSPlaylist(w http.ResponseWriter, r *http.Request, id string, height int, enforceOffered bool) {
	if _, err := s.startHLS(r.Context(), id, height, enforceOffered); err != nil {
		if errors.Is(err, errHLSHeightNotOffered) {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		s.writeTAError(w, "get video", err)
		return
	}
	name := media.HLSName(id, height)
	dir, err := s.mediaCache.WaitDir(r.Context(), name, media.HLSPlaylistReady, hlsPlaylistWait)
	if err != nil {
		switch {
		case errors.Is(err, media.ErrNotReady):
			// The job is still running — this is not a failure, just a slower
			// video than the wait allows for.
			w.Header().Set("Retry-After", hlsRetryAfter)
			w.Header().Set("Cache-Control", "no-store")
			writeError(w, http.StatusServiceUnavailable, "rendition not ready yet")
		case errors.Is(err, context.Canceled):
			// Client hung up; the transcode carries on regardless.
		default:
			s.log.Error("hls playlist", "video", id, "err", err)
			writeError(w, http.StatusBadGateway, "could not prepare video")
		}
		return
	}
	// A finished playlist never changes; a growing one must not be cached at
	// all, or the player keeps replaying the segments it first saw.
	cacheControl := "no-store"
	if s.mediaCache.DirState(name) == media.StateDone {
		cacheControl = "private, max-age=3600"
	}
	w.Header().Set("Cache-Control", cacheControl)
	serveHLSContent(w, r, filepath.Join(dir, media.HLSPlaylistName), media.HLSPlaylistType)
}

// serveHLSFile serves an init or media segment straight from the cache entry.
// A segment the transcode has not reached yet is a 404: the player asks again
// after re-reading the playlist, which is what an event playlist is for.
func (s *Server) serveHLSFile(w http.ResponseWriter, r *http.Request, id string, height int, file string) {
	name := media.HLSName(id, height)
	contentType := media.HLSSegmentType
	if file == media.HLSInitName {
		contentType = media.HLSInitType
	}
	// Watching a rendition counts as using it: without this a long video could
	// be evicted out from under the player it is streaming to.
	s.mediaCache.TouchDir(name)
	w.Header().Set("Cache-Control", "private, max-age=86400")
	serveHLSContent(w, r, filepath.Join(s.mediaCache.Dir(name), file), contentType)
}

// serveHLSContent hands a cache file to http.ServeContent, which handles
// Range, If-Range and 206 the same way the audio variants get them.
func serveHLSContent(w http.ResponseWriter, r *http.Request, path, contentType string) {
	f, err := os.Open(path) //nolint:gosec // cache dir + a validated id + a name from validHLSFile
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	w.Header().Set("Content-Type", contentType)
	http.ServeContent(w, r, filepath.Base(path), st.ModTime(), f)
}

// startHLS resolves the video and makes sure its rendition at height is being
// derived, without waiting for it. It is the shared body of the playlist route
// and the prefetch endpoint.
//
// enforceOffered rejects a height the source cannot fill; the legacy alias
// route passes false, because it has no other height to fall back to.
func (s *Server) startHLS(ctx context.Context, id string, height int, enforceOffered bool) (media.JobState, error) {
	v, err := s.ta.GetVideo(ctx, id)
	if err != nil {
		return media.StatePending, err
	}
	if v.MediaURL == "" {
		return media.StatePending, ta.ErrNotFound
	}
	if enforceOffered && !slices.Contains(media.HLSOfferedHeights(v.Height()), height) {
		return media.StatePending, errHLSHeightNotOffered
	}
	return s.startHLSFor(v, height), nil
}

// startHLSFor is startHLS once the video document is in hand, so the prefetch
// endpoint can read the offered heights off it without fetching it twice.
func (s *Server) startHLSFor(v *ta.Video, height int) media.JobState {
	src := taMediaPath(v.MediaURL)
	return s.mediaCache.StartDir(media.HLSName(v.YoutubeID, height),
		media.HLS(s.ffmpegPath, hlsSource(v), height, s.hwaccel, s.log,
			func(ctx context.Context) (io.ReadCloser, error) { return s.ta.OpenMedia(ctx, src) }))
}

// hlsSource reads the copy-vs-encode inputs off the TA document rather than
// probing the file: TA already parsed the streams at download time.
func hlsSource(v *ta.Video) media.HLSSource {
	out := media.HLSSource{AudioCodec: sourceAudioCodec(v), Height: v.Height()}
	for _, st := range v.Streams {
		if st.Type == "video" {
			out.VideoCodec = st.Codec
			break
		}
	}
	return out
}

// hlsState is what the video detail reports for one height. Without a media
// cache nothing can ever be derived, so every video is reported as never
// requested.
func (s *Server) hlsState(id string, height int) media.JobState {
	if s.mediaCache == nil {
		return media.StatePending
	}
	return s.mediaCache.DirState(media.HLSName(id, height))
}

// hlsVariants is the ladder the video detail advertises: every height this
// source can fill, tallest first, each with its own URL, codec and state. A
// client picks one — nothing here starts a transcode.
func (s *Server) hlsVariants(id string, sourceHeight int) []HLSVariantInfo {
	heights := media.HLSOfferedHeights(sourceHeight)
	out := make([]HLSVariantInfo, 0, len(heights))
	for _, h := range heights {
		out = append(out, HLSVariantInfo{
			Height: h,
			URL:    hlsURL(id, h),
			State:  string(s.hlsState(id, h)),
			Codec:  media.HLSCodecForHeight(h),
		})
	}
	return out
}

// postVideoHLS starts a rendition and returns immediately, so a client can
// warm one up (the next video in a playlist, say) instead of making the viewer
// wait at the moment they press play.
//
// `?height=` picks which; without it the video's default is started, the same
// one `hls_url` points at. A height the video does not offer is a 400 rather
// than a silent substitution: the client is working from a stale `hls_variants`
// and should re-read the detail.
func (s *Server) postVideoHLS(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !validMediaID.MatchString(id) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if s.mediaCache == nil {
		writeError(w, http.StatusServiceUnavailable, "derived media is not configured")
		return
	}
	v, err := s.ta.GetVideo(r.Context(), id)
	if err != nil {
		s.writeTAError(w, "get video", err)
		return
	}
	if v.MediaURL == "" {
		s.writeTAError(w, "get video", ta.ErrNotFound)
		return
	}
	height := media.HLSDefaultOffered(v.Height())
	if raw := strings.TrimSpace(r.URL.Query().Get("height")); raw != "" {
		h, err := strconv.Atoi(raw)
		if err != nil || !slices.Contains(media.HLSOfferedHeights(v.Height()), h) {
			writeError(w, http.StatusBadRequest, "height must be one of the video's hls_variants")
			return
		}
		height = h
	}
	writeJSON(w, http.StatusOK, map[string]string{"state": string(s.startHLSFor(v, height))})
}
