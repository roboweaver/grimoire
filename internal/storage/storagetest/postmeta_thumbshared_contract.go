package storagetest

import (
	"context"
	"strconv"
	"testing"

	"github.com/roboweaver/grimoire/internal/storage/rebind"
)

// Fixture ids for the ThumbSharedElsewhere contract, above the other contracts.
const (
	tshExcluded int64 = 9960 // attachment whose delete is being decided
	tshOther    int64 = 9961 // another attachment
	tshPlain    int64 = 9962 // non-attachment post carrying the same meta key
)

// tshThumbSharer is the lookup under test. The contract asserts the concrete
// repository exposes it, so a missing method fails the contract at runtime
// rather than breaking compilation of the whole package.
type tshThumbSharer interface {
	ThumbSharedElsewhere(ctx context.Context, excludeID int64, filename string) (bool, error)
}

// tshMeta builds a PHP-serialized _wp_attachment_metadata value whose "thumb"
// entry names thumb.
func tshMeta(thumb string) string {
	return `a:2:{s:4:"file";s:7:"x/m.jpg";s:5:"thumb";s:` + strconv.Itoa(len(thumb)) + `:"` + thumb + `";}`
}

// RunPostMetaThumbSharedContract covers ThumbSharedElsewhere: whether any
// attachment other than excludeID names filename as its metadata "thumb".
// Matching is on the whole filename, so a filename that is merely a substring
// of another file name, or that contains LIKE wildcards, is compared literally.
//
// The vendor and table prefix come from newRepos; the contract holds no
// literal prefix and no vendor-specific placeholder.
func RunPostMetaThumbSharedContract(t *testing.T, newRepos NewReposWithPrefixFunc) {
	t.Helper()
	ctx := context.Background()

	// setup seeds the three posts, then one metadata row per (post, value).
	type row struct {
		post  int64
		value string
	}
	setup := func(t *testing.T, rows ...row) (tshThumbSharer, func()) {
		t.Helper()
		r, vendor, prefix, cl := newRepos(t)
		db := r.DB()
		exec := func(q string, args ...any) {
			t.Helper()
			if _, err := db.ExecContext(ctx, rebind.Rebind(vendor, q), args...); err != nil {
				t.Fatalf("seed %q: %v", q, err)
			}
		}
		exec(postInsert(vendor, prefix), postArgs(tshExcluded, "tsh-excluded", "T tsh-excluded", "attachment", "publish", "2000-01-01 00:00:00", "open", 0, "image/jpeg", 0)...)
		exec(postInsert(vendor, prefix), postArgs(tshOther, "tsh-other", "T tsh-other", "attachment", "publish", "2000-01-01 00:00:00", "open", 0, "image/jpeg", 0)...)
		exec(postInsert(vendor, prefix), postArgs(tshPlain, "tsh-plain", "T tsh-plain", "post", "publish", "2000-01-01 00:00:00", "open", 0, "", 0)...)
		for _, rw := range rows {
			exec(`INSERT INTO `+prefix+`postmeta (post_id, meta_key, meta_value) VALUES (?, ?, ?)`, rw.post, "_wp_attachment_metadata", rw.value)
		}
		s, ok := r.PostMeta.(tshThumbSharer)
		if !ok {
			cl()
			t.Fatalf("PostMeta repository %T has no ThumbSharedElsewhere(ctx, excludeID, filename) (bool, error)", r.PostMeta)
		}
		return s, cl
	}
	check := func(t *testing.T, s tshThumbSharer, exclude int64, name string, want bool) {
		t.Helper()
		got, err := s.ThumbSharedElsewhere(ctx, exclude, name)
		if err != nil {
			t.Fatalf("ThumbSharedElsewhere(%d, %q): %v", exclude, name, err)
		}
		if got != want {
			t.Errorf("ThumbSharedElsewhere(%d, %q) = %v, want %v", exclude, name, got, want)
		}
	}

	t.Run("another attachment naming the thumb is shared", func(t *testing.T) {
		s, cl := setup(t, row{tshExcluded, tshMeta("a.jpg")}, row{tshOther, tshMeta("a.jpg")})
		defer cl()
		check(t, s, tshExcluded, "a.jpg", true)
	})

	t.Run("only the excluded attachment naming the thumb is not shared", func(t *testing.T) {
		s, cl := setup(t, row{tshExcluded, tshMeta("a.jpg")}, row{tshOther, tshMeta("z.jpg")})
		defer cl()
		check(t, s, tshExcluded, "a.jpg", false)
	})

	t.Run("a filename that is a substring of another file name is not shared", func(t *testing.T) {
		s, cl := setup(t, row{tshOther, tshMeta("ba.jpg")})
		defer cl()
		check(t, s, tshExcluded, "a.jpg", false)
	})

	t.Run("percent in the filename is literal", func(t *testing.T) {
		s, cl := setup(t, row{tshOther, tshMeta("aXb.jpg")})
		defer cl()
		check(t, s, tshExcluded, "a%b.jpg", false)
		check(t, s, tshExcluded, "%", false)
	})

	t.Run("underscore in the filename is literal", func(t *testing.T) {
		s, cl := setup(t, row{tshOther, tshMeta("abc.jpg")})
		defer cl()
		check(t, s, tshExcluded, "a_c.jpg", false)
	})

	t.Run("a filename containing wildcards still matches itself", func(t *testing.T) {
		s, cl := setup(t, row{tshOther, tshMeta("a%b_c.jpg")})
		defer cl()
		check(t, s, tshExcluded, "a%b_c.jpg", true)
	})

	t.Run("metadata on a non-attachment post is irrelevant", func(t *testing.T) {
		s, cl := setup(t, row{tshPlain, tshMeta("a.jpg")})
		defer cl()
		check(t, s, tshExcluded, "a.jpg", false)
	})

	t.Run("empty filename is not shared", func(t *testing.T) {
		s, cl := setup(t, row{tshOther, tshMeta("a.jpg")}, row{tshOther, tshMeta("")})
		defer cl()
		check(t, s, tshExcluded, "", false)
	})
}
