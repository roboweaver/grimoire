# M20 — Delete gap closure: Design

## Overview

M19 made post delete remove the menu items that point at the row and recorded four
gaps. M20 closes them in two pull requests. It adds no schema, no migration and no
capability. PR 1 adds no port; PR 2 adds one sibling port. The risk is in storage
portability and in shared-database safety, so this document starts with what reading
the code at `main` `508923e` and the WordPress source established. Where the brief
and the evidence disagree, the evidence wins and the disagreement is named.

**1. WordPress core does not re-parent child menu items on the server.** In
`wp-includes/nav-menu.php`, `_wp_delete_post_menu_item` collects the items that point
at the deleted object and calls `wp_delete_post( $id, true )` on each. In
`wp_delete_post`, the only re-pointing is by the `post_parent` column and only for
hierarchical types; `nav_menu_item` is not hierarchical, and no server code touches
`_menu_item_menu_item_parent` except `wp_update_nav_menu_item`, which writes the value
it was given. The re-parenting users see is client-side: in the nav-menus editor,
`removeMenuItem` in `nav-menu.js` calls `children.shiftDepthClass( -1 ).updateParentMenuItemDBId()`,
which moves the children of a removed item one level up, and the next menu save
stores those parents. The brief's premise that `wp_delete_post` re-parents is therefore
corrected: WordPress leaves a dangling parent when an item is deleted outside the
editor (a page delete), and its `Walker` shows such orphans at the top level.
Sources: [`nav-menu.php`](https://github.com/WordPress/wordpress-develop/blob/trunk/src/wp-includes/nav-menu.php),
[`post.php`](https://github.com/WordPress/wordpress-develop/blob/trunk/src/wp-includes/post.php),
[`nav-menu.js`](https://github.com/WordPress/wordpress-develop/blob/trunk/src/js/_enqueues/lib/nav-menu.js).

**The gap is still real and the fix is still safe.** grimoire's `buildMenuTree`
(`wprepo/menus.go:258`) builds children from `children[parentID]` starting at `0`.
A child whose parent no longer exists is never reached, so it disappears from
`GET /admin/api/menus/{id}` and from the public menu. That is worse than WordPress's
orphan-at-top-level display. Re-parenting to the deleted item's own parent is the
editor's result, reachable from either delete path, and a WordPress install that later
reads the menu sees a valid tree. It changes one postmeta value per child. The item is
kept, with the corrected rationale, and listed for sign-off (D1).

**2. `DeleteItem` and the purger disagree on the count.** `PurgeMenuItemsOf` recounts
each affected `term_taxonomy_id`; `DeleteItem` (`menus_write.go:355`) deletes the
relationship row for the given menu only and recounts nothing. Both now share one
helper (item 1 and 2 are one change).

**3. Media delete has one path, already transactional.** The admin API has no media
delete route (`adminapi_media.go` registers list, upload and attach). `MediaService.Delete`
is a stub returning `nil` that no handler calls. `DELETE /wp-json/wp/v2/media/{id}` runs
`MediaWriteService.Delete`: load, capability, `MediaWriter.Delete`, then unlink the file.
`MediaRepo.Delete` is one transaction deleting `term_relationships` (all taxonomies, by
`object_id`, no recount), `postmeta` and the `posts` row. So the cleanup extends that
transaction and needs no service option. It also does not filter `post_type` in the final
delete, which the transaction rollback and the service's `reader.ByID` currently cover.

**4. What WordPress does for an attachment.** `wp_delete_attachment` removes term
relationships (`category`, `post_tag` and `get_object_taxonomies`), deletes every
`_thumbnail_id` postmeta row whose value is the attachment id
(`delete_metadata( 'post', 0, '_thumbnail_id', $post_id, true )`), deletes comments
through `wp_delete_comment`, deletes all of its postmeta, fires `delete_post` (which
runs `_wp_delete_post_menu_item`, so its menu items go) and deletes the row. It does
**not** re-point child attachments (`wp_delete_post` does that, and returns early for
attachments), and it clears theme mods through `_delete_attachment_theme_mod` and removes
the image sizes through `wp_delete_attachment_files`. M20 does the first group and records
the second as gaps.

**5. "Delete relationships over all taxonomies" is not what WordPress does, and it is
unsafe as stated.** `wp_delete_post` calls
`wp_delete_object_term_relationships( $post_id, get_object_taxonomies( $post->post_type ) )`,
which covers the taxonomies *registered for that type in the running PHP process*. grimoire
cannot see plugin registrations, so it cannot reproduce that list. The nearest safe
equivalent is every taxonomy whose relationships can belong to a post. The hazard is
that `term_relationships.object_id` also holds ids of other object kinds. Core's
`link_category` relationships use `wp_links` ids. A delete by `object_id` alone removes a
link's categories when a link and a post share a number. `MediaRepo.Delete` does exactly
that today. M20 deletes by `object_id` for the deleted row **excluding** a short list,
`domain.NonPostObjectTaxonomies = {"link_category"}` (the only such taxonomy in core), and
records the residual risk: a plugin taxonomy of non-post objects with a colliding id is not
excluded (Finding 3).

**6. Count semantics.** WordPress counts published objects (and published or draft menu
items). grimoire's `SetPostTerms` and `PurgeMenuItemsOf` count relationships. M20 keeps the
relationship count everywhere, so every delete path agrees with every write path. For imported
rows in other statuses the count can differ from WordPress until WordPress recomputes it.

**Design in one paragraph.** Add one unexported item-delete helper in `wprepo` that reads the
item's menu parent, re-points its children, deletes meta, relationships and row, and returns
the `term_taxonomy_id`s it touched; call it from `PurgeMenuItemsOf` and `DeleteItem`, then recount
(PR 1). Extend `MediaRepo.Delete`'s transaction with the menu-item step (same helper), the
featured-image references, comments, and a recounting relationship step (PR 2). Add
`domain.PostTermRelationshipPurger` for the post delete sequence, backed by the same
relationship helper (PR 2). Wire the one new option in `buildHandler`.

## Architecture

```mermaid
flowchart LR
    subgraph content [internal/content]
        PW[PostWriteService.Delete<br/>+relationship purge step]
        MW[MediaWriteService.Delete<br/>unchanged]
        NW[NavMenuWriteService.DeleteItem<br/>unchanged]
    end
    subgraph domain [internal/domain]
        PMI[PostMenuItemPurger<br/>doc updated]
        PTR[PostTermRelationshipPurger<br/>NEW in PR 2]
        NMW[NavMenuWriter.DeleteItem<br/>doc updated]
        NP[NonPostObjectTaxonomies<br/>NEW in PR 2]
    end
    subgraph wprepo [internal/storage/wprepo]
        H[deleteMenuItemTx + reparentChildrenTx + recountTx<br/>NEW unexported, PR 1]
        RH[purgeRelationshipsTx<br/>NEW unexported, PR 2]
        PR[PostRepo.PurgeMenuItemsOf]
        PRR[PostRepo.PurgeTermRelationshipsOf<br/>NEW PR 2]
        NR[NavMenuRepo.DeleteItem]
        MR[MediaRepo.Delete]
    end
    PW --> PMI
    PW --> PTR
    MW --> MR
    NW --> NMW
    PMI --> PR
    PTR --> PRR
    NMW --> NR
    PR --> H
    NR --> H
    MR --> H
    MR --> RH
    PRR --> RH
    RH --> NP
    ASM[cmd/grimoire/assembly.go<br/>buildHandler] -.sets WithPostRelationshipPurger, PR 2.-> PW
```

**Layering is unchanged.** `content` and `web` are untouched by PR 1. PR 2 adds one option to
`PostWriteService`. The helpers are unexported in `wprepo` and take the caller's `bun.Tx`, so
one transaction spans all of a path's statements.

**Delete sequence after PR 2.**

```mermaid
sequenceDiagram
    participant H as DELETE handler
    participant PW as PostWriteService.Delete
    participant RV as RevisionWriter
    participant TD as PostTermsWriter
    participant PT as PostTermRelationshipPurger
    participant OP as Option ports
    participant PU as PostDependentsPurger
    participant MP as PostMenuItemPurger
    participant R as PostWriter
    H->>PW: Delete(actor, {ID})
    PW->>R: ByID
    PW->>PW: WritableType, CanDeletePostSuffix
    PW->>RV: DeleteRevisionsOf(id)
    loop each managed taxonomy of the type
        PW->>TD: SetPostTerms(id, tax, nil)
    end
    PW->>PT: PurgeTermRelationshipsOf(id)
    PW->>OP: unstick (type post only)
    PW->>PU: PurgePostDependents(id, parent, reparentType)
    PW->>MP: PurgeMenuItemsOf(id, type) (re-parents children, recounts)
    PW->>R: Delete(id)
```

Each arrow is its own transaction and every step is idempotent. The relationship step is placed
right after the managed detach so that the managed taxonomies keep their existing, tested path
and the new step only sweeps what remains. A failure stops the sequence before the row delete
(D8).

## Menu items: re-parenting and recount (Requirements 1-6)

### The helper

New file `internal/storage/wprepo/menuitemtx.go`, all unexported, all taking `tx bun.Tx`,
the table prefix and the vendor:

```go
// deleteMenuItemTx deletes one menu item inside tx and returns the term_taxonomy_ids
// its relationships belonged to, so the caller can recount them after every item is gone.
func deleteMenuItemTx(ctx, tx, prefix, vendor string, itemID int64) ([]int64, error)

// reparentChildrenTx sets _menu_item_menu_item_parent of the children of itemID to newParent.
func reparentChildrenTx(ctx, tx, prefix, vendor string, itemID, newParent int64) error

// recountTx sets term_taxonomy.count to the relationship count for each distinct id.
func recountTx(ctx, tx, prefix string, ttIDs []int64) error
```

`deleteMenuItemTx(item)`:

1. **Read the parent first.** `SELECT meta_value FROM {p}postmeta WHERE post_id = ? AND meta_key = '_menu_item_menu_item_parent' ORDER BY meta_id LIMIT 1`.
   No row, a non-integer, a negative value, or a value equal to `itemID` gives `newParent = 0`.
   `postmeta` has no unique key on `(post_id, meta_key)`, so the first row by `meta_id` is
   used, as the read path's `MAX(...)` pivot effectively does for a single row.
2. **Re-point children.**
   ```sql
   UPDATE {p}postmeta SET meta_value = ?          -- strconv.FormatInt(newParent, 10)
    WHERE meta_key = '_menu_item_menu_item_parent'
      AND meta_value = ?                          -- strconv.FormatInt(itemID, 10)
      AND post_id IN (SELECT "ID" FROM {p}posts WHERE post_type = 'nav_menu_item')
   ```
   The `ID` column goes through `rebind.Ident(vendor, "ID")` as `PurgeMenuItemsOf` does. The
   subquery reads `posts` while the statement writes `postmeta`, a different table, so MySQL's
   same-table subquery restriction does not apply. `meta_value` is `longtext` on MySQL, `text` on
   PostgreSQL and `TEXT` on SQLite, and comparing it with a string parameter is valid on all three.
   Only `postmeta` rows change. `menu_order`, titles and other meta of the children are untouched
   (Requirement 1.4).
3. **Collect relationships.** `SELECT term_taxonomy_id FROM {p}term_relationships WHERE object_id = ?`.
4. **Delete** the item's `postmeta`, all of its `term_relationships` rows, and its `posts` row.
5. Return the collected `term_taxonomy_id`s.

**Why every relationship, not only `nav_menu`.** `DeleteItem` used to delete the row for the given
menu only. A menu item has `nav_menu` relationships only in practice, but deleting only one of them
could leave a relationship for a deleted row, which is the bug item 4 fixes for posts. Deleting
and recounting all of them is the same cost and closes it.

### Order independence (Requirement 2)

`PurgeMenuItemsOf` collects the matched ids in one `SELECT`, de-duplicates them, sorts them
ascending, then calls `deleteMenuItemTx` for each in turn inside the one transaction, then
`recountTx` over the union of returned ids.

**Invariant.** When item `d` is processed, its stored menu parent is never an already-deleted item
other than `d` itself. Proof: when an item `e` was deleted earlier, step 2 re-pointed every row whose
parent was `e`, including `d`, to `e`'s parent. So `d`'s parent was rewritten before `d` is read.
A self-parent is mapped to `0` in step 1. A cycle `d1 → d2 → d1` processed `d1` first: `d1`'s
parent is `d2`, so `d2`'s parent becomes `d2` (its parent was `d1`); processing `d2` then sees a
self-parent and uses `0`. Every deleted id is removed, every re-pointing writes either a surviving
id, `0` or a not-yet-processed deleted id that is itself processed later, and the last item
processed resolves to `0` or a survivor, so the loop terminates with no survivor naming a deleted
item (Requirement 2.4). The contract fixtures put the parent at a lower id in one chain and at a
higher id in another, so both processing orders are exercised.

### `DeleteItem`

After the belongs-to-menu guard, `deleteMenuItemTx` then `recountTx` replace the three hand-written
deletes. The not-found result, the transaction, the signature and the `NavMenuWriteService` that
calls it are unchanged. `DeleteMenu` is not changed: it removes every item of the menu, so no item
survives with a dangling parent, and it deletes the menu term, so a recount has no row to update.

### Doc updates

`domain.PostMenuItemPurger` currently says it "changes no other row, including other menu items'
`_menu_item_menu_item_parent`". It now says that children of a deleted item get the deleted item's
menu parent and that nothing else changes. `domain.NavMenuWriter.DeleteItem` gains the same
sentence and the recount.

### Contracts

`RunPostMenuItemPurgerContract` (existing) changes: the M19 assertion that `I3`'s parent stays `I1`
becomes an assertion that it becomes `I1`'s own parent. New fixture rows and expectations:

| Row | Setup | Expected after `PurgeMenuItemsOf(P, "page")` |
|---|---|---|
| I7 | page `Q` item in `M1`, top level | kept, parent `0` |
| I1 | deleted item, menu parent `I7` | gone |
| I3 | child of `I1` | parent `I7` |
| I8 | custom item, child of `I3` | parent `I3` (grandchild unchanged) |
| I9 | `Q` item in `M2`, child of `I2` (top-level deleted) | parent `0` |
| I13 | deleted item (object `P`), child of `I1`, id above `I1` | gone |
| I14 | custom item, child of `I13` | parent `I7` (nearest surviving ancestor) |
| I15 | deleted item, parent = itself, id above all others | gone |
| I16 | custom item, child of `I15` | parent `0` |
| I20 | kept custom item in `M1`, top level | kept, parent `0` |
| I18 | deleted item (object `P`), menu parent `I20` | gone |
| I17 | deleted item (object `P`), menu parent `I18`, id **below** `I18`'s | gone |
| I19 | custom item, child of `I17` | parent `I20` |
| X | non-menu post with `_menu_item_menu_item_parent` = `I1` | unchanged |

Rows `I20`, `I18`, `I17`, `I19` form the child-first case: the chain is
`I20 ← I18 ← I17 ← I19` with `I17` and `I18` deleted and `I17`'s id below `I18`'s, so the first
item processed is the *child* of the other deleted item. The result `I19 → I20` must equal the
result of the parent-first pair `I1`/`I13`/`I14` above. `M1` and `M2` counts are asserted after
the purge.

A new `RunNavMenuDeleteItemContract(t, newRepos)` in
`internal/storage/storagetest/navmenuitemdelete_contract.go`, registered in the three vendor test
files, takes the writer from `repos.NavMenus.(domain.NavMenuWriter)` and covers: child moved to the
deleted item's parent, a top-level deleted item (`0`), recount before and after, an item of another
menu returning `ErrNotFound` with every row unchanged, and an item with relationships in two
`nav_menu` rows removed completely.

## Media delete (Requirements 7-9, 12-14)

`MediaRepo.Delete` stays one `RunInTx`. New sequence inside it:

1. **Menu items of the attachment.** The matching `SELECT` of `PurgeMenuItemsOf` with
   `objectType = "attachment"`, extracted into a shared `matchMenuItemsTx(ctx, tx, prefix, vendor,
   postID, objectType)` so both callers use one query, then `deleteMenuItemTx` per id in ascending
   order, then `recountTx`.
2. **Featured-image references.**
   `DELETE FROM {p}postmeta WHERE meta_key = '_thumbnail_id' AND meta_value = ?` with the decimal
   id string. No join and no post-type filter, as in WordPress. A value that is not exactly the
   decimal id is not matched (`"0201"`, `" 201"`): WordPress stores the integer and compares
   with `%d`, and a hand-edited value is not worth a looser rule.
3. **Comments.** `DELETE FROM {p}commentmeta WHERE comment_id IN (SELECT "comment_ID" FROM {p}comments WHERE "comment_post_ID" = ?)`,
   then the comments, written with `bun.Ident` for the mixed-case columns exactly as
   `PurgePostDependents` does. Every `comment_approved` state.
4. **Relationships.** `purgeRelationshipsTx` (below), which replaces the unconditional
   `DELETE ... WHERE object_id = ?`.
5. **Postmeta**, then **the row**: `DELETE FROM {p}posts WHERE "ID" = ? AND post_type = 'attachment'`,
   zero rows returns `domain.ErrNotFound` and the transaction rolls back, so steps 1-4 never leave
   a trace for an unknown id or for a non-attachment id. The `post_type` predicate is the one
   hardening M20 adds beyond the brief, because the new steps would otherwise run for a page id
   before the rollback; it makes the storage method safe on its own and changes no reachable
   behavior (`MediaWriteService` already resolves the id through `reader.ByID`, which filters
   attachments).

Not done, deliberately: child attachments are not re-pointed (`wp_delete_attachment` does not),
`_delete_attachment_theme_mod`'s theme-mod cleanup (it rewrites a serialized option, and
`sticky_posts` already shows the read-modify-write limit of that approach), and intermediate
image files (the service unlinks `cur.Filename` only, as before).

`MediaWriteService`, its option set and `buildHandler`'s construction of it do not change.

### Contract

`RunMediaDeleteContract(t, newRepos)` in
`internal/storage/storagetest/media_delete_contract.go`, registered in three vendor files. Fixture:
attachment `A` with postmeta, a `category` relationship and a relationship in two unmanaged
taxonomies (`product_cat`, `post_format`) with stale counts; two comments (one pending) with
`commentmeta`; a matching menu item `MI` (`post_type`/`attachment`/`A`) with a child; a non-matching
item for attachment `B`; posts `P1` with `_thumbnail_id = A` and `P2` with `_thumbnail_id = B`;
a child attachment `C` with `post_parent = A`; a `link_category` term and a relationship whose
`object_id` equals `A`; and a page `G`. Assertions are listed in Requirement 12.3, plus `Delete(G)`
returns `ErrNotFound` with every row unchanged.

## Relationships on post delete (Requirements 10-12)

### Port

```go
// internal/domain/repository.go (additive)

// NonPostObjectTaxonomies lists taxonomies whose term_relationships rows name objects that are
// not posts, so a delete keyed on a post id must not remove them. In WordPress core this is
// link_category (object ids are wp_links ids).
var NonPostObjectTaxonomies = []string{"link_category"}

// PostTermRelationshipPurger removes the term relationships of a deleted post that the
// managed-taxonomy detach does not reach, mirroring wp_delete_object_term_relationships
// for taxonomies grimoire does not manage. It never deletes the post row itself.
type PostTermRelationshipPurger interface {
    // PurgeTermRelationshipsOf, in one transaction, deletes every term_relationships row whose
    // object_id is postID and whose taxonomy is not in NonPostObjectTaxonomies, then recounts
    // term_taxonomy.count of every term_taxonomy_id it removed a row from. No relationships is not
    // an error and the post row need not exist. It never changes terms, term_taxonomy rows other
    // than their count, or another object's relationships.
    PurgeTermRelationshipsOf(ctx context.Context, postID int64) error
}
```

`wprepo.PostRepo` implements it in `internal/storage/wprepo/postrelationships.go`. `storage.Set`
gains `PostRelationships domain.PostTermRelationshipPurger` set to the `posts` repo beside
`PostMenuItems`. The compile-time assertion sits beside the others in `writers.go`.

### Statements

`purgeRelationshipsTx(ctx, tx, prefix, objectID)` is shared by `PostRepo` and `MediaRepo`:

```sql
SELECT tr.term_taxonomy_id FROM {p}term_relationships tr
  JOIN {p}term_taxonomy tt ON tt.term_taxonomy_id = tr.term_taxonomy_id
 WHERE tr.object_id = ? AND tt.taxonomy NOT IN (?, ...)      -- one ? per denylist entry
```

then per distinct id `DELETE FROM {p}term_relationships WHERE object_id = ? AND term_taxonomy_id = ?`,
then `recountTx`. Per-id statements match the PR 3 and `SetPostTerms` style and avoid `IN (...)` list
limits. The denylist is expanded into placeholders from `domain.NonPostObjectTaxonomies`, so the list
lives in one place. A relationship row whose `term_taxonomy_id` has no `term_taxonomy` row (an orphan
from another tool) is not matched by the join and is left alone, which is the safe direction.

**Shared-database safety.** The statements are keyed on the deleted row's id and a fixed denylist.
They never delete by term, taxonomy alone or term id, never touch `terms` or `term_taxonomy` beyond
`count`, and run only after the service has loaded the row, checked its type and the delete capability.
A second grimoire process and a live WordPress can both write relationships for other posts at the same
time; the recount is a `COUNT(*)` subquery inside one `UPDATE`, so it is correct at the moment it runs
and WordPress recomputes on its next relationship write. The residual risk is Finding 3.

### Service

```go
func WithPostRelationshipPurger(p domain.PostTermRelationshipPurger) PostWriteOption   // nil: skip
```

`Delete` calls `PurgeTermRelationshipsOf(ctx, cur.ID)` after the term detach and before the unstick
step, for every writable type, and returns its error before any later step. The managed detach keeps
running first, so managed taxonomies keep their tested path and the new step finds nothing for them
(Requirement 11.4). The `Delete` doc comment's step list gains the step.

### Contract

`RunPostTermRelationshipPurgerContract(t, newRepos)` in
`internal/storage/storagetest/postrelationships_contract.go`, registered in three vendor files. Fixture:
post `P` related to a `category`, a `post_tag` and terms of two unmanaged taxonomies (`product_cat`,
`post_format`), post `Q` related to the same terms, a `link_category` term with a relationship whose
`object_id` equals `P`, stale counts on every term, a nonexistent id. Assertions are Requirement 12.2.

## Components and Interfaces

| Package | Change | PR |
|---|---|---|
| `internal/storage/wprepo/menuitemtx.go` (new) | `matchMenuItemsTx`, `deleteMenuItemTx`, `reparentChildrenTx`, `recountTx` | 1 |
| `internal/storage/wprepo/postmenuitems.go` | `PurgeMenuItemsOf` uses the helpers | 1 |
| `internal/storage/wprepo/menus_write.go` | `DeleteItem` uses the helpers | 1 |
| `internal/domain/repository.go` | doc comments of `PostMenuItemPurger`, `NavMenuWriter.DeleteItem` | 1 |
| `internal/storage/storagetest/postmenuitems_contract.go`, `navmenuitemdelete_contract.go` (new), three vendor `*_test.go` | contracts | 1 |
| `internal/domain/repository.go` | `NonPostObjectTaxonomies`, `PostTermRelationshipPurger` | 2 |
| `internal/storage/wprepo/postrelationships.go` (new), `writers.go`, `internal/storage/factory.go` | port implementation, assertion, `Set.PostRelationships` | 2 |
| `internal/storage/wprepo/media.go` | `MediaRepo.Delete` sequence | 2 |
| `internal/storage/storagetest/postrelationships_contract.go`, `media_delete_contract.go` (new), three vendor `*_test.go` | contracts | 2 |
| `internal/content/writeservices.go` | `WithPostRelationshipPurger`, delete step | 2 |
| `cmd/grimoire/assembly.go` | `content.WithPostRelationshipPurger(repos.PostRelationships)` | 2 |

Web handlers, routes, the media service and the admin SPA keep their shape. Constructors gain
variadic options only.

## Data Models

No schema change and no migration. Rows read or written:

| Table.column | Operation | Where |
|---|---|---|
| `{prefix}postmeta` `_menu_item_menu_item_parent` | read for the deleted item, update for its children | `deleteMenuItemTx`, PR 1 |
| `{prefix}posts`, `postmeta`, `term_relationships` | delete for the item | `deleteMenuItemTx`, PR 1 |
| `{prefix}term_taxonomy.count` | recompute | `recountTx`, PR 1 and 2 |
| `{prefix}postmeta` `_thumbnail_id` of other posts | delete rows naming the attachment | `MediaRepo.Delete`, PR 2 |
| `{prefix}comments`, `commentmeta` | delete for the attachment | `MediaRepo.Delete`, PR 2 |
| `{prefix}term_relationships` | delete for the object, minus `link_category` | `purgeRelationshipsTx`, PR 2 |

No JSON body changes.

## Findings

1. **WordPress core leaves the dangling parent; only the editor re-parents.** Recorded
   in Overview 1. The change is justified by grimoire's tree builder dropping orphans, not by
   WordPress parity, and a WordPress reading the result sees a valid tree.
2. **`MediaRepo.Delete` removes `term_relationships` without a recount and by `object_id` alone.**
   It leaves stale counts and can remove a `link_category` row. PR 2 fixes both.
3. **Residual shared-database risk.** A plugin taxonomy whose relationships name non-post objects
   (for example users) and whose id collides with a deleted post's id is not on the denylist, so
   that row is removed. The probability needs a plugin taxonomy of that kind and a numeric collision;
   the effect is one lost relationship on a non-post object. The alternative (below, sign-off 5) is
   an allowlist.
4. **Count semantics differ from WordPress for non-published rows.** Same as M19 Finding 3; the
   recount is the relationship count everywhere.
5. **`MediaRepo.Delete` had no `post_type` guard.** Added with the new steps (Requirement 8.6).
6. **Menu items of media are rare but legal.** WordPress allows an attachment as a `post_type` menu
   item, and `wp_delete_attachment` fires `delete_post`, so WordPress removes them; grimoire now does.
7. **The recount reads all relationships of a menu item, not only `nav_menu` ones.** Items have no
   other kind in practice. Recounting every touched row is the safe superset.
8. **`MediaService.Delete` is an unused stub returning `nil`.** Left alone; a reader could mistake it
   for the delete path. Recorded, not changed.

## Error Handling

No new status codes, error codes or envelopes.

| Condition | REST | Admin API |
|---|---|---|
| Menu item delete fails in storage (any statement) | n/a | `500`, transaction rolled back, nothing changed |
| Page, post or custom-type delete: relationship purge fails | `500 rest_delete_failed` | `500`, row remains, retry converges |
| Page, post or custom-type delete: menu-item purge fails | `500 rest_delete_failed` | `500`, row remains, retry converges |
| Media delete: any cleanup statement fails | `500 rest_delete_failed`, nothing changed | n/a |
| Media id unknown or not an attachment | `404 rest_post_invalid_id` | n/a |
| Caller lacks capability | `403 rest_cannot_delete` / `403` | `403` |

## Decisions awaiting sign-off

Defaults are implemented as written; each is local to the task named.

1. **Re-parent children to the deleted item's own parent in storage (D1).** WordPress core does
   not do this on the server; only its editor does. Alternative: leave dangling parents, which hides
   the children in grimoire. Task 2.1.
2. **Media scope (D5).** Menu items, comments, `_thumbnail_id` references and recounted relationships
   are cleaned; theme mods, image sizes and child re-pointing are not. Alternative: also clear
   `custom_logo` and `header_image` theme mods (a serialized-option rewrite). Task 6.2.
3. **Denylist of non-post-object taxonomies (D6).** `link_category` only. Alternative: add more
   names, or make the list configurable. Task 6.1.
4. **Sibling port `PostTermRelationshipPurger` (D7).** Alternative: a method on `PostDependentsPurger`,
   which breaks every fake. Task 6.1.
5. **Delete every relationship except the denylist, versus an allowlist.** The allowlist alternative
   removes only relationships in `category`, `post_tag`, registered taxonomies, `post_format` and
   `nav_menu`, which cannot touch an unknown plugin taxonomy but leaves `product_cat` and every other
   plugin taxonomy behind, which is the gap itself. Task 6.1.
6. **`post_type` guard on the media row delete.** Beyond the brief; changes no reachable behavior.
   Task 6.2.
7. **Cross-vendor verification is by throwaway containers on the author's machine, plus CI
   (Requirement 5.4).** Tasks 2.2, 4.2, 6.2 and 8.2.

## Testing strategy

Pure Go over SQLite, no new dependency, no new `GRIMOIRE_TEST_*` variable; `gofmt -l .`,
`go vet ./...`, `go build ./...` and `go test ./...` green after every phase. Storage tests also run
on MySQL and PostgreSQL as described below.

**No Correctness Properties section, deliberately.** The behaviors are enumerable branches over fixed
fixtures: a parent chain with named orderings, one denylist entry, one matching rule for menu items and
a closed set of delete steps. The input space is small and every interesting case (self-parent, cycle,
colliding id, non-attachment id) is named, so a generator would add no case the fixtures do not
already hold. This matches the no-PBT decisions in M13 and M17-M19.

**Layers.**

- **Storage contracts** (`storagetest`, `NewReposWithPrefixFunc`, no literal prefix or vendor): the
  menu purger and `DeleteItem` (PR 1), the relationship purger and `MediaWriter.Delete` (PR 2), on all
  three vendors.
- **Storage unit tests** (SQLite): atomicity of each path by dropping a table so the last statement
  fails and asserting every row is unchanged.
- **Content services**, over the existing fakes: the new delete step, its order and failure behavior
  in `PostWriteService.Delete`.
- **Assembly (`cmd/grimoire`)**, through `buildHandler` over migrated SQLite with raw-seeded rows:
  PR 1 deletes a menu item through the menu editor route and a page through the post route and reads
  the tree back through the admin and public menu reads; PR 2 deletes an attachment through
  `DELETE /wp-json/wp/v2/media/{id}?force=true` and posts, pages and registered-type rows through their
  routes. **Wiring proof:** the one new option is removed once and the named test must fail; each
  storage change without an option is reverted once and the named test must fail; the task records the
  result.
- **Built-in golden**: `TestBuiltinGolden` runs in every phase gate and is unchanged throughout.

**Each RED test must fail for the stated reason** (a child still names a deleted id, `count` stays
stale, the comment row remains, the `product_cat` relationship remains), checked before the
implementation task starts.

**Cross-vendor verification** (Requirement 5.4), run in the implementation task and again in the
final gate of each PR:

```sh
docker run -d --name g20-mysql -e MYSQL_ROOT_PASSWORD=grimoire_ci -e MYSQL_DATABASE=grimoire_test -p 3306:3306 mysql:8.0
docker run -d --name g20-pg -e POSTGRES_PASSWORD=grimoire_ci -e POSTGRES_USER=grimoire -e POSTGRES_DB=grimoire_test -p 5432:5432 postgres:16
# wait for both to accept connections, then the CI DSNs from .github/workflows/ci.yml:
GRIMOIRE_TEST_MYSQL_DSN='root:grimoire_ci@tcp(127.0.0.1:3306)/grimoire_test?parseTime=true&multiStatements=true' \
GRIMOIRE_TEST_POSTGRES_DSN='postgres://grimoire:grimoire_ci@127.0.0.1:5432/grimoire_test?sslmode=disable' \
go test ./internal/storage/... -count=1 -v
docker rm -f g20-mysql g20-pg
```

The output must show the new contract subtests passing under `mysql` and `postgres`, not skipped.
If port 3306 or 5432 is busy, the task picks free ports and adjusts the DSNs instead of reusing
another container. The M18 and M19 contracts run in the same command to prove nothing regressed.

## Delivery

| PR | Phases | What a reviewer can use afterwards |
|---|---|---|
| 1 (menus) | 1-4 | Deleting a menu item, by the editor or by deleting its page, keeps its children in the menu and keeps the menu count right; proven on three databases; docs and gate |
| 2 (media and relationships) | 5-8 | Deleting an attachment cleans menu items, comments, featured-image references and counts; deleting a post, page or custom-type row removes its relationships in every taxonomy except the denylist; proven on three databases; docs and gate |

PR 2 branches from `main` after PR 1 merges, because it reuses the item-delete helper and edits
`writeservices.go` after PR 1's doc updates.

## Traceability

| Design element | Requirements |
|---|---|
| `deleteMenuItemTx`, `reparentChildrenTx` | 1, 2, 3 |
| `recountTx` in `DeleteItem` | 4 |
| Menu contracts and containers | 5 |
| PR 1 assembly | 6 |
| `MediaRepo.Delete` menu step | 7 |
| `MediaRepo.Delete` comments, `_thumbnail_id`, guard | 8 |
| `MediaRepo.Delete` relationships | 9 |
| `PostTermRelationshipPurger`, delete step | 10 |
| Denylist, shared-database safety | 11 |
| PR 2 contracts | 12 |
| PR 2 assembly and wiring proofs | 13, 14 |
| Golden handling, compatibility | 15 |
| Phases, RED/GREEN pairs, gates | 16 |
| Docs tasks | 17 |
