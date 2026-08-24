// Package password hashes and verifies grimoire user passwords.
//
// New and updated passwords are always hashed with bcrypt. Verification also
// accepts legacy formats found in imported WordPress databases — bcrypt written
// by PHP as $2y$ (functionally identical to Go's $2a$), phpass portable hashes
// ($P$/$H$), and WordPress 6.8+ wrapped-bcrypt hashes ($wp$, HMAC-SHA384 pre-hash
// + bcrypt) — so existing users can log in. NeedsRehash reports when a verified
// hash should be upgraded to a fresh bcrypt hash on successful login; $wp$ is
// already strong and is verified in place, never rehashed.
package password

import (
	"errors"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"github.com/roboweaver/grimoire/internal/auth/phpass"
)

// DefaultCost is the bcrypt cost used for all hashes grimoire issues. Hashes
// verified below this cost are flagged for rehashing.
const DefaultCost = bcrypt.DefaultCost

// ErrUnknownFormat is returned by Verify when the stored hash is neither a
// bcrypt nor a phpass portable hash.
var ErrUnknownFormat = errors.New("password: unrecognized hash format")

// Hash returns a bcrypt hash of password at DefaultCost.
func Hash(password string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), DefaultCost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Verify reports whether password matches the stored hash. A wrong password
// returns (false, nil); only a malformed or unrecognized hash returns a
// non-nil error. Comparisons are constant-time within each scheme.
//
// For legacy WordPress hash formats ($wp$ and phpass), a second comparison is
// attempted against the PHP addslashes()-escaped form of password if the
// literal form doesn't match. This compensates for a real, historical
// WordPress quirk: wp_magic_quotes() applies addslashes() to the entirety of
// $_POST (including the password field) before wp_signon() reads it, and
// wp_signon() never wp_unslash()'s $_POST['pwd']. So any WordPress account
// whose real password contains a quote, backslash, or NUL byte has always had
// that *slashed* string hashed and verified by WordPress itself — not the
// literal password the user typed. Without this fallback, such imported
// accounts could never log into grimoire even with their correct password.
func Verify(password, hash string) (bool, error) {
	switch {
	case isWPHash(hash):
		ok, err := wpVerify(password, hash)
		if err != nil || ok {
			return ok, err
		}
		if slashed := wpMagicQuotesSlash(password); slashed != password {
			return wpVerify(slashed, hash)
		}
		return false, nil
	case phpass.Identify(hash):
		if phpass.Verify(password, hash) {
			return true, nil
		}
		if slashed := wpMagicQuotesSlash(password); slashed != password {
			return phpass.Verify(slashed, hash), nil
		}
		return false, nil
	case isBcrypt(hash):
		err := bcrypt.CompareHashAndPassword([]byte(normalizeBcrypt(hash)), []byte(password))
		switch {
		case err == nil:
			return true, nil
		case errors.Is(err, bcrypt.ErrMismatchedHashAndPassword):
			return false, nil
		default:
			return false, err
		}
	default:
		return false, ErrUnknownFormat
	}
}

// wpMagicQuotesSlash reproduces PHP's addslashes(), which WordPress's
// wp_magic_quotes() historically applied to all of $_POST (including the
// password field) before wp_signon() ever saw it. It backslash-escapes
// single quotes, double quotes, backslashes, and NUL bytes.
func wpMagicQuotesSlash(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '\'', '"', '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case 0:
			b.WriteByte('\\')
			b.WriteByte('0')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// NeedsRehash reports whether a successfully verified hash should be replaced
// with a fresh bcrypt hash. It is true for phpass hashes, bcrypt hashes below
// DefaultCost, and any unrecognized format.
func NeedsRehash(hash string) bool {
	switch {
	case isWPHash(hash):
		return false
	case phpass.Identify(hash):
		return true
	case isBcrypt(hash):
		cost, err := bcrypt.Cost([]byte(normalizeBcrypt(hash)))
		if err != nil {
			return true
		}
		return cost < DefaultCost
	default:
		return true
	}
}

// isBcrypt reports whether hash carries a recognized bcrypt identifier.
func isBcrypt(hash string) bool {
	return strings.HasPrefix(hash, "$2a$") ||
		strings.HasPrefix(hash, "$2b$") ||
		strings.HasPrefix(hash, "$2y$")
}

// normalizeBcrypt rewrites the PHP-only $2y$ identifier to $2a$, which Go's
// bcrypt accepts. The two are cryptographically identical; $2y$ was a PHP flag
// for a bug that never affected the crypt_blowfish variant Go implements.
func normalizeBcrypt(hash string) string {
	if strings.HasPrefix(hash, "$2y$") {
		return "$2a$" + hash[len("$2y$"):]
	}
	return hash
}
