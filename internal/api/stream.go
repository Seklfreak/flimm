package api

import (
	"context"

	"github.com/google/uuid"

	"github.com/Seklfreak/flimm/internal/ta"
)

// Lazy composition.
//
// A feed is several TubeArchivist queries merged into one sorted run, with
// Flimm's own filters on top. Composing it by fetching everything first put a
// hard ceiling on how large an archive could be — the old `maxListVideos`
// cap — and made a request for page 0 pay for page 40.
//
// Instead each query becomes a cursor that pulls TA pages only when the merge
// asks for the next video, and the merge stops as soon as the requested window
// is full. Memory is one TA page per channel rather than the whole archive.

// videoStream is a lazily paged cursor over one TA video query. The zero value
// is not usable; build one with newStream.
type videoStream struct {
	q    ta.VideoQuery
	buf  []ta.Video
	i    int
	page int
	done bool
	// seen guards against a TA version that ignores `page` and keeps handing
	// back the same rows: a page that is entirely repeats ends the stream.
	seen map[string]bool
}

func newStream(q ta.VideoQuery) *videoStream {
	q.PageSize = maxPageSize
	return &videoStream{q: q, seen: map[string]bool{}}
}

// peek returns the next video without consuming it, fetching another TA page
// when the buffer runs dry. It returns nil when the stream is exhausted.
func (st *videoStream) peek(ctx context.Context, c ta.Client) (*ta.Video, error) {
	for st.i >= len(st.buf) {
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
		// A fresh slice: res.Data can alias the client's cached page, so
		// filtering in place would corrupt it for the next reader.
		fresh := make([]ta.Video, 0, len(res.Data))
		for _, v := range res.Data {
			if st.seen[v.YoutubeID] {
				continue
			}
			st.seen[v.YoutubeID] = true
			fresh = append(fresh, v)
		}
		if len(fresh) == 0 {
			st.done = true
		}
		st.buf, st.i = fresh, 0
	}
	return &st.buf[st.i], nil
}

func (st *videoStream) advance() { st.i++ }

// merge pulls from the streams in sorted order, calling emit for each video
// until emit returns false or every stream is exhausted. The comparison must
// match sortSummaries, or a window boundary could repeat or skip a video.
func merge(ctx context.Context, c ta.Client, streams []*videoStream, sortKey string, emit func(ta.Video) bool) error {
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
		if !emit(v) {
			return nil
		}
	}
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

// streamsFor turns listOpts into one cursor per TA query: one per channel, or
// a single unfiltered one for the everything feed.
func (s *Server) streamsFor(o listOpts) []*videoStream {
	field, order := taSort(o.Sort)
	watch := ""
	if o.UnseenOnly {
		watch = "unwatched"
	}
	if o.ChannelIDs == nil {
		return []*videoStream{newStream(ta.VideoQuery{Watch: watch, Sort: field, Order: order})}
	}
	out := make([]*videoStream, 0, len(o.ChannelIDs))
	for _, ch := range o.ChannelIDs {
		out = append(out, newStream(ta.VideoQuery{Channel: ch, Watch: watch, Sort: field, Order: order}))
	}
	return out
}

// buildWindow composes the first `need` items of a list, lazily.
//
// It returns what it composed and whether the streams still had more to give:
// a caller asking for offset+size+1 items learns both the window it wanted and
// whether a next page exists, without walking the archive.
//
// Videos are overlaid in chunks so the per-user state stays one database round
// trip per chunk rather than one per video. `walked` bounds the work when the
// filters reject almost everything — a feed whose channels are entirely
// watched would otherwise read the whole archive to fill one page.
func (s *Server) buildWindow(ctx context.Context, uid uuid.UUID, o listOpts, need int) (items []VideoSummary, more bool, err error) {
	streams := s.streamsFor(o)
	out := []VideoSummary{}
	chunk := make([]ta.Video, 0, overlayChunk)
	walked := 0

	flush := func() error {
		if len(chunk) == 0 {
			return nil
		}
		got, err := s.overlay(ctx, uid, chunk)
		if err != nil {
			return err
		}
		out = append(out, keepVisible(got, o)...)
		chunk = chunk[:0]
		return nil
	}

	err = merge(ctx, s.ta, streams, o.Sort, func(v ta.Video) bool {
		walked++
		if !keepRaw(v, o) {
			return walked < maxComposeVideos
		}
		chunk = append(chunk, v)
		// Overlay as soon as the chunk is full, or as soon as it could
		// already hold the rest of the window: a first page should cost the
		// TA pages it needs, not a full chunk of look-ahead.
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
	// The merge already emitted in order; this only re-applies the shared
	// tie-breaking so the result is byte-for-byte what the eager path gave.
	sortSummaries(out, o.Sort)
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

// keepVisible is the filtering that needs the per-user overlay.
func keepVisible(items []VideoSummary, o listOpts) []VideoSummary {
	kept := items[:0]
	for _, it := range items {
		if o.UnseenOnly && it.Watched {
			continue
		}
		if o.DropDismissed && it.Dismissed {
			continue
		}
		kept = append(kept, it)
	}
	return kept
}
