package api

import (
	"context"
	"time"
)

// Cleaning up after videos everybody has finished.
//
// Derived media is regenerable and expensive: an HLS rendition of a 1080p hour
// is a couple of gigabytes, and the cache is bounded only by
// `MEDIA_CACHE_MAX_BYTES` and least-recently-used eviction. Eviction alone
// throws away whatever was touched longest ago, which on a full disk is as
// likely to be a video somebody is half-way through as one they finished last
// month. This sweep takes the obvious candidates first — a video every viewer
// has finished is a video whose rendition nobody is about to ask for — and
// leaves eviction to handle whatever is left.
//
// It is not a cache invalidation: nothing here is authoritative, and a video
// cleaned up and then played again simply derives again. That is what makes
// the rule allowed to be approximate.

const (
	// mediaCleanupEvery is how often the sweep runs. Hours rather than
	// minutes: what it reclaims is not urgent, and it reads every pinned
	// playlist out of TubeArchivist each time.
	mediaCleanupEvery = 6 * time.Hour
	// mediaCleanupDelay keeps the sweep off the boot path. A deploy should not
	// be followed by a burst of TA requests while the app is serving its first
	// pages.
	mediaCleanupDelay = 5 * time.Minute
)

// StartMediaCleanup runs the sweep on a timer until the context ends.
func (s *Server) StartMediaCleanup(ctx context.Context) {
	if s.q == nil || s.mediaCache == nil {
		return
	}
	go func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(mediaCleanupDelay):
		}
		for {
			s.cleanupWatchedMedia(ctx)
			select {
			case <-ctx.Done():
				return
			case <-time.After(mediaCleanupEvery):
			}
		}
	}()
}

// cleanupWatchedMedia removes the derivations of videos every viewer has
// finished, keeping anything in a pinned playlist.
//
// It starts from the *disk*, not from the watch history. What the cache holds
// is hundreds of entries; what has been watched can be tens of thousands, and
// all but a handful of those have nothing derived to clean up.
func (s *Server) cleanupWatchedMedia(ctx context.Context) {
	held := s.mediaCache.Videos()
	if len(held) == 0 {
		return
	}
	finished, err := s.q.ListFinishedVideos(ctx, held)
	if err != nil {
		s.log.Debug("media cleanup: list finished", "err", err)
		return
	}
	if len(finished) == 0 {
		return
	}

	stale := make(map[string]bool, len(finished))
	for _, id := range finished {
		stale[id] = true
	}
	// A pin is a statement that this list is worth keeping to hand, which is
	// exactly the claim a rendition needs to survive being watched. Failing to
	// read the pins is a reason to do nothing at all, not a reason to delete
	// what they were protecting.
	kept, err := s.pinnedPlaylistVideos(ctx)
	if err != nil {
		s.log.Debug("media cleanup: read pinned playlists", "err", err)
		return
	}
	for id := range kept {
		delete(stale, id)
	}
	if len(stale) == 0 {
		return
	}

	entries, freed := s.mediaCache.RemoveFor(stale)
	if entries > 0 {
		s.log.Info("media cleanup", "videos", len(stale), "entries", entries, "bytes", freed, "pinned", len(kept))
	}
}

// pinnedPlaylistVideos is every video in a playlist anyone has pinned.
//
// A playlist that cannot be read is an error rather than an empty set: an
// unreachable TubeArchivist would otherwise read as "nothing is pinned", and
// the sweep would delete precisely what the pins existed to protect.
func (s *Server) pinnedPlaylistVideos(ctx context.Context) (map[string]bool, error) {
	ids, err := s.q.ListAllPinnedPlaylists(ctx)
	if err != nil {
		return nil, err
	}
	kept := map[string]bool{}
	for _, id := range ids {
		p, err := s.ta.GetPlaylist(ctx, id)
		if err != nil {
			return nil, err
		}
		for _, e := range p.PlaylistEntries {
			kept[e.YoutubeID] = true
		}
	}
	return kept, nil
}
