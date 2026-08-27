package api

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/Seklfreak/flimm/internal/db/sqlc"
	"github.com/Seklfreak/flimm/internal/sponsorblock"
	"github.com/Seklfreak/flimm/internal/ta"
)

// sponsorFixture wires a server whose SponsorBlock client talks to a stub
// service returning body (with status).
func sponsorFixture(t *testing.T, status int, body string) (*ta.Fake, *eventStore, http.Handler) {
	t.Helper()
	svc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(svc.Close)
	client := ta.NewFake()
	es := newEventStore()
	srv := NewServer(Options{
		Querier:      es.querier(),
		TA:           client,
		Log:          slog.New(slog.NewTextHandler(io.Discard, nil)),
		AppName:      "Flimm",
		MediaSecret:  testSecret,
		Sponsorblock: sponsorblock.New(sponsorblock.Options{BaseURL: svc.URL}),
	})
	return client, es, srv.Router()
}

// taSnapshot is the video TubeArchivist indexed, with one stale segment.
func taSnapshot() ta.Video {
	v := video("v1", "A", "2026-08-01", 1000, false)
	v.Sponsorblock.Segments = []ta.SponsorSegment{{Category: "sponsor", Segment: [2]float64{1, 2}}}
	return v
}

func TestVideoDetailPrefersTheLiveSegments(t *testing.T) {
	client, _, h := sponsorFixture(t, http.StatusOK, `[{"videoID":"v1","segments":[
	  {"category":"sponsor","actionType":"skip","segment":[12.5,45.5]},
	  {"category":"music_offtopic","actionType":"mute","segment":[60,70]},
	  {"category":"poi_highlight","actionType":"poi","segment":[100,100]},
	  {"category":"chapter","actionType":"chapter","segment":[0,90],"description":"Intro"},
	  {"category":"outro","actionType":"skip","segment":[990,1200]},
	  {"category":"sponsor","actionType":"skip","segment":[1500,1600]}
	]}]`)
	client.AddVideo(taSnapshot())

	rec := do(t, h, http.MethodGet, "/api/v1/videos/v1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	got := decode[VideoDetail](t, rec).Sponsorblock
	want := []SponsorSegment{
		// Chapter segments are not timeline tint; they reach clients through
		// GET /videos/{id}/chapters instead.
		{Category: "sponsor", ActionType: "skip", Start: 12.5, End: 45.5},
		{Category: "music_offtopic", ActionType: "mute", Start: 60, End: 70},
		{Category: "poi_highlight", ActionType: "poi", Start: 100, End: 100},
		// Submitted against a longer cut: clamped to this copy's duration.
		{Category: "outro", ActionType: "skip", Start: 990, End: 1000},
		// Starts past the end of this copy: dropped entirely.
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("sponsorblock = %+v\nwant %+v", got, want)
	}
}

func TestVideoDetailFallsBackToTheSnapshotWhenTheServiceFails(t *testing.T) {
	client, _, h := sponsorFixture(t, http.StatusInternalServerError, "boom")
	client.AddVideo(taSnapshot())

	rec := do(t, h, http.MethodGet, "/api/v1/videos/v1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	// TA stores no action type, so its segments are skips.
	want := []SponsorSegment{{Category: "sponsor", ActionType: "skip", Start: 1, End: 2}}
	if got := decode[VideoDetail](t, rec).Sponsorblock; !reflect.DeepEqual(got, want) {
		t.Errorf("sponsorblock = %+v, want %+v", got, want)
	}
}

func TestVideoDetailServiceAnswerOfNoneWinsOverTheSnapshot(t *testing.T) {
	// A segment that was removed or downvoted away must not come back from a
	// snapshot taken at download time.
	client, _, h := sponsorFixture(t, http.StatusNotFound, "")
	client.AddVideo(taSnapshot())

	rec := do(t, h, http.MethodGet, "/api/v1/videos/v1", "")
	if got := decode[VideoDetail](t, rec).Sponsorblock; len(got) != 0 {
		t.Errorf("sponsorblock = %+v, want none", got)
	}
	if rec.Body.String() == "" || !contains(rec.Body.String(), `"sponsorblock":[]`) {
		t.Errorf("empty segments must serialise as [], never null: %s", rec.Body.String())
	}
}

func TestVideoDetailWithoutAServiceUsesTheSnapshot(t *testing.T) {
	client := ta.NewFake()
	client.AddVideo(taSnapshot())
	es := newEventStore()
	h := newTestServer(client, es.querier()).Router()

	rec := do(t, h, http.MethodGet, "/api/v1/videos/v1", "")
	want := []SponsorSegment{{Category: "sponsor", ActionType: "skip", Start: 1, End: 2}}
	if got := decode[VideoDetail](t, rec).Sponsorblock; !reflect.DeepEqual(got, want) {
		t.Errorf("sponsorblock = %+v, want %+v", got, want)
	}
}

func TestChaptersFromSponsorBlock(t *testing.T) {
	client, _, h := sponsorFixture(t, http.StatusOK, `[{"videoID":"v1","segments":[
	  {"category":"chapter","actionType":"chapter","segment":[0,90],"description":"Intro"},
	  {"category":"chapter","actionType":"chapter","segment":[90,600],"description":"Build"},
	  {"category":"sponsor","actionType":"skip","segment":[12.5,45.5]}
	]}]`)
	v := taSnapshot()
	// A description the heuristic would happily parse: the crowd-sourced
	// names win over it, but not over chapters embedded in the file.
	v.Description = "0:00 Wrong\n5:00 Also wrong"
	client.AddVideo(v)

	rec := do(t, h, http.MethodGet, "/api/v1/videos/v1/chapters", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	want := ChaptersResponse{Source: "sponsorblock", Chapters: []Chapter{
		{Start: 0, End: 90, Title: "Intro"},
		{Start: 90, End: 1000, Title: "Build"},
	}}
	if got := decode[ChaptersResponse](t, rec); !reflect.DeepEqual(got, want) {
		t.Errorf("chapters = %+v, want %+v", got, want)
	}
}

func TestChaptersFallBackToTheDescriptionWithoutChapterSegments(t *testing.T) {
	client, _, h := sponsorFixture(t, http.StatusOK,
		`[{"videoID":"v1","segments":[{"category":"sponsor","actionType":"skip","segment":[12.5,45.5]}]}]`)
	v := taSnapshot()
	v.Description = "0:00 Intro\n5:00 Build"
	client.AddVideo(v)

	got := decode[ChaptersResponse](t, do(t, h, http.MethodGet, "/api/v1/videos/v1/chapters", ""))
	if got.Source != "description" || len(got.Chapters) != 2 {
		t.Errorf("chapters = %+v", got)
	}
}

func TestSponsorSegmentsWithoutDurationAreLeftAlone(t *testing.T) {
	// duration 0 means TA never reported one; clamping would drop everything.
	in := []sponsorblock.Segment{{Category: "sponsor", ActionType: "skip", Start: 10, End: 9999}}
	want := []SponsorSegment{{Category: "sponsor", ActionType: "skip", Start: 10, End: 9999}}
	if got := apiSponsorSegments(in, 0); !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestVideoDetailWatchEventUnaffected(t *testing.T) {
	// The lookup runs in its own goroutine beside the watch-state query; make
	// sure the detail still carries both.
	client, es, h := sponsorFixture(t, http.StatusOK,
		`[{"videoID":"v1","segments":[{"category":"sponsor","actionType":"skip","segment":[1,2]}]}]`)
	client.AddVideo(taSnapshot())
	es.events["v1"] = sqlc.WatchEvent{VideoID: "v1", Position: 250, Duration: 1000}

	d := decode[VideoDetail](t, do(t, h, http.MethodGet, "/api/v1/videos/v1", ""))
	if d.Position != 250 || len(d.Sponsorblock) != 1 {
		t.Errorf("detail position = %v, sponsorblock = %+v", d.Position, d.Sponsorblock)
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
