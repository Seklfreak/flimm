package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

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
	client.Playlists["PL1"] = &ta.Playlist{PlaylistID: "PL1", PlaylistName: "Series", PlaylistChannelID: "UC1", PlaylistType: "regular"}
	s := newTestServer(client, newEventStore().querier())
	s.subscribeWait, s.subscribePoll = 50*time.Millisecond, time.Millisecond

	// The dev user (auth disabled) is an admin: the call reaches TA, and the
	// request holds until the discovery has run, answering with what it
	// found rather than "check back later".
	rec := do(t, s.Router(), http.MethodPost, "/api/v1/channels/UC1/index-playlists", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if !slices.Contains(client.Calls, "index-playlists:UC1") {
		t.Error("TubeArchivist was never asked to index")
	}
	var indexed struct {
		Status    string            `json:"status"`
		Playlists []PlaylistSummary `json:"playlists"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &indexed); err != nil {
		t.Fatal(err)
	}
	if indexed.Status != "indexed" || len(indexed.Playlists) != 1 || indexed.Playlists[0].ID != "PL1" {
		t.Errorf("indexed = %+v, want the channel's playlist", indexed)
	}
	if rec := do(t, s.Router(), http.MethodGet, "/api/v1/channels/UC1/index-playlists", ""); !strings.Contains(rec.Body.String(), `"idle"`) {
		t.Errorf("status after the task landed = %s, want idle", rec.Body.String())
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

// Subscribing a brand-new channel hands TA the raw URL/handle/id and holds
// the request until TA's own task has created it, answering with the channel
// — read by id when the input carried one, found as the one new channel when
// it was a handle TA had to resolve. Admin-only, like every archive-side
// write.
func TestSubscribeNewChannel(t *testing.T) {
	client := ta.NewFake()
	s := newTestServer(client, newEventStore().querier())
	s.subscribeWait, s.subscribePoll = 50*time.Millisecond, time.Millisecond

	rec := do(t, s.Router(), http.MethodPost, "/api/v1/channels", `{"channel":"https://www.youtube.com/@Gronkh"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("subscribe = %d: %s", rec.Code, rec.Body.String())
	}
	if !slices.Contains(client.Calls, "subscribe:https://www.youtube.com/@Gronkh:true") {
		t.Errorf("TA never received the subscribe: %v", client.Calls)
	}
	var added struct {
		Status  string         `json:"status"`
		Channel ChannelSummary `json:"channel"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &added); err != nil {
		t.Fatal(err)
	}
	if want := ta.ResolveChannelID("https://www.youtube.com/@Gronkh"); added.Status != "added" || added.Channel.ID != want {
		t.Errorf("handle resolved to %+v, want added %s", added, want)
	}

	// An id in the input is read directly, even with channels the snapshot
	// could not tell apart.
	client.Channels["UCother00000000000000000"] = &ta.Channel{ChannelID: "UCother00000000000000000", ChannelName: "Other"}
	rec = do(t, s.Router(), http.MethodPost, "/api/v1/channels", `{"channel":"https://www.youtube.com/channel/UCabcdefghijklmnopqrstuv"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("subscribe by id = %d: %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &added); err != nil {
		t.Fatal(err)
	}
	if added.Channel.ID != "UCabcdefghijklmnopqrstuv" {
		t.Errorf("id input gave %q", added.Channel.ID)
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

// A discovery that outlives the wait answers pending, and the status
// endpoint says running until TA's task is gone; a failed one carries TA's
// reason. Both used to be invisible: the request answered 204 either way.
func TestIndexChannelPlaylistsPendingAndFailed(t *testing.T) {
	client := ta.NewFake()
	client.Channels["UC1"] = &ta.Channel{ChannelID: "UC1", ChannelName: "One"}
	client.IndexOutcome = "PENDING"
	s := newTestServer(client, newEventStore().querier())
	s.subscribeWait, s.subscribePoll = 20*time.Millisecond, time.Millisecond

	rec := do(t, s.Router(), http.MethodPost, "/api/v1/channels/UC1/index-playlists", "")
	if rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), `"pending"`) {
		t.Fatalf("pending index = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := do(t, s.Router(), http.MethodGet, "/api/v1/channels/UC1/index-playlists", ""); !strings.Contains(rec.Body.String(), `"running"`) {
		t.Errorf("status while the task runs = %s, want running", rec.Body.String())
	}

	client.IndexOutcome, client.IndexError = "FAILURE", "channel has no playlists tab"
	rec = do(t, s.Router(), http.MethodPost, "/api/v1/channels/UC1/index-playlists", "")
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "ValueError: channel has no playlists tab") {
		t.Fatalf("failed index = %d: %s", rec.Code, rec.Body.String())
	}
}

// TA's task can fail — a handle that does not resolve, a URL off
// youtube.com — and used to fail silently into the archive's logs while the
// client said "asked the archive". The reason now comes back as a 502.
func TestSubscribeNewChannelReportsTAFailure(t *testing.T) {
	client := ta.NewFake()
	client.SubscribeOutcome, client.SubscribeError = "FAILURE", "invalid domain: example.com"
	s := newTestServer(client, newEventStore().querier())
	s.subscribeWait, s.subscribePoll = 50*time.Millisecond, time.Millisecond

	rec := do(t, s.Router(), http.MethodPost, "/api/v1/channels", `{"channel":"https://example.com/@nope"}`)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("failed subscribe = %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "ValueError: invalid domain: example.com") {
		t.Errorf("reason missing: %s", rec.Body.String())
	}
}

// A task still running when the wait runs out — a busy TA queue — answers
// 202: the old contract, the channel appears when the task lands.
func TestSubscribeNewChannelPendingPastTheWait(t *testing.T) {
	client := ta.NewFake()
	client.SubscribeOutcome = "PENDING"
	s := newTestServer(client, newEventStore().querier())
	s.subscribeWait, s.subscribePoll = 20*time.Millisecond, time.Millisecond

	rec := do(t, s.Router(), http.MethodPost, "/api/v1/channels", `{"channel":"@slow"}`)
	if rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), `"pending"`) {
		t.Fatalf("pending subscribe = %d: %s", rec.Code, rec.Body.String())
	}
}

// A channel list longer than one page has to say so. The default order is by
// name, which the handler answers without enriching every channel — and that
// path used to build its own page and leave `has_more` at false, so a picker
// paging until "no more" stopped at the first page and a subscriber with 218
// channels only ever saw 100 of them.
func TestListChannelsPagesPastTheFirstPage(t *testing.T) {
	client := ta.NewFake()
	for i := range 218 {
		id := fmt.Sprintf("UC%03d", i)
		client.Channels[id] = &ta.Channel{ChannelID: id, ChannelName: fmt.Sprintf("Channel %03d", i)}
	}
	s := newTestServer(client, newEventStore().querier())

	var seen []string
	for page := 0; ; page++ {
		rec := do(t, s.Router(), http.MethodGet,
			fmt.Sprintf("/api/v1/channels?page=%d&page_size=100", page), "")
		if rec.Code != http.StatusOK {
			t.Fatalf("page %d status = %d: %s", page, rec.Code, rec.Body.String())
		}
		got := decode[Page[ChannelSummary]](t, rec)
		if got.Total != 218 {
			t.Errorf("page %d total = %d, want 218", page, got.Total)
		}
		for _, c := range got.Items {
			seen = append(seen, c.ID)
		}
		if !got.HasMore {
			break
		}
		if page > 5 {
			t.Fatal("paging never ended")
		}
	}
	if len(seen) != 218 {
		t.Errorf("paged through %d channels, want all 218", len(seen))
	}

	// The count-dependent orders page through the same list.
	rec := do(t, s.Router(), http.MethodGet, "/api/v1/channels?sort=videos&page=0&page_size=100", "")
	if got := decode[Page[ChannelSummary]](t, rec); !got.HasMore {
		t.Error("sort=videos page 0 says there is nothing more")
	}
}
