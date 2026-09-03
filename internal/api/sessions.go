package api

// What the server is doing right now.
//
// Every other view Flimm has is per-viewer and after the fact: history says
// what was watched, stats say how much of it. Neither answers the question an
// admin actually asks, which is about the present tense — *is anything playing,
// is the box transcoding for it, and is anyone watching a spinner?* A server
// that is quietly re-encoding a 4K film for a television nobody is sitting in
// front of looks, from every existing screen, exactly like a server doing
// nothing.
//
// So this is the one place that keeps a live picture, and it is assembled from
// what clients already send rather than from anything new they have to say:
//
//   - the progress heartbeat (`POST /videos/{id}/progress`) — every client, on
//     a ten-second beat, and the only thing that reaches the server while a
//     video is simply playing. It carries who, what and where in it.
//   - the `/media/*` request itself — which gives the half a client cannot
//     report: whether the archived file is being served directly or a
//     rendition is, and how many bytes actually left the machine.
//   - a stall report — the viewer watched a spinner, and which session it
//     happened in.
//   - a published remote session (tvOS) — the player's own readings, relayed
//     whole.
//
// Nothing here is asked of a client that was not already sent, which is why
// this shipped without touching the web, iPhone, iPad or Apple TV code: the
// observation is entirely the server's, and a client that never learns it
// exists cannot drift from it.
//
// It lives in memory, like remote sessions and for the same reason: it
// describes what is happening *now*, and a row that survived a restart would
// only be a lie about a television that has since been switched off. It
// assumes a single-process server, as the transcode slot and the prepare job
// already do.

import (
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/Seklfreak/flimm/internal/media"
)

const (
	// liveSessionTTL is how long a session stays listed without a sign of
	// life. Every client heartbeats every ten seconds while playing, so this
	// is six missed beats.
	//
	// A *paused* player stops heartbeating, so it drops off within a minute of
	// being paused. That is deliberate: this list answers "what is the server
	// doing", and a paused video is not something it is doing. A media request
	// that is still open keeps its session listed however long it runs — a
	// direct play is one request that can last the whole film.
	liveSessionTTL = 60 * time.Second
	// maxLiveSessions bounds what the tracker can grow to. Far above any real
	// deployment of a self-hosted archive; it exists so a client inventing
	// video ids cannot turn this into a memory leak. Over the ceiling the
	// least recently heard from makes way.
	maxLiveSessions = 200
)

// The delivery paths a session can be on. They are the same three words the
// playback stats panel uses, so a reading here and a reading on the player
// mean the same thing.
const (
	// liveDirect — the archived file is being served as it is.
	liveDirect = "direct"
	// liveRendition — a compatible rendition is being derived and served.
	liveRendition = "rendition"
	// liveAudio — the audio track only.
	liveAudio = "audio"
)

// liveDelivery is what a media request says about how a video is reaching a
// screen. `Name` is a rendition's cache entry, which is how the running
// transcode is found again when the list is read.
type liveDelivery struct {
	Kind   string
	Height int
	Name   string
}

// liveSession is one person watching one video, as the server can see it.
//
// The key is the viewer and the video, not a session id, because there is no
// session id to be had: a heartbeat and a segment request are two unrelated
// requests, and the only thing that ties them together is who sent them and
// what they are about. The consequence to know is that one account playing the
// same video on two screens at once is one row here rather than two — rare,
// and worth less than the alternative, which is every playback appearing twice
// because its heartbeats and its segments could not be recognised as the same
// thing.
type liveSession struct {
	userID  uuid.UUID
	videoID string
	// user is the display the admin sees: an email, a name, or neither. Taken
	// from the request context, so it costs no database read — and a media
	// request authenticated by the media cookie carries only an id, which is
	// why a session can be listed before its label is known.
	user      string
	title     string
	channel   string
	client    string
	device    string
	position  float64
	duration  float64
	paused    bool
	startedAt time.Time
	updatedAt time.Time
	delivery  liveDelivery
	// bytes is what has actually reached the client. It is written by the
	// serving goroutines and read by whoever lists the sessions, so it is
	// atomic rather than under the hub's lock: a listing must never wait on a
	// stream, and a stream must never wait on a listing.
	bytes atomic.Int64
	// streams is how many media requests are open for this session. Above
	// zero the session cannot lapse, which is what keeps a direct play — one
	// request that may run for an hour — from disappearing mid-film.
	streams   int
	stalls    int
	lastStall string
	stats     *RemotePlaybackStats
}

// liveHub holds every session. One mutex covers the map and the fields on the
// sessions in it; only `bytes` is written outside it.
type liveHub struct {
	mu       sync.Mutex
	sessions map[string]*liveSession
	// now is time.Now except in tests.
	now func() time.Time
}

func newLiveHub() *liveHub {
	return &liveHub{sessions: map[string]*liveSession{}, now: time.Now}
}

func liveKey(uid uuid.UUID, videoID string) string { return uid.String() + "\x00" + videoID }

// touch finds or starts the session for this request's viewer and video, marks
// it alive, and hands it to `apply` under the lock.
//
// It returns nil when the request carries no user, which cannot happen on a
// route behind either auth middleware but keeps this from inventing a session
// for uuid.Nil if one ever ends up in front of one.
func (h *liveHub) touch(r *http.Request, videoID string, apply func(*liveSession)) *liveSession {
	uid := currentUserID(r.Context())
	if uid == uuid.Nil || videoID == "" {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.pruneLocked()

	key := liveKey(uid, videoID)
	sess, ok := h.sessions[key]
	if !ok {
		if len(h.sessions) >= maxLiveSessions {
			h.evictOldestLocked()
		}
		sess = &liveSession{userID: uid, videoID: videoID, startedAt: h.now()}
		h.sessions[key] = sess
	}
	sess.updatedAt = h.now()
	// The label is filled in from whichever request happens to carry it: the
	// API routes always do, a media request authenticated by the cookie never
	// does. Never cleared, so a cookie-authenticated segment cannot blank a
	// name a heartbeat established.
	if label := userLabel(r); label != "" {
		sess.user = label
	}
	sess.client = betterClient(sess.client, clientFromUserAgent(r.UserAgent()))
	apply(sess)
	return sess
}

// pruneLocked drops sessions nothing has been heard from, leaving alone any
// with a media request still open. Caller holds the lock.
func (h *liveHub) pruneLocked() {
	cutoff := h.now().Add(-liveSessionTTL)
	for key, sess := range h.sessions {
		if sess.streams == 0 && sess.updatedAt.Before(cutoff) {
			delete(h.sessions, key)
		}
	}
}

// evictOldestLocked removes the least recently active session. Caller holds
// the lock.
func (h *liveHub) evictOldestLocked() {
	var oldestKey string
	var oldest time.Time
	for key, sess := range h.sessions {
		if sess.streams > 0 {
			continue
		}
		if oldestKey == "" || sess.updatedAt.Before(oldest) {
			oldestKey, oldest = key, sess.updatedAt
		}
	}
	if oldestKey != "" {
		delete(h.sessions, oldestKey)
	}
}

// ---- what the server hears ----

// watching records a progress heartbeat: the one thing that reaches the server
// while a video is simply playing.
func (h *liveHub) watching(r *http.Request, videoID, title, channel string, position, duration float64) {
	h.touch(r, videoID, func(sess *liveSession) {
		sess.title = title
		sess.channel = channel
		sess.position = position
		if duration > 0 {
			sess.duration = duration
		}
		// A heartbeat is only sent while playing, so hearing one is the end of
		// whatever a published session last said about being paused.
		sess.paused = false
	})
}

// stalled records that a viewer watched a spinner, and the server's own word
// for why. The count is what makes a session worth looking at twice.
func (h *liveHub) stalled(r *http.Request, videoID, reason, client string) {
	h.touch(r, videoID, func(sess *liveSession) {
		sess.stalls++
		sess.lastStall = reason
		sess.client = betterClient(sess.client, client)
	})
}

// published attaches a player's own readings to the session, for the clients
// that publish them. Nothing here is derived or checked — see RemoteSession.
func (h *liveHub) published(r *http.Request, s RemoteSession) {
	h.touch(r, s.VideoID, func(sess *liveSession) {
		sess.title = s.Title
		sess.channel = s.ChannelName
		sess.position = s.Position
		sess.duration = s.Duration
		sess.paused = s.Paused
		sess.device = s.Device
		sess.client = betterClient(sess.client, s.Platform)
		sess.stats = s.Stats
	})
}

// streaming marks a media request as open for the length of it and counts what
// leaves the machine.
//
// It returns the writer the handler must serve through and the function that
// closes the stream out; both are inert when there is no session to attribute
// the request to, so a caller can use them unconditionally.
func (h *liveHub) streaming(
	w http.ResponseWriter, r *http.Request, videoID string, d liveDelivery,
) (http.ResponseWriter, func()) {
	sess := h.touch(r, videoID, func(sess *liveSession) {
		// A rendition request settles the delivery path: it is the one thing a
		// client cannot be wrong about, because it *is* the request. Audio
		// never overwrites video — a client that fetched the audio track once
		// is not an audio-only session.
		if d.Kind != liveAudio || sess.delivery.Kind == "" {
			sess.delivery = d
		}
		sess.streams++
	})
	if sess == nil {
		return w, func() {}
	}
	counted := &countingWriter{ResponseWriter: w, n: &sess.bytes}
	return counted, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		sess.streams--
		sess.updatedAt = h.now()
	}
}

// ---- what an admin reads ----

// LiveSession is one playback happening now, as `GET /admin/sessions` reports
// it. Everything in it is either what the server observed or what a player
// published about itself; see `stats`, which is relayed untouched.
type LiveSession struct {
	UserID string `json:"user_id"`
	// User is an email or a display name — whichever the account has.
	User        string `json:"user,omitempty"`
	VideoID     string `json:"video_id"`
	Title       string `json:"title,omitempty"`
	ChannelName string `json:"channel_name,omitempty"`
	// Client is the platform, derived from the User-Agent unless a player
	// named itself: "web", "ios", "ipados", "tvos", or "apple" when a request
	// is plainly from one of the native clients but does not say which.
	Client string `json:"client,omitempty"`
	// Device is the screen's own name, and exists only for a player that
	// publishes a remote session.
	Device   string  `json:"device,omitempty"`
	Position float64 `json:"position"`
	Duration float64 `json:"duration"`
	Paused   bool    `json:"paused"`
	// StartedAt is when this session was first seen, which is not when
	// playback started: a server restarted mid-video sees it from then on.
	StartedAt time.Time `json:"started_at"`
	UpdatedAt time.Time `json:"updated_at"`
	// Streaming is whether a media request is open right now. A session that
	// is not streaming is not necessarily stalled — a browser that has
	// buffered the whole file plays for minutes without asking for anything.
	Streaming bool `json:"streaming"`
	// Bytes is what has left the machine for this session since it was first
	// seen, across every media request in it.
	Bytes     int64        `json:"bytes"`
	Stalls    int          `json:"stalls"`
	LastStall string       `json:"last_stall,omitempty"`
	Delivery  LiveDelivery `json:"delivery"`
	// Stats is the player's own readings, when it publishes them. Only the
	// Apple TV does today.
	Stats *RemotePlaybackStats `json:"stats,omitempty"`
}

// LiveDelivery is how the video is reaching the screen, as the server knows it
// from the requests it served.
type LiveDelivery struct {
	// Kind is "direct", "rendition", "audio", or "" when nothing has been
	// requested yet — a heartbeat can arrive before the first media request,
	// and a fully buffered video sends heartbeats and nothing else.
	Kind   string `json:"kind,omitempty"`
	Height int    `json:"height,omitempty"`
	// Job is the transcode behind a rendition, read when this list is built,
	// and nil once the rendition is finished — there is nothing running to
	// report, which is itself the answer to "why is this stalling".
	Job *LiveJob `json:"job,omitempty"`
}

// LiveJob is a rendition being derived: the server-side half of a stall.
type LiveJob struct {
	VideoID string `json:"video_id"`
	Height  int    `json:"height"`
	// Segments is how many the finished rendition has, Progress the fraction
	// that exists (not where anyone is watching).
	Segments int     `json:"segments"`
	Progress float64 `json:"progress"`
	// EncoderSegment is the segment ffmpeg is on, or -1 when the job exists
	// but nothing is encoding — it is waiting for the transcode slot.
	EncoderSegment int `json:"encoder_segment"`
}

// LiveResponse is the whole answer: who is watching, what is being derived for
// them, and what has recently gone wrong.
type LiveResponse struct {
	Sessions []LiveSession `json:"sessions"`
	// Jobs is every running transcode, including the ones no session is
	// attached to — a viewer who closed the tab leaves one behind, and that is
	// exactly the thing this view exists to make visible.
	Jobs []LiveJob `json:"jobs"`
	// Stalls is the same recent list `/healthz` shows an admin.
	Stalls []Stall `json:"stalls"`
	// Now is the server's clock, so a reader can age these without depending
	// on its own being right.
	Now time.Time `json:"now"`
}

// snapshot builds the admin's answer. The job states are read here rather than
// recorded as they change, so what is reported is what is true at the moment
// of the read.
func (h *liveHub) snapshot(jobs *media.HLSRegistry, stalls *stallLog) LiveResponse {
	running := map[string]LiveJob{}
	for _, st := range jobs.List() {
		id, height, ok := media.ParseHLSName(st.Name)
		if !ok {
			continue
		}
		running[st.Name] = LiveJob{
			VideoID:        id,
			Height:         height,
			Segments:       st.Segments,
			Progress:       st.Progress,
			EncoderSegment: st.RunPosition,
		}
	}

	h.mu.Lock()
	h.pruneLocked()
	out := make([]LiveSession, 0, len(h.sessions))
	for _, sess := range h.sessions {
		item := LiveSession{
			UserID:      sess.userID.String(),
			User:        sess.user,
			VideoID:     sess.videoID,
			Title:       sess.title,
			ChannelName: sess.channel,
			Client:      sess.client,
			Device:      sess.device,
			Position:    sess.position,
			Duration:    sess.duration,
			Paused:      sess.paused,
			StartedAt:   sess.startedAt.UTC(),
			UpdatedAt:   sess.updatedAt.UTC(),
			Streaming:   sess.streams > 0,
			Bytes:       sess.bytes.Load(),
			Stalls:      sess.stalls,
			LastStall:   sess.lastStall,
			Delivery:    LiveDelivery{Kind: sess.delivery.Kind, Height: sess.delivery.Height},
			Stats:       sess.stats,
		}
		if job, ok := running[sess.delivery.Name]; ok {
			item.Delivery.Job = &job
		}
		out = append(out, item)
	}
	now := h.now().UTC()
	h.mu.Unlock()

	// Most recently active first: the screen that spoke last is the one an
	// admin looking at this is most likely to be sitting in front of.
	slices.SortFunc(out, func(a, b LiveSession) int {
		if !a.UpdatedAt.Equal(b.UpdatedAt) {
			if a.UpdatedAt.After(b.UpdatedAt) {
				return -1
			}
			return 1
		}
		return strings.Compare(a.VideoID, b.VideoID)
	})

	all := make([]LiveJob, 0, len(running))
	for _, job := range running {
		all = append(all, job)
	}
	slices.SortFunc(all, func(a, b LiveJob) int {
		if a.VideoID != b.VideoID {
			return strings.Compare(a.VideoID, b.VideoID)
		}
		return a.Height - b.Height
	})

	return LiveResponse{Sessions: out, Jobs: all, Stalls: stalls.list(), Now: now}
}

// listLiveSessions answers GET /admin/sessions: every playback on this server
// right now, whoever it belongs to.
//
// Admin-only, and the one endpoint in Flimm that deliberately crosses the
// per-user boundary every other one keeps: an admin running the archive is the
// person who has to answer for what the box is doing, and cannot from a view
// that shows only their own screens. Everyone else gets a 403 — not a 404,
// because unlike a feed id there is nothing here whose existence is a secret.
func (s *Server) listLiveSessions(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r.Context()) {
		writeError(w, http.StatusForbidden, "admin only")
		return
	}
	writeJSON(w, http.StatusOK, s.live.snapshot(s.hlsJobs, s.stalls))
}

// ---- naming a client ----

// clientFromUserAgent names the platform a request came from, best effort.
//
// It is the User-Agent because nothing else is available: a heartbeat and a
// segment request say nothing about who sent them, and asking every client to
// start saying so would be four client changes to learn something two of them
// already announce. AVFoundation's own agent names the device outright, which
// is why an Apple TV and an iPhone can be told apart at all; the API calls
// FlimmKit makes are indistinguishable between them, and get the honest
// "apple" rather than a guess.
func clientFromUserAgent(ua string) string {
	switch {
	case ua == "":
		return ""
	case strings.Contains(ua, "Apple TV"):
		return "tvos"
	case strings.Contains(ua, "iPhone"):
		return "ios"
	case strings.Contains(ua, "iPad"):
		return "ipados"
	case strings.Contains(ua, "AppleCoreMedia"), strings.Contains(ua, "CFNetwork"):
		return "apple"
	case strings.Contains(ua, "Mozilla/"):
		return "web"
	}
	return ""
}

// betterClient keeps the more specific of two labels, so a heartbeat that can
// only say "apple" never overwrites the "tvos" a segment request established.
func betterClient(have, got string) string {
	if clientRank(got) > clientRank(have) {
		return got
	}
	return have
}

func clientRank(client string) int {
	switch client {
	case "":
		return 0
	case "apple":
		return 1
	default:
		return 2
	}
}

// userLabel is how an admin will read the account: its email, or its display
// name when the token carried no email.
func userLabel(r *http.Request) string {
	if email := currentEmail(r.Context()); email != "" {
		return email
	}
	return currentName(r.Context())
}

// ---- counting what is sent ----

// countingWriter records the bytes that actually reach the client.
//
// Content-Length would be a guess: a client that seeks away abandons the rest
// of the response, and a range request asks for a slice of a file rather than
// the file. What matters to whoever runs the server is what left the machine,
// which is only knowable by counting it on the way out.
//
// Flush and ReadFrom are forwarded rather than inherited because losing them
// would be a real cost, not a formality: without Flush the reverse proxy
// streaming a video buffers it, and without ReadFrom every segment served from
// the cache gives up sendfile and is copied through userspace.
type countingWriter struct {
	http.ResponseWriter
	n *atomic.Int64
}

func (c *countingWriter) Write(b []byte) (int, error) {
	n, err := c.ResponseWriter.Write(b)
	c.n.Add(int64(n))
	return n, err
}

func (c *countingWriter) Flush() {
	if f, ok := c.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (c *countingWriter) ReadFrom(r io.Reader) (int64, error) {
	if rf, ok := c.ResponseWriter.(io.ReaderFrom); ok {
		n, err := rf.ReadFrom(r)
		c.n.Add(n)
		return n, err
	}
	n, err := io.Copy(c.ResponseWriter, r)
	c.n.Add(n)
	return n, err
}

// Unwrap is how http.ResponseController reaches the real writer past this one.
func (c *countingWriter) Unwrap() http.ResponseWriter { return c.ResponseWriter }
