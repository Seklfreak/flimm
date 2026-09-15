package faketa_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Seklfreak/flimm/internal/faketa"
	"github.com/Seklfreak/flimm/internal/ta"
)

// The fake is only worth anything if the real client can read it, so every
// test here drives ta.HTTP — the same code the server uses — rather than
// asserting on JSON by hand.
func fixture(t *testing.T) (ta.Client, *faketa.Catalogue) {
	t.Helper()
	catalogue := faketa.NewCatalogue()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	// No media generation: these tests are about the API, and ffmpeg is not a
	// test dependency.
	srv := httptest.NewServer(faketa.NewServer(catalogue, faketa.NewMedia(t.TempDir(), "ffmpeg", log), log).Handler())
	t.Cleanup(srv.Close)
	return ta.New(srv.URL, "dev-token"), catalogue
}

func TestPingAndListing(t *testing.T) {
	client, catalogue := fixture(t)
	ctx := context.Background()

	if err := client.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}
	page, err := client.ListVideos(ctx, ta.VideoQuery{PageSize: 100})
	if err != nil {
		t.Fatalf("list videos: %v", err)
	}
	if page.Paginate.TotalHits != len(catalogue.Videos) {
		t.Errorf("total_hits = %d, want %d", page.Paginate.TotalHits, len(catalogue.Videos))
	}
	// One page only: TA ignores the requested page_size and uses its own, so
	// total_hits is the whole catalogue while data is a single page of it.
	if len(page.Data) != faketa.PageSize {
		t.Fatalf("got %d videos, want one page of %d", len(page.Data), faketa.PageSize)
	}
	// Newest first, the way every list in the app expects.
	for i := 1; i < len(page.Data); i++ {
		if page.Data[i-1].Published < page.Data[i].Published {
			t.Errorf("not sorted newest first at %d: %q then %q", i, page.Data[i-1].Published, page.Data[i].Published)
		}
	}
}

func TestPaginationWalksTheWholeCatalogue(t *testing.T) {
	client, catalogue := fixture(t)
	seen := map[string]bool{}
	for page := 1; page <= 10; page++ {
		got, err := client.ListVideos(context.Background(), ta.VideoQuery{PageSize: 3, Page: page})
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		for _, v := range got.Data {
			if seen[v.YoutubeID] {
				t.Errorf("video %s served twice", v.YoutubeID)
			}
			seen[v.YoutubeID] = true
		}
		if page >= got.Paginate.LastPage {
			break
		}
	}
	if len(seen) != len(catalogue.Videos) {
		t.Errorf("walked %d videos, want %d", len(seen), len(catalogue.Videos))
	}
}

func TestVideoDetailCarriesWhatTheAppNeeds(t *testing.T) {
	client, catalogue := fixture(t)
	want := catalogue.Videos[0]

	v, err := client.GetVideo(context.Background(), want.YoutubeID)
	if err != nil {
		t.Fatalf("get video: %v", err)
	}
	if v.Title != want.Title || v.Channel.ChannelID != want.Channel.ChannelID {
		t.Errorf("video = %+v", v)
	}
	if v.Player.Duration <= 0 || len(v.Streams) == 0 || len(v.Subtitles) == 0 {
		t.Errorf("video is missing playback data: %+v", v)
	}
	if _, err := client.GetVideo(context.Background(), "nope"); err == nil {
		t.Error("want an error for an unknown id")
	}
}

func TestWatchedAndProgressAreRemembered(t *testing.T) {
	client, catalogue := fixture(t)
	ctx := context.Background()
	id := catalogue.Videos[0].YoutubeID

	if err := client.SetWatched(ctx, id, true); err != nil {
		t.Fatalf("set watched: %v", err)
	}
	v, err := client.GetVideo(ctx, id)
	if err != nil {
		t.Fatalf("get video: %v", err)
	}
	if !v.Player.Watched {
		t.Error("watched flag did not stick")
	}
	// Unwatched listing must drop it, which is what the unseen counts read.
	page, err := client.ListVideos(ctx, ta.VideoQuery{Watch: "unwatched", PageSize: 100})
	if err != nil {
		t.Fatalf("list unwatched: %v", err)
	}
	for _, got := range page.Data {
		if got.YoutubeID == id {
			t.Error("a watched video came back from the unwatched list")
		}
	}

	if err := client.SetProgress(ctx, id, 12); err != nil {
		t.Fatalf("set progress: %v", err)
	}
	if v, err = client.GetVideo(ctx, id); err != nil || v.Player.Progress <= 0 {
		t.Errorf("progress = %v, err = %v", v.Player.Progress, err)
	}
	if err := client.DeleteProgress(ctx, id); err != nil {
		t.Fatalf("delete progress: %v", err)
	}
}

func TestChannelsAndPerChannelListing(t *testing.T) {
	client, catalogue := fixture(t)
	ctx := context.Background()

	channels, err := client.ListChannels(ctx)
	if err != nil {
		t.Fatalf("list channels: %v", err)
	}
	if len(channels) != len(catalogue.Channels) {
		t.Fatalf("got %d channels, want %d", len(channels), len(catalogue.Channels))
	}
	first := channels[0]
	if _, err = client.GetChannel(ctx, first.ChannelID); err != nil {
		t.Fatalf("get channel: %v", err)
	}
	page, err := client.ListVideos(ctx, ta.VideoQuery{Channel: first.ChannelID, PageSize: 100})
	if err != nil {
		t.Fatalf("channel videos: %v", err)
	}
	if len(page.Data) == 0 {
		t.Fatal("channel has no videos")
	}
	for _, v := range page.Data {
		if v.Channel.ChannelID != first.ChannelID {
			t.Errorf("video %s is from %s", v.YoutubeID, v.Channel.ChannelID)
		}
	}
	// What the sidebar's unseen badge reads.
	if _, err := client.UnseenCount(ctx, first.ChannelID); err != nil {
		t.Errorf("unseen count: %v", err)
	}
}

func TestPlaylistsIncludingACustomOne(t *testing.T) {
	client, _ := fixture(t)
	ctx := context.Background()

	lists, err := client.ListPlaylists(ctx, "", "")
	if err != nil {
		t.Fatalf("list playlists: %v", err)
	}
	if len(lists) < 2 {
		t.Fatalf("got %d playlists", len(lists))
	}

	created, err := client.CreateCustomPlaylist(ctx, "Test list")
	if err != nil {
		t.Fatalf("create playlist: %v", err)
	}
	page, err := client.ListVideos(ctx, ta.VideoQuery{PageSize: 1})
	if err != nil || len(page.Data) == 0 {
		t.Fatalf("need a video: %v", err)
	}
	videoID := page.Data[0].YoutubeID
	if err := client.CustomPlaylistAction(ctx, created.PlaylistID, "create", videoID); err != nil {
		t.Fatalf("add to playlist: %v", err)
	}
	got, err := client.GetPlaylist(ctx, created.PlaylistID)
	if err != nil {
		t.Fatalf("get playlist: %v", err)
	}
	if len(got.PlaylistEntries) != 1 || got.PlaylistEntries[0].YoutubeID != videoID {
		t.Fatalf("entries = %+v", got.PlaylistEntries)
	}
	if err := client.CustomPlaylistAction(ctx, created.PlaylistID, "remove", videoID); err != nil {
		t.Fatalf("remove from playlist: %v", err)
	}
	if got, err = client.GetPlaylist(ctx, created.PlaylistID); err != nil || len(got.PlaylistEntries) != 0 {
		t.Fatalf("entries after remove = %+v, err = %v", got, err)
	}
	if err := client.DeletePlaylist(ctx, created.PlaylistID); err != nil {
		t.Fatalf("delete playlist: %v", err)
	}
}

func TestSearchBucketsAndSubtitleHits(t *testing.T) {
	client, catalogue := fixture(t)
	ctx := context.Background()

	result, err := client.Search(ctx, catalogue.Videos[0].Title)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(result.Videos) == 0 {
		t.Error("no video results for an exact title")
	}
	channels, err := client.Search(ctx, "channel:Workshop")
	if err != nil {
		t.Fatalf("channel search: %v", err)
	}
	if len(channels.Channels) == 0 || len(channels.Videos) != 0 {
		t.Errorf("prefixed search leaked buckets: %+v", channels)
	}
	// The subtitle hit is what "search jumps to a timestamp" is built on.
	full, err := client.Search(ctx, "full:this line starts at 0:20")
	if err != nil {
		t.Fatalf("fulltext search: %v", err)
	}
	if len(full.Fulltext) == 0 {
		t.Fatal("no subtitle hits")
	}
	if full.Fulltext[0].SubtitleStart != 20 {
		t.Errorf("hit at %v, want 20", full.Fulltext[0].SubtitleStart)
	}
}

// Real TA crashes with a 500 on any word holding two colons; the fake
// mirrors that so an unsanitized query fails in tests the way it fails in
// production.
func TestSearchColonWordCrashesLikeTA(t *testing.T) {
	client, _ := fixture(t)

	_, err := client.Search(context.Background(), "video:re:zero")
	if !errors.Is(err, ta.ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable (TA 500)", err)
	}
	// One colon per word is fine, exactly like real TA.
	if _, err := client.Search(context.Background(), "video:foo 10:30"); err != nil {
		t.Fatalf("single-colon word: %v", err)
	}
}

func TestSimilarAndComments(t *testing.T) {
	client, catalogue := fixture(t)
	ctx := context.Background()
	id := catalogue.Videos[0].YoutubeID

	similar, err := client.SimilarVideos(ctx, id)
	if err != nil {
		t.Fatalf("similar: %v", err)
	}
	for _, v := range similar {
		if v.YoutubeID == id {
			t.Error("a video is similar to itself")
		}
	}
	comments, err := client.Comments(ctx, id)
	if err != nil || len(comments) == 0 {
		t.Fatalf("comments = %+v, err = %v", comments, err)
	}
	// The tree is what makes the fixture worth having: a thread with replies,
	// one of them from the uploader.
	if len(comments[0].Replies) == 0 {
		t.Error("the first comment should carry its replies")
	}
	if !comments[0].Favorited {
		t.Error("the first comment should be hearted, so a client can show that")
	}
}

func TestSubtitlesAreServedAsWebVTT(t *testing.T) {
	client, catalogue := fixture(t)
	v := catalogue.Videos[0]

	body, err := client.FetchRange(context.Background(), "/media/"+v.Subtitles[0].MediaURL, 0, 4095)
	if err != nil {
		t.Fatalf("fetch subtitles: %v", err)
	}
	if string(body[:6]) != "WEBVTT" {
		t.Errorf("subtitles start with %q", string(body[:6]))
	}
}

// The stand-in has to produce the queue's interesting state, not just a list:
// items the downloader can pick up, and items a stored message has masked from
// it forever. A Downloads panel developed against a queue with no blocked item
// in it would never show the thing it exists to show.
func TestDownloadQueueHasBlockedAndReadyItems(t *testing.T) {
	client, _ := fixture(t)
	ctx := context.Background()

	ready, err := client.DownloadQueue(ctx, ta.DownloadQuery{Filter: "pending", Error: ta.ErrorNo})
	if err != nil {
		t.Fatalf("ready queue: %v", err)
	}
	blocked, err := client.DownloadQueue(ctx, ta.DownloadQuery{Filter: "pending", Error: ta.ErrorYes})
	if err != nil {
		t.Fatalf("blocked queue: %v", err)
	}
	if ready.Paginate.TotalHits == 0 || blocked.Paginate.TotalHits == 0 {
		t.Fatalf("ready = %d, blocked = %d; want both", ready.Paginate.TotalHits, blocked.Paginate.TotalHits)
	}
	for _, it := range blocked.Data {
		if it.Message == "" {
			t.Errorf("%q is in the blocked half with no message", it.YoutubeID)
		}
	}
	for _, it := range ready.Data {
		if it.Message != "" {
			t.Errorf("%q is in the ready half carrying %q", it.YoutubeID, it.Message)
		}
	}
}

// A channel's badge reads its own queue, so the filter has to hold.
func TestDownloadQueueFiltersByChannel(t *testing.T) {
	client, catalogue := fixture(t)
	id := catalogue.Channels[0].ChannelID

	n, err := client.QueuedCount(context.Background(), id)
	if err != nil {
		t.Fatalf("queued count: %v", err)
	}
	if n == 0 {
		t.Fatal("queued count = 0, want the channel's own pending items")
	}
	page, err := client.DownloadQueue(context.Background(), ta.DownloadQuery{Filter: "pending", Channel: id})
	if err != nil {
		t.Fatalf("queue: %v", err)
	}
	for _, it := range page.Data {
		if it.ChannelID != id {
			t.Errorf("channel %q in a queue filtered to %q", it.ChannelID, id)
		}
	}
}

// The progress message is what makes a bar move in local development, and it
// has to carry TubeArchivist's own detail line — the rate and the ETA live
// nowhere else.
func TestProgressReportsADownloadInFlight(t *testing.T) {
	client, _ := fixture(t)

	msgs, err := client.DownloadProgress(context.Background())
	if err != nil {
		t.Fatalf("progress: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("progress messages = %d, want 1", len(msgs))
	}
	m := msgs[0]
	if m.Progress == nil || *m.Progress < 0 || *m.Progress > 1 {
		t.Errorf("progress = %v, want a fraction", m.Progress)
	}
	if len(m.Messages) != 2 || !strings.Contains(m.Messages[1], "time left:") {
		t.Errorf("messages = %q, want a title and TA's own detail line", m.Messages)
	}
}
