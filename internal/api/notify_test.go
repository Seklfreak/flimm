package api

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Seklfreak/flimm/internal/apns"
	"github.com/Seklfreak/flimm/internal/db/sqlc"
	"github.com/Seklfreak/flimm/internal/ta"
)

// pushCall is one notification the fake APNs received.
type pushCall struct {
	token   string
	alert   map[string]any
	payload map[string]any
}

// fakeAPNs records what is sent and answers per token: 200 unless the token
// is in `status`.
type fakeAPNs struct {
	mu     sync.Mutex
	calls  []pushCall
	status map[string]int
}

func (f *fakeAPNs) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.URL.Path, "/3/device/")
		raw, _ := io.ReadAll(r.Body)
		var payload map[string]any
		_ = json.Unmarshal(raw, &payload)
		aps, _ := payload["aps"].(map[string]any)
		alert, _ := aps["alert"].(map[string]any)
		f.mu.Lock()
		f.calls = append(f.calls, pushCall{token: token, alert: alert, payload: payload})
		code := f.status[token]
		f.mu.Unlock()
		if code == 0 {
			code = http.StatusOK
		}
		w.WriteHeader(code)
		if code == http.StatusGone {
			_, _ = w.Write([]byte(`{"reason":"Unregistered"}`))
		}
	})
}

func (f *fakeAPNs) sent() []pushCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]pushCall(nil), f.calls...)
}

func testPushClient(t *testing.T, url string) *apns.Client {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	c, err := apns.New(apns.Options{
		Key:   pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}),
		KeyID: "KEY", TeamID: "TEAM", Topic: "dev.example.flimm", BaseURL: url,
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// notifyFixture is one notifying feed over channel A, seeded, with its mark
// at `mark`, and a fake archive holding whatever the test adds.
type notifyFixture struct {
	srv     *Server
	client  *ta.Fake
	es      *eventStore
	apns    *fakeAPNs
	feed    sqlc.Feed
	devices []sqlc.PushDevice
	marks   []time.Time
	forgot  []string
	// seen is the user's notify_seen set; seeded records SetFeedNotifySeeded.
	seen      map[string]bool
	seeded    []bool
	dismissed map[string]bool
}

func newNotifyFixture(t *testing.T, mark time.Time) *notifyFixture {
	t.Helper()
	fx := &notifyFixture{
		client:    ta.NewFake(),
		es:        newEventStore(),
		apns:      &fakeAPNs{status: map[string]int{}},
		seen:      map[string]bool{},
		dismissed: map[string]bool{},
		feed: sqlc.Feed{
			ID: uuid.New(), UserID: DevUserID, Name: "DevOps", Sort: "newest", Notify: true, NotifySeeded: true,
			NotifiedAt: pgtype.Timestamptz{Time: mark, Valid: true},
		},
	}
	fx.devices = []sqlc.PushDevice{
		{Token: "phone", UserID: DevUserID, Environment: "production"},
		{Token: "ipad", UserID: DevUserID, Environment: "sandbox"},
	}
	srv := httptest.NewServer(fx.apns.handler())
	t.Cleanup(srv.Close)

	q := fx.es.querier()
	q.ListNotifyFeedsFn = func(context.Context) ([]sqlc.Feed, error) { return []sqlc.Feed{fx.feed}, nil }
	q.ListPushDevicesFn = func(context.Context, uuid.UUID) ([]sqlc.PushDevice, error) { return fx.devices, nil }
	q.ListFeedChannelsFn = func(context.Context, uuid.UUID) ([]string, error) { return []string{"A"}, nil }
	q.ListFeedPlaylistsFn = func(context.Context, uuid.UUID) ([]string, error) { return nil, nil }
	q.SetFeedNotifiedAtFn = func(_ context.Context, arg sqlc.SetFeedNotifiedAtParams) error {
		fx.marks = append(fx.marks, arg.NotifiedAt.Time)
		fx.feed.NotifiedAt = arg.NotifiedAt
		return nil
	}
	q.SetFeedNotifySeededFn = func(_ context.Context, arg sqlc.SetFeedNotifySeededParams) error {
		fx.seeded = append(fx.seeded, arg.NotifySeeded)
		fx.feed.NotifySeeded = arg.NotifySeeded
		return nil
	}
	q.MarkNotifySeenFn = func(_ context.Context, arg sqlc.MarkNotifySeenParams) error {
		for _, id := range arg.VideoIds {
			fx.seen[id] = true
		}
		return nil
	}
	q.ListNotifySeenFn = func(_ context.Context, arg sqlc.ListNotifySeenParams) ([]string, error) {
		var out []string
		for _, id := range arg.VideoIds {
			if fx.seen[id] {
				out = append(out, id)
			}
		}
		return out, nil
	}
	q.ForgetPushDeviceFn = func(_ context.Context, token string) error {
		fx.forgot = append(fx.forgot, token)
		return nil
	}
	q.ListDismissedForVideosFn = func(_ context.Context, arg sqlc.ListDismissedForVideosParams) ([]string, error) {
		var out []string
		for _, id := range arg.VideoIds {
			if fx.dismissed[id] {
				out = append(out, id)
			}
		}
		return out, nil
	}
	fx.srv = NewServer(Options{
		Querier: q, TA: fx.client, Push: testPushClient(t, srv.URL),
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), AppName: "Flimm", MediaSecret: testSecret,
	})
	return fx
}

// indexed adds a video to channel A that TubeArchivist indexed at `at`.
func (fx *notifyFixture) indexed(id string, at time.Time, watched bool) ta.Video {
	v := video(id, "A", "2026-08-01", 600, watched)
	v.DateDownloaded = at.Unix()
	fx.client.AddVideo(v)
	return v
}

// known adds a video the feed has seen before — indexed at `at`, which for
// a refreshed one is *after* the mark.
func (fx *notifyFixture) known(id string, at time.Time) {
	fx.indexed(id, at, false)
	fx.seen[id] = true
}

func TestNotifyAnnouncesOneNewVideoByName(t *testing.T) {
	mark := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	fx := newNotifyFixture(t, mark)
	fx.known("old", mark.Add(-time.Hour))
	// The archive refreshed an old video's metadata: it now reads as
	// downloaded after the mark, and it is not news.
	fx.known("refreshed", mark.Add(15*time.Minute))
	fx.indexed("fresh", mark.Add(10*time.Minute), false)
	fx.indexed("seen", mark.Add(20*time.Minute), true)
	short := fx.indexed("clip", mark.Add(30*time.Minute), false)
	short.VidType = "shorts"
	fx.client.AddVideo(short)
	// Dismissed in Flimm: taken out of the feed, so not news either.
	fx.indexed("nope", mark.Add(40*time.Minute), false)
	fx.dismissed["nope"] = true

	before := time.Now()
	fx.srv.notifyOnce(t.Context())

	sent := fx.apns.sent()
	if len(sent) != 2 {
		t.Fatalf("sent %d notifications, want one per device: %+v", len(sent), sent)
	}
	tokens := map[string]bool{sent[0].token: true, sent[1].token: true}
	if !tokens["phone"] || !tokens["ipad"] {
		t.Errorf("tokens = %v", tokens)
	}
	got := sent[0]
	if got.alert["title"] != "Channel A" || got.alert["subtitle"] != "DevOps" || got.alert["body"] != "Video fresh" {
		t.Errorf("alert = %v", got.alert)
	}
	if got.payload["feed"] != fx.feed.ID.String() || got.payload["video"] != "fresh" {
		t.Errorf("payload = %v", got.payload)
	}
	// The mark moves to the start of the pass, and everything indexed in
	// the window is now known — announced or not.
	if len(fx.marks) != 1 || fx.marks[0].Before(before) {
		t.Errorf("marks = %v", fx.marks)
	}
	for _, id := range []string{"fresh", "seen", "clip", "nope", "refreshed"} {
		if !fx.seen[id] {
			t.Errorf("%s not marked seen", id)
		}
	}

	// The next pass has nothing to say, whatever the archive refreshes.
	fx.client.Videos["fresh"].DateDownloaded = time.Now().Unix()
	fx.srv.notifyOnce(t.Context())
	if len(fx.apns.sent()) != 2 {
		t.Errorf("a second pass announced the same video again")
	}
}

func TestNotifyDigestsSeveralAndOpensTheFeed(t *testing.T) {
	mark := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	fx := newNotifyFixture(t, mark)
	for i, id := range []string{"v1", "v2", "v3", "v4", "v5"} {
		fx.indexed(id, mark.Add(time.Duration(i+1)*time.Minute), false)
	}
	fx.srv.notifyOnce(t.Context())
	sent := fx.apns.sent()
	if len(sent) != 2 {
		t.Fatalf("sent %d, want 2", len(sent))
	}
	got := sent[0]
	if got.alert["title"] != "DevOps" || got.alert["subtitle"] != nil {
		t.Errorf("alert = %v", got.alert)
	}
	// Newest index first, three named, the rest counted.
	if got.alert["body"] != "5 new videos: Video v5, Video v4, Video v3 and 2 more" {
		t.Errorf("body = %q", got.alert["body"])
	}
	if _, has := got.payload["video"]; has {
		t.Error("a digest names one video to open")
	}
}

// A feed switched on is seeded before it speaks: everything its sources
// hold becomes known, nothing is announced, and only what arrives after
// that is news.
func TestNotifySeedsASwitchedOnFeed(t *testing.T) {
	fx := newNotifyFixture(t, time.Time{})
	fx.feed.NotifySeeded = false
	fx.feed.NotifiedAt = pgtype.Timestamptz{}
	for i := range 30 {
		fx.indexed(fmt.Sprintf("v%02d", i), time.Now().Add(-time.Duration(i)*time.Hour), false)
	}
	fx.srv.notifyOnce(t.Context())
	if len(fx.apns.sent()) != 0 {
		t.Error("seeding announced the archive")
	}
	if len(fx.seen) != 30 {
		t.Errorf("seeded %d videos, want every one", len(fx.seen))
	}
	if len(fx.seeded) != 1 || !fx.seeded[0] || len(fx.marks) != 1 {
		t.Errorf("seeded = %v, marks = %v", fx.seeded, fx.marks)
	}

	fx.indexed("new", time.Now(), false)
	fx.srv.notifyOnce(t.Context())
	sent := fx.apns.sent()
	if len(sent) != 2 || sent[0].alert["body"] != "Video new" {
		t.Errorf("the arrival after seeding was not announced: %+v", sent)
	}
}

// shiftingArchive is an archive whose pages move under a walk: on the first
// walk one video falls between two pages, the way a reindex sweep reorders
// a list sorted by download time. Every later walk is whole.
type shiftingArchive struct {
	ta.Client
	mu      sync.Mutex
	queries []ta.VideoQuery
}

func (a *shiftingArchive) ListVideos(ctx context.Context, q ta.VideoQuery) (*ta.VideoPage, error) {
	page, err := a.Client.ListVideos(ctx, q)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.queries = append(a.queries, q)
	if len(a.queries) == 1 && len(page.Data) > 1 {
		page.Data = page.Data[1:]
	}
	return page, nil
}

// A seed that missed a video announces it at its next refresh, so a walk is
// checked against the archive's own count and done again when it is short.
func TestNotifySeedWalksAgainWhenAPageShifted(t *testing.T) {
	fx := newNotifyFixture(t, time.Time{})
	fx.feed.NotifySeeded = false
	fx.feed.NotifiedAt = pgtype.Timestamptz{}
	for i := range 30 {
		fx.indexed(fmt.Sprintf("v%02d", i), time.Now().Add(-time.Duration(i)*time.Hour), false)
	}
	archive := &shiftingArchive{Client: fx.client}
	fx.srv.ta = archive
	fx.srv.notifyOnce(t.Context())
	if len(fx.seen) != 30 {
		t.Errorf("seeded %d videos, want every one", len(fx.seen))
	}
	if !fx.feed.NotifySeeded {
		t.Error("the feed was not marked seeded")
	}
	if len(archive.queries) < 2 {
		t.Errorf("a short walk was accepted: %d queries", len(archive.queries))
	}
	// The walk is in publish order, oldest first: a refresh does not move a
	// video there, and an arrival lands at the end.
	for _, q := range archive.queries {
		if q.Sort != "published" || q.Order != "asc" {
			t.Errorf("seed walk not in publish order: %+v", q)
		}
	}
}

// A token Apple has given up on is forgotten, and the pass still counts as
// delivered — the other device got it.
func TestNotifyForgetsDeadTokens(t *testing.T) {
	mark := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	fx := newNotifyFixture(t, mark)
	fx.apns.status["ipad"] = http.StatusGone
	fx.indexed("fresh", mark.Add(time.Minute), false)
	fx.srv.notifyOnce(t.Context())
	if len(fx.forgot) != 1 || fx.forgot[0] != "ipad" {
		t.Errorf("forgot = %v", fx.forgot)
	}
	if len(fx.marks) != 1 {
		t.Errorf("mark not advanced: %v", fx.marks)
	}
}

// Apple being down is not a reason to lose the news: nothing is marked
// seen, the mark stays, and the next pass tries again.
func TestNotifyRetriesAfterAnOutage(t *testing.T) {
	mark := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	fx := newNotifyFixture(t, mark)
	fx.apns.status["phone"] = http.StatusServiceUnavailable
	fx.apns.status["ipad"] = http.StatusServiceUnavailable
	fx.indexed("fresh", mark.Add(time.Minute), false)
	fx.srv.notifyOnce(t.Context())
	if len(fx.marks) != 0 || fx.seen["fresh"] {
		t.Errorf("a video nobody received was written off: marks=%v seen=%v", fx.marks, fx.seen)
	}
	if len(fx.forgot) != 0 {
		t.Errorf("an outage cost a registration: %v", fx.forgot)
	}
	delete(fx.apns.status, "phone")
	delete(fx.apns.status, "ipad")
	fx.srv.notifyOnce(t.Context())
	if len(fx.marks) != 1 || !fx.seen["fresh"] {
		t.Errorf("the retry did not deliver: marks = %v", fx.marks)
	}
}

// Nobody to tell still marks the news seen: a phone registered next week
// must not get this week's downloads as one burst.
func TestNotifyWithoutDevicesStillMarksSeen(t *testing.T) {
	mark := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	fx := newNotifyFixture(t, mark)
	fx.devices = nil
	fx.indexed("fresh", mark.Add(time.Minute), false)
	fx.srv.notifyOnce(t.Context())
	if len(fx.apns.sent()) != 0 {
		t.Error("sent to nobody")
	}
	if len(fx.marks) != 1 || !fx.seen["fresh"] {
		t.Errorf("marks = %v, seen = %v", fx.marks, fx.seen)
	}
}

// Without a push client the job never starts: nothing to send with.
func TestNotifierNeedsAClient(t *testing.T) {
	q := newEventStore().querier()
	q.ListNotifyFeedsFn = func(context.Context) ([]sqlc.Feed, error) {
		t.Error("the notifier ran on a server with no APNs client")
		return nil, nil
	}
	srv := newTestServer(ta.NewFake(), q)
	ctx, cancel := context.WithCancel(t.Context())
	srv.StartFeedNotifier(ctx)
	cancel()
}

func TestDigestWording(t *testing.T) {
	v := func(titles ...string) []VideoSummary {
		out := make([]VideoSummary, 0, len(titles))
		for _, t := range titles {
			out = append(out, VideoSummary{Title: t})
		}
		return out
	}
	cases := map[string]string{
		"2 new videos: A and B":             digest(v("A", "B")),
		"3 new videos: A, B and C":          digest(v("A", "B", "C")),
		"4 new videos: A, B, C and 1 more":  digest(v("A", "B", "C", "D")),
		"12 new videos: A, B, C and 9 more": digest(v("A", "B", "C", "D", "E", "F", "G", "H", "I", "J", "K", "L")),
		"1 new videos: A":                   digest(v("A")), // never shown: one video is announced by name
	}
	for want, got := range cases {
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}

// The flag on a feed round-trips through create and update, and a PUT that
// leaves it out keeps it — a client built before it existed cannot switch
// someone's notifications off with a full update.
func TestFeedNotifyFlag(t *testing.T) {
	client := ta.NewFake()
	client.AddVideo(video("a1", "A", "2026-08-01", 600, false))
	es := newEventStore()
	q := es.querier()
	var created sqlc.CreateFeedParams
	q.CreateFeedFn = func(_ context.Context, arg sqlc.CreateFeedParams) (sqlc.Feed, error) {
		created = arg
		return sqlc.Feed{ID: uuid.New(), Name: arg.Name, Sort: arg.Sort, Notify: arg.Notify}, nil
	}
	cur := sqlc.Feed{ID: uuid.New(), Name: "Home", Sort: "newest", Notify: true}
	var updated sqlc.UpdateFeedParams
	q.GetFeedFn = func(context.Context, sqlc.GetFeedParams) (sqlc.Feed, error) { return cur, nil }
	q.UpdateFeedFn = func(_ context.Context, arg sqlc.UpdateFeedParams) (sqlc.Feed, error) {
		updated = arg
		cur.Notify = arg.Notify
		return cur, nil
	}
	q.ListFeedChannelsFn = func(context.Context, uuid.UUID) ([]string, error) { return []string{"A"}, nil }
	q.ListFeedPlaylistsFn = func(context.Context, uuid.UUID) ([]string, error) { return nil, nil }
	q.NextFeedPositionFn = func(context.Context, uuid.UUID) (int32, error) { return 0, nil }
	q.DeleteFeedChannelsFn = func(context.Context, uuid.UUID) error { return nil }
	q.AddFeedChannelFn = func(context.Context, sqlc.AddFeedChannelParams) error { return nil }
	q.DeleteFeedPlaylistsFn = func(context.Context, uuid.UUID) error { return nil }
	h := newTestServer(client, q).Router()

	rec := do(t, h, http.MethodPost, "/api/v1/feeds", `{"name":"Maker","channel_ids":["A"],"notify":true}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	if !created.Notify || !decode[FeedDTO](t, rec).Notify {
		t.Error("notify was not stored or not reported on create")
	}

	rec = do(t, h, http.MethodPut, "/api/v1/feeds/"+cur.ID.String(), `{"name":"Home"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("update: %d %s", rec.Code, rec.Body.String())
	}
	if !updated.Notify {
		t.Error("a PUT without the field switched notifications off")
	}
	rec = do(t, h, http.MethodPut, "/api/v1/feeds/"+cur.ID.String(), `{"name":"Home","notify":false}`)
	if rec.Code != http.StatusOK || updated.Notify || decode[FeedDTO](t, rec).Notify {
		t.Errorf("explicit false did not stick: %d %s", rec.Code, rec.Body.String())
	}

	// Changing a notifying feed's sources re-seeds it: the new channel's
	// back catalogue is not news.
	cur.Notify = true
	var reseeds []bool
	q.SetFeedNotifySeededFn = func(_ context.Context, arg sqlc.SetFeedNotifySeededParams) error {
		reseeds = append(reseeds, arg.NotifySeeded)
		return nil
	}
	q.DeleteFeedChannelsFn = func(context.Context, uuid.UUID) error { return nil }
	q.AddFeedChannelFn = func(context.Context, sqlc.AddFeedChannelParams) error { return nil }
	if rec := do(t, h, http.MethodPut, "/api/v1/feeds/"+cur.ID.String(), `{"name":"Home","sort":"oldest"}`); rec.Code != http.StatusOK {
		t.Fatalf("update: %d", rec.Code)
	}
	if len(reseeds) != 0 {
		t.Error("an edit that left the sources alone re-seeded the feed")
	}
	if rec := do(t, h, http.MethodPut, "/api/v1/feeds/"+cur.ID.String(), `{"name":"Home","channel_ids":["A","B"]}`); rec.Code != http.StatusOK {
		t.Fatalf("update: %d", rec.Code)
	}
	if len(reseeds) != 1 || reseeds[0] {
		t.Errorf("reseeds = %v", reseeds)
	}
}
