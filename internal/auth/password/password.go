// Package password hashes and verifies grimoire user passwords.
//
// New and updated passwords are always hashed with bcrypt. Verification also
// accepts legacy formats found in imported WordPress databases — bcrypt written
// by PHP as $2y$ (functionally identical to Go's $2a$) and phpass portable
// hashes ($P$/$H$) — so existing users can log in. NeedsRehash reports when a
// verified hash should be upgraded to a fresh bcrypt hash on successful login.
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
func Verify(password, hash string) (bool, error) {
	switch {
	case phpass.Identify(hash):
		return phpass.Verify(password, hash), nil
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

// NeedsRehash reports whether a successfully verified hash should be replaced
// with a fresh bcrypt hash. It is true for phpass hashes, bcrypt hashes below
// DefaultCost, and any unrecognized format.
func NeedsRehash(hash string) bool {
	switch {
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
