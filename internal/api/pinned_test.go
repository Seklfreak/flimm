package api

import (
	"net/http"
	"testing"

	"github.com/Seklfreak/flimm/internal/ta"
)

func pinFixture(t *testing.T) (http.Handler, *ta.Fake) {
	t.Helper()
	client := ta.NewFake()
	for _, id := range []string{"PL1", "PL2"} {
		client.Playlists[id] = &ta.Playlist{PlaylistID: id, PlaylistName: "List " + id, PlaylistType: "custom"}
	}
	return newTestServer(client, newEventStore().querier()).Router(), client
}

func TestPinAndUnpinPlaylist(t *testing.T) {
	h, _ := pinFixture(t)

	if rec := do(t, h, http.MethodGet, "/api/v1/playlists/pinned", ""); len(decode[[]PlaylistSummary](t, rec)) != 0 {
		t.Fatal("expected no pins to start with")
	}
	if rec := do(t, h, http.MethodPut, "/api/v1/playlists/PL1/pinned", `{"pinned":true}`); rec.Code != http.StatusNoContent {
		t.Fatalf("pin status = %d: %s", rec.Code, rec.Body.String())
	}

	pins := decode[[]PlaylistSummary](t, do(t, h, http.MethodGet, "/api/v1/playlists/pinned", ""))
	if len(pins) != 1 || pins[0].ID != "PL1" || !pins[0].Pinned {
		t.Fatalf("pinned = %+v, want PL1 pinned", pins)
	}
	// The flag must show on the playlist itself, so the pin button renders in
	// the right state when the page is opened directly.
	detail := decode[PlaylistDetail](t, do(t, h, http.MethodGet, "/api/v1/playlists/PL1", ""))
	if !detail.Pinned {
		t.Error("playlist detail does not report the pin")
	}
	if other := decode[PlaylistDetail](t, do(t, h, http.MethodGet, "/api/v1/playlists/PL2", "")); other.Pinned {
		t.Error("PL2 reports pinned but was never pinned")
	}

	if rec := do(t, h, http.MethodPut, "/api/v1/playlists/PL1/pinned", `{"pinned":false}`); rec.Code != http.StatusNoContent {
		t.Fatalf("unpin status = %d", rec.Code)
	}
	if pins := decode[[]PlaylistSummary](t, do(t, h, http.MethodGet, "/api/v1/playlists/pinned", "")); len(pins) != 0 {
		t.Errorf("still pinned after unpin: %+v", pins)
	}
}

// Pinning twice must not duplicate the sidebar entry.
func TestPinPlaylistIsIdempotent(t *testing.T) {
	h, _ := pinFixture(t)
	for range 2 {
		if rec := do(t, h, http.MethodPut, "/api/v1/playlists/PL1/pinned", `{"pinned":true}`); rec.Code != http.StatusNoContent {
			t.Fatalf("pin status = %d", rec.Code)
		}
	}
	if pins := decode[[]PlaylistSummary](t, do(t, h, http.MethodGet, "/api/v1/playlists/pinned", "")); len(pins) != 1 {
		t.Errorf("pinned twice gave %d entries, want 1", len(pins))
	}
}

// TubeArchivist owns playlists, so one deleted there must drop out of the
// sidebar rather than failing the whole request and wedging it.
func TestPinnedPlaylistDeletedInTAIsSkipped(t *testing.T) {
	h, client := pinFixture(t)
	for _, id := range []string{"PL1", "PL2"} {
		if rec := do(t, h, http.MethodPut, "/api/v1/playlists/"+id+"/pinned", `{"pinned":true}`); rec.Code != http.StatusNoContent {
			t.Fatalf("pin %s status = %d", id, rec.Code)
		}
	}
	delete(client.Playlists, "PL1")

	rec := do(t, h, http.MethodGet, "/api/v1/playlists/pinned", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	pins := decode[[]PlaylistSummary](t, rec)
	if len(pins) != 1 || pins[0].ID != "PL2" {
		t.Errorf("pinned = %+v, want only PL2", pins)
	}
}

// A pin for a playlist that never existed is refused, so the sidebar can't
// accumulate entries that will never resolve.
func TestPinUnknownPlaylistIsRejected(t *testing.T) {
	h, _ := pinFixture(t)
	if rec := do(t, h, http.MethodPut, "/api/v1/playlists/nope/pinned", `{"pinned":true}`); rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}
