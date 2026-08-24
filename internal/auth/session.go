package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/roboweaver/grimoire/internal/auth/password"
	"github.com/roboweaver/grimoire/internal/domain"
)

// DefaultTTL is the rolling session lifetime used when a SessionManager does not
// set one explicitly.
const DefaultTTL = 14 * 24 * time.Hour

// Generic, non-enumerating errors surfaced to callers.
var (
	// ErrInvalidCredentials is returned for every failed login regardless of
	// whether the username exists, to prevent user enumeration.
	ErrInvalidCredentials = errors.New("auth: invalid username or password")
	// ErrNoSession is returned when a token maps to no live session.
	ErrNoSession = errors.New("auth: no valid session")
)

// dummyHash is a fixed, valid bcrypt hash used to perform a throwaway password
// comparison when the supplied username does not exist. Comparing against it
// makes an unknown-user login take the same time as a wrong-password login, so
// the two cannot be distinguished by timing.
const dummyHash = "$2a$10$BuBiyRraVWDuEMWVVgVxueNR7JvX2AmusApCL/9ahqOfvH.fhhkIK"

// verifyPassword is the password comparator, indirected so tests can observe
// that both the unknown-user and wrong-password paths perform a hash compare.
var verifyPassword = password.Verify

// SessionManager implements login, session authentication with rolling expiry,
// logout, and expired-session garbage collection over the domain repositories.
type SessionManager struct {
	Users    domain.UserRepository
	Meta     domain.UserMetaRepository
	Sessions domain.SessionRepository
	// TTL is the rolling session lifetime; zero means DefaultTTL.
	TTL time.Duration
	// Prefix is the table/meta prefix; the capabilities meta key is
	// Prefix+"capabilities".
	Prefix string
	// Now supplies the current time; nil means time.Now. Injected for tests.
	Now func() time.Time
}

func (m *SessionManager) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func (m *SessionManager) ttl() time.Duration {
	if m.TTL > 0 {
		return m.TTL
	}
	return DefaultTTL
}

func (m *SessionManager) capsKey() string { return m.Prefix + "capabilities" }

// Login verifies a username/password pair and, on success, creates a session
// and returns the opaque session token (for the cookie) and the user's
// Principal. On any failure it returns ErrInvalidCredentials without revealing
// whether the username exists. A successful login against a legacy phpass hash
// transparently rehashes the password to bcrypt.
func (m *SessionManager) Login(ctx context.Context, username, pw string) (string, Principal, error) {
	// WordPress's wp_authenticate() does `$password = trim($password)` before
	// running the authenticate filter chain (and thus before
	// wp_check_password). Replicate that here so a password with incidental
	// surrounding whitespace (e.g. from a credential manager) verifies the
	// same way it would against real WordPress.
	pw = strings.TrimSpace(pw)

	u, err := m.Users.ByLogin(ctx, username)
	if errors.Is(err, domain.ErrNotFound) {
		// Constant-time guard: compare against a throwaway hash so a missing
		// user is indistinguishable from a wrong password by timing.
		_, _ = verifyPassword(pw, dummyHash)
		return "", Principal{}, ErrInvalidCredentials
	}
	if err != nil {
		return "", Principal{}, err
	}

	ok, verr := verifyPassword(pw, u.Pass)
	if verr != nil || !ok {
		return "", Principal{}, ErrInvalidCredentials
	}

	// Upgrade legacy hashes to bcrypt on successful login (best effort).
	if password.NeedsRehash(u.Pass) {
		if nh, herr := password.Hash(pw); herr == nil {
			_ = m.Users.UpdatePass(ctx, u.ID, nh)
		}
	}

	p, err := m.principal(ctx, u)
	if err != nil {
		return "", Principal{}, err
	}

	token, err := randToken()
	if err != nil {
		return "", Principal{}, err
	}
	csrf, err := randToken()
	if err != nil {
		return "", Principal{}, err
	}
	now := m.now()
	s := domain.Session{
		ID:        hashToken(token),
		UserID:    u.ID,
		CSRFToken: csrf,
		Created:   now,
		Expires:   now.Add(m.ttl()),
	}
	if err := m.Sessions.Create(ctx, s); err != nil {
		return "", Principal{}, err
	}
	return token, p, nil
}

// Authenticate resolves a session token to its Principal and session, applying a
// rolling-expiry refresh. Expired or unknown tokens yield ErrNoSession; expired
// sessions are deleted as a side effect.
func (m *SessionManager) Authenticate(ctx context.Context, token string) (Principal, domain.Session, error) {
	id := hashToken(token)
	s, err := m.Sessions.ByID(ctx, id)
	if errors.Is(err, domain.ErrNotFound) {
		return Principal{}, domain.Session{}, ErrNoSession
	}
	if err != nil {
		return Principal{}, domain.Session{}, err
	}

	now := m.now()
	if !s.Expires.After(now) {
		_ = m.Sessions.Delete(ctx, id)
		return Principal{}, domain.Session{}, ErrNoSession
	}

	newExpiry := now.Add(m.ttl())
	if err := m.Sessions.Touch(ctx, id, newExpiry); err != nil && !errors.Is(err, domain.ErrNotFound) {
		return Principal{}, domain.Session{}, err
	}
	s.Expires = newExpiry

	u, err := m.Users.ByID(ctx, s.UserID)
	if errors.Is(err, domain.ErrNotFound) {
		_ = m.Sessions.Delete(ctx, id)
		return Principal{}, domain.Session{}, ErrNoSession
	}
	if err != nil {
		return Principal{}, domain.Session{}, err
	}

	p, err := m.principal(ctx, u)
	if err != nil {
		return Principal{}, domain.Session{}, err
	}
	return p, s, nil
}

// Logout deletes the session identified by the token. It is idempotent.
func (m *SessionManager) Logout(ctx context.Context, token string) error {
	return m.Sessions.Delete(ctx, hashToken(token))
}

// GC deletes all sessions whose expiry has passed and returns the count removed.
func (m *SessionManager) GC(ctx context.Context) (int64, error) {
	return m.Sessions.DeleteExpired(ctx, m.now())
}

// principal builds the Principal for a user from their capabilities meta.
// A missing or malformed capabilities value yields a role-less principal.
func (m *SessionManager) principal(ctx context.Context, u domain.User) (Principal, error) {
	raw, err := m.Meta.Get(ctx, u.ID, m.capsKey())
	if errors.Is(err, domain.ErrNotFound) {
		return NewPrincipal(u.ID, u.Login, nil), nil
	}
	if err != nil {
		return Principal{}, err
	}
	keys, perr := ParseCapabilities(raw)
	if perr != nil {
		return NewPrincipal(u.ID, u.Login, nil), nil
	}
	return NewPrincipal(u.ID, u.Login, keys), nil
}

// randToken returns 32 bytes of cryptographically-random data as a 64-character
// hex string. It is used for opaque session tokens and CSRF tokens.
func randToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// hashToken returns the hex-encoded SHA-256 of a token. Only this hash is stored
// server-side, so a leaked sessions table cannot be used to forge cookies.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
