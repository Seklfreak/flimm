package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Seklfreak/flimm/internal/ta"
)

// Asking TubeArchivist to index a channel's playlists flips instance-wide TA
// state, so only an admin may do it.
func TestIndexChannelPlaylistsIsAdminOnly(t *testing.T) {
	client := ta.NewFake()
	client.Channels["UC1"] = &ta.Channel{ChannelID: "UC1", ChannelName: "One"}
	s := newTestServer(client, newEventStore().querier())

	// The dev user (auth disabled) is an admin: the call reaches TA.
	rec := do(t, s.Router(), http.MethodPost, "/api/v1/channels/UC1/index-playlists", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if !slices.Contains(client.Calls, "index-playlists:UC1") {
		t.Error("TubeArchivist was never asked to index")
	}

	// Without the admin flag the same request is refused.
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "UC1")
	ctx := context.WithValue(context.Background(), chi.RouteCtxKey, rctx)
	ctx = context.WithValue(ctx, userIDKey, DevUserID)
	ctx = context.WithValue(ctx, isAdminKey, false)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/channels/UC1/index-playlists", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	s.indexChannelPlaylists(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("non-admin status = %d, want 403", w.Code)
	}
	if calls := len(client.Calls); calls != 1 {
		t.Errorf("TA called %d times, want the one admin call", calls)
	}

	// A channel TA does not know propagates as 404.
	if rec := do(t, s.Router(), http.MethodPost, "/api/v1/channels/nope/index-playlists", ""); rec.Code != http.StatusNotFound {
		t.Errorf("unknown channel = %d, want 404", rec.Code)
	}
}
