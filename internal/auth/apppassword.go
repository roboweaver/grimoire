package auth

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/roboweaver/grimoire/internal/auth/password"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/php"
)

// appPasswordsMetaKey is the WordPress usermeta key that stores a user's
// Application Passwords. It is a literal meta_key (never table-prefixed);
// only table names use {prefix}.
const appPasswordsMetaKey = "_application_passwords"

// ApplicationPassword is a single stored Application Password record, as kept
// in a user's "_application_passwords" usermeta (a PHP-serialized indexed
// array of associative arrays). Hash is the stored digest (either the WP
// 6.8+ "$generic$" keyed-BLAKE2b form, or a pre-6.8 phpass/"$wp$"/bcrypt hash
// via the existing password.Verify fallback); the plaintext secret itself is
// never stored and is only returned once, at creation.
type ApplicationPassword struct {
	UUID     string
	AppID    string
	Name     string
	Hash     string
	Created  time.Time
	LastUsed *time.Time
	LastIP   *string
}

// ApplicationPasswords manages a user's Application Passwords: creation,
// listing, revocation, and HTTP-Basic verification with the wp_fast_hash
// ($generic$, WordPress 6.8+) primary path and a phpass/$wp$/bcrypt fallback
// for entries created by pre-6.8 WordPress installs.
type ApplicationPasswords struct {
	Users domain.UserRepository
	Meta  domain.UserMetaRepository
	// Prefix is the table/meta prefix; used only to build the
	// {prefix}capabilities meta key when constructing a Principal on
	// successful verification (shared with SessionManager via
	// PrincipalForUser).
	Prefix string
	// Now returns the current time; defaults to time.Now if nil.
	Now func() time.Time

	// userLocks serializes the load-modify-store cycle over a single
	// user's "_application_passwords" usermeta blob (Create/Revoke/Verify
	// all read the full list, mutate it, and write the whole thing back —
	// there is no row-level or CAS write in UserMetaRepository). Without
	// this, a Verify in flight for credential X racing a concurrent
	// Revoke(X) can lose the revoke: Verify's later store re-writes its
	// stale pre-revoke snapshot (now carrying an updated LastUsed),
	// silently resurrecting a credential that was just revoked. Keyed by
	// userID via sync.Map since ApplicationPasswords instances are shared
	// across requests/goroutines.
	userLocks sync.Map // map[int64]*sync.Mutex
}

// userLock returns (creating if necessary) the mutex guarding userID's
// "_application_passwords" load-modify-store cycle.
func (a *ApplicationPasswords) userLock(userID int64) *sync.Mutex {
	v, _ := a.userLocks.LoadOrStore(userID, &sync.Mutex{})
	return v.(*sync.Mutex)
}

func (a *ApplicationPasswords) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

// Create generates a new Application Password for the given user, appends it
// to their existing list (never clobbering prior entries), and returns the
// stored record plus the plaintext secret. The plaintext secret is returned
// exactly once and is not recoverable from the stored record.
func (a *ApplicationPasswords) Create(ctx context.Context, userID int64, name string) (ApplicationPassword, string, error) {
	secret, err := randToken()
	if err != nil {
		return ApplicationPassword{}, "", err
	}
	id, err := uuid.NewRandom()
	if err != nil {
		return ApplicationPassword{}, "", err
	}
	rec := ApplicationPassword{
		UUID:    id.String(),
		AppID:   uuidNoHyphens(id),
		Name:    name,
		Hash:    HashFast(secret),
		Created: a.now(),
	}

	lock := a.userLock(userID)
	lock.Lock()
	defer lock.Unlock()

	existing, err := a.loadRaw(ctx, userID)
	if err != nil {
		return ApplicationPassword{}, "", err
	}
	existing = append(existing, rec)
	if err := a.storeRaw(ctx, userID, existing); err != nil {
		return ApplicationPassword{}, "", err
	}
	return rec, secret, nil
}

// List returns all Application Passwords stored for the given user, in
// stored (creation) order.
func (a *ApplicationPasswords) List(ctx context.Context, userID int64) ([]ApplicationPassword, error) {
	return a.loadRaw(ctx, userID)
}

// Revoke removes the Application Password with the given UUID from the
// user's stored list. It is a no-op (not an error) if the UUID is not found,
// matching WordPress's idempotent revoke behavior.
func (a *ApplicationPasswords) Revoke(ctx context.Context, userID int64, id string) error {
	lock := a.userLock(userID)
	lock.Lock()
	defer lock.Unlock()

	existing, err := a.loadRaw(ctx, userID)
	if err != nil {
		return err
	}
	out := existing[:0]
	for _, rec := range existing {
		if rec.UUID != id {
			out = append(out, rec)
		}
	}
	return a.storeRaw(ctx, userID, out)
}

// Verify authenticates an Application Password presented as HTTP Basic
// credentials: login is the WordPress user_login, secret is the plaintext
// password component (WordPress Application Passwords are conventionally
// presented with spaces, which callers should strip before calling Verify).
// ip is recorded as the entry's last_ip on success (Req 8.5).
//
// Both the WordPress 6.8+ wp_fast_hash ("$generic$") format and legacy
// phpass/"$wp$"/bcrypt hashes (via the existing password.Verify fallback) are
// accepted transparently; the caller never branches on format. Any failure
// mode (unknown login, no stored Application Passwords, wrong secret) maps
// to the single ErrInvalidCredentials sentinel, never revealing which case
// occurred.
func (a *ApplicationPasswords) Verify(ctx context.Context, login, secret, ip string) (Principal, error) {
	u, err := a.Users.ByLogin(ctx, login)
	if errors.Is(err, domain.ErrNotFound) {
		return Principal{}, ErrInvalidCredentials
	}
	if err != nil {
		return Principal{}, err
	}

	lock := a.userLock(u.ID)
	lock.Lock()
	defer lock.Unlock()

	records, err := a.loadRaw(ctx, u.ID)
	if err != nil {
		return Principal{}, err
	}

	for i := range records {
		if !verifyAppPasswordHash(records[i].Hash, secret) {
			continue
		}
		now := a.now()
		records[i].LastUsed = &now
		ipCopy := ip
		records[i].LastIP = &ipCopy
		if err := a.storeRaw(ctx, u.ID, records); err != nil {
			return Principal{}, err
		}
		return PrincipalForUser(ctx, a.Meta, a.Prefix, u)
	}
	return Principal{}, ErrInvalidCredentials
}

// verifyAppPasswordHash checks secret against a stored Application Password
// hash, whether it is a WordPress 6.8+ "$generic$" wp_fast_hash digest or a
// legacy phpass/"$wp$"/bcrypt hash (via the existing password.Verify).
func verifyAppPasswordHash(hash, secret string) bool {
	if IsFastHash(hash) {
		return VerifyFast(secret, hash)
	}
	ok, err := password.Verify(secret, hash)
	return err == nil && ok
}

// loadRaw fetches and decodes a user's _application_passwords meta value. A
// missing meta row (never created any Application Passwords) yields an empty
// slice, not an error.
func (a *ApplicationPasswords) loadRaw(ctx context.Context, userID int64) ([]ApplicationPassword, error) {
	raw, err := a.Meta.Get(ctx, userID, appPasswordsMetaKey)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return decodeAppPasswords(raw)
}

// storeRaw encodes and persists the given Application Password list as the
// user's _application_passwords meta value.
func (a *ApplicationPasswords) storeRaw(ctx context.Context, userID int64, records []ApplicationPassword) error {
	raw, err := encodeAppPasswords(records)
	if err != nil {
		return err
	}
	return a.Meta.Set(ctx, userID, appPasswordsMetaKey, raw)
}

// encodeAppPasswords serializes an ordered list of Application Passwords as
// a PHP indexed array of associative arrays, matching how WordPress stores
// "_application_passwords". php.Serialize's map[string]any path sorts keys
// lexically, which would misorder an indexed array past 9 elements ("10"
// before "2"); the outer "a:N:{i:0;...i:1;...}" wrapper is therefore built
// directly here, with php.Serialize used only for each individual record.
func encodeAppPasswords(records []ApplicationPassword) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "a:%d:{", len(records))
	for i, r := range records {
		fmt.Fprintf(&b, "i:%d;", i)
		enc, err := php.Serialize(appPasswordRecordToMap(r))
		if err != nil {
			return "", err
		}
		b.WriteString(enc)
	}
	b.WriteString("}")
	return b.String(), nil
}

func appPasswordRecordToMap(r ApplicationPassword) map[string]any {
	m := map[string]any{
		"uuid":     r.UUID,
		"app_id":   r.AppID,
		"name":     r.Name,
		"password": r.Hash,
		"created":  int(r.Created.Unix()),
	}
	if r.LastUsed != nil {
		m["last_used"] = int(r.LastUsed.Unix())
	} else {
		m["last_used"] = nil
	}
	if r.LastIP != nil {
		m["last_ip"] = *r.LastIP
	} else {
		m["last_ip"] = nil
	}
	return m
}

// decodeAppPasswords parses a PHP-serialized _application_passwords value
// (an indexed array of associative-array records) into an ordered slice. An
// empty raw value yields an empty slice.
func decodeAppPasswords(raw string) ([]ApplicationPassword, error) {
	if raw == "" {
		return nil, nil
	}
	v, err := php.Unserialize(raw)
	if err != nil {
		return nil, err
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("auth: %s value is %T, want array", appPasswordsMetaKey, v)
	}

	idxs := make([]int, 0, len(m))
	for k := range m {
		n, err := strconv.Atoi(k)
		if err != nil {
			return nil, fmt.Errorf("auth: %s has non-numeric index %q", appPasswordsMetaKey, k)
		}
		idxs = append(idxs, n)
	}
	sort.Ints(idxs)

	out := make([]ApplicationPassword, 0, len(idxs))
	for _, idx := range idxs {
		rec, ok := m[strconv.Itoa(idx)].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("auth: %s entry %d is not an array", appPasswordsMetaKey, idx)
		}
		ap, err := appPasswordRecordFromMap(rec)
		if err != nil {
			return nil, err
		}
		out = append(out, ap)
	}
	return out, nil
}

func appPasswordRecordFromMap(m map[string]any) (ApplicationPassword, error) {
	var ap ApplicationPassword
	var err error
	if ap.UUID, err = appPasswordStrField(m, "uuid"); err != nil {
		return ap, err
	}
	if ap.AppID, err = appPasswordStrField(m, "app_id"); err != nil {
		return ap, err
	}
	if ap.Name, err = appPasswordStrField(m, "name"); err != nil {
		return ap, err
	}
	if ap.Hash, err = appPasswordStrField(m, "password"); err != nil {
		return ap, err
	}
	created, err := appPasswordIntField(m, "created")
	if err != nil {
		return ap, err
	}
	ap.Created = time.Unix(int64(created), 0).UTC()

	if v, ok := m["last_used"]; ok && v != nil {
		n, ok := v.(int)
		if !ok {
			return ap, fmt.Errorf("auth: %s last_used is %T, want int", appPasswordsMetaKey, v)
		}
		t := time.Unix(int64(n), 0).UTC()
		ap.LastUsed = &t
	}
	if v, ok := m["last_ip"]; ok && v != nil {
		s, ok := v.(string)
		if !ok {
			return ap, fmt.Errorf("auth: %s last_ip is %T, want string", appPasswordsMetaKey, v)
		}
		ap.LastIP = &s
	}
	return ap, nil
}

func appPasswordStrField(m map[string]any, key string) (string, error) {
	v, ok := m[key]
	if !ok {
		return "", fmt.Errorf("auth: %s missing field %q", appPasswordsMetaKey, key)
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("auth: %s field %q is %T, want string", appPasswordsMetaKey, key, v)
	}
	return s, nil
}

func appPasswordIntField(m map[string]any, key string) (int, error) {
	v, ok := m[key]
	if !ok {
		return 0, fmt.Errorf("auth: %s missing field %q", appPasswordsMetaKey, key)
	}
	n, ok := v.(int)
	if !ok {
		return 0, fmt.Errorf("auth: %s field %q is %T, want int", appPasswordsMetaKey, key, v)
	}
	return n, nil
}

// uuidNoHyphens returns the UUID's hex form without hyphens, used as the
// WordPress "app_id" (WordPress itself derives app_id from a UUIDv4 in the
// same way).
func uuidNoHyphens(id uuid.UUID) string {
	return strings.ReplaceAll(id.String(), "-", "")
}
