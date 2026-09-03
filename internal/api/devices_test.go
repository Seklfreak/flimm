package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/Seklfreak/flimm/internal/db/sqlc"
	"github.com/Seklfreak/flimm/internal/ta"
)

func TestRegisterPushDevice(t *testing.T) {
	es := newEventStore()
	q := es.querier()
	var stored []sqlc.UpsertPushDeviceParams
	q.UpsertPushDeviceFn = func(_ context.Context, arg sqlc.UpsertPushDeviceParams) error {
		stored = append(stored, arg)
		return nil
	}
	h := newTestServer(ta.NewFake(), q).Router()

	token := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	// The shipped app: no body needed, production is the default.
	if rec := do(t, h, http.MethodPut, "/api/v1/me/devices/"+token, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	// A build from Xcode says so, or its notifications are refused by the
	// production host.
	if rec := do(t, h, http.MethodPut, "/api/v1/me/devices/"+token, `{"environment":"sandbox","platform":"ipados"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if len(stored) != 2 {
		t.Fatalf("stored %d registrations, want 2", len(stored))
	}
	if stored[0].Token != token || stored[0].UserID != DevUserID || stored[0].Environment != "production" || stored[0].Platform != "ios" {
		t.Errorf("first = %+v", stored[0])
	}
	if stored[1].Environment != "sandbox" || stored[1].Platform != "ipados" {
		t.Errorf("second = %+v", stored[1])
	}

	if rec := do(t, h, http.MethodPut, "/api/v1/me/devices/not-hex", ""); rec.Code != http.StatusBadRequest {
		t.Errorf("garbage token: %d", rec.Code)
	}
	if rec := do(t, h, http.MethodPut, "/api/v1/me/devices/"+token, `{"environment":"staging"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown environment: %d", rec.Code)
	}
}

// Sign-out forgets the caller's registration and nobody else's: a token that
// belongs to another account is a 404, like every other resource that is
// not theirs.
func TestDeletePushDeviceIsScopedToTheCaller(t *testing.T) {
	other := uuid.New()
	owners := map[string]uuid.UUID{"mine": DevUserID, "theirs": other}
	es := newEventStore()
	q := es.querier()
	q.DeletePushDeviceFn = func(_ context.Context, arg sqlc.DeletePushDeviceParams) (int64, error) {
		if owners[arg.Token] == arg.UserID {
			delete(owners, arg.Token)
			return 1, nil
		}
		return 0, nil
	}
	h := newTestServer(ta.NewFake(), q).Router()

	if rec := do(t, h, http.MethodDelete, "/api/v1/me/devices/theirs", ""); rec.Code != http.StatusNotFound {
		t.Errorf("someone else's device: %d", rec.Code)
	}
	if _, still := owners["theirs"]; !still {
		t.Error("someone else's registration was removed")
	}
	if rec := do(t, h, http.MethodDelete, "/api/v1/me/devices/mine", ""); rec.Code != http.StatusNoContent {
		t.Errorf("own device: %d", rec.Code)
	}
	if rec := do(t, h, http.MethodDelete, "/api/v1/me/devices/mine", ""); rec.Code != http.StatusNotFound {
		t.Errorf("already forgotten: %d", rec.Code)
	}
}

func TestMeCountsPushDevices(t *testing.T) {
	es := newEventStore()
	q := es.querier()
	q.CountPushDevicesFn = func(context.Context, uuid.UUID) (int64, error) { return 2, nil }
	h := newTestServer(ta.NewFake(), q).Router()
	rec := do(t, h, http.MethodGet, "/api/v1/me", "")
	me := decode[map[string]any](t, rec)
	if me["push_devices"] != float64(2) {
		t.Errorf("push_devices = %v", me["push_devices"])
	}
}

// /config says whether the flag reaches anyone, so a client can hide the
// option on a server that has no key rather than offer a switch that does
// nothing.
func TestConfigReportsPush(t *testing.T) {
	h := newTestServer(ta.NewFake(), newEventStore().querier()).Router()
	cfg := decode[ConfigResponse](t, do(t, h, http.MethodGet, "/api/v1/config", ""))
	if cfg.PushEnabled {
		t.Error("a server with no APNs client claims push")
	}
}
