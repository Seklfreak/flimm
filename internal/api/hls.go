package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Seklfreak/flimm/internal/media"
	"github.com/Seklfreak/flimm/internal/ta"
)

// The compatible video rendition: H.264/AAC as HLS with fMP4 segments, derived
// on first request. See docs/api.md "Compatible video rendition (HLS)".
//
// The playlist request is the one that waits. It starts the transcode, blocks
// until the first segments exist and then serves the playlist as it stands —
// so a viewer starts watching after a few seconds rather than after the whole
// video has been transcoded. Segment requests never wait and never touch
// TubeArchivist: by the time one arrives the job is running and the file is
// either there or not yet.
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

// hlsURL is the playlist a client loads to play the rendition.
func hlsURL(id string) string { return "/media/hls/" + id + "/" + media.HLSPlaylistName }

// mediaHLS serves one file of a video's HLS rendition. Both the playlist and
// the segments go through the same route because AVPlayer re-sends the media
// credentials on every segment request, and they must all be gated the same
// way.
func (s *Server) mediaHLS(w http.ResponseWriter, r *http.Request) {
	id, file := chi.URLParam(r, "id"), chi.URLParam(r, "file")
	if s.mediaCache == nil || !validMediaID.MatchString(id) || !validHLSFile.MatchString(file) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if file == media.HLSPlaylistName {
		s.serveHLSPlaylist(w, r, id)
		return
	}
	s.serveHLSFile(w, r, id, file)
}

// serveHLSPlaylist starts the transcode if it is not already running and
// serves the playlist once it names a segment.
func (s *Server) serveHLSPlaylist(w http.ResponseWriter, r *http.Request, id string) {
	if _, err := s.startHLS(r.Context(), id); err != nil {
		s.writeTAError(w, "get video", err)
		return
	}
	name := media.HLSName(id)
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
func (s *Server) serveHLSFile(w http.ResponseWriter, r *http.Request, id, file string) {
	name := media.HLSName(id)
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

// startHLS resolves the video and makes sure its rendition is being derived,
// without waiting for it. It is the shared body of the playlist route and the
// prefetch endpoint.
func (s *Server) startHLS(ctx context.Context, id string) (media.JobState, error) {
	v, err := s.ta.GetVideo(ctx, id)
	if err != nil {
		return media.StatePending, err
	}
	if v.MediaURL == "" {
		return media.StatePending, ta.ErrNotFound
	}
	src := taMediaPath(v.MediaURL)
	return s.mediaCache.StartDir(media.HLSName(id), media.HLS(s.ffmpegPath, hlsSource(v), s.log,
		func(ctx context.Context) (io.ReadCloser, error) { return s.ta.OpenMedia(ctx, src) })), nil
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

// hlsState is what the video detail reports. Without a media cache nothing can
// ever be derived, so every video is reported as never requested.
func (s *Server) hlsState(id string) media.JobState {
	if s.mediaCache == nil {
		return media.StatePending
	}
	return s.mediaCache.DirState(media.HLSName(id))
}

// postVideoHLS starts the rendition and returns immediately, so a client can
// warm one up (the next video in a playlist, say) instead of making the viewer
// wait at the moment they press play.
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
	state, err := s.startHLS(r.Context(), id)
	if err != nil {
		s.writeTAError(w, "get video", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"state": string(state)})
}
