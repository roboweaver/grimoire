package storagetest

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/roboweaver/grimoire/internal/auth/password"
	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/storage"
)

// TestRealWordPressDB validates grimoire against a restored, REAL WordPress
// database (read-only). It is gated behind GRIMOIRE_TEST_WP_DSN so CI without
// the database still passes.
//
// Run it against the provisioned MariaDB copy with:
//
//	GRIMOIRE_TEST_WP_DSN='grimoire:PASS@tcp(127.0.0.1:3306)/wordpress?parseTime=true&loc=UTC' \
//	GRIMOIRE_TEST_WP_PREFIX=accuweaver \
//	go test ./internal/storage/storagetest/ -run TestRealWordPressDB -v -count=1
//
// The real DB is READ-ONLY here: the test never migrates or writes. It reads a
// phpass ($P$) and a $wp$ user's stored hash directly and asserts grimoire's
// verifier RECOGNIZES both formats (a wrong password must fail cleanly, never
// ErrUnknownFormat). No real users' hashes are hardcoded — they are read from
// the live DB at run time. An always-on synthetic $wp$ vector is covered by the
// unit tests in internal/auth/password.
//
// Optionally set GRIMOIRE_TEST_WP_LOGIN + GRIMOIRE_TEST_WP_PASSWORD to assert a
// full known-plaintext verification against a real account.
func TestRealWordPressDB(t *testing.T) {
	dsn := os.Getenv("GRIMOIRE_TEST_WP_DSN")
	if dsn == "" {
		t.Skip("set GRIMOIRE_TEST_WP_DSN to run the real WordPress DB validation")
	}
	prefix := os.Getenv("GRIMOIRE_TEST_WP_PREFIX")
	if prefix == "" {
		prefix = "accuweaver"
	}

	ctx := context.Background()
	cfg := config.DatabaseConfig{Vendor: "mysql", DSN: dsn, TablePrefix: prefix}
	repos, err := storage.New(cfg) // opens the pool WITHOUT migrating
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	defer repos.Close()

	t.Run("reads real posts", func(t *testing.T) {
		posts, err := repos.Posts.RecentPosts(ctx, 5, 0)
		if err != nil {
			t.Fatalf("RecentPosts: %v", err)
		}
		if len(posts) == 0 {
			t.Fatal("expected at least one published post in the real DB")
		}
		t.Logf("read %d recent posts; newest title=%q", len(posts), posts[0].Title)
	})

	// verifiesFormat reads one user_login whose stored hash matches likePrefix
	// and asserts grimoire recognizes the format: a deliberately wrong password
	// must return (false, nil) — NOT ErrUnknownFormat.
	verifiesFormat := func(t *testing.T, label, likePrefix string) {
		t.Helper()
		var login string
		row := repos.DB().QueryRowContext(ctx,
			"SELECT user_login FROM "+prefix+"users WHERE user_pass LIKE ? LIMIT 1",
			likePrefix+"%")
		if err := row.Scan(&login); err != nil {
			t.Skipf("no %s user found (LIKE %q): %v", label, likePrefix, err)
		}
		u, err := repos.Users.ByLogin(ctx, login)
		if err != nil {
			t.Fatalf("ByLogin(%q): %v", login, err)
		}
		ok, err := password.Verify("definitely-the-wrong-password", u.Pass)
		if err != nil {
			t.Fatalf("%s Verify returned error (format not recognized): %v", label, err)
		}
		if ok {
			t.Fatalf("%s: wrong password unexpectedly verified true", label)
		}
		t.Logf("%s format recognized for user %q (wrong password correctly rejected)", label, login)
	}

	t.Run("recognizes phpass $P$ format", func(t *testing.T) {
		verifiesFormat(t, "phpass", "$P$")
	})
	t.Run("recognizes WordPress 6.8 $wp$ format", func(t *testing.T) {
		verifiesFormat(t, "$wp$", "$wp$")
	})

	t.Run("known-plaintext login (optional)", func(t *testing.T) {
		login := os.Getenv("GRIMOIRE_TEST_WP_LOGIN")
		pw := os.Getenv("GRIMOIRE_TEST_WP_PASSWORD")
		if login == "" || pw == "" {
			t.Skip("set GRIMOIRE_TEST_WP_LOGIN and GRIMOIRE_TEST_WP_PASSWORD for the e2e check")
		}
		u, err := repos.Users.ByLogin(ctx, login)
		if err != nil {
			t.Fatalf("ByLogin(%q): %v", login, err)
		}
		ok, err := password.Verify(pw, u.Pass)
		if err != nil {
			if errors.Is(err, password.ErrUnknownFormat) {
				t.Fatalf("stored hash for %q is an unrecognized format", login)
			}
			t.Fatalf("Verify(%q): %v", login, err)
		}
		if !ok {
			t.Fatalf("known-plaintext verification failed for user %q", login)
		}
		t.Logf("known-plaintext login verified for user %q", login)
	})
}
