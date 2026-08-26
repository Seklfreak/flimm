package api

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Seklfreak/archive-client/internal/ta"
)

const (
	sourceEmbedded    = "embedded"
	sourceDescription = "description"
	sourceNone        = "none"

	// chaptersHeadBytes is the first range read of a media file. These files
	// are faststart and moov runs to a few MB at most, so one read is enough
	// in practice.
	chaptersHeadBytes = 5 << 20
	// chaptersHeadMax bounds a second read when moov turned out to be bigger
	// (it must stay within the TA client's own read cap).
	chaptersHeadMax = 16 << 20

	// chaptersTTL is long because chapters are baked into a downloaded file
	// and never change; the cache only ever needs to survive a restart-free
	// browsing session.
	chaptersTTL = 12 * time.Hour
	// chaptersCacheMax bounds the cache so a crawl over a big archive cannot
	// grow it without end.
	chaptersCacheMax = 1024
)

// getChapters answers GET /videos/{id}/chapters: markers embedded in the
// media file, else parsed out of the description, else none.
func (s *Server) getChapters(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if out, ok := s.chapters.get(id); ok {
		writeJSON(w, http.StatusOK, out)
		return
	}
	v, err := s.ta.GetVideo(r.Context(), id)
	if err != nil {
		s.writeTAError(w, "get video", err)
		return
	}
	duration := v.Player.Duration
	source := sourceEmbedded
	found := s.embeddedChapters(r.Context(), v)
	if len(found) == 0 {
		source = sourceDescription
		found = descriptionChapters(v.Description, duration)
	}
	out := ChaptersResponse{Chapters: buildChapters(found, duration)}
	out.Source = source
	if len(out.Chapters) == 0 {
		out.Source = sourceNone
	}
	s.chapters.put(id, out)
	writeJSON(w, http.StatusOK, out)
}

// embeddedChapters range-fetches the head of the media file and parses the
// container's chapter boxes. Every failure is a miss, not an error: the
// caller falls back to the description.
func (s *Server) embeddedChapters(ctx context.Context, v *ta.Video) []ta.Chapter {
	if v.MediaURL == "" {
		return nil
	}
	path := taMediaPath(v.MediaURL)
	head, err := s.ta.FetchRange(ctx, path, 0, chaptersHeadBytes-1)
	if err != nil {
		s.log.Debug("chapters: fetch media head", "video", v.YoutubeID, "err", err)
		return nil
	}
	chs, err := ta.ChaptersFromMP4Head(head)
	var short *ta.ShortHeadError
	if errors.As(err, &short) && short.Need > int64(len(head)) && short.Need <= chaptersHeadMax {
		head, err = s.ta.FetchRange(ctx, path, 0, short.Need-1)
		if err != nil {
			s.log.Debug("chapters: refetch media head", "video", v.YoutubeID, "err", err)
			return nil
		}
		chs, err = ta.ChaptersFromMP4Head(head)
	}
	if err != nil {
		if !errors.Is(err, ta.ErrNoChapters) {
			s.log.Debug("chapters: parse media head", "video", v.YoutubeID, "err", err)
		}
		return nil
	}
	return chs
}

// buildChapters turns parsed markers into the API shape: sorted, trimmed,
// clamped to the video duration, with each end at the next start and the last
// at the duration.
func buildChapters(in []ta.Chapter, duration float64) []Chapter {
	kept := make([]ta.Chapter, 0, len(in))
	for _, c := range in {
		title := strings.TrimSpace(c.Title)
		if title == "" || c.Start < 0 {
			continue
		}
		if duration > 0 && c.Start >= duration {
			continue
		}
		kept = append(kept, ta.Chapter{Start: c.Start, Title: title})
	}
	sort.SliceStable(kept, func(i, j int) bool { return kept[i].Start < kept[j].Start })
	dedup := kept[:0]
	for _, c := range kept {
		if len(dedup) > 0 && c.Start <= dedup[len(dedup)-1].Start {
			continue
		}
		dedup = append(dedup, c)
	}
	out := make([]Chapter, 0, len(dedup))
	for i, c := range dedup {
		end := duration
		if i+1 < len(dedup) {
			end = dedup[i+1].Start
		}
		if duration > 0 && end > duration {
			end = duration
		}
		if end < c.Start {
			end = c.Start
		}
		out = append(out, Chapter{Start: c.Start, End: end, Title: c.Title})
	}
	return out
}

// ---- description parsing ----

// timestampRe matches one M:SS / MM:SS / H:MM:SS / HH:MM:SS timestamp that
// stands on its own at a word boundary, optionally wrapped in round or square
// brackets and optionally followed by a dash or colon separator.
var timestampRe = regexp.MustCompile(`(?:^|\s)[(\[]?(\d{1,3}):([0-5]?\d)(?::([0-5]\d))?[)\]]?[-–—:]?(?:\s|$)`)

// titleTrim are the separators a title may be padded with once the timestamp
// is cut out of the line.
const titleTrim = " \t\v\f-–—:•·|"

// descriptionChapters parses a YouTube-style chapter list out of a video
// description.
//
// A line qualifies when it carries exactly one leading or trailing timestamp
// and some text. To keep a stray "see 4:32" mention from being read as a
// chapter list the result is only accepted when at least two timestamps
// parse, the list opens at 0:00 (found within the first few entries, anything
// before it dropped as noise) and the times strictly increase.
func descriptionChapters(description string, duration float64) []ta.Chapter {
	var found []ta.Chapter
	for line := range strings.Lines(description) {
		c, ok := parseChapterLine(line)
		if !ok {
			continue
		}
		found = append(found, c)
		if len(found) > 512 {
			break
		}
	}
	// The list must open at the start of the video; allow a couple of stray
	// timestamps above it (a header line, a "0:00" inside a sentence).
	const leadSlack = 3
	start := -1
	for i, c := range found {
		if i >= leadSlack {
			break
		}
		if c.Start <= 1 {
			start = i
			break
		}
	}
	if start < 0 {
		return nil
	}
	found = found[start:]

	out := make([]ta.Chapter, 0, len(found))
	for _, c := range found {
		if len(out) > 0 && c.Start <= out[len(out)-1].Start {
			return nil // not a monotonically increasing list
		}
		if duration > 0 && c.Start >= duration {
			break
		}
		out = append(out, c)
	}
	if len(out) < 2 {
		return nil
	}
	return out
}

// parseChapterLine pulls the timestamp and the title out of one line.
func parseChapterLine(line string) (ta.Chapter, bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return ta.Chapter{}, false
	}
	m := timestampRe.FindStringSubmatchIndex(line)
	if m == nil {
		return ta.Chapter{}, false
	}
	secs, ok := timestampSeconds(group(line, m, 1), group(line, m, 2), group(line, m, 3))
	if !ok {
		return ta.Chapter{}, false
	}
	title := strings.TrimSpace(line[:m[0]]) + " " + strings.TrimSpace(line[m[1]:])
	title = strings.Trim(strings.TrimSpace(title), titleTrim)
	title = strings.TrimSpace(title)
	if title == "" {
		return ta.Chapter{}, false
	}
	return ta.Chapter{Start: secs, Title: title}, true
}

// group returns submatch n of a FindStringSubmatchIndex result, "" when the
// group did not participate.
func group(s string, m []int, n int) string {
	if 2*n+1 >= len(m) || m[2*n] < 0 {
		return ""
	}
	return s[m[2*n]:m[2*n+1]]
}

// timestampSeconds turns the captured groups into seconds. With three groups
// the timestamp is H:MM:SS, with two it is M:SS.
func timestampSeconds(a, b, c string) (float64, bool) {
	x, err := strconv.Atoi(a)
	if err != nil {
		return 0, false
	}
	y, err := strconv.Atoi(b)
	if err != nil {
		return 0, false
	}
	if c == "" {
		if y > 59 {
			return 0, false
		}
		return float64(x*60 + y), true
	}
	z, err := strconv.Atoi(c)
	if err != nil {
		return 0, false
	}
	if y > 59 || z > 59 {
		return 0, false
	}
	return float64(x*3600 + y*60 + z), true
}

// ---- cache ----

// chaptersCache is a small TTL cache of finished responses, keyed by video
// id. Chapters are immutable for a downloaded file, so the TTL is long and
// the map is bounded instead.
type chaptersCache struct {
	mu  sync.Mutex
	ttl time.Duration
	max int
	m   map[string]chaptersEntry
}

type chaptersEntry struct {
	val ChaptersResponse
	exp time.Time
}

func newChaptersCache() *chaptersCache {
	return &chaptersCache{ttl: chaptersTTL, max: chaptersCacheMax, m: map[string]chaptersEntry{}}
}

func (c *chaptersCache) get(id string) (ChaptersResponse, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[id]
	if !ok || time.Now().After(e.exp) {
		return ChaptersResponse{}, false
	}
	return e.val, true
}

func (c *chaptersCache) put(id string, v ChaptersResponse) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.m) >= c.max {
		c.evictLocked()
	}
	c.m[id] = chaptersEntry{val: v, exp: time.Now().Add(c.ttl)}
}

// evictLocked drops expired entries, and the entry closest to expiry when
// that freed nothing.
func (c *chaptersCache) evictLocked() {
	now := time.Now()
	oldest, oldestExp := "", time.Time{}
	for id, e := range c.m {
		if now.After(e.exp) {
			delete(c.m, id)
			continue
		}
		if oldest == "" || e.exp.Before(oldestExp) {
			oldest, oldestExp = id, e.exp
		}
	}
	if len(c.m) >= c.max && oldest != "" {
		delete(c.m, oldest)
	}
}
