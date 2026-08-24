package auth

import (
	"crypto/subtle"
	"encoding/base64"
	"strings"

	"golang.org/x/crypto/blake2b"
)

// fastHashPrefix is the marker WordPress 6.8+ prepends to a wp_fast_hash()
// output so it can be distinguished from other stored-hash formats
// (phpass, $wp$, bcrypt) at verification time.
const fastHashPrefix = "$generic$"

// fastHashKey is the fixed literal key WordPress core bakes into
// wp_fast_hash() — not a per-site secret derived from wp-config.php, so this
// value is safe to hardcode and reproduces WordPress's own output exactly.
const fastHashKey = "wp_fast_hash_6.8+"

// fastHashSize is the digest length (in bytes) wp_fast_hash() requests from
// sodium_crypto_generichash(). It is intentionally shorter than BLAKE2b's
// default 32-byte output.
const fastHashSize = 30

// HashFast reproduces WordPress 6.8+'s wp_fast_hash(): a keyed BLAKE2b hash
// of secret (key = the fixed literal "wp_fast_hash_6.8+", output = 30
// bytes), base64url-no-pad encoded and prefixed "$generic$". It is intended
// for hashing high-entropy secrets such as Application Passwords, never
// user-chosen login passwords (see internal/auth/password for those).
func HashFast(secret string) string {
	h, err := blake2b.New(fastHashSize, []byte(fastHashKey))
	if err != nil {
		// Only returns an error for an invalid size/key length, and both are
		// fixed, valid, compile-time constants here.
		panic("auth: invalid wp_fast_hash parameters: " + err.Error())
	}
	h.Write([]byte(secret))
	sum := h.Sum(nil)
	return fastHashPrefix + base64.RawURLEncoding.EncodeToString(sum)
}

// VerifyFast reports whether secret matches a "$generic$"-prefixed hash
// produced by HashFast (or real WordPress's wp_fast_hash()), matching
// WordPress's own wp_verify_fast_hash(). It returns false for any hash that
// isn't "$generic$"-prefixed or isn't validly formed, rather than erroring —
// callers fall back to internal/auth/password.Verify for other formats.
func VerifyFast(secret, hash string) bool {
	if !strings.HasPrefix(hash, fastHashPrefix) {
		return false
	}
	want, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(hash, fastHashPrefix))
	if err != nil {
		return false
	}
	h, err := blake2b.New(fastHashSize, []byte(fastHashKey))
	if err != nil {
		panic("auth: invalid wp_fast_hash parameters: " + err.Error())
	}
	h.Write([]byte(secret))
	got := h.Sum(nil)
	return subtle.ConstantTimeCompare(got, want) == 1
}

// IsFastHash reports whether hash is in the "$generic$" (wp_fast_hash)
// format, as opposed to phpass/$wp$/bcrypt.
func IsFastHash(hash string) bool {
	return strings.HasPrefix(hash, fastHashPrefix)
}
