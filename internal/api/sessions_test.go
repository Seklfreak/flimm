package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
)

// liveRequest is a request already through the auth middleware: a user in the
// context, and whatever agent the caller wants to be seen as.
func liveRequest(userAgent string, uid uuid.UUID) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := context.WithValue(req.Context(), userIDKey, uid)
	ctx = context.WithValue(ctx, emailKey, "viewer@example.com")
	if userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	}
	return req.WithContext(ctx)
}

func adminSessions(t *testing.T, h http.Handler) LiveResponse {
	t.Helper()
	rec := do(t, h, http.MethodGet, "/api/v1/admin/sessions", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	return decode[LiveResponse](t, rec)
}

// The whole point of the view: an admin can see who is watching what, and
// which of the two ways it is reaching them — without either half of that
// having been reported by a client.
func TestLiveSessionsShowWhoIsWatchingAndHow(t *testing.T) {
	h, _ := hlsFixture(t, []byte("segment-bytes"))

	if rec := do(t, h, http.MethodPost, "/api/v1/videos/v1/progress", `{"position":120}`); rec.Code != http.StatusOK {
		t.Fatalf("heartbeat: %d: %s", rec.Code, rec.Body.String())
	}
	// One session, from the heartbeat alone: nothing has been streamed yet.
	got := adminSessions(t, h)
	if len(got.Sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(got.Sessions))
	}
	sess := got.Sessions[0]
	switch {
	case sess.VideoID != "v1":
		t.Errorf("video = %q", sess.VideoID)
	case sess.Title != "Video v1":
		t.Errorf("title = %q", sess.Title)
	case sess.Position != 120:
		t.Errorf("position = %v, want the heartbeat's", sess.Position)
	case sess.Duration != 600:
		t.Errorf("duration = %v", sess.Duration)
	case sess.User != "dev@localhost":
		t.Errorf("user = %q, want the account the request was made as", sess.User)
	case sess.Delivery.Kind != "":
		t.Errorf("delivery = %q before anything was streamed", sess.Delivery.Kind)
	case sess.Streaming:
		t.Error("streaming with no media request open")
	}

	// Now the rendition. The delivery path is the request itself, so it is the
	// one thing here a client cannot be wrong about.
	if rec := getMedia(t, h, "/media/hls/v1/1080/seg00000.m4s", ""); rec.Code != http.StatusOK {
		t.Fatalf("segment: %d", rec.Code)
	}
	got = adminSessions(t, h)
	if len(got.Sessions) != 1 {
		t.Fatalf("sessions = %d, want the same one", len(got.Sessions))
	}
	sess = got.Sessions[0]
	if sess.Delivery.Kind != liveRendition || sess.Delivery.Height != 1080 {
		t.Errorf("delivery = %q at %d, want a 1080 rendition", sess.Delivery.Kind, sess.Delivery.Height)
	}
	// The rendition is finished, so there is no job to report — which is
	// itself the answer to "why is this stalling".
	if sess.Delivery.Job != nil {
		t.Errorf("job = %+v for a finished rendition", sess.Delivery.Job)
	}
	if sess.Bytes != int64(len("segment-bytes")) {
		t.Errorf("bytes = %d, want what the segment weighs", sess.Bytes)
	}
	if len(got.Jobs) != 0 {
		t.Errorf("jobs = %+v, nothing is being derived", got.Jobs)
	}
}

// A stall is counted against the session that suffered it, so the list says
// which viewer is the one watching a spinner.
func TestAStallIsCountedAgainstTheSession(t *testing.T) {
	h, _ := hlsFixture(t, []byte("seg"))

	if rec := do(t, h, http.MethodPost, "/api/v1/videos/v1/progress", `{"position":30}`); rec.Code != http.StatusOK {
		t.Fatalf("heartbeat: %d", rec.Code)
	}
	stallReport(t, h, `{"position":30,"seconds":3,"height":0,"client":"web"}`)

	sessions := adminSessions(t, h).Sessions
	if len(sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(sessions))
	}
	if sessions[0].Stalls != 1 || sessions[0].LastStall != stallSource {
		t.Errorf("stalls = %d (%q), want one attributed to the source",
			sessions[0].Stalls, sessions[0].LastStall)
	}
	// The report names the platform, and a named one beats anything derived
	// from an agent string.
	if sessions[0].Client != "web" {
		t.Errorf("client = %q, want the one the report named", sessions[0].Client)
	}
}

// Every account's playback, which is the one place Flimm crosses the per-user
// boundary — so it is the one place the admin check has to hold.
func TestLiveSessionsAreAdminOnly(t *testing.T) {
	h, _ := hlsFixture(t, []byte("seg"))
	if rec := do(t, h, http.MethodGet, "/api/v1/admin/sessions", ""); rec.Code != http.StatusOK {
		t.Fatalf("admin status = %d", rec.Code)
	}

	s := newTestServer(nil, newEventStore().querier())
	ctx := context.WithValue(context.Background(), userIDKey, DevUserID)
	ctx = context.WithValue(ctx, isAdminKey, false)
	w := httptest.NewRecorder()
	s.listLiveSessions(w, httptest.NewRequest(http.MethodGet, "/api/v1/admin/sessions", nil).WithContext(ctx))
	if w.Code != http.StatusForbidden {
		t.Errorf("non-admin status = %d, want 403", w.Code)
	}
}

// Two accounts watching at once are two sessions, and the same account
// watching two videos is two as well.
func TestSessionsAreOnePerViewerAndVideo(t *testing.T) {
	hub := newLiveHub()
	other := uuid.MustParse("00000000-0000-0000-0000-0000000000ff")

	hub.watching(liveRequest("", DevUserID), "v1", "One", "Channel", 10, 600)
	hub.watching(liveRequest("", DevUserID), "v1", "One", "Channel", 20, 600)
	hub.watching(liveRequest("", DevUserID), "v2", "Two", "Channel", 5, 300)
	hub.watching(liveRequest("", other), "v1", "One", "Channel", 90, 600)

	got := hub.snapshot(nil, &stallLog{})
	if len(got.Sessions) != 3 {
		t.Fatalf("sessions = %d, want 3", len(got.Sessions))
	}
	// The repeated heartbeat moved the first one rather than adding to it.
	for _, sess := range got.Sessions {
		if sess.VideoID == "v1" && sess.UserID == DevUserID.String() && sess.Position != 20 {
			t.Errorf("position = %v, want the latest heartbeat's", sess.Position)
		}
	}
}

// A session lapses when nothing is heard from it — except while a media
// request is still open, which is the whole of a direct play.
func TestASessionLapsesUnlessSomethingIsStillStreaming(t *testing.T) {
	hub := newLiveHub()
	now := time.Now()
	hub.now = func() time.Time { return now }

	hub.watching(liveRequest("", DevUserID), "v1", "One", "Channel", 10, 600)
	rec := httptest.NewRecorder()
	_, done := hub.streaming(rec, liveRequest("", DevUserID), "v2", liveDelivery{Kind: liveDirect})

	now = now.Add(liveSessionTTL + time.Second)
	got := hub.snapshot(nil, &stallLog{})
	if len(got.Sessions) != 1 {
		t.Fatalf("sessions = %d, want only the one still streaming", len(got.Sessions))
	}
	if got.Sessions[0].VideoID != "v2" || !got.Sessions[0].Streaming {
		t.Errorf("kept %q (streaming %v), want the open direct play",
			got.Sessions[0].VideoID, got.Sessions[0].Streaming)
	}

	// Once it finishes it is like any other: alive for the TTL, then gone.
	done()
	now = now.Add(liveSessionTTL + time.Second)
	if got := hub.snapshot(nil, &stallLog{}); len(got.Sessions) != 0 {
		t.Errorf("sessions = %d after the stream ended and lapsed", len(got.Sessions))
	}
}

// What actually leaves the machine, counted on the way out — not the
// Content-Length, which a client that seeks away never takes.
func TestBytesAreCountedAsTheyAreSent(t *testing.T) {
	hub := newLiveHub()
	rec := httptest.NewRecorder()
	w, done := hub.streaming(rec, liveRequest("", DevUserID), "v1", liveDelivery{Kind: liveDirect})
	if _, err := w.Write([]byte("twelve bytes")); err != nil {
		t.Fatal(err)
	}
	// Still open: the byte count is live, not a total published at the end.
	if got := hub.snapshot(nil, &stallLog{}).Sessions[0].Bytes; got != 12 {
		t.Errorf("bytes = %d mid-stream, want 12", got)
	}
	done()
	if got := hub.snapshot(nil, &stallLog{}).Sessions[0].Bytes; got != 12 {
		t.Errorf("bytes = %d after the stream closed, want 12", got)
	}
}

// The Apple TV publishes its own readings; the live view relays them whole.
func TestAPublishedSessionCarriesItsStatsIntoTheLiveView(t *testing.T) {
	hub := newLiveHub()
	hub.published(liveRequest("", DevUserID), RemoteSession{
		VideoID: "v1", Title: "One", ChannelName: "Channel", Device: "Living Room",
		Platform: "tvos", Position: 561, Duration: 1476, Paused: true,
		Stats: &RemotePlaybackStats{Delivery: RemoteStatsDelivery{Kind: "rendition", Reason: "no-decoder"}},
	})

	sessions := hub.snapshot(nil, &stallLog{}).Sessions
	if len(sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(sessions))
	}
	sess := sessions[0]
	switch {
	case sess.Device != "Living Room" || sess.Client != "tvos":
		t.Errorf("device %q on %q", sess.Device, sess.Client)
	case !sess.Paused:
		t.Error("paused was not carried over")
	case sess.Stats == nil || sess.Stats.Delivery.Reason != "no-decoder":
		t.Errorf("stats = %+v, want the published block untouched", sess.Stats)
	}

	// A heartbeat is only sent while playing, so hearing one ends the pause.
	hub.watching(liveRequest("", DevUserID), "v1", "One", "Channel", 570, 1476)
	if hub.snapshot(nil, &stallLog{}).Sessions[0].Paused {
		t.Error("still paused after a heartbeat")
	}
}

// AVFoundation names the device it is on, which is the only reason an Apple TV
// and an iPhone can be told apart at all; the API calls FlimmKit makes cannot
// be, and must say so rather than guess.
func TestTheClientIsNamedFromTheUserAgent(t *testing.T) {
	cases := map[string]string{
		"AppleCoreMedia/1.0.0.21K69 (Apple TV; U; CPU OS 17_1 like Mac OS X)": "tvos",
		"AppleCoreMedia/1.0.0.21A340 (iPhone; U; CPU OS 17_0 like Mac OS X)":  "ios",
		"AppleCoreMedia/1.0.0.21A340 (iPad; U; CPU OS 17_0 like Mac OS X)":    "ipados",
		"Flimm/1 CFNetwork/1568.100.1 Darwin/24.0.0":                          "apple",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) Safari/605.1.15":     "web",
		"":                                                                    "",
	}
	for ua, want := range cases {
		if got := clientFromUserAgent(ua); got != want {
			t.Errorf("%q → %q, want %q", ua, got, want)
		}
	}

	// A generic label never overwrites one that says which screen it is.
	if got := betterClient("tvos", "apple"); got != "tvos" {
		t.Errorf("betterClient(tvos, apple) = %q", got)
	}
	if got := betterClient("apple", "ios"); got != "ios" {
		t.Errorf("betterClient(apple, ios) = %q", got)
	}
	if got := betterClient("web", ""); got != "web" {
		t.Errorf("betterClient(web, \"\") = %q", got)
	}
}
