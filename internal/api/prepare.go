package api

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Seklfreak/flimm/internal/media"
	"github.com/Seklfreak/flimm/internal/ta"
)

// Preparing what is about to be watched.
//
// The two cheap derivations — the scrub-preview sheet and the loudness
// measurement — are only made when a player asks for them, which means the
// first person to open a video scrubs a bare timeline and hears it at whatever
// level it was archived at. Both are read once per video and then never
// derived again, so doing them ahead of time turns "the first view is worse"
// into "no view is".
//
// Renditions are deliberately **not** prepared. A scrub sheet is half a
// megabyte whatever the video's length and a measurement is a hundred bytes; a
// 1080p rendition is about a gigabyte and a half per hour. Ten thousand videos
// of the first pair is six gigabytes, and of the last is five terabytes — the
// difference between something that fits in a cache and something that does
// not exist. See "Derived media" in docs/api.md.

const (
	// prepareEvery is how long after finishing one pass the next begins.
	prepareEvery = 2 * time.Hour
	// prepareDelay keeps the job off the boot path, behind the other sweeps.
	prepareDelay = 10 * time.Minute
	// prepareQuietFor is how long after the last progress heartbeat the job
	// stays out of the way. Clients beat every ten seconds while playing, so
	// this is a handful of missed beats — long enough not to start work
	// between two of them, short enough to get going again soon after a video
	// ends.
	prepareQuietFor = 45 * time.Second
	// prepareCheckEvery is how often a paused job looks again.
	prepareCheckEvery = 20 * time.Second
	// prepareJobWait bounds one derivation. Both passes decode the whole file;
	// a long video on a busy box is minutes, and something that has taken an
	// hour is not going to finish.
	prepareJobWait = time.Hour
	// prepareFeedDepth is how far into each feed to look. A feed's first pages
	// are what anyone actually opens; preparing the ten-thousandth video in it
	// would be work for a view that never comes.
	prepareFeedDepth = 60
)

// PrepareStatus is what the job is doing, for the UI to show.
type PrepareStatus struct {
	// State is idle|running|paused. `paused` is running-but-waiting: something
	// is being played, and the job does not compete with it.
	State string `json:"state"`
	// Done and Total count videos in the current pass. Total is 0 when there
	// is no pass in flight.
	Done  int `json:"done"`
	Total int `json:"total"`
	// Video is the title being prepared, for a line that says what is
	// happening rather than only how much of it is left.
	Video string `json:"video"`
	// PreparedAt is when the last pass finished, so a UI can say "up to date"
	// rather than only "idle". Zero before the first one.
	PreparedAt *time.Time `json:"prepared_at,omitempty"`
}

// prepareState is the job's own view of the same thing.
type prepareState struct {
	mu     sync.Mutex
	status PrepareStatus
}

func (p *prepareState) get() PrepareStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.status
}

func (p *prepareState) set(f func(*PrepareStatus)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	f(&p.status)
}

// notePlayback records that something is being played right now. Called from
// the progress heartbeat, which is the only signal that reaches the server
// while a video is simply playing — nothing else is requested once the file is
// buffering.
func (s *Server) notePlayback() {
	s.lastPlayback.Store(time.Now().UnixNano())
}

// playbackRecent reports whether a viewer is mid-video.
func (s *Server) playbackRecent() bool {
	last := s.lastPlayback.Load()
	if last == 0 {
		return false
	}
	return time.Since(time.Unix(0, last)) < prepareQuietFor
}

// PrepareStatusOf is what the status endpoint answers with.
func (s *Server) PrepareStatusOf() PrepareStatus {
	st := s.prepare.get()
	if st.State == "running" && s.playbackRecent() {
		st.State = "paused"
	}
	return st
}

// StartMediaPrepare runs the preparation pass on a timer until the context
// ends.
func (s *Server) StartMediaPrepare(ctx context.Context) {
	if s.q == nil || s.mediaCache == nil {
		return
	}
	go func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(prepareDelay):
		}
		for {
			s.prepareOnce(ctx)
			select {
			case <-ctx.Done():
				return
			case <-time.After(prepareEvery):
			}
		}
	}()
}

// prepareOnce derives the preview sheet and the loudness measurement for
// everything near the top of everyone's feeds that has neither.
func (s *Server) prepareOnce(ctx context.Context) {
	videos, err := s.prepareCandidates(ctx)
	if err != nil {
		s.log.Debug("prepare: gather candidates", "err", err)
		return
	}
	if len(videos) == 0 {
		return
	}
	s.prepare.set(func(st *PrepareStatus) {
		st.State, st.Done, st.Total, st.Video = "running", 0, len(videos), ""
	})
	// Both counters go back to zero together. Leaving `done` behind a `total`
	// of nought reads as "2 of 0", which is the sort of thing a progress line
	// says right before nobody trusts it again.
	defer s.prepare.set(func(st *PrepareStatus) {
		now := time.Now()
		st.State, st.Done, st.Total, st.Video, st.PreparedAt = "idle", 0, 0, "", &now
	})

	for i, v := range videos {
		if !s.waitForQuiet(ctx) {
			return
		}
		s.prepare.set(func(st *PrepareStatus) { st.Done, st.Video = i, v.Title })
		s.prepareVideo(ctx, v)
	}
	s.prepare.set(func(st *PrepareStatus) { st.Done, st.Video = len(videos), "" })
}

// waitForQuiet blocks until nothing is being played, and reports whether it is
// worth carrying on.
//
// Only *starting* work waits. A pass already running is left to finish: both
// derivations read the whole file, and killing one halfway through throws away
// the minutes it has spent without giving the player back anything it can use.
func (s *Server) waitForQuiet(ctx context.Context) bool {
	for s.playbackRecent() {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(prepareCheckEvery):
		}
	}
	return ctx.Err() == nil
}

// prepareVideo derives both of a video's cheap entries, one after the other,
// waiting for each. Serially on purpose: the scan lane is two wide and shared
// with whatever a viewer opens, and this job's whole job is to be the one that
// yields.
func (s *Server) prepareVideo(ctx context.Context, v ta.Video) {
	if media.PreviewReady(s.mediaCache.Dir(previewName(v.YoutubeID))) {
		s.log.Debug("prepare: preview already derived", "video", v.YoutubeID)
	} else if s.startPreviewScan(&v) == media.StateRunning {
		s.awaitScan(ctx, previewName(v.YoutubeID))
	}
	if _, done := media.ReadLoudness(s.mediaCache.Dir(loudnessName(v.YoutubeID))); done {
		return
	}
	if s.startLoudnessScan(&v) == media.StateRunning {
		s.awaitScan(ctx, loudnessName(v.YoutubeID))
	}
}

// awaitScan waits for one derivation to stop running.
func (s *Server) awaitScan(ctx context.Context, name string) {
	deadline := time.NewTimer(prepareJobWait)
	defer deadline.Stop()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-deadline.C:
			s.log.Debug("prepare: gave up waiting", "entry", name)
			return
		case <-tick.C:
			if s.mediaCache.DirState(name) != media.StateRunning {
				return
			}
		}
	}
}

// prepareCandidates is the head of every user's every feed, deduplicated.
//
// It reuses the feed listing itself rather than a query of its own, so what
// gets prepared is exactly what the viewer would be shown — the same sort, the
// same hide-seen, the same dismissals.
func (s *Server) prepareCandidates(ctx context.Context) ([]ta.Video, error) {
	users, err := s.q.ListUserIDs(ctx)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	out := []ta.Video{}
	for _, uid := range users {
		feeds, err := s.q.ListFeeds(ctx, uid)
		if err != nil {
			return nil, err
		}
		for _, f := range feeds {
			ids, err := s.feedHeadIDs(ctx, uid, f.ID)
			if err != nil {
				// One unreadable feed is not a reason to prepare nothing.
				s.log.Debug("prepare: list feed", "feed", f.ID, "err", err)
				continue
			}
			for _, id := range ids {
				if seen[id] {
					continue
				}
				seen[id] = true
				v, err := s.ta.GetVideo(ctx, id)
				if err != nil || v.MediaURL == "" {
					continue
				}
				out = append(out, *v)
			}
		}
	}
	return out, nil
}

// feedHeadIDs is the first prepareFeedDepth videos of one feed.
func (s *Server) feedHeadIDs(ctx context.Context, uid, feedID uuid.UUID) ([]string, error) {
	feed, chans, pls, err := s.loadFeed(ctx, uid, feedID.String())
	if err != nil {
		return nil, err
	}
	o := listOpts{
		ChannelIDs: chans, PlaylistIDs: pls, Sort: feed.Sort,
		IncludeShorts: feed.IncludeShorts, SubtitlesOnly: feed.SubtitlesOnly,
		// Always unseen, whatever the feed shows: a video already watched has
		// had whatever it needed derived, and preparing it again would be work
		// for a view that has happened.
		UnseenOnly: true, DropDismissed: true,
	}
	page, err := s.listFeedPage(ctx, uid, o, paging{Size: prepareFeedDepth})
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(page.Items))
	for _, it := range page.Items {
		ids = append(ids, it.ID)
	}
	return ids, nil
}

// getPrepareStatus answers GET /prepare: what the background preparation is
// doing right now. Unauthenticated-adjacent by design — it says nothing about
// any one person's library, only about the server's own housekeeping — but it
// sits behind the same auth as everything else.
func (s *Server) getPrepareStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.PrepareStatusOf())
}
