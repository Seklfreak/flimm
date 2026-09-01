package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Seklfreak/flimm/internal/ta"
)

// remoteTestServer is a server whose polls answer at once, so a test that is
// about what a poll returns never waits 25 seconds for it.
func remoteTestServer(t *testing.T) *Server {
	t.Helper()
	s := newTestServer(ta.NewFake(), newEventStore().querier())
	s.remoteWait = 0
	return s
}

func publishBody(video string, position float64, paused bool) string {
	body, _ := json.Marshal(map[string]any{
		"device": "Living Room", "platform": "tvos",
		"video_id": video, "title": "A Video", "channel_name": "A Channel",
		"position": position, "duration": 600, "paused": paused, "speed": 1,
		"can_next": true,
	})
	return string(body)
}

func TestRemoteSessionPublishAndList(t *testing.T) {
	s := remoteTestServer(t)
	h := s.Router()
	id := uuid.NewString()

	if rec := do(t, h, http.MethodPut, "/api/v1/playback/sessions/"+id, publishBody("v1", 42, false)); rec.Code != http.StatusNoContent {
		t.Fatalf("publish: %d %s", rec.Code, rec.Body.String())
	}

	type listing struct {
		Sessions []RemoteSession `json:"sessions"`
		Version  uint64          `json:"version"`
	}
	got := decode[listing](t, do(t, h, http.MethodGet, "/api/v1/playback/sessions", ""))
	if len(got.Sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(got.Sessions))
	}
	session := got.Sessions[0]
	if session.ID != id || session.VideoID != "v1" || session.Position != 42 || session.Paused {
		t.Fatalf("session = %+v", session)
	}
	if session.Device != "Living Room" || session.Title != "A Video" || !session.CanNext {
		t.Fatalf("session lost its labels: %+v", session)
	}
	if session.UpdatedAt.IsZero() {
		t.Fatal("session has no updated_at, so a controller cannot tell how old it is")
	}
	if got.Version == 0 {
		t.Fatal("version = 0, so a controller has nothing to poll against")
	}

	// Ending it is what a player does when it stops, rather than leaving the
	// controller to wait out the TTL.
	if rec := do(t, h, http.MethodDelete, "/api/v1/playback/sessions/"+id, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", rec.Code)
	}
	if after := decode[listing](t, do(t, h, http.MethodGet, "/api/v1/playback/sessions", "")); len(after.Sessions) != 0 {
		t.Fatalf("sessions after delete = %d, want 0", len(after.Sessions))
	}
	// Ending one that is already gone is still a success: a player tearing
	// down twice must not have to care which call got there first.
	if rec := do(t, h, http.MethodDelete, "/api/v1/playback/sessions/"+id, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("second delete: %d", rec.Code)
	}
}

func TestRemoteSessionIDMustBeUUID(t *testing.T) {
	s := remoteTestServer(t)
	rec := do(t, s.Router(), http.MethodPut, "/api/v1/playback/sessions/not-a-uuid", publishBody("v1", 0, false))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400", rec.Code)
	}
}

func TestRemoteSessionNeedsVideo(t *testing.T) {
	s := remoteTestServer(t)
	rec := do(t, s.Router(), http.MethodPut, "/api/v1/playback/sessions/"+uuid.NewString(), `{"device":"TV"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400", rec.Code)
	}
}

func TestRemoteCommandRoundTrip(t *testing.T) {
	s := remoteTestServer(t)
	h := s.Router()
	id := uuid.NewString()
	do(t, h, http.MethodPut, "/api/v1/playback/sessions/"+id, publishBody("v1", 42, false))

	type accepted struct {
		Seq uint64 `json:"seq"`
	}
	sent := decode[accepted](t, do(t, h, http.MethodPost,
		"/api/v1/playback/sessions/"+id+"/commands", `{"kind":"seek","position":120.5}`))
	if sent.Seq != 1 {
		t.Fatalf("seq = %d, want 1", sent.Seq)
	}

	type drained struct {
		Commands []RemoteCommand `json:"commands"`
		Cursor   uint64          `json:"cursor"`
	}
	got := decode[drained](t, do(t, h, http.MethodGet, "/api/v1/playback/sessions/"+id+"/commands", ""))
	if len(got.Commands) != 1 || got.Commands[0].Kind != "seek" || got.Commands[0].Position != 120.5 {
		t.Fatalf("commands = %+v", got.Commands)
	}
	if got.Cursor != 1 {
		t.Fatalf("cursor = %d, want 1", got.Cursor)
	}

	// Coming back with the cursor is what stops a publisher applying the same
	// seek on every poll.
	again := decode[drained](t, do(t, h, http.MethodGet, "/api/v1/playback/sessions/"+id+"/commands?after=1", ""))
	if len(again.Commands) != 0 {
		t.Fatalf("commands after cursor = %+v, want none", again.Commands)
	}
	if again.Cursor != 1 {
		t.Fatalf("cursor = %d, want it to hold at 1", again.Cursor)
	}
}

func TestRemoteCommandValidation(t *testing.T) {
	s := remoteTestServer(t)
	h := s.Router()
	id := uuid.NewString()
	do(t, h, http.MethodPut, "/api/v1/playback/sessions/"+id, publishBody("v1", 0, false))
	path := "/api/v1/playback/sessions/" + id + "/commands"

	for _, tc := range []struct {
		name string
		body string
		want int
	}{
		{"unknown kind", `{"kind":"selfdestruct"}`, http.StatusBadRequest},
		{"no kind", `{}`, http.StatusBadRequest},
		{"negative seek", `{"kind":"seek","position":-5}`, http.StatusBadRequest},
		{"zero skip", `{"kind":"skip","delta":0}`, http.StatusBadRequest},
		{"absurd skip", `{"kind":"skip","delta":9000}`, http.StatusBadRequest},
		{"backward skip", `{"kind":"skip","delta":-10}`, http.StatusAccepted},
		{"play", `{"kind":"play"}`, http.StatusAccepted},
		{"next", `{"kind":"next"}`, http.StatusAccepted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if rec := do(t, h, http.MethodPost, path, tc.body); rec.Code != tc.want {
				t.Fatalf("code = %d, want %d (%s)", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

// A command for a session nobody is running is a 404, not a queue that fills up
// against a television that is not there.
func TestRemoteCommandUnknownSession(t *testing.T) {
	s := remoteTestServer(t)
	h := s.Router()
	path := "/api/v1/playback/sessions/" + uuid.NewString() + "/commands"
	if rec := do(t, h, http.MethodPost, path, `{"kind":"pause"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("post code = %d, want 404", rec.Code)
	}
	if rec := do(t, h, http.MethodGet, path, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("poll code = %d, want 404", rec.Code)
	}
}

// Another account's session is not merely uncontrollable, it is invisible —
// the same 404-not-403 rule the rest of the per-user API follows.
func TestRemoteHubIsolatesUsers(t *testing.T) {
	hub := newRemoteHub()
	mine, theirs := uuid.New(), uuid.New()
	id := uuid.NewString()
	hub.publish(mine, RemoteSession{ID: id, VideoID: "v1"})

	if sessions, _ := hub.list(theirs); len(sessions) != 0 {
		t.Fatalf("another user sees %d sessions", len(sessions))
	}
	if _, err := hub.command(theirs, id, RemoteCommand{Kind: "pause"}); err == nil {
		t.Fatal("another user could command the session")
	}
	if _, _, err := hub.waitCommands(t.Context(), theirs, id, 0, 0); err == nil {
		t.Fatal("another user could read the session's commands")
	}
	if err := hub.end(theirs, id); err == nil {
		t.Fatal("another user could end the session")
	}
	if sessions, _ := hub.list(mine); len(sessions) != 1 {
		t.Fatalf("owner sees %d sessions, want 1", len(sessions))
	}
}

// The whole point of the long poll: a controller asking with the version it
// already has is answered the moment the television publishes, not on a timer.
func TestRemoteListPollWakesOnPublish(t *testing.T) {
	hub := newRemoteHub()
	uid := uuid.New()
	id := uuid.NewString()
	hub.publish(uid, RemoteSession{ID: id, VideoID: "v1", Position: 10})
	_, version := hub.list(uid)

	done := make(chan []RemoteSession, 1)
	go func() {
		sessions, _ := hub.waitList(context.Background(), uid, version, time.Second)
		done <- sessions
	}()
	// Give the waiter a moment to be waiting, then move the television on.
	time.Sleep(20 * time.Millisecond)
	hub.publish(uid, RemoteSession{ID: id, VideoID: "v1", Position: 11, Paused: true})

	select {
	case sessions := <-done:
		if len(sessions) != 1 || !sessions[0].Paused {
			t.Fatalf("poll returned %+v, want the paused session", sessions)
		}
	case <-time.After(time.Second):
		t.Fatal("poll did not wake on a publish")
	}
}

// And the other half: a publisher's poll wakes on the press, not on its own
// heartbeat.
func TestRemoteCommandPollWakesOnCommand(t *testing.T) {
	hub := newRemoteHub()
	uid := uuid.New()
	id := uuid.NewString()
	hub.publish(uid, RemoteSession{ID: id, VideoID: "v1"})

	done := make(chan []RemoteCommand, 1)
	go func() {
		commands, _, _ := hub.waitCommands(context.Background(), uid, id, 0, time.Second)
		done <- commands
	}()
	time.Sleep(20 * time.Millisecond)
	if _, err := hub.command(uid, id, RemoteCommand{Kind: "pause"}); err != nil {
		t.Fatalf("command: %v", err)
	}

	select {
	case commands := <-done:
		if len(commands) != 1 || commands[0].Kind != "pause" {
			t.Fatalf("poll returned %+v", commands)
		}
	case <-time.After(time.Second):
		t.Fatal("poll did not wake on a command")
	}
}

// A television that stops publishing disappears on its own. Without this the
// phone would keep offering a transport for a screen that is off.
func TestRemoteSessionLapses(t *testing.T) {
	hub := newRemoteHub()
	now := time.Now()
	hub.now = func() time.Time { return now }
	uid := uuid.New()
	hub.publish(uid, RemoteSession{ID: uuid.NewString(), VideoID: "v1"})

	now = now.Add(remoteSessionTTL / 2)
	if sessions, _ := hub.list(uid); len(sessions) != 1 {
		t.Fatalf("session vanished after half the TTL: %+v", sessions)
	}
	now = now.Add(remoteSessionTTL)
	sessions, version := hub.list(uid)
	if len(sessions) != 0 {
		t.Fatalf("lapsed session is still listed: %+v", sessions)
	}
	// The lapse is a change like any other, so a controller polling on the
	// version learns about it rather than sitting on a stale list.
	if version < 2 {
		t.Fatalf("version = %d, want the lapse to have bumped it", version)
	}
}

// A poll must not sleep through a lapse: it comes back when the session it is
// reporting goes stale, even though nothing was published.
func TestRemoteListPollReturnsOnLapse(t *testing.T) {
	hub := newRemoteHub()
	uid := uuid.New()
	// A session already at the end of its life, so the wait shortens to
	// nothing rather than the caller's second.
	hub.publish(uid, RemoteSession{ID: uuid.NewString(), VideoID: "v1"})
	_, version := hub.list(uid)
	hub.now = func() time.Time { return time.Now().Add(remoteSessionTTL) }

	start := time.Now()
	sessions, _ := hub.waitList(context.Background(), uid, version, time.Second)
	if len(sessions) != 0 {
		t.Fatalf("poll returned %+v, want the lapsed session gone", sessions)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("poll waited %v for a session that had already lapsed", elapsed)
	}
}

// The backlog is bounded, and a publisher that missed the overflow must still
// be able to move its cursor past what it will never receive.
func TestRemoteCommandBacklogIsBounded(t *testing.T) {
	hub := newRemoteHub()
	uid := uuid.New()
	id := uuid.NewString()
	hub.publish(uid, RemoteSession{ID: id, VideoID: "v1"})
	for i := 0; i < remoteCommandBacklog*2; i++ {
		if _, err := hub.command(uid, id, RemoteCommand{Kind: "skip", Delta: 10}); err != nil {
			t.Fatalf("command %d: %v", i, err)
		}
	}
	commands, cursor, err := hub.waitCommands(context.Background(), uid, id, 0, 0)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if len(commands) != remoteCommandBacklog {
		t.Fatalf("backlog = %d, want it capped at %d", len(commands), remoteCommandBacklog)
	}
	if cursor != uint64(remoteCommandBacklog*2) {
		t.Fatalf("cursor = %d, want the sequence to have run on past the dropped commands", cursor)
	}
}

// One account cannot fill memory with sessions; the quietest makes way.
func TestRemoteSessionsAreCapped(t *testing.T) {
	hub := newRemoteHub()
	now := time.Now()
	hub.now = func() time.Time { return now }
	uid := uuid.New()
	first := uuid.NewString()
	hub.publish(uid, RemoteSession{ID: first, VideoID: "v0"})
	for i := 0; i < maxRemoteSessions; i++ {
		now = now.Add(time.Second)
		hub.publish(uid, RemoteSession{ID: uuid.NewString(), VideoID: "v1"})
	}
	sessions, _ := hub.list(uid)
	if len(sessions) != maxRemoteSessions {
		t.Fatalf("sessions = %d, want %d", len(sessions), maxRemoteSessions)
	}
	for _, s := range sessions {
		if s.ID == first {
			t.Fatal("the least recently published session survived the cap")
		}
	}
}
