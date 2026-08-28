package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Seklfreak/flimm/internal/ta"
)

// A readiness probe gives /healthz about a second. TubeArchivist is reported
// beside the verdict rather than deciding it, so a slow archive must not be
// able to hold the probe open — three late probes would take the only replica
// out of service over something it can still serve around.
func TestHealthzDoesNotWaitForASlowArchive(t *testing.T) {
	client := ta.NewFake()
	client.PingDelay = 5 * time.Second
	h := newTestServer(client, newEventStore().querier()).Router()

	start := time.Now()
	rec := do(t, h, http.MethodGet, "/healthz", "")
	elapsed := time.Since(start)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200: a slow archive is not an unready server", rec.Code)
	}
	if elapsed > 2*time.Second {
		t.Errorf("took %s; the archive check is meant to be time-boxed", elapsed)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["ta"] != "slow" {
		t.Errorf("ta = %v, want \"slow\"", body["ta"])
	}
	if body["status"] != "ok" {
		t.Errorf("status = %v, want ok", body["status"])
	}
}

// Probing every ten seconds must not mean asking TubeArchivist every ten
// seconds.
func TestHealthzCachesTheArchiveCheck(t *testing.T) {
	client := &countingTA{Fake: ta.NewFake()}
	h := newTestServer(client, newEventStore().querier()).Router()

	for range 5 {
		if rec := do(t, h, http.MethodGet, "/healthz", ""); rec.Code != http.StatusOK {
			t.Fatalf("status = %d", rec.Code)
		}
	}
	if n := client.pings.Load(); n != 1 {
		t.Errorf("pinged TubeArchivist %d times for 5 probes, want 1", n)
	}
}

// Liveness answers "should I be restarted". Restarting cannot fix an archive
// that is down, so it must not depend on one.
func TestLivezIgnoresTheArchive(t *testing.T) {
	client := ta.NewFake()
	client.PingErr = errors.New("archive is down")
	h := newTestServer(client, newEventStore().querier()).Router()

	for _, path := range []string{"/livez", "/api/v1/livez"} {
		rec := do(t, h, http.MethodGet, path, "")
		if rec.Code != http.StatusOK {
			t.Errorf("%s = %d, want 200", path, rec.Code)
		}
	}
	// And /healthz still says so, because that is its job.
	var body map[string]any
	if err := json.Unmarshal(do(t, h, http.MethodGet, "/healthz", "").Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["ta"] != "unreachable" {
		t.Errorf("ta = %v, want unreachable", body["ta"])
	}
}
