package api

import (
	"bytes"
	"encoding/binary"
	"net/http"
	"reflect"
	"testing"

	"github.com/Seklfreak/archive-client/internal/ta"
)

// ---- synthetic mp4 with a Nero chpl box ----
//
// The fixture sizes are small and known, so these narrowing conversions are
// safe by construction.

func u32(n int) uint32 { return uint32(n) } //nolint:gosec // test fixture sizes are small
func u8(n int) byte    { return byte(n) }   //nolint:gosec // test fixture sizes are small

func abox(typ string, parts ...[]byte) []byte {
	body := bytes.Join(parts, nil)
	out := binary.BigEndian.AppendUint32(nil, u32(8+len(body)))
	out = append(out, typ...)
	return append(out, body...)
}

type mark struct {
	start float64
	title string
}

// chplMP4 builds a faststart mp4 head carrying the given chapters in
// moov/udta/chpl, in the layout ffmpeg writes.
func chplMP4(marks ...mark) []byte {
	body := []byte{1, 0, 0, 0, 0, 0, 0, 0, u8(len(marks))}
	for _, m := range marks {
		body = binary.BigEndian.AppendUint64(body, uint64(int64(m.start*1e7))) //nolint:gosec // test fixture sizes are small
		body = append(body, u8(len(m.title)))
		body = append(body, m.title...)
	}
	return bytes.Join([][]byte{
		abox("ftyp", []byte("isom\x00\x00\x02\x00isomiso2")),
		abox("moov", abox("mvhd", make([]byte, 100)), abox("udta", abox("chpl", body))),
		abox("mdat", make([]byte, 32)),
	}, nil)
}

// ---- description parsing ----

func TestDescriptionChapters(t *testing.T) {
	tests := []struct {
		name       string
		desc       string
		duration   float64
		wantStarts []float64
		wantTitles []string
	}{
		{
			name: "leading timestamps",
			desc: `Everything you need to know about input shaping.

Chapters:
0:00 Intro
1:12 Why ringing happens
4:05 - Measuring the resonance
12:30 — Applying the result
1:02:03 Outro

Links: https://example.com`,
			duration:   4000,
			wantStarts: []float64{0, 72, 245, 750, 3723},
			wantTitles: []string{"Intro", "Why ringing happens", "Measuring the resonance", "Applying the result", "Outro"},
		},
		{
			name:       "timestamps after the title",
			desc:       "Intro 0:00\nThe build - 2:30\nWrap up 10:00",
			duration:   1200,
			wantStarts: []float64{0, 150, 600},
			wantTitles: []string{"Intro", "The build", "Wrap up"},
		},
		{
			name:       "bracketed timestamps",
			desc:       "(0:00) Intro\n[03:20] Deep dive\n(1:00:00) Conclusion",
			duration:   4000,
			wantStarts: []float64{0, 200, 3600},
			wantTitles: []string{"Intro", "Deep dive", "Conclusion"},
		},
		{
			name:       "colon separator and numbering",
			desc:       "0:00: 1. Getting started\n0:45: 2. The hard part",
			duration:   300,
			wantStarts: []float64{0, 45},
			wantTitles: []string{"1. Getting started", "2. The hard part"},
		},
		{
			name:       "list opens one second in",
			desc:       "0:01 Cold open\n5:00 Main",
			duration:   600,
			wantStarts: []float64{1, 300},
			wantTitles: []string{"Cold open", "Main"},
		},
		{
			name:       "a stray mention above the list is dropped",
			desc:       "As I explained at 4:32 in the last video, this is tricky.\n\n0:00 Recap\n2:00 New stuff",
			duration:   600,
			wantStarts: []float64{0, 120},
			wantTitles: []string{"Recap", "New stuff"},
		},
		{
			name:       "entries past the duration are dropped",
			desc:       "0:00 Intro\n1:00 Middle\n30:00 Bonus from another cut",
			duration:   300,
			wantStarts: []float64{0, 60},
			wantTitles: []string{"Intro", "Middle"},
		},
		{
			name:     "a single stray timestamp is not a chapter list",
			desc:     "Great tip from Dave at 4:32 — go watch his channel.\nThanks for watching!",
			duration: 600,
		},
		{
			name:     "two timestamps that never start at zero",
			desc:     "See 4:32 for the jig\nand 9:10 for the finish",
			duration: 600,
		},
		{
			name:     "non-increasing timestamps are rejected",
			desc:     "0:00 Intro\n5:00 Middle\n2:00 Wait, back again",
			duration: 600,
		},
		{
			name:     "timestamps without titles",
			desc:     "0:00\n1:00\n2:00",
			duration: 600,
		},
		{
			name:     "only one chapter",
			desc:     "0:00 The whole thing",
			duration: 600,
		},
		{
			name:     "empty description",
			desc:     "",
			duration: 600,
		},
		{
			name:     "prices and version numbers are not timestamps",
			desc:     "Costs 9:99 in some currency\nRatio 16:9 all the way",
			duration: 600,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := descriptionChapters(tc.desc, tc.duration)
			var gotStarts []float64
			var gotTitles []string
			for _, c := range got {
				gotStarts = append(gotStarts, c.Start)
				gotTitles = append(gotTitles, c.Title)
			}
			if !reflect.DeepEqual(gotStarts, tc.wantStarts) {
				t.Errorf("starts = %v, want %v", gotStarts, tc.wantStarts)
			}
			if !reflect.DeepEqual(gotTitles, tc.wantTitles) {
				t.Errorf("titles = %q, want %q", gotTitles, tc.wantTitles)
			}
		})
	}
}

func TestBuildChaptersEndsAndClamping(t *testing.T) {
	in := []ta.Chapter{
		{Start: 0, Title: " Intro "},
		{Start: 60, Title: "Body"},
		{Start: 60, Title: "Duplicate start"},
		{Start: 90, Title: "  "},
		{Start: 120, Title: "Outro"},
		{Start: 900, Title: "Past the end"},
		{Start: -5, Title: "Negative"},
	}
	got := buildChapters(in, 200)
	want := []Chapter{
		{Start: 0, End: 60, Title: "Intro"},
		{Start: 60, End: 120, Title: "Body"},
		{Start: 120, End: 200, Title: "Outro"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("buildChapters = %+v, want %+v", got, want)
	}
	if got := buildChapters(nil, 200); len(got) != 0 {
		t.Errorf("empty input = %+v", got)
	}
}

// ---- handler ----

func chaptersFixture(t *testing.T) (*ta.Fake, http.Handler) {
	t.Helper()
	client := ta.NewFake()
	es := newEventStore()
	return client, newTestServer(client, es.querier()).Router()
}

func TestChaptersEmbedded(t *testing.T) {
	client, h := chaptersFixture(t)
	v := video("v1", "A", "2026-08-01", 300, false)
	v.Description = "0:00 Not this one\n1:00 Nor this"
	client.AddVideo(v)
	client.Media["/media/A/v1.mp4"] = chplMP4(mark{0, "Intro"}, mark{132.5, "Build"}, mark{240, "Outro"})

	rec := do(t, h, http.MethodGet, "/api/v1/videos/v1/chapters", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	got := decode[ChaptersResponse](t, rec)
	want := ChaptersResponse{Source: "embedded", Chapters: []Chapter{
		{Start: 0, End: 132.5, Title: "Intro"},
		{Start: 132.5, End: 240, Title: "Build"},
		{Start: 240, End: 300, Title: "Outro"},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}

	// Cached: a second request must not read the media file again.
	before := len(client.Calls)
	if rec := do(t, h, http.MethodGet, "/api/v1/videos/v1/chapters", ""); rec.Code != http.StatusOK {
		t.Fatalf("second status = %d", rec.Code)
	}
	if len(client.Calls) != before {
		t.Errorf("second request hit TA again: %v", client.Calls[before:])
	}
}

func TestChaptersRefetchesWhenMoovOverrunsTheFirstRead(t *testing.T) {
	client, h := chaptersFixture(t)
	client.AddVideo(video("v1", "A", "2026-08-01", 300, false))
	file := chplMP4(mark{0, "Intro"}, mark{100, "Outro"})

	var ranges [][2]int64
	client.FetchRangeFn = func(_ string, start, end int64) ([]byte, error) {
		ranges = append(ranges, [2]int64{start, end})
		to := min(end+1, int64(len(file)))
		if len(ranges) == 1 {
			// Simulate a head read that stops inside moov.
			to = min(to, 40)
		}
		return file[start:to], nil
	}

	rec := do(t, h, http.MethodGet, "/api/v1/videos/v1/chapters", "")
	got := decode[ChaptersResponse](t, rec)
	if got.Source != "embedded" || len(got.Chapters) != 2 {
		t.Fatalf("got %+v", got)
	}
	if len(ranges) != 2 {
		t.Fatalf("ranges = %v, want two reads", ranges)
	}
	if ranges[0] != [2]int64{0, chaptersHeadBytes - 1} {
		t.Errorf("first range = %v", ranges[0])
	}
	if ranges[1][0] != 0 || ranges[1][1] >= chaptersHeadBytes-1 {
		t.Errorf("second range = %v, want a smaller targeted read", ranges[1])
	}
}

func TestChaptersFromDescription(t *testing.T) {
	client, h := chaptersFixture(t)
	v := video("v1", "A", "2026-08-01", 600, false)
	v.Description = "Timestamps:\n0:00 Intro\n2:00 Middle\n8:00 Outro"
	client.AddVideo(v)
	// No media bytes seeded: the embedded read misses and we fall back.

	rec := do(t, h, http.MethodGet, "/api/v1/videos/v1/chapters", "")
	got := decode[ChaptersResponse](t, rec)
	want := ChaptersResponse{Source: "description", Chapters: []Chapter{
		{Start: 0, End: 120, Title: "Intro"},
		{Start: 120, End: 480, Title: "Middle"},
		{Start: 480, End: 600, Title: "Outro"},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestChaptersNone(t *testing.T) {
	client, h := chaptersFixture(t)
	v := video("v1", "A", "2026-08-01", 600, false)
	v.Description = "Thanks for watching! Merch at https://example.com"
	client.AddVideo(v)
	client.Media["/media/A/v1.mp4"] = chplMP4() // a file without chapters

	rec := do(t, h, http.MethodGet, "/api/v1/videos/v1/chapters", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	got := decode[ChaptersResponse](t, rec)
	if got.Source != "none" {
		t.Errorf("source = %q, want none", got.Source)
	}
	if got.Chapters == nil || len(got.Chapters) != 0 {
		t.Errorf("chapters = %+v, want an empty list (never null)", got.Chapters)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"chapters":[]`)) {
		t.Errorf("body = %s, want an empty JSON array", rec.Body.String())
	}
}

func TestChaptersUnknownVideoIs404(t *testing.T) {
	_, h := chaptersFixture(t)
	if rec := do(t, h, http.MethodGet, "/api/v1/videos/nope/chapters", ""); rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestChaptersCacheEviction(t *testing.T) {
	c := &chaptersCache{ttl: chaptersTTL, max: 3, m: map[string]chaptersEntry{}}
	for _, id := range []string{"a", "b", "c", "d"} {
		c.put(id, ChaptersResponse{Source: "none", Chapters: []Chapter{}})
	}
	if len(c.m) > 3 {
		t.Errorf("cache grew to %d entries past its bound", len(c.m))
	}
	if _, ok := c.get("d"); !ok {
		t.Error("the newest entry should still be cached")
	}
	if _, ok := c.get("missing"); ok {
		t.Error("unknown id reported as cached")
	}
}
