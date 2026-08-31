package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/Seklfreak/flimm/internal/ta"
)

// Lazy composition.
//
// A feed is several TubeArchivist queries merged into one sorted run, with
// Flimm's own filters on top. Composing it by fetching everything first put a
// hard ceiling on how large an archive could be, and made a request for page 0
// pay for page 40.
//
// Instead each query becomes a cursor that pulls TA pages only when the merge
// asks for the next video, and the merge stops as soon as the requested window
// is full. Memory is one TA page per channel rather than the whole archive.
//
// Offset paging still costs what it always did — page 40 has to walk the pages
// before it to know where to start. A caller that hands back the `next_cursor`
// from its last response skips that walk: the cursor records where each stream
// had got to, so any page costs what the first one did.

// streamPos is one stream's place in its TA query: a page, and how far into
// that page's rows it has read. Both are TA's own numbers, so they survive a
// round trip through a client.
type streamPos struct {
	Page  int `json:"p"`
	Index int `json:"i"`
}

// videoStream is a lazily paged cursor over one TA video query. The zero value
// is not usable; build one with newStream.
type videoStream struct {
	q    ta.VideoQuery
	buf  []ta.Video
	i    int
	page int
	done bool
	// skip is how far into the next page fetched to start, set by seek when
	// resuming from a cursor.
	skip int
	// seen keeps one stream from handing back the same video twice when TA
	// shifts rows between pages.
	seen map[string]bool
}

func newStream(q ta.VideoQuery) *videoStream {
	q.PageSize = maxPageSize
	return &videoStream{q: q, seen: map[string]bool{}}
}

// seek resumes the stream where a cursor left it.
func (st *videoStream) seek(p streamPos) {
	if p.Page < 1 || p.Index < 0 {
		return
	}
	st.page = p.Page - 1
	st.skip = p.Index
	st.buf, st.i, st.done = nil, 0, false
}

// pos is where the stream is now: the page it last fetched and the row the
// next read would come from. Index counts TA's rows, not the ones that
// survived filtering, so it means the same thing on the way back in.
func (st *videoStream) pos() streamPos { return streamPos{Page: st.page, Index: st.i} }

// peek returns the next video without consuming it, fetching another TA page
// when the buffer runs dry. It returns nil when the stream is exhausted.
func (st *videoStream) peek(ctx context.Context, c ta.Client) (*ta.Video, error) {
	for {
		if st.i < len(st.buf) {
			v := &st.buf[st.i]
			if st.seen[v.YoutubeID] {
				st.i++
				continue
			}
			return v, nil
		}
		if st.done {
			return nil, nil
		}
		st.page++
		q := st.q
		q.Page = st.page
		res, err := c.ListVideos(ctx, q)
		if err != nil {
			return nil, err
		}
		// TA picks its own page size and ignores the one we ask for, so a
		// short page is not the last page — only its own pagination says so.
		if res.Paginate.LastPage > 0 && st.page >= res.Paginate.LastPage {
			st.done = true
		}
		if len(res.Data) == 0 {
			st.done = true
		}
		st.buf, st.i = res.Data, min(st.skip, len(res.Data))
		st.skip = 0
	}
}

// advance consumes the video peek returned.
func (st *videoStream) advance() {
	if st.i < len(st.buf) {
		st.seen[st.buf[st.i].YoutubeID] = true
		st.i++
	}
}

// merge pulls from the streams in sorted order, calling emit with each video
// and the position to resume from just after it, until emit returns false or
// every stream is exhausted. The comparison must match sortSummaries, or a
// window boundary could repeat or skip a video.
func merge(
	ctx context.Context,
	c ta.Client,
	streams []*videoStream,
	sortKey string,
	emit func(ta.Video, []streamPos) bool,
) error {
	seen := map[string]bool{}
	for {
		var best *ta.Video
		var from *videoStream
		for _, st := range streams {
			v, err := st.peek(ctx, c)
			if err != nil {
				return err
			}
			if v == nil {
				continue
			}
			if best == nil || lessVideo(*v, *best, sortKey) {
				best, from = v, st
			}
		}
		if from == nil {
			return nil
		}
		v := *best
		from.advance()
		// Channels do not overlap, but a playlist query and a channel query
		// can hand back the same video.
		if seen[v.YoutubeID] {
			continue
		}
		seen[v.YoutubeID] = true
		if !emit(v, positions(streams)) {
			return nil
		}
	}
}

func positions(streams []*videoStream) []streamPos {
	out := make([]streamPos, len(streams))
	for i, st := range streams {
		out[i] = st.pos()
	}
	return out
}

// lessVideo orders two TA videos the way sortSummaries orders the summaries
// built from them, down to the tie-breakers: the merge and the final sort must
// agree exactly.
func lessVideo(a, b ta.Video, sortKey string) bool {
	switch sortKey {
	case "oldest":
		if !a.PublishedTime().Equal(b.PublishedTime()) {
			return a.PublishedTime().Before(b.PublishedTime())
		}
	case "shortest":
		if a.Player.Duration != b.Player.Duration {
			return a.Player.Duration < b.Player.Duration
		}
	case "longest":
		if a.Player.Duration != b.Player.Duration {
			return a.Player.Duration > b.Player.Duration
		}
	default:
		if !a.PublishedTime().Equal(b.PublishedTime()) {
			return a.PublishedTime().After(b.PublishedTime())
		}
	}
	if a.DateDownloaded != b.DateDownloaded {
		return a.DateDownloaded > b.DateDownloaded
	}
	return a.YoutubeID < b.YoutubeID
}

// streamsFor turns listOpts into one cursor per TA query: one per channel and
// one per playlist source, or a single unfiltered one for the everything feed.
func (s *Server) streamsFor(o listOpts) []*videoStream {
	field, order := taSort(o.Sort)
	watch := ""
	if o.UnseenOnly {
		watch = "unwatched"
	}
	if o.ChannelIDs == nil {
		return []*videoStream{newStream(ta.VideoQuery{Watch: watch, Sort: field, Order: order})}
	}
	out := make([]*videoStream, 0, len(o.ChannelIDs)+len(o.PlaylistIDs))
	for _, ch := range o.ChannelIDs {
		out = append(out, newStream(ta.VideoQuery{Channel: ch, Watch: watch, Sort: field, Order: order}))
	}
	for _, pl := range o.PlaylistIDs {
		out = append(out, newStream(ta.VideoQuery{Playlist: pl, Watch: watch, Sort: field, Order: order}))
	}
	return out
}

// composed is one item and the place to resume from just after it.
type composed struct {
	item VideoSummary
	pos  []streamPos
}

func summaries(items []composed) []VideoSummary {
	out := make([]VideoSummary, 0, len(items))
	for _, c := range items {
		out = append(out, c.item)
	}
	return out
}

// buildWindow composes the next `need` items of a list, lazily, starting where
// `from` left off (nil = the beginning).
//
// It returns what it composed and whether the streams still had more to give:
// a caller asking for one item more than its window learns both the window and
// whether a next page exists, without walking the archive.
//
// Videos are overlaid in chunks so per-user state stays one database round
// trip per chunk rather than one per video. `walked` bounds the work when the
// filters reject almost everything — a feed whose channels are entirely
// watched would otherwise read the whole archive to fill one page.
func (s *Server) buildWindow(
	ctx context.Context,
	uid uuid.UUID,
	o listOpts,
	from []streamPos,
	need int,
) (items []composed, more bool, err error) {
	streams := s.streamsFor(o)
	for i, p := range from {
		if i < len(streams) {
			streams[i].seek(p)
		}
	}

	out := []composed{}
	var chunk []ta.Video
	var chunkPos [][]streamPos
	walked := 0

	flush := func() error {
		if len(chunk) == 0 {
			return nil
		}
		got, err := s.overlay(ctx, uid, chunk)
		if err != nil {
			return err
		}
		// overlay is 1:1 and order-preserving, so each summary still lines up
		// with the position recorded for the video it came from.
		for i, it := range got {
			if visible(it, o) {
				out = append(out, composed{item: it, pos: chunkPos[i]})
			}
		}
		chunk, chunkPos = chunk[:0], chunkPos[:0]
		return nil
	}

	err = merge(ctx, s.ta, streams, o.Sort, func(v ta.Video, pos []streamPos) bool {
		walked++
		if !keepRaw(v, o) {
			return walked < maxComposeVideos
		}
		chunk = append(chunk, v)
		chunkPos = append(chunkPos, pos)
		// Overlay as soon as the chunk is full, or as soon as it could already
		// hold the rest of the window: a first page should cost the TA pages
		// it needs, not a full chunk of look-ahead.
		if len(chunk) >= overlayChunk || len(out)+len(chunk) > need {
			if err := flush(); err != nil {
				return false
			}
		}
		return len(out) <= need && walked < maxComposeVideos
	})
	if err != nil {
		return nil, false, err
	}
	if err := flush(); err != nil {
		return nil, false, err
	}
	if len(out) > need {
		return out[:need], true, nil
	}
	return out, false, nil
}

// keepRaw is the filtering that needs nothing from the database.
func keepRaw(v ta.Video, o listOpts) bool {
	if !o.IncludeShorts && v.Kind() == "short" {
		return false
	}
	if o.SubtitlesOnly && len(v.Subtitles) == 0 {
		return false
	}
	return true
}

// visible is the filtering that needs the per-user overlay.
func visible(it VideoSummary, o listOpts) bool {
	if o.UnseenOnly && it.Watched {
		return false
	}
	if o.ExcludeIDs[it.ID] {
		return false
	}
	if o.DropDismissed && it.Dismissed {
		return false
	}
	return true
}

// errBadCursor marks a cursor the caller must not simply retry: the answer is
// to start the list again, not to be silently served page 0 and shown
// everything twice.
var errBadCursor = errors.New("bad cursor")

// ---- cursors ----

// listCursor is what `next_cursor` carries: where every stream had got to, how
// many items came before it, and a fingerprint of the list it belongs to.
type listCursor struct {
	Fingerprint uint64      `json:"f"`
	Positions   []streamPos `json:"s"`
	Before      int         `json:"n"`
	// Head is how many of the list's in-progress items have been served. Only
	// an unseen feed has any; everything else leaves it zero.
	Head int `json:"h,omitempty"`
}

// fingerprint identifies the list a cursor belongs to. Positions are aligned
// with the streams by index and mean nothing anywhere else, so a cursor handed
// to a different list has to be refused rather than silently resumed.
func fingerprint(o listOpts) uint64 {
	h := fnv.New64a()
	ids := slices.Clone(o.ChannelIDs)
	slices.Sort(ids)
	// Writing to an fnv hash cannot fail.
	_, _ = fmt.Fprintf(h, "%t|%s|%s|%t|%t|%t|%t",
		o.ChannelIDs == nil, strings.Join(ids, ","), o.Sort,
		o.IncludeShorts, o.SubtitlesOnly, o.UnseenOnly, o.DropDismissed)
	// Appended only when present, so cursors from before playlist sources
	// existed (and every channel-only list's) stay valid.
	if len(o.PlaylistIDs) > 0 {
		pls := slices.Clone(o.PlaylistIDs)
		slices.Sort(pls)
		_, _ = fmt.Fprintf(h, "|pl:%s", strings.Join(pls, ","))
	}
	return h.Sum64()
}

func encodeCursor(o listOpts, pos []streamPos, before, head int) string {
	raw, err := json.Marshal(listCursor{Fingerprint: fingerprint(o), Positions: pos, Before: before, Head: head})
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

// decodeCursor reads a cursor for this list. One that is unreadable or belongs
// to a different list is an error: serving it from the start instead would
// silently repeat everything the caller has already shown.
func decodeCursor(s string, o listOpts) (*listCursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("%w: not valid base64", errBadCursor)
	}
	var c listCursor
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("%w: not valid", errBadCursor)
	}
	if c.Fingerprint != fingerprint(o) || c.Before < 0 {
		return nil, fmt.Errorf("%w: belongs to a different list", errBadCursor)
	}
	return &c, nil
}

// listVideosPage answers one page of a lazily composed video list, by cursor
// when the caller has one and by offset when it does not.
//
// Both forms return a `next_cursor`, so a client can start with `page=0` and
// follow cursors from then on without ever asking for a deep offset.
func (s *Server) listVideosPage(
	ctx context.Context,
	uid uuid.UUID,
	o listOpts,
	p paging,
) (Page[VideoSummary], error) {
	return s.listVideosPageAfterHead(ctx, uid, o, p, 0, 0)
}

// listVideosPageAfterHead is listVideosPage for a list that has a head in
// front of it — the in-progress videos an unseen feed opens with. `head` is
// how many of those there are in total, and `served` how many this page has
// already emitted; both ride the cursor so the next page knows where it is.
func (s *Server) listVideosPageAfterHead(
	ctx context.Context,
	uid uuid.UUID,
	o listOpts,
	p paging,
	head, served int,
) (Page[VideoSummary], error) {
	var from []streamPos
	before := 0
	skip := 0
	if p.Cursor != "" {
		c, err := decodeCursor(p.Cursor, o)
		if err != nil {
			return Page[VideoSummary]{}, err
		}
		from, before = c.Positions, c.Before
	} else {
		skip = p.offset()
	}

	prefix, more, err := s.buildWindow(ctx, uid, o, from, skip+p.Size+1)
	if err != nil {
		return Page[VideoSummary]{}, err
	}
	lo := min(skip, len(prefix))
	hi := min(lo+p.Size, len(prefix))
	window := prefix[lo:hi]

	page := Page[VideoSummary]{
		Items:    summaries(window),
		Page:     p.Page,
		PageSize: p.Size,
		// A floor, not a length: composition stopped as soon as the window was
		// full. `before` keeps it climbing across cursor pages.
		Total:   int64(head + before + len(prefix)),
		HasMore: more || hi < len(prefix),
	}
	if page.HasMore && len(window) > 0 {
		page.NextCursor = encodeCursor(o, window[len(window)-1].pos, before+hi, served)
	}
	return page, nil
}
