package auth

import "testing"

// TestHashFastKnownVector checks HashFast against a keyed-BLAKE2b vector
// computed independently of this package's implementation, using Python's
// hashlib.blake2b (RFC 7693 BLAKE2b, the same primitive real WordPress
// invokes via libsodium's sodium_crypto_generichash):
//
//	hashlib.blake2b(b"abcd1234EFGH5678ijkl9012", digest_size=30,
//	    key=b"wp_fast_hash_6.8+")
//	base64.urlsafe_b64encode(...).rstrip(b"=")
//
// This matches the documented wp_fast_hash() algorithm exactly:
// sodium_crypto_generichash($message, 'wp_fast_hash_6.8+', 30), base64url
// no-pad encoded, "$generic$"-prefixed.
func TestHashFastKnownVector(t *testing.T) {
	const secret = "abcd1234EFGH5678ijkl9012"
	const want = "$generic$-r4FB5NrhNs1VZlhj9uMueqVu30SZqjhd5q-NUtA"

	got := HashFast(secret)
	if got != want {
		t.Fatalf("HashFast(%q) = %q, want %q", secret, got, want)
	}
}

func TestVerifyFastKnownVector(t *testing.T) {
	const secret = "abcd1234EFGH5678ijkl9012"
	const hash = "$generic$-r4FB5NrhNs1VZlhj9uMueqVu30SZqjhd5q-NUtA"

	if !VerifyFast(secret, hash) {
		t.Fatalf("VerifyFast(%q, %q) = false, want true", secret, hash)
	}
}

func TestVerifyFastWrongSecretFails(t *testing.T) {
	const hash = "$generic$-r4FB5NrhNs1VZlhj9uMueqVu30SZqjhd5q-NUtA"

	if VerifyFast("wrong-secret", hash) {
		t.Fatal("VerifyFast with wrong secret = true, want false")
	}
}

// TestHashFastDeterministic confirms wp_fast_hash has no random salt: the
// "salt" is the fixed literal key baked into WordPress core, so hashing the
// same secret twice always yields the same $generic$ hash.
func TestHashFastDeterministic(t *testing.T) {
	const secret = "some-application-password-secret"
	a := HashFast(secret)
	b := HashFast(secret)
	if a != b {
		t.Fatalf("HashFast not deterministic: %q != %q", a, b)
	}
}

func TestVerifyFastRejectsMalformedHash(t *testing.T) {
	if VerifyFast("secret", "not-a-generic-hash") {
		t.Fatal("VerifyFast against malformed hash = true, want false")
	}
	if VerifyFast("secret", "$generic$not-valid-base64!!!") {
		t.Fatal("VerifyFast against invalid base64 payload = true, want false")
	}
}
