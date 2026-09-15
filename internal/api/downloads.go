package api

import (
	"context"
	"net/http"

	"golang.org/x/sync/errgroup"

	"github.com/Seklfreak/flimm/internal/ta"
)

// What the archive is fetching right now.
//
// TubeArchivist knows two separate things about a download, and a view that
// shows either one alone misleads. The *progress* — a title and a percentage,
// written to Redis by yt-dlp's hook and expiring seconds later — says
// something is moving; it says nothing about whether anything else ever will.
// The *queue* says how much is waiting, and it hides the failure that actually
// bites: the downloader picks work with `must_not: exists(message)`, so an item
// that has ever recorded an error is invisible to it forever, with nothing to
// retry it and nothing to clear the field. A cache filling up once stamped
// "No space left on device" onto 933 queued videos, and the queue total sat
// unchanged for three weeks — a number that big looks like a healthy backlog.
//
// So the counts are split the way the downloader itself splits them: `ready`
// is what it is willing to pick up, `blocked` is what a stored message has
// masked from it. Blocked items are listed before ready ones for the same
// reason — the ones that need a person are the ones worth reading.
//
// Admin-only, like the rest of the Server page: this is instance-wide state,
// and it names channels no particular viewer subscribed to.

// DownloadsResponse is GET /api/v1/admin/downloads.
type DownloadsResponse struct {
	// Active is what TA reports in flight, empty when nothing is downloading.
	Active []DownloadActivity `json:"active"`
	Counts DownloadCounts     `json:"counts"`
	// Pending is the head of the queue, blocked items first.
	Pending []QueuedItem `json:"pending"`
}

// DownloadActivity is one live progress message.
//
// Detail is TubeArchivist's own line — "45.2% of 120MiB at 3.5MiB/s - time
// left: 00:32" — passed through as it stands. The rate and the ETA exist
// nowhere else: TA formats them into that string inside the progress hook and
// reports only the fraction as a number. Parsing it back into fields would be
// inventing a contract out of somebody's format string, and it would break
// quietly the first time they changed it.
type DownloadActivity struct {
	Title string `json:"title"`
	// Detail is empty when TA sent only a title.
	Detail string `json:"detail"`
	// Progress is 0–1, null when TA reported none (a step that cannot say
	// how far along it is, which is most of post-processing).
	Progress *float64 `json:"progress"`
	// Level is "info" or "error".
	Level string `json:"level"`
}

// DownloadCounts is the queue split the way the downloader sees it.
type DownloadCounts struct {
	// Ready is pending and pickable.
	Ready int `json:"ready"`
	// Blocked is pending but masked by a stored error message.
	Blocked int `json:"blocked"`
	// Ignored is the ignore list.
	Ignored int `json:"ignored"`
}

// QueuedItem is one waiting video.
//
// There is no thumbnail. A queued video is not in TubeArchivist's video index
// yet, so Flimm's media proxy has nothing to serve for it, and the artwork TA
// carries on the queue entry is a YouTube URL — a maintenance view is not
// worth sending a viewer's browser to youtube.com for.
type QueuedItem struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	ChannelID   string `json:"channel_id"`
	ChannelName string `json:"channel_name"`
	// Seconds is the video's duration, 0 when TA does not know it.
	Seconds int `json:"seconds"`
	// Kind is "video", "short" or "stream".
	Kind string `json:"kind"`
	// AutoStart marks an item queued to jump the line.
	AutoStart bool `json:"auto_start"`
	// Error is the stored message that blocks the item, empty when ready.
	Error string `json:"error"`
}

// maxPendingListed caps the queue sample. The counts answer "how much", and a
// queue this feature exists to diagnose has hundreds of entries; the list is
// there to show *what*, and a page of it is enough to recognise the pattern.
const maxPendingListed = 24

func (s *Server) getDownloads(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r.Context()) {
		writeError(w, http.StatusForbidden, "admin only")
		return
	}
	out, err := s.downloadsSnapshot(r.Context())
	if err != nil {
		s.writeTAError(w, "list downloads", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// downloadsSnapshot reads the four independent things the view is made of at
// once. They are independent, and this is re-read every few seconds by a page
// watching a bar move: four round trips in series is the difference between a
// panel that keeps up and one that lags behind the thing it describes.
func (s *Server) downloadsSnapshot(ctx context.Context) (*DownloadsResponse, error) {
	var (
		progress                []ta.Notification
		blocked, ready, ignored *ta.DownloadPage
	)
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() (err error) { progress, err = s.ta.DownloadProgress(gctx); return })
	g.Go(func() (err error) {
		blocked, err = s.ta.DownloadQueue(gctx, ta.DownloadQuery{Filter: "pending", Error: ta.ErrorYes})
		return
	})
	g.Go(func() (err error) {
		ready, err = s.ta.DownloadQueue(gctx, ta.DownloadQuery{Filter: "pending", Error: ta.ErrorNo})
		return
	})
	g.Go(func() (err error) {
		ignored, err = s.ta.DownloadQueue(gctx, ta.DownloadQuery{Filter: "ignore"})
		return
	})
	if err := g.Wait(); err != nil {
		return nil, err
	}
	out := &DownloadsResponse{
		Active: activityOf(progress),
		Counts: DownloadCounts{
			Ready:   ready.Paginate.TotalHits,
			Blocked: blocked.Paginate.TotalHits,
			Ignored: ignored.Paginate.TotalHits,
		},
		Pending: []QueuedItem{},
	}
	for _, it := range append(append([]ta.DownloadItem{}, blocked.Data...), ready.Data...) {
		if len(out.Pending) == maxPendingListed {
			break
		}
		out.Pending = append(out.Pending, queuedItemOf(it))
	}
	return out, nil
}

func activityOf(msgs []ta.Notification) []DownloadActivity {
	out := make([]DownloadActivity, 0, len(msgs))
	for _, m := range msgs {
		a := DownloadActivity{Title: m.Title, Progress: m.Progress, Level: m.Level}
		// TA sends messages as [title, detail]; either half may be missing,
		// and the notification's own title is the fallback for the first.
		if len(m.Messages) > 0 && m.Messages[0] != "" {
			a.Title = m.Messages[0]
		}
		if len(m.Messages) > 1 {
			a.Detail = m.Messages[1]
		}
		if a.Level == "" {
			a.Level = "info"
		}
		out = append(out, a)
	}
	return out
}

func queuedItemOf(it ta.DownloadItem) QueuedItem {
	return QueuedItem{
		ID:          it.YoutubeID,
		Title:       it.Title,
		ChannelID:   it.ChannelID,
		ChannelName: it.ChannelName,
		Seconds:     it.Duration,
		Kind:        it.Kind(),
		AutoStart:   it.AutoStart,
		Error:       it.Message,
	}
}
