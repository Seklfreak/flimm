package ta

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestLayoutForVersion(t *testing.T) {
	for v, want := range map[string]Layout{
		"v0.5.12":  LayoutNginx,
		"v0.4.13":  LayoutNginx,
		"0.5.1":    LayoutNginx,
		"v0.6.0":   LayoutApp,
		"v0.6.1":   LayoutApp,
		"v1.0.0":   LayoutApp,
		"unstable": LayoutApp,
		"":         LayoutApp,
	} {
		if got := LayoutForVersion(v); got != want {
			t.Errorf("%q: %d, want %d", v, got, want)
		}
	}
}

func TestMediaLayoutReadsAndCachesPing(t *testing.T) {
	var pings atomic.Int64
	version := "v0.5.12"
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/ping/" {
			http.NotFound(w, r)
			return
		}
		pings.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"response":"pong","user":1,"version":"` + version + `","ta_update":{}}`))
	}))
	defer up.Close()

	c := New(up.URL, "token")
	if got := c.MediaLayout(context.Background()); got != LayoutNginx {
		t.Fatalf("v0.5.12: %d", got)
	}
	version = "v0.6.0"
	if got := c.MediaLayout(context.Background()); got != LayoutNginx || pings.Load() != 1 {
		t.Fatalf("within the TTL the cached layout stands: %d after %d pings", got, pings.Load())
	}

	// Past the TTL the archive is asked again, which is how an upgrade under a
	// running Flimm is picked up.
	c.mu.Lock()
	c.layout.exp = c.layout.exp.Add(-2 * layoutTTL)
	c.mu.Unlock()
	if got := c.MediaLayout(context.Background()); got != LayoutApp {
		t.Fatalf("after upgrade: %d", got)
	}

	// An unreachable archive keeps the last known layout.
	up.Close()
	c.mu.Lock()
	c.layout.exp = c.layout.exp.Add(-2 * layoutTTL)
	c.mu.Unlock()
	if got := c.MediaLayout(context.Background()); got != LayoutApp {
		t.Fatalf("unreachable: %d", got)
	}
}
