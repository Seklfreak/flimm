package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Seklfreak/flimm/internal/db/sqlc"
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

// Channel pins mirror playlist pins: per-user sidebar state, pin order, and a
// channel TubeArchivist no longer knows simply drops out of the list.
func TestPinAndUnpinChannel(t *testing.T) {
	client := ta.NewFake()
	client.Channels["UC1"] = &ta.Channel{ChannelID: "UC1", ChannelName: "One"}
	client.Channels["UC2"] = &ta.Channel{ChannelID: "UC2", ChannelName: "Two"}

	es := newEventStore()
	q := es.querier()
	var pins []sqlc.PinnedChannel
	q.ListPinnedChannelsFn = func(context.Context, uuid.UUID) ([]sqlc.PinnedChannel, error) { return pins, nil }
	q.PinChannelFn = func(_ context.Context, arg sqlc.PinChannelParams) error {
		for _, p := range pins {
			if p.ChannelID == arg.ChannelID {
				return nil
			}
		}
		pins = append(pins, sqlc.PinnedChannel{UserID: arg.UserID, ChannelID: arg.ChannelID, Position: int32(len(pins))}) //nolint:gosec // test fixture
		return nil
	}
	q.UnpinChannelFn = func(_ context.Context, arg sqlc.UnpinChannelParams) error {
		kept := pins[:0]
		for _, p := range pins {
			if p.ChannelID != arg.ChannelID {
				kept = append(kept, p)
			}
		}
		pins = kept
		return nil
	}
	h := newTestServer(client, q).Router()

	// Pinning an unknown channel is refused.
	if rec := do(t, h, http.MethodPut, "/api/v1/channels/nope/pinned", `{"pinned":true}`); rec.Code != http.StatusNotFound {
		t.Errorf("unknown channel pin = %d, want 404", rec.Code)
	}

	for _, id := range []string{"UC1", "UC2", "UC1"} { // re-pin is idempotent
		if rec := do(t, h, http.MethodPut, "/api/v1/channels/"+id+"/pinned", `{"pinned":true}`); rec.Code != http.StatusNoContent {
			t.Fatalf("pin %s = %d: %s", id, rec.Code, rec.Body.String())
		}
	}
	got := decode[[]ChannelSummary](t, do(t, h, http.MethodGet, "/api/v1/channels/pinned", ""))
	if len(got) != 2 || got[0].ID != "UC1" || got[1].ID != "UC2" || !got[0].Pinned {
		t.Fatalf("pinned = %+v, want UC1 then UC2, both pinned", got)
	}

	// The flag rides the directory and the channel page too.
	detail := decode[ChannelSummary](t, do(t, h, http.MethodGet, "/api/v1/channels/UC1", ""))
	if !detail.Pinned {
		t.Error("channel detail does not report the pin")
	}

	// A channel deleted in TA drops out instead of failing the request.
	delete(client.Channels, "UC2")
	got = decode[[]ChannelSummary](t, do(t, h, http.MethodGet, "/api/v1/channels/pinned", ""))
	if len(got) != 1 || got[0].ID != "UC1" {
		t.Errorf("after TA delete = %+v, want only UC1", got)
	}

	if rec := do(t, h, http.MethodPut, "/api/v1/channels/UC1/pinned", `{"pinned":false}`); rec.Code != http.StatusNoContent {
		t.Fatalf("unpin = %d", rec.Code)
	}
	if got := decode[[]ChannelSummary](t, do(t, h, http.MethodGet, "/api/v1/channels/pinned", "")); len(got) != 0 {
		t.Errorf("still pinned after unpin: %+v", got)
	}
}

// The archive's own subscription is instance-wide TA state, so flipping it
// is admin-only, refused for channels TA does not know, and lands in TA.
func TestSetChannelSubscribedIsAdminOnly(t *testing.T) {
	client := ta.NewFake()
	client.Channels["UC1"] = &ta.Channel{ChannelID: "UC1", ChannelName: "One", ChannelSubscribed: true}
	s := newTestServer(client, newEventStore().querier())

	if rec := do(t, s.Router(), http.MethodPut, "/api/v1/channels/UC1/subscribed", `{"subscribed":false}`); rec.Code != http.StatusNoContent {
		t.Fatalf("unsubscribe = %d: %s", rec.Code, rec.Body.String())
	}
	if client.Channels["UC1"].ChannelSubscribed {
		t.Error("TA still shows the channel subscribed")
	}
	if rec := do(t, s.Router(), http.MethodPut, "/api/v1/channels/nope/subscribed", `{"subscribed":true}`); rec.Code != http.StatusNotFound {
		t.Errorf("unknown channel = %d, want 404", rec.Code)
	}

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "UC1")
	ctx := context.WithValue(context.Background(), chi.RouteCtxKey, rctx)
	ctx = context.WithValue(ctx, userIDKey, DevUserID)
	ctx = context.WithValue(ctx, isAdminKey, false)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/channels/UC1/subscribed", strings.NewReader(`{"subscribed":true}`)).WithContext(ctx)
	w := httptest.NewRecorder()
	s.setChannelSubscribed(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("non-admin = %d, want 403", w.Code)
	}
}

// Subscribing a brand-new channel hands TA the raw URL/handle/id; TA's own
// task resolves and creates it. Admin-only, like every archive-side write.
func TestSubscribeNewChannel(t *testing.T) {
	client := ta.NewFake()
	s := newTestServer(client, newEventStore().querier())

	if rec := do(t, s.Router(), http.MethodPost, "/api/v1/channels", `{"channel":"https://www.youtube.com/@Gronkh"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("subscribe = %d: %s", rec.Code, rec.Body.String())
	}
	if !slices.Contains(client.Calls, "subscribe:https://www.youtube.com/@Gronkh:true") {
		t.Errorf("TA never received the subscribe: %v", client.Calls)
	}
	if rec := do(t, s.Router(), http.MethodPost, "/api/v1/channels", `{"channel":"  "}`); rec.Code != http.StatusBadRequest {
		t.Errorf("blank = %d, want 400", rec.Code)
	}

	rctx := chi.NewRouteContext()
	ctx := context.WithValue(context.Background(), chi.RouteCtxKey, rctx)
	ctx = context.WithValue(ctx, userIDKey, DevUserID)
	ctx = context.WithValue(ctx, isAdminKey, false)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/channels", strings.NewReader(`{"channel":"UCX"}`)).WithContext(ctx)
	w := httptest.NewRecorder()
	s.subscribeNewChannel(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("non-admin = %d, want 403", w.Code)
	}
}
