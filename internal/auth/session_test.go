package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/roboweaver/grimoire/internal/auth/password"
	"github.com/roboweaver/grimoire/internal/domain"
)

// --- in-memory fakes -------------------------------------------------------

type fakeUsers struct {
	byLogin map[string]domain.User
	byID    map[int64]domain.User
	updated map[int64]string // id -> new hash from UpdatePass
}

func newFakeUsers() *fakeUsers {
	return &fakeUsers{byLogin: map[string]domain.User{}, byID: map[int64]domain.User{}, updated: map[int64]string{}}
}
func (f *fakeUsers) add(u domain.User) {
	f.byLogin[u.Login] = u
	f.byID[u.ID] = u
}
func (f *fakeUsers) ByLogin(_ context.Context, login string) (domain.User, error) {
	u, ok := f.byLogin[login]
	if !ok {
		return domain.User{}, domain.ErrNotFound
	}
	return u, nil
}
func (f *fakeUsers) ByID(_ context.Context, id int64) (domain.User, error) {
	u, ok := f.byID[id]
	if !ok {
		return domain.User{}, domain.ErrNotFound
	}
	return u, nil
}
func (f *fakeUsers) Create(_ context.Context, u domain.User) (int64, error) {
	f.add(u)
	return u.ID, nil
}
func (f *fakeUsers) UpdatePass(_ context.Context, id int64, hash string) error {
	if _, ok := f.byID[id]; !ok {
		return domain.ErrNotFound
	}
	f.updated[id] = hash
	u := f.byID[id]
	u.Pass = hash
	f.add(u)
	return nil
}

type fakeMeta struct{ m map[int64]map[string]string }

func newFakeMeta() *fakeMeta { return &fakeMeta{m: map[int64]map[string]string{}} }
func (f *fakeMeta) Get(_ context.Context, userID int64, key string) (string, error) {
	if v, ok := f.m[userID][key]; ok {
		return v, nil
	}
	return "", domain.ErrNotFound
}
func (f *fakeMeta) Set(_ context.Context, userID int64, key, value string) error {
	if f.m[userID] == nil {
		f.m[userID] = map[string]string{}
	}
	f.m[userID][key] = value
	return nil
}
func (f *fakeMeta) ByUser(_ context.Context, userID int64) (map[string]string, error) {
	out := map[string]string{}
	for k, v := range f.m[userID] {
		out[k] = v
	}
	return out, nil
}

type fakeSessions struct {
	m       map[string]domain.Session
	touched map[string]time.Time
}

func newFakeSessions() *fakeSessions {
	return &fakeSessions{m: map[string]domain.Session{}, touched: map[string]time.Time{}}
}
func (f *fakeSessions) Create(_ context.Context, s domain.Session) error {
	f.m[s.ID] = s
	return nil
}
func (f *fakeSessions) ByID(_ context.Context, id string) (domain.Session, error) {
	s, ok := f.m[id]
	if !ok {
		return domain.Session{}, domain.ErrNotFound
	}
	return s, nil
}
func (f *fakeSessions) Touch(_ context.Context, id string, expires time.Time) error {
	s, ok := f.m[id]
	if !ok {
		return domain.ErrNotFound
	}
	s.Expires = expires
	f.m[id] = s
	f.touched[id] = expires
	return nil
}
func (f *fakeSessions) Delete(_ context.Context, id string) error {
	delete(f.m, id)
	return nil
}
func (f *fakeSessions) DeleteByUser(_ context.Context, userID int64) (int64, error) {
	var n int64
	for id, s := range f.m {
		if s.UserID == userID {
			delete(f.m, id)
			n++
		}
	}
	return n, nil
}
func (f *fakeSessions) DeleteExpired(_ context.Context, before time.Time) (int64, error) {
	var n int64
	for id, s := range f.m {
		if s.Expires.Before(before) {
			delete(f.m, id)
			n++
		}
	}
	return n, nil
}

// --- helpers ---------------------------------------------------------------

func newTestManager(now time.Time) (*SessionManager, *fakeUsers, *fakeMeta, *fakeSessions) {
	u, meta, sess := newFakeUsers(), newFakeMeta(), newFakeSessions()
	m := &SessionManager{
		Users:    u,
		Meta:     meta,
		Sessions: sess,
		TTL:      14 * 24 * time.Hour,
		Prefix:   "wp_",
		Now:      func() time.Time { return now },
	}
	return m, u, meta, sess
}

func bcryptUser(t *testing.T, id int64, login, pw string) domain.User {
	t.Helper()
	h, err := password.Hash(pw)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	return domain.User{ID: id, Login: login, Pass: h}
}

// --- tests -----------------------------------------------------------------

func TestSessionManager_LoginSuccess(t *testing.T) {
	now := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	m, users, meta, sess := newTestManager(now)
	u := bcryptUser(t, 1, "admin", "s3cret!")
	users.add(u)
	caps, _ := SerializeCapabilities(RoleAdministrator)
	_ = meta.Set(context.Background(), 1, "wp_capabilities", caps)

	token, p, err := m.Login(context.Background(), "admin", "s3cret!")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if token == "" {
		t.Fatal("expected non-empty session token")
	}
	if !p.Can("manage_options") {
		t.Errorf("principal should carry administrator caps")
	}
	if len(sess.m) != 1 {
		t.Fatalf("expected 1 session created, got %d", len(sess.m))
	}
	for id, s := range sess.m {
		if id != hashToken(token) {
			t.Errorf("stored session id must be the hash of the token, not the token itself")
		}
		if s.UserID != 1 {
			t.Errorf("session user id = %d, want 1", s.UserID)
		}
		if s.CSRFToken == "" {
			t.Errorf("session must carry a CSRF token")
		}
		if !s.Expires.Equal(now.Add(m.TTL)) {
			t.Errorf("session expires = %v, want %v", s.Expires, now.Add(m.TTL))
		}
	}
}

func TestSessionManager_LoginWrongPassword(t *testing.T) {
	now := time.Now().UTC()
	m, users, _, sess := newTestManager(now)
	users.add(bcryptUser(t, 1, "admin", "s3cret!"))

	_, _, err := m.Login(context.Background(), "admin", "wrong")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("err = %v, want ErrInvalidCredentials", err)
	}
	if len(sess.m) != 0 {
		t.Errorf("no session should be created on failed login")
	}
}

func TestSessionManager_LoginUnknownUser(t *testing.T) {
	now := time.Now().UTC()
	m, _, _, sess := newTestManager(now)

	_, _, err := m.Login(context.Background(), "ghost", "whatever")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("err = %v, want ErrInvalidCredentials", err)
	}
	if len(sess.m) != 0 {
		t.Errorf("no session should be created for unknown user")
	}
}

// TestSessionManager_NoUserEnumerationTiming asserts that both the unknown-user
// and wrong-password paths perform a password-hash comparison, so a missing user
// cannot be distinguished from a wrong password by timing (Req 2.5). The unknown
// path must compare against the fixed throwaway hash.
func TestSessionManager_NoUserEnumerationTiming(t *testing.T) {
	orig := verifyPassword
	t.Cleanup(func() { verifyPassword = orig })

	var calls []string // hashes passed to the comparator
	verifyPassword = func(pw, hash string) (bool, error) {
		calls = append(calls, hash)
		return orig(pw, hash)
	}

	now := time.Now().UTC()
	m, users, _, _ := newTestManager(now)
	real := bcryptUser(t, 1, "admin", "s3cret!")
	users.add(real)

	// Unknown user: must still hit exactly one compare, against the dummy hash.
	calls = nil
	_, _, _ = m.Login(context.Background(), "ghost", "whatever")
	if len(calls) != 1 {
		t.Fatalf("unknown-user path made %d compares, want 1 (no fast-path return)", len(calls))
	}
	if calls[0] != dummyHash {
		t.Errorf("unknown-user compare used %q, want the fixed dummy hash", calls[0])
	}

	// Wrong password: must hit exactly one compare, against the real hash.
	calls = nil
	_, _, _ = m.Login(context.Background(), "admin", "wrong")
	if len(calls) != 1 {
		t.Fatalf("wrong-password path made %d compares, want 1", len(calls))
	}
	if calls[0] != real.Pass {
		t.Errorf("wrong-password compare used %q, want the user's stored hash", calls[0])
	}
}

func TestSessionManager_LoginRehashesPhpass(t *testing.T) {
	now := time.Now().UTC()
	m, users, _, _ := newTestManager(now)
	// Known phpass vector: password "admin".
	users.add(domain.User{ID: 5, Login: "legacy", Pass: "$P$6Salt.AbCUzpPVIK9ZXMm5j8H0Tczx."})

	_, _, err := m.Login(context.Background(), "legacy", "admin")
	if err != nil {
		t.Fatalf("Login with phpass hash: %v", err)
	}
	newHash, ok := users.updated[5]
	if !ok {
		t.Fatal("expected password to be rehashed on successful phpass login")
	}
	if !strings.HasPrefix(newHash, "$2") {
		t.Errorf("rehashed password = %q, want a bcrypt hash", newHash)
	}
}

// wpGoldenHash is the synthetic WordPress 6.8 "$wp$" hash for "grimoire-test-123"
// (see internal/auth/password). A "$wp$" login must succeed WITHOUT triggering a
// rehash: the format is already strong and grimoire verifies it in place.
const (
	wpGoldenHash  = "$wp$2y$12$iWN5xRwDE7i9R5jVJvCyqOxS1CNnwUggQaF8O2W9Bg8TuXQz.ngrS"
	wpGoldenLogin = "grimoire-test-123"
)

func TestSessionManager_LoginWPHashNoRehash(t *testing.T) {
	now := time.Now().UTC()
	m, users, _, sess := newTestManager(now)
	users.add(domain.User{ID: 7, Login: "wpuser", Pass: wpGoldenHash})

	token, _, err := m.Login(context.Background(), "wpuser", wpGoldenLogin)
	if err != nil {
		t.Fatalf("Login with $wp$ hash: %v", err)
	}
	if token == "" {
		t.Fatal("expected a non-empty session token for a $wp$ user")
	}
	if len(sess.m) != 1 {
		t.Fatalf("expected 1 session created, got %d", len(sess.m))
	}
	if newHash, ok := users.updated[7]; ok {
		t.Fatalf("a $wp$ hash must NOT be rehashed on login; UpdatePass wrote %q", newHash)
	}
}

func TestSessionManager_AuthenticateSuccessAndRollingRefresh(t *testing.T) {
	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	m, users, meta, sess := newTestManager(now)
	users.add(bcryptUser(t, 1, "admin", "pw"))
	caps, _ := SerializeCapabilities(RoleEditor)
	_ = meta.Set(context.Background(), 1, "wp_capabilities", caps)
	token, _, err := m.Login(context.Background(), "admin", "pw")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	// Advance the clock; Authenticate should refresh the expiry.
	later := now.Add(24 * time.Hour)
	m.Now = func() time.Time { return later }
	p, s, err := m.Authenticate(context.Background(), token)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if p.UserID != 1 || !p.Can("edit_others_posts") {
		t.Errorf("authenticated principal missing expected identity/caps")
	}
	if !s.Expires.Equal(later.Add(m.TTL)) {
		t.Errorf("expires not rolled forward: got %v want %v", s.Expires, later.Add(m.TTL))
	}
	if _, ok := sess.touched[hashToken(token)]; !ok {
		t.Errorf("expected session to be touched on authenticate")
	}
}

func TestSessionManager_AuthenticateExpired(t *testing.T) {
	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	m, users, _, sess := newTestManager(now)
	users.add(bcryptUser(t, 1, "admin", "pw"))
	token, _, err := m.Login(context.Background(), "admin", "pw")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	// Jump past expiry.
	m.Now = func() time.Time { return now.Add(m.TTL + time.Hour) }
	_, _, err = m.Authenticate(context.Background(), token)
	if !errors.Is(err, ErrNoSession) {
		t.Fatalf("err = %v, want ErrNoSession", err)
	}
	if len(sess.m) != 0 {
		t.Errorf("expired session should be deleted on authenticate")
	}
}

func TestSessionManager_AuthenticateUnknownToken(t *testing.T) {
	now := time.Now().UTC()
	m, _, _, _ := newTestManager(now)
	_, _, err := m.Authenticate(context.Background(), "deadbeef")
	if !errors.Is(err, ErrNoSession) {
		t.Fatalf("err = %v, want ErrNoSession", err)
	}
}

func TestSessionManager_Logout(t *testing.T) {
	now := time.Now().UTC()
	m, users, _, sess := newTestManager(now)
	users.add(bcryptUser(t, 1, "admin", "pw"))
	token, _, err := m.Login(context.Background(), "admin", "pw")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if err := m.Logout(context.Background(), token); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if len(sess.m) != 0 {
		t.Errorf("session should be gone after logout")
	}
	if _, _, err := m.Authenticate(context.Background(), token); !errors.Is(err, ErrNoSession) {
		t.Errorf("authenticate after logout = %v, want ErrNoSession", err)
	}
}

func TestSessionManager_GC(t *testing.T) {
	now := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	m, _, _, sess := newTestManager(now)
	_ = sess.Create(context.Background(), domain.Session{ID: "a", UserID: 1, Expires: now.Add(-time.Hour)})
	_ = sess.Create(context.Background(), domain.Session{ID: "b", UserID: 1, Expires: now.Add(time.Hour)})
	n, err := m.GC(context.Background())
	if err != nil {
		t.Fatalf("GC: %v", err)
	}
	if n != 1 {
		t.Errorf("GC removed %d, want 1", n)
	}
	if _, ok := sess.m["b"]; !ok {
		t.Errorf("live session should survive GC")
	}
}
