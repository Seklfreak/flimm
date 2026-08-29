package api

import (
	"testing"

	"github.com/Seklfreak/flimm/internal/dearrow"
)

func at(seconds float64) *float64 { return &seconds }

// The two halves are set apart, because they are apart: a viewer can trust the
// crowd's words and not its frames.
func TestTitlesAndThumbnailsAreDecidedSeparately(t *testing.T) {
	b := dearrow.Branding{Title: "What it is actually about", ThumbnailTime: at(12), RandomTime: 0.5}
	item := VideoSummary{ID: "v1", Title: "WATCH THIS NOW", ThumbURL: "/media/thumb/video/v1", Duration: 100}

	if got := brandedTitle(dearrowManual, item.Title, b); got != "What it is actually about" {
		t.Errorf("title = %q", got)
	}
	if got := brandedTitle(dearrowOff, item.Title, b); got != "WATCH THIS NOW" {
		t.Errorf("titles off = %q, want the archive's own", got)
	}
	if got := brandedThumbURL(dearrowManual, item, b); got != "/media/frame/v1/12000.jpg" {
		t.Errorf("thumb = %q", got)
	}
	if got := brandedThumbURL(dearrowOff, item, b); got != item.ThumbURL {
		t.Errorf("thumbnails off = %q, want the archive's own", got)
	}
}

// "manual" is what a person submitted; "all" also takes what DeArrow generates
// when nobody has. That distinction is the whole point of the two settings.
func TestManualTakesSubmissionsOnlyAndAllTakesTheGeneratedOnesToo(t *testing.T) {
	nothingSubmitted := dearrow.Branding{RandomTime: 0.5}
	item := VideoSummary{ID: "v1", Title: "WHY THIS KEEPS HAPPENING", ThumbURL: "/media/thumb/video/v1", Duration: 100}

	// Manual: nobody submitted anything, so nothing changes.
	if got := brandedTitle(dearrowManual, item.Title, nothingSubmitted); got != item.Title {
		t.Errorf("manual title = %q, want the archive's own", got)
	}
	if got := brandedThumbURL(dearrowManual, item, nothingSubmitted); got != item.ThumbURL {
		t.Errorf("manual thumb = %q, want the archive's own", got)
	}

	// All: the title loses its shouting, and the frame DeArrow suggests is
	// half way through a hundred seconds.
	if got := brandedTitle(dearrowAll, item.Title, nothingSubmitted); got != "Why this keeps happening" {
		t.Errorf("all title = %q", got)
	}
	if got := brandedThumbURL(dearrowAll, item, nothingSubmitted); got != "/media/frame/v1/50000.jpg" {
		t.Errorf("all thumb = %q", got)
	}
}

// A crowd that voted to keep the original said something, and it is not
// "nobody has looked at this".
func TestAVoteForTheOriginalIsObeyedByBothSettings(t *testing.T) {
	b := dearrow.Branding{OriginalTitleWon: true, OriginalThumbnailWon: true, RandomTime: 0.5}
	item := VideoSummary{ID: "v1", Title: "STOP DOING THIS", ThumbURL: "/media/thumb/video/v1", Duration: 100}

	for _, setting := range []string{dearrowManual, dearrowAll} {
		if got := brandedTitle(setting, item.Title, b); got != item.Title {
			t.Errorf("%s title = %q, want the archive's own", setting, got)
		}
		if got := brandedThumbURL(setting, item, b); got != item.ThumbURL {
			t.Errorf("%s thumb = %q, want the archive's own", setting, got)
		}
	}
}

// The generated title only takes the shouting out, and only when a title is
// actually shouting. Everything else is the uploader's words.
func TestTidyTitleIsConservative(t *testing.T) {
	cases := map[string]string{
		"WHY THIS KEEPS HAPPENING":            "Why this keeps happening",
		"I BOUGHT THE CHEAPEST LATHE":         "I bought the cheapest lathe",
		"A perfectly ordinary title":          "A perfectly ordinary title",
		"How USB-C actually works":            "How USB-C actually works",
		"NASA lands a probe":                  "NASA lands a probe",
		"Making a DOVETAIL jig by hand":       "Making a DOVETAIL jig by hand",
		"THE END (of my patience) WITH THESE": "The end (of my patience) with these",
	}
	for in, want := range cases {
		if got := tidyTitle(in); got != want {
			t.Errorf("tidyTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

// A frame is addressed in milliseconds, so two viewers asking for the same
// timestamp share one cache entry rather than missing on rounding.
func TestFrameURLIsKeyedInMilliseconds(t *testing.T) {
	if got := frameURL("v1", 3.92349); got != "/media/frame/v1/3923.jpg" {
		t.Errorf("frameURL = %q", got)
	}
	if got := frameURL("v1", -5); got != "/media/frame/v1/0.jpg" {
		t.Errorf("a negative timestamp = %q, want the start", got)
	}
}

// Nothing is looked up for a viewer who asked for neither.
func TestNoLookupWhenBothAreOff(t *testing.T) {
	if brandingWanted(defaultPrefs()) {
		t.Error("the defaults must not ask a third party anything")
	}
	if !brandingWanted(Prefs{DeArrowTitles: dearrowManual, DeArrowThumbnails: dearrowOff}) {
		t.Error("titles alone should still be looked up")
	}
	if !brandingWanted(Prefs{DeArrowTitles: dearrowOff, DeArrowThumbnails: dearrowAll}) {
		t.Error("thumbnails alone should still be looked up")
	}
}
