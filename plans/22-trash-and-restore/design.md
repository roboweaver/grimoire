# M22 — Trash and restore: Design

## Overview

Every grimoire delete of a post, page or registered-type row is permanent today
(`PostWriteService.Delete`, M18 PR 2 sequence extended by M19 and M20). M22 adds a trash that a live
WordPress on the same database reads natively, a restore, and an auto-purge, and keeps the M18-M20
sequence as the permanent delete used by `?force=true`, Empty Trash and the purge. It adds one storage
port in PR 1 and one read port in PR 3, no schema, no migration and no capability name. The risk is in
the exact rows written and in capability evaluation, so this document starts with what the WordPress
source and the code at `main` `25fd4f6` established. Where the brief and the evidence disagree, the
evidence wins and the disagreement is named.

**Sources** (WordPress `trunk`, read for this spec):
[`post.php`](https://github.com/WordPress/wordpress-develop/blob/trunk/src/wp-includes/post.php)
(`wp_trash_post`, `wp_untrash_post`, `wp_trash_post_comments`, `wp_untrash_post_comments`,
`wp_delete_post`, `wp_insert_post`, `wp_add_trashed_suffix_to_post_name_for_post`,
`wp_untrash_post_set_previous_status`, `_reset_front_page_settings_for_post`),
[`class-wp-rest-posts-controller.php`](https://github.com/WordPress/wordpress-develop/blob/trunk/src/wp-includes/rest-api/endpoints/class-wp-rest-posts-controller.php)
(`delete_item`, `sanitize_post_statuses`, `check_read_permission`),
[`capabilities.php`](https://github.com/WordPress/wordpress-develop/blob/trunk/src/wp-includes/capabilities.php)
(`map_meta_cap` for `edit_post` and `delete_post`),
[`functions.php`](https://github.com/WordPress/wordpress-develop/blob/trunk/src/wp-includes/functions.php)
(`wp_scheduled_delete`),
[`nav-menu.php`](https://github.com/WordPress/wordpress-develop/blob/trunk/src/wp-includes/nav-menu.php)
(`wp_setup_nav_menu_item`, `wp_get_nav_menu_items`),
[`taxonomy.php`](https://github.com/WordPress/wordpress-develop/blob/trunk/src/wp-includes/taxonomy.php)
(`_update_post_term_count`),
[`default-constants.php`](https://github.com/WordPress/wordpress-develop/blob/trunk/src/wp-includes/default-constants.php)
(`EMPTY_TRASH_DAYS` 30, `MEDIA_TRASH` false),
[`edit.php`](https://github.com/WordPress/wordpress-develop/blob/trunk/src/wp-admin/edit.php)
(the `untrash` action).

**1. What `wp_trash_post` writes, in order** (this is the representation grimoire must reproduce):

1. `unstick_post` (through the `wp_trash_post` action's `_reset_front_page_settings_for_post`).
2. `add_post_meta( id, '_wp_trash_meta_status', previous_status )`.
3. `add_post_meta( id, '_wp_trash_meta_time', time() )`.
4. `wp_update_post( id, post_status = 'trash' )`. Inside `wp_insert_post`, a transition into `trash`
   calls `wp_add_trashed_suffix_to_post_name_for_post`: if `post_name` does not already end in
   `__trashed`, it adds `_wp_desired_post_slug` meta (the old name) and sets
   `post_name = wp_truncate_slug( name, 191 ) . '__trashed'`. `post_modified` and
   `post_modified_gmt` move to now.
5. `wp_trash_post_comments`: when the post has comments, `add_post_meta( id,
   '_wp_trash_meta_comments_status', array( comment_ID => comment_approved ) )` and
   `UPDATE comments SET comment_approved = 'post-trashed' WHERE comment_post_ID = id`.

`wp_trash_post` does not touch term relationships, revisions, children, attachments or menu items.
The term count falls through the status transition (`_update_post_term_count` counts `publish` only).

**2. Restore is not what the brief described.** `wp_untrash_post` sets the status to `draft`
(`attachment` to `inherit`) through the `wp_untrash_post_status` filter. Only the posts list's "Undo"
link, right after a trash, adds `wp_untrash_post_set_previous_status` and so restores the status in
`_wp_trash_meta_status`. `wp_untrash_post` deletes `_wp_trash_meta_status` and `_wp_trash_meta_time`,
calls `wp_update_post` (which restores the slug from `_wp_desired_post_slug`, deletes that meta, and
makes the name unique) and calls `wp_untrash_post_comments`, which restores each comment from the
serialized array (a recorded `post-trashed` becomes `0`) and deletes the array. The REST update route,
by contrast, calls only `wp_update_post`, so a REST restore restores the slug but leaves
`_wp_trash_meta_*` and the `post-trashed` comments behind. Core copes with the stale meta:
`wp_scheduled_delete` deletes the meta of any row that is no longer in `trash` instead of deleting the
row. grimoire's restore runs the full untrash steps (Requirement 2, D5) because a grimoire restore
that left `post-trashed` comments would hide them for good.

**3. REST `DELETE`.** `delete_item` computes `supports_trash = EMPTY_TRASH_DAYS > 0` (and, for
attachments, `&& MEDIA_TRASH`). Order: `404 rest_post_invalid_id`; `403 rest_user_cannot_delete_post`
(the route's permission check answers `rest_cannot_delete` first); then, with `force`, a permanent
delete and `{deleted: true, previous: <object>}`; without `force`, `501 rest_trash_not_supported`
when trash is unsupported, `410 rest_already_trashed` when the status is already `trash`, else
`wp_trash_post` and the post object as `GET` returns it (the controller sets `context=edit` before
building it). The "post and page only" rule the brief mentions lives in `wp_delete_post( $id, false )`
and does not apply: the REST route trashes any type, so registered types trash by default.

**4. Capabilities.** `check_delete_permission` (`delete_post`) gates trash and force alike. For a
trashed row `map_meta_cap` evaluates `edit_post` and `delete_post` against the status the row had
before trashing: it reads `_wp_trash_meta_status` and, for `publish` or `future`, requires
`edit_published_posts` or `delete_published_posts` (author) instead of the plain capability. Without
this a Contributor could restore or delete a formerly published row. grimoire reproduces the rule
with the Effective status (Requirement 6.2). The admin list's Restore link checks `delete_post`, the
REST restore (an update) checks `edit_post`; grimoire has one restore path and uses the edit check
(sign-off 4).

**5. Reads.** Every public read in grimoire already filters `post_status = 'publish'`
(`wprepo/repo.go`), so a trashed row is invisible to the public routes without a code change; M22 pins
that with tests. The leaks are elsewhere: the admin list and `CountByStatus(page, "")` take an empty
status to mean every status, which includes `trash`; the public menu resolves a `post_type` item with
no status check (`menus.go resolveMenuTarget`) and would link to the suffixed name; term counts are
raw relationship counts; the admin comment list's empty status filter would show `post-trashed`.
Each gets an explicit treatment below. grimoire has no public feed, sitemap or search route today.

**6. Auto-purge.** `wp_scheduled_delete` (daily wp-cron) selects postmeta rows `_wp_trash_meta_time <
now - EMPTY_TRASH_DAYS * DAY_IN_SECONDS`; for each, a row that is not `trash` has its trash meta
deleted, otherwise `wp_delete_post( $id )` (permanent because the row is already trashed). It never
purges a trashed row that has no `_wp_trash_meta_time`, and it purges every post type, including
attachments trashed by a site with `MEDIA_TRASH` true.

**7. Shared-database consequences.** A grimoire that purges unmanaged types would delete rows
WordPress plugins own (WooCommerce orders, for example). The purge is therefore restricted to managed
types in SQL (the lister takes the type list) and checked again by the M18 gate in `Delete`. Rows that
WordPress wrote are restorable by grimoire and the reverse, because the representation is identical;
M22 adds a read-only real-database check of that claim (Requirement 3.8, 27).

**Design in one paragraph.** Add `domain.PostTrasher` (`TrashPost`, `RestorePost`, `TrashInfo`) on
`PostRepo`, each call one transaction writing the rows of finding 1 and 2, including comments and term
counts. Make the term count one shared statement that excludes trashed objects. Give
`PostWriteService` `Trash` and `Restore`, route `Update`'s status transitions through them, evaluate
capabilities against the Effective status, and keep `Delete` as the permanent sequence. REST
`DELETE` and the admin `DELETE` call `Trash` unless `force`. Hide trash from admin "all" lists and
counts in storage, add the `status` collection parameter, drop trashed menu targets from the public
menu only, and extend the admin API and SPA (PR 2). Add `content.trash_days`, a paged lister of trashed
rows and an hourly purge in the Scheduler that calls `Delete` (PR 3).

## Architecture

```mermaid
flowchart LR
    subgraph web [internal/web]
        RD[REST DELETE posts/pages/types<br/>force? Delete : Trash]
        RU[REST PUT/PATCH<br/>status transitions]
        RG[REST GET single + collection ?status]
        AD[Admin DELETE ?force<br/>restore + empty-trash routes PR 2]
        AL[Admin list/count<br/>status=trash]
    end
    subgraph content [internal/content]
        PW[PostWriteService<br/>Trash, Restore, Update, Delete]
        NM[NavMenuService<br/>public menus drop trashed targets]
    end
    subgraph domain [internal/domain]
        PT[PostTrasher NEW PR 1]
        TL[TrashedPostLister NEW PR 3]
        ER[ErrAlreadyTrashed, ErrNotTrashed NEW]
    end
    subgraph wprepo [internal/storage/wprepo]
        TR[PostRepo.TrashPost / RestorePost / TrashInfo]
        CT[countSQL shared: excludes trashed objects]
        CR[CommentRepo: empty filter excludes post-trashed]
        LS[PostRepo.TrashedPosts PR 3]
    end
    subgraph sched [internal/scheduler]
        SP[hourly purge PR 3]
    end
    RD --> PW
    RU --> PW
    AD --> PW
    PW --> PT
    PT --> TR
    TR --> CT
    RG --> AL
    NM --> TR
    SP --> TL
    TL --> LS
    SP --> PW
    ASM[cmd/grimoire/assembly.go] -.WithPostTrasher, WithTrashDays, scheduler options.-> PW
```

**Layering is unchanged.** `content` and `web` depend on `domain` ports; the SQL lives in `wprepo`.
The Scheduler depends on narrow interfaces it declares itself, as it does for publishing.

### Trash sequence

```mermaid
sequenceDiagram
    participant H as DELETE handler
    participant PW as PostWriteService.Trash
    participant OP as unstick (type post)
    participant TS as PostTrasher.TrashPost
    H->>PW: Trash(actor, id)
    PW->>PW: ByID, WritableType
    PW->>PW: CanDeletePostSuffix(effective status)
    PW->>PW: trash enabled? already trashed?
    PW->>OP: unstick(id)
    PW->>TS: TrashPost(id, now)
    Note over TS: one tx: meta, post_status + post_name,<br/>comments + comment meta, recount terms
    TS-->>PW: previous status
    H->>H: re-read, map, 200
```

### Restore sequence

```mermaid
sequenceDiagram
    participant H as PUT/PATCH or POST /restore
    participant PW as PostWriteService
    participant RS as PostTrasher.RestorePost
    H->>PW: Update(status != trash) or Restore(id, status)
    PW->>PW: ByID, WritableType, CanEditPostSuffix(effective status)
    PW->>PW: snapshot revision, sanitize and apply field changes (status stays trash)
    PW->>PW: CanEditPostSuffix(new status) when it is publish
    PW->>RS: RestorePost(id, status, slug rule)
    Note over RS: one tx: status, slug restore + uniqueness,<br/>delete trash meta, restore comments, recount terms
```

Field changes are written first, with the status still `trash`; if the restore then fails the row is
still safely trashed with the edits applied, and a retry converges.

## Storage (Requirements 1-5, 21)

### Port

```go
// internal/domain/errors.go (additive)
var (
    ErrAlreadyTrashed = errors.New("grimoire: post is already in the trash")
    ErrNotTrashed     = errors.New("grimoire: post is not in the trash")
)

// internal/domain/repository.go (additive)

// TrashInfo is what WordPress recorded when it trashed a row. Each Has flag reports whether the
// corresponding meta row exists.
type TrashInfo struct {
    PreviousStatus    string
    HasPreviousStatus bool
    TrashedAt         time.Time
    HasTrashedAt      bool
    DesiredSlug       string
    HasDesiredSlug    bool
}

// RestoreRequest parameterizes PostTrasher.RestorePost.
type RestoreRequest struct {
    Status            string    // new post_status; never "trash"
    Slug              string    // explicit post_name; "" restores the stored desired name
    HierarchicalScope bool      // uniqueness is scoped to post_parent (pages, hierarchical types)
    At                time.Time // post_modified for the restore
}

// PostTrasher moves a row to and from the trash in the representation WordPress writes
// (wp_trash_post, wp_untrash_post, wp_trash_post_comments, wp_untrash_post_comments), so a
// WordPress sharing the database reads the result natively. Each method is one transaction.
// It never deletes a row, a revision, a term, a menu item or a child.
type PostTrasher interface {
    TrashPost(ctx context.Context, id int64, at time.Time) (previousStatus string, err error)
    RestorePost(ctx context.Context, id int64, req RestoreRequest) error
    TrashInfo(ctx context.Context, id int64) (TrashInfo, error)
}
```

`wprepo.PostRepo` implements it in `internal/storage/wprepo/posttrash.go`. `storage.Set`
gains `PostTrash domain.PostTrasher` beside `PostRelationships` (the `Set` struct in
`internal/storage/factory.go`, set to the `posts` repo in the same place), and `wprepo/writers.go` gains
the compile-time assertion.

### `TrashPost` statements

All statements use the repo prefix, `rebind.Ident(vendor, "ID")` for the key and `bun.Ident` for the
mixed-case comment columns, as `PurgePostDependents` does. In one `RunInTx`:

1. `SELECT post_status, post_name FROM {p}posts WHERE "ID" = ?`. No row: `ErrNotFound`. Status
   `trash`: `ErrAlreadyTrashed`.
2. `DELETE FROM {p}postmeta WHERE post_id = ? AND meta_key IN ('_wp_trash_meta_status',
   '_wp_trash_meta_time')`. WordPress's `add_post_meta` would append a second row beside a stale one
   and `get_post_meta( ..., true )` would read the stale first row; its comment trash path deletes
   before adding (`comment.php`), so grimoire does the same for posts.
3. `INSERT INTO {p}postmeta (post_id, meta_key, meta_value)` for `_wp_trash_meta_status` (the status)
   and `_wp_trash_meta_time` (`strconv.FormatInt(at.Unix(), 10)`).
4. When `post_name` does not end in `__trashed`: `INSERT` `_wp_desired_post_slug` with the old name,
   then compute `newName = trashedName(name)`, which is `truncateSlug(name, 191) + "__trashed"`. `truncateSlug` cuts at 191 bytes
   and, if the cut falls inside a `%XX` escape (the last 1 or 2 bytes after a `%`), cuts before the
   `%`, which is what `wp_truncate_slug` does. An empty name becomes `__trashed`, as in WordPress.
5. `UPDATE {p}posts SET post_status = 'trash', post_name = ?, post_modified = ?, post_modified_gmt = ?
   WHERE "ID" = ? AND post_status <> 'trash'`. Zero rows: roll back with `ErrAlreadyTrashed` (a
   concurrent trash won). `post_modified` is `formatTS(at)` and `post_modified_gmt` is
   `formatTS(at.UTC())`, the same helpers `PostRepo.Update` uses.
6. Comments: `SELECT "comment_ID", comment_approved FROM {p}comments WHERE "comment_post_ID" = ?`.
   When rows exist: `INSERT` postmeta `_wp_trash_meta_comments_status` with
   `php.Serialize(map[int]string{...})` (below), then `UPDATE {p}comments SET comment_approved =
   'post-trashed' WHERE "comment_post_ID" = ?`. `posts.comment_count` is left alone, as WordPress
   leaves it.
7. Counts: `SELECT DISTINCT tr.term_taxonomy_id` for the row, joined to `term_taxonomy` with
   `taxonomy NOT IN (domain.NonPostObjectTaxonomies)` (the M20 `purgeRelationshipsTx` select, factored
   out as `relatedTermTaxonomyIDsTx`), then `recountTx`.

The previous status is returned. The status inserted is the row's own `post_status`, never normalized.

### `RestorePost` statements

In one `RunInTx`:

1. Read `post_status, post_name, post_type, post_parent`. No row: `ErrNotFound`. Not `trash`:
   `ErrNotTrashed`.
2. Resolve the new name. `desired` is the first `_wp_desired_post_slug` row by `meta_id`. Name :=
   `req.Slug` if set, else `desired` if present, else `post_name` with a trailing `__trashed` removed.
   Then `uniqueName`: while `SELECT 1 FROM {p}posts WHERE post_name = ? AND post_type = ? AND "ID" <>
   ? AND post_status <> 'trash' [AND post_parent = ?] LIMIT 1` finds a row, retry with
   `base + "-" + n`, `n` from 2, where `base` is cut so the result stays within 200 bytes. The
   `post_parent` clause is added when `req.HierarchicalScope`. An empty name is stored empty (the
   service derives it from the title first, Requirement 2.6). `post_status <> 'trash'` is the live-rows
   rule of Requirement 6.11: an unsuffixed legacy trashed row never blocks a restore.
3. `DELETE` postmeta rows `_wp_trash_meta_status`, `_wp_trash_meta_time`, `_wp_desired_post_slug`.
4. `UPDATE {p}posts SET post_status = ?, post_name = ?, post_modified = ?, post_modified_gmt = ? WHERE
   "ID" = ? AND post_status = 'trash'`. Zero rows: roll back with `ErrNotTrashed`.
5. Comments: read `_wp_trash_meta_comments_status`. When present and it unserializes to a map, group
   ids by recorded status and run one `UPDATE {p}comments SET comment_approved = ? WHERE
   "comment_ID" IN (...)` per status (`post-trashed` recorded as `0`), chunking the id list at 500;
   ids that no longer exist match nothing. Then delete the meta. A value that does not unserialize is
   deleted and the post's `post-trashed` comments are set to `0`, the "confidence check" outcome in
   WordPress.
6. Recount the row's related `term_taxonomy_id`s.

`TrashInfo` is three reads of the first row by `meta_id` per key, plus `ErrNotFound` when the post row
is missing.

**Why the field changes are not in the store.** The store owns only what `wp_trash_post` and
`wp_untrash_post` own. Titles, content, dates and parent edits stay in `PostWriter.Update`, so no
second write path for those fields exists.

### Term count rule (Requirement 5)

M20's `recountTx` and `SetPostTerms` both run
`UPDATE term_taxonomy SET count = (SELECT COUNT(*) FROM term_relationships WHERE term_taxonomy_id = ?)`.
M22 replaces both with one helper in `wprepo/menuitemtx.go`'s neighbor `termcount.go`:

```sql
UPDATE {p}term_taxonomy SET count = (
  SELECT COUNT(*) FROM {p}term_relationships tr
   WHERE tr.term_taxonomy_id = ?
     AND NOT EXISTS (SELECT 1 FROM {p}posts tp
                      WHERE tp."ID" = tr.object_id AND tp.post_status = 'trash')
) WHERE term_taxonomy_id = ?
```

The subquery reads `posts` while the statement writes `term_taxonomy`, so MySQL's same-table
restriction does not apply. A relationship whose object is not a post (link categories) matches no
`posts` row and is counted, as before (Requirement 5.4). `NOT EXISTS` on an indexed primary key costs
one probe per relationship. `recountTx` gains a `vendor` parameter for `rebind.Ident`; its callers in
`menuitemtx.go`, `postrelationships.go`, `media.go` and `postterms.go` pass it. The M19 and M20
contracts keep passing because none seeds a trashed object; task 2.1 adds one case to the shared
helper's own contract.

**Options considered.** (A) Leave counts alone: a trashed post keeps counting, so a category page
shows "5" over four visible posts. (B) WordPress's published-only rule everywhere: correct, but it
changes the M6 and M19-M20 contracts and every draft's effect on counts, a larger change than
trash needs. (C), chosen: the existing relationship count minus trashed objects, one rule in one
place. The remaining gap (drafts count, WordPress does not) predates M22 and stays documented.

### Comment repository (Requirement 4.6)

`CommentRepo.List` and `Count` add `comment_approved <> 'post-trashed'` when `f.Statuses` is empty.
Handlers that name statuses are unchanged. `domain.CommentFilter` gains nothing. The comment contract
(`comments_contract.go`) gets a `post-trashed` row, so the rule is proven on three vendors.

### PHP serialization of the comment-status meta (Requirement 4.5)

`php.Serialize` supports `[]int`, `map[string]any` and `map[string]bool` only, and `serializeMap`
writes string keys (`s:2:"12"`), which WordPress would read as a different key. M22 adds a `map[int]string`
case that writes `a:N:{i:ID;s:LEN:"VALUE";...}` in ascending key order, which is what
`add_post_meta( ..., array( $id => $status ) )` produces for the integer keys of `$statuses`.
`php.Unserialize` already returns integer keys as decimal strings, so the round trip compares as
strings. WordPress stores `comment_approved` values as strings (`"1"`, `"0"`, `"spam"`), and so does
the new case.

### Lister (PR 3, Requirement 21)

```go
type TrashedPost struct {
    Post         Post
    TrashedAt    time.Time
    HasTrashedAt bool
}
type TrashedPostLister interface {
    TrashedPosts(ctx context.Context, types []string, afterID int64, limit int) ([]TrashedPost, error)
}
```

`SELECT {columns} FROM {p}posts WHERE post_status = 'trash' AND post_type IN (?) AND "ID" > ? ORDER BY
"ID" LIMIT ?`, then one `SELECT post_id, meta_value FROM {p}postmeta WHERE meta_key =
'_wp_trash_meta_time' AND post_id IN (?) ORDER BY meta_id` per page, first row per post, parsed with
`strconv.ParseInt` in Go. WordPress compares `meta_value < %d` in SQL; grimoire compares in Go because
`CAST(text AS BIGINT)` on a non-numeric value errors on PostgreSQL and the three vendors disagree on
the rest. Trash is small (bounded by Trash days), so the page-per-batch read is cheap.

## Service (Requirements 6, 11, 13)

```go
var (
    ErrTrashUnsupported = errors.New("content: trash is not enabled")
)
func WithPostTrasher(t domain.PostTrasher) PostWriteOption
func WithTrashDays(days int) PostWriteOption          // 0 disables; default set by NewPostWriteService is 0
func (s *PostWriteService) TrashEnabled() bool         // trasher != nil && days > 0
func (s *PostWriteService) Trash(ctx, actor, id int64) error
func (s *PostWriteService) Restore(ctx, actor, id int64, status string) error
```

`WithTrashDays` defaults to 0 inside the service so a service built without options (every existing
test) has trash off and `Delete` unchanged; `buildHandler` passes the configured value.

### Capability matrix

| Operation | Gate | Status passed to the gate |
|---|---|---|
| `Trash` | `CanDeletePostSuffix` | Effective status of the stored row (its `post_status`, never `trash` here) |
| `Delete` (force, Empty Trash, purge) | `CanDeletePostSuffix` | Effective status (`_wp_trash_meta_status` when trashed) |
| `Update`, same status | `CanEditPostSuffix` | Effective status |
| `Update`/`Restore` leaving the trash | `CanEditPostSuffix` | Effective status, then the new status (publish check) |
| `Update` entering the trash | `CanEditPostSuffix` then `CanDeletePostSuffix` | stored status |

The suffix comes from `CapabilitySuffixFor` (M17), so a registered type with `capability_type:
product` is gated by `edit_products` and `delete_products`, as for every other write. `effectiveStatus`
is a private helper: it returns `cur.Status` unless it is `trash`, in which case it asks the Trash_Store
for `TrashInfo` and returns the previous status, or `draft` when absent or when no store is wired.
`CanDeletePostSuffix` and `CanEditPostSuffix` test `status == "publish"` only, so `future` is treated
as a draft by the policy today. WordPress treats `future` like `publish` here. M22 does not widen the
policy (an M17 behavior outside this spec) and records it as a gap, so Effective status maps a
recorded `future` to `future` and the policy decides.

### `Trash`

1. `ByID` (`ErrNotFound` becomes `ErrForbidden`), `WritableType`, capability, as `Delete` does.
2. `TrashEnabled` false: `ErrTrashUnsupported`. Status `trash`: `domain.ErrAlreadyTrashed`.
3. Type `post` with the sticky ports: `unstick` (same function `Delete` uses). A failure returns
   before the store call and leaves the row untouched.
4. `TrashPost(ctx, id, time.Now())`.

No revision, dependent, menu or relationship step runs. A trashed page keeps its children's
`post_parent`, its menu items and its relationships, as in WordPress.

### `Update` transitions

After authorization (Effective status) and the optimistic-concurrency check, `Update` runs its existing
revision snapshot, sanitization and field merge unchanged, then decides:

| Stored status | Requested status | Action |
|---|---|---|
| not `trash` | not `trash` | `w.Update(cur)` as before |
| not `trash` | `trash` | `w.Update(cur)` with the status unchanged, then `Trash` (delete capability also required) |
| `trash` | `trash` | `w.Update(cur)`, status stays `trash` |
| `trash` | other | `w.Update(cur)` with the status still `trash`, then the restore below |

Restore slug rule. The REST merge base is the stored row, so a `PATCH` without `slug` arrives with
`p.Slug == cur.Slug` (the suffixed name). Then the service passes `RestoreRequest.Slug = ""` and the
store restores the desired name. A request that sends another slug passes it through the type's
existing rule (`resolveSlug` or `uniquePageSlug` for validation and the charset on registered types)
and the store applies only the uniqueness loop. For a row whose resolved name is empty the service
derives it with `Slugify( title )` first.

`w.Update(cur)` for a trashed row writes `post_name = cur.Slug` (the suffixed name) and so leaves the
trash rename untouched; `Modified` moves, as for any edit.

### `Restore`

`Restore(ctx, actor, id, status)` is `Update`'s restore branch without field changes: authorization on
the Effective status, `status` resolved (`""` is `draft`, `previous` is `TrashInfo.PreviousStatus` or
`draft`), the publish check for the new status, then `RestorePost`. It returns `domain.ErrNotTrashed`
for a live row. `Update` and `Restore` share one private `restoreRow` function.

### `Delete`

Unchanged except the capability line: `CanDeletePostSuffix(actor, suffix, effectiveStatus(cur),
cur.Author)`. The M18-M20 sequence, its option ports and its error handling are not edited. The trash
meta, `_wp_desired_post_slug` and `_wp_trash_meta_comments_status` go with `PurgePostDependents` step 5
(all postmeta of the row), and the `post-trashed` comments go with steps 3-4 (every state). A test
pins both. `PurgeTermRelationshipsOf` recounts with the shared rule, so counts after a purge exclude
the other trashed objects.

### Slug logic and the page graph (Requirements 6.11, 9.5)

`slugTaken` in `content/pagehier.go` skips nodes whose `Status == "trash"`; `loadPostNodes` keeps
returning them, because `PageHierarchy` (permalinks, ancestors, `postLink`) must see trashed
ancestors. This matters for one case: after trashing a page `x` (stored as `x__trashed`), a published
child `x/c` has the stored ancestor `x__trashed`, so its computed URL is `/x__trashed/c/`. WordPress
does the same (`get_page_uri` walks stored names), so the child stops resolving at `/x/c/` until the
parent is restored. The public resolver already matches stored names, so no resolver change is needed.
A test pins it.

## REST API (Requirements 7, 8)

### `DELETE` flow

`handleRESTPostDelete(typ)` keeps its CSRF handling, id parse, `ByID`, type check and `previous`
build. After them:

```
force (restForceDelete)        -> postWrite.Delete   -> 200 {deleted:true, previous}
otherwise                      -> postWrite.Trash    -> re-read, map, 200 <post object>
error mapping (both paths):
  content.ErrForbidden            -> 403 rest_cannot_delete  "Sorry, you are not allowed to delete this <typ>."
  content.ErrTrashUnsupported     -> 501 rest_trash_not_supported "The post does not support trashing. Set 'force=true' to delete."
  domain.ErrAlreadyTrashed        -> 410 rest_already_trashed "The post has already been deleted."
  other                           -> 500 rest_delete_failed (unchanged)
```

`postWrite` is the `postAdminWriter` interface. M22 adds a sibling interface `postTrashWriter` with
`Trash`, `Restore` and `TrashEnabled`, set by `WithPostTrash(w postTrashWriter)`, so the existing fakes
that implement `postAdminWriter` keep compiling. When `s.postTrash` is nil, a non-force `DELETE` answers
`501 rest_trash_not_supported` and deletes nothing (Requirement 14.2). The failure mode of an unwired
option is therefore a refused delete, never a silent permanent delete.

The forbidden code changes from `rest_cannot_edit` (M11, a placeholder because the M10 spec had no
delete code) to WordPress's `rest_cannot_delete`; the change is listed for sign-off and in the
migration note.

For a trashed row `TrashEnabled` is irrelevant: `force` deletes it, and a non-force `DELETE` answers
`410` when enabled and `501` when disabled, in WordPress's order (`supports_trash` first).

### `PUT` / `PATCH`

`parseRESTPostWrite` uses `validPostStatuses` (create and update share it). M22 adds
`validUpdateStatuses` (the same set plus `trash`) used by the two update parsers only, so `POST` with
`status: trash` stays `400 rest_invalid_param`-style invalid, and `PUT`/`PATCH` accept it. The merge
base keeps the stored status, so a sparse `PATCH {"title": "x"}` on a trashed row keeps it trashed
(Requirement 7.8). An update that leaves the trash needs `status` in the body, as in WordPress. The
response is the re-read object.

### `GET`

`handleRESTPostSingle` already returns a non-published row to a caller with the edit capability and
`404` to everyone else, so the single-read behavior (Requirement 8.1) needs a test, not code.

`handleRESTPostsCollection` gains `parseRESTStatuses(r, typ)`:

| Input | Result |
|---|---|
| absent or `publish` | `["publish"]` (today's behavior, byte-identical) |
| unknown token | `400 rest_invalid_param` ("Invalid parameter(s): status") |
| any token other than `publish`, caller lacks `canReadUnpublished(r, typ)` | `403 rest_forbidden_status` ("Status is forbidden.") |
| `any` | `publish, future, draft, pending, private` |
| `trash`, `draft`, ... | the listed statuses |

The filter reaches `ListForAdmin` and `CountForAdmin` as explicit `Statuses`, so `X-WP-Total` matches.
`writeRESTPostsBySlug` keeps its published-only `BySlug` lookup. Registered-type collections go through
the same function through `handleRESTPostsCollection(pt.Name)`.

The `link` of a trashed row is whatever `RESTMapper.postLink` produces from the stored name; WordPress's
`get_permalink` uses the pretty form for `trash` (only `draft`, `pending`, `auto-draft`, `future` fall
back to `?p=`), so no mapper change is needed. The golden diff pins it.

## Reads that must not show trash (Requirements 9, 10)

| Surface | Today | M22 |
|---|---|---|
| Public single, slug, `?p=`, preview, pages by path (`BySlug`, `ByID` public, page resolve) | `post_status = 'publish'` | Unchanged; test pins `404` for a trashed row of each kind |
| Home, archives, category, tag, author, date, `/page/n`, `?m=`, counts (`RecentPosts`, `PublishedArchive`, `CountPublishedArchive`) | `publish` only | Unchanged; test pins |
| Public search, feed, sitemap | not implemented | n/a, recorded |
| REST collections | `publish` only | `status` parameter (above) |
| REST single | `publish` or edit capability | Unchanged; test pins `200` for an editor and `404` for anonymous |
| Admin list (`ListForAdmin`) and count (`CountForAdmin`) | empty `Statuses` = every status | empty = every status **except** `trash`; explicit `["trash"]` returns trash |
| `CountByStatus` | empty status = every status | empty = every status except `trash` |
| Admin detail | any status | Unchanged |
| `AuthorsOfTypes` | any status | Unchanged (an author whose only rows are trashed still appears; recorded) |
| Page hierarchy, `PostNodeReader` | every status | Unchanged (Requirement 9.5) |
| Public menus | no status check | drop trashed `post_type` targets (below) |
| Admin menus | no status check | Unchanged (Requirement 9.3) |
| Comments (public list, REST, admin) | named statuses; admin and REST `any` = unfiltered | empty filter excludes `post-trashed` |
| M14 backfill, `PostTypeAuditor`, CLI content types | every status | Unchanged; trashed rows are still sanitized and counted |

`adminreads.go`'s `applyAdminStatuses(q, f)` is one new helper used by `ListForAdmin` and
`CountForAdmin`: explicit statuses, else `post_status <> 'trash'`. Its contract
(`admin_contract.go`) adds trashed rows and the explicit case.

### Public menus

`NavMenuItem` gains the additive field `TargetTrashed bool`. `NavMenuRepo.resolveMenuTarget` already
loads the target's `post_title` and `post_name`; it adds `post_status` to the same query and sets the
flag when it is `trash`. `NavMenuService.ByLocation` and `BySlug` (the public path; `handlers.go` uses
`ByLocation`) pass the result through `pruneInvalidItems`:

```go
// pruneInvalidItems removes items with TargetTrashed and re-parents each removed item's children to
// the nearest kept ancestor, so they stay visible as WordPress's Walker shows orphans.
func pruneInvalidItems(items []domain.NavMenuItem) []domain.NavMenuItem
```

The function works on the built tree (`Children`): it walks it, and for a removed node it splices the
node's pruned children into its parent's list at the node's position. `List`, `Get` and the admin routes
do not prune. Stored data is untouched, so restoring the row brings the item back in place. A test
covers top-level, nested, chained and all-removed menus.

## Admin API and SPA (PR 2: Requirements 15-19)

### Routes

| Route | Behavior |
|---|---|
| `DELETE /admin/api/posts/{id}` | no `force`: `Trash`, `204`. `?force=true`: `Delete`, `204`. Errors: `403`; `409 already_trashed`; `409 trash_unsupported`; `404`/`400` as before |
| `POST /admin/api/posts/{id}/restore` | body `{"status": ""|"previous"|<status>}`; `Restore`; `200` post detail; `400 bad_request` for a live row or invalid status; `403` |
| `POST /admin/api/posts/empty-trash` | body `{"type": ...}`; `200 {"deleted": n, "skipped": m, "more": bool}` |
| `GET /admin/api/posts?status=trash` | `invalid_status` list gains `trash` |
| `GET /admin/api/session` | gains `trashEnabled` |
| `PUT /admin/api/posts/{id}` | accepts `status: "trash"` through `validUpdateStatuses`; a trashed row leaves the trash through the same call |

`adminPostDelete` reads `force` with the existing `restForceDelete`. The restore and empty-trash routes
sit behind the same `edit_posts` floor, login and CSRF middleware as the other post writes, registered
where `adminroutes.go:99` registers them. `empty-trash` is registered before `/posts/{id}` routes; `POST`
on `/posts/{id}` does not exist, so there is no ambiguity, and a test asserts it.

`empty-trash` collects ids with `AdminService.List`-style paging (`ListForAdmin`, `Statuses:
["trash"]`, `OrderBy: "id"`, 100 per page, `Types: [type]`) up to 1000 ids, calls `Delete` per id,
counts `ErrForbidden` as `skipped` (the actor's capability or a vanished row; the two are
indistinguishable by design) and any other error as a stop with `500`. `more` is true when the type has
more trashed rows than the 1000 examined. The SPA repeats the call while `deleted > 0 && more`.

### SPA

Files: `web/admin/src/views/PostsList.tsx` (and its test), `PostEditor.tsx`, `PageEditor.tsx`,
`api/client.ts`, `api/types.ts`, `views/Dashboard.tsx` (no change needed: the count is server-side).

- Status picker gains `Trash`; `STATUS_VARIANT` gains `trash: "neutral"`.
- Row actions are chosen by the selected status: Trash view shows `Restore` and `Delete permanently`;
  every other view shows `Move to Trash` (or, when `session.trashEnabled` is false, `Delete`
  permanently with the destructive dialog). `Move to Trash` calls `api.deletePost(id)` and shows an
  inline notice "Moved to Trash. Undo"; `Undo` calls `api.restorePost(id, "previous")` and reloads.
  The notice is a `Flex` with `StatusLight` and an `ActionButton`, because the installed
  `@adobe/react-spectrum` `^3.47.0` toast queue would add a provider to `main.tsx` for one message.
- `Delete permanently` and `Empty Trash` use the existing destructive `AlertDialog`; the empty-state
  copy says "Trash is empty".
- `api.deletePost(id, {force})` appends `?force=true`; `api.restorePost(id, status?)` and
  `api.emptyTrash(type)` are new in `client.ts`; the `Session` type gains `trashEnabled`.
- Editors: a loaded post with `status === "trash"` renders a read-only notice with a Restore
  action and no Save (WordPress's "You can't edit this item because it is in the Trash").
- `internal/admin/dist` is rebuilt with `make admin` and committed with the source, as M8 did. If Node
  is unavailable the task records that the bundle was not rebuilt and the PR is not marked ready.

## Configuration and scheduler (PR 3: Requirements 20-23)

### Config

`ContentConfig` gains `TrashDays *int` (`yaml:"trash_days"`). Because `ContentConfig.UnmarshalYAML`
re-decodes with `KnownFields(true)`, the key is accepted and unknown keys still fail. A non-integer
scalar is caught before the decode with `content.trash_days must be a non-negative integer`, because
`yaml.v3`'s own message does not name the key. `applyDefaults` sets 30 when nil. `Validate` rejects a
negative value. `GRIMOIRE_CONTENT_TRASH_DAYS` is parsed in the env-override block next to
`GRIMOIRE_SCHEDULER_INTERVAL_SECONDS`; an unparsable value is ignored like its neighbors, so the file
value stays. The key lives under `content:` as the brief asked, which also keeps it out of the
`scheduler:` section, whose interval governs polling only.

**Why `0` means "trash disabled", not only "no purge".** WordPress has one constant,
`EMPTY_TRASH_DAYS`, and `0` disables trash everywhere (REST answers `501`, the admin shows "Delete
Permanently"). Two knobs for one concept would let a site configure a trash that never empties with
no signal. An operator who wants a long retention sets a large number of days. Sign-off 3 records the
alternative.

### Scheduler

```go
// internal/scheduler (additive)
type trashDeleter interface {
    Delete(ctx context.Context, actor auth.Principal, p domain.Post) error
}
func WithTrashPurge(l domain.TrashedPostLister, d trashDeleter, days int, types []string) Option
func WithDeleteCapabilitySuffixes(suffixes ...string) Option
func withClock(now func() time.Time) Option   // tests only
```

`tick` runs the publish loop as today, then `purgeTrash(ctx)` when `days > 0` and at least an hour has
passed since `lastPurge` (zero on start, so the first tick purges). `purgeTrash`:

```
cutoff := now - days*24h
after := 0
for { batch := lister.TrashedPosts(ctx, types, after, 100); if empty: break
      for each tp: after = tp.Post.ID
          if !tp.HasTrashedAt || !tp.TrashedAt.Before(cutoff): continue
          err := deleter.Delete(ctx, s.principal, domain.Post{ID: tp.Post.ID})
          ErrForbidden -> debug log (gone or unmanaged); other err -> error log, continue
          else purged++ }
log info "scheduler: purged trashed posts" count (only when > 0)
```

The scheduler principal gains `delete_`, `delete_others_`, `delete_published_` and `delete_private_`
for the suffixes `posts`, `pages` and each registered suffix, through `WithDeleteCapabilitySuffixes`.
(The publish capabilities use the same per-suffix mechanism; `pages` is added for delete only, so M22
does not change which rows the publish loop can publish.) `types` is the managed type list built in
`buildScheduler`: `post`, `page` and `reg.PostTypes()` names.

Failure isolation, idempotence and unmanaged safety:

- **Unmanaged types** are excluded by the `types` argument in SQL and again by `Delete`'s
  `WritableType` gate.
- **Rows without the meta** are never purged (finding 6).
- **Races.** A row deleted by another grimoire process or by WordPress between the list and the delete
  yields `ErrForbidden` (missing row), logged at debug. A row restored in between has a status other
  than `trash` but `Delete` has no such precondition: it would delete a live row. `purgeTrash`
  therefore re-reads the row (`TrashInfo` is not enough) through the deleter's `ByID`: the `trashDeleter`
  interface adds `ByID`, and the purge skips a row whose status is no longer `trash`. `PostWriteService`
  gains `ByID` as a pass-through to its writer.
- **Batch bound.** A run examines every trashed row of the managed types once (cursor by id), 100 at a
  time.

## Golden changes (Requirement 14, 19)

`TestBuiltinGolden` replays `builtinGoldenScript()` and compares to
`cmd/grimoire/testdata/builtin_golden/{without_registry,with_registry}.json`, 49 entries each, the same
script for both. Posts use `AUTOINCREMENT`, so ids are never reused and no entry after the changed ones
shifts.

**PR 1: exactly three existing entries change, in both files.**

| Entry (index) | Before | After |
|---|---|---|
| `rest post delete` (7) | `200`, `{"deleted":true,"previous":<post>}` | `200`, `<post>` with `status: "trash"`, `slug: <old slug>"__trashed"`, `link` ending `<old slug>__trashed/`, `modified`/`modified_gmt` `<ts>`; every other key equal to `previous` |
| `rest post get deleted` (8) | `404`, `rest_post_invalid_id` | `200`, byte-equal to the new `rest post delete` body (same actor, same row) |
| `rest page delete` (13) | `200`, `{"deleted":true,"previous":<page>}` | `200`, `<page>` with `status: "trash"` and the suffixed slug and link, every other key equal |

Every other existing entry is byte-identical. Admin `post delete` (34) and `page delete` (39) stay
`204` with no body in both PRs.

**PR 1: appended entries** (a pure addition after entry 48, so the existing indices do not move):
`rest trash create` (201), `rest trash delete` (200, trashed object), `rest trash delete again` (410),
`rest trash list default` (200, row absent), `rest trash list status trash` (200, one row),
`rest trash list anonymous` (anonymous `GET ?status=trash`, `403 rest_forbidden_status`),
`rest trash restore` (`PATCH {"status":"draft"}`, 200, slug without suffix),
`rest trash force delete` (`DELETE ?force=true`, 200 `deleted`), `rest trash gone` (404).

**PR 2: appended entries, plus one changed entry.** PR 1's appended entry `rest trash list status trash`
(index 53) changes: the script's admin `post delete` step (34) now trashes its row instead of deleting it
(still `204`, no body), so the list returns three trashed posts instead of two (`X-WP-Total` `3`). This
was not predicted when the design was written. The check script allows exactly that entry to differ in
mode `pr2`; every other entry that existed before PR 2 stays byte-identical. The nine appended entries
are: `admin trash create` (201), `admin trash delete` (204),
`admin trash get` (200, `status: "trash"`), `admin trash list` (`?status=trash&type=post`, one row),
`admin trash restore` (200, `draft`), `admin trash delete again` (204), `admin trash force` (204),
`admin trash gone` (404), `admin empty trash` (200 `{deleted, skipped, more}`).

**Verification, `scripts/check-golden-trash.sh <base-ref> <pr1|pr2>`** (precedent:
`scripts/check-golden-parent.sh`, M16/M19; needs `jq`). For each of the two files:

1. The first 49 entry names equal the base names in order.
2. Mode `pr2`: the base entries (the 58 of PR 1) equal the new ones (`jq -S` equality), except `rest trash
   list status trash`, which gains the admin-trashed post (see PR 2 above). Mode `pr1`: the same for
   every index except 7, 8, 13.
3. Mode `pr1`, index 7 and 13: `.status == 200`; `.body | has("deleted") | not`; `.body.status ==
   "trash"`; `.body.slug == base.body.previous.slug + "__trashed"`; and `del(.status, .slug, .link,
   .modified, .modified_gmt)` of the new body equals the same deletion applied to `base.body.previous`
   (so `_links`, `author`, `content`, `id`, `guid` and the rest are proven unchanged).
4. Mode `pr1`, index 8: `.status == 200` and `.body` equals the new body of index 7.
5. Appended entries: the names equal the list above, and each recorded status equals the expected
   status listed above.
6. `with_registry` and `without_registry` agree on the appended entries.

The golden regeneration (`go test ./cmd/grimoire -run TestBuiltinGolden -update`) is its own commit,
made after the behavior commits and before the PR opens, with the message naming the cause. The script
runs in the task and its output goes into the PR description. If it fails, the cause is fixed in code
or in the script's expectation with a written reason; the golden is not hand-edited.

## Components and Interfaces

| Package | Change | PR |
|---|---|---|
| `internal/domain/errors.go` | `ErrAlreadyTrashed`, `ErrNotTrashed` | 1 |
| `internal/domain/repository.go` | `TrashInfo`, `RestoreRequest`, `PostTrasher`; `AdminPostFilter` and `PostCounter` doc comments (empty status excludes trash); `NavMenuItem.TargetTrashed` in `entities.go` | 1 |
| `internal/php/serialize.go` | `map[int]string` case | 1 |
| `internal/storage/wprepo/posttrash.go` (new) | `TrashPost`, `RestorePost`, `TrashInfo`, `truncateSlug`, `uniqueName` | 1 |
| `internal/storage/wprepo/termcount.go` (new), `menuitemtx.go`, `postterms.go`, `postrelationships.go`, `media.go` | shared count statement; `recountTx` takes the vendor | 1 |
| `internal/storage/wprepo/adminreads.go` | `applyAdminStatuses`; `CountByStatus` empty status | 1 |
| `internal/storage/wprepo/comments.go` | `post-trashed` exclusion | 1 |
| `internal/storage/wprepo/menus.go` | `TargetTrashed` | 1 |
| `internal/storage/wprepo/writers.go`, `internal/storage/factory.go` | assertion; `PostTrash` on `Repositories` | 1 |
| `internal/content/writeservices.go` | `WithPostTrasher`, `WithTrashDays`, `Trash`, `Restore`, `TrashEnabled`, `Update` transitions, `effectiveStatus`, `Delete` capability line, `ByID` | 1 |
| `internal/content/pagehier.go` | `slugTaken` skips `trash` | 1 |
| `internal/content/menus.go` | `pruneInvalidItems` in `ByLocation`, `BySlug` | 1 |
| `internal/web/rest_posts.go`, `rest_custom_posts.go`, `rest_delete.go` | delete flow, status parameter, update statuses | 1 |
| `internal/web/adminapi.go`, `adminapi_posts.go` | list `status=trash` | 1 |
| `internal/web/adminapi_posts.go`, `adminroutes.go`, `adminapi.go` | force delete, restore, empty-trash, `trashEnabled` | 2 |
| `web/admin/src/**`, `internal/admin/dist/**` | Trash view and actions | 2 |
| `cmd/grimoire/assembly.go` | `WithPostTrasher`, `WithTrashDays`, `WithPostTrash` | 1 |
| `cmd/grimoire/testdata/builtin_golden/*.json`, `scripts/check-golden-trash.sh`, `assembly_builtin_golden_test.go` | goldens and the check | 1, 2 |
| `internal/config/config.go`, `content.go`, `configs/*.yaml` | `trash_days` | 3 |
| `internal/domain/repository.go`, `wprepo/trashed.go` (new), `storage/factory.go` | `TrashedPostLister` | 3 |
| `internal/scheduler/scheduler.go`, `cmd/grimoire/assembly.go` | purge | 3 |

No handler outside the post, page and registered-type routes changes. Constructors gain options only.

## Data Models

No schema change and no migration. Rows read or written:

| Table.column | Operation | Where |
|---|---|---|
| `{prefix}posts.post_status`, `post_name`, `post_modified`, `post_modified_gmt` | update | `TrashPost`, `RestorePost` |
| `{prefix}postmeta` `_wp_trash_meta_status`, `_wp_trash_meta_time`, `_wp_desired_post_slug` | insert, delete | same |
| `{prefix}postmeta` `_wp_trash_meta_comments_status` | insert, delete | same; PHP-serialized `array( comment_ID => comment_approved )` |
| `{prefix}comments.comment_approved` | update to `post-trashed`, back | same |
| `{prefix}term_taxonomy.count` | recompute | the shared statement |
| `{prefix}posts`, `postmeta` | read | `TrashedPosts`, `TrashInfo` |

New JSON fields: `trashEnabled` on `GET /admin/api/session`; `{"deleted","skipped","more"}` from
`empty-trash`; `{"status"}` request body of `restore`. The REST trash response is the existing post
object.

**WordPress representation, row by row**, for a published post `42` named `hello`, trashed at Unix time
`T`, with comments `7` (approved `"1"`) and `8` (`"0"`):

| Row | Value |
|---|---|
| `posts.ID=42` | `post_status = 'trash'`, `post_name = 'hello__trashed'` |
| `postmeta(42, '_wp_trash_meta_status')` | `publish` |
| `postmeta(42, '_wp_trash_meta_time')` | `T` as decimal text |
| `postmeta(42, '_wp_desired_post_slug')` | `hello` |
| `postmeta(42, '_wp_trash_meta_comments_status')` | `a:2:{i:7;s:1:"1";i:8;s:1:"0";}` |
| `comments.comment_ID in (7, 8)` | `comment_approved = 'post-trashed'` |

The contract asserts exactly these rows after `TrashPost`, and the real-database check (Requirement
3.8) confirms the first four against rows WordPress wrote.

## Findings

1. **The brief's restore status is WordPress's "Undo", not its restore.** Default `draft`; `previous`
   is opt-in on grimoire's restore route (Overview 2).
2. **Registered types trash by default over REST.** The "post and page only" rule is
   `wp_delete_post`'s, which the REST route does not use (Overview 3).
3. **Core's REST restore leaves stale trash meta and `post-trashed` comments.** grimoire cleans both
   (D5). A WordPress that later reads the row finds a clean live row.
4. **Capability checks on trashed rows use the previous status.** Without it a Contributor could
   restore or delete a formerly published row (Overview 4).
5. **`future` is not treated like `publish` by `CanEditPostSuffix`/`CanDeletePostSuffix`.** Existing
   M17 behavior; unchanged and recorded.
6. **`CountByStatus(page, "")` and the admin list's empty status include trash today.** They would
   have shown trashed rows under "All" in PR 1. The storage change in PR 1 is required by it, not
   optional.
7. **A trashed menu target would render as a link to `/x__trashed`.** `resolveMenuTarget` has no
   status check; fixed for the public menus only (Requirement 9).
8. **Core purges only rows with `_wp_trash_meta_time`.** A trashed row from `wp_update_post(
   status=trash)` carries a suffix but no time and is kept forever by core; grimoire matches.
9. **The existing media and user `DELETE` without `force` answer `400 rest_trash_not_supported`;
   WordPress answers `501`.** The 400 is an M11 decision. M22 uses `501` for posts, as the source
   says, and leaves media and users alone, so the two codes differ across resources. Recorded, not
   changed.
10. **`PostTypeAuditor` and the M14 backfill see trashed rows.** Both are all-status tools by design.
11. **Unsuffixed legacy trashed rows can block a restore in WordPress, not in grimoire.** grimoire's
    uniqueness skips every `trash` row. WordPress renames such rows when a live row takes the slug
    (`wp_add_trashed_suffix_to_post_name_for_trashed_posts`); grimoire does not rename them
    (Out of scope).

## Correctness Properties

*A property is a characteristic or behavior that should hold true across all valid executions of a system-essentially, a formal statement about what the system should do. Properties serve as the bridge between human-readable specifications and machine-verifiable correctness guarantees.*

### Property 1: Trashed names have the WordPress shape

*For any* string `name`, `trashedName(name)` ends in `__trashed`; the part before the suffix is a
byte-prefix of `name`; that part is at most 191 bytes; it ends inside no `%XX` escape that `name`
contains; and when `name` is at most 191 bytes the part before the suffix equals `name`.

**Validates: Requirements 1.2**

### Property 2: Trash then restore round-trips a live row

*For any* live row of a managed type with a status from {`publish`, `draft`, `pending`, `private`,
`future`}, a `post_name` that does not end in `__trashed` (empty, short, 191-byte, longer than 191
bytes, multibyte, containing `%XX`), any set of comments with `comment_approved` drawn from {`0`,
`1`, `spam`, `trash`}, and any set of term relationships, `TrashPost` followed by `RestorePost` with
the returned previous status and an empty slug restores `post_status`, `post_name`, every comment's
`comment_approved` and every other column except `post_modified` and `post_modified_gmt`, leaves no
`_wp_trash_meta_*`, `_wp_desired_post_slug` or `_wp_trash_meta_comments_status` row, and leaves every
related `term_taxonomy.count` equal to its count before the trash.

**Validates: Requirements 1.1, 1.4, 2.1, 2.2, 4.1, 4.3, 4.7, 5.3**

### Property 3: A second trash changes nothing

*For any* live row, after `TrashPost` succeeds, a second `TrashPost` returns `ErrAlreadyTrashed` and
the snapshot of the row, its meta, its comments and the counts equals the snapshot taken after the
first call.

**Validates: Requirements 1.7**

### Property 4: A restored name is free

*For any* trashed row with desired name `n` and *any* set of live rows (not `trash`) of the same type
whose names include `n`, `n-2`, ..., `n-k` for some `k >= 0`, the restored name is `n-(k+1)` (or `n`
when no live row holds it), is held by no other live row of the same type (and parent, when the scope
is hierarchical), and is at most 200 bytes.

**Validates: Requirements 2.4**

### Property 5: Integer-keyed PHP arrays round-trip

*For any* `map[int]string`, `php.Unserialize(php.Serialize(m))` yields the same key-value pairs (keys
compared as decimal strings), and the serialized form lists keys in ascending numeric order with `i:`
keys.

**Validates: Requirements 4.5**

### Property 6: The term count ignores trashed objects

*For any* set of posts with statuses from {`publish`, `draft`, `trash`}, any relationships between them
and a term, and any relationships whose object is not a post, `recountTx` sets `term_taxonomy.count`
to the number of relationships whose object is not a row with status `trash`.

**Validates: Requirements 5.1, 5.4**

### Property 7: A trashed row is authorized as its previous status

*For any* capability set, ownership (own or another author's) and previous status in {`publish`,
`draft`, `pending`, `private`}, `Delete` and `Update`-out-of-trash on a trashed row are allowed exactly
when `Delete` and `Update` on a live row with that status and owner are allowed.

**Validates: Requirements 6.2**

### Property 8: Pruning trashed menu items keeps the rest in place

*For any* menu tree and *any* subset of items flagged `TargetTrashed`, `pruneInvalidItems` returns a
tree that contains no flagged item, contains every unflagged item exactly once, keeps unflagged items
in their original preorder, makes each unflagged item's parent its nearest unflagged ancestor (top
level when none), and returns the same tree when applied twice.

**Validates: Requirements 9.2**

### Property 9: The default admin list excludes trash

*For any* set of rows of types {`post`, `page`} with statuses from {`publish`, `draft`, `pending`,
`private`, `future`, `trash`}, `ListForAdmin` and `CountForAdmin` with an empty `Statuses` return
exactly the rows whose status is not `trash`, and with `Statuses = ["trash"]` exactly the trashed rows.

**Validates: Requirements 10.1**

## Error Handling

| Condition | REST | Admin API |
|---|---|---|
| Unknown id, wrong type | `404 rest_post_invalid_id` | `404` |
| Not allowed to delete | `403 rest_cannot_delete` | `403` |
| Trash disabled or unwired, no force | `501 rest_trash_not_supported` | `409 trash_unsupported` |
| Already trashed, no force | `410 rest_already_trashed` | `409 already_trashed` |
| Restore a live row | n/a (a status update is a normal update) | `400 bad_request` |
| `status` list names an unknown value | `400 rest_invalid_param` | `400 invalid_status` |
| `status` other than `publish` without edit capability | `403 rest_forbidden_status` | `403` (route floor) |
| Storage failure in trash, restore or delete | `500 rest_delete_failed`, nothing changed (one transaction) | `500` |
| Delete step fails after the earlier idempotent steps | `500`; row remains; retry converges | `500` |
| Empty-trash non-authorization failure | n/a | `500`; earlier rows stay deleted; retry continues |
| Config `trash_days` negative or non-integer | startup error naming `content.trash_days` | n/a |
| Purge delete fails | logged, next row | n/a |

## Testing Strategy

**Scope of the properties.** Generated-input testing applies to the pure functions and to the storage transitions that have a non-strict inverse (trash then restore). It does not apply to the HTTP status tables, the SPA, the configuration cases, the scheduler wiring or the cross-vendor runs, which are example and integration tests (Testing Strategy). After the prework reflection, nine properties remain: the criteria "1.1 exact rows" and "1.8 previous status" are examples inside the contract fixture, "1.4 other columns unchanged", "2.1", "2.2", "4.1", "4.3", "4.7" and "5.3" are folded into Property 2, and "10.1" and "5.1" stay separate because they hold over different generators.

**Property tests** implement Properties 1-9 with the standard library's `testing/quick`, as
`internal/sanitize/properties_test.go` does: no new dependency, a seeded `rand.New(rand.NewSource(1))`
and `quick.Config{MaxCount: 200}` (storage-backed properties 2, 3, 4, 6 and 9 use 100 and one migrated
in-memory SQLite database per test with fresh high ids per iteration). Each test carries the tag
`// Feature: trash-and-restore, Property N: <text>`. Properties 2, 3, 4, 6 and 9 run on SQLite only (the
iteration count is too high for container runs); the same behaviors are pinned by the fixture-based
contracts on all three vendors. Everything else is example-based.

Pure Go over SQLite, no new dependency, no new `GRIMOIRE_TEST_*` variable;
`gofmt -l .`, `go vet ./...`, `go build ./...` and `go test ./...` green after every phase;
`go test -count=2 ./...` in each final gate. SPA: `npm test` (Vitest) and `npm run build`.

**Layers.**

- **Storage contracts** (`storagetest`, `NewReposWithPrefixFunc`, no literal prefix or vendor):
  `RunPostTrasherContract` (trash rows and restore rows of Requirements 1-4, the whole matrix of 3.5
  and 3.6), `RunTermCountContract` (the shared statement, including a trashed object and a link
  category), additions to the comment, admin-read and `PostCounter` contracts, and in PR 3
  `RunTrashedPostListerContract`. Each runs on SQLite always and on MySQL and PostgreSQL when the DSNs
  are set.
- **Storage unit tests** (SQLite): atomicity of `TrashPost` and `RestorePost` by dropping `comments` or
  `term_taxonomy` so the last statement fails, then asserting every row, meta row and count unchanged.
- **Pure tests:** the `trashedName` examples (empty, 191, 192, escape cut), `php.Serialize` examples,
  `pruneInvalidItems` trees, `parseRESTStatuses`, the config parser; Properties 1, 5, 7 and 8 run here.
- **Content service tests** over fakes: the capability matrix, the Update transition table, ordering of
  unstick and store call, `ErrTrashUnsupported`, `ErrAlreadyTrashed`, restore slug rule, `Delete`'s
  effective-status check and unchanged step order.
- **Web handler tests** over fakes: REST status codes and bodies, collection `status`, admin routes and
  error mapping.
- **Assembly (`cmd/grimoire`)**, through `buildHandler` over migrated SQLite with raw-seeded rows
  (`seedRawPost`, `seedRawMeta`, `rowSnapshot`, `newCustomWriteHandler`, `doJSON`, as M18-M20): every
  behavior of Requirements 7-12 and 15-17, in one file per PR. **Wiring proof:** each option
  (`WithPostTrasher`, `WithTrashDays`, `WithPostTrash`, the scheduler options) is removed once and the
  named test must fail; each task records that it was tried.
- **Scheduler** over fakes and a fake clock, then one assembly test through `buildScheduler` and
  `tick`, then one `test/e2e` case.
- **Real-database** (`GRIMOIRE_TEST_WP_DSN`, read-only, skips without it): `test/e2e/m22_trash_realdb_test.go`,
  patterned on `m9b_realdb_census_test.go`.
- **Golden**: `TestBuiltinGolden` plus `scripts/check-golden-trash.sh`.

**Each RED test must fail for the stated reason** (the row is deleted instead of trashed, the slug has
no suffix, the comments stay approved, the count includes the trashed row, the unmanaged row is purged),
checked before the implementation task starts.

**Cross-vendor verification** (Requirement 3.7), in the implementation task and again in the final gate
of PR 1 and PR 3:

```sh
docker run -d --name g22-mysql -e MYSQL_ROOT_PASSWORD=grimoire_ci -e MYSQL_DATABASE=grimoire_test -p 3306:3306 mysql:8.0
docker run -d --name g22-pg -e POSTGRES_PASSWORD=grimoire_ci -e POSTGRES_USER=grimoire -e POSTGRES_DB=grimoire_test -p 5432:5432 postgres:16
# wait for both to accept connections, then the CI DSNs from .github/workflows/ci.yml:
GRIMOIRE_TEST_MYSQL_DSN='root:grimoire_ci@tcp(127.0.0.1:3306)/grimoire_test?parseTime=true&multiStatements=true' \
GRIMOIRE_TEST_POSTGRES_DSN='postgres://grimoire:grimoire_ci@127.0.0.1:5432/grimoire_test?sslmode=disable' \
go test ./internal/storage/... -count=1 -v
docker rm -f g22-mysql g22-pg
```

The output must show the new contract subtests passing under `mysql` and `postgres`, not skipped. If
port 3306 or 5432 is busy the task picks free ports and adjusts the DSNs instead of reusing another
container. The M18-M20 contracts run in the same command to prove the shared count statement
regressed nothing. The likely vendor traps are the mixed-case `comment_ID`/`comment_post_ID` columns
on PostgreSQL, `longtext` meta compares on MySQL, and the `"ID"` identifier quoting in the count
subquery.

## Delivery

| PR | Phases | What a reviewer can use afterwards |
|---|---|---|
| 1 (core and REST) | 1-6 | `DELETE` on posts, pages and registered types trashes in WordPress's representation; `?force=true` deletes permanently; a status update restores; `?status=trash` lists; public, admin-list, menu and count reads hide trash; proven on three databases; goldens and docs |
| 2 (admin API and SPA) | 7-9 | The admin deletes to trash, restores, empties the trash, with a Trash view; docs |
| 3 (purge, config, docs) | 10-12 | `content.trash_days`, hourly purge through the permanent-delete sequence, final documentation and gap-analysis updates |

PR 2 branches from `main` after PR 1 merges (it calls `Trash` and `Restore`). PR 3 branches after
PR 2 (it appends no golden, but edits `assembly.go` and `scheduler.go` after PR 1's edits). PR 1
alone is a coherent release: trash works over REST and the admin API still deletes permanently, which
is a documented, temporary asymmetry (Decisions, sign-off 6).

## Decisions awaiting sign-off

Defaults are implemented as written; each is local to the task named.

1. **Behavior change.** REST `DELETE` without `force=true` now trashes instead of deleting
   permanently, on posts, pages and registered types, and the admin `DELETE` does the same in PR 2.
   Clients that relied on permanent deletes must add `?force=true` or set `content.trash_days: 0`
   (which answers `501`, not the old behavior). Tasks 4.2, 6.4, 7.2.
2. **Forbidden delete code.** `rest_cannot_edit` becomes `rest_cannot_delete` (WordPress). Task 4.1.
3. **`trash_days: 0` disables trash**, as `EMPTY_TRASH_DAYS = 0` does, and does not mean "trash with
   no purge". Alternative: `0` means keep forever and a separate flag disables trash. Task 10.1.
4. **One restore capability: edit, on the previous status.** WordPress's admin Restore link checks
   delete; its REST restore checks edit. Alternative: require both. Task 3.1.
5. **Admin restore defaults to `draft`**, WordPress's default; `previous` is opt-in and used by the
   Undo notice. Task 7.1.
6. **PR 1 ships with the admin `DELETE` still permanent** (resolved: PR 2 made it trash by default). Alternative: land PR 1 and PR 2 together.
   The cost of the split is one release where REST and the admin disagree. Tasks 6.4 and 7.2.
7. **Comments are trashed and restored with the post** in WordPress's representation, including the
   `post-trashed` status, and the admin and REST comment lists hide that status. Alternative: leave
   comments alone (a WordPress reading the row would then show approved comments on a trashed post).
   Task 2.3.
8. **Term-count rule C**: the relationship count minus trashed objects, in every count path.
   Alternatives: do nothing (A) or WordPress's published-only rule everywhere (B). Task 2.1.
9. **Public menus drop trashed targets and lift their children**; the admin menu editor keeps them.
   Task 5.1.
10. **Purge cadence is one hour and not configurable**, the first tick purges, and rows without
    `_wp_trash_meta_time` are never purged (core's rule). Alternative: also purge those by
    `post_modified`. Task 11.1.
11. **Scheduler principal gains delete capabilities** for `posts`, `pages` and registered suffixes.
    Task 11.3.
12. **Cross-vendor verification is by throwaway containers on the author's machine plus CI.** Tasks
    2.5, 6.5, 11.2 and 12.4.

## Traceability

| Design element | Requirements |
|---|---|
| `PostTrasher`, `TrashPost`, `RestorePost`, `TrashInfo` | 1, 2, 3, 4 |
| Shared count statement | 5 |
| `Trash`, `Restore`, `Update` transitions, capability matrix | 6, 11, 12, 13 |
| REST delete flow, `PUT`/`PATCH`, `GET`, `status` | 7, 8 |
| Read inventory, menus, slug and page graph | 9, 10 |
| Admin routes and SPA | 15, 16, 17, 18, 19 |
| Config and scheduler | 20, 21, 22, 23 |
| Golden changes and the check script | 14, 19 |
| Migration note, sign-off | 24 |
| Fences | 25 |
| Contracts, wiring proofs, gates | 3, 26, 27 |
| Documentation | 24, 28 |
