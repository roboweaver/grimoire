package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/php"
)

// buildRawAppPasswordsMeta hand-builds a "_application_passwords" usermeta
// value the way real WordPress stores it: a PHP-serialized indexed array of
// associative-array records. It uses the independently-tested php.Serialize
// only for each record (never apppassword.go's own encoder), so decoding it
// exercises the decode path against WordPress-shaped data, not just against
// our own round-trip.
func buildRawAppPasswordsMeta(t *testing.T, records []map[string]any) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("a:")
	b.WriteString(itoa(len(records)))
	b.WriteString(":{")
	for i, rec := range records {
		b.WriteString("i:")
		b.WriteString(itoa(i))
		b.WriteString(";")
		enc, err := php.Serialize(rec)
		if err != nil {
			t.Fatalf("php.Serialize(%#v) error: %v", rec, err)
		}
		b.WriteString(enc)
	}
	b.WriteString("}")
	return b.String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if neg {
		digits = append([]byte{'-'}, digits...)
	}
	return string(digits)
}

func newAppPasswordsManager(users *fakeUsers, meta *fakeMeta, now time.Time) *ApplicationPasswords {
	return &ApplicationPasswords{
		Users:  users,
		Meta:   meta,
		Prefix: "wp_",
		Now:    func() time.Time { return now },
	}
}

func TestApplicationPasswordsCreateAppendsWithoutClobbering(t *testing.T) {
	users := newFakeUsers()
	meta := newFakeMeta()
	u := domain.User{ID: 1, Login: "alice"}
	users.add(u)
	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	mgr := newAppPasswordsManager(users, meta, now)

	first, secret1, err := mgr.Create(context.Background(), u.ID, "First App")
	if err != nil {
		t.Fatalf("Create #1 error: %v", err)
	}
	if secret1 == "" {
		t.Fatal("Create #1: empty plaintext secret")
	}
	if first.Hash == "" || strings.Contains(first.Hash, secret1) {
		t.Fatalf("Create #1: stored hash %q must not contain/equal the plaintext secret", first.Hash)
	}
	if first.LastUsed != nil || first.LastIP != nil {
		t.Fatalf("Create #1: LastUsed/LastIP must be nil before first use, got %#v/%#v", first.LastUsed, first.LastIP)
	}
	if first.Name != "First App" {
		t.Fatalf("Create #1: Name = %q, want %q", first.Name, "First App")
	}
	if first.AppID == "" {
		t.Fatal("Create #1: AppID must not be empty")
	}
	if !first.Created.Equal(now) {
		t.Fatalf("Create #1: Created = %v, want %v", first.Created, now)
	}

	second, secret2, err := mgr.Create(context.Background(), u.ID, "Second App")
	if err != nil {
		t.Fatalf("Create #2 error: %v", err)
	}
	if secret2 == secret1 {
		t.Fatal("Create #2: secret must differ from Create #1's secret")
	}

	list, err := mgr.List(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("List error: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("List returned %d entries, want 2 (Create must append, not clobber)", len(list))
	}
	names := map[string]bool{}
	for _, ap := range list {
		names[ap.Name] = true
		if ap.Hash == "" {
			t.Fatal("List entry has empty Hash")
		}
	}
	if !names["First App"] || !names["Second App"] {
		t.Fatalf("List entries = %#v, want both First App and Second App present", list)
	}
	_ = second
}

func TestApplicationPasswordsVerifyGenericFixture(t *testing.T) {
	users := newFakeUsers()
	meta := newFakeMeta()
	u := domain.User{ID: 5, Login: "bob"}
	users.add(u)

	const secret = "wp-6-8-plus-secret-abcdef"
	hash := HashFast(secret)
	raw := buildRawAppPasswordsMeta(t, []map[string]any{
		{
			"uuid":      "11111111-1111-1111-1111-111111111111",
			"app_id":    "app-generic",
			"name":      "WP 6.8+ fixture",
			"password":  hash,
			"created":   1700000000,
			"last_used": nil,
			"last_ip":   nil,
		},
	})
	if err := meta.Set(context.Background(), u.ID, appPasswordsMetaKey, raw); err != nil {
		t.Fatalf("seeding meta: %v", err)
	}

	mgr := newAppPasswordsManager(users, meta, time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC))
	p, err := mgr.Verify(context.Background(), "bob", secret, "203.0.113.7")
	if err != nil {
		t.Fatalf("Verify($generic$ fixture) error: %v", err)
	}
	if p.UserID != u.ID || p.Login != "bob" {
		t.Fatalf("Verify returned Principal %#v, want UserID=%d Login=bob", p, u.ID)
	}
}

func TestApplicationPasswordsVerifyPreWP68Fixture(t *testing.T) {
	users := newFakeUsers()
	meta := newFakeMeta()
	u := domain.User{ID: 6, Login: "carol"}
	users.add(u)

	// A genuine phpass vector (see internal/auth/phpass/phpass_test.go),
	// standing in for a pre-6.8 WordPress-authored Application Password,
	// which is hashed identically to a login password.
	const secret = "correct horse battery staple"
	const hash = "$P$6abcd1234y7zbjnd3hLLiE10LQSp4j0"
	raw := buildRawAppPasswordsMeta(t, []map[string]any{
		{
			"uuid":      "22222222-2222-2222-2222-222222222222",
			"app_id":    "app-legacy",
			"name":      "pre-6.8 fixture",
			"password":  hash,
			"created":   1600000000,
			"last_used": nil,
			"last_ip":   nil,
		},
	})
	if err := meta.Set(context.Background(), u.ID, appPasswordsMetaKey, raw); err != nil {
		t.Fatalf("seeding meta: %v", err)
	}

	mgr := newAppPasswordsManager(users, meta, time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC))
	// Same Verify call, no format-specific branching by the caller.
	p, err := mgr.Verify(context.Background(), "carol", secret, "203.0.113.8")
	if err != nil {
		t.Fatalf("Verify(phpass fixture) error: %v", err)
	}
	if p.UserID != u.ID {
		t.Fatalf("Verify returned Principal %#v, want UserID=%d", p, u.ID)
	}
}

func TestApplicationPasswordsVerifyWrongSecretFailsBothFixtures(t *testing.T) {
	users := newFakeUsers()
	meta := newFakeMeta()

	uGeneric := domain.User{ID: 7, Login: "dave"}
	users.add(uGeneric)
	rawGeneric := buildRawAppPasswordsMeta(t, []map[string]any{
		{
			"uuid": "33333333-3333-3333-3333-333333333333", "app_id": "a", "name": "n",
			"password": HashFast("right-secret"), "created": 1234, "last_used": nil, "last_ip": nil,
		},
	})
	if err := meta.Set(context.Background(), uGeneric.ID, appPasswordsMetaKey, rawGeneric); err != nil {
		t.Fatal(err)
	}

	uLegacy := domain.User{ID: 8, Login: "erin"}
	users.add(uLegacy)
	rawLegacy := buildRawAppPasswordsMeta(t, []map[string]any{
		{
			"uuid": "44444444-4444-4444-4444-444444444444", "app_id": "a", "name": "n",
			"password": "$P$6abcd1234y7zbjnd3hLLiE10LQSp4j0", "created": 1234, "last_used": nil, "last_ip": nil,
		},
	})
	if err := meta.Set(context.Background(), uLegacy.ID, appPasswordsMetaKey, rawLegacy); err != nil {
		t.Fatal(err)
	}

	mgr := newAppPasswordsManager(users, meta, time.Now())
	if _, err := mgr.Verify(context.Background(), "dave", "wrong-secret", "1.2.3.4"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Verify(dave, wrong secret) error = %v, want ErrInvalidCredentials", err)
	}
	if _, err := mgr.Verify(context.Background(), "erin", "wrong-secret", "1.2.3.4"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Verify(erin, wrong secret) error = %v, want ErrInvalidCredentials", err)
	}
}

func TestApplicationPasswordsVerifyUpdatesLastUsedAndIP(t *testing.T) {
	users := newFakeUsers()
	meta := newFakeMeta()
	u := domain.User{ID: 9, Login: "frank"}
	users.add(u)

	now := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	mgr := newAppPasswordsManager(users, meta, now)
	created, secret, err := mgr.Create(context.Background(), u.ID, "App")
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}
	if created.LastUsed != nil || created.LastIP != nil {
		t.Fatal("newly created entry must have nil LastUsed/LastIP")
	}

	verifyTime := now.Add(time.Hour)
	mgr.Now = func() time.Time { return verifyTime }
	if _, err := mgr.Verify(context.Background(), "frank", secret, "198.51.100.9"); err != nil {
		t.Fatalf("Verify error: %v", err)
	}

	list, err := mgr.List(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("List error: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("List returned %d entries, want 1", len(list))
	}
	got := list[0]
	if got.LastUsed == nil || !got.LastUsed.Equal(verifyTime) {
		t.Fatalf("LastUsed = %#v, want %v", got.LastUsed, verifyTime)
	}
	if got.LastIP == nil || *got.LastIP != "198.51.100.9" {
		t.Fatalf("LastIP = %#v, want %q", got.LastIP, "198.51.100.9")
	}
}

func TestApplicationPasswordsRevoke(t *testing.T) {
	users := newFakeUsers()
	meta := newFakeMeta()
	u := domain.User{ID: 10, Login: "grace"}
	users.add(u)

	mgr := newAppPasswordsManager(users, meta, time.Now())
	ap, secret, err := mgr.Create(context.Background(), u.ID, "App")
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}

	if err := mgr.Revoke(context.Background(), u.ID, ap.UUID); err != nil {
		t.Fatalf("Revoke error: %v", err)
	}

	list, err := mgr.List(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("List error: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("List after revoke = %#v, want empty", list)
	}

	if _, err := mgr.Verify(context.Background(), "grace", secret, "1.2.3.4"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Verify after revoke error = %v, want ErrInvalidCredentials", err)
	}
}

func TestApplicationPasswordsVerifyNoMetaAtAllFailsCleanly(t *testing.T) {
	users := newFakeUsers()
	meta := newFakeMeta()
	u := domain.User{ID: 11, Login: "henry"}
	users.add(u)
	// No _application_passwords meta set at all for this user.

	mgr := newAppPasswordsManager(users, meta, time.Now())
	_, err := mgr.Verify(context.Background(), "henry", "any-secret", "1.2.3.4")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Verify with no meta at all error = %v, want ErrInvalidCredentials (not a panic, not a different error)", err)
	}
}

func TestApplicationPasswordsVerifyUnknownLoginFailsCleanly(t *testing.T) {
	users := newFakeUsers()
	meta := newFakeMeta()
	mgr := newAppPasswordsManager(users, meta, time.Now())
	_, err := mgr.Verify(context.Background(), "nobody", "any-secret", "1.2.3.4")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Verify(unknown login) error = %v, want ErrInvalidCredentials", err)
	}
}
