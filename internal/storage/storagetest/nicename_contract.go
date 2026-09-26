package storagetest

import (
	"context"
	"errors"
	"testing"

	"github.com/roboweaver/grimoire/internal/domain"
)

// RunNicenameContract covers UserRepository.ByNicename: it resolves a seeded
// user, reports domain.ErrNotFound for an unknown nicename, and resolves a
// duplicated nicename deterministically to the lowest ID (Req 7.2, 7.6, 12.3,
// 12.10). Mirrors the "UserRepository ByLogin, ByID, and not-found" case in
// contract.go, which is the read this one sits beside.
//
// newNicenameRepos MUST build a backend carrying SeedDuplicateNicenames on top
// of SeedFixtures. It is a separate backend from RunContract's because that
// suite asserts an absolute user Count (3, after creating 2), and this one needs
// two more rows.
func RunNicenameContract(t *testing.T, newNicenameRepos NewReposFunc) {
	t.Helper()
	ctx := context.Background()

	t.Run("ByNicename resolves a seeded user", func(t *testing.T) {
		repos, cleanup := newNicenameRepos(t)
		defer cleanup()

		u, err := repos.Users.ByNicename(ctx, UniqueNicename)
		if err != nil {
			t.Fatalf("ByNicename(%q): %v", UniqueNicename, err)
		}
		if u.ID != UniqueNicenameUserID {
			t.Errorf("ByNicename(%q).ID = %d, want %d", UniqueNicename, u.ID,
				UniqueNicenameUserID)
		}
		if u.Nicename != UniqueNicename {
			t.Errorf("ByNicename(%q).Nicename = %q, want %q", UniqueNicename,
				u.Nicename, UniqueNicename)
		}
	})

	t.Run("ByNicename reports ErrNotFound for an unknown nicename", func(t *testing.T) {
		repos, cleanup := newNicenameRepos(t)
		defer cleanup()

		if _, err := repos.Users.ByNicename(ctx, UnknownNicename); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("ByNicename(%q) err = %v, want ErrNotFound", UnknownNicename, err)
		}

		// A login is not a nicename. The fixture rows happen to share both
		// values for the admin user, so the duplicate pair — whose logins differ
		// from their nicename — is what keeps the two columns from being
		// confused.
		if _, err := repos.Users.ByNicename(ctx, DuplicateNicenameLoserLogin); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("ByNicename(%q) err = %v, want ErrNotFound — that value is a user_login, not a user_nicename",
				DuplicateNicenameLoserLogin, err)
		}
	})

	// Req 7.6: duplicates are schema-legal and are not an error. The read picks
	// the lowest ID, which is a deliberate divergence from WordPress's own
	// unordered LIMIT 1.
	t.Run("a duplicated nicename resolves to the lowest ID", func(t *testing.T) {
		repos, cleanup := newNicenameRepos(t)
		defer cleanup()

		// Both rows are really there: without this, "lowest wins" could be
		// satisfied by a seed that inserted only one of them.
		for _, want := range []struct {
			id    int64
			login string
		}{
			{DuplicateNicenameLoserID, DuplicateNicenameLoserLogin},
			{DuplicateNicenameWinnerID, DuplicateNicenameWinnerLogin},
		} {
			row, err := repos.Users.ByID(ctx, want.id)
			if err != nil {
				t.Fatalf("fixture user %d is missing (%v); the duplicate case cannot be verified",
					want.id, err)
			}
			if row.Login != want.login || row.Nicename != DuplicateNicename {
				t.Fatalf("fixture user %d = {login %q, nicename %q}, want {%q, %q}",
					want.id, row.Login, row.Nicename, want.login, DuplicateNicename)
			}
		}

		u, err := repos.Users.ByNicename(ctx, DuplicateNicename)
		if err != nil {
			t.Fatalf("ByNicename(%q): %v — a duplicated nicename is not an error", DuplicateNicename, err)
		}
		if u.ID != DuplicateNicenameWinnerID {
			t.Errorf("ByNicename(%q).ID = %d, want %d — the lowest ID must win, and the higher id was inserted first so insertion order cannot stand in for ORDER BY",
				DuplicateNicename, u.ID, DuplicateNicenameWinnerID)
		}
		if u.Login != DuplicateNicenameWinnerLogin {
			t.Errorf("ByNicename(%q).Login = %q, want %q", DuplicateNicename,
				u.Login, DuplicateNicenameWinnerLogin)
		}

		// Deterministic, not merely correct once: repeated reads on the same
		// backend must agree, since an unordered LIMIT 1 is free to vary.
		for i := 0; i < 3; i++ {
			again, err := repos.Users.ByNicename(ctx, DuplicateNicename)
			if err != nil {
				t.Fatalf("ByNicename(%q) repeat %d: %v", DuplicateNicename, i, err)
			}
			if again.ID != u.ID {
				t.Fatalf("ByNicename(%q) returned %d then %d — resolution must be deterministic",
					DuplicateNicename, u.ID, again.ID)
			}
		}
	})
}
