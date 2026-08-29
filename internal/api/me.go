package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Seklfreak/flimm/internal/db/sqlc"
)

func (s *Server) loadPrefs(r *http.Request, uid uuid.UUID) (Prefs, error) {
	return s.prefsFor(r.Context(), uid)
}

// prefsFor is loadPrefs for the paths that hold a context rather than a
// request — every list, which needs to know what the viewer asked to be shown.
func (s *Server) prefsFor(ctx context.Context, uid uuid.UUID) (Prefs, error) {
	raw, err := s.q.GetPrefs(ctx, uid)
	if errors.Is(err, pgx.ErrNoRows) {
		return defaultPrefs(), nil
	}
	if err != nil {
		return Prefs{}, err
	}
	return parsePrefs(raw), nil
}

func (s *Server) getMe(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r.Context())
	prefs, err := s.loadPrefs(r, uid)
	if err != nil {
		s.writeDBError(w, "load prefs", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":       uid.String(),
		"name":     currentName(r.Context()),
		"email":    currentEmail(r.Context()),
		"is_admin": isAdmin(r.Context()),
		"prefs":    prefs,
	})
}

func (s *Server) patchPrefs(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r.Context())
	var patch map[string]json.RawMessage
	if err := decodeBody(r, &patch); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	cur, err := s.loadPrefs(r, uid)
	if err != nil {
		s.writeDBError(w, "load prefs", err)
		return
	}
	merged, err := mergePrefs(cur, patch)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.savePrefs(r, uid, merged); err != nil {
		s.writeDBError(w, "save prefs", err)
		return
	}
	writeJSON(w, http.StatusOK, merged)
}

func (s *Server) savePrefs(r *http.Request, uid uuid.UUID, p Prefs) error {
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return s.q.UpsertPrefs(r.Context(), sqlc.UpsertPrefsParams{UserID: uid, Prefs: raw})
}
