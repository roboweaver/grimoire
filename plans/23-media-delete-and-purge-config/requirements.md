# Requirements Document
M23 — Media delete file cleanup, theme-mod cleanup, and a configurable purge cadence

## Introduction

`docs/compatibility.md` "M20 — Delete gap closure (PR 2)" lists three open gaps against
`wp_delete_attachment`: theme mods that reference a deleted attachment are not cleared, the
intermediate image-size files and the original file on disk are not removed, and the on-disk
unlink runs outside the delete transaction. Separately, "M22 — Trash and restore (PR 3)" records
that the automatic trash purge runs at a fixed, non-configurable one-hour cadence. M23 closes all
three.

**Corrections to the brief, from reading WordPress core (trunk, `wp-includes/theme.php`,
`wp-includes/post.php`, `wp-admin/includes/class-wp-site-icon.php`) and grimoire at `main`
`ed3a0aa`.**

| # | Brief said | WordPress and grimoire at `ed3a0aa` |
|---|---|---|
| 1 | "`_delete_attachment_theme_mod()` ... decide which postmeta keys it checks." | It checks no postmeta key. It compares `wp_get_attachment_url( $id )` against `get_header_image()` and `get_background_image()` (both resolve through theme mods, not a postmeta flag), and compares the attachment id itself against `(int) get_theme_mod( 'custom_logo' )` and `(int) get_option( 'site_logo' )`. The postmeta keys `_wp_attachment_is_custom_header` / `_wp_attachment_is_custom_background` exist in WordPress but are written by the Customizer's cropping flow for *candidate* header/background images, not read by `_delete_attachment_theme_mod()`; they do not gate this cleanup and grimoire does not need them. |
| 2 | "I believe custom_logo/site_icon are NOT cleaned by core." | `custom_logo` **is** cleaned by `_delete_attachment_theme_mod()` (since WordPress 4.5), which also removes the `header_text` theme mod in the same branch. `site_icon` is cleaned by a **separate** hook, `WP_Site_Icon::delete_attachment_data()`, which removes option `site_icon` (not a theme mod) by exact id match. `site_logo` (the site-logo block's option, since 6.6) is a fourth, independent target the brief did not name; it is small enough to include for completeness (Requirement 1.5). |
| 3 | "Decide scope carefully." | Scope is bounded to exactly core's five targets: `header_image`/`header_image_data` (URL match), `background_image` (URL match), `custom_logo`/`header_text` (id match), `site_logo` option (id match), `site_icon` option (id match). No grimoire-only addition. |
| 4 | "`_thumbnail_id` is already cleared in M20 — confirm." | Confirmed. `wprepo.MediaRepo.Delete` (`internal/storage/wprepo/media.go`) deletes every `postmeta` row with `meta_key = '_thumbnail_id'` and `meta_value` equal to the attachment id, inside the same transaction, before M20 shipped. No work needed; recorded as already closed. |
| 5 | "REST media DELETE still requires force and answers 400 vs WordPress 501 — decide if in scope." | Confirmed divergent (`internal/web/rest_media.go`): grimoire answers `400 rest_trash_not_supported`; WordPress answers `501 rest_trash_not_supported` because `wp_delete_attachment` trashes before deleting and the REST attachments controller computes `supports_trash` from `EMPTY_TRASH_DAYS && MEDIA_TRASH`. Changing the status code is an unrelated, narrow REST-layer fix with no shared dependency on the two gaps above; out of scope for M23, recorded as a gap (Requirement 11). |
| 6 | "Which image-size-related keys does `wp_delete_attachment_files` remove, beyond sizes." | `meta['sizes']` (each variant's `file`), `meta['thumb']` (guarded against removing a thumb another attachment's metadata still references), `meta['original_image']`, `backup_sizes` postmeta entries, and, in current trunk, `meta['source_image']` and `meta['animated_video']`/`meta['animated_video_poster']` (sideloaded HEIC/GIF-to-video companions). All resolved relative to `dirname($file)` and deleted only via `wp_delete_file_from_directory`, which requires the resolved real path to sit inside the resolved real uploads directory (after `realpath`), else it is a no-op. grimoire has no "thumb" or animated-video/source-image sideload feature yet, so those keys will simply never appear in `_wp_attachment_metadata`; the cleanup code still SHOULD handle them (forward-compatible, and the WordPress-written data a shared database may already contain) but no test can exercise a grimoire-created example of them (Requirement 4.6, 4.7). |
| 7 | "Decide ordering relative to the transaction." | Core deletes the database rows first (`wp_delete_attachment`), then calls `wp_delete_attachment_files` after the row is gone, and ignores its boolean return value in the one call site that matters (`wp_delete_attachment` itself discards it). grimoire already deletes DB-first then best-effort unlinks (`content.MediaWriteService.Delete`, main file only) for the same reason recorded in that method's comment: a stranded live row pointing at absent bytes is worse than a committed delete with a leftover file. M23 keeps that ordering and extends it to every size/variant file, still after the DB transaction commits, still best-effort (Requirement 4, 5). |
| 8 | "Purge cadence configurable — decide name/type, minimum, 0 meaning." | Named `scheduler.trash_purge_interval_seconds`, an integer scalar in seconds (consistent with the sibling `scheduler.interval_seconds`, not a Go duration string — matching the existing config package's style, which has no duration-string field anywhere). Default keeps today's behavior (3600 = one hour). There is no "0 means off": purge being enabled/disabled is already owned by `content.trash_days` (`0` disables trash and the purge entirely); the cadence config only affects *how often* an enabled purge scans. A value below `scheduler.interval_seconds` is accepted (the purge simply uns at the ticker's granularity, at most once per configured interval, exactly as the fixed `purgeInterval` constant works today for ticks finer than an hour), but a value of `0` or negative is rejected at load, naming the key, because "purge on every tick with no spacing" is a meaningful behavior change the brief did not ask for and more cheaply achieved by setting the interval equal to `scheduler.interval_seconds`. |

M23 ships as three independent pull requests, each a complete working increment:

- **PR 1, purge cadence config:** one new config key, `scheduler.trash_purge_interval_seconds`,
  threaded into the scheduler in place of the fixed `time.Hour` constant. No storage change.
  Requirements 7-10.
- **PR 2, media file cleanup on delete:** `MediaWriteService.Delete` removes every file named in
  the attachment's `_wp_attachment_metadata` and `_wp_attachment_backup_sizes` postmeta, in
  addition to the main file, all guarded to the configured uploads directory. Requirements 2-6.
- **PR 3, theme-mod and option cleanup on delete:** one new storage port implemented by
  `OptionRepo`, called from `MediaWriteService.Delete`, that removes `custom_logo`/`header_text`,
  `header_image`/`header_image_data`, `background_image` theme mods and `site_logo`/`site_icon`
  options when the deleted attachment is the one they reference. Requirement 1.

PR 2 and PR 3 both touch `MediaWriteService.Delete`; PR 3 is sequenced after PR 2 to avoid two
concurrent edits to the same method, but neither depends on the other's config or schema, so
either could ship alone if reordered.

## Glossary

- **Attachment**: a `{prefix}posts` row with `post_type = 'attachment'`.
- **Media_Write_Service**: `content.MediaWriteService`, specifically its `Delete` method.
- **Uploads_Dir**: `media.uploads_dir` (`config.MediaConfig.UploadsDir`), the directory
  `content.MediaService.Config().UploadsDir` resolves relative or absolute file paths against.
- **Attachment_Metadata**: the attachment's `_wp_attachment_metadata` postmeta value, PHP-serialized,
  decoded today by `php.Unserialize` into `map[string]any` (as `content.sizesFromMetadata` and
  `content.mediaDetailsFromSerialized` already do).
- **Backup_Sizes**: the attachment's `_wp_attachment_backup_sizes` postmeta value, PHP-serialized,
  an associative array of size name to `{file, width, height, ...}`, written by WordPress's image
  editor when it overwrites an original (grimoire does not write this key itself but must read one
  written by WordPress on a shared database).
- **Theme_Mod_Option**: the site option named `theme_mods_{stylesheet}`, where `{stylesheet}` is the
  value of option `stylesheet` (grimoire's `cfg.Theme`, as `wprepo.NavMenuRepo.MenuByLocation` already
  reads it), a PHP-serialized associative array.
- **Theme_Mod_Cleaner**: the new storage port that performs Requirement 1's cleanup in one
  transaction, implemented by `wprepo.OptionRepo`.
- **Purge_Interval**: the minimum spacing between automatic trash-purge scans in
  `internal/scheduler.Scheduler`, today the unexported constant `purgeInterval = time.Hour`.
- **Scheduler_Tick_Interval**: `scheduler.interval_seconds` (`config.SchedulerConfig.IntervalSeconds`),
  the ticker period the Scheduler already polls for due scheduled posts on.
- **Config_Loader**: `internal/config`.

## Requirements

### Requirement 1: Deleting an attachment clears the theme mods and options that reference it

**User Story:** As a site owner, I want deleting the image used as my site's logo, header,
background or site icon to also clear that setting, so that the admin and front end do not keep
pointing at a missing attachment.

#### Acceptance Criteria

1. WHEN the Media_Write_Service deletes an attachment whose id equals the numeric value of the
   `custom_logo` key in the Theme_Mod_Option, THE Theme_Mod_Cleaner SHALL remove the `custom_logo`
   and `header_text` keys from the Theme_Mod_Option, in the same call.
2. WHEN the Media_Write_Service deletes an attachment whose attachment URL equals the `header_image`
   key of the Theme_Mod_Option, THE Theme_Mod_Cleaner SHALL remove the `header_image` and
   `header_image_data` keys from the Theme_Mod_Option.
3. WHEN the Media_Write_Service deletes an attachment whose attachment URL equals the
   `background_image` key of the Theme_Mod_Option, THE Theme_Mod_Cleaner SHALL remove the
   `background_image` key from the Theme_Mod_Option.
4. WHEN removing a key from the Theme_Mod_Option under Requirement 1.1, 1.2 or 1.3 leaves the
   Theme_Mod_Option with no remaining keys, THE Theme_Mod_Cleaner SHALL delete the
   `theme_mods_{stylesheet}` option row entirely rather than writing back an empty array.
5. WHEN the Media_Write_Service deletes an attachment whose id equals the numeric value of site
   option `site_logo`, THE Theme_Mod_Cleaner SHALL delete the `site_logo` option.
6. WHEN the Media_Write_Service deletes an attachment whose id equals the numeric value of site
   option `site_icon`, THE Theme_Mod_Cleaner SHALL delete the `site_icon` option.
7. IF none of the conditions in 1.1, 1.2, 1.3, 1.5 or 1.6 holds for the deleted attachment, THEN THE
   Theme_Mod_Cleaner SHALL leave every theme mod and option unchanged.
8. IF the `theme_mods_{stylesheet}` option is absent, unset, or not a PHP-serialized associative
   array, THEN THE Theme_Mod_Cleaner SHALL treat Requirements 1.1-1.4 as not applicable and SHALL
   change no theme mod, without returning an error.
9. IF the `site_logo` or `site_icon` option is absent or not a parsable integer, THEN THE
   Theme_Mod_Cleaner SHALL treat the corresponding sub-requirement (1.5 or 1.6) as not applicable
   and SHALL change nothing for that option, without returning an error.
10. THE Theme_Mod_Cleaner SHALL perform every change made under Requirements 1.1-1.6 for one
    deleted attachment in a single transaction that commits only after the attachment's `posts` row
    delete (the existing `wprepo.MediaRepo.Delete` transaction) has committed, so a failure here
    never blocks or rolls back the row delete that Requirement 2 of the M20 PR 2 design already
    guarantees.
11. IF the Theme_Mod_Cleaner's transaction fails for a reason other than the attachment not matching
    any of Requirements 1.1-1.6, THEN THE Media_Write_Service SHALL log the failure and SHALL still
    return the deleted attachment to its caller as it does today, because the row delete has already
    committed and is not reversible from this step.
12. THE Theme_Mod_Cleaner SHALL compute the attachment URL used in Requirements 1.2 and 1.3 the same
    way the already-deleted attachment's stored `_wp_attached_file` and the configured uploads base
    URL would have produced it before the delete, so the comparison matches what
    `wp_get_attachment_url` would have returned in WordPress.

### Requirement 2: Deleting an attachment removes its intermediate size files from disk

**User Story:** As a site operator, I want deleting an image to also remove its resized copies from
disk, so that deleted media does not leave behind orphaned files that consume storage indefinitely.

#### Acceptance Criteria

1. WHEN the Media_Write_Service deletes an attachment whose Attachment_Metadata decodes to a map
   containing a `sizes` sub-map, THE Media_Write_Service SHALL attempt to delete, for every entry of
   that sub-map that has a non-empty `file` value, the file at that entry's name in the same
   directory as the attachment's main file.
2. WHEN the Media_Write_Service deletes an attachment whose Attachment_Metadata contains a non-empty
   `original_image` value, THE Media_Write_Service SHALL attempt to delete the file at that name in
   the same directory as the attachment's main file.
3. WHEN the Media_Write_Service deletes an attachment whose Attachment_Metadata contains a non-empty
   `thumb` value, THE Media_Write_Service SHALL attempt to delete the file at that name in the same
   directory as the attachment's main file, UNLESS another attachment's `_wp_attachment_metadata`
   postmeta value also names that same file as its `thumb`, in which case THE Media_Write_Service
   SHALL leave that file in place.
4. WHEN the Media_Write_Service deletes an attachment whose Attachment_Metadata contains a non-empty
   `source_image` value, THE Media_Write_Service SHALL attempt to delete the file at that name in
   the same directory as the attachment's main file.
5. WHEN the Media_Write_Service deletes an attachment whose Attachment_Metadata contains a non-empty
   `animated_video` or `animated_video_poster` value, THE Media_Write_Service SHALL attempt to
   delete the file at that name, for each key present, in the same directory as the attachment's
   main file.
6. WHEN the Media_Write_Service deletes an attachment that has a Backup_Sizes value, THE
   Media_Write_Service SHALL attempt to delete, for every entry of that value that has a non-empty
   `file` value, the file at that name in the same directory as the attachment's main file.
7. THE Media_Write_Service SHALL attempt to delete the attachment's main file (its `_wp_attached_file`
   postmeta value resolved against Uploads_Dir) exactly as it does today, unaffected by Requirements
   2.1-2.6.
8. IF the Attachment_Metadata is empty, absent, or does not decode into a map, THEN THE
   Media_Write_Service SHALL perform only the main-file deletion of Requirement 2.7 and SHALL treat
   every other sub-requirement of Requirement 2 as not applicable, without returning an error.

### Requirement 3: File deletion stays inside the configured uploads directory

**User Story:** As a site operator, I want the file-cleanup logic to never touch a path outside the
configured uploads directory, even when a stored metadata value is malformed or hostile.

#### Acceptance Criteria

1. FOR EACH candidate file path computed under Requirement 2, THE Media_Write_Service SHALL resolve
   it to an absolute path and SHALL compare it against the resolved absolute Uploads_Dir using the
   same real-path containment check `internal/web/uploads.go`'s `resolveWithinRoot` already performs
   for serving uploaded files.
2. IF a candidate file path resolves outside the Uploads_Dir (including via `..` segments, an
   absolute path override, or a symlink planted inside Uploads_Dir that points outside it), THEN THE
   Media_Write_Service SHALL NOT attempt to delete that path and SHALL treat the attempt as skipped,
   not as an error.
3. THE Media_Write_Service SHALL perform the containment check of Requirement 3.1 independently for
   each candidate path, so one path escaping the Uploads_Dir SHALL NOT prevent the deletion of any
   other, in-bounds candidate path for the same attachment.

### Requirement 4: File deletion failures are non-fatal and ordered after the database delete

**User Story:** As a site operator, I want a missing or locked file to never block a media delete,
and I want the delete to never run file cleanup before the database rows are safely gone.

#### Acceptance Criteria

1. THE Media_Write_Service SHALL perform every file deletion of Requirement 2 only after the storage
   layer's `Delete` call (the `posts`/`postmeta`/relationships transaction) has returned without
   error, exactly as it orders the existing main-file unlink today.
2. IF a file named by Requirement 2 does not exist on disk, THEN THE Media_Write_Service SHALL treat
   that as success for that file and SHALL continue attempting the remaining files.
3. IF deleting a file named by Requirement 2 fails for a reason other than the file not existing
   (for example a permission error), THEN THE Media_Write_Service SHALL continue attempting the
   remaining files and SHALL NOT return an error from `Delete` on account of that failure, matching
   today's handling of the main file.
4. THE Media_Write_Service SHALL return the deleted attachment (the pre-delete snapshot) from
   `Delete` exactly as it does today, regardless of how many of the file deletions in Requirement 2
   succeeded.

### Requirement 5: Media file cleanup is proven with a temporary uploads directory

**User Story:** As a maintainer, I want the file-cleanup behavior proven against a real filesystem,
not only against decoded metadata values.

#### Acceptance Criteria

1. THE test suite SHALL cover `MediaWriteService.Delete` against a `t.TempDir()`-backed uploads
   directory with: an attachment with no metadata (only the main file is removed); an attachment
   with `sizes`, `original_image`, `thumb`, `source_image`, `animated_video` and
   `animated_video_poster` entries (every named file is removed); a `thumb` file shared with another
   attachment's metadata (the shared file survives); a Backup_Sizes value (its files are removed); a
   metadata value naming a file that does not exist on disk (no error); and a metadata value naming
   a path that escapes the uploads directory via `..` or an absolute path (the escape attempt is
   skipped and every other file for the same attachment is still removed).
2. THE test suite SHALL cover the case where the storage delete itself fails (for example a
   not-found attachment id): no file is deleted and `Delete` returns the storage error unchanged.

### Requirement 6: Media file cleanup requires no schema change

**User Story:** As a maintainer, I want this feature to add no migration, table or column.

#### Acceptance Criteria

1. THE file-cleanup feature of Requirements 2-5 SHALL add no database migration, table, column, or
   index, and SHALL add no method to `domain.MediaWriter` or any other existing storage port.
2. THE file-cleanup feature SHALL read the Attachment_Metadata and Backup_Sizes values the storage
   layer already exposes (`domain.PostMetaRepository.AttachmentMetadata` for the former; a new,
   equally narrow read for the latter, or an equivalent already-available read path) without adding
   a write-side port.

### Requirement 7: The trash-purge scan interval is configurable

**User Story:** As a site operator, I want to control how often the automatic trash purge scans for
rows to delete, instead of the fixed one-hour cadence.

#### Acceptance Criteria

1. THE Config_Loader SHALL accept an integer config key `scheduler.trash_purge_interval_seconds`.
2. WHEN `scheduler.trash_purge_interval_seconds` is absent from the config file and the environment,
   THE Config_Loader SHALL apply a default of `3600` (today's fixed one-hour cadence), so an
   existing deployment's observed behavior is unchanged.
3. THE environment variable `GRIMOIRE_SCHEDULER_TRASH_PURGE_INTERVAL_SECONDS` SHALL override the file
   value when it parses as an integer, and SHALL be silently ignored (leaving the file value or
   default in effect) when it does not parse as an integer, matching the existing
   `GRIMOIRE_SCHEDULER_INTERVAL_SECONDS` and `GRIMOIRE_CONTENT_TRASH_DAYS` precedent in
   `internal/config/config.go`.
4. IF `scheduler.trash_purge_interval_seconds` in the config file is zero, negative, or not an
   integer scalar (a float, a non-numeric string, or a quoted numeric string), THEN THE Config_Loader
   SHALL fail startup with an error naming `scheduler.trash_purge_interval_seconds`.
5. IF the environment variable's value is zero or negative after it parses as an integer, THEN THE
   Config_Loader SHALL fail startup with an error naming `scheduler.trash_purge_interval_seconds`,
   matching Requirement 7.4's rejection for the same reason, checked after the override is applied.

### Requirement 8: The configured purge interval governs the scheduler's purge cadence

**User Story:** As a site operator, I want the configured interval to actually change how often the
scheduler scans for expired trashed rows.

#### Acceptance Criteria

1. THE Scheduler SHALL accept the configured Purge_Interval through its existing `WithTrashPurge`
   option (or a new, additive option alongside it) rather than using the unexported `time.Hour`
   constant it uses today.
2. WHEN the Scheduler's tick fires and less than the configured Purge_Interval has elapsed since the
   last purge scan, THE Scheduler SHALL skip the purge scan on that tick, exactly as it does today
   with the fixed one-hour constant.
3. WHEN the Scheduler starts (no prior purge scan recorded) and a tick fires, THE Scheduler SHALL
   run the purge scan on that first tick regardless of the configured Purge_Interval, matching
   today's "the first pass after startup purges at once" behavior.
4. WHERE the configured Purge_Interval is smaller than the Scheduler_Tick_Interval, THE Scheduler
   SHALL run the purge scan at most once per actual tick (it cannot scan more often than it ticks),
   which is an existing, documented limitation this requirement does not change.
5. THE Scheduler SHALL continue to leave the purge scan fully disabled (neither this nor any other
   requirement enables it) when `content.trash_days` resolves to `0` or `WithTrashPurge` is not
   wired, independent of the configured Purge_Interval.

### Requirement 9: Purge cadence configuration is wired through the real binary

**User Story:** As a maintainer, I want proof that the configured interval reaches the running
scheduler, not just a config struct.

#### Acceptance Criteria

1. THE system SHALL have an automated test that constructs the scheduler through `cmd/grimoire`'s
   `buildScheduler` with a non-default `scheduler.trash_purge_interval_seconds` value and asserts the
   resulting `Scheduler`'s purge does not run again before that configured interval has elapsed
   (using an injected clock, not real sleeps).
2. THE task implementing Requirement 9.1 SHALL record that removing the wiring edit in
   `cmd/grimoire/assembly.go` (passing the configured value into `buildScheduler`) makes that test
   fail for the stated reason (the purge runs on the fixed one-hour cadence instead of the
   configured one, or a compile failure if the option is removed outright).

### Requirement 10: Documentation reflects the configurable cadence

**User Story:** As an operator reading the shipped documentation, I want to know the cadence is
configurable and how to set it.

#### Acceptance Criteria

1. `docs/compatibility.md`'s M22 PR 3 entry and its "Known gaps" list SHALL be updated to say the
   purge cadence is configurable via `scheduler.trash_purge_interval_seconds` (default 3600,
   override `GRIMOIRE_SCHEDULER_TRASH_PURGE_INTERVAL_SECONDS`), SHALL no longer say the cadence is "a
   fixed constant, not configurable", and SHALL record the batch size (100) as still fixed.
2. EACH shipped `configs/grimoire.*.yaml` (sqlite, mysql, postgres, podman) SHALL carry a commented
   `scheduler.trash_purge_interval_seconds` example near the existing `scheduler.interval_seconds`
   and `content.trash_days` examples, with a comment explaining the default and the override
   variable.
3. `plans/post-m10-gap-analysis/README.md` and `plans/README.md`'s row 22 summary SHALL no longer
   list "the purge cadence ... not configurable" as an open gap once this requirement's PR ships,
   and SHALL instead note it was closed in M23.

### Requirement 11: Out-of-scope items are recorded, not implemented

**User Story:** As a maintainer, I want the items adjacent to this work that are deliberately not
being changed written down, so a future reader does not mistake silence for an oversight.

#### Acceptance Criteria

1. THE design and the final documentation updates SHALL record, as explicitly out of scope for M23:
   the REST media `DELETE` status-code divergence (`400` vs WordPress's `501`) for a request without
   `force=true`; re-pointing child attachments' `post_parent` away from a deleted parent attachment;
   WordPress hooks and the object cache; the purge batch size (100, still fixed); and trash for media
   (media deletion remains immediate, `force=true` required, as M20 and M22 left it).
