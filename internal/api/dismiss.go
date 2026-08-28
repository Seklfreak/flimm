package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Seklfreak/flimm/internal/db/sqlc"
)

// dismissVideo answers POST /videos/{id}/dismiss: take this video out of the
// feeds without watching it.
//
// Deliberately not "mark seen". Marking a video seen to clear it out of a feed
// lies about the watch state, and Flimm writes that back to TubeArchivist —
// so it would follow the viewer into TA's own UI and every other client. A
// dismissal is Flimm's own per-user state and says nothing about playback.
//
// The video is verified against TA first so a typo cannot litter the table
// with rows that resolve to nothing.
func (s *Server) dismissVideo(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r.Context())
	id := chi.URLParam(r, "id")
	if _, err := s.ta.GetVideo(r.Context(), id); err != nil {
		s.writeTAError(w, "get video", err)
		return
	}
	if err := s.q.DismissVideo(r.Context(), sqlc.DismissVideoParams{UserID: uid, VideoID: id}); err != nil {
		s.writeDBError(w, "dismiss video", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"dismissed": true})
}

// undismissVideo answers DELETE /videos/{id}/dismiss: put it back in the
// feeds. Undoing something that was never dismissed is a success, so an undo
// button cannot fail on a double tap.
func (s *Server) undismissVideo(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r.Context())
	id := chi.URLParam(r, "id")
	if err := s.q.UndismissVideo(r.Context(), sqlc.UndismissVideoParams{UserID: uid, VideoID: id}); err != nil {
		s.writeDBError(w, "undismiss video", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"dismissed": false})
}
