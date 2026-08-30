package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Seklfreak/flimm/internal/ta"
)

func commentsServer(t *testing.T, tree ta.Comments) http.Handler {
	t.Helper()
	client := ta.NewFake()
	client.AddVideo(video("v1", "A", "2026-08-01", 600, false))
	client.CommentsFn = func(string) (ta.Comments, error) { return tree, nil }
	return newTestServer(client, newEventStore().querier()).Router()
}

func commentsPage(t *testing.T, h http.Handler, query string) Page[Comment] {
	t.Helper()
	rec := do(t, h, http.MethodGet, "/api/v1/videos/v1/comments"+query, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var page Page[Comment]
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	return page
}

// The endpoint is a contract, not a passthrough: upstream's key names stop
// here, so no client ever learns them.
func TestCommentsAreNormalised(t *testing.T) {
	h := commentsServer(t, ta.Comments{{
		ID: "c1", Text: " Worth the wait. ", Author: "@someone", AuthorID: "UC-someone",
		Likes: 128, TimeText: "1 week ago", Timestamp: 1_755_680_400,
		Favorited: true, FromUploader: false,
		Replies: []ta.Comment{{ID: "c1r1", Text: "Agreed", Author: "@other", Likes: 4, FromUploader: true}},
	}})

	page := commentsPage(t, h, "")
	if len(page.Items) != 1 || page.Total != 1 || page.HasMore {
		t.Fatalf("page = %+v", page)
	}
	got := page.Items[0]
	if got.Text != "Worth the wait." {
		t.Errorf("text = %q, want it trimmed", got.Text)
	}
	if got.Author != "@someone" || got.AuthorID != "UC-someone" || got.Likes != 128 {
		t.Errorf("comment = %+v", got)
	}
	if !got.Hearted {
		t.Error("a hearted comment must say so")
	}
	if got.Published == nil || got.Published.Year() != 2025 {
		t.Errorf("published = %v, want the timestamp as a date", got.Published)
	}
	if got.TimeText != "1 week ago" {
		t.Errorf("time_text = %q, want it kept as the fallback", got.TimeText)
	}
	if len(got.Replies) != 1 || !got.Replies[0].FromUploader {
		t.Errorf("replies = %+v, want the uploader's reply carried with its parent", got.Replies)
	}
}

// An archive that kept no timestamp still has something to show.
func TestACommentWithoutATimestampHasNoPublishedDate(t *testing.T) {
	h := commentsServer(t, ta.Comments{{ID: "c1", Text: "First.", Author: "@early", TimeText: "2 days ago"}})
	got := commentsPage(t, h, "").Items[0]
	if got.Published != nil {
		t.Errorf("published = %v, want nil rather than 1970", got.Published)
	}
	if got.TimeText != "2 days ago" {
		t.Errorf("time_text = %q", got.TimeText)
	}
}

// A record with neither text nor author is a half-indexed row, not a comment.
func TestEmptyCommentsAreDropped(t *testing.T) {
	h := commentsServer(t, ta.Comments{
		{ID: "c1", Text: "Real one", Author: "@someone"},
		{ID: "c2"},
		{ID: "c3", Text: "   "},
	})
	page := commentsPage(t, h, "")
	if len(page.Items) != 1 || page.Items[0].ID != "c1" {
		t.Errorf("items = %+v, want only the comment with something in it", page.Items)
	}
}

// Threads page; replies do not — a reply on its own says nothing.
func TestCommentsPageByThread(t *testing.T) {
	var tree ta.Comments
	for i := range 7 {
		tree = append(tree, ta.Comment{
			ID: string(rune('a' + i)), Text: "comment", Author: "@someone",
			Replies: []ta.Comment{{ID: "r", Text: "reply", Author: "@other"}},
		})
	}
	h := commentsServer(t, tree)

	first := commentsPage(t, h, "?page_size=3")
	if len(first.Items) != 3 || !first.HasMore || first.Total != 7 {
		t.Fatalf("first page = %+v", first)
	}
	if len(first.Items[0].Replies) != 1 {
		t.Error("a thread's replies travel with it on every page")
	}
	last := commentsPage(t, h, "?page_size=3&page=2")
	if len(last.Items) != 1 || last.HasMore {
		t.Errorf("last page = %+v", last)
	}
	past := commentsPage(t, h, "?page_size=3&page=9")
	if len(past.Items) != 0 || past.HasMore {
		t.Errorf("past the end = %+v, want an empty page rather than an error", past)
	}
}

// A video nobody archived comments for is not an error.
func TestNoCommentsIsAnEmptyPage(t *testing.T) {
	h := commentsServer(t, ta.Comments{})
	page := commentsPage(t, h, "")
	if len(page.Items) != 0 || page.HasMore || page.Total != 0 {
		t.Errorf("page = %+v", page)
	}
}

// The exact key set a real TubeArchivist archive returns, checked against a
// live one: `comment_author_is_uploader` (not `comment_is_uploader`, which is
// what a reasonable guess produces and what shipped first), the favourited
// flag, a Unix `comment_timestamp`, and replies nested under the same shape.
//
// A wrong key here does not fail anything — it decodes as false, or zero, on
// every comment in the archive, which is exactly the kind of bug that survives
// a test suite built only on a fake.
func TestCommentsDecodeTheArchivesOwnKeys(t *testing.T) {
	raw := `[{
	  "comment_id": "Ug123",
	  "comment_text": "Worth the wait.",
	  "comment_author": "@someone",
	  "comment_author_id": "UCsomeone",
	  "comment_author_is_uploader": false,
	  "comment_author_thumbnail": "https://yt3.ggpht.com/…",
	  "comment_is_favorited": true,
	  "comment_likecount": 128,
	  "comment_parent": "root",
	  "comment_time_text": "1 week ago",
	  "comment_timestamp": 1755680400,
	  "comment_replies": [{
	    "comment_id": "Ug123.1",
	    "comment_text": "Thanks!",
	    "comment_author": "@the-channel",
	    "comment_author_id": "UCchannel",
	    "comment_author_is_uploader": true,
	    "comment_is_favorited": false,
	    "comment_likecount": 9,
	    "comment_parent": "Ug123",
	    "comment_time_text": "6 days ago",
	    "comment_timestamp": 1755766800,
	    "comment_replies": []
	  }]
	}]`
	var tree ta.Comments
	if err := json.Unmarshal([]byte(raw), &tree); err != nil {
		t.Fatal(err)
	}
	got := commentList(tree)
	if len(got) != 1 || len(got[0].Replies) != 1 {
		t.Fatalf("comments = %+v", got)
	}
	if !got[0].Hearted {
		t.Error("comment_is_favorited did not reach `hearted`")
	}
	if got[0].FromUploader {
		t.Error("a viewer's comment was marked as the uploader's")
	}
	if !got[0].Replies[0].FromUploader {
		t.Error("comment_author_is_uploader did not reach `from_uploader` — the archive's spelling changed or was guessed")
	}
	if got[0].Published == nil {
		t.Error("comment_timestamp did not reach `published`")
	}
	if got[0].Likes != 128 || got[0].AuthorID != "UCsomeone" {
		t.Errorf("comment = %+v", got[0])
	}
}
