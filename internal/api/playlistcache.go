package api

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/Seklfreak/flimm/internal/db/sqlc"
	"github.com/Seklfreak/flimm/internal/ta"
)

// A playlist's videos, cached — the numbers a sidebar shows, without the
// documents they were read from.
//
// A playlist summary reports six things: how many videos, how long in total,
// how many seen, how many started, the fraction done, and where to resume.
// Producing them cost every video document in the playlist — one request per
// hundred entries, then DeArrow branding over all of them to render integers
// nobody reads a title from. One `/playlists` was traced making sixteen calls
// for this, and the pinned-playlists sidebar seven more on every page load.
//
// Only two of the six actually need the archive: the count and the total
// duration. The rest is the user's own watch state, which is already in
// Postgres and keyed by video id. So what is cached here is exactly the part
// the archive owns — each video's duration, and TubeArchivist's own watched
// flag, which `summarize` falls back to when Flimm has never seen the video —
// and the per-user half is computed fresh on every request, as it must be.
//
// The rule is the one the other sources follow, with one addition. A playlist
// changes in a way a viewer notices immediately: a video finishes downloading
// and belongs in the count *now*. Time-based freshness would be up to half an
// hour late on that, so the row also records how many entries were downloaded
// when it was written, and a mismatch is treated as stale on the spot rather
// than refreshed in the background.

// playlistVideoState is what the archive knows about one video in a playlist.
// The fields mirror ta.Player because that is what they are rebuilt into.
type playlistVideoState struct {
	ID       string  `json:"id"`
	Duration float64 `json:"duration"`
	Watched  bool    `json:"watched,omitempty"`
	Position float64 `json:"position,omitempty"`
}

// playlistAggregate is one playlist's row.
type playlistAggregate struct {
	Videos []playlistVideoState `json:"videos"`
	// Downloaded is how many of the playlist's entries were marked downloaded
	// when this was written. It is the row's validity token: TubeArchivist
	// flips that flag the moment a video lands, which is the moment the count
	// on screen should change.
	Downloaded int `json:"downloaded"`
}

// videos rebuilds the documents a summary needs. Only the fields summarize()
// reads for a tally are set; a title or a thumbnail would be dead weight in
// the row, and no summary shows either.
func (a playlistAggregate) videos() []ta.Video {
	out := make([]ta.Video, 0, len(a.Videos))
	for _, v := range a.Videos {
		out = append(out, ta.Video{
			YoutubeID: v.ID,
			Player:    ta.Player{Duration: v.Duration, Watched: v.Watched, Progress: v.Position},
		})
	}
	return out
}

// downloadedEntries counts the playlist entries TubeArchivist holds a file for.
func downloadedEntries(p *ta.Playlist) int {
	n := 0
	for _, e := range p.PlaylistEntries {
		if e.Downloaded {
			n++
		}
	}
	return n
}

func aggregateOf(p *ta.Playlist, videos []ta.Video) playlistAggregate {
	agg := playlistAggregate{
		Videos:     make([]playlistVideoState, 0, len(videos)),
		Downloaded: downloadedEntries(p),
	}
	for _, v := range videos {
		agg.Videos = append(agg.Videos, playlistVideoState{
			ID:       v.YoutubeID,
			Duration: v.Player.Duration,
			Watched:  v.Player.Watched,
			Position: v.Player.Progress,
		})
	}
	return agg
}

// savePlaylistAggregate records what the documents said. has_data is whether
// the playlist holds anything: an empty one is re-checked sooner, because that
// is what a playlist still downloading looks like.
func (s *Server) savePlaylistAggregate(ctx context.Context, p *ta.Playlist, videos []ta.Video) {
	agg := aggregateOf(p, videos)
	s.detachedSave(ctx, sourcePlaylist, p.PlaylistID, agg, len(agg.Videos) > 0)
}

// fetchPlaylistAggregate is the background refresh: re-read the playlist and
// its documents, and store what a summary would want.
func (s *Server) fetchPlaylistAggregate(ctx context.Context, id string) {
	p, err := s.ta.GetPlaylist(ctx, id)
	if err != nil {
		return
	}
	videos, err := s.playlistVideoDocs(ctx, p)
	if err != nil {
		return
	}
	s.savePlaylistAggregate(ctx, p, videos)
}

// playlistAggregateFor resolves a playlist's archive-side state, from the cache
// when it can. A row that is merely old is served and refreshed behind the
// response; a row whose downloaded count no longer matches the playlist is
// wrong about something visible, so it is rebuilt now.
func (s *Server) playlistAggregateFor(ctx context.Context, p *ta.Playlist) ([]ta.Video, error) {
	row, ok := s.cacheLoad(ctx, sourcePlaylist, []string{p.PlaylistID})[p.PlaylistID]
	var agg playlistAggregate
	if ok && row.decode(&agg) && agg.Downloaded == downloadedEntries(p) {
		if !row.fresh(sourcePlaylist, time.Now()) {
			s.cacheQueue(sourcePlaylist, p.PlaylistID)
		}
		return agg.videos(), nil
	}
	videos, err := s.playlistVideoDocs(ctx, p)
	if err != nil {
		return nil, err
	}
	s.savePlaylistAggregate(ctx, p, videos)
	return videos, nil
}

// playlistSummaryOnly builds a summary without fetching the playlist's video
// documents — the archive's half from the cache, the user's half from Postgres.
func (s *Server) playlistSummaryOnly(ctx context.Context, uid uuid.UUID, p *ta.Playlist) (*PlaylistSummary, error) {
	videos, err := s.playlistAggregateFor(ctx, p)
	if err != nil {
		return nil, err
	}
	events, err := s.loadEvents(ctx, uid, videoIDs(videos))
	if err != nil {
		return nil, err
	}
	summaries := make([]VideoSummary, 0, len(videos))
	for _, v := range videos {
		var ev *sqlc.WatchEvent
		if e, found := events[v.YoutubeID]; found {
			ev = &e
		}
		summaries = append(summaries, summarize(v, ev))
	}
	out := playlistShell(p)
	tallyPlaylist(out, summaries)
	return out, nil
}

func videoIDs(videos []ta.Video) []string {
	out := make([]string, 0, len(videos))
	for _, v := range videos {
		out = append(out, v.YoutubeID)
	}
	return out
}
