package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Seklfreak/flimm/internal/db/sqlc"
)

// NewVerifier builds an OIDC token verifier from the issuer's discovery document.
// It performs a network call (discovery + JWKS), so call it once at startup.
func NewVerifier(ctx context.Context, issuer, clientID string) (*oidc.IDTokenVerifier, error) {
	provider, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery (%s): %w", issuer, err)
	}
	// Access tokens are validated by issuer + signature + expiry. The issuer
	// is per-application, so only this app's tokens carry it — the audience
	// check is redundant, and some providers' access-token `aud` differs from
	// the client_id, so skip it. (clientID kept for reference.)
	_ = clientID
	return provider.Verifier(&oidc.Config{SkipClientIDCheck: true}), nil
}

type ctxKey string

const (
	userIDKey  ctxKey = "user-id"
	emailKey   ctxKey = "user-email"
	nameKey    ctxKey = "user-name"
	isAdminKey ctxKey = "is-admin"
)

// DevUserID is the fixed local user used when AUTH_DISABLED is set (dev). The
// server seeds a matching users row at startup (see db.BootstrapDevUser).
var DevUserID = uuid.MustParse("00000000-0000-0000-0000-000000000001")

// DevUserSub is the OIDC subject of the local dev user.
const DevUserSub = "dev-user"

type authedUser struct {
	id      uuid.UUID
	email   string
	name    string
	isAdmin bool
}

// authenticate resolves the Bearer token to a user, upserting the users row.
// With no verifier (AUTH_DISABLED) every request is the fixed dev user.
func (s *Server) authenticate(r *http.Request) (*authedUser, int, string) {
	if s.verifier == nil {
		return &authedUser{id: DevUserID, name: "Dev User", email: "dev@localhost", isAdmin: true}, 0, ""
	}
	raw := bearerToken(r)
	if raw == "" {
		return nil, http.StatusUnauthorized, "missing bearer token"
	}
	tok, err := s.verifier.Verify(r.Context(), raw)
	if err != nil {
		s.log.Warn("token verification failed", "err", err)
		return nil, http.StatusUnauthorized, "invalid token"
	}
	var claims map[string]any
	_ = tok.Claims(&claims)

	sub := stringClaim(claims, "sub")
	if sub == "" {
		return nil, http.StatusUnauthorized, "token has no subject"
	}
	email := stringClaim(claims, "email")
	name := displayName(claims)
	user, err := s.q.UpsertUser(r.Context(), sqlc.UpsertUserParams{
		OidcSub: sub,
		Email:   optText(email),
		Name:    optText(name),
	})
	if err != nil {
		s.log.Error("upsert user", "err", err)
		return nil, http.StatusInternalServerError, "failed to resolve user"
	}
	return &authedUser{id: user.ID, email: email, name: name, isAdmin: s.isAdminEmail(email)}, 0, ""
}

// authMiddleware validates the Bearer JWT on every /api/v1 request and
// stashes the user in context.
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, status, msg := s.authenticate(r)
		if u == nil {
			writeError(w, status, msg)
			return
		}
		next.ServeHTTP(w, r.WithContext(withUser(r.Context(), u)))
	})
}

func withUser(ctx context.Context, u *authedUser) context.Context {
	ctx = context.WithValue(ctx, userIDKey, u.id)
	ctx = context.WithValue(ctx, emailKey, u.email)
	ctx = context.WithValue(ctx, nameKey, u.name)
	return context.WithValue(ctx, isAdminKey, u.isAdmin)
}

// mediaAuthMiddleware guards /media/*: a Bearer header (native players) or
// the signed flimm_media cookie (browser <video>) is accepted.
func (s *Server) mediaAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if bearerToken(r) != "" {
			u, status, msg := s.authenticate(r)
			if u == nil {
				writeError(w, status, msg)
				return
			}
			next.ServeHTTP(w, r.WithContext(withUser(r.Context(), u)))
			return
		}
		if c, err := r.Cookie(mediaCookieName); err == nil {
			if uid, ok := s.verifyMediaToken(c.Value, time.Now()); ok {
				ctx := context.WithValue(r.Context(), userIDKey, uid)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
		}
		// The same signed token in the query, for a fetcher that can set
		// neither a header nor a cookie. The Apple TV's top shelf is drawn by
		// the system from URLs an extension hands it, in a process with no
		// session of ours — without this its artwork cannot be authenticated
		// at all. It is the same 12-hour, media-only token as the cookie.
		if token, _ := r.Context().Value(mediaTokenKey).(string); token != "" {
			if uid, ok := s.verifyMediaToken(token, time.Now()); ok {
				ctx := context.WithValue(r.Context(), userIDKey, uid)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
		}
		writeError(w, http.StatusUnauthorized, "media authentication required")
	})
}

// redactMediaToken replaces the media token in the request URL with a marker
// before anything logs it, and puts the real value where the middleware can
// still read it.
//
// chi's logger formats straight from `r.URL`, so the only way a credential
// stays out of the line is for it not to be in the URL by the time the logger
// sees it.
func redactMediaToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		token := query.Get(mediaTokenParam)
		if token == "" {
			next.ServeHTTP(w, r)
			return
		}
		query.Set(mediaTokenParam, "redacted")
		redacted := *r.URL
		redacted.RawQuery = query.Encode()
		r2 := r.Clone(context.WithValue(r.Context(), mediaTokenKey, token))
		r2.URL = &redacted
		r2.RequestURI = redacted.RequestURI()
		next.ServeHTTP(w, r2)
	})
}

type mediaTokenCtxKey struct{}

var mediaTokenKey = mediaTokenCtxKey{}

// ---- media token ----

const (
	mediaCookieName = "flimm_media"
	// mediaTokenParam carries the same token in a URL. Scrubbed from the
	// access log by `redactMediaToken`, because a credential in a log line is
	// a credential given away.
	mediaTokenParam = "media_token"
	// defaultMediaTokenTTL is how long a media token stays good, and the knob
	// is `MEDIA_TOKEN_SECONDS`.
	//
	// Thirty days, not the twelve hours this started at, because the token now
	// outlives the session that minted it: the Apple TV's top shelf holds URLs
	// the *system* fetches days later, and a viewer who does not open Flimm
	// for a fortnight should not come back to a row of missing pictures. The
	// web client re-mints on any media 401, so a longer life costs it nothing.
	//
	// It is a bearer credential for `/media/*` and nothing else — no API, no
	// account — signed and carrying its own user id and expiry. A month of
	// validity is a month a leaked URL would work, which is the trade being
	// made deliberately here; shorten it with the env var if that is not the
	// right trade for a deployment.
	defaultMediaTokenTTL = 30 * 24 * time.Hour
)

// mediaToken is "<user uuid>.<expiry unix>.<base64url hmac>".
func (s *Server) mediaToken(userID uuid.UUID, now time.Time) string {
	exp := now.Add(s.mediaTokenTTL).Unix()
	payload := userID.String() + "." + strconv.FormatInt(exp, 10)
	return payload + "." + s.signMedia(payload)
}

func (s *Server) signMedia(payload string) string {
	mac := hmac.New(sha256.New, s.mediaSecret)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *Server) verifyMediaToken(tok string, now time.Time) (uuid.UUID, bool) {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return uuid.Nil, false
	}
	payload := parts[0] + "." + parts[1]
	if !hmac.Equal([]byte(s.signMedia(payload)), []byte(parts[2])) {
		return uuid.Nil, false
	}
	exp, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || now.Unix() > exp {
		return uuid.Nil, false
	}
	uid, err := uuid.Parse(parts[0])
	if err != nil {
		return uuid.Nil, false
	}
	return uid, true
}

// setMediaCookie issues the flimm_media cookie for the current user.
// MediaSession is what `POST /session/media` answers with: the same token the
// cookie carries, for a client that has to put it in a URL instead.
type MediaSession struct {
	Token     string `json:"token"`
	ExpiresIn int    `json:"expires_in"`
}

func (s *Server) setMediaCookie(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r.Context())
	token := s.mediaToken(uid, time.Now())
	// Secure follows PUBLIC_URL's scheme: on in every https deploy, off only
	// for plain-http local dev where browsers would drop a Secure cookie.
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // G124: Secure is config-driven, see above
		Name:     mediaCookieName,
		Value:    token,
		Path:     "/media",
		MaxAge:   int(s.mediaTokenTTL / time.Second),
		HttpOnly: true,
		Secure:   s.secureCookies,
		SameSite: http.SameSiteLaxMode,
	})
	// The body is for the native clients; browsers use the cookie and ignore
	// it. It was a 204 before, which a client with no cookie jar could do
	// nothing with.
	writeJSON(w, http.StatusOK, MediaSession{Token: token, ExpiresIn: int(s.mediaTokenTTL / time.Second)})
}

// ---- helpers ----

// isAdminEmail reports whether the email is in the admin allowlist.
func (s *Server) isAdminEmail(email string) bool {
	email = strings.ToLower(strings.TrimSpace(email))
	return email != "" && s.adminEmails[email]
}

func isAdmin(ctx context.Context) bool {
	v, _ := ctx.Value(isAdminKey).(bool)
	return v
}

// currentUserID returns the authenticated user's id from context, or uuid.Nil
// if the request never passed through authMiddleware (shouldn't happen on /api).
func currentUserID(ctx context.Context) uuid.UUID {
	if v, ok := ctx.Value(userIDKey).(uuid.UUID); ok {
		return v
	}
	return uuid.Nil
}

func currentEmail(ctx context.Context) string {
	v, _ := ctx.Value(emailKey).(string)
	return v
}

func currentName(ctx context.Context) string {
	v, _ := ctx.Value(nameKey).(string)
	return v
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if h == "" {
		return ""
	}
	const prefix = "Bearer "
	if len(h) > len(prefix) && strings.EqualFold(h[:len(prefix)], prefix) {
		return strings.TrimSpace(h[len(prefix):])
	}
	return ""
}

func stringClaim(claims map[string]any, key string) string {
	if v, ok := claims[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

// displayName picks a human name from the usual OIDC claims, falling back to the
// preferred username (some providers populate that even when `name` is empty).
func displayName(claims map[string]any) string {
	if n := stringClaim(claims, "name"); n != "" {
		return n
	}
	return stringClaim(claims, "preferred_username")
}

func optText(s string) pgtype.Text {
	if s == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: s, Valid: true}
}
