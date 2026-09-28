package storagetest

import (
	"context"
	"testing"

	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/storage"
)

// RunNicenameAuditorContract covers domain.NicenameAuditor.DuplicateNicenames,
// the operator-only diagnostic "grimoire-cli migrate -check" reports from
// (Req 7.8, 12.10). It asserts the duplicate pair is reported with the right
// Count and the right WinnerID, and that a database with no shared nicename
// reports nothing at all.
//
// Two backends, because the two halves of the guarantee need opposite data:
//
//   - newDuplicateRepos MUST carry SeedDuplicateNicenames on top of
//     SeedFixtures — the same backend RunNicenameContract takes.
//   - newPlainRepos MUST carry SeedFixtures alone, i.e. the backend RunContract
//     uses. Its single user row makes it the natural no-duplicates case: nothing
//     has to be deleted to produce the condition, so "reports nothing" is
//     asserted against a populated database rather than an empty one.
//
// It is a separate runner from RunNicenameContract rather than two more
// parameters on it for two reasons. DuplicateNicenames is a separate interface
// with a separate consumer — the request path holds UserRepository and never
// reaches the auditor — so a separate suite mirrors the split the design makes,
// the way RunTermParentContract and RunArchiveContract are already split per
// read surface. And widening RunNicenameContract's signature would drag its
// three registration sites and its already-green ByNicename cases into this
// task's compile failure, which would bury the one thing that is supposed to be
// failing.
func RunNicenameAuditorContract(t *testing.T, newDuplicateRepos, newPlainRepos NewReposFunc) {
	t.Helper()
	ctx := context.Background()

	t.Run("DuplicateNicenames reports the shared nicename", func(t *testing.T) {
		repos, cleanup := newDuplicateRepos(t)
		defer cleanup()

		// The winner comes from the read itself, not from the fixture constant.
		// A report that named a different ID than ByNicename resolves to would
		// be telling the operator to go look at a row the site never serves, so
		// the two have to be compared against each other rather than each
		// against the seed.
		want, err := repos.Users.ByNicename(ctx, DuplicateNicename)
		if err != nil {
			t.Fatalf("ByNicename(%q): %v", DuplicateNicename, err)
		}

		got, err := nicenameAuditor(t, repos).DuplicateNicenames(ctx)
		if err != nil {
			t.Fatalf("DuplicateNicenames: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("DuplicateNicenames returned %d conflicts (%+v), want exactly 1 — only %q is shared; SeedFixtures' own %q user appears once and must not be reported",
				len(got), got, DuplicateNicename, UniqueNicename)
		}

		c := got[0]
		if c.Nicename != DuplicateNicename {
			t.Errorf("conflict.Nicename = %q, want %q", c.Nicename, DuplicateNicename)
		}
		if c.Count != 2 {
			t.Errorf("conflict.Count = %d, want 2 — two rows share %q", c.Count, DuplicateNicename)
		}
		if c.WinnerID != want.ID {
			t.Errorf("conflict.WinnerID = %d, but ByNicename(%q) resolves to %d — the report must name the row the read actually picks, not the other one",
				c.WinnerID, DuplicateNicename, want.ID)
		}
		// Belt and braces on the fixture's own claim: the winner is the lower
		// of the two seeded ids, so a report agreeing with a *wrong* ByNicename
		// would still be caught here.
		if c.WinnerID != DuplicateNicenameWinnerID {
			t.Errorf("conflict.WinnerID = %d, want %d (the lower of %d and %d)",
				c.WinnerID, DuplicateNicenameWinnerID, DuplicateNicenameWinnerID,
				DuplicateNicenameLoserID)
		}
	})

	t.Run("DuplicateNicenames reports nothing when no nicename is shared", func(t *testing.T) {
		repos, cleanup := newPlainRepos(t)
		defer cleanup()

		// The users table is populated — otherwise an empty report would prove
		// nothing about HAVING COUNT(*) > 1.
		if _, err := repos.Users.ByNicename(ctx, UniqueNicename); err != nil {
			t.Fatalf("ByNicename(%q): %v — this backend must carry SeedFixtures, or the empty report below is vacuous",
				UniqueNicename, err)
		}

		got, err := nicenameAuditor(t, repos).DuplicateNicenames(ctx)
		if err != nil {
			t.Fatalf("DuplicateNicenames: %v — no duplicates is the common case, not an error", err)
		}
		if len(got) != 0 {
			t.Errorf("DuplicateNicenames returned %+v, want none — every nicename in this backend is unique",
				got)
		}
	})
}

// nicenameAuditor reaches domain.NicenameAuditor through the same concrete
// *wprepo.UserRepo that backs repos.Users. The auditor is deliberately its own
// narrow interface rather than a method on domain.UserRepository, so
// storage.Set exposes no field for it and the assertion has to be made here.
func nicenameAuditor(t *testing.T, repos *storage.Repositories) domain.NicenameAuditor {
	t.Helper()
	a, ok := repos.Users.(domain.NicenameAuditor)
	if !ok {
		t.Fatalf("repos.Users (%T) does not implement domain.NicenameAuditor — the same concrete user repo must satisfy both interfaces, since the split exists to keep the method off UserRepository, not to add a second type",
			repos.Users)
	}
	return a
}
