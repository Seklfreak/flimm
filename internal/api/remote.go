package api

// Remote control: one screen plays, another steers it.
//
// A player publishes what it is doing as a *session*; anything else signed in
// as the same user can read those sessions and send commands back. That is the
// whole model. It is deliberately not a cast protocol — no client tells another
// what to open — so a session only ever exists because a player already started
// something on its own.
//
// It lives in memory rather than in Postgres, because none of it is worth
// keeping. A session describes a player that is running *right now*; after a
// restart of either end the truth is republished within one heartbeat, and a
// row that survived would only ever be a lie about a television that has since
// been turned off. The one consequence to know about: a deployment running
// several server replicas behind a load balancer would have the phone and the
// television talking to different memories. Flimm is a single-process
// self-hosted server (one transcode slot, one prepare job), and this is one
// more thing that assumes it.
//
// Both directions are long polls rather than a socket. The two clients are
// already built around `APIClient`, which is plain request/response with the
// bearer token and its refresh-and-retry on it; a WebSocket would need its own
// authentication, its own reconnect, and its own place in every proxy between
// here and the couch, to save what amounts to one idle connection each.

import (
	"cmp"
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

const (
	// How long a session stays live without being republished. Publishers
	// heartbeat every 10s, so this is four missed beats — long enough to ride
	// out a stalled request, short enough that a television unplugged mid-video
	// disappears from the phone while the viewer is still holding it.
	remoteSessionTTL = 45 * time.Second
	// How long a poll waits before answering "nothing changed". Under the
	// 120s route timeout and well under the 60s a URLSession request gets by
	// default, so a wait that runs its full length is a normal answer on both
	// ends rather than a timeout either has to recover from.
	defaultRemoteWait = 25 * time.Second
	// Commands kept per session. A publisher drains them within a poll cycle;
	// this only bounds what a session accumulates while one is disconnected,
	// and dropping the oldest is right — a stale seek is worse than no seek.
	remoteCommandBacklog = 32
	// Sessions kept per user. Enough for every screen in a house to be
	// playing at once, and a ceiling on what one account can hold in memory.
	maxRemoteSessions = 8
	// The furthest a single skip may move. Bounded so a controller cannot use
	// it to express a seek it should have sent as one.
	maxRemoteSkip = 600
)

// RemoteSession is one player saying what it is playing.
//
// Title, channel and thumbnail travel with it so a controller can draw the
// "playing on…" bar the moment it hears about a session, without a round trip
// for a video it may never open. Everything else about the video — the
// description, the chapters, the comments — it fetches for itself from the
// endpoints it already uses, because that is the same video detail every other
// screen reads and there is no reason for a second copy of it to exist here.
type RemoteSession struct {
	ID     string `json:"id"`
	Device string `json:"device"`
	// Platform is the client kind: "tvos", "ios", "web". Free-form on
	// purpose — a controller uses it to pick an icon, never to decide what a
	// session can do.
	Platform    string `json:"platform"`
	VideoID     string `json:"video_id"`
	Title       string `json:"title"`
	ChannelName string `json:"channel_name"`
	ThumbURL    string `json:"thumb_url"`
	// Position when the session was last published, in seconds. A controller
	// that wants a moving clock runs it forward from here itself; see
	// `RemoteClock` in FlimmKit, which is the one implementation of that.
	Position float64 `json:"position"`
	Duration float64 `json:"duration"`
	Paused   bool    `json:"paused"`
	// Speed is the rate the position advances at, so a controller running its
	// own clock advances it at the same rate the television does.
	Speed       float64   `json:"speed"`
	AudioOnly   bool      `json:"audio_only"`
	CanNext     bool      `json:"can_next"`
	CanPrevious bool      `json:"can_previous"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// RemoteCommand is one instruction for a player, in the order it was sent.
//
// The publisher acknowledges nothing: it applies what it can and publishes its
// state, and the state *is* the acknowledgement. A controller that sent "pause"
// and sees a paused session knows it landed; one that sees nothing change knows
// it did not, without a second protocol to say so.
type RemoteCommand struct {
	Seq  uint64 `json:"seq"`
	Kind string `json:"kind"`
	// Position is the seek target, in seconds. Only for kind "seek".
	Position float64 `json:"position,omitempty"`
	// Delta is how far to move from wherever playback is, in seconds, signed.
	// Only for kind "skip" — which exists separately from "seek" because a
	// controller pressing ±10s does not know where the television is to within
	// the round trip, and a seek computed from a projected clock would land
	// somewhere slightly wrong every time.
	Delta float64 `json:"delta,omitempty"`
}

// remoteCommandKinds is the whole vocabulary. Anything else is a 400: a
// publisher must be able to trust that a command it does not recognise is a
// server it is too old for, not a controller typing.
var remoteCommandKinds = map[string]bool{
	"play":     true,
	"pause":    true,
	"seek":     true,
	"skip":     true,
	"next":     true,
	"previous": true,
}

// remoteEntry is a session plus what has been sent to it.
type remoteEntry struct {
	session  RemoteSession
	userID   uuid.UUID
	commands []RemoteCommand
	nextSeq  uint64
	// commanded is closed and replaced whenever a command arrives, waking the
	// publisher's poll. State changes do not touch it — a publisher waking on
	// its own heartbeat would poll in a loop for as long as it played.
	commanded chan struct{}
}

// remoteUser is one account's live sessions.
//
// The version counts changes to the *set* — a session appearing, lapsing, or
// publishing new state — so a controller can hold a poll open against a number
// instead of comparing lists.
type remoteUser struct {
	version uint64
	changed chan struct{}
	ids     map[string]bool
}

// remoteHub holds every live session. One mutex covers all of it; the work
// under it is map writes and closing a channel, and the only thing that waits
// is a poll, which does so outside the lock.
type remoteHub struct {
	mu      sync.Mutex
	entries map[string]*remoteEntry
	users   map[uuid.UUID]*remoteUser
	// now is time.Now except in tests.
	now func() time.Time
}

func newRemoteHub() *remoteHub {
	return &remoteHub{
		entries: map[string]*remoteEntry{},
		users:   map[uuid.UUID]*remoteUser{},
		now:     time.Now,
	}
}

// errNoRemoteSession is "no such live session for this user" — the answer for a
// session that never existed, one that belongs to somebody else, and one that
// has lapsed alike. A controller cannot tell those apart, which is the same
// rule feeds and history follow: existence is not leaked, and a 404 is what a
// caller gets.
var errNoRemoteSession = errors.New("no such session")

// userState returns the user's bucket, creating it on first use.
// Caller holds the lock.
func (h *remoteHub) userState(uid uuid.UUID) *remoteUser {
	u, ok := h.users[uid]
	if !ok {
		u = &remoteUser{changed: make(chan struct{}), ids: map[string]bool{}}
		h.users[uid] = u
	}
	return u
}

// bumped records a change to a user's set of sessions and wakes every poll
// waiting on it. Caller holds the lock.
func (h *remoteHub) bumped(u *remoteUser) {
	u.version++
	close(u.changed)
	u.changed = make(chan struct{})
}

// prune drops the user's lapsed sessions, reporting whether any went away.
// Caller holds the lock.
func (h *remoteHub) prune(u *remoteUser) bool {
	cutoff := h.now().Add(-remoteSessionTTL)
	dropped := false
	for id := range u.ids {
		entry, ok := h.entries[id]
		if !ok || entry.session.UpdatedAt.Before(cutoff) {
			delete(h.entries, id)
			delete(u.ids, id)
			dropped = true
		}
	}
	return dropped
}

// publish records what a player is doing now.
func (h *remoteHub) publish(uid uuid.UUID, s RemoteSession) {
	h.mu.Lock()
	defer h.mu.Unlock()
	u := h.userState(uid)
	h.prune(u)
	s.UpdatedAt = h.now()
	entry, ok := h.entries[s.ID]
	if ok && entry.userID != uid {
		// Another account already holds this id. Refusing outright would tell
		// the caller the id exists; ignoring the publish leaves it with a
		// session that never appears, which is what a colliding random id
		// deserves and is indistinguishable from a lapsed one.
		return
	}
	if !ok {
		// Over the ceiling, the least recently heard from makes way — a
		// screen that stopped publishing is the one nobody is watching.
		if len(u.ids) >= maxRemoteSessions {
			h.evictOldest(u)
		}
		entry = &remoteEntry{userID: uid, commanded: make(chan struct{})}
		h.entries[s.ID] = entry
		u.ids[s.ID] = true
	}
	entry.session = s
	h.bumped(u)
}

// evictOldest removes the user's least recently published session.
// Caller holds the lock.
func (h *remoteHub) evictOldest(u *remoteUser) {
	var oldestID string
	var oldest time.Time
	for id := range u.ids {
		entry, ok := h.entries[id]
		if !ok {
			continue
		}
		if oldestID == "" || entry.session.UpdatedAt.Before(oldest) {
			oldestID, oldest = id, entry.session.UpdatedAt
		}
	}
	if oldestID != "" {
		delete(h.entries, oldestID)
		delete(u.ids, oldestID)
	}
}

// end retires a session: the player stopped, rather than went quiet.
func (h *remoteHub) end(uid uuid.UUID, id string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	u := h.userState(uid)
	h.prune(u)
	entry, ok := h.entries[id]
	if !ok || entry.userID != uid {
		return errNoRemoteSession
	}
	delete(h.entries, id)
	delete(u.ids, id)
	h.bumped(u)
	return nil
}

// list is the user's live sessions and the version they were read at.
func (h *remoteHub) list(uid uuid.UUID) ([]RemoteSession, uint64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.listLocked(h.userState(uid))
}

// listLocked prunes, then reads. Pruning here rather than on a timer is what
// makes a poll that woke for nothing still report a television that lapsed
// while it was asleep. Caller holds the lock.
func (h *remoteHub) listLocked(u *remoteUser) ([]RemoteSession, uint64) {
	if h.prune(u) {
		h.bumped(u)
	}
	out := make([]RemoteSession, 0, len(u.ids))
	for id := range u.ids {
		if entry, ok := h.entries[id]; ok {
			out = append(out, entry.session)
		}
	}
	// Newest first, so a controller with no preference attaches to the screen
	// that spoke most recently. Ties break on id so the order is stable.
	slices.SortFunc(out, func(a, b RemoteSession) int {
		switch {
		case a.UpdatedAt.After(b.UpdatedAt):
			return -1
		case b.UpdatedAt.After(a.UpdatedAt):
			return 1
		}
		return cmp.Compare(a.ID, b.ID)
	})
	return out, u.version
}

// waitList answers as soon as the user's sessions differ from `since`, and
// otherwise waits — but never past the moment the next session lapses, or the
// phone would keep drawing a television that stopped answering.
func (h *remoteHub) waitList(ctx context.Context, uid uuid.UUID, since uint64, wait time.Duration) ([]RemoteSession, uint64) {
	for {
		h.mu.Lock()
		u := h.userState(uid)
		sessions, version := h.listLocked(u)
		if version != since || wait <= 0 {
			h.mu.Unlock()
			return sessions, version
		}
		changed := u.changed
		deadline := h.nextLapseLocked(u, wait)
		h.mu.Unlock()

		timer := time.NewTimer(deadline)
		select {
		case <-changed:
			timer.Stop()
			// Loop rather than answer: a change that leaves the version where
			// the caller already was cannot happen, but re-reading under the
			// lock is what keeps that an invariant rather than an assumption.
		case <-timer.C:
			return h.list(uid)
		case <-ctx.Done():
			timer.Stop()
			return sessions, version
		}
	}
}

// nextLapseLocked is how long until the earliest session goes stale, capped at
// the caller's wait. Caller holds the lock.
func (h *remoteHub) nextLapseLocked(u *remoteUser, wait time.Duration) time.Duration {
	now := h.now()
	for id := range u.ids {
		entry, ok := h.entries[id]
		if !ok {
			continue
		}
		// A hair past the deadline, so the poll that wakes finds it expired
		// rather than one nanosecond short of it.
		remaining := entry.session.UpdatedAt.Add(remoteSessionTTL + time.Millisecond).Sub(now)
		if remaining < wait {
			wait = remaining
		}
	}
	if wait < 0 {
		wait = 0
	}
	return wait
}

// command queues one instruction for a session and returns its sequence
// number.
func (h *remoteHub) command(uid uuid.UUID, id string, cmd RemoteCommand) (uint64, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	u := h.userState(uid)
	h.prune(u)
	entry, ok := h.entries[id]
	if !ok || entry.userID != uid {
		return 0, errNoRemoteSession
	}
	entry.nextSeq++
	cmd.Seq = entry.nextSeq
	entry.commands = append(entry.commands, cmd)
	if len(entry.commands) > remoteCommandBacklog {
		entry.commands = entry.commands[len(entry.commands)-remoteCommandBacklog:]
	}
	close(entry.commanded)
	entry.commanded = make(chan struct{})
	return cmd.Seq, nil
}

// waitCommands hands a publisher everything sent to it since `after`, waiting
// for the first one when there is nothing yet.
//
// The cursor comes back even when the list is empty, and a publisher must
// adopt it: a session whose backlog overflowed has commands it will never see,
// and a cursor that only moved on delivery would ask for them forever.
func (h *remoteHub) waitCommands(
	ctx context.Context, uid uuid.UUID, id string, after uint64, wait time.Duration,
) ([]RemoteCommand, uint64, error) {
	for {
		h.mu.Lock()
		entry, ok := h.entries[id]
		if !ok || entry.userID != uid {
			h.mu.Unlock()
			return nil, 0, errNoRemoteSession
		}
		pending := pendingAfter(entry.commands, after)
		cursor := entry.nextSeq
		if len(pending) > 0 || wait <= 0 {
			h.mu.Unlock()
			return pending, cursor, nil
		}
		commanded := entry.commanded
		h.mu.Unlock()

		timer := time.NewTimer(wait)
		select {
		case <-commanded:
			timer.Stop()
		case <-timer.C:
			return nil, cursor, nil
		case <-ctx.Done():
			timer.Stop()
			return nil, cursor, nil
		}
	}
}

// pendingAfter is the tail of the backlog a publisher has not seen.
func pendingAfter(commands []RemoteCommand, after uint64) []RemoteCommand {
	var out []RemoteCommand
	for _, c := range commands {
		if c.Seq > after {
			out = append(out, c)
		}
	}
	return out
}

// ---- handlers ----

// putRemoteSession is a player publishing itself: PUT /playback/sessions/{id}.
//
// Upsert rather than create-then-update, because the publisher owns the id (a
// UUID it makes once per playback session) and a television that lost the
// network must be able to come back to the same session without discovering
// first whether the server still remembers it.
func (s *Server) putRemoteSession(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r.Context())
	id := chi.URLParam(r, "id")
	// A client-chosen key that ends up in a map wants a shape. A UUID is what
	// every client already has to hand, and it bounds both the length and how
	// many a confused client can invent before the per-user ceiling stops it.
	if _, err := uuid.Parse(id); err != nil {
		writeError(w, http.StatusBadRequest, "session id must be a uuid")
		return
	}
	var req RemoteSession
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid session")
		return
	}
	if req.VideoID == "" {
		writeError(w, http.StatusBadRequest, "video_id is required")
		return
	}
	req.ID = id
	req.Position = max(0, req.Position)
	req.Duration = max(0, req.Duration)
	if req.Speed <= 0 {
		req.Speed = 1
	}
	req.Device = clampRemoteText(req.Device, 64)
	req.Platform = clampRemoteText(req.Platform, 16)
	req.Title = clampRemoteText(req.Title, 300)
	req.ChannelName = clampRemoteText(req.ChannelName, 200)
	req.ThumbURL = clampRemoteText(req.ThumbURL, 500)
	s.remote.publish(uid, req)
	w.WriteHeader(http.StatusNoContent)
}

// deleteRemoteSession retires a session the moment a player stops, so a
// controller never has to wait out the TTL to learn the screen went dark.
func (s *Server) deleteRemoteSession(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r.Context())
	// Ending a session that is already gone is a success: a player tearing
	// down after a dropped connection must not have to care whether its own
	// lapse got there first.
	_ = s.remote.end(uid, chi.URLParam(r, "id"))
	w.WriteHeader(http.StatusNoContent)
}

// listRemoteSessions is what a controller lives on: every screen of this
// user's that is playing, and a version to come back with.
//
// With `since` it is a long poll — it answers the moment anything changes, and
// after the wait otherwise. Without one it answers immediately, which is what a
// client that has just opened wants.
func (s *Server) listRemoteSessions(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r.Context())
	sessions, version := s.remote.list(uid)
	if raw := r.URL.Query().Get("since"); raw != "" {
		since, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "since must be a number")
			return
		}
		sessions, version = s.remote.waitList(r.Context(), uid, since, s.remoteWait)
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": sessions, "version": version})
}

// pollRemoteCommands is the publisher's half: hold a request open until
// somebody presses something.
func (s *Server) pollRemoteCommands(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r.Context())
	id := chi.URLParam(r, "id")
	var after uint64
	if raw := r.URL.Query().Get("after"); raw != "" {
		parsed, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "after must be a number")
			return
		}
		after = parsed
	}
	commands, cursor, err := s.remote.waitCommands(r.Context(), uid, id, after, s.remoteWait)
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if commands == nil {
		commands = []RemoteCommand{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"commands": commands, "cursor": cursor})
}

// postRemoteCommand is the controller pressing something.
func (s *Server) postRemoteCommand(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r.Context())
	var cmd RemoteCommand
	if err := decodeBody(r, &cmd); err != nil {
		writeError(w, http.StatusBadRequest, "invalid command")
		return
	}
	if !remoteCommandKinds[cmd.Kind] {
		writeError(w, http.StatusBadRequest, "unknown command")
		return
	}
	switch cmd.Kind {
	case "seek":
		if cmd.Position < 0 {
			writeError(w, http.StatusBadRequest, "position must not be negative")
			return
		}
		cmd.Delta = 0
	case "skip":
		if cmd.Delta == 0 || cmd.Delta > maxRemoteSkip || cmd.Delta < -maxRemoteSkip {
			writeError(w, http.StatusBadRequest, "delta is out of range")
			return
		}
		cmd.Position = 0
	default:
		cmd.Position, cmd.Delta = 0, 0
	}
	seq, err := s.remote.command(uid, chi.URLParam(r, "id"), cmd)
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"seq": seq})
}

// clampRemoteText bounds a string a client chose. None of these are rendered
// as anything but text, so the only risk is size.
func clampRemoteText(v string, limit int) string {
	if len(v) <= limit {
		return v
	}
	return v[:limit]
}
