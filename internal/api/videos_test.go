package api

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Seklfreak/archive-client/internal/db/sqlc"
	"github.com/Seklfreak/archive-client/internal/ta"
)

var errNoRows = pgx.ErrNoRows

func progressFixture() (*ta.Fake, *eventStore, http.Handler) {
	client := ta.NewFake()
	client.AddVideo(video("v1", "A", "2026-08-01", 1000, false))
	client.AddVideo(video("v2", "A", "2026-08-02", 100, false))
	es := newEventStore()
	return client, es, newTestServer(client, es.querier()).Router()
}

func TestProgressHeartbeatBelowThreshold(t *testing.T) {
	client, es, h := progressFixture()
	rec := do(t, h, http.MethodPost, "/api/v1/videos/v1/progress", `{"position": 400}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	got := decode[map[string]any](t, rec)
	if got["position"] != 400.0 || got["watched"] != false {
		t.Errorf("resp = %v", got)
	}
	if client.Progress["v1"] != 400 {
		t.Errorf("TA progress = %v", client.Progress["v1"])
	}
	if client.Videos["v1"].Player.Watched {
		t.Error("marked watched below threshold")
	}
	if ev := es.events["v1"]; ev.Position != 400 || ev.Duration != 1000 || ev.CompletedAt.Valid || ev.Title != "Video v1" || ev.ChannelID != "A" {
		t.Errorf("event = %+v", ev)
	}
}

func TestProgressHeartbeatAt90PercentMarksWatched(t *testing.T) {
	client, es, h := progressFixture()
	rec := do(t, h, http.MethodPost, "/api/v1/videos/v1/progress", `{"position": 900}`)
	got := decode[map[string]any](t, rec)
	if got["watched"] != true {
		t.Errorf("resp = %v", got)
	}
	if !client.Videos["v1"].Player.Watched || !es.events["v1"].CompletedAt.Valid {
		t.Error("expected watched in TA and Archive")
	}
	if !reflect.DeepEqual(client.Calls, []string{"progress:v1", "watched:v1"}) {
		t.Errorf("calls = %v", client.Calls)
	}
}

func TestProgressHeartbeatLast30SecondsMarksWatched(t *testing.T) {
	// v2 is 100 s: 75 s is only 75 % but leaves 25 s → watched.
	client, _, h := progressFixture()
	rec := do(t, h, http.MethodPost, "/api/v1/videos/v2/progress", `{"position": 75}`)
	if got := decode[map[string]any](t, rec); got["watched"] != true {
		t.Errorf("resp = %v", got)
	}
	if !client.Videos["v2"].Player.Watched {
		t.Error("expected watched")
	}
	// 60 s of 100 s stays in progress.
	rec = do(t, h, http.MethodPost, "/api/v1/videos/v1/progress", `{"position": 60}`)
	if got := decode[map[string]any](t, rec); got["watched"] != false {
		t.Errorf("v1 resp = %v", got)
	}
}

func TestProgressRejectsBadBodyAndUnknownVideo(t *testing.T) {
	_, _, h := progressFixture()
	if rec := do(t, h, http.MethodPost, "/api/v1/videos/v1/progress", `{"position": -1}`); rec.Code != http.StatusBadRequest {
		t.Errorf("negative: %d", rec.Code)
	}
	if rec := do(t, h, http.MethodPost, "/api/v1/videos/nope/progress", `{"position": 1}`); rec.Code != http.StatusNotFound {
		t.Errorf("unknown: %d", rec.Code)
	}
}

func TestWatchedToggleAndStartOver(t *testing.T) {
	client, es, h := progressFixture()
	do(t, h, http.MethodPost, "/api/v1/videos/v1/progress", `{"position": 400}`)

	rec := do(t, h, http.MethodPost, "/api/v1/videos/v1/watched", `{"watched": true}`)
	if rec.Code != http.StatusOK || !client.Videos["v1"].Player.Watched || !es.events["v1"].CompletedAt.Valid {
		t.Fatalf("watched=true: %d %s ta=%v", rec.Code, rec.Body.String(), client.Videos["v1"].Player.Watched)
	}
	if es.events["v1"].Position != 400 {
		t.Errorf("marking watched should keep position, got %v", es.events["v1"].Position)
	}

	rec = do(t, h, http.MethodPost, "/api/v1/videos/v1/watched", `{"watched": false}`)
	if rec.Code != http.StatusOK || client.Videos["v1"].Player.Watched || es.events["v1"].CompletedAt.Valid || es.events["v1"].Position != 0 {
		t.Fatalf("watched=false: %d ev=%+v", rec.Code, es.events["v1"])
	}
	if _, ok := client.Progress["v1"]; ok {
		t.Error("TA progress should be deleted on unwatch")
	}

	do(t, h, http.MethodPost, "/api/v1/videos/v1/progress", `{"position": 950}`)
	rec = do(t, h, http.MethodDelete, "/api/v1/videos/v1/progress", "")
	if rec.Code != http.StatusNoContent || es.events["v1"].Position != 0 || !es.events["v1"].CompletedAt.Valid {
		t.Errorf("start over: %d ev=%+v", rec.Code, es.events["v1"])
	}
	if _, ok := client.Progress["v1"]; ok {
		t.Error("TA progress should be deleted on start over")
	}
	if rec := do(t, h, http.MethodPost, "/api/v1/videos/v1/watched", `{}`); rec.Code != http.StatusBadRequest {
		t.Errorf("missing watched: %d", rec.Code)
	}
}

func TestVideoDetail(t *testing.T) {
	client := ta.NewFake()
	v := video("v1", "A", "2026-08-01", 1000, false)
	v.Subtitles = []ta.Subtitle{{Lang: "en", Source: "auto", MediaURL: "A/v1.en.vtt"}}
	v.Sponsorblock.Segments = []ta.SponsorSegment{{Category: "sponsor", Segment: [2]float64{12.3, 45.6}}}
	v.Streams = []ta.Stream{{Type: "video", Height: 1080}}
	v.Playlist = []string{"PL1"}
	client.AddVideo(v)
	client.Playlists["PL1"] = &ta.Playlist{PlaylistID: "PL1", PlaylistName: "Series", PlaylistType: "regular", PlaylistEntries: []ta.PlaylistEntry{{YoutubeID: "x"}, {YoutubeID: "v1"}}}
	es := newEventStore()
	es.events["v1"] = sqlc.WatchEvent{VideoID: "v1", Position: 250, Duration: 1000, LastPlayedAt: pgtype.Timestamptz{Valid: true}}
	h := newTestServer(client, es.querier()).Router()

	rec := do(t, h, http.MethodGet, "/api/v1/videos/v1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	d := decode[VideoDetail](t, rec)
	if d.MediaURL != "/media/video/v1.mp4" || d.Height != 1080 || d.Progress != 0.25 || !d.HasAutoSubtitles {
		t.Errorf("detail = %+v", d)
	}
	if len(d.Subtitles) != 1 || d.Subtitles[0].URL != "/media/subtitles/v1/en.vtt" {
		t.Errorf("subtitles = %+v", d.Subtitles)
	}
	if len(d.Sponsorblock) != 1 || d.Sponsorblock[0].End != 45.6 {
		t.Errorf("sponsorblock = %+v", d.Sponsorblock)
	}
	if len(d.Playlists) != 1 || d.Playlists[0].Position != 1 || d.Playlists[0].Count != 2 {
		t.Errorf("playlists = %+v", d.Playlists)
	}
	if d.Channel.ID != "A" || d.Channel.VideoCount != 1 {
		t.Errorf("channel = %+v", d.Channel)
	}
	if rec := do(t, h, http.MethodGet, "/api/v1/videos/missing", ""); rec.Code != http.StatusNotFound {
		t.Errorf("missing: %d", rec.Code)
	}
}

func TestTAUnavailableIs502(t *testing.T) {
	client := ta.NewFake()
	client.Err = errors.Join(ta.ErrUnavailable, errors.New("dial tcp: refused"))
	h := newTestServer(client, newEventStore().querier()).Router()
	rec := do(t, h, http.MethodGet, "/api/v1/videos/v1", "")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := decode[map[string]string](t, rec); got["error"] != "tubearchivist unavailable" {
		t.Errorf("body = %v", got)
	}
}

func TestUpNextInFeedAndFallback(t *testing.T) {
	feed := sqlc.Feed{ID: uuid.New(), Name: "Home", Sort: "newest", HideSeen: true}
	client, _, h := feedFixture(t, feed, []string{"A", "B"})
	client.SimilarFn = func(string) ([]ta.Video, error) { return []ta.Video{*client.Videos["c1"]}, nil }

	// unseen, newest: b1 (08-02), a1 (08-01). After b1 → a1.
	rec := do(t, h, http.MethodGet, "/api/v1/videos/b1/up-next?feed="+feed.ID.String(), "")
	if got := ids(decode[[]VideoSummary](t, rec)); !reflect.DeepEqual(got, []string{"a1"}) {
		t.Errorf("up-next = %v", got)
	}
	// After the last one nothing is left → similar.
	rec = do(t, h, http.MethodGet, "/api/v1/videos/a1/up-next?feed="+feed.ID.String(), "")
	if got := ids(decode[[]VideoSummary](t, rec)); !reflect.DeepEqual(got, []string{"c1"}) {
		t.Errorf("fallback = %v", got)
	}
	if rec := do(t, h, http.MethodGet, "/api/v1/videos/a1/up-next?feed="+uuid.NewString(), ""); rec.Code != http.StatusNotFound {
		t.Errorf("unknown feed: %d", rec.Code)
	}
}

func TestPrefsPatch(t *testing.T) {
	client := ta.NewFake()
	var stored []byte
	q := newEventStore().querier()
	q.GetPrefsFn = func(context.Context, uuid.UUID) ([]byte, error) {
		if stored == nil {
			return nil, pgx.ErrNoRows
		}
		return stored, nil
	}
	q.UpsertPrefsFn = func(_ context.Context, arg sqlc.UpsertPrefsParams) error { stored = arg.Prefs; return nil }
	h := newTestServer(client, q).Router()

	rec := do(t, h, http.MethodPatch, "/api/v1/me/prefs", `{"theme":"dark","subtitle_lang":"en","playback_speed":1.5}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	p := decode[Prefs](t, rec)
	if p.Theme != "dark" || p.SubtitleLang == nil || *p.SubtitleLang != "en" || p.PlaybackSpeed != 1.5 || !p.Autoplay {
		t.Errorf("prefs = %+v", p)
	}
	if rec := do(t, h, http.MethodPatch, "/api/v1/me/prefs", `{"theme":"neon"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("invalid theme: %d", rec.Code)
	}
	if rec := do(t, h, http.MethodPatch, "/api/v1/me/prefs", `{"bogus":1}`); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown key: %d", rec.Code)
	}
	rec = do(t, h, http.MethodGet, "/api/v1/me", "")
	me := decode[map[string]any](t, rec)
	if me["id"] != DevUserID.String() || me["prefs"].(map[string]any)["theme"] != "dark" {
		t.Errorf("me = %v", me)
	}
}

func TestConfigIsPublic(t *testing.T) {
	h := newTestServer(ta.NewFake(), newEventStore().querier()).Router()
	rec := do(t, h, http.MethodGet, "/api/v1/config", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := decode[map[string]string](t, rec); got["app_name"] != "Archive" || got["version"] == "" {
		t.Errorf("config = %v", got)
	}
}
