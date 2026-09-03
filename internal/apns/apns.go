// Package apns sends notifications through the Apple Push Notification
// service, which is how a feed reaches an iPhone or iPad that is not open.
//
// It is the smallest client that does the job: token-based auth (a .p8 key
// from the developer portal, signed into a JWT and reused for most of an
// hour), HTTP/2 to Apple's two hosts, and one call that either delivered or
// says why not. The one distinction a caller has to act on is a dead device
// token — a phone that deleted the app, a token Apple rotated — which comes
// back as ErrBadToken so the registration can be forgotten rather than
// retried forever.
package apns

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Seklfreak/flimm/internal/obs"
)

// Environment is which APNs a device token belongs to. A build run from
// Xcode registers with the sandbox, a TestFlight or App Store build with
// production, and each refuses the other's tokens — so the client reports
// its own when it registers, and the server sends to the matching host.
type Environment string

const (
	Production Environment = "production"
	Sandbox    Environment = "sandbox"
)

// ParseEnvironment accepts the two names and nothing else. The empty string
// is production: a client built before the field existed is a shipped one.
func ParseEnvironment(s string) (Environment, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", string(Production):
		return Production, true
	case string(Sandbox):
		return Sandbox, true
	}
	return "", false
}

func (e Environment) host() string {
	if e == Sandbox {
		return "https://api.sandbox.push.apple.com"
	}
	return "https://api.push.apple.com"
}

// ErrBadToken means Apple said the device token is not one it will ever
// deliver to again: the app was removed, or the token belongs to the other
// environment. The registration behind it should be forgotten.
var ErrBadToken = errors.New("apns: device token rejected")

// ErrUnavailable is any other failure: a refused JWT, a 5xx, a network
// error. Worth logging, not worth acting on.
var ErrUnavailable = errors.New("apns: unavailable")

const (
	// jwtLifetime is how long one signed token is reused. Apple wants a fresh
	// one at least every hour and no more often than every twenty minutes.
	jwtLifetime = 50 * time.Minute
	// expiration is how long Apple may hold a notification for a phone that
	// is off. A day: a "new video" older than that is not news.
	expiration     = 24 * time.Hour
	defaultTimeout = 10 * time.Second
	maxBody        = 64 << 10
)

// Options configures a Client.
type Options struct {
	// Key is the PEM-encoded ES256 private key (.p8) from the developer
	// portal's Keys page, with the APNs service enabled.
	Key []byte
	// KeyID is the ten-character id shown next to that key.
	KeyID string
	// TeamID is the Apple developer team the key belongs to.
	TeamID string
	// Topic is the app's bundle identifier.
	Topic string
	// BaseURL replaces Apple's hosts for every environment — for tests and
	// for a development stack pointed at a stand-in. Empty uses Apple's.
	BaseURL string
	// HTTPClient is optional; the default is traced and time-boxed.
	HTTPClient *http.Client
	UserAgent  string
	Log        *slog.Logger
}

// Client sends notifications. Safe for concurrent use.
type Client struct {
	key       *ecdsa.PrivateKey
	keyID     string
	teamID    string
	topic     string
	baseURL   string
	userAgent string
	http      *http.Client
	log       *slog.Logger
	now       func() time.Time

	mu        sync.Mutex
	jwt       string
	jwtIssued time.Time
}

// New parses the key and checks the ids are there. It makes no request:
// nothing about the key can be checked without sending something.
func New(o Options) (*Client, error) {
	key, err := parseKey(o.Key)
	if err != nil {
		return nil, err
	}
	if o.KeyID == "" || o.TeamID == "" || o.Topic == "" {
		return nil, errors.New("apns: key id, team id and topic are all required")
	}
	httpClient := o.HTTPClient
	if httpClient == nil {
		// Traced like every other outbound call, so a slow pass of the
		// notifier reads as "waiting on Apple" rather than as unexplained time.
		httpClient = &http.Client{Timeout: defaultTimeout, Transport: obs.Transport{}}
	}
	log := o.Log
	if log == nil {
		log = slog.Default()
	}
	return &Client{
		key:       key,
		keyID:     o.KeyID,
		teamID:    o.TeamID,
		topic:     o.Topic,
		baseURL:   strings.TrimRight(o.BaseURL, "/"),
		userAgent: o.UserAgent,
		http:      httpClient,
		log:       log,
		now:       time.Now,
	}, nil
}

// Topic is the bundle id notifications are sent for.
func (c *Client) Topic() string { return c.topic }

func parseKey(pemBytes []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(bytes.TrimSpace(pemBytes))
	if block == nil {
		return nil, errors.New("apns: key is not PEM")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("apns: parse key: %w", err)
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("apns: key is not an EC key")
	}
	return key, nil
}

// Notification is one alert for one device.
type Notification struct {
	Token       string
	Environment Environment
	Title       string
	Subtitle    string
	Body        string
	// ThreadID groups alerts in Notification Center; one per feed here.
	ThreadID string
	// Data rides along beside "aps" and comes back to the app when the
	// alert is tapped — what to open.
	Data map[string]any
}

// Send delivers one notification. ErrBadToken means forget the device;
// anything else wrapped in ErrUnavailable means try again next pass.
func (c *Client) Send(ctx context.Context, n Notification) error {
	if n.Token == "" {
		return fmt.Errorf("%w: empty token", ErrBadToken)
	}
	payload := map[string]any{}
	for k, v := range n.Data {
		payload[k] = v
	}
	alert := map[string]any{"body": n.Body}
	if n.Title != "" {
		alert["title"] = n.Title
	}
	if n.Subtitle != "" {
		alert["subtitle"] = n.Subtitle
	}
	aps := map[string]any{"alert": alert, "sound": "default"}
	if n.ThreadID != "" {
		aps["thread-id"] = n.ThreadID
	}
	payload["aps"] = aps
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("%w: encode: %w", ErrUnavailable, err)
	}
	token, err := c.bearer()
	if err != nil {
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	base := c.baseURL
	if base == "" {
		base = n.Environment.host()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/3/device/"+n.Token, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	req.Header.Set("authorization", "bearer "+token)
	req.Header.Set("apns-topic", c.topic)
	req.Header.Set("apns-push-type", "alert")
	req.Header.Set("apns-priority", "10")
	req.Header.Set("apns-expiration", strconv.FormatInt(c.now().Add(expiration).Unix(), 10))
	req.Header.Set("content-type", "application/json")
	if c.userAgent != "" {
		req.Header.Set("user-agent", c.userAgent)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	reason := readReason(resp.Body)
	if isDeadToken(resp.StatusCode, reason) {
		return fmt.Errorf("%w: %s", ErrBadToken, reason)
	}
	return fmt.Errorf("%w: status %d %s", ErrUnavailable, resp.StatusCode, reason)
}

// isDeadToken is Apple's list of "and it always will be": 410 is a device
// that unregistered, the two 400 reasons are a token that never was one for
// this app in this environment.
func isDeadToken(status int, reason string) bool {
	if status == http.StatusGone {
		return true
	}
	return status == http.StatusBadRequest && (reason == "BadDeviceToken" || reason == "DeviceTokenNotForTopic")
}

func readReason(r io.Reader) string {
	var out struct {
		Reason string `json:"reason"`
	}
	raw, err := io.ReadAll(io.LimitReader(r, maxBody))
	if err != nil {
		return ""
	}
	if json.Unmarshal(raw, &out) != nil {
		return strings.TrimSpace(string(raw))
	}
	return out.Reason
}

// bearer is the current JWT, signed afresh once the last one is old enough.
func (c *Client) bearer() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if c.jwt != "" && now.Sub(c.jwtIssued) < jwtLifetime {
		return c.jwt, nil
	}
	token, err := signJWT(c.key, c.keyID, c.teamID, now)
	if err != nil {
		return "", err
	}
	c.jwt, c.jwtIssued = token, now
	return token, nil
}

// signJWT is the provider token Apple documents: an ES256-signed
// `{ alg, kid } . { iss, iat }`. Hand-rolled because it is twelve lines and
// the signature format (r‖s, not DER) is the only thing to get right.
func signJWT(key *ecdsa.PrivateKey, keyID, teamID string, now time.Time) (string, error) {
	header, err := json.Marshal(map[string]string{"alg": "ES256", "kid": keyID})
	if err != nil {
		return "", err
	}
	claims, err := json.Marshal(map[string]any{"iss": teamID, "iat": now.Unix()})
	if err != nil {
		return "", err
	}
	enc := base64.RawURLEncoding
	signing := enc.EncodeToString(header) + "." + enc.EncodeToString(claims)
	digest := sha256.Sum256([]byte(signing))
	r, s, err := ecdsa.Sign(rand.Reader, key, digest[:])
	if err != nil {
		return "", err
	}
	size := (key.Params().BitSize + 7) / 8
	sig := make([]byte, 2*size)
	r.FillBytes(sig[:size])
	s.FillBytes(sig[size:])
	return signing + "." + enc.EncodeToString(sig), nil
}
