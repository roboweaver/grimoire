# M23 — Media delete file cleanup, theme-mod cleanup, and a configurable purge cadence: Design

## Overview

Three independent gaps recorded against M20 PR 2 and M22 PR 3 close in this milestone, each as its
own pull request:

- **PR 1 — configurable purge cadence.** `internal/scheduler.Scheduler` purges trashed rows at a
  fixed, unexported `purgeInterval = time.Hour`. A new config key,
  `scheduler.trash_purge_interval_seconds`, replaces the constant with an operator-set value,
  default 3600 (today's behavior unchanged).
- **PR 2 — media file cleanup on delete.** `content.MediaWriteService.Delete` unlinks only the
  attachment's main file. WordPress's `wp_delete_attachment_files` also removes every registered
  size variant, the original image, backup sizes, and (on current trunk) a shared `thumb`, a
  `source_image` sideload companion, and animated-video companions, each resolved relative to the
  uploads directory and guarded against escaping it.
- **PR 3 — theme-mod and option cleanup on delete.** WordPress's `_delete_attachment_theme_mod()`
  and `WP_Site_Icon::delete_attachment_data()` clear `custom_logo`/`header_text`,
  `header_image`/`header_image_data`, `background_image` (all via `theme_mods_{stylesheet}`), and
  the `site_logo`/`site_icon` options, when the deleted attachment is the one referenced. grimoire
  does none of this today.

No schema change, no migration, in any of the three PRs. PR 1 touches only
`internal/config`, `internal/scheduler` and `cmd/grimoire`. PR 2 and PR 3 both edit
`content.MediaWriteService.Delete`, so PR 3 is sequenced after PR 2 merges to avoid two
concurrent edits to the same function; neither has a data dependency on the other and either
could ship first if reordered.

### Evidence from WordPress core (trunk, `wordpress-develop`)

- `wp-includes/theme.php`, `_delete_attachment_theme_mod( $id )` (hooked on `delete_attachment`,
  `wp-includes/default-filters.php:588`): compares `wp_get_attachment_url( $id )` against
  `get_header_image()` / `get_background_image()`, and compares `$id` against
  `(int) get_theme_mod( 'custom_logo' )` and `(int) get_option( 'site_logo' )`. On a `custom_logo`
  match it removes `custom_logo` **and** `header_text`. On a header/background URL match it removes
  `header_image`+`header_image_data`, or `background_image`, respectively.
- `wp-admin/includes/class-wp-site-icon.php`, `WP_Site_Icon::delete_attachment_data( $post_id )`
  (hooked on `delete_attachment` in the constructor): compares `$post_id` against
  `(int) get_option( 'site_icon' )` and calls `delete_option( 'site_icon' )` on a match. `site_icon`
  is a plain option, not a theme mod.
- `remove_theme_mod()` / `remove_theme_mods()` (`wp-includes/theme.php`): removing the last key from
  `theme_mods_{stylesheet}` deletes the option row rather than writing back `a:0:{}`.
- `wp-includes/post.php`, `wp_delete_attachment()`: deletes the database rows first (`$wpdb->delete`
  on `wp_posts`), fires `deleted_post`, and only **then** calls
  `wp_delete_attachment_files( $post_id, $meta, $backup_sizes, $file )`, discarding its return
  value.
- `wp_delete_attachment_files()`: resolves `thumb`, each `sizes[*].file`, `original_image`,
  `source_image`, `animated_video`, `animated_video_poster`, and each `backup_sizes[*].file` against
  `dirname($file)` under the uploads base dir, and the main `$file` itself against the uploads base
  dir, each through `wp_delete_file_from_directory( $target, $dir )`, which `realpath()`s both sides
  and requires the target to be a (trailing-slash-normalized) prefix match of the directory before
  calling `wp_delete_file` (`@unlink`). A `thumb` is skipped when another attachment's
  `_wp_attachment_metadata` LIKE-matches the same filename.
- Links: [`wp-includes/theme.php`](https://github.com/WordPress/wordpress-develop/blob/trunk/src/wp-includes/theme.php),
  [`wp-admin/includes/class-wp-site-icon.php`](https://github.com/WordPress/wordpress-develop/blob/trunk/src/wp-admin/includes/class-wp-site-icon.php),
  [`wp-includes/post.php`](https://github.com/WordPress/wordpress-develop/blob/trunk/src/wp-includes/post.php).

### Grounding against grimoire at `main` `ed3a0aa`

- `content.MediaWriteService.Delete` (`internal/content/mediawriteservice.go:206-232`): loads the
  attachment, authorizes, calls `s.writer.Delete` (the DB transaction), then best-effort unlinks
  exactly `filepath.Join(s.media.Config().UploadsDir, cur.Filename)`. No size variants, no backup
  sizes, no theme mods.
- `wprepo.MediaRepo.Delete` (`internal/storage/wprepo/media.go:263-317`) already removes
  `_thumbnail_id` postmeta on other posts referencing the attachment, inside the transaction — this
  closes the "confirm `_thumbnail_id` is already cleared" item from the brief; no new work.
- `internal/web/rest_media.go:387-391`: REST `DELETE /media/{id}` without `?force=true` answers
  `400 rest_trash_not_supported` where WordPress answers `501`. Confirmed divergent; recorded as a
  gap (Requirement 11), not changed here, because it shares no code path with the file-cleanup or
  theme-mod work and changing a REST status code for an unrelated reason would be an unrequested,
  separate behavior change.
- `internal/scheduler/scheduler.go:44-48,205-233`: `purgeInterval = time.Hour` is an unexported
  constant checked once per tick in `purgeTrash`; `WithTrashPurge` takes no interval parameter
  today.
- `internal/config/content.go` / `internal/config/config.go`: the existing
  `content.trash_days` / `scheduler.interval_seconds` precedent (integer scalar, pointer-typed
  only where "unset vs. zero" matters, `applyEnv`/`applyDefaults`/`Validate` three-stage pipeline,
  env var named `GRIMOIRE_<SECTION>_<KEY>`) is the pattern this design reuses exactly for
  `scheduler.trash_purge_interval_seconds`.
- `internal/web/uploads.go`'s `resolveWithinRoot`/`evalSymlinksLenient` is the existing,
  already-reviewed path-containment check for "is this resolved path inside the uploads root,
  even through symlinks and even when the leaf doesn't exist" — PR 2 reuses it rather than writing
  a second containment check.

## Architecture

```mermaid
flowchart TD
    subgraph "PR 1: purge cadence"
        CFG[config.SchedulerConfig.TrashPurgeIntervalSeconds] --> ASM1[cmd/grimoire assembly.go buildScheduler]
        ASM1 --> SCHED[scheduler.WithTrashPurge interval]
        SCHED --> TICK[Scheduler.purgeTrash]
    end
    subgraph "PR 2: file cleanup"
        DEL[MediaWriteService.Delete] --> DBDEL[writer.Delete DB tx]
        DBDEL -->|committed| META[Attachment_Metadata + Backup_Sizes read]
        META --> CAND[candidate file paths]
        CAND --> GUARD[uploads-dir containment check]
        GUARD -->|inside| UNLINK[best-effort os.Remove]
        GUARD -->|outside| SKIP[skip, no error]
    end
    subgraph "PR 3: theme-mod cleanup"
        DEL --> THEME[ThemeModCleaner.CleanAttachmentReferences]
        THEME --> OPTS[OptionRepo: theme_mods_stylesheet, site_logo, site_icon]
    end
```

### Sequencing within `MediaWriteService.Delete`

After PR 2 and PR 3 both ship, `Delete` performs, in this order:

1. Load the attachment (`s.reader.ByID`) — unchanged.
2. Authorize (`auth.CanDeletePost`) — unchanged.
3. Read Attachment_Metadata and Backup_Sizes for file-cleanup candidates (new in PR 2) — **before**
   the database delete, because `writer.Delete` removes the attachment's own postmeta rows as part
   of its transaction (M20), so the metadata must be captured before that transaction commits.
   WordPress reads `$meta`/`$backup_sizes`/`$file` the same way, before its own delete (see
   `wp_delete_attachment`, which reads them right after the `pre_delete_attachment` filter and right
   before `delete_attachment` fires, well before the `$wpdb->delete` call).
4. Call `s.writer.Delete` (the existing DB transaction: menu items, `_thumbnail_id`, comments,
   relationships, postmeta, the `posts` row) — unchanged.
5. On a `writer.Delete` error, return immediately — unchanged. No file or theme-mod cleanup runs.
6. Best-effort unlink the main file (existing) **and** every file-cleanup candidate from step 3
   (new in PR 2), each independently guarded against escaping Uploads_Dir, each failure logged and
   swallowed.
7. Best-effort theme-mod/option cleanup (new in PR 3) using the pre-delete snapshot (`cur`) already
   held from step 1 — the attachment id and its (pre-delete) filename/URL.
8. Return `cur`, the pre-delete snapshot — unchanged.

Steps 6 and 7 are independent of each other (file paths vs. options) and both independent of
whether the other succeeds, matching Requirement 4's "no file deletion failure blocks another" and
Requirement 1.11's "a theme-mod cleanup failure does not change the returned attachment."

## Components and Interfaces

### PR 1 — `internal/config`

```go
// SchedulerConfig, extended:
type SchedulerConfig struct {
    IntervalSeconds           int  `yaml:"interval_seconds"`
    TrashPurgeIntervalSeconds *int `yaml:"trash_purge_interval_seconds"`
}

// DefaultTrashPurgeIntervalSeconds is the cadence applied when the key is
// unset: today's fixed hourly cadence, unchanged.
const DefaultTrashPurgeIntervalSeconds = 3600

// EffectiveTrashPurgeInterval returns the configured cadence as a
// time.Duration, or the default when unset. Mirrors
// ContentConfig.EffectiveTrashDays's nil-vs-zero handling, but zero is
// rejected at load (Requirement 7.4), not a valid "effective" value here.
func (s SchedulerConfig) EffectiveTrashPurgeInterval() time.Duration {
    if s.TrashPurgeIntervalSeconds == nil {
        return DefaultTrashPurgeIntervalSeconds * time.Second
    }
    return time.Duration(*s.TrashPurgeIntervalSeconds) * time.Second
}
```

`applyEnv` gains:

```go
if v, ok := os.LookupEnv("GRIMOIRE_SCHEDULER_TRASH_PURGE_INTERVAL_SECONDS"); ok {
    if n, err := strconv.Atoi(v); err == nil {
        c.Scheduler.TrashPurgeIntervalSeconds = &n
    }
}
```

`Validate` gains, alongside the existing `content.trash_days` check:

```go
if c.Scheduler.TrashPurgeIntervalSeconds != nil && *c.Scheduler.TrashPurgeIntervalSeconds <= 0 {
    return fmt.Errorf("config: scheduler.trash_purge_interval_seconds must be a positive integer, got %d", *c.Scheduler.TrashPurgeIntervalSeconds)
}
```

A non-integer YAML scalar (`7.5`, `"abc"`, a quoted `"30"`) is already rejected by plain
`yaml.Unmarshal` into a `*int` field the same way `content.trash_days`'s unstructured path would be
if `ContentConfig` did not have its stricter `UnmarshalYAML`; `SchedulerConfig` has no custom
`UnmarshalYAML` today and does not need one here, because `*int` unmarshaling already errors on a
non-integer scalar (the design deliberately does **not** add a `checkTrashPurgeIntervalNode`-style
strict-YAML gate, matching how `scheduler.interval_seconds`, a plain `int`, is handled today —
only `ContentConfig` has the strict, unknown-key-rejecting `UnmarshalYAML`, and that is unrelated
to this key's type validation).

### PR 1 — `internal/scheduler`

```go
// purgeInterval constant is removed; WithTrashPurge gains a parameter.
func WithTrashPurge(l domain.TrashedPostLister, d trashDeleter, days int, types []string, interval time.Duration) Option {
    return func(s *Scheduler) {
        s.trashed, s.deleter, s.purgeDays, s.purgeTyps, s.purgeInterval = l, d, days, types, interval
    }
}
```

`Scheduler` gains a `purgeInterval time.Duration` field (replacing the read of the package
constant inside `purgeTrash`). `New` defaults `purgeInterval` to `DefaultTrashPurgeIntervalSeconds`
seconds so a caller that does not pass the new parameter (none remain once `cmd/grimoire` is
updated, but this keeps the zero value sane for any other test construction) does not silently get
a zero-duration "always purge" scheduler. `purgeTrash`'s existing `now.Sub(s.lastPurge) <
purgeInterval` check becomes `now.Sub(s.lastPurge) < s.purgeInterval`; no other line in
`purgeTrash` changes.

### PR 1 — `cmd/grimoire/assembly.go`

```go
scheduler.WithTrashPurge(deps.Repos.TrashedPosts, posts, deps.Config.Content.EffectiveTrashDays(),
    registeredTypeNames(deps.Registry), deps.Config.Scheduler.EffectiveTrashPurgeInterval())
```

### PR 2 — `internal/content/mediawriteservice.go`

```go
// backupSizeReader is the narrow read MediaWriteService.Delete needs for
// Requirement 2.6. It is satisfied by wprepo.PostMetaRepo through a new,
// equally narrow accessor method (not a new domain port -- Requirement 6.2
// forbids a new port; this is an additional exported method on the
// existing PostMetaRepository interface instead, the same shape as
// AttachmentMetadata).
//
// domain.PostMetaRepository gains one method:
//   BackupSizes(ctx context.Context, postID int64) (string, error)
// returning the raw _wp_attachment_backup_sizes value, "" if unset -- the
// same shape as the existing AttachmentMetadata method on the same
// interface, so no new port is introduced (Requirement 6.1 forbids a new
// port; this is one more method on an existing read-only port already
// implemented by wprepo.PostMetaRepo, which already has the private `get`
// helper this reuses).

// mediaFileCandidates decodes an attachment's Attachment_Metadata and
// Backup_Sizes into the list of relative (to the main file's directory)
// filenames that wp_delete_attachment_files would also delete, skipping a
// thumb filename when thumbSharedElsewhere reports it is still referenced.
// It is a pure function over decoded data, independent of any I/O, mirroring
// content.sizesFromMetadata's decode style (same php.Unserialize, same
// tolerate-malformed-as-empty rule).
func mediaFileCandidates(metaRaw, backupRaw string) []string

// deleteMediaFiles resolves each candidate (plus the main file) against
// cfg.UploadsDir, applies the same containment check internal/web/uploads.go
// uses, and best-effort os.Remove()s each in-bounds path, swallowing
// per-file errors (Requirement 4).
func deleteMediaFiles(cfg MediaConfig, mainFile string, candidates []string, log *slog.Logger)
```

`Delete` becomes:

```go
func (s *MediaWriteService) Delete(ctx context.Context, actor auth.Principal, id int64) (domain.Media, error) {
    cur, err := s.reader.ByID(ctx, id)
    if err != nil {
        return domain.Media{}, err
    }
    if !auth.CanDeletePost(actor, "post", "inherit", cur.AuthorID) {
        return domain.Media{}, ErrForbidden
    }
    var candidates []string
    if s.meta != nil {
        metaRaw, _ := s.meta.AttachmentMetadata(ctx, id)
        backupRaw, _ := s.meta.BackupSizes(ctx, id)
        candidates = mediaFileCandidates(metaRaw, backupRaw)
        candidates = filterSharedThumb(ctx, s.meta, id, candidates) // Req 2.3
    }
    if err := s.writer.Delete(ctx, id); err != nil {
        return domain.Media{}, err
    }
    if rel := cur.Filename; rel != "" {
        deleteMediaFiles(s.media.Config(), rel, candidates, s.log)
    }
    if s.themeMods != nil {
        if err := s.themeMods.CleanAttachmentReferences(ctx, id, cur.Filename, s.media.Config()); err != nil && s.log != nil {
            s.log.Error("mediawriteservice: theme-mod cleanup failed", "attachment_id", id, "error", err)
        }
    }
    return cur, nil
}
```

`s.meta domain.PostMetaRepository` and `s.log *slog.Logger` are new, optional fields with
`WithMediaMetaReader` / a logger option (or threading the process logger the same way other
services that log already do — `internal/web` has `*Server`'s existing logger; `MediaWriteService`
has none today, so this introduces its first). Both are nil-safe: Delete behaves exactly as it does
today (main-file-only cleanup, no theme-mod cleanup) when either is left unwired, so an existing
caller that constructs `MediaWriteService` without the new options keeps today's behavior exactly
— no silent new side effect.

**`filterSharedThumb`** (Requirement 2.3): queries whether any *other* attachment's
`_wp_attachment_metadata` also names the candidate `thumb` filename. This is the one candidate that
needs a lookup beyond the deleted attachment's own metadata. It is implemented as one additional,
narrow, read-only method on the same `PostMetaRepository`-shaped reader (or a dedicated tiny
interface `thumbSharedChecker` satisfied by the same concrete repo), backed by the existing
`LIKE '%' || ? || '%'` pattern WordPress itself uses (`wp_delete_attachment_files`'s own
`$wpdb->get_row` guard), scoped to `meta_key = '_wp_attachment_metadata' AND post_id <> ?`.

### PR 3 — new storage port `domain.ThemeModCleaner`

```go
// ThemeModCleaner removes theme mods and options that reference a specific
// attachment, mirroring WordPress's _delete_attachment_theme_mod() and
// WP_Site_Icon::delete_attachment_data(). It is additive: no existing port
// gains a method (Req 1 design intent, matching M20/M22's "one additive
// port" convention).
type ThemeModCleaner interface {
    // CleanAttachmentReferences removes, in one transaction:
    //  - the custom_logo and header_text keys of theme_mods_{stylesheet}
    //    when custom_logo's numeric value equals attachmentID;
    //  - the header_image and header_image_data keys when header_image's
    //    value equals attachmentURL;
    //  - the background_image key when background_image's value equals
    //    attachmentURL;
    //  - option site_logo when its numeric value equals attachmentID;
    //  - option site_icon when its numeric value equals attachmentID.
    // When removing a theme_mods_{stylesheet} key leaves no keys, the
    // option row is deleted rather than written back empty. Neither the
    // attachment nor any other row needs to exist; a no-match is not an
    // error. stylesheet is the current "stylesheet" option's value.
    CleanAttachmentReferences(ctx context.Context, attachmentID int64, attachmentURL string, stylesheet string) error
}
```

Implemented by `wprepo.OptionRepo` (it already implements `domain.OptionWriter` and has `r.db`,
`r.prefix`; it does not yet read `stylesheet`, but `wprepo.NavMenuRepo.MenuByLocation` already
shows the exact `theme_mods_{stylesheet}` read/decode pattern to follow). `stylesheet` is passed in
by the caller (`MediaWriteService`, via `MediaConfig` gaining a `Theme string` field populated from
`cfg.Theme` the same way `content.NewNavMenuService(repos.NavMenus, cfg.Theme)` already receives
it in `cmd/grimoire/assembly.go`) rather than read by the port itself, so the port has no dependency
on `internal/config` and stays a pure storage operation parameterized by its caller, consistent
with `NavMenuRepo.MenuByLocation(ctx, theme, location)`'s existing shape.

```go
func (r *OptionRepo) CleanAttachmentReferences(ctx context.Context, attachmentID int64, attachmentURL, stylesheet string) error {
    return r.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
        modsOptionName := "theme_mods_" + stylesheet
        raw, mods, err := readOptionMapTx(ctx, tx, r.prefix, modsOptionName) // "", nil on absent/malformed
        if err == nil && mods != nil {
            changed := false
            if logoID, ok := asInt64(mods["custom_logo"]); ok && logoID == attachmentID {
                delete(mods, "custom_logo")
                delete(mods, "header_text")
                changed = true
            }
            if hdr, ok := mods["header_image"].(string); ok && hdr != "" && hdr == attachmentURL {
                delete(mods, "header_image")
                delete(mods, "header_image_data")
                changed = true
            }
            if bg, ok := mods["background_image"].(string); ok && bg != "" && bg == attachmentURL {
                delete(mods, "background_image")
                changed = true
            }
            if changed {
                if err := writeOrDeleteOptionMapTx(ctx, tx, r.prefix, modsOptionName, mods); err != nil {
                    return err
                }
            }
        }
        for _, name := range []string{"site_logo", "site_icon"} {
            if id, ok := optionIntValueTx(ctx, tx, r.prefix, name); ok && id == attachmentID {
                if err := deleteOptionTx(ctx, tx, r.prefix, name); err != nil && !errors.Is(err, domain.ErrNotFound) {
                    return err
                }
            }
        }
        return nil
    })
}
```

`asInt64` already exists in `wprepo` (used by `MenuByLocation`). `readOptionMapTx`,
`writeOrDeleteOptionMapTx`, `optionIntValueTx` and `deleteOptionTx` are small new private helpers
in `wprepo/options.go` (or added to the existing option-writer file), each a thin wrapper over one
`SELECT`/`UPDATE`/`DELETE` inside the passed transaction — no new exported surface beyond the one
port method.

### `content.MediaConfig`, extended

```go
type MediaConfig struct {
    UploadsDir   string
    BaseURL      string
    AllowedMIMEs []string
    MaxUploadSize int64
    // Theme is the active theme's stylesheet slug (cfg.Theme), used only by
    // Delete's theme-mod cleanup (Req 1) to name theme_mods_{stylesheet}.
    // Zero value "" is harmless: CleanAttachmentReferences on
    // theme_mods_ (empty stylesheet) simply never matches a real site's
    // option row.
    Theme string
}
```

## Data Models

No new tables or columns. Data read and written:

| Name | Shape | Read by | Written by |
|---|---|---|---|
| `{prefix}postmeta` `_wp_attachment_metadata` | PHP-serialized map, keys `sizes` (map), `original_image`, `thumb`, `source_image`, `animated_video`, `animated_video_poster` (strings) | PR 2's `mediaFileCandidates`, via a new `AttachmentMetadata`-adjacent read (existing) | not written by this milestone |
| `{prefix}postmeta` `_wp_attachment_backup_sizes` | PHP-serialized map, values `{file, width, height, ...}` | PR 2's `mediaFileCandidates`, via the new `PostMetaRepository.BackupSizes` method | not written by this milestone |
| `{prefix}options` `theme_mods_{stylesheet}` | PHP-serialized associative array | PR 3's `ThemeModCleaner` | PR 3's `ThemeModCleaner` (key removal or row delete) |
| `{prefix}options` `site_logo`, `site_icon` | PHP-serialized or plain scalar integer string | PR 3's `ThemeModCleaner` | PR 3's `ThemeModCleaner` (row delete) |
| `scheduler.trash_purge_interval_seconds` (config, not DB) | integer seconds | `internal/scheduler`, via `cmd/grimoire` | operator (YAML/env) |

## Correctness Properties

*A property is a characteristic or behavior that should hold true across all valid executions of a
system—essentially, a formal statement about what the system should do. Properties serve as the
bridge between human-readable specifications and machine-verifiable correctness guarantees.*

### Prework

0.1 WHEN `scheduler.trash_purge_interval_seconds` is unset, THE Config_Loader SHALL apply 3600.
  Thoughts: specific value, specific condition. Example.
  Testable: yes - example
0.2 WHEN the env var overrides and parses, THE Config_Loader SHALL use it.
  Thoughts: this is "for all integer strings the env var carries, the resulting value equals that
  integer" -- varies meaningfully with input (any positive int), pure parsing logic.
  Testable: yes - property
0.3 IF the file value is zero, negative or non-integer, THEN THE Config_Loader SHALL fail naming the key.
  Thoughts: "for all non-positive integers and all non-integer scalars" is a property over a
  generated input space (ints <= 0, plus malformed scalar shapes).
  Testable: yes - property
1.1-1.6 attachment-reference matching and removal (custom_logo/header_text, header_image/
  header_image_data, background_image, site_logo, site_icon).
  Thoughts: each is "for all attachment ids and all theme-mod/option payloads, a removal happens
  exactly when the stored value equals the attachment's id/URL, and never otherwise" -- this is a
  pure function over a generated map once the DB read is abstracted into a value object. Testable
  as a property over the pure "what changes" decision function; the DB transaction wrapper itself
  is better covered by a handful of integration examples (seeded option rows, one call, assert the
  row).
  Testable: yes - property (the decision function); yes - example (the storage integration)
1.4 WHEN removal empties theme_mods, THE option row is deleted.
  Thoughts: specific edge case of 1.1-1.3's removal, same generated-map property can assert "if the
  result map is empty, no theme_mods_{stylesheet} row remains" as part of the same property.
  Testable: yes - property (folded into the Requirement 1 property)
1.7-1.9 no match leaves things unchanged / absent-or-malformed is not an error.
  Thoughts: the negative space of the same property -- "for all inputs that do NOT match, nothing
  changes" is the natural counterpart assertion inside the same property test, not a separate one.
  Testable: yes - property (folded in)
1.10-1.12 transaction/ordering/URL-construction mechanics.
  Thoughts: ordering relative to the already-tested M20 transaction and "the URL is built the same
  way MediaService builds it" are integration/wiring concerns, not properties over generated data.
  Testable: no (covered by assembly/integration tests, not PBT)
2.1-2.6 each metadata key's file is deleted; shared thumb survives; missing metadata means
  main-file-only.
  Thoughts: "for all generated Attachment_Metadata/Backup_Sizes shapes (including meta missing
  sizes, missing original_image, a thumb shared or not shared with another attachment's metadata),
  the resulting set of filenames the function returns as candidates is exactly the set
  wp_delete_attachment_files would compute" is a clean, pure, input-varying property over
  mediaFileCandidates/filterSharedThumb, independent of any real filesystem.
  Testable: yes - property
2.7-2.8 main file always attempted; malformed metadata means no candidates beyond the main file.
  Thoughts: folds into the same property (malformed/absent metadata is one of the generated shapes,
  asserting the candidate set is then empty).
  Testable: yes - property (folded in)
3.1-3.3 containment: a candidate outside Uploads_Dir (via .., absolute override, or a symlink
  escape) is skipped; every other in-bounds candidate for the same attachment still deletes.
  Thoughts: "for all candidate path strings, the path is attempted if and only if its resolved form
  is contained in the resolved Uploads_Dir" is exactly the kind of universal property
  internal/web/uploads.go's own resolveWithinRoot is suited to, reused rather than re-derived; a
  property test here exercises deleteMediaFiles's call of that existing function across generated
  relative/absolute/traversal/symlink inputs against a real t.TempDir() tree (filesystem I/O is
  cheap at this scale, unlike an AWS call, so PBT is still appropriate per the decision guide).
  Testable: yes - property
4.1-4.4 ordering after DB delete; missing file is success; other errors are swallowed; Delete still
  returns cur.
  Thoughts: 4.1 is a wiring/ordering fact (integration), not a property. 4.2-4.3 are "for all
  generated candidate sets, however many fail to exist or fail to unlink, Delete's returned error
  is unaffected and every remaining candidate is still attempted" -- a property over a fake/failing
  filesystem. 4.4 folds into the same property (assert the returned attachment every time).
  Testable: 4.1 no (integration/ordering); 4.2-4.4 yes - property
5.1-5.2 the temp-directory test matrix; the storage-delete-failure short-circuit.
  Thoughts: these ARE the test tasks themselves (they name the fixture matrix the properties and
  the storage contract above exercise), not additional requirements to convert into their own
  property.
  Testable: no (this requirement specifies the test plan, not new behavior)
6.1-6.2 no schema change; no new write port; the new read sits on the existing read port.
  Thoughts: structural constraints on the diff, checked by the final gate's `git diff` commands as
  in M20/M22, not by a test assertion over generated data.
  Testable: no
7.1-7.5 (see 0.1-0.3 above; Requirement 7 and the "Prework 0.x" entries are the same acceptance
  criteria renumbered to match requirements.md's own numbering -- consolidated below.)
8.1-8.5 the configured interval governs cadence; first tick always purges regardless of interval;
  interval smaller than tick interval purges at most once per tick; purge stays off when trash_days
  is 0 or unwired.
  Thoughts: "for all positive configured intervals and all generated sequences of tick timestamps,
  a purge scan runs on a tick if and only if no prior scan happened within the configured interval
  (or no prior scan has ever happened)" is a clean property over Scheduler.purgeTrash with an
  injected clock -- directly analogous to M22's own trash-cutoff property testing style
  (scheduler_test.go already does fake-clock testing for the existing fixed interval).
  Testable: yes - property
9.1-9.2 wiring proof through buildScheduler.
  Thoughts: assembly/wiring test, not a property over generated data.
  Testable: no
10.1-10.3, 11.1 documentation and scope records.
  Thoughts: not testable as code behavior.
  Testable: no

### Reflection on redundancy

- The Requirement 1 properties (1.1 through 1.9) are one property over one generated input shape
  (an attachment id/URL pair plus a generated `theme_mods` map and `site_logo`/`site_icon` option
  values): "removal happens exactly on a match, and the result is well-formed (no empty
  `theme_mods` row left behind)". Collapsing five numbered criteria into Property 1 avoids five
  near-identical tests that would all fail or pass together on the same underlying decision logic.
- The Requirement 2 candidate-set properties (2.1 through 2.8) collapse into Property 2: one
  generated `Attachment_Metadata`/`Backup_Sizes` shape in, one candidate filename set out, checked
  against the set WordPress's own function would compute for the same shape. The shared-thumb rule
  (2.3) is a generator dimension (sometimes another attachment's metadata names the same thumb),
  not a separate property, because it is still "does the candidate set match WordPress's rule",
  just with one more input axis.
- The containment property (Requirement 3) is kept separate from Property 2 because it exercises
  real filesystem paths and symlinks, a materially different mechanism (I/O and path resolution)
  from the pure decoding Property 2 covers; merging them would force every Property 2 case through
  a temp directory for no benefit.
- The non-fatal-failure property (Requirement 4.2-4.4) is kept separate from Property 3 (the
  containment property) because it varies a different axis — which candidates fail to delete and
  why — not which candidates are in-bounds; a path can be in-bounds and still fail to unlink (a
  permission error), and the two concerns should not be conflated into one generator.
- The purge-cadence property (Requirement 8) is independent of every file/option property above —
  different subsystem, different inputs (time, not file metadata) — and stays its own property.

### Property 1: Theme-mod and option cleanup matches exactly on a reference, and leaves no empty row

*For any* attachment id, attachment URL, and generated `theme_mods_{stylesheet}` map containing any
combination of `custom_logo`, `header_text`, `header_image`, `header_image_data`,
`background_image` and unrelated keys, plus generated `site_logo` and `site_icon` option values:
after `ThemeModCleaner.CleanAttachmentReferences` runs, a key or option is absent afterward if and
only if its matching rule (numeric `custom_logo`/`site_logo`/`site_icon` equals the attachment id,
or string `header_image`/`background_image` equals the attachment URL) held before the call; every
non-matching key and option is unchanged; and if removing matched keys leaves `theme_mods_` with no
remaining keys, no `theme_mods_{stylesheet}` option row exists afterward.

**Validates: Requirements 1.1, 1.2, 1.3, 1.4, 1.5, 1.6, 1.7, 1.8, 1.9**

### Property 2: Media file candidates match WordPress's own set

*For any* generated `_wp_attachment_metadata` and `_wp_attachment_backup_sizes` PHP-serialized
payload (including absent, empty, or malformed values, and including a `thumb` filename that may
or may not also appear in another attachment's generated metadata), the set of relative filenames
`mediaFileCandidates` (after `filterSharedThumb`) returns equals exactly the set
`wp_delete_attachment_files` would resolve and attempt to delete for the same payload (every
present, non-empty `sizes[*].file`, `original_image`, `source_image`, `animated_video`,
`animated_video_poster` and `backup_sizes[*].file`, plus `thumb` only when no other attachment's
metadata also names it), and the main file is always a member of the final delete attempt
independent of the metadata shape.

**Validates: Requirements 2.1, 2.2, 2.3, 2.4, 2.5, 2.6, 2.7, 2.8**

### Property 3: File deletion never escapes the uploads directory

*For any* generated candidate path string (a plain relative name, a name containing `..` segments,
an absolute path, or a path through a symlink planted inside a temporary uploads directory that
points outside it), `deleteMediaFiles` attempts an unlink if and only if the path's fully resolved
form (symlinks included) lies within the resolved uploads directory, and a path judged out of
bounds never causes any other, in-bounds candidate for the same call to be skipped.

**Validates: Requirements 3.1, 3.2, 3.3**

### Property 4: File deletion failures never change the delete outcome

*For any* generated set of candidate files, each independently marked in the test double as
"missing", "fails with a permission-style error", or "deletes successfully", `MediaWriteService.
Delete` still attempts every candidate exactly once, returns the pre-delete `Media` snapshot
unchanged, and returns no error attributable to any individual file outcome.

**Validates: Requirements 4.2, 4.3, 4.4**

### Property 5: The trash purge scan respects the configured interval

*For any* positive configured Purge_Interval and any generated, strictly increasing sequence of
tick timestamps, a purge scan executes on a given tick if and only if no prior scan has ever run,
or the elapsed time since the last scan that did run is at least the configured Purge_Interval;
this holds independent of the configured Purge_Interval's value relative to the Scheduler_Tick_Interval.

**Validates: Requirements 8.1, 8.2, 8.3, 8.4**

## Error Handling

| Condition | Behavior |
|---|---|
| `scheduler.trash_purge_interval_seconds` is zero, negative, or non-integer in the file | `config.Load` returns an error naming `scheduler.trash_purge_interval_seconds`; the process does not start (Req 7.4). |
| The env var is set but unparsable | ignored; file value or default stands (Req 7.3), matching `GRIMOIRE_CONTENT_TRASH_DAYS`. |
| The env var parses but is <= 0 | `config.Load` returns an error naming the key, checked after the override is applied (Req 7.5). |
| `_wp_attachment_metadata` / `_wp_attachment_backup_sizes` absent, empty, or undecodable | treated as "no size/backup candidates"; only the main file is attempted; no error (Req 2.8). |
| A size/backup/thumb candidate's resolved path escapes Uploads_Dir | skipped silently; every other candidate for the same attachment is still attempted (Req 3.2, 3.3). |
| `os.Remove` on a candidate fails because the file does not exist | treated as success for that file (Req 4.2). |
| `os.Remove` on a candidate fails for another reason (permissions, busy) | logged if a logger is wired, swallowed, remaining candidates still attempted, `Delete` still returns the pre-delete snapshot (Req 4.3, 4.4). |
| `s.writer.Delete` (the DB transaction) fails | `Delete` returns that error immediately; no file or theme-mod cleanup is attempted (Req 4.1, existing behavior unchanged). |
| `ThemeModCleaner.CleanAttachmentReferences` fails (e.g. a transient DB error) | logged if a logger is wired, swallowed; `Delete` still returns the pre-delete snapshot, because the row delete already committed and is not reversible from this step (Req 1.11). |
| `theme_mods_{stylesheet}` absent, unset, or not a decodable associative array | Requirement 1.1-1.4's logic treats it as "no theme mods to clean"; no error (Req 1.8). |
| `site_logo` / `site_icon` absent or not a parsable integer | that sub-rule is skipped; no error (Req 1.9). |

## Testing Strategy

**Dual approach.** Unit/example tests cover specific scenarios (a seeded `theme_mods` row with a
real attachment id, the `thumb`-shared-with-another-attachment case, a config file missing the new
key); property tests (Go's `testing/quick`, the existing project convention, seeded, minimum 100
iterations) cover the universal decision rules named in the five properties above. No new test
dependency.

**PR 1 (`internal/config`, `internal/scheduler`, `cmd/grimoire`):**
- Unit/example: default value is 3600; file value honored; env override honored and recorded;
  env override unparsable is ignored; file value zero/negative/non-integer fails naming the key;
  env value <= 0 after parsing fails naming the key — mirroring the existing
  `TestLoadTrashDays*` suite's exact shape in `internal/config/config_test.go`.
- Property 5, `TestPropertyTrashPurgeRespectsInterval` (`testing/quick`, seeded, `MaxCount: 100`),
  tag `// Feature: media-delete-and-purge-config, Property 5: The trash purge scan respects the
  configured interval`, over `internal/scheduler`'s existing fake-clock harness (the same
  `withClock` option M22's scheduler tests already use).
- Assembly: one `cmd/grimoire` test through `buildScheduler` (Requirement 9.1) asserting the
  configured value reaches the constructed `Scheduler`; the task records that removing the
  `deps.Config.Scheduler.EffectiveTrashPurgeInterval()` argument from the `buildScheduler` call
  makes that test fail (Requirement 9.2).

**PR 2 (`internal/content`, `internal/storage/wprepo`, `internal/domain`):**
- Property 2, `TestPropertyMediaFileCandidates` (`testing/quick`, seeded, `MaxCount: 100`), tag
  `// Feature: media-delete-and-purge-config, Property 2: Media file candidates match WordPress's
  own set`, over `mediaFileCandidates`/`filterSharedThumb` with a generated metadata/backup map and
  a fake "other attachments' metadata" lookup.
- Property 3, `TestPropertyFileDeletionStaysInUploadsDir` (`testing/quick`, seeded, `MaxCount:
  100`), tag `// Feature: media-delete-and-purge-config, Property 3: File deletion never escapes
  the uploads directory`, over `deleteMediaFiles` against a real `t.TempDir()` tree with generated
  relative paths, `..`-containing paths, absolute paths, and one planted symlink per run pointing
  outside the tree.
- Property 4, `TestPropertyFileDeletionFailuresAreNonFatal` (`testing/quick`, seeded, `MaxCount:
  100`), tag `// Feature: media-delete-and-purge-config, Property 4: File deletion failures never
  change the delete outcome`, over a fake unlink function that returns missing/permission/success
  per generated candidate.
- Unit/example: `MediaWriteService.Delete` end-to-end against a `t.TempDir()` uploads directory, the
  exact fixture matrix of Requirement 5.1 (no metadata; full metadata with every key; shared
  `thumb`; backup sizes; a name that does not exist on disk; a name that escapes via `..` and via
  an absolute path); the storage-delete-failure short-circuit of Requirement 5.2.
- Storage contract: `PostMetaRepository.BackupSizes` gets one `RunPostMetaContract`-style addition
  (seeded value, unset value), run on SQLite, MySQL and PostgreSQL via the existing
  `NewReposWithPrefixFunc` convention, no hardcoded prefix or vendor.
- Cross-vendor verification (this PR touches storage): the whole storage suite against throwaway
  `mysql:8.0` and `postgres:16` containers, as M19-M22 required, before the PR opens and again in
  its final gate:
  ```sh
  docker run -d --name g23-mysql -e MYSQL_ROOT_PASSWORD=grimoire_ci -e MYSQL_DATABASE=grimoire_test -p 3306:3306 mysql:8.0
  docker run -d --name g23-pg -e POSTGRES_PASSWORD=grimoire_ci -e POSTGRES_USER=grimoire -e POSTGRES_DB=grimoire_test -p 5432:5432 postgres:16
  # wait for both to accept connections, then:
  GRIMOIRE_TEST_MYSQL_DSN='root:grimoire_ci@tcp(127.0.0.1:3306)/grimoire_test?parseTime=true&multiStatements=true' \
  GRIMOIRE_TEST_POSTGRES_DSN='postgres://grimoire:grimoire_ci@127.0.0.1:5432/grimoire_test?sslmode=disable' \
  go test ./internal/storage/... -count=1 -v
  docker rm -f g23-mysql g23-pg
  ```

**PR 3 (`internal/storage/wprepo`, `internal/domain`, `internal/content`):**
- Property 1, `TestPropertyThemeModCleanupMatchesReference` (`testing/quick`, seeded, `MaxCount:
  100`), tag `// Feature: media-delete-and-purge-config, Property 1: Theme-mod and option cleanup
  matches exactly on a reference, and leaves no empty row`, over the pure decision logic (a
  function taking the decoded `mods map[string]any` plus id/URL/option values and returning the
  resulting map and the list of options to delete), independent of the DB.
- Storage contract: a new `RunThemeModCleanerContract(t, newRepos NewReposWithPrefixFunc)` in
  `internal/storage/storagetest/`, registered in `sqlite_test.go`, `mysql_test.go`,
  `postgres_test.go`, covering: a `custom_logo` match (removes `custom_logo` and `header_text`,
  leaves other keys); a `header_image`/`header_image_data` URL match; a `background_image` URL
  match; a `site_logo` match; a `site_icon` match; no match (nothing changes); a match that empties
  `theme_mods_` (the option row is gone, verified by a direct `SELECT`); an absent `theme_mods_`
  option; a malformed (non-array) `theme_mods_` value; an absent `site_logo`/`site_icon`.
- Cross-vendor verification (this PR touches storage): same container procedure as PR 2, run again
  for PR 3's changes, before the PR opens and again in its final gate.
- Assembly: one `cmd/grimoire` test through `buildHandler` that deletes a seeded attachment
  referenced by a seeded `custom_logo` theme mod and asserts the option row changes; the task
  records that removing the `WithMediaThemeModCleaner`-style wiring edit (or passing a nil
  `ThemeModCleaner`) makes that test fail (no cleanup happens).

**Every PR's final gate:** `gofmt -l .` empty; `go vet ./...`; `go build ./...`; `go test ./...`;
`go test -count=2 ./...`; `go test -race ./internal/content ./internal/storage/... ./internal/
scheduler ./internal/config ./cmd/grimoire`; `TestBuiltinGolden` green (none of the three PRs
changes any golden-exercised response shape, so no golden file update is expected — a failure there
is a stop, not an `-update`).

## Delivery

| PR | What a reviewer can use afterwards |
|---|---|
| 1 (purge cadence config) | `scheduler.trash_purge_interval_seconds` (default 3600) controls how often the automatic trash purge scans, file/env precedent matches `content.trash_days`, proven through `buildScheduler`. |
| 2 (media file cleanup) | Deleting an attachment also removes its registered size variants, original image, backup sizes, a non-shared thumb, and sideload companions, all guarded to the configured uploads directory; proven on three databases. |
| 3 (theme-mod cleanup) | Deleting the attachment used as the site logo, header image, background image, site logo (block), or site icon also clears that setting; proven on three databases. |

Each PR is independently mergeable and independently releasable; PR 3 is sequenced after PR 2 only
to avoid two concurrent edits to `MediaWriteService.Delete`, not because of a data or schema
dependency.

## Decisions awaiting sign-off

1. **Config key name and type.** `scheduler.trash_purge_interval_seconds` (an integer, seconds),
   mirroring the sibling `scheduler.interval_seconds` rather than a Go duration string. Alternative:
   a duration string (`"1h"`); rejected because no existing grimoire config key uses that style.
   Task: PR 1, config task.
2. **No "0 disables the purge" meaning for the new key.** Purge enable/disable stays owned entirely
   by `content.trash_days`; `trash_purge_interval_seconds` of `0` or negative is a load error, not a
   second way to disable the purge. Alternative: treat `0` as "every tick". Task: PR 1, config
   task.
3. **`site_logo` is in scope even though the brief did not name it.** It is one more line in the
   same core function (`_delete_attachment_theme_mod`, since WP 6.6) the brief already asked about;
   leaving it out would mean matching core's `custom_logo`/`header_image`/`background_image`
   handling but not its `site_logo` handling in the very same hook, for no stated reason. Task: PR
   3, theme-mod task.
4. **REST media `DELETE`'s `400` vs WordPress's `501` status code is explicitly not changed here.**
   Recorded as an open gap (Requirement 11), not folded into this milestone, because it is a
   REST-transport decision unrelated to either file cleanup or theme-mod cleanup and changing it
   would be an unrequested behavior change outside the three gaps this milestone targets. Task: none
   (documentation only, final gate of PR 3).
5. **`PostMetaRepository` gains a `BackupSizes` method rather than a new domain port.** Keeps the
   M20/M22 "additive port" convention reserved for genuinely new capabilities (like
   `ThemeModCleaner`), while a second narrow read alongside the existing `AttachmentMetadata` read
   is not a new capability class. Alternative: a new `BackupSizeReader` port. Task: PR 2, storage
   task.
6. **Real-WordPress-database grounding for this design was not performed.** The repository's own
   sandbox policy declined a request to read a database credential out of a running container and
   issue `SELECT`s with it, even read-only and scoped to counting rows, so the design's estimates of
   how common `sizes`/`backup_sizes`/theme-mod rows are on the shared dev database are not
   confirmed against real data; the design instead relies entirely on reading WordPress core's
   source for the exact representation. A maintainer with direct access to the dev database should
   run the read-only counts this design would have run (attachments with `sizes` /
   `original_image` / `backup_sizes` in `_wp_attachment_metadata`; whether `theme_mods_*`,
   `site_logo`, or `site_icon` rows exist) before or during PR 2/PR 3 review, and adjust the test
   fixture matrix if real data reveals a shape (for example WordPress's legacy `thumb`-only
   metadata predating registered sizes) this design did not anticipate. Task: PR 2 and PR 3's
   real-database check task, gated on `GRIMOIRE_TEST_WP_DSN`, read-only.

## Traceability

| Design element | Requirements |
|---|---|
| `SchedulerConfig.TrashPurgeIntervalSeconds`, `EffectiveTrashPurgeInterval`, env/validate | 7 |
| `Scheduler.purgeInterval` field, `WithTrashPurge` new parameter | 8 |
| `buildScheduler` wiring, assembly test | 9 |
| `docs/compatibility.md`, `configs/*.yaml`, gap-analysis updates (PR 1) | 10 |
| `mediaFileCandidates`, `filterSharedThumb`, `deleteMediaFiles` | 2, 4 |
| `PostMetaRepository.BackupSizes` | 2.6, 6 |
| Uploads-dir containment reuse | 3 |
| Temp-directory test matrix, storage-failure short-circuit | 5 |
| `domain.ThemeModCleaner`, `wprepo.OptionRepo` implementation | 1 |
| `MediaConfig.Theme` | 1.12 |
| Out-of-scope record (REST status code, re-parenting, hooks, batch size, media trash) | 11 |
