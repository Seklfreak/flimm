package api

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestRemoteReceiverRegisterCastAndRetire(t *testing.T) {
	s := remoteTestServer(t)
	h := s.Router()
	id := uuid.NewString()

	if rec := do(t, h, http.MethodPut, "/api/v1/playback/receivers/"+id, `{"device":"Living Room","platform":"tvos"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("register: %d %s", rec.Code, rec.Body.String())
	}
	got := decode[remoteListing](t, do(t, h, http.MethodGet, "/api/v1/playback/sessions", ""))
	if len(got.Receivers) != 1 || got.Receivers[0].ID != id || got.Receivers[0].Device != "Living Room" {
		t.Fatalf("receivers = %+v", got.Receivers)
	}
	// A receiver is not playing anything, so it is not a session to steer.
	if len(got.Sessions) != 0 {
		t.Fatalf("a receiver was listed as a session: %+v", got.Sessions)
	}

	body := `{"kind":"open","video_id":"v1","position":42,"context":{"playlist":"pl1","shuffle":"abc"}}`
	if rec := do(t, h, http.MethodPost, "/api/v1/playback/receivers/"+id+"/commands", body); rec.Code != http.StatusAccepted {
		t.Fatalf("cast: %d %s", rec.Code, rec.Body.String())
	}
	type batch struct {
		Commands []RemoteCommand `json:"commands"`
		Cursor   uint64          `json:"cursor"`
	}
	polled := decode[batch](t, do(t, h, http.MethodGet, "/api/v1/playback/receivers/"+id+"/commands?after=0", ""))
	if len(polled.Commands) != 1 {
		t.Fatalf("commands = %+v", polled.Commands)
	}
	cmd := polled.Commands[0]
	if cmd.Kind != "open" || cmd.VideoID != "v1" || cmd.Position != 42 ||
		cmd.Context == nil || cmd.Context.Playlist != "pl1" || cmd.Context.Shuffle != "abc" {
		t.Fatalf("command = %+v (context %+v)", cmd, cmd.Context)
	}

	if rec := do(t, h, http.MethodDelete, "/api/v1/playback/receivers/"+id, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("retire: %d", rec.Code)
	}
	if after := decode[remoteListing](t, do(t, h, http.MethodGet, "/api/v1/playback/sessions", "")); len(after.Receivers) != 0 {
		t.Fatalf("receivers after retire = %+v", after.Receivers)
	}
}

func TestRemoteReceiverCommandValidation(t *testing.T) {
	s := remoteTestServer(t)
	h := s.Router()
	id := uuid.NewString()
	do(t, h, http.MethodPut, "/api/v1/playback/receivers/"+id, `{"device":"TV"}`)
	path := "/api/v1/playback/receivers/" + id + "/commands"

	for name, body := range map[string]string{
		"steering kind":     `{"kind":"pause"}`,
		"no video":          `{"kind":"open"}`,
		"negative position": `{"kind":"open","video_id":"v1","position":-1}`,
		"two sources":       `{"kind":"open","video_id":"v1","context":{"feed":"f","playlist":"p"}}`,
	} {
		if rec := do(t, h, http.MethodPost, path, body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", name, rec.Code)
		}
	}
	if rec := do(t, h, http.MethodPut, "/api/v1/playback/receivers/not-a-uuid", `{}`); rec.Code != http.StatusBadRequest {
		t.Errorf("non-uuid id: %d, want 400", rec.Code)
	}
}

// Sessions and receivers share one id space, but not their vocabularies: a
// cast cannot be sent to a session, nor a pause to a receiver.
func TestRemoteReceiverAndSessionAreNotInterchangeable(t *testing.T) {
	s := remoteTestServer(t)
	h := s.Router()
	session, receiver := uuid.NewString(), uuid.NewString()
	do(t, h, http.MethodPut, "/api/v1/playback/sessions/"+session, publishBody("v1", 0, false))
	do(t, h, http.MethodPut, "/api/v1/playback/receivers/"+receiver, `{"device":"TV"}`)

	if rec := do(t, h, http.MethodPost, "/api/v1/playback/receivers/"+session+"/commands", `{"kind":"open","video_id":"v2"}`); rec.Code != http.StatusNotFound {
		t.Errorf("cast to a session: %d, want 404", rec.Code)
	}
	if rec := do(t, h, http.MethodPost, "/api/v1/playback/sessions/"+receiver+"/commands", `{"kind":"pause"}`); rec.Code != http.StatusNotFound {
		t.Errorf("pause to a receiver: %d, want 404", rec.Code)
	}
	if rec := do(t, h, http.MethodGet, "/api/v1/playback/receivers/"+session+"/commands", ""); rec.Code != http.StatusNotFound {
		t.Errorf("session polled as a receiver: %d, want 404", rec.Code)
	}
	// And a session publish cannot take over a receiver's id.
	do(t, h, http.MethodPut, "/api/v1/playback/sessions/"+receiver, publishBody("v1", 0, false))
	got := decode[remoteListing](t, do(t, h, http.MethodGet, "/api/v1/playback/sessions", ""))
	if len(got.Sessions) != 1 || len(got.Receivers) != 1 {
		t.Fatalf("sessions %d, receivers %d; want 1 and 1", len(got.Sessions), len(got.Receivers))
	}
}

func TestRemoteReceiverIsolatesUsers(t *testing.T) {
	hub := newRemoteHub()
	mine, theirs := uuid.New(), uuid.New()
	id := uuid.NewString()
	hub.publishReceiver(mine, RemoteReceiver{ID: id, Device: "TV"})

	if receivers := hub.list(theirs).Receivers; len(receivers) != 0 {
		t.Fatalf("another user sees %d receivers", len(receivers))
	}
	if _, err := hub.command(theirs, id, true, RemoteCommand{Kind: "open", VideoID: "v1"}); err == nil {
		t.Fatal("another user could cast to the receiver")
	}
	if _, _, err := hub.waitCommands(t.Context(), theirs, id, true, 0, 0); err == nil {
		t.Fatal("another user could read the receiver's commands")
	}
	if err := hub.end(theirs, id, true); err == nil {
		t.Fatal("another user could retire the receiver")
	}
	// Nor can they claim the id by registering it themselves.
	hub.publishReceiver(theirs, RemoteReceiver{ID: id, Device: "Theirs"})
	if receivers := hub.list(mine).Receivers; len(receivers) != 1 || receivers[0].Device != "TV" {
		t.Fatalf("owner sees %+v", receivers)
	}
}

// A heartbeat that changes nothing must not wake every controller's poll; a
// receiver appearing must.
func TestRemoteReceiverHeartbeatIsQuiet(t *testing.T) {
	hub := newRemoteHub()
	uid := uuid.New()
	id := uuid.NewString()
	version := hub.list(uid).Version

	done := make(chan remoteListing, 1)
	go func() { done <- hub.waitList(context.Background(), uid, version, time.Second) }()
	time.Sleep(20 * time.Millisecond)
	hub.publishReceiver(uid, RemoteReceiver{ID: id, Device: "TV"})
	select {
	case got := <-done:
		if len(got.Receivers) != 1 {
			t.Fatalf("poll returned %+v", got.Receivers)
		}
		version = got.Version
	case <-time.After(time.Second):
		t.Fatal("poll did not wake when a receiver appeared")
	}

	hub.publishReceiver(uid, RemoteReceiver{ID: id, Device: "TV"})
	if got := hub.list(uid).Version; got != version {
		t.Fatalf("an unchanged heartbeat moved the version %d → %d", version, got)
	}
	hub.publishReceiver(uid, RemoteReceiver{ID: id, Device: "Living Room"})
	if got := hub.list(uid).Version; got == version {
		t.Fatal("a renamed receiver did not move the version")
	}
}

func TestRemoteReceiverLapsesAndIsCapped(t *testing.T) {
	hub := newRemoteHub()
	now := time.Now()
	hub.now = func() time.Time { return now }
	uid := uuid.New()
	hub.publishReceiver(uid, RemoteReceiver{ID: uuid.NewString(), Device: "Old"})
	now = now.Add(remoteSessionTTL + time.Second)
	if receivers := hub.list(uid).Receivers; len(receivers) != 0 {
		t.Fatalf("lapsed receiver is still listed: %+v", receivers)
	}

	// The ceiling counts receivers apart from sessions, so a house full of
	// televisions does not push out the one that is playing.
	playing := uuid.NewString()
	hub.publish(uid, RemoteSession{ID: playing, VideoID: "v1"})
	for range maxRemoteReceivers + 2 {
		now = now.Add(time.Second)
		hub.publishReceiver(uid, RemoteReceiver{ID: uuid.NewString(), Device: "TV"})
	}
	listing := hub.list(uid)
	if len(listing.Receivers) != maxRemoteReceivers {
		t.Fatalf("receivers = %d, want %d", len(listing.Receivers), maxRemoteReceivers)
	}
	if len(listing.Sessions) != 1 || listing.Sessions[0].ID != playing {
		t.Fatalf("sessions = %+v, want the playing one kept", listing.Sessions)
	}
}
