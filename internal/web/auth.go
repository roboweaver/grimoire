package web

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/url"
	"strings"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/domain"
)

// Sessions is the auth surface the web layer depends on. *auth.SessionManager
// satisfies it; tests inject a fake so handler behavior can be exercised without
// a database.
type Sessions interface {
	Login(ctx context.Context, username, pw string) (string, auth.Principal, error)
	Authenticate(ctx context.Context, token string) (auth.Principal, domain.Session, error)
	Logout(ctx context.Context, token string) error
}

// Default cookie names. Kept as constants (not secrets) so tests and the CLI can
// reference them.
const (
	sessionCookieName = "grimoire_session"
	csrfCookieName    = "grimoire_csrf"
	csrfFieldName     = "csrf_token"
)

// AuthConfig tunes cookie emission. Secure gates the Secure attribute (set true
// behind TLS); CookieName overrides the session cookie name; MaxAge overrides
// the session cookie Max-Age in seconds.
type AuthConfig struct {
	CookieName string
	Secure     bool
	MaxAge     int
}

// DefaultCookieMaxAge is the session cookie Max-Age used when AuthConfig.MaxAge
// is unset: 14 days, matching the server-side rolling session TTL.
const DefaultCookieMaxAge = 14 * 24 * 3600

func (c AuthConfig) cookieName() string {
	if c.CookieName != "" {
		return c.CookieName
	}
	return sessionCookieName
}

func (c AuthConfig) maxAge() int {
	if c.MaxAge > 0 {
		return c.MaxAge
	}
	return DefaultCookieMaxAge
}

// ctxKey is a private context key type to avoid collisions.
type ctxKey int

const (
	principalKey ctxKey = iota
	sessionKey
)

// withAuth returns a copy of ctx carrying the authenticated principal and its
// session (for CSRF synchronizer-token checks).
func withAuth(ctx context.Context, p auth.Principal, s domain.Session) context.Context {
	ctx = context.WithValue(ctx, principalKey, p)
	ctx = context.WithValue(ctx, sessionKey, s)
	return ctx
}

// PrincipalFrom returns the authenticated principal on the request context, if
// any. The second result is false for anonymous requests.
func PrincipalFrom(ctx context.Context) (auth.Principal, bool) {
	p, ok := ctx.Value(principalKey).(auth.Principal)
	return p, ok
}

// sessionFrom returns the live session on the request context, if any.
func sessionFrom(ctx context.Context) (domain.Session, bool) {
	s, ok := ctx.Value(sessionKey).(domain.Session)
	return s, ok
}

// randToken returns a 256-bit random token, hex-encoded (64 chars).
func randToken() string {
	var b [32]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// constantTimeEqual compares two strings without leaking length-independent
// timing on the compared bytes.
func constantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// safeRedirect returns dst only if it is a local path (single leading slash, no
// scheme/host), else "/". This blocks open-redirect via the post-login target.
//
// Backslashes are rejected outright because browsers normalize "\" to "/", so a
// value like "/\evil.com" would be treated as the scheme-relative URL
// "//evil.com" pointing at an external host. As a belt-and-suspenders check the
// value is also parsed and must yield an empty Host and empty Scheme.
func safeRedirect(dst string) string {
	if dst == "" || !strings.HasPrefix(dst, "/") || strings.HasPrefix(dst, "//") {
		return "/"
	}
	if strings.Contains(dst, `\`) {
		return "/"
	}
	u, err := url.Parse(dst)
	if err != nil || u.Host != "" || u.Scheme != "" {
		return "/"
	}
	return dst
}
