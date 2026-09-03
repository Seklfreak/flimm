package api

import (
	"net/http"
	"regexp"

	"github.com/go-chi/chi/v5"

	"github.com/Seklfreak/flimm/internal/apns"
	"github.com/Seklfreak/flimm/internal/db/sqlc"
)

// deviceTokenRe is an APNs device token as the app receives it: the hex of
// 32 bytes today, but Apple only promises "variable length", so the check is
// "hex, and not absurd" rather than a fixed width.
var deviceTokenRe = regexp.MustCompile(`^[0-9a-fA-F]{16,512}$`)

type pushDeviceBody struct {
	// Platform is informational — "ios" or "ipados"; the topic is the same.
	Platform string `json:"platform"`
	// Environment is which APNs the token came from: "sandbox" for a build
	// run from Xcode, "production" (the default) for TestFlight and the App
	// Store. A push sent to the wrong one is refused, so the app says.
	Environment string `json:"environment"`
}

// putPushDevice registers the caller's device for feed notifications. It is
// a PUT on the token because it is idempotent: the app sends it on every
// launch, since Apple may hand out a new token at any time and the old one
// then stops delivering without a word.
func (s *Server) putPushDevice(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r.Context())
	token := chi.URLParam(r, "token")
	if !deviceTokenRe.MatchString(token) {
		writeError(w, http.StatusBadRequest, "invalid device token")
		return
	}
	var body pushDeviceBody
	if r.ContentLength != 0 {
		if err := decodeBody(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
	}
	env, ok := apns.ParseEnvironment(body.Environment)
	if !ok {
		writeError(w, http.StatusBadRequest, "environment must be production or sandbox")
		return
	}
	platform := body.Platform
	if platform == "" {
		platform = "ios"
	}
	err := s.q.UpsertPushDevice(r.Context(), sqlc.UpsertPushDeviceParams{
		Token: token, UserID: uid, Platform: platform, Environment: string(env),
	})
	if err != nil {
		s.writeDBError(w, "register device", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// deletePushDevice is sign-out: the phone stops being this account's. Scoped
// to the caller, so someone else's token is a 404 like any other resource
// that is not theirs.
func (s *Server) deletePushDevice(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r.Context())
	token := chi.URLParam(r, "token")
	n, err := s.q.DeletePushDevice(r.Context(), sqlc.DeletePushDeviceParams{Token: token, UserID: uid})
	if err != nil {
		s.writeDBError(w, "forget device", err)
		return
	}
	if n == 0 {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
