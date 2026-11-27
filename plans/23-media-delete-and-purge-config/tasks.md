# Implementation Plan: M23 — Media delete file cleanup, theme-mod cleanup, and a configurable purge cadence

## Overview

> **This milestone closes three open gaps:** the automatic trash purge's hourly cadence becomes
> configurable, deleting an attachment also removes its registered image sizes, original image,
> backup sizes and sideload companions from disk, and deleting the attachment used as a site's
> logo, header image, background image, or site icon also clears that setting. No schema change, no
> migration, no new dependency. One additive storage port (`domain.ThemeModCleaner`).
>
> **Three pull requests, each a complete increment, each with its own wiring test and final gate.**
>
> | PR | Phases | Increment |
> |---|---|---|
> | 1 (purge cadence config) | 1-3 | `scheduler.trash_purge_interval_seconds` config key, `Scheduler.purgeInterval` field, `buildScheduler` wiring, docs, gate |
> | 2 (media file cleanup) | 4-7 | `PostMetaRepository.BackupSizes`, `mediaFileCandidates`/`filterSharedThumb`/`deleteMediaFiles`, `MediaWriteService.Delete` extension, cross-vendor verification, docs, gate |
> | 3 (theme-mod cleanup) | 8-10 | `domain.ThemeModCleaner`, `wprepo.OptionRepo` implementation, `MediaWriteService.Delete` extension, cross-vendor verification, docs, gate |
>
> PR 2 branches from `main` after PR 1 merges (no dependency, just to avoid a config-only PR
> blocking on storage work); PR 3 branches from `main` after PR 2 merges, because both edit
> `MediaWriteService.Delete` and a second concurrent edit to the same method is best avoided.
>
> **No behavior change requiring a migration note.** PR 1 changes only the purge's internal
> cadence (same eventual behavior, default unchanged at one hour); PR 2 and PR 3 make `DELETE`
> remove more things than it did before, strictly a superset of today's cleanup, with no response
> shape change (confirmed by `TestBuiltinGolden` staying green with no `-update`).
>
> **Strict-TDD spine.** Every behavior-adding implementation task is preceded by the task that
> writes its failing test, run and seen to fail **for the stated reason** before the implementation
> starts. A test that fails only because a new symbol does not compile counts as RED.
> Documentation, verification and gate tasks have no RED step and say so.
>
> **Storage handling.** The new `ThemeModCleaner` contract (PR 3) and the `BackupSizes` contract
> addition (PR 2) take `storagetest.NewReposWithPrefixFunc`, read the vendor and prefix they are
> given, and hold no literal prefix, vendor name or placeholder style. Both PRs run the whole
> storage suite against throwaway MySQL 8.0 and PostgreSQL 16 containers in their implementation
> task and again in their final gate (`design.md` "Testing Strategy" has the exact commands).
>
> **Standing gate convention.** After every phase: `gofmt -l .` empty, and `go vet ./...`,
> `go build ./...`, `go test ./...` green. `TestBuiltinGolden` runs in every gate; a failure there is
> a stop, not an `-update` — none of this milestone's changes alters a golden-exercised response
> shape.
>
> **Property tests** use `testing/quick` with a seeded generator, as `internal/sanitize` and M22's
> property tests do. They sit inside the task that owns the behavior, not as stand-alone tasks.

## Tasks

## Phase 1 — Purge cadence config (`internal/config`, TDD) — PR 1

- [x] 1.1 Write the failing config tests first. In `internal/config/config_test.go` (or a sibling
      `scheduler_trash_purge_test.go` following the existing `TestLoadTrashDays*` naming and helper
      shape): default is `3600` when the key is absent; an explicit file value (`scheduler:\n
      trash_purge_interval_seconds: 120\n`) is honored; `GRIMOIRE_SCHEDULER_TRASH_PURGE_INTERVAL_SECONDS`
      overrides the file when it parses as an integer; an unparsable env value is ignored (file
      value stands); a file value of `0`, a negative integer, `7.5`, or a quoted `"30"` fails `Load`
      with an error naming `scheduler.trash_purge_interval_seconds`; an env value that parses but is
      `<= 0` also fails naming the same key, checked after the override is applied.
      Files: `internal/config/config_test.go`
      Verify: `go test ./internal/config -run TrashPurgeInterval` — fails (compile:
      `TrashPurgeIntervalSeconds`, `EffectiveTrashPurgeInterval` do not exist on `SchedulerConfig`).
      _(Req 7.1-7.5)_

- [x] 1.2 Implement the config key. In `internal/config/config.go`: add
      `TrashPurgeIntervalSeconds *int` to `SchedulerConfig` (tag `yaml:"trash_purge_interval_seconds"`),
      `DefaultTrashPurgeIntervalSeconds = 3600`, and `EffectiveTrashPurgeInterval() time.Duration`
      exactly as `design.md` "Components and Interfaces" (PR 1) specifies. In `applyEnv`, add the
      `GRIMOIRE_SCHEDULER_TRASH_PURGE_INTERVAL_SECONDS` branch (same shape as the existing
      `GRIMOIRE_CONTENT_TRASH_DAYS` branch). In `Validate`, add the `<= 0` rejection for a non-nil
      `TrashPurgeIntervalSeconds`, naming the key in the error.
      Files: `internal/config/config.go`
      Verify: `go test ./internal/config -run TrashPurgeInterval` — 1.1 passes; `go build ./...`
      green. _(Req 7.1-7.5)_

## Phase 2 — Scheduler and wiring (`internal/scheduler`, `cmd/grimoire`, TDD) — PR 1

- [x] 2.1 Write the failing scheduler test first. In `internal/scheduler/purge_test.go`, add a case
      (reusing the existing fake-clock harness `withClock`/`fakeClock` already used by the file's
      other tests): construct a `Scheduler` with `WithTrashPurge(l, d, days, types, interval)` where
      `interval` is a short, non-default duration (e.g. 5 minutes); drive the fake clock through a
      sequence of ticks spaced less than, then more than, the configured interval apart; assert a
      purge scan runs on the first tick (no prior scan), is skipped on a tick before the configured
      interval has elapsed, and runs again once the configured interval has elapsed — using the
      configured 5-minute interval, not the old fixed one-hour constant, so the test fails today.
      Also add **Property 5** (`TestPropertyTrashPurgeRespectsInterval`, `testing/quick`, seeded,
      `MaxCount: 100`, tag `// Feature: media-delete-and-purge-config, Property 5: The trash purge
      scan respects the configured interval`): for any generated positive interval and any
      generated strictly increasing tick-timestamp sequence, a scan runs on a tick if and only if no
      prior scan ran or the elapsed time since the last scan is at least the configured interval.
      Files: `internal/scheduler/purge_test.go`
      Verify: `go test ./internal/scheduler -run 'TrashPurge|PropertyTrashPurgeRespectsInterval'` —
      fails to compile (`WithTrashPurge` takes 4 args, not 5).
      _(Req 8.1-8.4, 26.1, 26.2)_

- [x] 2.2 Implement the configurable purge interval. In `internal/scheduler/scheduler.go`: remove
      the `purgeInterval = time.Hour` constant; add a `purgeInterval time.Duration` field to
      `Scheduler`; change `WithTrashPurge`'s signature to accept `interval time.Duration` and set
      `s.purgeInterval = interval`; in `New`, default `purgeInterval` to
      `config.DefaultTrashPurgeIntervalSeconds` seconds before options run (import only the
      constant's value, 3600, as a local literal to avoid an import cycle with `internal/config`
      rather than importing the config package into `internal/scheduler`); change `purgeTrash`'s
      `now.Sub(s.lastPurge) < purgeInterval` check to use `s.purgeInterval`. Update every existing
      call site of `WithTrashPurge` in `internal/scheduler/*_test.go` to pass an explicit interval
      (the prior fixed-hour tests pass `time.Hour` to keep their existing assertions intact).
      Files: `internal/scheduler/scheduler.go`, `internal/scheduler/purge_test.go`
      Verify: `go test ./internal/scheduler` — 2.1 passes, every pre-existing scheduler test still
      passes with its interval argument supplied; `go build ./...` green. _(Req 8.1-8.4)_

- [x] 2.3 Write the failing assembly wiring test first. In `cmd/grimoire`, add a test (new file
      `assembly_trash_purge_interval_test.go` or alongside the existing `assembly_trash_purge_test.go`)
      that builds the scheduler through `buildScheduler` with a config carrying a non-default
      `scheduler.trash_purge_interval_seconds` (e.g. 60), drives it through a fixed-clock `tick`
      sequence, and asserts the purge scan follows the configured 60-second cadence rather than one
      hour.
      Files: `cmd/grimoire/assembly_trash_purge_interval_test.go`
      Verify: `go test ./cmd/grimoire -run TrashPurgeInterval` — fails: `buildScheduler` still passes
      the fixed one-hour behavior (compile error once `WithTrashPurge`'s new signature lands in
      2.2, until this call site is updated).
      _(Req 9.1)_

- [x] 2.4 Wire the configured interval into the real binary. In `cmd/grimoire/assembly.go`, change
      the `scheduler.WithTrashPurge(...)` call in `buildScheduler` to pass
      `deps.Config.Scheduler.EffectiveTrashPurgeInterval()` as the new final argument.
      Files: `cmd/grimoire/assembly.go`
      Verify: `go test ./cmd/grimoire` — 2.3 passes. **Proof of wiring, one removal, recorded in the
      PR description:** revert the new argument to a literal `time.Hour`: `TrashPurgeInterval`
      fails (the 60-second cadence test observes hourly behavior instead); restore. _(Req 9.2)_

- [x] 2.5 Final documentation for PR 1. `docs/compatibility.md`: in the M22 PR 3 entry's "Known
      gaps" bullet "The purge cadence (hourly) and batch size (100) are not configurable.", change
      it to say the cadence is now configurable via `scheduler.trash_purge_interval_seconds`
      (default 3600, override `GRIMOIRE_SCHEDULER_TRASH_PURGE_INTERVAL_SECONDS`) and the batch size
      (100) remains fixed; add a short new bullet under that entry (or a new `M23` entry, matching
      the file's per-milestone-entry convention) naming the key, default, and override variable.
      Each of `configs/grimoire.sqlite.yaml`, `configs/grimoire.mysql.yaml`,
      `configs/grimoire.postgres.yaml`, `configs/grimoire.podman.yaml`: add a commented
      `scheduler.trash_purge_interval_seconds` example near the existing `scheduler.interval_seconds`
      block, explaining the default and override variable, matching the style of the existing
      `content.trash_days` comment block in the same files. `plans/post-m10-gap-analysis/README.md`
      and `plans/README.md` row 22: remove "the purge cadence ... not configurable" from the open-gap
      list and note it closed in M23. No RED step (documentation).
      Files: `docs/compatibility.md`, `configs/grimoire.sqlite.yaml`, `configs/grimoire.mysql.yaml`,
      `configs/grimoire.postgres.yaml`, `configs/grimoire.podman.yaml`,
      `plans/post-m10-gap-analysis/README.md`, `plans/README.md`
      Verify: `grep -rn "cadence.*not configurable\|fixed constant, not configurable" docs plans`
      finds nothing stale. _(Req 10.1-10.3)_

- [x] 2.6 Final gate for PR 1. Confirm: no new file under `internal/storage/migrations`; `git diff
      main -- internal/domain/repository.go` is empty (no new port); `go.mod`/`go.sum` unchanged; no
      new `GRIMOIRE_TEST_*` variable (only a new, ordinary `GRIMOIRE_SCHEDULER_*` env override). Run
      `gofmt -l .` (empty), `go vet ./...`, `go build ./...`, `go test ./...`, `go test -count=2
      ./...`, `go test -race ./internal/config ./internal/scheduler ./cmd/grimoire`. No storage
      change in this PR, so no cross-vendor run is required. Ensure all tests pass, ask the user if
      questions arise.
      Files: (verification only)
      Verify: all gate commands green; the git checks show no violation. _(Req 26.3; sign-off 1, 2)_

## Phase 3 — Checkpoint

- [~] 3. Checkpoint — Ensure all tests pass, ask the user if questions arise.

## Phase 4 — Backup-sizes read and pure candidate logic (`internal/domain`, `internal/storage/wprepo`, `internal/content`, TDD) — PR 2

- [x] 4.1 Write the failing tests first. (a) `internal/storage/storagetest/postmeta_contract.go`:
      extend `runPostMetaContract` with a case seeding `_wp_attachment_backup_sizes` on an
      attachment and asserting `PostMetaRepository.BackupSizes` returns the raw value, and a case
      asserting `""` when unset — same shape as the existing `AttachmentMetadata` cases in the same
      function. (b) `internal/content/mediawriteservice_test.go` (or a new
      `mediafiles_test.go` in the same package): examples for `mediaFileCandidates` (empty/absent
      metadata and backup values give no candidates; a `sizes` map with two entries gives both
      files; `original_image`, `source_image`, `animated_video`, `animated_video_poster` each
      contribute their named file when present and non-empty; a malformed/undecodable metadata
      string gives no candidates, not an error; `backup_sizes` entries contribute their named
      files) and **Property 2** (`TestPropertyMediaFileCandidates`, `testing/quick`, seeded,
      `MaxCount: 100`, tag `// Feature: media-delete-and-purge-config, Property 2: Media file
      candidates match WordPress's own set`): for any generated metadata/backup payload shape
      (including a `thumb` that may or may not also appear in a generated "other attachments'
      metadata" set passed to `filterSharedThumb`), the returned candidate set equals exactly the
      set WordPress's `wp_delete_attachment_files` would resolve for the same payload.
      Files: `internal/storage/storagetest/postmeta_contract.go`,
      `internal/content/mediafiles_test.go`
      Verify: `go test ./internal/storage/storagetest ./internal/content -run
      'BackupSizes|MediaFileCandidates|PropertyMediaFileCandidates'` — fails to compile
      (`BackupSizes`, `mediaFileCandidates`, `filterSharedThumb` do not exist).
      _(Req 2.1-2.8, 6.2, 26.1, 26.2)_

- [x] 4.2 Implement `BackupSizes` and the pure candidate functions. In `internal/domain/repository.go`,
      add `BackupSizes(ctx context.Context, postID int64) (string, error)` to the
      `PostMetaRepository` interface (no new port; Requirement 6.1). In
      `internal/storage/wprepo/postmeta.go`, implement it with the existing private `get` helper,
      same shape as `AttachmentMetadata`. In a new `internal/content/mediafiles.go`: implement
      `mediaFileCandidates(metaRaw, backupRaw string) []string` and `filterSharedThumb(ctx,
      meta domain.PostMetaRepository-shaped-or-narrower, id int64, candidates []string) []string`
      exactly as `design.md` "Components and Interfaces" (PR 2) specifies, reusing `php.Unserialize`
      the same way `content.sizesFromMetadata` does.
      Files: `internal/domain/repository.go`, `internal/storage/wprepo/postmeta.go`,
      `internal/content/mediafiles.go`
      Verify: `go test ./internal/storage/storagetest ./internal/content` — 4.1 passes; `go build
      ./...` green. _(Req 2.1-2.8, 6.1, 6.2)_

## Phase 5 — Uploads-dir containment and best-effort deletion (`internal/content`, TDD) — PR 2

- [x] 5.1 Write the failing tests first. In `internal/content/mediafiles_test.go`: unit examples
      over a `t.TempDir()` tree (a plain relative candidate deletes; a candidate containing `..`
      that would escape is skipped and the file is untouched; an absolute-path candidate pointing
      outside the tree is skipped; a candidate reached only through a symlink planted inside the
      tree that points outside it is skipped; a missing candidate is not an error; a candidate that
      fails to unlink for a non-missing reason — simulated via a narrow deletion function seam, not
      a real permission change — does not stop the remaining candidates from being attempted).
      **Property 3** (`TestPropertyFileDeletionStaysInUploadsDir`, `testing/quick`, seeded,
      `MaxCount: 100`, tag `// Feature: media-delete-and-purge-config, Property 3: File deletion
      never escapes the uploads directory`): for any generated candidate path shape (plain relative,
      `..`-containing, absolute, symlink-indirected) against a real temporary uploads tree, an
      unlink is attempted if and only if the resolved path is contained in the resolved uploads
      directory. **Property 4** (`TestPropertyFileDeletionFailuresAreNonFatal`, `testing/quick`,
      seeded, `MaxCount: 100`, tag `// Feature: media-delete-and-purge-config, Property 4: File
      deletion failures never change the delete outcome`): for any generated set of candidates each
      independently marked missing/failing/succeeding in a fake deletion function, every candidate
      is attempted exactly once and the caller observes no error attributable to any one candidate.
      Files: `internal/content/mediafiles_test.go`
      Verify: `go test ./internal/content -run
      'FileDeletionStaysInUploadsDir|FileDeletionFailuresAreNonFatal|PropertyFileDeletion'` — fails
      to compile (`deleteMediaFiles` does not exist).
      _(Req 3.1-3.3, 4.2-4.4, 26.1, 26.2)_

- [x] 5.2 Implement `deleteMediaFiles`. In `internal/content/mediafiles.go`, add
      `deleteMediaFiles(cfg MediaConfig, mainFile string, candidates []string, log *slog.Logger)`,
      reusing the exact real-path containment check `internal/web/uploads.go`'s
      `resolveWithinRoot`/`evalSymlinksLenient` already implements (export those two functions from
      `internal/web` for reuse, or lift them into a small shared internal helper package imported by
      both `internal/web` and `internal/content` — choose whichever avoids `internal/content`
      importing `internal/web`, since `internal/web` already imports `internal/content`). Each
      candidate (plus `mainFile`) is resolved against `cfg.UploadsDir`; an out-of-bounds candidate is
      skipped without error; `os.Remove` failures are swallowed per Requirement 4.2-4.3; a `nil`
      logger is valid (no logging).
      Files: `internal/content/mediafiles.go`, `internal/web/uploads.go` (if functions are exported
      for reuse)
      Verify: `go test ./internal/content ./internal/web` — 5.1 passes; `go build ./...` green.
      _(Req 3.1-3.3, 4.2-4.4)_

## Phase 6 — Wire file cleanup into `MediaWriteService.Delete` (`internal/content`, `cmd/grimoire`, TDD) — PR 2

- [x] 6.1 Write the failing tests first. In `internal/content/mediawriteservice_test.go`: the full
      Requirement 5.1 fixture matrix against a `t.TempDir()`-backed `MediaConfig.UploadsDir` wired
      through a fake `domain.PostMetaRepository` and a fake `domain.MediaRepository`/`MediaWriter`:
      an attachment with no metadata (only the main file is removed); an attachment with `sizes`,
      `original_image`, `thumb`, `source_image`, `animated_video` and `animated_video_poster`
      entries (every named file is removed); a `thumb` file shared with another attachment's
      metadata (the shared file survives); a `BackupSizes` value (its files are removed); a metadata
      value naming a file that does not exist on disk (no error, `Delete` still returns the
      pre-delete snapshot); a metadata value naming a path that escapes the uploads directory via
      `..` or an absolute path (skipped, every other file for the same attachment is still removed);
      and the Requirement 5.2 case (the storage delete itself fails for a not-found id: no file is
      deleted, `Delete` returns the storage error unchanged, and `s.meta`/`s.media.Config()` are
      never consulted for deletion).
      Files: `internal/content/mediawriteservice_test.go`
      Verify: `go test ./internal/content -run TestMediaWriteServiceDelete` — fails: `Delete` still
      removes only the main file (no size/backup/thumb/source/animated files are removed; test
      assertions on their absence fail).
      _(Req 2.1-2.8, 3.1-3.3, 4.1-4.4, 5.1, 5.2, 26.1, 26.2)_

- [x] 6.2 Extend `MediaWriteService.Delete`. In `internal/content/mediawriteservice.go`: add an
      optional `meta domain.PostMetaRepository` field and a `log *slog.Logger` field to
      `MediaWriteService`, with `WithMediaMetaReader(r domain.PostMetaRepository)` and
      `WithMediaLogger(l *slog.Logger)` `MediaWriteOption`s (both nil-safe: `Delete` falls back to
      main-file-only cleanup with no logging when either is left unwired, matching today's
      behavior exactly). Reorder `Delete` to read `Attachment_Metadata`/`BackupSizes` and compute
      candidates (via `mediaFileCandidates`/`filterSharedThumb`) **before** calling `s.writer.Delete`
      (the metadata rows are removed by that transaction), then, after a successful
      `s.writer.Delete`, call `deleteMediaFiles` for the main file plus every candidate exactly as
      `design.md`'s `Delete` listing specifies.
      Files: `internal/content/mediawriteservice.go`
      Verify: `go test ./internal/content` — 6.1 passes; `go build ./...` green. _(Req 2.1-2.8,
      3.1-3.3, 4.1-4.4)_

- [x] 6.3 Write the failing assembly wiring test first. In `cmd/grimoire`, add a test (e.g.
      `assembly_media_file_cleanup_test.go`) that, through `buildHandler`, creates an attachment with
      seeded `_wp_attachment_metadata` naming a real size-variant file written into the test's
      uploads temp dir, issues a REST `DELETE ?force=true` (or the admin API delete), and asserts
      the size-variant file is gone from disk afterward.
      Files: `cmd/grimoire/assembly_media_file_cleanup_test.go`
      Verify: `go test ./cmd/grimoire -run MediaFileCleanup` — fails: the size-variant file is still
      present (the `WithMediaMetaReader` wiring is not yet added to `cmd/grimoire/assembly.go`).
      _(Req 9.1-equivalent wiring proof for PR 2)_

- [x] 6.4 Wire the metadata reader into the real binary. In `cmd/grimoire/assembly.go`, pass
      `content.WithMediaMetaReader(repos.PostMeta)` to the existing
      `content.NewMediaWriteService(...)` call.
      Files: `cmd/grimoire/assembly.go`
      Verify: `go test ./cmd/grimoire` — 6.3 passes. **Proof of wiring, one removal, recorded in the
      PR description:** remove `WithMediaMetaReader(repos.PostMeta)`: `MediaFileCleanup` fails (the
      size-variant file is never removed); restore.

- [x] 6.5 Cross-vendor verification for PR 2, before the final gate. Run the whole storage suite
      against throwaway MySQL 8.0 and PostgreSQL 16 containers (the `design.md` "Testing Strategy"
      command block for PR 2), confirming the new `BackupSizes` contract cases pass under both
      vendors, not skipped, alongside every pre-existing contract. Record the commands and output in
      the PR description; remove the containers afterward. No RED step (verification).
      Files: (verification only)
      Verify: `go test ./internal/storage/... -count=1 -v` with both `GRIMOIRE_TEST_MYSQL_DSN` and
      `GRIMOIRE_TEST_POSTGRES_DSN` set shows the `BackupSizes` subtests passing under `mysql` and
      `postgres`. _(Req 26.6-equivalent cross-vendor proof)_

## Phase 7 — Documentation and final gate for PR 2

- [x] 7.1 Final documentation for PR 2. `docs/compatibility.md`: in the M20 PR 2 entry's "Known
      gaps" list, remove "The intermediate image-size files and the original file on disk are not
      removed by the repository (the on-disk unlink is outside the transaction)." and replace it
      with a short description of what is now removed (sizes, original image, backup sizes, a
      non-shared thumb, source-image and animated-video sideload companions, all guarded to the
      uploads directory) and a pointer to the new M23 entry; add an `M23 — Media delete file cleanup
      (PR 2)` entry naming the scope and the "Verified on SQLite, MySQL 8.0 and PostgreSQL 16"
      line, matching the style of the M20 PR 2 entry it supersedes in part.
      `plans/post-m10-gap-analysis/README.md` and `plans/README.md` row 20/23: update the open-gap
      language the same way. No RED step (documentation).
      Files: `docs/compatibility.md`, `plans/post-m10-gap-analysis/README.md`, `plans/README.md`
      Verify: `grep -rn "image-size files.*not removed\|on-disk unlink is outside the transaction"
      docs plans` finds nothing stale (outside the historical M20 entry's own corrected wording).
      _(Req 10-equivalent documentation requirement; Req 11.1 recorded alongside)_

- [x] 7.2 Final gate for PR 2, with explicit cross-vendor verification. Confirm: no new file under
      `internal/storage/migrations`; `git diff main -- internal/domain/repository.go` adds exactly
      one method (`BackupSizes`) to an existing interface, no new interface; no new capability
      string; `go.mod`/`go.sum` unchanged; no new `GRIMOIRE_TEST_*` variable. Run `gofmt -l .`
      (empty), `go vet ./...`, `go build ./...`, `go test ./...`, `go test -count=2 ./...`, `go test
      -race ./internal/content ./internal/storage/... ./internal/web ./cmd/grimoire`.
      **Cross-vendor:** fresh throwaway MySQL 8.0 and PostgreSQL 16 containers (same procedure as
      6.5), the whole storage suite with both variables set, no subtest skipped, final lines in the
      PR description, containers removed. `TestBuiltinGolden` green (no `-update`, since no response
      shape changed). Ensure all tests pass, ask the user if questions arise.
      Files: (verification only)
      Verify: all gate commands green on SQLite, MySQL and PostgreSQL; `docker ps -a` shows no
      leftover container. _(Req 26.3, 26.6; sign-off 5, 6)_

## Phase 8 — Checkpoint

- [~] 8. Checkpoint — Ensure all tests pass, ask the user if questions arise.

## Phase 9 — `ThemeModCleaner` port and storage implementation (`internal/domain`, `internal/storage/wprepo`, TDD) — PR 3

- [x] 9.1 Write the failing tests first. (a) A pure-logic test (new file
      `internal/storage/wprepo/thememod_test.go` or a package-free pure function test if the
      decision logic is factored out, e.g. `internal/content/thememod_test.go`, for a function
      taking a decoded `map[string]any` plus id/URL/option values and returning the resulting map
      and the options to delete): examples for a `custom_logo` match removing `custom_logo` and
      `header_text`; a `header_image`/`header_image_data` URL match; a `background_image` URL
      match; a `site_logo` id match; a `site_icon` id match; no match (nothing changes); a match
      that empties the map (signals "delete the row"); an absent/malformed `theme_mods` map
      (no-op); and **Property 1** (`TestPropertyThemeModCleanupMatchesReference`, `testing/quick`,
      seeded, `MaxCount: 100`, tag `// Feature: media-delete-and-purge-config, Property 1: Theme-mod
      and option cleanup matches exactly on a reference, and leaves no empty row`): for any
      generated attachment id/URL and generated `theme_mods` map plus `site_logo`/`site_icon`
      values, a key/option is absent afterward if and only if its matching rule held before the
      call, every non-matching key/option is unchanged, and an emptied map signals row deletion. (b)
      `internal/storage/storagetest/thememod_contract.go`, `RunThemeModCleanerContract(t, newRepos
      NewReposWithPrefixFunc)`, registered in `sqlite_test.go`, `mysql_test.go`, `postgres_test.go`:
      the full Requirement 1 matrix from `design.md` "Testing Strategy" (PR 3) against seeded
      `theme_mods_{stylesheet}`, `site_logo`, `site_icon` option rows.
      Files: `internal/content/thememod_test.go` (or `internal/storage/wprepo/thememod_test.go`),
      `internal/storage/storagetest/thememod_contract.go`,
      `internal/storage/storagetest/sqlite_test.go`, `internal/storage/storagetest/mysql_test.go`,
      `internal/storage/storagetest/postgres_test.go`
      Verify: `go test ./internal/content ./internal/storage/... -run
      'ThemeMod|PropertyThemeModCleanupMatchesReference'` — fails to compile (`ThemeModCleaner`,
      `CleanAttachmentReferences` do not exist).
      _(Req 1.1-1.9, 6.1, 26.1, 26.2)_

- [x] 9.2 Implement the port and the storage logic. In `internal/domain/repository.go`, add the
      additive `ThemeModCleaner` interface exactly as `design.md` "Components and Interfaces" (PR 3)
      specifies. In `internal/storage/wprepo/` (a new `thememod.go`, or added to the existing
      options-writer file), implement `CleanAttachmentReferences` on `*OptionRepo` as one
      transaction: read `theme_mods_{stylesheet}` and edit the serialized string by byte span (no
      decode/re-encode; see `design.md`), applying the pure decision logic from 9.1(a),
      write back or delete the row, then check and delete `site_logo`/`site_icon` as specified.
      Register `_ domain.ThemeModCleaner = (*OptionRepo)(nil)` alongside the existing compile-time
      checks in `internal/storage/wprepo/writers.go`.
      Files: `internal/domain/repository.go`, `internal/storage/wprepo/thememod.go`,
      `internal/storage/wprepo/writers.go`
      Verify: `go test ./internal/content ./internal/storage/...` — 9.1 passes; `go build ./...`
      green. _(Req 1.1-1.9, 6.1)_

- [x] 9.3 Cross-vendor verification for the new port, before wiring. Run the whole storage suite
      against throwaway MySQL 8.0 and PostgreSQL 16 containers (`design.md` "Testing Strategy" for
      PR 3), confirming every `ThemeModCleaner` subtest passes under both vendors. Record the
      commands and output; remove the containers afterward. No RED step (verification).
      Files: (verification only)
      Verify: `go test ./internal/storage/... -count=1 -v` with both DSNs set shows the
      `ThemeModCleaner` subtests passing under `mysql` and `postgres`. _(Req 26.6-equivalent
      cross-vendor proof)_

## Phase 10 — Wire theme-mod cleanup into `MediaWriteService.Delete` (`internal/content`, `cmd/grimoire`, TDD) — PR 3

- [x] 10.1 Write the failing tests first. In `internal/content/mediawriteservice_test.go`: extend
      `Delete`'s test matrix with a fake `ThemeModCleaner` double: deleting an attachment whose id
      matches a seeded `custom_logo` calls the cleaner with the right id/URL/stylesheet; deleting an
      unrelated attachment does not change the fake's recorded state in a way a no-match wouldn't;
      a cleaner that returns an error is logged (via a test `*slog.Logger` writing to a buffer) and
      swallowed, `Delete` still returns the pre-delete snapshot with no error; `Delete` with no
      `ThemeModCleaner` wired (nil) behaves exactly as before 10.2 lands (no call attempted, no
      panic).
      Files: `internal/content/mediawriteservice_test.go`
      Verify: `go test ./internal/content -run TestMediaWriteServiceDelete` — fails: the fake
      `ThemeModCleaner` is never called (no field, no option exists yet on `MediaWriteService`).
      _(Req 1.10-1.12, 26.1, 26.2)_

- [x] 10.2 Extend `MediaWriteService.Delete` and `MediaConfig`. In
      `internal/content/mediawriteservice.go`: add an optional `themeMods domain.ThemeModCleaner`
      field and `WithMediaThemeModCleaner(c domain.ThemeModCleaner)` option (nil-safe: `Delete`
      performs no theme-mod cleanup when unwired). In `internal/content/media.go`, add `Theme
      string` to `MediaConfig`. After the file-cleanup step in `Delete` (and only after
      `s.writer.Delete` has already succeeded), call
      `s.themeMods.CleanAttachmentReferences(ctx, id, attachmentURL, s.media.Config().Theme)` when
      `s.themeMods != nil`, computing `attachmentURL` from `s.media.Config().BaseURL` and `cur.
      Filename` the same way `MediaService.Store` builds a stored attachment's URL; log and swallow
      any error from the call via `s.log` (added in task 6.2), never altering the returned `cur` or
      error.
      Files: `internal/content/mediawriteservice.go`, `internal/content/media.go`
      Verify: `go test ./internal/content` — 10.1 passes; `go build ./...` green. _(Req 1.10-1.12)_

- [x] 10.3 Write the failing assembly wiring test first. In `cmd/grimoire`, add a test (e.g.
      `assembly_media_thememod_cleanup_test.go`) that, through `buildHandler`, seeds a
      `theme_mods_{stylesheet}` option whose `custom_logo` equals a seeded attachment's id, deletes
      that attachment via REST `DELETE ?force=true`, and asserts the `custom_logo` key is gone from
      the option afterward.
      Files: `cmd/grimoire/assembly_media_thememod_cleanup_test.go`
      Verify: `go test ./cmd/grimoire -run MediaThemeModCleanup` — fails: the option is unchanged
      (`WithMediaThemeModCleaner` and `MediaConfig.Theme` are not yet wired in
      `cmd/grimoire/assembly.go`).
      _(Req 9-equivalent wiring proof for PR 3)_

- [x] 10.4 Wire the theme-mod cleaner into the real binary. In `cmd/grimoire/assembly.go`: pass
      `content.WithMediaThemeModCleaner(repos.Options)` (or the concrete `*wprepo.OptionRepo` the
      factory already constructs, whichever the `storage.Repositories` struct already exposes under
      a name implementing `domain.OptionWriter`) to `content.NewMediaWriteService(...)`, and set
      `Theme: cfg.Theme` on the `content.MediaConfig` passed to `content.NewMediaService(...)`.
      Files: `cmd/grimoire/assembly.go`
      Verify: `go test ./cmd/grimoire` — 10.3 passes. **Proof of wiring, two removals, each recorded
      in the PR description:** remove `WithMediaThemeModCleaner(...)`: `MediaThemeModCleanup` fails
      (the option never changes); restore. Revert `Theme: cfg.Theme` to `Theme: ""`:
      `MediaThemeModCleanup` fails (the cleaner looks up `theme_mods_` instead of the seeded
      stylesheet's row); restore.

- [x] 10.5 Final documentation for PR 3. `docs/compatibility.md`: in the M20 PR 2 entry's "Known
      gaps" list, remove "Theme mods that reference the attachment (`custom_logo`, `header_image`
      and similar) are not cleared." and replace it with a short description of what is now cleared
      (`custom_logo`/`header_text`, `header_image`/`header_image_data`, `background_image`,
      `site_logo`, `site_icon`) and a pointer to the new M23 entry; extend the M23 entry added in
      task 7.1 (or add a new `M23 — Theme-mod cleanup (PR 3)` entry if the milestone's entries are
      kept per-PR, matching the M20/M22 convention of one entry per PR) naming the scope and the
      cross-vendor verification line. Record, per Requirement 11.1, that the REST media `DELETE`
      status-code divergence (`400` vs WordPress's `501`), child-attachment re-parenting, WordPress
      hooks/the object cache, the fixed purge batch size, and media trash remain open, unchanged by
      M23. `plans/post-m10-gap-analysis/README.md` and `plans/README.md` row 20/23: update the
      open-gap language the same way and mark row 23 `✅ Implemented (3 of 3 shipped)` once this
      task lands as the milestone's last documentation task.
      Files: `docs/compatibility.md`, `plans/post-m10-gap-analysis/README.md`, `plans/README.md`
      Verify: `grep -rn "theme mods.*not cleared" docs plans` finds nothing stale (outside the
      historical M20 entry's own corrected wording); `plans/README.md` row 23 reads Implemented.
      _(Req 10-equivalent documentation requirement; Req 11.1)_

- [x] 10.6 Final gate for PR 3, with explicit cross-vendor verification. Confirm: no new file under
      `internal/storage/migrations`; `git diff main -- internal/domain/repository.go` adds exactly
      one interface (`ThemeModCleaner`); no new capability string; `go.mod`/`go.sum` unchanged; no
      new `GRIMOIRE_TEST_*` variable. Run `gofmt -l .` (empty), `go vet ./...`, `go build ./...`, `go
      test ./...`, `go test -count=2 ./...`, `go test -race ./internal/content
      ./internal/storage/... ./cmd/grimoire`. **Cross-vendor:** fresh throwaway MySQL 8.0 and
      PostgreSQL 16 containers (same procedure as 9.3), the whole storage suite with both variables
      set, no `ThemeModCleaner` subtest skipped, final lines in the PR description, containers
      removed. `TestBuiltinGolden` green (no `-update`). Ensure all tests pass, ask the user if
      questions arise.
      Files: (verification only)
      Verify: all gate commands green on SQLite, MySQL and PostgreSQL; `docker ps -a` shows no
      leftover container. _(Req 26.3, 26.6; sign-off 3, 4)_

## Notes

- Tasks marked with `*` are optional; **no sub-task here is optional**. The `ThemeModCleaner`
  contract, the four property tests, the cross-vendor runs, the assembly tests and the
  `TestBuiltinGolden` check are this milestone's done-definition.
- **Property tests** use `testing/quick` with a seeded generator. They are inside the task that
  owns the behavior (2.1, 4.1, 5.1, 9.1), not stand-alone tasks.
- **TDD exceptions are explicit:** 2.5, 2.6, 6.3's assembly wiring is itself a RED/implementation
  pair (6.3/6.4) rather than an exception, and likewise 10.3/10.4; the true exceptions are the
  documentation tasks (2.5, 7.1, 10.5) and the verification/gate tasks (2.6, 6.5, 7.2, 9.3, 10.6).
- **Wiring is tested where it is wired.** Task 2.4 pairs one removal with the named test, task 6.4
  pairs one removal with the named test, task 10.4 pairs two removals with the named test.
- **No golden file changes are expected in any PR.** `TestBuiltinGolden` staying green with no
  `-update` is itself evidence that no response shape changed; a failure there is a stop, not a
  signal to regenerate.
- **Real-WordPress-database grounding was not completed during design** (see `design.md`
  "Decisions awaiting sign-off", item 6): the design's estimate of how often `sizes`/
  `backup_sizes`/theme-mod rows occur in practice rests entirely on reading WordPress core's
  source, because the sandbox used to write this spec declined a request to read a database
  password from a running container and issue read-only `SELECT`s with it. Before or during PR 2
  and PR 3's cross-vendor gate tasks (6.5/7.2 and 9.3/10.6), a maintainer with direct access to the
  dev WordPress database should add a read-only `GRIMOIRE_TEST_WP_DSN`-gated check (patterned on
  `test/e2e/m9b_realdb_census_test.go` or `m22_trash_realdb_test.go`) that counts attachments
  carrying `sizes`/`original_image`/`backup_sizes` in `_wp_attachment_metadata` and checks whether
  `theme_mods_*`, `site_logo`, or `site_icon` rows exist, and adjust the fixture matrix in 6.1/9.1
  if real data reveals a metadata shape (for example legacy `thumb`-only attachments predating
  registered sizes) this design did not anticipate. This is not blocking the three PRs above — the
  design's behavior matches WordPress core's own source exactly regardless — but it is the one
  verification step this spec could not perform itself and should not be silently skipped.
- **Open sign-off items** are listed in `design.md` "Decisions awaiting sign-off", with the task
  that implements each default. Reversing one is local to that task.
- **Not done here, recorded as gaps (Requirement 11):** the REST media `DELETE` status-code
  divergence (`400` vs WordPress's `501`); re-pointing child attachments' `post_parent` away from a
  deleted parent attachment; WordPress hooks and the object cache; the fixed purge batch size
  (100); trash for media.

## Task Dependency Graph

```json
{
  "waves": [
    { "id": 0, "tasks": ["1.1"] },
    { "id": 1, "tasks": ["1.2"] },
    { "id": 2, "tasks": ["2.1"] },
    { "id": 3, "tasks": ["2.2"] },
    { "id": 4, "tasks": ["2.3"] },
    { "id": 5, "tasks": ["2.4"] },
    { "id": 6, "tasks": ["2.5"] },
    { "id": 7, "tasks": ["2.6"] },
    { "id": 8, "tasks": ["3"] },
    { "id": 9, "tasks": ["4.1"] },
    { "id": 10, "tasks": ["4.2"] },
    { "id": 11, "tasks": ["5.1"] },
    { "id": 12, "tasks": ["5.2"] },
    { "id": 13, "tasks": ["6.1"] },
    { "id": 14, "tasks": ["6.2"] },
    { "id": 15, "tasks": ["6.3"] },
    { "id": 16, "tasks": ["6.4"] },
    { "id": 17, "tasks": ["6.5"] },
    { "id": 18, "tasks": ["7.1"] },
    { "id": 19, "tasks": ["7.2"] },
    { "id": 20, "tasks": ["8"] },
    { "id": 21, "tasks": ["9.1"] },
    { "id": 22, "tasks": ["9.2"] },
    { "id": 23, "tasks": ["9.3"] },
    { "id": 24, "tasks": ["10.1"] },
    { "id": 25, "tasks": ["10.2"] },
    { "id": 26, "tasks": ["10.3"] },
    { "id": 27, "tasks": ["10.4"] },
    { "id": 28, "tasks": ["10.5"] },
    { "id": 29, "tasks": ["10.6"] }
  ]
}
```
