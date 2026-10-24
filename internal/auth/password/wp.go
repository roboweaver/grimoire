package password

import (
	"crypto/hmac"
	"crypto/sha512"
	"encoding/base64"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// WordPress 6.8+ stores passwords as "$wp$" immediately followed by a standard
// bcrypt hash of a domain-separated HMAC-SHA384 pre-hash of the password. The
// pre-hash exists so passwords longer than bcrypt's 72-byte input limit are not
// truncated. See wp-includes/pluggable.php (wp_hash_password/wp_check_password):
//
//	"$wp$" . bcrypt( base64_encode( hash_hmac('sha384', $pw, 'wp-sha384', true) ) )
const (
	wpPrefix  = "$wp$"
	wpHMACKey = "wp-sha384"
)

// isWPHash reports whether hash is a WordPress 6.8 wrapped-bcrypt hash.
func isWPHash(hash string) bool { return strings.HasPrefix(hash, wpPrefix) }

// HashWP returns grimoire's issued password hash: a WordPress 6.8 "$wp$"
// wrapped-bcrypt hash, "$wp$" + bcrypt(wpPreHash(password)) at DefaultCost. The
// HMAC-SHA384 pre-hash (wpPreHash) domain-separates the input and bounds its
// length to 64 bytes, within bcrypt's 72-byte limit, so a password longer than
// 72 bytes is not silently truncated. The embedded bcrypt is Go's "$2a$", which
// wpVerify reads back through normalizeBcrypt; a WordPress-written "$wp$2y$..."
// and a grimoire-written "$wp$2a$..." both verify. It reproduces WordPress's
// wp_hash_password.
func HashWP(password string) (string, error) {
	b, err := bcrypt.GenerateFromPassword(wpPreHash(password), DefaultCost)
	if err != nil {
		return "", err
	}
	// wpPrefix ("$wp$") already carries the '$' that opens the embedded bcrypt
	// hash (which starts "$2a$..."). Concatenate on the literal "$wp" so the
	// result is "$wp" + "$2a$..." = "$wp$2a$...", the exact shape wpVerify reads
	// back via hash[len("$wp"):] -- not "$wp$" + "$2a$..." = "$wp$$2a$..." (a
	// double '$' bcrypt cannot parse).
	return "$wp" + string(b), nil
}

// wpPreHash reproduces WordPress's pre-hash of the password:
// base64_encode( hash_hmac('sha384', password, 'wp-sha384', true) ). SHA-384 is
// provided by crypto/sha512 as New384.
func wpPreHash(password string) []byte {
	mac := hmac.New(sha512.New384, []byte(wpHMACKey))
	mac.Write([]byte(password))
	sum := mac.Sum(nil)
	enc := make([]byte, base64.StdEncoding.EncodedLen(len(sum)))
	base64.StdEncoding.Encode(enc, sum)
	return enc
}

// wpVerify verifies password against a "$wp$"-prefixed WordPress 6.8 hash. A
// wrong password or a malformed embedded bcrypt hash returns (false, nil); the
// comparison is bcrypt's constant-time compare. Only a non-"$wp$" input (which
// wpVerify should never be called with) yields ErrUnknownFormat.
func wpVerify(password, hash string) (bool, error) {
	if !isWPHash(hash) {
		return false, ErrUnknownFormat
	}
	// Strip the literal "$wp" (3 chars), keeping the leading '$' of the embedded
	// "$2y$..." bcrypt hash. TrimPrefix("$wp$") would wrongly drop that '$'.
	bc := normalizeBcrypt(hash[len("$wp"):])
	if bcrypt.CompareHashAndPassword([]byte(bc), wpPreHash(password)) != nil {
		return false, nil
	}
	return true, nil
}
