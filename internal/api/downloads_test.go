package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Seklfreak/flimm/internal/ta"
)

func queued(id, channel, message string) ta.DownloadItem {
	return ta.DownloadItem{
		YoutubeID: id, Title: "Queued " + id, ChannelID: channel,
		ChannelName: "Channel " + channel, VidType: "videos",
		Duration: 600, Status: "pending", Message: message,
	}
}

func downloadsFixture(t *testing.T) (*Server, *ta.Fake) {
	t.Helper()
	client := ta.NewFake()
	client.Channels["UC1"] = &ta.Channel{ChannelID: "UC1", ChannelName: "One"}
	return newTestServer(client, newEventStore().querier()), client
}

// The split the whole view exists for: a stored error message hides an item
// from TubeArchivist's downloader forever, so a queue of 3 that can only ever
// move on 1 of them must not report itself as 3 pending.
func TestDownloadCountsSeparateReadyFromBlocked(t *testing.T) {
	s, client := downloadsFixture(t)
	client.Queue = []ta.DownloadItem{
		queued("q1", "UC1", ""),
		queued("q2", "UC1", "[Errno 28] No space left on device"),
		queued("q3", "UC1", "[Errno 28] No space left on device"),
		{YoutubeID: "q4", ChannelID: "UC1", Status: "ignore"},
	}

	rec := do(t, s.Router(), http.MethodGet, "/api/v1/admin/downloads", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	got := decode[DownloadsResponse](t, rec)
	switch {
	case got.Counts.Ready != 1:
		t.Errorf("ready = %d, want the 1 item the downloader can pick up", got.Counts.Ready)
	case got.Counts.Blocked != 2:
		t.Errorf("blocked = %d, want the 2 masked by a message", got.Counts.Blocked)
	case got.Counts.Ignored != 1:
		t.Errorf("ignored = %d", got.Counts.Ignored)
	}
}

// Blocked items lead the list, and each carries the message that blocked it:
// a queue only needs reading when something is stuck, and then the reason is
// the thing being looked for.
func TestBlockedItemsAreListedFirstWithTheirReason(t *testing.T) {
	s, client := downloadsFixture(t)
	client.Queue = []ta.DownloadItem{
		queued("ready1", "UC1", ""),
		queued("ready2", "UC1", ""),
		queued("stuck", "UC1", "HTTP Error 403: Forbidden"),
	}

	got := decode[DownloadsResponse](t, do(t, s.Router(), http.MethodGet, "/api/v1/admin/downloads", ""))
	if len(got.Pending) != 3 {
		t.Fatalf("pending = %d, want all three", len(got.Pending))
	}
	if got.Pending[0].ID != "stuck" {
		t.Errorf("first pending = %q, want the blocked one", got.Pending[0].ID)
	}
	if got.Pending[0].Error != "HTTP Error 403: Forbidden" {
		t.Errorf("error = %q, want TubeArchivist's own message", got.Pending[0].Error)
	}
	if got.Pending[1].Error != "" {
		t.Errorf("ready item carries error %q", got.Pending[1].Error)
	}
}

// TubeArchivist's progress line holds the rate and the ETA and nothing else
// does, so it is passed through whole; the fraction is the only number.
func TestActiveDownloadReportsTAsOwnProgressLine(t *testing.T) {
	s, client := downloadsFixture(t)
	frac := 0.452
	client.Downloading = []ta.Notification{{
		ID: "download:v1", Title: "Downloading", Level: "info",
		Messages: []string{"A video title", "45.2% of 120MiB at 3.5MiB/s - time left: 00:32"},
		Progress: &frac,
	}}

	got := decode[DownloadsResponse](t, do(t, s.Router(), http.MethodGet, "/api/v1/admin/downloads", ""))
	if len(got.Active) != 1 {
		t.Fatalf("active = %d, want 1", len(got.Active))
	}
	a := got.Active[0]
	switch {
	case a.Title != "A video title":
		t.Errorf("title = %q, want the message's own first line", a.Title)
	case a.Detail != "45.2% of 120MiB at 3.5MiB/s - time left: 00:32":
		t.Errorf("detail = %q, want TA's line unparsed", a.Detail)
	case a.Progress == nil || *a.Progress != frac:
		t.Errorf("progress = %v, want %v", a.Progress, frac)
	}
}

// A step that cannot say how far along it is must report no progress at all
// rather than 0%, which reads as a download that has stalled at the start.
func TestProgressIsNullWhenTubeArchivistSendsNone(t *testing.T) {
	s, client := downloadsFixture(t)
	client.Downloading = []ta.Notification{{
		ID: "download:v1", Title: "Post processing", Messages: []string{"Validating playlists"},
	}}

	got := decode[DownloadsResponse](t, do(t, s.Router(), http.MethodGet, "/api/v1/admin/downloads", ""))
	if len(got.Active) != 1 {
		t.Fatalf("active = %d, want 1", len(got.Active))
	}
	switch {
	case got.Active[0].Progress != nil:
		t.Errorf("progress = %v, want null", *got.Active[0].Progress)
	case got.Active[0].Detail != "":
		t.Errorf("detail = %q, want empty when TA sent only a title", got.Active[0].Detail)
	case got.Active[0].Level != "info":
		t.Errorf("level = %q, want the info default", got.Active[0].Level)
	}
}

// An idle archive is an empty list, not an error: TA's progress messages
// expire seconds after the work does.
func TestNothingDownloadingIsAnEmptyList(t *testing.T) {
	s, _ := downloadsFixture(t)
	got := decode[DownloadsResponse](t, do(t, s.Router(), http.MethodGet, "/api/v1/admin/downloads", ""))
	if len(got.Active) != 0 || len(got.Pending) != 0 {
		t.Errorf("idle archive = %+v, want empty lists", got)
	}
}

// Instance-wide state naming channels nobody in particular subscribed to.
func TestDownloadsAreAdminOnly(t *testing.T) {
	s, _ := downloadsFixture(t)
	if rec := do(t, s.Router(), http.MethodGet, "/api/v1/admin/downloads", ""); rec.Code != http.StatusOK {
		t.Fatalf("admin status = %d", rec.Code)
	}

	ctx := context.WithValue(context.Background(), userIDKey, DevUserID)
	ctx = context.WithValue(ctx, isAdminKey, false)
	w := httptest.NewRecorder()
	s.getDownloads(w, httptest.NewRequest(http.MethodGet, "/api/v1/admin/downloads", nil).WithContext(ctx))
	if w.Code != http.StatusForbidden {
		t.Errorf("non-admin status = %d, want 403", w.Code)
	}
}

// The badge every client shows: how much of this channel is still coming.
func TestChannelDetailCarriesItsQueueDepth(t *testing.T) {
	s, client := downloadsFixture(t)
	client.Queue = []ta.DownloadItem{
		queued("q1", "UC1", ""),
		queued("q2", "UC1", "stuck"),
		queued("q3", "UC2", ""),
		{YoutubeID: "q4", ChannelID: "UC1", Status: "ignore"},
	}

	rec := do(t, s.Router(), http.MethodGet, "/api/v1/channels/UC1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	// Blocked items are still waiting, so they count; an ignored one is not
	// waiting for anything, and another channel's queue is not this one's.
	if got := decode[ChannelDetail](t, rec).QueuedCount; got != 2 {
		t.Errorf("queued_count = %d, want 2", got)
	}
}
