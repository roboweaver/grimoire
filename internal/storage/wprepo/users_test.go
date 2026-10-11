package wprepo

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/storage/migrate"
	sqlitemig "github.com/roboweaver/grimoire/internal/storage/migrations/sqlite"
	sqliteadapter "github.com/roboweaver/grimoire/internal/storage/sqlite"
)

// newUserRepo builds a migrated SQLite-backed *UserRepo for the user write
// contract tests. SQLite always runs — there is no GRIMOIRE_TEST_* gate — and
// the backend is a fresh temp-file database per test, migrated with the same
// greenfield set the production path and the storagetest SQLite runner use, so
// the {prefix}users row shape is identical. The repo is constructed directly
// via NewUserRepo rather than through storage.New: this test lives in package
// wprepo, which storage imports, so importing storage here would be an import
// cycle. Opening the driver + dialect and applying the embedded migrations
// reproduces exactly the slice of that setup these tests need.
func newUserRepo(t *testing.T) *UserRepo {
	t.Helper()
	ctx := context.Background()
	const prefix = "wp_"
	cfg := config.DatabaseConfig{
		Vendor:      "sqlite",
		DSN:         filepath.Join(t.TempDir(), "grimoire.db"),
		TablePrefix: prefix,
	}
	db, err := sqliteadapter.Open(cfg)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := migrate.Apply(ctx, db, sqlitemig.FS(), cfg.Vendor, prefix); err != nil {
		t.Fatalf("migrate.Apply: %v", err)
	}
	return NewUserRepo(sqliteadapter.NewBunDB(db), prefix)
}

// seedUser inserts a user through the repo's own Create path and returns its
// generated ID, so UpdateProfile/ByEmail are asserted against a row written the
// way the production write path writes it (Finding 1 round-trip).
func seedUser(t *testing.T, repo *UserRepo, u domain.User) int64 {
	t.Helper()
	id, err := repo.Create(context.Background(), u)
	if err != nil {
		t.Fatalf("seed Create(%q): %v", u.Login, err)
	}
	return id
}

func strptr(s string) *string { return &s }

// TestUserRepoUpdateProfileFullWrite covers the all-fields-supplied case: every
// one of the four UserProfileFields pointers is non-nil, so UpdateProfile must
// write display_name, user_email, user_url, and user_nicename onto the user's
// {prefix}users row, observable on a subsequent ByID read (Req 3.1).
//
// user_nicename is deliberately exercised because it is the column behind the
// REST "slug" field this milestone updates (Req 7.3).
func TestUserRepoUpdateProfileFullWrite(t *testing.T) {
	ctx := context.Background()
	repo := newUserRepo(t)
	id := seedUser(t, repo, domain.User{
		Login:       "alice",
		Nicename:    "alice",
		DisplayName: "Alice Original",
		Pass:        "$2a$10$originalhashoriginalhashorigi",
		Email:       "alice.old@example.com",
		URL:         "https://alice-old.example.com",
		Registered:  time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
	})

	err := repo.UpdateProfile(ctx, id, domain.UserProfileFields{
		DisplayName: strptr("Alice Updated"),
		Email:       strptr("alice.new@example.com"),
		URL:         strptr("https://alice-new.example.com"),
		Nicename:    strptr("alice-new"),
	})
	if err != nil {
		t.Fatalf("UpdateProfile: %v", err)
	}

	got, err := repo.ByID(ctx, id)
	if err != nil {
		t.Fatalf("ByID after update: %v", err)
	}
	if got.DisplayName != "Alice Updated" {
		t.Errorf("display_name = %q, want %q", got.DisplayName, "Alice Updated")
	}
	if got.Email != "alice.new@example.com" {
		t.Errorf("user_email = %q, want %q", got.Email, "alice.new@example.com")
	}
	if got.URL != "https://alice-new.example.com" {
		t.Errorf("user_url = %q, want %q", got.URL, "https://alice-new.example.com")
	}
	if got.Nicename != "alice-new" {
		t.Errorf("user_nicename (slug) = %q, want %q", got.Nicename, "alice-new")
	}
	// A profile update must never touch the password hash — that is UpdatePass's
	// job — so the stored hash is unchanged.
	if got.Pass != "$2a$10$originalhashoriginalhashorigi" {
		t.Errorf("user_pass changed by UpdateProfile: got %q, want it untouched", got.Pass)
	}
}

// TestUserRepoUpdateProfilePartialLeavesOmittedColumns covers Req 7.2: only the
// columns whose pointer is non-nil are written, and an omitted field leaves the
// stored column at exactly its prior value. It snapshots the row before the
// one-field update and compares the three omitted columns after.
func TestUserRepoUpdateProfilePartialLeavesOmittedColumns(t *testing.T) {
	ctx := context.Background()
	repo := newUserRepo(t)
	id := seedUser(t, repo, domain.User{
		Login:       "bob",
		Nicename:    "bob",
		DisplayName: "Bob Before",
		Pass:        "$2a$10$bobhashbobhashbobhashbobhash",
		Email:       "bob@example.com",
		URL:         "https://bob.example.com",
		Registered:  time.Date(2024, 2, 2, 0, 0, 0, 0, time.UTC),
	})

	before, err := repo.ByID(ctx, id)
	if err != nil {
		t.Fatalf("ByID snapshot before: %v", err)
	}

	// Supply only DisplayName; the other three pointers stay nil.
	if err := repo.UpdateProfile(ctx, id, domain.UserProfileFields{
		DisplayName: strptr("Bob After"),
	}); err != nil {
		t.Fatalf("UpdateProfile partial: %v", err)
	}

	after, err := repo.ByID(ctx, id)
	if err != nil {
		t.Fatalf("ByID after partial update: %v", err)
	}
	if after.DisplayName != "Bob After" {
		t.Errorf("display_name = %q, want %q (the one supplied field must change)", after.DisplayName, "Bob After")
	}
	// The three omitted columns must each retain their prior stored value.
	if after.Email != before.Email {
		t.Errorf("user_email changed by a display_name-only update: got %q, want %q", after.Email, before.Email)
	}
	if after.URL != before.URL {
		t.Errorf("user_url changed by a display_name-only update: got %q, want %q", after.URL, before.URL)
	}
	if after.Nicename != before.Nicename {
		t.Errorf("user_nicename changed by a display_name-only update: got %q, want %q", after.Nicename, before.Nicename)
	}
}

// TestUserRepoUpdateProfileUnknownID covers Req 3.3: an update for an ID with no
// matching row returns domain.ErrNotFound, matching the existing UpdatePass
// contract.
func TestUserRepoUpdateProfileUnknownID(t *testing.T) {
	ctx := context.Background()
	repo := newUserRepo(t)

	err := repo.UpdateProfile(ctx, 999999, domain.UserProfileFields{
		DisplayName: strptr("Nobody"),
	})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("UpdateProfile(unknown id) err = %v, want domain.ErrNotFound", err)
	}
}

// TestUserRepoByEmailHitAndMiss covers Finding 1: ByEmail returns the stored row
// for a known user_email and domain.ErrNotFound when no user carries that email.
// The hit round-trips against a row that UpdateProfile wrote, so the write and
// the new read are proven against the same backing row (not just Create's).
func TestUserRepoByEmailHitAndMiss(t *testing.T) {
	ctx := context.Background()
	repo := newUserRepo(t)
	id := seedUser(t, repo, domain.User{
		Login:       "carol",
		Nicename:    "carol",
		DisplayName: "Carol",
		Pass:        "$2a$10$carolhashcarolhashcarolhashc",
		Email:       "carol.initial@example.com",
		URL:         "https://carol.example.com",
		Registered:  time.Date(2024, 3, 3, 0, 0, 0, 0, time.UTC),
	})

	// Move the email with UpdateProfile, then look it up by the new value: the
	// lookup must resolve the row the write produced.
	const newEmail = "carol.moved@example.com"
	if err := repo.UpdateProfile(ctx, id, domain.UserProfileFields{
		Email: strptr(newEmail),
	}); err != nil {
		t.Fatalf("UpdateProfile email: %v", err)
	}

	got, err := repo.ByEmail(ctx, newEmail)
	if err != nil {
		t.Fatalf("ByEmail(%q): %v", newEmail, err)
	}
	if got.ID != id {
		t.Errorf("ByEmail(%q).ID = %d, want %d", newEmail, got.ID, id)
	}
	if got.Email != newEmail {
		t.Errorf("ByEmail(%q).Email = %q, want %q", newEmail, got.Email, newEmail)
	}
	if got.Login != "carol" {
		t.Errorf("ByEmail(%q).Login = %q, want %q", newEmail, got.Login, "carol")
	}

	// The pre-update email no longer belongs to any row, so it must now miss.
	if _, err := repo.ByEmail(ctx, "carol.initial@example.com"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("ByEmail(moved-away email) err = %v, want domain.ErrNotFound", err)
	}

	// An email that never existed must miss as well.
	if _, err := repo.ByEmail(ctx, "ghost@example.com"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("ByEmail(absent) err = %v, want domain.ErrNotFound", err)
	}
}
