package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Seklfreak/flimm/internal/ta"
)

// Comments, as a contract rather than a passthrough.
//
// This endpoint used to hand TubeArchivist's own JSON straight to whoever
// asked — `comment_likecount`, `comment_time_text` and all — which made every
// client a second parser of somebody else's shape. Four clients reading raw
// upstream keys is exactly the drift the rest of this API exists to prevent,
// so the tree is normalised here: the same object as everywhere else, and one
// place to fix when an archive turns out to have spelled something differently.
//
// Nothing is fetched from YouTube. These are the comments TubeArchivist
// downloaded with the video, which is the only reason showing them is free of
// asking a third party anything — and it is why an author's avatar is *not*
// carried: the URL in the archive points at Google's CDN, and putting it in a
// client would have every viewer's browser announce every video they open. A
// name and its first letter is the whole of an identity here.

// getVideoComments answers GET /videos/{id}/comments.
//
// Top-level comments are paged; a comment's replies ride along with it. A
// thread is a unit — a reply on its own says nothing — and they are few
// enough per comment that paging them would cost more requests than it saves
// bytes.
func (s *Server) getVideoComments(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	tree, err := s.ta.Comments(r.Context(), id)
	if err != nil {
		s.writeTAError(w, "video comments", err)
		return
	}
	// The whole tree is in hand, so the window is exact and `total` is the
	// real number of threads — unlike the lazily composed lists, where it is
	// a floor.
	writeJSON(w, http.StatusOK, slicePage(commentList(tree), parsePaging(r)))
}

// commentList converts a TA tree, dropping anything that has nothing to say.
func commentList(tree []ta.Comment) []Comment {
	out := make([]Comment, 0, len(tree))
	for _, c := range tree {
		if converted, ok := comment(c); ok {
			out = append(out, converted)
		}
	}
	return out
}

// comment converts one comment and its replies. The bool is false for a
// comment with neither text nor author, which is what a half-indexed record
// looks like and is not worth a blank row in a list.
func comment(c ta.Comment) (Comment, bool) {
	text := strings.TrimSpace(c.Text)
	author := strings.TrimSpace(c.Author)
	if text == "" && author == "" {
		return Comment{}, false
	}
	out := Comment{
		ID:           c.ID,
		Author:       author,
		AuthorID:     c.AuthorID,
		Text:         text,
		Likes:        max(c.Likes, 0),
		TimeText:     strings.TrimSpace(c.TimeText),
		Hearted:      c.Favorited,
		FromUploader: c.FromUploader,
		Replies:      commentList(c.Replies),
	}
	// TubeArchivist carries yt-dlp's Unix timestamp. Clients format dates
	// themselves everywhere else, so this is the same RFC 3339 they already
	// handle; `time_text` ("2 days ago") is kept as the fallback for archives
	// that have one and no timestamp.
	if c.Timestamp > 0 {
		published := time.Unix(int64(c.Timestamp), 0).UTC()
		out.Published = &published
	}
	return out, true
}
