package api

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/Seklfreak/flimm/internal/dearrow"
)

// Applying DeArrow to what a client sees.
//
// The server does this, not the clients: a title is what a video is *called*
// everywhere it is listed — feeds, search, history, up next, the player — and
// four clients each deciding when to substitute one is four chances to
// disagree with each other about what you are watching. It is the same rule
// the rendition ladder and the feed order follow.
//
// Nothing is looked up unless the viewer asked for it, and the lookup itself
// never names the video: `internal/dearrow` sends four characters of a hash.

// brandingWanted reports whether either preference asks for a lookup at all.
func brandingWanted(p Prefs) bool {
	return p.DeArrowTitles != dearrowOff || p.DeArrowThumbnails != dearrowOff
}

// applyBranding rewrites titles and thumbnail URLs in place for the videos a
// viewer is about to see.
//
// A failed lookup changes nothing: the archive's own title and thumbnail are
// the fallback for "the service is down", "nobody has submitted anything" and
// "the crowd voted to keep the original" alike — three different answers that
// happen to look the same on screen, which is the point.
func (s *Server) applyBranding(ctx context.Context, prefs Prefs, items []VideoSummary) {
	if s.dearrow == nil || !brandingWanted(prefs) || len(items) == 0 {
		return
	}
	// One lookup per video, in parallel and cached across the page — a prefix
	// answers for every video that shares it, so a second page of the same
	// channel usually costs nothing.
	branding := make([]dearrow.Branding, len(items))
	found := make([]bool, len(items))
	err := parallel(ctx, items, func(ctx context.Context, i int, item VideoSummary) error {
		b, err := s.dearrow.Branding(ctx, item.ID)
		if err != nil {
			return nil // a lookup failure is not this request's failure
		}
		branding[i], found[i] = b, true
		return nil
	})
	if err != nil {
		return
	}
	for i := range items {
		if !found[i] {
			continue
		}
		items[i].Title = brandedTitle(prefs.DeArrowTitles, items[i].Title, branding[i])
		items[i].ThumbURL = brandedThumbURL(prefs.DeArrowThumbnails, items[i], branding[i])
	}
}

// brandedTitle is the title to show.
//
//   - "manual" uses what a person submitted and the crowd voted on, and
//     nothing else.
//   - "all" also tidies the archive's own title when nobody has submitted one:
//     that is DeArrow's other half, and the only "generated" title there is.
//
// A crowd that voted for the original is obeyed in both: it is a decision, and
// tidying a title the crowd explicitly kept would be second-guessing it.
func brandedTitle(setting, original string, b dearrow.Branding) string {
	if setting == dearrowOff {
		return original
	}
	if b.Title != "" {
		return b.Title
	}
	if setting == dearrowAll && !b.OriginalTitleWon {
		return tidyTitle(original)
	}
	return original
}

// brandedThumbURL is the thumbnail to show: the frame the crowd picked, or —
// only when asked for "all" — the one DeArrow picked itself, at a fraction of
// the way through the video.
//
// Either way it is *this* server's URL for a frame of its own copy of the
// video: DeArrow returns a timestamp, not an image, so nothing is fetched from
// anyone and the thumbnail works with the archive offline.
func brandedThumbURL(setting string, item VideoSummary, b dearrow.Branding) string {
	if setting == dearrowOff || b.OriginalThumbnailWon {
		return item.ThumbURL
	}
	switch {
	case b.ThumbnailTime != nil:
		return frameURL(item.ID, *b.ThumbnailTime)
	case setting == dearrowAll && b.RandomTime > 0 && item.Duration > 0:
		return frameURL(item.ID, b.RandomTime*float64(item.Duration))
	}
	return item.ThumbURL
}

// frameURL addresses one still of a video. The time is in milliseconds so the
// path is an integer — a cache entry keyed by a float is a cache entry that
// misses on rounding.
func frameURL(id string, seconds float64) string {
	if seconds < 0 {
		seconds = 0
	}
	return "/media/frame/" + id + "/" + strconv.FormatInt(int64(seconds*1000), 10) + ".jpg"
}

// shoutyRun matches a run of capitals long enough to be shouting rather than
// an acronym: "HAPPENS" but not "USB" or "NASA".
var shoutyRun = regexp.MustCompile(`\p{Lu}{5,}`)

// shoutingRatio is how much of a title has to be capitals before it counts as
// shouted rather than as ordinary words with an acronym in them.
const shoutingRatio = 0.5

// tidyTitle is the generated half of DeArrow titles: the archive's own title
// with the shouting taken out.
//
// Deliberately conservative, and at the level of the *title* rather than the
// word: a title is only touched when it has a long run of capitals **and** is
// at least half capitals, which is what separates "WHY THIS KEEPS HAPPENING"
// from "How USB-C actually works". Inside a title that failed that test,
// nothing is changed at all — "Making a DOVETAIL jig by hand" is someone's
// emphasis, not shouting.
//
// Inside one that passed, every all-capital word is lowered, including short
// ones: an acronym in a title that is otherwise shouted cannot be told from a
// shouted "THE", and leaving the short words up produces "WHY THIS keeps
// happening", which is worse than either extreme.
func tidyTitle(title string) string {
	if !shoutyRun.MatchString(title) || !mostlyCapitals(title) {
		return title
	}
	words := strings.Fields(title)
	for i, w := range words {
		if isAllUpper(w) {
			words[i] = strings.ToLower(w)
		}
	}
	out := strings.Join(words, " ")
	// A sentence starts with a capital, and that is the only one this puts
	// back: a name in the middle of a shouted title is unrecoverable, and a
	// wrong guess at one reads worse than a lower-case word.
	for i, r := range out {
		return out[:i] + string(unicode.ToUpper(r)) + out[i+len(string(r)):]
	}
	return out
}

func mostlyCapitals(s string) bool {
	var letters, upper int
	for _, r := range s {
		if unicode.IsLetter(r) {
			letters++
			if unicode.IsUpper(r) {
				upper++
			}
		}
	}
	return letters > 0 && float64(upper) >= float64(letters)*shoutingRatio
}

func isAllUpper(w string) bool {
	upper := false
	for _, r := range w {
		if unicode.IsLower(r) {
			return false
		}
		if unicode.IsUpper(r) {
			upper = true
		}
	}
	return upper
}
