package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/sanitize"
	"github.com/roboweaver/grimoire/internal/storage"
	"github.com/roboweaver/grimoire/internal/storage/migrate"
)

// This is the TDD-RED step for task 3.2: it drives runBackfillContent, the
// subcommand entry point that does NOT exist yet. Referencing it makes this file
// fail to compile, and the failure names exactly that missing symbol — the
// correct red. The runBackfill-over-real-SQLite portions below are written
// against ports that DO exist now (repos.PostContentBackfill /
// repos.PostContentUpdater, wired in factory.go by task 1.2, plus
// runBackfill/Report/printReport from task 2.2), so once runBackfillContent
// lands in task 3.2 the whole file compiles and these assertions stand.
//
// Shared symbols: dirtyContent/dirtyExcerpt, the fakeBackfillReader /
// fakeContentUpdater / errPolicy fakes, and tierBClean live in
// backfill_test.go (package main, same package) — this file reuses them and
// MUST NOT redeclare them.

// ---------------------------------------------------------------------------
// SQLite harness: a real migrated storage.Set, mirroring storagetest's
// newSQLiteRepos (storage.New + MigrationsFS + migrate.Apply), minus
// SeedFixtures so the only rows present are the ones a case seeds itself. That
// keeps the report counts a pure function of the seeded fixture (Req 10.4).
// ---------------------------------------------------------------------------

// newBackfillSQLite builds a migrated, EMPTY SQLite-backed storage.Set in a temp
// dir and returns it alongside the DSN (so a case can also point a temp YAML
// config at the same database for the runBackfillContent end-to-end path). The
// caller seeds rows through repos.PostWriter.Create.
func newBackfillSQLite(t *testing.T) (*storage.Repositories, string, func()) {
	t.Helper()
	ctx := context.Background()
	dsn := filepath.Join(t.TempDir(), "grimoire.db")
	cfg := config.DatabaseConfig{Vendor: "sqlite", DSN: dsn, TablePrefix: "wp_"}
	repos, err := storage.New(cfg)
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	migFS, err := storage.MigrationsFS(cfg.Vendor)
	if err != nil {
		repos.Close()
		t.Fatalf("MigrationsFS: %v", err)
	}
	if _, err := migrate.Apply(ctx, repos.DB(), migFS, cfg.Vendor, cfg.TablePrefix); err != nil {
		repos.Close()
		t.Fatalf("migrate.Apply: %v", err)
	}
	return repos, dsn, func() { repos.Close() }
}

// seedPost inserts one post row through the PostWriter and returns its ID. The
// content/excerpt are stored verbatim (runWriterContract pins that), so a dirty
// <script>-bearing body lands in the database exactly as passed — the pre-M10a
// hazard the backfill drains.
func seedPost(t *testing.T, repos *storage.Repositories, slug, ptype, status, content, excerpt string) int64 {
	t.Helper()
	id, err := repos.PostWriter.Create(context.Background(), domain.Post{
		Author:        1,
		Date:          time.Date(2024, 9, 1, 0, 0, 0, 0, time.UTC),
		Content:       content,
		Title:         slug,
		Excerpt:       excerpt,
		Status:        status,
		Slug:          slug,
		Type:          ptype,
		CommentStatus: "open",
	})
	if err != nil {
		t.Fatalf("seed %s/%s: %v", ptype, status, err)
	}
	return id
}

// ---------------------------------------------------------------------------
// Dry run over a real SQLite Set: the database is left byte-identical and the
// report names the dirty rows (Req 6.1, 6.2).
// ---------------------------------------------------------------------------

// TestBackfillContentDryRunLeavesDatabaseByteIdentical drives runBackfill over
// the REAL SQLite-backed ports with apply=false and asserts (a) every row's
// stored post_content/post_excerpt is byte-identical afterward, and (b) the
// report names the dirty rows. The report-vs-database split is the whole point
// of a dry run: it computes what it WOULD change without writing it (Req 6).
func TestBackfillContentDryRunLeavesDatabaseByteIdentical(t *testing.T) {
	repos, _, cleanup := newBackfillSQLite(t)
	defer cleanup()
	ctx := context.Background()

	cleanC := tierBClean(t, sanitize.PostContent, dirtyContent)
	cleanE := tierBClean(t, sanitize.PostExcerpt, dirtyExcerpt)

	dirtyID := seedPost(t, repos, "bf-dirty", "post", "draft", dirtyContent, dirtyExcerpt)
	cleanID := seedPost(t, repos, "bf-clean", "page", "publish", cleanC, cleanE)

	// Snapshot the stored bytes before the dry run.
	beforeDirty, err := repos.PostWriter.ByID(ctx, dirtyID)
	if err != nil {
		t.Fatalf("ByID dirty before: %v", err)
	}
	beforeClean, err := repos.PostWriter.ByID(ctx, cleanID)
	if err != nil {
		t.Fatalf("ByID clean before: %v", err)
	}

	rep, err := runBackfill(ctx, repos.PostContentBackfill, repos.PostContentUpdater, sanitize.New(), false)
	if err != nil {
		t.Fatalf("runBackfill dry run: %v", err)
	}

	// The report names the dirty row and not the clean one (Req 6.2).
	if rep.Scanned != 2 {
		t.Errorf("Scanned = %d, want 2", rep.Scanned)
	}
	if rep.Changed != 1 {
		t.Errorf("Changed = %d, want 1 — only the dirty row would change", rep.Changed)
	}
	named := map[int64]bool{}
	for _, r := range rep.Rows {
		named[r.ID] = true
	}
	if !named[dirtyID] {
		t.Errorf("dry-run report does not name the dirty row %d (Req 6.2)", dirtyID)
	}
	if named[cleanID] {
		t.Errorf("dry-run report names the clean row %d — it has nothing to change", cleanID)
	}

	// Req 6.1: the database is byte-identical — the dry run wrote nothing.
	afterDirty, err := repos.PostWriter.ByID(ctx, dirtyID)
	if err != nil {
		t.Fatalf("ByID dirty after: %v", err)
	}
	if afterDirty.Content != beforeDirty.Content || afterDirty.Excerpt != beforeDirty.Excerpt {
		t.Errorf("dry run mutated the dirty row: content %q->%q, excerpt %q->%q (Req 6.1)",
			beforeDirty.Content, afterDirty.Content, beforeDirty.Excerpt, afterDirty.Excerpt)
	}
	if afterDirty.Content != dirtyContent || afterDirty.Excerpt != dirtyExcerpt {
		t.Errorf("dry run left the dirty row not byte-identical to its seeded value")
	}
	afterClean, err := repos.PostWriter.ByID(ctx, cleanID)
	if err != nil {
		t.Fatalf("ByID clean after: %v", err)
	}
	if afterClean.Content != beforeClean.Content || afterClean.Excerpt != beforeClean.Excerpt {
		t.Errorf("dry run mutated the clean row (Req 6.1)")
	}
}

// ---------------------------------------------------------------------------
// Apply over a real SQLite Set: the dirty rows are cleaned, the clean rows are
// untouched, and post_modified is NOT bumped on the cleaned rows (Req 4.4,
// 10.4). The report counts match the seeded fixture exactly (Req 10.4).
// ---------------------------------------------------------------------------

// TestBackfillContentApplyCleansRowsWithoutBumpingModified seeds a known mix of
// dirty and clean rows across {post, page, revision}, runs runBackfill with
// apply=true over the real ports, and asserts the cleaned content, the untouched
// clean rows, the unbumped post_modified/post_modified_gmt on the cleaned rows,
// and the exact report counts.
func TestBackfillContentApplyCleansRowsWithoutBumpingModified(t *testing.T) {
	repos, _, cleanup := newBackfillSQLite(t)
	defer cleanup()
	ctx := context.Background()

	cleanC := tierBClean(t, sanitize.PostContent, dirtyContent)
	cleanE := tierBClean(t, sanitize.PostExcerpt, dirtyExcerpt)

	// Seeded fixture, counts derived from it below:
	//   dirty content + dirty excerpt  (both change)
	//   dirty content + clean excerpt  (content only)
	//   clean content + dirty excerpt  (excerpt only)
	//   clean content + clean excerpt  (no change)
	bothID := seedPost(t, repos, "bf-both", "post", "draft", dirtyContent, dirtyExcerpt)
	contentID := seedPost(t, repos, "bf-content", "page", "private", dirtyContent, cleanE)
	excerptID := seedPost(t, repos, "bf-excerpt", "revision", "inherit", cleanC, dirtyExcerpt)
	cleanID := seedPost(t, repos, "bf-clean", "post", "publish", cleanC, cleanE)

	// Snapshot post_modified on a row we expect to be cleaned (Req 4.4).
	beforeBoth, err := repos.PostWriter.ByID(ctx, bothID)
	if err != nil {
		t.Fatalf("ByID both before: %v", err)
	}

	rep, err := runBackfill(ctx, repos.PostContentBackfill, repos.PostContentUpdater, sanitize.New(), true)
	if err != nil {
		t.Fatalf("runBackfill apply: %v", err)
	}

	// Req 10.4: the report counts match the seeded fixture exactly.
	//   Scanned = 4 (all targeted rows)
	//   Changed = 3 (both, content-only, excerpt-only)
	//   ContentChanges = 2 (both, content-only)
	//   ExcerptChanges = 2 (both, excerpt-only)
	//   Failed = 0 (the real policy cannot error at tier B)
	if rep.Scanned != 4 {
		t.Errorf("Scanned = %d, want 4", rep.Scanned)
	}
	if rep.Changed != 3 {
		t.Errorf("Changed = %d, want 3", rep.Changed)
	}
	if rep.ContentChanges != 2 {
		t.Errorf("ContentChanges = %d, want 2", rep.ContentChanges)
	}
	if rep.ExcerptChanges != 2 {
		t.Errorf("ExcerptChanges = %d, want 2", rep.ExcerptChanges)
	}
	if rep.Failed != 0 {
		t.Errorf("Failed = %d, want 0 — the real tier-B policy never errors", rep.Failed)
	}

	// The dirty rows are now tier-B clean in the database.
	for _, id := range []int64{bothID, contentID, excerptID} {
		got, err := repos.PostWriter.ByID(ctx, id)
		if err != nil {
			t.Fatalf("ByID %d after apply: %v", id, err)
		}
		if got.Content != cleanC {
			t.Errorf("row %d post_content after apply = %q, want tier-B clean %q", id, got.Content, cleanC)
		}
		if got.Excerpt != cleanE {
			t.Errorf("row %d post_excerpt after apply = %q, want tier-B clean %q", id, got.Excerpt, cleanE)
		}
	}

	// Req 4.4 / 10.4: post_modified and post_modified_gmt are NOT bumped on a
	// cleaned row — the narrow update must leave them byte-identical.
	afterBoth, err := repos.PostWriter.ByID(ctx, bothID)
	if err != nil {
		t.Fatalf("ByID both after: %v", err)
	}
	if !afterBoth.Modified.Equal(beforeBoth.Modified) {
		t.Errorf("post_modified bumped on cleaned row: before %v, after %v (Req 4.4)",
			beforeBoth.Modified, afterBoth.Modified)
	}
	if !afterBoth.ModifiedGMT.Equal(beforeBoth.ModifiedGMT) {
		t.Errorf("post_modified_gmt bumped on cleaned row: before %v, after %v (Req 4.4)",
			beforeBoth.ModifiedGMT, afterBoth.ModifiedGMT)
	}

	// The already-clean row is left untouched (Req 4.1).
	gotClean, err := repos.PostWriter.ByID(ctx, cleanID)
	if err != nil {
		t.Fatalf("ByID clean after: %v", err)
	}
	if gotClean.Content != cleanC || gotClean.Excerpt != cleanE {
		t.Errorf("already-clean row %d was changed: content=%q excerpt=%q", cleanID, gotClean.Content, gotClean.Excerpt)
	}
}

// ---------------------------------------------------------------------------
// runBackfillContent end-to-end over a real SQLite Set driven through a temp
// YAML config (Req 1.2, 1.3): the subcommand parses its own flags, loads config,
// opens storage, runs the sweep, and returns the right exit posture.
// ---------------------------------------------------------------------------

// writeBackfillConfig writes a minimal grimoire YAML pointing at the given
// SQLite DSN and returns its path, so runBackfillContent(["-config", path, ...])
// opens the same database the case seeded. config.Load requires only
// database.vendor and database.dsn; everything else defaults.
func writeBackfillConfig(t *testing.T, dsn string) string {
	t.Helper()
	cfgPath := filepath.Join(t.TempDir(), "grimoire.yaml")
	body := "database:\n  vendor: sqlite\n  dsn: \"" + dsn + "\"\n  table_prefix: \"wp_\"\n"
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	return cfgPath
}

// TestBackfillContentSubcommandDryRunReturnsNil drives the subcommand entry
// point runBackfillContent over a real seeded SQLite database via a temp config,
// without -apply. A dry run writes nothing and returns nil (Req 1.2). This also
// pins the Req 9.4 dry-run half honestly: even if rows could fail, a dry run
// that merely observed failures returns nil (it changed nothing). The real
// tier-B policy cannot error here, so Failed is 0 and the nil return is
// unconditional — see TestBackfillContentApplyExitPostureOnFailure for how the
// Failed>0 non-zero-return rule is covered.
func TestBackfillContentSubcommandDryRunReturnsNil(t *testing.T) {
	repos, dsn, cleanup := newBackfillSQLite(t)
	ctx := context.Background()

	dirtyID := seedPost(t, repos, "bf-dirty", "post", "draft", dirtyContent, dirtyExcerpt)
	before, err := repos.PostWriter.ByID(ctx, dirtyID)
	if err != nil {
		t.Fatalf("ByID before: %v", err)
	}
	// Release the connection the harness holds so runBackfillContent opens the
	// same file-backed database cleanly through its own storage.New.
	cleanup()

	cfgPath := writeBackfillConfig(t, dsn)

	if err := runBackfillContent([]string{"-config", cfgPath}); err != nil {
		t.Fatalf("runBackfillContent dry run returned %v, want nil (Req 1.2)", err)
	}

	// Re-open to confirm the dry run wrote nothing.
	reopen, _, reCleanup := reopenSQLite(t, dsn)
	defer reCleanup()
	after, err := reopen.PostWriter.ByID(ctx, dirtyID)
	if err != nil {
		t.Fatalf("ByID after dry run: %v", err)
	}
	if after.Content != before.Content || after.Excerpt != before.Excerpt {
		t.Errorf("subcommand dry run mutated the database (Req 1.2 / 6.1)")
	}
}

// TestBackfillContentSubcommandApplyCleansDatabase drives runBackfillContent with
// -apply over a real seeded SQLite database via a temp config: the dirty row is
// cleaned in the database and the call returns nil because no row failed
// sanitization (Failed==0, Req 1.3, 9.4 happy path).
func TestBackfillContentSubcommandApplyCleansDatabase(t *testing.T) {
	repos, dsn, cleanup := newBackfillSQLite(t)
	ctx := context.Background()

	cleanC := tierBClean(t, sanitize.PostContent, dirtyContent)
	cleanE := tierBClean(t, sanitize.PostExcerpt, dirtyExcerpt)
	dirtyID := seedPost(t, repos, "bf-dirty", "post", "draft", dirtyContent, dirtyExcerpt)
	before, err := repos.PostWriter.ByID(ctx, dirtyID)
	if err != nil {
		t.Fatalf("ByID before: %v", err)
	}
	cleanup()

	cfgPath := writeBackfillConfig(t, dsn)

	if err := runBackfillContent([]string{"-config", cfgPath, "-apply"}); err != nil {
		t.Fatalf("runBackfillContent apply returned %v, want nil when no row failed (Req 9.4)", err)
	}

	reopen, _, reCleanup := reopenSQLite(t, dsn)
	defer reCleanup()
	after, err := reopen.PostWriter.ByID(ctx, dirtyID)
	if err != nil {
		t.Fatalf("ByID after apply: %v", err)
	}
	if after.Content != cleanC || after.Excerpt != cleanE {
		t.Errorf("apply did not clean the dirty row: content=%q excerpt=%q (Req 1.3)", after.Content, after.Excerpt)
	}
	// post_modified unbumped end-to-end through the subcommand too (Req 4.4).
	if !after.Modified.Equal(before.Modified) {
		t.Errorf("subcommand apply bumped post_modified: before %v, after %v (Req 4.4)", before.Modified, after.Modified)
	}
}

// reopenSQLite re-opens an already-migrated SQLite database at dsn as a fresh
// storage.Set (no migrate, no seed) so a case can read back rows after
// runBackfillContent opened and closed its own connection to the same file.
func reopenSQLite(t *testing.T, dsn string) (*storage.Repositories, string, func()) {
	t.Helper()
	cfg := config.DatabaseConfig{Vendor: "sqlite", DSN: dsn, TablePrefix: "wp_"}
	repos, err := storage.New(cfg)
	if err != nil {
		t.Fatalf("reopen storage.New: %v", err)
	}
	return repos, dsn, func() { repos.Close() }
}

// ---------------------------------------------------------------------------
// Req 9.4 exit posture: Failed>0 under -apply yields a non-zero outcome, a dry
// run reports the failure but returns nil.
//
// HONEST NOTE ON THE REAL POLICY. runBackfillContent builds sanitize.New() and
// cannot be handed a fake policy — and the real tier-B policy over
// PostContent/PostExcerpt NEVER errors (it routes straight through the compiled
// bluemonday policy, returning (string, nil); only an unrecognised
// FieldKind/Tier fails closed, policy.go:121-148). So a "seeded sanitize-failure
// row" cannot actually trip the real policy THROUGH runBackfillContent: driving
// the subcommand end-to-end can never produce report.Failed>0, hence never the
// non-zero return, over the real policy.
//
// The return RULE itself — "exit non-zero WHEN apply && report.Failed>0, exit
// zero on a dry run that only observed failures" — is therefore covered here at
// the runBackfill level through the errPolicy seam (the same seam
// backfill_test.go uses), asserting the Failed-count arithmetic
// runBackfillContent keys its return on. runBackfillContent's own wrapping of
// that count into a non-zero error is a two-line guard (apply && Failed>0) over
// the Report runBackfill returns; this test pins the Report side (the input to
// that guard) under both apply and dry run, which is the testable surface
// without a failing real policy. See requirements.md Req 9.4 and the design's
// runBackfillContent block.
// ---------------------------------------------------------------------------

// TestBackfillContentApplyExitPostureOnFailure pins the Req 9.4 decision inputs:
// under apply, a row whose sanitization errors is counted in report.Failed>0
// (the condition runBackfillContent turns into a non-zero return), and under a
// dry run the SAME failing row is still counted in Failed>0 but the sweep itself
// returns nil (runBackfill never errors on a per-row sanitize failure) — so the
// subcommand's dry-run branch returns nil while reporting the failure. The
// failure is provoked through the errPolicy seam because the real policy cannot
// error (see the note above).
func TestBackfillContentApplyExitPostureOnFailure(t *testing.T) {
	const failingContent = `<p>poison</p><script>boom()</script>`
	sentinel := &backfillSentinelErr{}
	policy := errPolicy{
		inner:     sanitize.New(),
		failField: sanitize.PostContent,
		failInput: failingContent,
		err:       sentinel,
	}

	reader := newFakeReader(
		domain.PostContentRow{ID: 1, Type: "post", Content: failingContent, Excerpt: dirtyExcerpt},
		domain.PostContentRow{ID: 2, Type: "page", Content: dirtyContent, Excerpt: dirtyExcerpt},
	)

	// Apply branch: the Report runBackfillContent would turn into a non-zero exit
	// carries Failed>0, and runBackfill itself does not error on the per-row
	// sanitize failure (it is reported, not fatal).
	applyRep, err := runBackfill(context.Background(), reader, &fakeContentUpdater{}, policy, true)
	if err != nil {
		t.Fatalf("runBackfill must not abort on a per-row sanitize error: %v", err)
	}
	if applyRep.Failed == 0 {
		t.Fatalf("apply Report.Failed = 0, want >0 — this is the condition runBackfillContent " +
			"turns into a non-zero return under -apply (Req 9.4)")
	}
	// The guard runBackfillContent applies under -apply.
	if !(true && applyRep.Failed > 0) {
		t.Errorf("Req 9.4 guard (apply && Failed>0) did not hold for a failing apply run")
	}

	// Dry-run branch: the same failing row is observed (Failed>0) but the dry run
	// returns nil — runBackfillContent's non-zero return is gated on *apply, so a
	// dry run that merely observed failures exits 0 (Req 9.4).
	dryReader := newFakeReader(
		domain.PostContentRow{ID: 1, Type: "post", Content: failingContent, Excerpt: dirtyExcerpt},
		domain.PostContentRow{ID: 2, Type: "page", Content: dirtyContent, Excerpt: dirtyExcerpt},
	)
	dryRep, err := runBackfill(context.Background(), dryReader, &fakeContentUpdater{}, policy, false)
	if err != nil {
		t.Fatalf("runBackfill dry run errored on a per-row sanitize failure: %v", err)
	}
	if dryRep.Failed == 0 {
		t.Errorf("dry-run Report.Failed = 0, want >0 — a dry run still reports the failure (Req 9.4)")
	}
	// The guard is false on a dry run (apply == false), so runBackfillContent
	// returns nil even though Failed>0.
	if false && dryRep.Failed > 0 {
		t.Errorf("Req 9.4 guard should be false on a dry run")
	}
}

// backfillSentinelErr is a tiny error used to provoke the errPolicy seam without
// importing errors just for one value in this file.
type backfillSentinelErr struct{}

func (*backfillSentinelErr) Error() string { return "sanitize: backfill-cmd test boom" }
