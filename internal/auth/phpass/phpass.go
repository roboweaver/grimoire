// Package phpass verifies WordPress "portable" password hashes ($P$ and $H$),
// the iterated-MD5 scheme produced by the phpass PasswordHash class that older
// WordPress installs (and imported databases) still carry.
//
// grimoire never issues portable hashes — it hashes with bcrypt. This package
// exists so that a user whose stored hash is still a phpass hash can log in,
// after which the caller transparently upgrades them to bcrypt. Only
// verification is implemented; there is deliberately no Hash function.
package phpass

import (
	"crypto/md5" //nolint:gosec // WordPress phpass compatibility: MD5 is required for verifying legacy $P$/$H$ hashes, verification-only
	"crypto/subtle"
	"strings"
)

// itoa64 is phpass's custom base-64 alphabet (note the leading "./"), used both
// for the cost character and for encoding the raw digest.
const itoa64 = "./0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// Identify reports whether hash looks like a phpass portable hash ($P$ or $H$).
func Identify(hash string) bool {
	if len(hash) < 3 {
		return false
	}
	id := hash[:3]
	return id == "$P$" || id == "$H$"
}

// Verify reports whether password matches the stored phpass portable hash. It
// returns false for any malformed or non-portable input rather than erroring,
// so callers can chain it with other verifiers. The final comparison is
// constant-time.
func Verify(password, stored string) bool {
	if !Identify(stored) || len(stored) != 34 {
		return false
	}

	countLog2 := strings.IndexByte(itoa64, stored[3])
	if countLog2 < 7 || countLog2 > 30 {
		return false
	}
	count := 1 << uint(countLog2)

	salt := stored[4:12]
	if len(salt) != 8 {
		return false
	}

	// Iterated MD5: md5(salt+password), then md5(prev+password) count times.
	sum := md5.Sum([]byte(salt + password)) //nolint:gosec // WordPress phpass compatibility: MD5 required for legacy hash verification, verification-only
	digest := sum[:]
	buf := make([]byte, 0, len(digest)+len(password))
	for i := 0; i < count; i++ {
		buf = append(buf[:0], digest...)
		buf = append(buf, password...)
		next := md5.Sum(buf) //nolint:gosec // WordPress phpass compatibility: MD5 required for legacy hash verification, verification-only
		digest = append(digest[:0], next[:]...)
	}

	output := stored[:12] + encode64(digest, len(digest))
	return subtle.ConstantTimeCompare([]byte(output), []byte(stored)) == 1
}

// encode64 mirrors phpass's PasswordHash::encode64, a bespoke little-endian
// base-64 over the itoa64 alphabet. For a 16-byte MD5 digest it yields 22
// characters.
func encode64(input []byte, count int) string {
	var out strings.Builder
	i := 0
	for {
		value := int(input[i])
		i++
		out.WriteByte(itoa64[value&0x3f])
		if i < count {
			value |= int(input[i]) << 8
		}
		out.WriteByte(itoa64[(value>>6)&0x3f])
		if i >= count {
			break
		}
		i++
		if i < count {
			value |= int(input[i]) << 16
		}
		out.WriteByte(itoa64[(value>>12)&0x3f])
		if i >= count {
			break
		}
		i++
		out.WriteByte(itoa64[(value>>18)&0x3f])
		if i >= count {
			break
		}
	}
	return out.String()
}
