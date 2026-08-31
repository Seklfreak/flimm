package api

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Seklfreak/flimm/internal/db/sqlc"
)

// Everything Flimm asks a third party goes through here.
//
// Three services are involved — DeArrow for crowd titles, SponsorBlock for
// segments, Return YouTube Dislike for the other half of a vote — and all three
// had the same shape of problem: a lookup on somebody else's host, inside a
// request, behind a cache that lived in memory and died on every deploy. The
// host they share answers in a couple of hundred milliseconds when it is happy
// and has been measured at fifteen seconds when it is not.
//
// The rule for all of them: **serve what is known, refresh what is stale behind
// the response, and never wait twice for the same thing.** What differs is only
// what to do when nothing is known yet, and that is a decision each caller
// makes for itself:
//
//   - DeArrow has no fallback — an archive title is not a crowd title — so a
//     list page waits, briefly and once, for a video it has never seen.
//   - SponsorBlock has one: the snapshot TubeArchivist stored at download time.
//     So it never waits at all; the snapshot goes out and the service is asked
//     in the background.
//   - Return YouTube Dislike has one too: the count TubeArchivist archived. Same
//     answer, never waits.

// cacheSource names one third party. It is the first half of a row's key.
type cacheSource string

const (
	sourceDeArrow      cacheSource = "dearrow"
	sourceSponsorBlock cacheSource = "sponsorblock"
	sourceRYD          cacheSource = "ryd"
	// sourceChannel is the deployment's own archive rather than a third party:
	// per-channel counts that TubeArchivist can only answer one channel at a
	// time. See channelcache.go.
	sourceChannel cacheSource = "channel"
)

// cachePolicy is how long one source's answers stay fresh.
//
// Two windows, because the two kinds of answer age differently. A row carrying
// data can change — a title is voted on, a segment is corrected — while "nobody
// has submitted anything" is most of the table and rarely becomes something
// else. Being a week late on a title nobody has written costs nothing.
type cachePolicy struct {
	withData time.Duration
	empty    time.Duration
}

var cachePolicies = map[cacheSource]cachePolicy{
	sourceDeArrow: {withData: 24 * time.Hour, empty: 7 * 24 * time.Hour},
	// Shorter, because a stale segment is the one kind of staleness a viewer
	// *sees*: a skip that lands in the wrong place. Segments are also corrected
	// mostly in the days after an upload, which is exactly when a video is new
	// to the archive too.
	sourceSponsorBlock: {withData: 12 * time.Hour, empty: 3 * 24 * time.Hour},
	// Vote counts drift slowly and nothing depends on them being exact.
	sourceRYD: {withData: 24 * time.Hour, empty: 7 * 24 * time.Hour},
	// Minutes, not hours: an unseen count moves as its owner watches, and it is
	// cheap to refresh — the archive is in the same cluster. Long enough that a
	// page of a hundred channels is not a hundred queries, short enough that a
	// badge is never far wrong. A channel with nothing in it is re-checked
	// sooner, because that is what a newly subscribed channel looks like.
	sourceChannel: {withData: 5 * time.Minute, empty: 1 * time.Minute},
}

// cacheEntry is one row, still encoded.
type cacheEntry struct {
	payload []byte
	// hasData is whether the service had anything to say, which is all that can
	// be known about a payload without opening it — and all the freshness rule
	// needs.
	hasData   bool
	fetchedAt time.Time
}

// fresh reports whether this row can be served without queueing a refresh.
func (e cacheEntry) fresh(source cacheSource, now time.Time) bool {
	policy, ok := cachePolicies[source]
	if !ok {
		return false
	}
	window := policy.empty
	if e.hasData {
		window = policy.withData
	}
	return now.Sub(e.fetchedAt) < window
}

// decode unpacks a payload into v.
func (e cacheEntry) decode(v any) bool {
	return json.Unmarshal(e.payload, v) == nil
}

// cacheLoad reads what is known for these keys. A cache that cannot be read is
// a slow page, not a broken one, so an error is an empty result.
func (s *Server) cacheLoad(ctx context.Context, source cacheSource, keys []string) map[string]cacheEntry {
	if s.q == nil || len(keys) == 0 {
		return nil
	}
	rows, err := s.q.ListCached(ctx, sqlc.ListCachedParams{Source: string(source), Keys: keys})
	if err != nil {
		s.log.Debug("external cache read failed", "source", source, "err", err)
		return nil
	}
	out := make(map[string]cacheEntry, len(rows))
	for _, row := range rows {
		out[row.Key] = cacheEntry{payload: row.Payload, hasData: row.HasData, fetchedAt: row.FetchedAt.Time}
	}
	return out
}

// cacheSave records what a service said, including that it said nothing —
// which is the answer worth keeping most, because it is the one that would
// otherwise be asked for again and again.
func (s *Server) cacheSave(ctx context.Context, source cacheSource, key string, payload any, hasData bool) {
	if s.q == nil {
		return
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		s.log.Debug("external cache encode failed", "source", source, "key", key, "err", err)
		return
	}
	err = s.q.UpsertCached(ctx, sqlc.UpsertCachedParams{
		Source: string(source), Key: key, Payload: encoded, HasData: hasData,
	})
	if err != nil {
		s.log.Debug("external cache write failed", "source", source, "key", key, "err", err)
	}
}

// detachedSave gives a write its own deadline: the request that triggered a
// lookup may be finished, and the answer is still worth keeping.
func (s *Server) detachedSave(ctx context.Context, source cacheSource, key string, payload any, hasData bool) {
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	s.cacheSave(saveCtx, source, key, payload, hasData)
}

// cacheJob is one background lookup.
type cacheJob struct {
	source cacheSource
	key    string
}

// cacheQueue asks for keys to be fetched in the background. It never blocks: a
// full queue means the refresh happens on some later request instead, which is
// a slightly staler answer and nothing a viewer sees.
func (s *Server) cacheQueue(source cacheSource, keys ...string) {
	if s.cacheJobs == nil {
		return
	}
	for _, key := range keys {
		select {
		case s.cacheJobs <- cacheJob{source: source, key: key}:
		default:
			return
		}
	}
}

// StartCacheWarmer runs the background half: workers draining the queue, and
// the DeArrow sweep that fills it from the archive.
//
// Only DeArrow is swept. It is the one read on *list* pages — thirty videos at
// a time, on every scroll — so a video nobody has looked up yet is a page that
// waits. The other two are read when a single video is opened, so the first
// open pays and every later one is free; sweeping them would be sixteen
// thousand requests to warm data that is read once per video.
func (s *Server) StartCacheWarmer(ctx context.Context) {
	if s.q == nil || s.cacheJobs == nil {
		return
	}
	for range cacheWorkers {
		go s.cacheWorker(ctx)
	}
	if s.dearrow != nil {
		go s.brandingSweep(ctx)
	}
}

func (s *Server) cacheWorker(ctx context.Context) {
	// A gap between lookups: this is somebody else's service, and a cache warm
	// an hour from now is worth what one warm right now is.
	tick := time.NewTicker(cacheLookupGap)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-s.cacheJobs:
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
			s.runCacheJob(ctx, job)
		}
	}
}

// runCacheJob performs one background lookup. Each source knows how to fetch
// and store its own answer; this only decides that it happens.
func (s *Server) runCacheJob(ctx context.Context, job cacheJob) {
	switch job.source {
	case sourceDeArrow:
		s.fetchBranding(ctx, job.key)
	case sourceSponsorBlock:
		s.fetchSponsorSegments(ctx, job.key)
	case sourceRYD:
		s.fetchVotes(ctx, job.key)
	case sourceChannel:
		s.fetchChannelAggregate(ctx, job.key)
	}
}
