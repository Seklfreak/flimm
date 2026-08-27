package media

import (
	"reflect"
	"testing"
)

// The planner is the whole resume behaviour in one pure function, so this is
// where the interesting sequences live: a viewer resuming in the middle, then
// seeking somewhere else, then the job finishing off the leftovers.
func TestPlanNextRun(t *testing.T) {
	for _, tc := range []struct {
		name      string
		produced  []segRange
		requested int
		n         int
		want      segRange
		wantOK    bool
	}{
		{
			name: "nothing produced and nothing asked for: start at the beginning",
			n:    150, requested: -1,
			want: segRange{0, 150}, wantOK: true,
		},
		{
			name: "from=0 is the single run from the start, as before",
			n:    150, requested: 0,
			want: segRange{0, 150}, wantOK: true,
		},
		{
			name: "resuming at 40:00 encodes from there to the end — run A",
			n:    630, requested: 600,
			want: segRange{600, 630}, wantOK: true,
		},
		{
			name: "the part before the resume point is what is left — run B",
			// After run A: [600,630) exists, and the viewer is still at 600.
			produced: []segRange{{600, 630}}, requested: 600, n: 630,
			want: segRange{0, 600}, wantOK: true,
		},
		{
			name:     "and then there is nothing left",
			produced: []segRange{{0, 630}}, requested: 600, n: 630,
			wantOK: false,
		},
		{
			name: "a seek ahead into a gap starts at the seek, not at the gap",
			// Run A got as far as 620 before the viewer jumped to 700.
			produced: []segRange{{600, 620}}, requested: 700, n: 900,
			want: segRange{700, 900}, wantOK: true,
		},
		{
			name:     "a seek into a produced stretch falls back to the earliest gap",
			produced: []segRange{{0, 100}, {600, 630}}, requested: 610, n: 900,
			want: segRange{100, 600}, wantOK: true,
		},
		{
			name:     "several gaps: the earliest wins when nothing is asked for",
			produced: []segRange{{100, 200}, {400, 500}}, requested: -1, n: 600,
			want: segRange{0, 100}, wantOK: true,
		},
		{
			name:     "several gaps: the one holding the request wins over the earliest",
			produced: []segRange{{100, 200}, {400, 500}}, requested: 450, n: 600,
			// 450 is produced, so the earliest gap it is.
			want: segRange{0, 100}, wantOK: true,
		},
		{
			name:     "several gaps: the request lands in the last one",
			produced: []segRange{{100, 200}, {400, 500}}, requested: 550, n: 600,
			want: segRange{550, 600}, wantOK: true,
		},
		{
			name:     "a run ends at the gap, not at the end of the video",
			produced: []segRange{{300, 400}}, requested: 100, n: 400,
			want: segRange{100, 300}, wantOK: true,
		},
		{
			name: "a request past the end is ignored",
			n:    10, requested: 99,
			want: segRange{0, 10}, wantOK: true,
		},
		{
			name: "a video with no segments has nothing to plan",
			n:    0, requested: -1,
			wantOK: false,
		},
	} {
		got, ok := planNextRun(tc.produced, tc.requested, tc.n)
		if ok != tc.wantOK {
			t.Errorf("%s: ok = %v, want %v", tc.name, ok, tc.wantOK)
			continue
		}
		if ok && got != tc.want {
			t.Errorf("%s: run = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// A whole resume-first job, planned end to end: run A from the resume point,
// run B for the rest, done.
func TestPlanNextRunResumeSequence(t *testing.T) {
	const n = 630
	produced := []segRange{}
	requested := 600

	first, ok := planNextRun(produced, requested, n)
	if !ok || first != (segRange{600, 630}) {
		t.Fatalf("run A = %v (%v), want [600,630)", first, ok)
	}
	produced = append(produced, first)

	second, ok := planNextRun(produced, requested, n)
	if !ok || second != (segRange{0, 600}) {
		t.Fatalf("run B = %v (%v), want [0,600)", second, ok)
	}
	produced = append(produced, second)

	if _, ok := planNextRun(produced, requested, n); ok {
		t.Error("a rendition with every segment still plans a run")
	}
}

func TestRangesFromSet(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  []int
		n    int
		want []segRange
	}{
		{"empty", nil, 10, nil},
		{"one", []int{3}, 10, []segRange{{3, 4}}},
		{"contiguous coalesce", []int{0, 1, 2, 5, 6}, 10, []segRange{{0, 3}, {5, 7}}},
		{"unordered", []int{6, 0, 5, 2, 1}, 10, []segRange{{0, 3}, {5, 7}}},
		{"outside the rendition is dropped", []int{-1, 3, 99}, 10, []segRange{{3, 4}}},
	} {
		set := map[int]bool{}
		for _, i := range tc.set {
			set[i] = true
		}
		if got := rangesFromSet(set, tc.n); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: rangesFromSet = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestGapsIn(t *testing.T) {
	for _, tc := range []struct {
		name     string
		produced []segRange
		n        int
		want     []segRange
	}{
		{"nothing produced", nil, 5, []segRange{{0, 5}}},
		{"everything produced", []segRange{{0, 5}}, 5, nil},
		{"a hole in the middle", []segRange{{0, 2}, {4, 5}}, 5, []segRange{{2, 4}}},
		{"a tail", []segRange{{0, 3}}, 5, []segRange{{3, 5}}},
		{"a head", []segRange{{2, 5}}, 5, []segRange{{0, 2}}},
		{"overlapping ranges are one", []segRange{{0, 3}, {2, 4}}, 5, []segRange{{4, 5}}},
		{"touching ranges are one", []segRange{{0, 2}, {2, 4}}, 5, []segRange{{4, 5}}},
		{"out-of-order input", []segRange{{4, 5}, {0, 2}}, 5, []segRange{{2, 4}}},
	} {
		if got := gapsIn(tc.produced, tc.n); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: gapsIn = %v, want %v", tc.name, got, tc.want)
		}
	}
}
