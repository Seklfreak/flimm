package api

// Casting: asking another screen to open a video.
//
// Remote control (remote.go) steers a player that is already playing. This is
// the step before it. A screen able to play — the Apple TV app, while it is
// open — registers as a *receiver*; a controller signed in as the same account
// sees it beside the sessions and can send it one command, "open". The
// receiver starts playing on its own terms (its codec gate, its quality, its
// progress heartbeat), publishes a session like any other player, and from
// then on it is steered through that session. Nothing here knows about
// playback beyond the video, where to start, and the list it belongs to.
//
// Receivers live in the same hub as sessions: the same TTL, the same per-user
// bucket and the same version, so the one long poll a controller already holds
// open tells it when a television becomes available or goes away.

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// maxRemoteReceivers is how many receivers one account may hold. Every
// television in a house, with room to spare.
const maxRemoteReceivers = 8

// RemoteReceiver is a screen that can be asked to play something.
type RemoteReceiver struct {
	ID string `json:"id"`
	// Device is the name the viewer gave the screen ("Living Room").
	Device string `json:"device"`
	// Platform is the client kind, as on a session: for an icon, never for a
	// decision.
	Platform  string    `json:"platform"`
	UpdatedAt time.Time `json:"updated_at"`
}

// RemoteContext is the list a cast video is played from — the same parameters
// the web client carries in its URL and the Apple clients in
// `PlaybackContext`, so previous/next, autoplay and a shuffled run carry on on
// the television exactly as they were going in the hand.
type RemoteContext struct {
	Feed     string `json:"feed,omitempty"`
	Playlist string `json:"playlist,omitempty"`
	Channel  string `json:"channel,omitempty"`
	Shuffle  string `json:"shuffle,omitempty"`
	Audio    bool   `json:"audio,omitempty"`
}

// publishReceiver records that a screen is available. A heartbeat that changes
// nothing only keeps it alive: waking every controller's poll every few
// seconds to tell it a television is still there would be a poll for nothing.
func (h *remoteHub) publishReceiver(uid uuid.UUID, rcv RemoteReceiver) {
	h.mu.Lock()
	defer h.mu.Unlock()
	u := h.userState(uid)
	if h.prune(u) {
		h.bumped(u)
	}
	rcv.UpdatedAt = h.now()
	entry, ok := h.entries[rcv.ID]
	if ok && (entry.userID != uid || !entry.is(true)) {
		// Somebody else's id, or a session's: ignored, as a session publish
		// ignores a collision — see publish.
		return
	}
	if ok {
		changed := entry.receiver.Device != rcv.Device || entry.receiver.Platform != rcv.Platform
		entry.receiver = &rcv
		if changed {
			h.bumped(u)
		}
		return
	}
	if h.count(u, true) >= maxRemoteReceivers {
		h.evictOldest(u, true)
	}
	h.entries[rcv.ID] = &remoteEntry{userID: uid, receiver: &rcv, commanded: make(chan struct{})}
	u.ids[rcv.ID] = true
	h.bumped(u)
}

// ---- handlers ----

// putRemoteReceiver is a screen saying it is available:
// PUT /playback/receivers/{id}. Upsert, and its own heartbeat.
func (s *Server) putRemoteReceiver(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		writeError(w, http.StatusBadRequest, "receiver id must be a uuid")
		return
	}
	var req RemoteReceiver
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid receiver")
		return
	}
	req.ID = id
	req.Device = clampRemoteText(req.Device, 64)
	req.Platform = clampRemoteText(req.Platform, 16)
	s.remote.publishReceiver(currentUserID(r.Context()), req)
	w.WriteHeader(http.StatusNoContent)
}

// deleteRemoteReceiver is a screen going away — the app closed or went to the
// background — so a controller stops offering it now rather than at the TTL.
func (s *Server) deleteRemoteReceiver(w http.ResponseWriter, r *http.Request) {
	_ = s.remote.end(currentUserID(r.Context()), chi.URLParam(r, "id"), true)
	w.WriteHeader(http.StatusNoContent)
}

// pollReceiverCommands is the receiver waiting to be asked for something.
func (s *Server) pollReceiverCommands(w http.ResponseWriter, r *http.Request) {
	s.pollCommands(w, r, true)
}

// postReceiverCommand is a controller asking a receiver to play a video.
// "open" is the whole vocabulary; anything else is a 400, for the reason a
// session's vocabulary is closed.
func (s *Server) postReceiverCommand(w http.ResponseWriter, r *http.Request) {
	var cmd RemoteCommand
	if err := decodeBody(r, &cmd); err != nil {
		writeError(w, http.StatusBadRequest, "invalid command")
		return
	}
	if cmd.Kind != "open" {
		writeError(w, http.StatusBadRequest, "unknown command")
		return
	}
	cmd.VideoID = clampRemoteText(cmd.VideoID, 64)
	if cmd.VideoID == "" {
		writeError(w, http.StatusBadRequest, "video_id is required")
		return
	}
	if cmd.Position < 0 {
		writeError(w, http.StatusBadRequest, "position must not be negative")
		return
	}
	if c := cmd.Context; c != nil {
		sources := 0
		for _, v := range []string{c.Feed, c.Playlist, c.Channel} {
			if v != "" {
				sources++
			}
		}
		// Mutually exclusive, as they are on every endpoint that takes them.
		if sources > 1 {
			writeError(w, http.StatusBadRequest, "context takes one of feed, playlist and channel")
			return
		}
		c.Feed = clampRemoteText(c.Feed, 64)
		c.Playlist = clampRemoteText(c.Playlist, 64)
		c.Channel = clampRemoteText(c.Channel, 64)
		c.Shuffle = clampRemoteText(c.Shuffle, 64)
	}
	cmd.Delta = 0
	seq, err := s.remote.command(currentUserID(r.Context()), chi.URLParam(r, "id"), true, cmd)
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"seq": seq})
}
