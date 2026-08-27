package api

import (
	"bytes"
	"context"
	"errors"
	"math"
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
// The playlist is complete from the very first request — the segment grid is
// fixed, so it is written from the video's duration before anything is encoded
// — which means a player can seek anywhere in the rendition immediately,
// including to the position a viewer is resuming from. What waits is the
// *segment* request: a segment that has not been encoded yet blocks until it
// lands rather than 404ing, and a request far ahead of the encoder re-aims it.
//
// Clients pass `from=` (the resume position) so the transcode starts there in
// the first place, which is the difference between watching immediately and
// waiting for the encoder to grind up to 40:00.

// hlsRetryAfter is what a client is told to wait before asking for the playlist
// again.
const hlsRetryAfter = "5"

// hlsSegmentRetryAfter is the same for a segment. It is shorter because a
// segment request that ran out of patience is waiting on one four-second
// encode, not on a whole job starting.
const hlsSegmentRetryAfter = "2"

// hlsPlaylistWait bounds the block on the playlist appearing. It is written
// before the job queues for a transcode slot, so this is a guard against a
// wedged filesystem rather than a budget for encoding. A variable so tests need
// not wait out the real one.
var hlsPlaylistWait = 45 * time.Second

// defaultSegmentWait is MEDIA_SEGMENT_WAIT's default: how long a segment
// request blocks for a segment the encoder has not reached. A minute covers a
// re-aimed run reaching a fresh seek point on a slow box; past that a client is
// better off being told to come back than sitting on a dead connection.
const defaultSegmentWait = 60 * time.Second

// hlsFilePoll is how often a waiting segment request re-checks the directory.
// ffmpeg gives no signal when it publishes one, and a stat is nothing.
const hlsFilePoll = 100 * time.Millisecond

// validHLSFile is the complete set of names a client may ask for. The rendition
// on disk holds index.m3u8, init.mp4 and the segments; master.m3u8 is not a file
// but the multivariant playlist rendered on the fly (see serveHLSMaster).
// Anything else — a traversal, a stray temp file, the completion marker — is a
// 404 before it reaches the filesystem.
var validHLSFile = regexp.MustCompile(`^(master\.m3u8|index\.m3u8|init\.mp4|seg[0-9]{5}\.m4s)$`)

// errHLSHeightNotOffered is what starting a rendition the video cannot fill
// returns: a 4K rendition of a 1080p source is a transcode nobody asked for.
var errHLSHeightNotOffered = errors.New("hls: height not offered for this video")

// hlsURL is the playlist a client loads to play the rendition at height: the
// multivariant (master) playlist, so hls.js learns the codecs up front and
// schedules the fMP4 fragments. It references the media playlist (index.m3u8)
// at the same path, which stays reachable for the byte-range and native paths.
func hlsURL(id string, height int) string {
	return "/media/hls/" + id + "/" + strconv.Itoa(height) + "/" + media.HLSMasterName
}

// hlsCodecsWait bounds how long a master request waits for the init segment to
// land so it can name the real codecs, before falling back to the height's
// default. It only bites on a rendition whose transcode has not written init.mp4
// yet; a finished or already-parsed one answers at once. A variable so tests
// need not wait it out.
var hlsCodecsWait = 3 * time.Second

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
	switch file {
	case media.HLSMasterName:
		s.serveHLSMaster(w, r, id, height, enforceOffered)
	case media.HLSPlaylistName:
		s.serveHLSPlaylist(w, r, id, height, enforceOffered)
	default:
		s.serveHLSFile(w, r, id, height, file)
	}
}

// serveHLSPlaylist starts (or re-aims) the transcode and serves the playlist.
//
// `?from=` is the resume position, for a client that cannot POST first: the
// transcode starts at that point rather than at 0:00. The playlist itself is
// the same either way — it describes the whole video from the first request.
func (s *Server) serveHLSPlaylist(w http.ResponseWriter, r *http.Request, id string, height int, enforceOffered bool) {
	from := fromSeconds(r)
	if _, err := s.startHLS(r.Context(), id, height, enforceOffered, from); err != nil {
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
	// A finished rendition's playlist never changes. A running one's is
	// rewritten once at the end with the real segment durations, so it must not
	// be cached in the meantime. A `from`-specific playlist carries an
	// EXT-X-START computed from the query, so it is per-`from` and must never be
	// cached — least of all as the canonical (no-`from`) playlist.
	cacheControl := "no-store"
	if from == 0 && s.mediaCache.DirState(name) == media.StateDone {
		cacheControl = "private, max-age=3600"
	}
	w.Header().Set("Cache-Control", cacheControl)

	path := filepath.Join(dir, media.HLSPlaylistName)
	if from == 0 {
		serveHLSContent(w, r, path, media.HLSPlaylistType)
		return
	}
	// Resume: add EXT-X-START so the player begins at `from` and fetches that
	// segment first (the resume-first transcode produces it first), instead of
	// blocking on segment 0, which it produces last. The segment list is
	// unchanged; this is a pure header addition, so the body stays a complete
	// VOD list the player may still seek anywhere within.
	body, err := os.ReadFile(path) //nolint:gosec // cache dir + a validated id + a fixed name
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	body = media.InsertHLSStart(body, from)
	w.Header().Set("Content-Type", media.HLSPlaylistType)
	http.ServeContent(w, r, media.HLSPlaylistName, time.Time{}, bytes.NewReader(body))
}

// serveHLSMaster starts (or re-aims) the transcode and serves the multivariant
// playlist. It exists so hls.js schedules the fMP4 fragments at all: a media
// playlist with no CODECS attribute leaves it unable to create the MSE
// SourceBuffer, so it parses the playlist and then never requests a fragment. A
// one-entry master naming the codecs fixes that, and native players take it too.
//
// The CODECS come from the init segment the job actually produced, so they are
// truthful even for a copied source; before the first init lands, the height's
// default is used — correct for the fixed encoder settings — and the real value
// is cached once the init exists.
func (s *Server) serveHLSMaster(w http.ResponseWriter, r *http.Request, id string, height int, enforceOffered bool) {
	from := fromSeconds(r)
	if _, err := s.startHLS(r.Context(), id, height, enforceOffered, from); err != nil {
		if errors.Is(err, errHLSHeightNotOffered) {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		s.writeTAError(w, "get video", err)
		return
	}
	dir := s.mediaCache.Dir(media.HLSName(id, height))
	info, exact := s.hlsCodecs(r.Context(), dir, height)
	// When resuming, the variant URI carries `?from=` through to the media
	// playlist, which then serves an EXT-X-START at that point.
	master := media.BuildHLSMaster(info.Codecs, media.HLSBandwidth(height), info.Width, info.Height, from)

	// A master built from parsed codecs never changes; one built from the
	// default is provisional until the init lands, so it must not be cached. A
	// `from`-specific master points at a per-`from` media playlist, so it too is
	// never cached — never as the canonical (no-`from`) master.
	cacheControl := "no-store"
	if exact && from == 0 {
		cacheControl = "private, max-age=3600"
	}
	w.Header().Set("Cache-Control", cacheControl)
	w.Header().Set("Content-Type", media.HLSPlaylistType)
	http.ServeContent(w, r, media.HLSMasterName, time.Time{}, bytes.NewReader(master))
}

// hlsCodecs resolves the rendition's CODECS, waiting briefly for the init
// segment so the master can name the real streams rather than the default. The
// wait only matters for a fresh transcode that has not written init.mp4 yet; a
// finished rendition, or one whose codecs are already cached, returns at once.
func (s *Server) hlsCodecs(ctx context.Context, dir string, height int) (media.HLSCodecsInfo, bool) {
	if info, exact := media.EnsureHLSCodecs(dir, height); exact {
		return info, true
	}
	// Not cached and no parseable init yet: wait briefly for the first run to
	// write init.mp4 so the codecs are the real ones, then try once more.
	initPath := filepath.Join(dir, media.HLSInitName)
	deadline := time.NewTimer(hlsCodecsWait)
	defer deadline.Stop()
	poll := time.NewTicker(hlsFilePoll)
	defer poll.Stop()
	for !hlsFileReady(initPath) {
		select {
		case <-poll.C:
		case <-deadline.C:
			return media.EnsureHLSCodecs(dir, height)
		case <-ctx.Done():
			return media.EnsureHLSCodecs(dir, height)
		}
	}
	return media.EnsureHLSCodecs(dir, height)
}

// serveHLSFile serves an init or media segment out of the cache entry, waiting
// for it when the job has not produced it yet.
//
// Waiting rather than 404ing is what makes the complete playlist honest: the
// player is allowed to ask for any segment in it, so a segment that has not
// been encoded yet is a slow segment and not a missing one. A request far ahead
// of the encoder also re-aims the run, so a seek does not wait out everything
// in between.
func (s *Server) serveHLSFile(w http.ResponseWriter, r *http.Request, id string, height int, file string) {
	name := media.HLSName(id, height)
	contentType := media.HLSSegmentType
	if file == media.HLSInitName {
		contentType = media.HLSInitType
	}
	// Watching a rendition counts as using it: without this a long video could
	// be evicted out from under the player it is streaming to.
	s.mediaCache.TouchDir(name)
	path := filepath.Join(s.mediaCache.Dir(name), file)

	if !hlsFileReady(path) && !s.waitForHLSFile(w, r, name, file, path) {
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=86400")
	serveHLSContent(w, r, path, contentType)
}

// waitForHLSFile blocks until the file lands, and reports whether the caller
// should go on and serve it. When it returns false it has already answered the
// request.
func (s *Server) waitForHLSFile(w http.ResponseWriter, r *http.Request, name, file, path string) bool {
	if i := media.HLSSegmentIndex(file); i >= 0 {
		job := s.hlsJobs.Get(name)
		if n := job.Segments(); n > 0 && i >= n {
			// Past the end of the rendition: no run will ever write it.
			writeError(w, http.StatusNotFound, "not found")
			return false
		}
		// Tell the job what is being waited for. Far enough ahead of the
		// encoder and it restarts the run there.
		job.Request(i)
	}

	deadline := time.NewTimer(s.segmentWait)
	defer deadline.Stop()
	poll := time.NewTicker(hlsFilePoll)
	defer poll.Stop()
	for {
		if hlsFileReady(path) {
			return true
		}
		switch s.mediaCache.DirState(name) {
		case media.StateFailed:
			writeError(w, http.StatusBadGateway, "could not prepare video")
			return false
		case media.StateRunning:
			// Keep waiting.
		default:
			// Nothing is being derived and the file is not there: for a done
			// entry it never will be, and for a pending one the playlist
			// request is what starts the job.
			writeError(w, http.StatusNotFound, "not found")
			return false
		}
		select {
		case <-poll.C:
		case <-r.Context().Done():
			// Client hung up; the transcode carries on.
			return false
		case <-deadline.C:
			w.Header().Set("Retry-After", hlsSegmentRetryAfter)
			w.Header().Set("Cache-Control", "no-store")
			writeError(w, http.StatusServiceUnavailable, "segment not ready yet")
			return false
		}
	}
}

// hlsFileReady reports whether a segment or init file is on disk and complete.
// The muxer publishes both by rename, so any non-empty file with the right name
// is whole.
func hlsFileReady(path string) bool {
	st, err := os.Stat(path) //nolint:gosec // cache dir + a validated id + a name from validHLSFile
	return err == nil && !st.IsDir() && st.Size() > 0
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

// fromSeconds reads the `from=` resume position off a request. Anything that is
// not a non-negative number is 0, which is "start at the beginning" — the
// behaviour before `from` existed.
func fromSeconds(r *http.Request) float64 {
	raw := strings.TrimSpace(r.URL.Query().Get("from"))
	if raw == "" {
		return 0
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil || v <= 0 || math.IsInf(v, 0) {
		return 0
	}
	return v
}

// startHLS resolves the video and makes sure its rendition at height is being
// derived from the right place, without waiting for it. It is the shared body
// of the playlist route and the prefetch endpoint.
//
// enforceOffered rejects a height the source cannot fill; the legacy alias
// route passes false, because it has no other height to fall back to.
func (s *Server) startHLS(ctx context.Context, id string, height int, enforceOffered bool, from float64) (media.JobState, error) {
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
	return s.startHLSFor(v, height, from), nil
}

// startHLSFor is startHLS once the video document is in hand, so the prefetch
// endpoint can read the offered heights off it without fetching it twice.
//
// A job that is already running is *steered* rather than started again: the
// resume position becomes the segment it encodes next, so a viewer who opens
// the same video at a different point does not queue behind a transcode
// heading somewhere else.
func (s *Server) startHLSFor(v *ta.Video, height int, from float64) media.JobState {
	name := media.HLSName(v.YoutubeID, height)
	s.hlsJobs.Get(name).RequestSeconds(from)
	prepare, derive := media.HLS(s.hlsConfig(v, height, from))
	return s.mediaCache.StartDirJob(name, prepare, derive)
}

// hlsConfig is everything one rendition's job needs, including the reader that
// gives ffmpeg a seekable source without ever handing it the TA token.
func (s *Server) hlsConfig(v *ta.Video, height int, from float64) media.HLSConfig {
	src := taMediaPath(v.MediaURL)
	return media.HLSConfig{
		FFmpegPath:        s.ffmpegPath,
		Source:            hlsSource(v),
		Height:            height,
		HW:                s.hwaccel,
		Log:               s.log,
		Registry:          s.hlsJobs,
		From:              from,
		SeekAheadSegments: s.seekAheadSegments,
		Open: func(ctx context.Context, rangeHeader string) (*media.SourceStream, error) {
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
		},
	}
}

// hlsSource reads the copy-vs-encode inputs and the duration off the TA
// document rather than probing the file: TA already parsed the streams at
// download time, and the duration is what the whole segment grid comes from.
func hlsSource(v *ta.Video) media.HLSSource {
	out := media.HLSSource{AudioCodec: sourceAudioCodec(v), Height: v.Height(), Duration: v.Player.Duration}
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

// hlsProgress is how much of a rendition exists, 0..1. A finished one is 1 even
// after the job is long gone; one nothing has asked for is 0.
func (s *Server) hlsProgress(id string, height int) float64 {
	if s.mediaCache == nil {
		return 0
	}
	name := media.HLSName(id, height)
	if s.mediaCache.DirState(name) == media.StateDone {
		return 1
	}
	return s.hlsJobs.Get(name).Progress()
}

// hlsVariants is the ladder the video detail advertises: every height this
// source can fill, tallest first, each with its own URL, codec, state and
// progress. A client picks one — nothing here starts a transcode.
func (s *Server) hlsVariants(id string, sourceHeight int) []HLSVariantInfo {
	heights := media.HLSOfferedHeights(sourceHeight)
	out := make([]HLSVariantInfo, 0, len(heights))
	for _, h := range heights {
		out = append(out, HLSVariantInfo{
			Height:   h,
			URL:      hlsURL(id, h),
			State:    string(s.hlsState(id, h)),
			Codec:    media.HLSCodecForHeight(h),
			Progress: s.hlsProgress(id, h),
		})
	}
	return out
}

// postVideoHLS starts a rendition and returns immediately, so a client can warm
// one up (at the position it is about to play) instead of making the viewer
// wait at the moment they press play.
//
// `?height=` picks which; without it the video's default is started, the same
// one `hls_url` points at. A height the video does not offer is a 400 rather
// than a silent substitution: the client is working from a stale `hls_variants`
// and should re-read the detail.
//
// `?from=` is the resume position in seconds. It is what makes resuming
// instant: the transcode starts at that point instead of at 0:00, and a job
// that is already running is re-aimed at it.
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
	state := s.startHLSFor(v, height, fromSeconds(r))
	writeJSON(w, http.StatusOK, HLSStartResponse{
		State:    string(state),
		Height:   height,
		Progress: s.hlsProgress(id, height),
	})
}
