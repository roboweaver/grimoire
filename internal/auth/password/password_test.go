package password

import (
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestHashAndVerify(t *testing.T) {
	h, err := Hash("s3cret-passphrase")
	if err != nil {
		t.Fatalf("Hash error: %v", err)
	}
	if !strings.HasPrefix(h, "$2") {
		t.Fatalf("Hash = %q, want a bcrypt $2 hash", h)
	}
	ok, err := Verify("s3cret-passphrase", h)
	if err != nil || !ok {
		t.Fatalf("Verify(correct) = (%v, %v), want (true, nil)", ok, err)
	}
	ok, err = Verify("wrong", h)
	if err != nil || ok {
		t.Fatalf("Verify(wrong) = (%v, %v), want (false, nil)", ok, err)
	}
}

// TestVerifyWPHashMagicQuotesLegacy reproduces a real WordPress historical
// quirk: wp_magic_quotes() applies addslashes() to all of $_POST (including
// pwd) before wp_signon() reads it, and wp_signon() never wp_unslash()'s the
// password field. So any password containing a quote, backslash, or NUL byte
// was actually hashed (and is later checked) in its *slashed* form by real
// WordPress — not its literal form. Imported accounts whose real password
// contains such a character can never log in unless Verify accounts for it.
//
// Hash below is genuine PHP output: wp_hash_password(addslashes("O'Brien123!")).
func TestVerifyWPHashMagicQuotesLegacy(t *testing.T) {
	const raw = "O'Brien123!"
	const h = "$wp$2y$12$pIZ38i.tj5zPQCDAd2VEG.JiCUXo.PdElX0/c/ldr80nPl2YplsUG"

	ok, err := Verify(raw, h)
	if err != nil || !ok {
		t.Fatalf("Verify(raw password containing quote) = (%v, %v), want (true, nil)", ok, err)
	}
}

func TestVerifyPhpass(t *testing.T) {
	// Genuine WordPress portable hash for "hunter2".
	const h = "$P$6WXYZ7890QmrM8eXI0pZSYhKFDWsuF0"
	ok, err := Verify("hunter2", h)
	if err != nil || !ok {
		t.Fatalf("Verify(phpass correct) = (%v, %v), want (true, nil)", ok, err)
	}
	ok, err = Verify("nope", h)
	if err != nil || ok {
		t.Fatalf("Verify(phpass wrong) = (%v, %v), want (false, nil)", ok, err)
	}
}

func TestVerifyBcrypt2yNormalization(t *testing.T) {
	// Go's bcrypt emits $2a$; WordPress/PHP often stores the functionally
	// identical $2y$ variant, which Go's bcrypt rejects outright. Verify must
	// normalize it and still match.
	raw, err := bcrypt.GenerateFromPassword([]byte("php-side"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("GenerateFromPassword: %v", err)
	}
	h2y := "$2y$" + strings.TrimPrefix(string(raw), "$2a$")
	if !strings.HasPrefix(h2y, "$2y$") {
		t.Fatalf("test setup: expected $2a$ hash, got %q", raw)
	}
	ok, err := Verify("php-side", h2y)
	if err != nil || !ok {
		t.Fatalf("Verify($2y$) = (%v, %v), want (true, nil)", ok, err)
	}
}

func TestVerifyUnknownFormat(t *testing.T) {
	if _, err := Verify("x", "not-a-hash"); err == nil {
		t.Fatalf("Verify(unknown) err = nil, want error")
	}
	if _, err := Verify("x", ""); err == nil {
		t.Fatalf("Verify(empty) err = nil, want error")
	}
}

// wpGolden is the synthetic WordPress 6.8 "$wp$" hash for wpGoldenPlain. It was
// produced by a live wp_hash_password and confirmed against wp_check_password;
// it is the only "$wp$" vector asserted by the always-on unit tests (no real
// user hashes are ever embedded in the repo).
const (
	wpGolden      = "$wp$2y$12$iWN5xRwDE7i9R5jVJvCyqOxS1CNnwUggQaF8O2W9Bg8TuXQz.ngrS"
	wpGoldenPlain = "grimoire-test-123"
)

func TestVerifyWPHash(t *testing.T) {
	ok, err := Verify(wpGoldenPlain, wpGolden)
	if err != nil || !ok {
		t.Fatalf("Verify($wp$ correct) = (%v, %v), want (true, nil)", ok, err)
	}
	ok, err = Verify("grimoire-test-124", wpGolden)
	if err != nil || ok {
		t.Fatalf("Verify($wp$ wrong) = (%v, %v), want (false, nil)", ok, err)
	}
	// A "$wp$" prefix must be recognized (not ErrUnknownFormat) even when the
	// embedded remainder is malformed; verification simply fails.
	for _, bad := range []string{"$wp$", "$wp$notbcrypt", "$wp$2y$12$short"} {
		ok, err := Verify(wpGoldenPlain, bad)
		if ok {
			t.Fatalf("Verify(malformed %q) ok = true, want false", bad)
		}
		if err == ErrUnknownFormat {
			t.Fatalf("Verify(malformed %q) err = ErrUnknownFormat, want format recognized", bad)
		}
	}
}

func TestNeedsRehashWPHash(t *testing.T) {
	// "$wp$" is WordPress's current strong standard (HMAC-SHA384 pre-hash +
	// bcrypt); grimoire verifies it in place and never rehashes it.
	if NeedsRehash(wpGolden) {
		t.Fatalf("NeedsRehash($wp$) = true, want false")
	}
}

func TestNeedsRehash(t *testing.T) {
	strong, err := bcrypt.GenerateFromPassword([]byte("pw"), DefaultCost)
	if err != nil {
		t.Fatalf("GenerateFromPassword: %v", err)
	}
	weak, err := bcrypt.GenerateFromPassword([]byte("pw"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("GenerateFromPassword: %v", err)
	}

	cases := []struct {
		name string
		hash string
		want bool
	}{
		{"phpass always rehash", "$P$6WXYZ7890QmrM8eXI0pZSYhKFDWsuF0", true},
		{"bcrypt default cost", string(strong), false},
		{"bcrypt below default cost", string(weak), true},
		{"unknown format", "garbage", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := NeedsRehash(c.hash); got != c.want {
				t.Fatalf("NeedsRehash(%q) = %v, want %v", c.hash, got, c.want)
			}
		})
	}
}
