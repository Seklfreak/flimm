package faketa

import (
	"fmt"
	"math"
	"net/http"
	"time"

	"github.com/Seklfreak/flimm/internal/ta"
)

// The download queue, and the progress of whatever is being fetched.
//
// Both are derived, not stored: the queue is a deterministic handful of
// not-yet-archived videos per channel, and the progress is a function of the
// clock, so a bar drawn against this stand-in actually moves and a developer
// can see the panel doing its job without a real archive downloading anything.
//
// One queued item per channel carries an error message. That is not decoration:
// an item with a message is invisible to the real downloader forever, and the
// view exists mostly to show that state, so the fake must produce it.

// queueDepth is how many items the stand-in queues per channel.
const queueDepth = 3

// downloadCycle is how long a fake download takes from 0 to 100%.
const downloadCycle = 90 * time.Second

func (s *Server) downloadQueue() []ta.DownloadItem {
	out := []ta.DownloadItem{}
	for _, c := range s.catalogue.Channels {
		for i := range queueDepth {
			it := ta.DownloadItem{
				YoutubeID:   fmt.Sprintf("q%s%d", c.ChannelID[len(c.ChannelID)-3:], i),
				Title:       fmt.Sprintf("%s — queued upload %d", c.ChannelName, i+1),
				ChannelID:   c.ChannelID,
				ChannelName: c.ChannelName,
				VidType:     "videos",
				Duration:    420 + i*137,
				Published:   time.Now().AddDate(0, 0, -i).Format("2006-01-02"),
				Status:      "pending",
				AutoStart:   i == 0,
				Timestamp:   time.Now().Add(-time.Duration(i) * time.Hour).Unix(),
			}
			// The last of each channel's items is the stuck kind.
			if i == queueDepth-1 {
				it.Message = "[Errno 28] No space left on device"
			}
			out = append(out, it)
		}
	}
	return out
}

func (s *Server) listDownloads(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	items := s.downloadQueue()
	// The ignore list is empty here: nothing in the stand-in ever ignores a
	// video, and a filter TA knows must still answer rather than 404.
	if q.Get("filter") == "ignore" {
		items = nil
	}
	if channel := q.Get("channel"); channel != "" {
		items = filter(items, func(it ta.DownloadItem) bool { return it.ChannelID == channel })
	}
	switch q.Get("error") {
	case "true":
		items = filter(items, func(it ta.DownloadItem) bool { return it.Message != "" })
	case "false":
		items = filter(items, func(it ta.DownloadItem) bool { return it.Message == "" })
	}
	const pageSize = PageSize
	page := intParam(q.Get("page"), 1)
	total := len(items)
	start := min((page-1)*pageSize, total)
	end := min(start+pageSize, total)
	last := 0
	if total > 0 {
		last = (total + pageSize - 1) / pageSize
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data": items[start:end],
		"paginate": ta.Paginate{
			PageSize:    pageSize,
			PageFrom:    start,
			CurrentPage: page,
			LastPage:    last,
			TotalHits:   total,
		},
	})
}

// notifications answers TA's /api/notification/: the live progress messages,
// which in the real archive live in Redis for seconds at a time.
//
// The download group reports the first queued item, advancing through
// downloadCycle on the wall clock and starting over — an archive that is
// always busy, which is the state worth looking at. Any other group is idle,
// the way TA answers when nothing of that kind is running.
func (s *Server) notifications(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("filter") != "download" {
		writeJSON(w, http.StatusOK, []ta.Notification{})
		return
	}
	queue := s.downloadQueue()
	if len(queue) == 0 {
		writeJSON(w, http.StatusOK, []ta.Notification{})
		return
	}
	it := queue[0]
	frac := float64(time.Now().UnixNano()%int64(downloadCycle)) / float64(downloadCycle)
	total := 120.0 // MiB
	rate := total / downloadCycle.Seconds()
	left := time.Duration((1-frac)*downloadCycle.Seconds()) * time.Second
	writeJSON(w, http.StatusOK, []ta.Notification{{
		ID:    "download:" + it.YoutubeID,
		Title: "Downloading",
		Group: "download:" + it.YoutubeID,
		Level: "info",
		Messages: []string{
			it.Title,
			fmt.Sprintf("%.1f%% of %.0fMiB at %.1fMiB/s - time left: %02d:%02d",
				frac*100, total, rate,
				int(left.Minutes()), int(math.Mod(left.Seconds(), 60))),
		},
		Progress: &frac,
	}})
}
