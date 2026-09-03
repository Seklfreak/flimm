package apns

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testKey(t *testing.T) (*ecdsa.PrivateKey, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return key, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}

func newTestClient(t *testing.T, baseURL string) (*Client, *ecdsa.PrivateKey) {
	t.Helper()
	key, pemBytes := testKey(t)
	c, err := New(Options{Key: pemBytes, KeyID: "KEY1234567", TeamID: "TEAM123456", Topic: "dev.example.app", BaseURL: baseURL})
	if err != nil {
		t.Fatal(err)
	}
	return c, key
}

// The JWT is the one thing here that cannot be checked by looking: Apple
// either accepts it or answers 403 for every notification. So the test does
// what Apple does — verifies the signature with the public key — and checks
// the two claims and two header fields it reads.
func TestJWTVerifiesWithThePublicKey(t *testing.T) {
	c, key := newTestClient(t, "")
	c.now = func() time.Time { return time.Unix(1_700_000_000, 0) }
	token, err := c.bearer()
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("token has %d parts, want 3", len(parts))
	}
	enc := base64.RawURLEncoding
	var header map[string]string
	raw, _ := enc.DecodeString(parts[0])
	if err := json.Unmarshal(raw, &header); err != nil {
		t.Fatal(err)
	}
	if header["alg"] != "ES256" || header["kid"] != "KEY1234567" {
		t.Errorf("header = %v", header)
	}
	var claims map[string]any
	raw, _ = enc.DecodeString(parts[1])
	if err := json.Unmarshal(raw, &claims); err != nil {
		t.Fatal(err)
	}
	if claims["iss"] != "TEAM123456" || claims["iat"] != float64(1_700_000_000) {
		t.Errorf("claims = %v", claims)
	}
	sig, err := enc.DecodeString(parts[2])
	if err != nil || len(sig) != 64 {
		t.Fatalf("signature is %d bytes (%v), want 64 raw r‖s", len(sig), err)
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	r, s := new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])
	if !ecdsa.Verify(&key.PublicKey, digest[:], r, s) {
		t.Error("signature does not verify")
	}

	// Reused within the hour, re-signed after: Apple refuses a token older
	// than an hour and throttles one minted more often than every twenty
	// minutes.
	again, _ := c.bearer()
	if again != token {
		t.Error("a fresh token was signed for every request")
	}
	c.now = func() time.Time { return time.Unix(1_700_000_000, 0).Add(jwtLifetime + time.Second) }
	later, _ := c.bearer()
	if later == token {
		t.Error("an hour-old token was reused")
	}
}

func TestNewRejectsBadKeys(t *testing.T) {
	_, pemBytes := testKey(t)
	if _, err := New(Options{Key: []byte("not a key"), KeyID: "K", TeamID: "T", Topic: "x"}); err == nil {
		t.Error("garbage was accepted as a key")
	}
	if _, err := New(Options{Key: pemBytes, KeyID: "", TeamID: "T", Topic: "x"}); err == nil {
		t.Error("a missing key id was accepted")
	}
}

type captured struct {
	path    string
	headers http.Header
	payload map[string]any
}

// fakeAPNs answers like Apple: 200 with nothing, or a status with a reason.
func fakeAPNs(t *testing.T, status int, reason string) (*httptest.Server, *captured) {
	t.Helper()
	got := &captured{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.path = r.URL.Path
		got.headers = r.Header.Clone()
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &got.payload)
		w.WriteHeader(status)
		if reason != "" {
			_ = json.NewEncoder(w).Encode(map[string]string{"reason": reason})
		}
	}))
	t.Cleanup(srv.Close)
	return srv, got
}

func TestSendPostsWhatAppleExpects(t *testing.T) {
	srv, got := fakeAPNs(t, http.StatusOK, "")
	c, _ := newTestClient(t, srv.URL)
	err := c.Send(t.Context(), Notification{
		Token: "abc123", Environment: Sandbox,
		Title: "A channel", Subtitle: "DevOps", Body: "A video", ThreadID: "feed-1",
		Data: map[string]any{"feed": "feed-1", "video": "v1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.path != "/3/device/abc123" {
		t.Errorf("path = %q", got.path)
	}
	for k, want := range map[string]string{
		"Apns-Topic": "dev.example.app", "Apns-Push-Type": "alert", "Apns-Priority": "10",
	} {
		if v := got.headers.Get(k); v != want {
			t.Errorf("%s = %q, want %q", k, v, want)
		}
	}
	if !strings.HasPrefix(got.headers.Get("Authorization"), "bearer ") {
		t.Errorf("authorization = %q", got.headers.Get("Authorization"))
	}
	if got.headers.Get("Apns-Expiration") == "" {
		t.Error("no expiration: a phone that is off for a week would get a week of stale news at once")
	}
	aps, _ := got.payload["aps"].(map[string]any)
	alert, _ := aps["alert"].(map[string]any)
	if alert["title"] != "A channel" || alert["subtitle"] != "DevOps" || alert["body"] != "A video" {
		t.Errorf("alert = %v", alert)
	}
	if aps["thread-id"] != "feed-1" || aps["sound"] != "default" {
		t.Errorf("aps = %v", aps)
	}
	if got.payload["feed"] != "feed-1" || got.payload["video"] != "v1" {
		t.Errorf("custom data = %v", got.payload)
	}
}

func TestSendTellsADeadTokenFromAnOutage(t *testing.T) {
	cases := []struct {
		status int
		reason string
		dead   bool
	}{
		{http.StatusGone, "Unregistered", true},
		{http.StatusBadRequest, "BadDeviceToken", true},
		{http.StatusBadRequest, "DeviceTokenNotForTopic", true},
		{http.StatusBadRequest, "PayloadEmpty", false},
		{http.StatusForbidden, "InvalidProviderToken", false},
		{http.StatusServiceUnavailable, "", false},
	}
	for _, tc := range cases {
		srv, _ := fakeAPNs(t, tc.status, tc.reason)
		c, _ := newTestClient(t, srv.URL)
		err := c.Send(t.Context(), Notification{Token: "t", Body: "x"})
		if err == nil {
			t.Errorf("%d %s: no error", tc.status, tc.reason)
			continue
		}
		if errors.Is(err, ErrBadToken) != tc.dead {
			t.Errorf("%d %s: dead=%v, want %v (%v)", tc.status, tc.reason, !tc.dead, tc.dead, err)
		}
		if !tc.dead && !errors.Is(err, ErrUnavailable) {
			t.Errorf("%d %s: not ErrUnavailable: %v", tc.status, tc.reason, err)
		}
	}
}

// Without a base URL the environment picks Apple's host — the only thing the
// environment is for.
func TestEnvironmentPicksTheHost(t *testing.T) {
	if Production.host() != "https://api.push.apple.com" || Sandbox.host() != "https://api.sandbox.push.apple.com" {
		t.Error("wrong hosts")
	}
	for in, want := range map[string]Environment{"": Production, "production": Production, "Sandbox": Sandbox} {
		if got, ok := ParseEnvironment(in); !ok || got != want {
			t.Errorf("ParseEnvironment(%q) = %q, %v", in, got, ok)
		}
	}
	if _, ok := ParseEnvironment("staging"); ok {
		t.Error("an unknown environment was accepted")
	}
}
