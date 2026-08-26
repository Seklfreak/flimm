package api

import (
	"slices"
	"testing"
)

func shuffleFixture(n int) []VideoSummary {
	out := make([]VideoSummary, 0, n)
	for i := range n {
		out = append(out, VideoSummary{ID: string(rune('a' + i))})
	}
	return out
}

// The same seed must always give the same order, or previous/next/autoplay
// would disagree with each other and with the list after a reload.
func TestShuffleBySeedIsDeterministic(t *testing.T) {
	a, b := shuffleFixture(12), shuffleFixture(12)
	shuffleBySeed(a, "seed-1")
	shuffleBySeed(b, "seed-1")
	if !slices.Equal(ids(a), ids(b)) {
		t.Errorf("same seed gave different orders:\n%v\n%v", ids(a), ids(b))
	}
}

func TestShuffleBySeedActuallyReorders(t *testing.T) {
	original := ids(shuffleFixture(12))
	got := shuffleFixture(12)
	shuffleBySeed(got, "seed-1")
	if slices.Equal(ids(got), original) {
		t.Errorf("shuffle left the list in its original order: %v", ids(got))
	}
	other := shuffleFixture(12)
	shuffleBySeed(other, "seed-2")
	if slices.Equal(ids(got), ids(other)) {
		t.Errorf("different seeds gave the same order: %v", ids(got))
	}
	// A permutation, not a rewrite: every item survives exactly once.
	sorted := ids(got)
	slices.Sort(sorted)
	if !slices.Equal(sorted, original) {
		t.Errorf("shuffle lost or duplicated items: %v", ids(got))
	}
}

// The whole reason for hashing per item rather than permuting positions:
// removing one item must not reshuffle the rest, so marking a video seen in a
// hide-seen feed doesn't scramble the running order.
func TestShuffleBySeedIsStableWhenItemsDisappear(t *testing.T) {
	full := shuffleFixture(12)
	shuffleBySeed(full, "seed-1")
	dropped := full[3].ID

	rest := shuffleFixture(12)
	rest = slices.DeleteFunc(rest, func(v VideoSummary) bool { return v.ID == dropped })
	shuffleBySeed(rest, "seed-1")

	want := slices.DeleteFunc(ids(full), func(id string) bool { return id == dropped })
	if !slices.Equal(ids(rest), want) {
		t.Errorf("dropping %q reordered the rest:\ngot  %v\nwant %v", dropped, ids(rest), want)
	}
}
