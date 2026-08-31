package api

import (
	"context"
	"time"

	"github.com/Seklfreak/flimm/internal/ta"
)

// Per-channel counts, cached.
//
// Two numbers are wanted for every channel on a page — how many videos it has,
// and how many are unseen — and TubeArchivist has no aggregate that answers
// them for more than one channel at a time (`stats/channel` is archive-wide).
// So they cost two queries per channel, and a page of channels cost hundreds:
// one request to `/channels` was traced making **429** calls.
//
// The client already kept them in memory for a minute, which helps within one
// process and not at all across a deploy — and a deploy is exactly when a cold
// page fans out to hundreds of calls at once. These are the same numbers, kept
// where a restart cannot reach them, under the rule the third-party lookups
// follow: serve what is known, refresh what is stale behind the response.
//
// Nothing is swept. Unlike crowd titles these are cheap and local, and a
// channel nobody looks at does not need a current count; the refresh happens
// when something asks.

// channelAggregate is what a channel's row holds.
type channelAggregate struct {
	VideoCount int    `json:"video_count"`
	Unseen     int    `json:"unseen"`
	LastUpload string `json:"last_upload,omitempty"` // RFC 3339, empty when unknown
}

func (a channelAggregate) lastUpload() *time.Time {
	if a.LastUpload == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, a.LastUpload)
	if err != nil {
		return nil
	}
	return &t
}

// channelAggregates resolves the counts for a set of channels.
//
// Known rows are returned as they are and refreshed behind the response when
// stale. Channels with no row are fetched now: unlike the third-party lookups
// this is the deployment's own archive, a couple of tens of milliseconds away,
// and a channel with no counts at all would render as an empty row rather than
// a slightly old one.
func (s *Server) channelAggregates(ctx context.Context, ids []string) map[string]channelAggregate {
	out := make(map[string]channelAggregate, len(ids))
	if len(ids) == 0 {
		return out
	}
	known := s.cacheLoad(ctx, sourceChannel, ids)
	now := time.Now()
	var missing []string
	for _, id := range ids {
		row, ok := known[id]
		var agg channelAggregate
		if !ok || !row.decode(&agg) {
			missing = append(missing, id)
			continue
		}
		out[id] = agg
		if !row.fresh(sourceChannel, now) {
			s.cacheQueue(sourceChannel, id)
		}
	}
	if len(missing) == 0 {
		return out
	}
	fetched := make([]channelAggregate, len(missing))
	ok := make([]bool, len(missing))
	_ = parallel(ctx, missing, func(ctx context.Context, i int, id string) error {
		agg, got := s.fetchChannelAggregate(ctx, id)
		fetched[i], ok[i] = agg, got
		return nil // one channel's counts are not the page's failure
	})
	for i, id := range missing {
		if ok[i] {
			out[id] = fetched[i]
		}
	}
	return out
}

// fetchChannelAggregate asks TubeArchivist for one channel's counts and stores
// them.
func (s *Server) fetchChannelAggregate(ctx context.Context, id string) (channelAggregate, bool) {
	stats, err := s.ta.ChannelStats(ctx, id)
	if err != nil {
		return channelAggregate{}, false
	}
	unseen, err := s.ta.UnseenCount(ctx, id)
	if err != nil {
		return channelAggregate{}, false
	}
	agg := channelAggregate{VideoCount: stats.VideoCount, Unseen: unseen}
	if !stats.LastUpload.IsZero() {
		agg.LastUpload = stats.LastUpload.UTC().Format(time.RFC3339)
	}
	// has_data is true for any channel that holds a video: a channel with
	// nothing in it is the one case worth re-checking sooner, because that is
	// the state a newly subscribed channel is in.
	s.detachedSave(ctx, sourceChannel, id, agg, agg.VideoCount > 0)
	return agg, true
}

// taUnseenQuery counts the unwatched videos in the whole archive: one query
// with a page of one, read for its total.
func taUnseenQuery() ta.VideoQuery {
	return ta.VideoQuery{Watch: "unwatched", PageSize: 1}
}

// unseenForChannels totals the unseen videos across a set of channels, from the
// cache. `nil` means the whole archive, which TubeArchivist answers in one.
func (s *Server) unseenForChannels(ctx context.Context, channelIDs []string) (int, error) {
	if channelIDs == nil {
		p, err := s.ta.ListVideos(ctx, taUnseenQuery())
		if err != nil {
			return 0, err
		}
		return p.Paginate.TotalHits, nil
	}
	total := 0
	for _, agg := range s.channelAggregates(ctx, channelIDs) {
		total += agg.Unseen
	}
	return total, nil
}
