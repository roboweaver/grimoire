# M19 — Term-side write gate and delete parity: Design

## Overview

M18 closed the post side of "grimoire must not touch what it does not manage" and
recorded the term side, `sticky_posts`, nav-menu items and the REST page `parent`
as open. M19 closes those four in three pull requests. It is small in new
machinery (one helper, two service options, one storage port, one `php` helper)
and its risk is in guards and in storage portability, so this document starts
with what reading the code at `main` `332548a` established. Where the brief and
the code disagree, the code wins and the disagreement is named.

**1. Term create is already gated; update and delete are not.**
`TermWriteService.Create` returns `ErrTaxonomyUnknown` for a taxonomy that is not
`category`, `post_tag` or registered (`writeservices.go:790-794`), and both create
handlers map it to `400` (`termValidationMessage`, `rest_terms.go:287`).
`Update` and `Delete` check only `auth.CanManageTerms`. `Update` reads the stored
term for the name step but never looks at `stored.Taxonomy`, and `Delete` does not
read the term at all. The admin handlers hand them any id (`adminapi_terms.go:119,177`).
A holder of `manage_categories` can therefore rename or delete a `nav_menu` term,
which breaks every menu that uses it, or a `product_cat` term.

**2. The REST term routes are already safe.** Every REST term handler is bound to
one taxonomy and starts with `restTermByID`, which answers `404
rest_term_invalid_id` when the stored term's taxonomy differs from the route's
(`rest_terms.go:205-222`). Both the built-in routes (`rest_terms.go:49`) and the
registered-taxonomy routes (`rest_custom_terms.go:23`) use the same closures. As
in M18, the fix goes in the service so every surface shares it, and REST is
pinned by characterization tests rather than changed.

**3. The wider term-side hole is post-term assignment.**
`PostTermsWriteService.SetPostTerms` validates the taxonomy only when the post
row is a registered type (`postterms.go:149-153`). For a `post` or `page` row the
taxonomy argument goes straight to `PostTermsWriter.SetPostTerms`. The admin
`termIds` map is `map[string][]int64` keyed by a caller-supplied taxonomy name
(`adminapi_posts.go:259`), so `{"termIds":{"nav_menu":[7]}}` on a post update
attempts a relationship write in an unmanaged taxonomy, and
`{"termIds":{"category":[7]}}` with a term id from another taxonomy is not
checked against the taxonomy for built-ins either. Both are closed in PR 1.

**4. Term inventory.** Every caller of the term write ports in the tree:

| Call site | Taxonomy source | Gate in M19 |
|---|---|---|
| `adminTermCreate` → `TermWriteService.Create` | request body | existing `ErrTaxonomyUnknown` (`400`), pinned |
| `adminTermUpdate` → `TermWriteService.Update` | stored term | **new**: stored-taxonomy gate (`403`) |
| `adminTermDelete` → `TermWriteService.Delete` | none (id) | **new**: reads stored term, gate (`403`) |
| `handleRESTTermCreate` (built-in and registered routes) | route | route taxonomy only; service gate as above |
| `handleRESTTermUpdate`, `handleRESTTermDelete` | stored term, must equal route | `404` at the route; service gate behind it |
| M13 parent re-parenting inside `Create`/`Update` | stored term and parent | `validateParent` already requires the parent to be in the same taxonomy; a managed term cannot be parented under an unmanaged one; pinned |
| `PostTermsWriteService.SetPostTerms` (admin `termIds`, REST term assignment) | request (admin), route (REST) | **new**: managed-taxonomy gate, built-in membership check |
| `PostWriteService.Delete` term detach (`SetPostTerms(…, nil)`) | `deleteTaxonomies` (managed only) | none needed; pinned |
| `NavMenuWriteService` → `NavMenuRepo` | fixed `nav_menu` | none: grimoire's own menu editor, own capability, never calls `TermWriteService` (D4); pinned |
| `cmd/grimoire-cli` | no term writes | none (checked by task 1.1's grep) |

**5. `sticky_posts` needs no new port.** `OptionRepository.Get` and
`OptionWriter.Set/Delete` exist, `storage.Set` exposes both (`Options`,
`OptionWriter`), and `internal/php` parses (`Unserialize` returns
`map[string]any` with string keys and `int` values) and writes
(`Serialize`) PHP arrays. `Serialize` supports `map[string]any` only, which
writes **string** keys (`s:1:"0"`), not the integer keys WordPress writes
(`a:1:{i:0;i:5;}`). PR 2 therefore adds an additive `[]int` case that writes a
list with integer keys.

**6. REST page `parent` is one struct tag.** `RESTPage.Parent` is `int64
\`json:"parent,omitempty"\`` (`rest.go:101`) and `RESTMapper.Page` already sets
it (`rest.go:380`). Posts have no parent field in WordPress. A hierarchical
custom type already emits `parent` including `0` through a `*int64`
(`rest.go:472-475`). The change is the tag, the regenerated golden files and a
diff check.

**7. Nav-menu items point at their object by postmeta.** A menu item is a
`nav_menu_item` post row with `_menu_item_type`, `_menu_item_object`,
`_menu_item_object_id` and `_menu_item_menu_item_parent` postmeta, linked to its
menu by a `term_relationships` row whose `term_taxonomy.taxonomy` is `nav_menu`
(`wprepo/menus_write.go`). `NavMenuRepo.DeleteItem` shows the delete shape: postmeta,
relationship, post row, in one transaction. It does not recount
`term_taxonomy.count`. WordPress deletes menu items of a deleted post through
`_wp_delete_post_menu_item`, which calls `wp_delete_post` on each item
([WordPress reference](https://developer.wordpress.org/reference/functions/_wp_delete_post_menu_item/)),
and that call removes the item's relationships and updates the menu term count.

**Design in one paragraph.** Add `content.ManagedTaxonomy` and gate term update,
term delete and post-term assignment on it (PR 1). Drop `omitempty` from
`RESTPage.Parent`, regenerate the built-in golden files in their own commit and
prove the only diff is `"parent": 0`; give `PostWriteService` an option holding
the option ports and unstick a deleted `post` from `sticky_posts` (PR 2). Add
`domain.PostMenuItemPurger`, implement it once in `wprepo`, and call it from
`PostWriteService.Delete` after the dependents purge (PR 3). Wire each in
`buildHandler` with a test that fails if the wiring or the change is removed.

## Architecture

```mermaid
flowchart LR
    subgraph web [internal/web]
        AT[adminapi_terms.go<br/>PUT / DELETE / POST<br/>unchanged]
        RT[rest_terms.go, rest_custom_terms.go<br/>by-id taxonomy 404<br/>unchanged]
        AP[adminapi_posts.go<br/>termIds partial map<br/>unchanged]
    end
    subgraph content [internal/content]
        MT[ManagedTaxonomy<br/>typegate.go]
        TW[TermWriteService<br/>+stored-taxonomy gate]
        PT[PostTermsWriteService<br/>+taxonomy gate +membership]
        PW[PostWriteService.Delete<br/>+unstick +menu-item purge]
        MP[RESTMapper.Page<br/>parent always]
    end
    subgraph storage [internal/storage]
        OP[OptionRepository / OptionWriter<br/>existing, PR 2]
        MI[domain.PostMenuItemPurger<br/>NEW in PR 3]
        EX[existing ports]
    end
    AT --> TW
    RT --> TW
    AP --> PT
    TW --> MT
    PT --> MT
    PW --> OP
    PW --> MI
    PW --> EX
    ASM[cmd/grimoire/assembly.go<br/>buildHandler] -.sets options.-> PW
```

**Layering is unchanged.** `content` imports `registry` and `php`; `domain`
declares the new port; `wprepo` implements it. `web` is untouched by PR 1 and
PR 3, and PR 2 touches no handler.

**Order of checks in every term write**, extending M17 and M18 once:
authentication and CSRF, parse (400), REST by-id existence and taxonomy (404,
REST only), **capability (403)**, **taxonomy gate** (`403` id-addressed, `400`
name-addressed), value validation (name, slug, parent; 400), write. The taxonomy
gate follows the capability check here, unlike the post type gate which precedes
it in M18, because the term services have no "missing id" response that must be
made indistinguishable: `adminTermUpdate` already answers `404` for a missing
term before the service runs. A caller without `manage_categories` sees `403`
for every term, so the gate discloses nothing.

**Delete sequence after PR 3.**

```mermaid
sequenceDiagram
    participant H as DELETE handler
    participant PW as PostWriteService.Delete
    participant RV as RevisionWriter
    participant TD as PostTermsWriter
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
    PW->>OP: unstick (type post only): Get, Set
    PW->>PU: PurgePostDependents(id, parent, reparentType)
    PW->>MP: PurgeMenuItemsOf(id, cur.Type)
    PW->>R: Delete(id)
    PW-->>H: nil
```

Each arrow is its own transaction. Every step is idempotent, and any failure
stops the sequence before the row is deleted, so the post remains addressable
and the call can be repeated (decision D8).

## Term gate (Requirements 1-5)

Add to `internal/content/typegate.go`:

```go
// ManagedTaxonomy reports whether the term services may write terms of
// taxonomy: the built-in taxonomies always, a registered taxonomy when reg holds
// it, nothing else.
func ManagedTaxonomy(reg *registry.Registry, taxonomy string) bool {
    switch taxonomy {
    case TaxonomyCategory, taxonomyPostTag:
        return true
    }
    if reg == nil {
        return false
    }
    _, ok := reg.Taxonomy(taxonomy)
    return ok
}
```

**`TermWriteService.Update`** reads the stored term as today (one `TermsByIDs`
read), and immediately after the not-found check:

```go
if !ManagedTaxonomy(s.reg, stored.Taxonomy) { return ErrForbidden }
t.Taxonomy = stored.Taxonomy   // the stored value is authoritative, never the caller's
```

The second line matters: today `t.Taxonomy` is whatever the handler merged in, and
a caller that bypasses the handler could claim `category` for a `nav_menu` term.
The gate and everything after it use the stored taxonomy.

**`TermWriteService.Delete`** gains one read: capability check, `TermsByIDs([id])`
(read error returned, nothing deleted), empty result → `domain.ErrNotFound`,
unmanaged → `ErrForbidden`, then `s.w.Delete`. The REST delete handler already
pre-reads and the admin handler does not, so the admin route now does one extra
read per delete.

**`TermWriteService.Create`** is unchanged: the existing `ErrTaxonomyUnknown`
check is the name-addressed gate. It is rewritten to call `ManagedTaxonomy`, so
one helper defines "managed" for all three services; behavior is identical.

**`PostTermsWriteService.SetPostTerms`**, after the type gate and capability
check:

```go
if !ManagedTaxonomy(s.reg, taxonomy) { return ErrTaxonomyUnknown }
if registered { checkTerms(...) }                       // attached + membership, as today
else if len(termIDs) > 0 && s.terms != nil {            // built-in post/page: membership only
    if err := s.checkMembership(ctx, taxonomy, termIDs); err != nil { return err }  // ErrTermInvalid
}
```

`checkMembership` is the loop already inside `checkTerms`, extracted so both
callers share it. `buildHandler` already passes `WithPostTermsReader`
(`assembly.go:127`), so the check runs in the shipped binary. An empty `termIDs`
for an unmanaged taxonomy is also refused: M18 made the empty-list case a
refusal for unwritable rows for the same reason (a clear request is still a
write path). `CheckAssignable` is a create-time pre-check for registered types
keyed by route type and is unchanged.

**Responses by surface (decision D2).**

| Surface | Unmanaged taxonomy | Why |
|---|---|---|
| `PUT`/`DELETE /admin/api/terms/{id}` | `403 forbidden` | id-addressed, as M18's `403` for an unwritable post row |
| `POST /admin/api/terms`, REST term create | `400` (existing) | name-addressed; shipped behavior |
| admin post write `termIds` for an unmanaged taxonomy | post write kept, entry in `partial` | the existing per-taxonomy failure path (`applyTermIDs`) |
| REST `PUT`/`PATCH`/`DELETE` of a term in another taxonomy | `404 rest_term_invalid_id` | existing route check, runs before the service |
| any `GET` | unchanged | reads are not gated |

**What the gate does not do.** It does not hide terms from reads, does not touch
`NavMenuWriteService` (D4), and does not move terms of a removed registry entry.
A taxonomy removed from `content:` becomes unwritable until it is registered
again (Requirement 4.5), the same rule M18 set for post types.

## REST page `parent` (Requirements 6, 7)

`RESTPage.Parent` becomes `json:"parent"`. Nothing else in the mapper changes:
`restItemCommon` marshals first and `Parent` follows, and the golden writer sorts
keys, so the only visible change is the new key.

**Decision for the other types (D5).** WordPress's `WP_REST_Posts_Controller`
adds `parent` to the schema only for hierarchical post types
([REST posts controller](https://developer.wordpress.org/reference/classes/wp_rest_posts_controller/)).
Posts are not hierarchical, so `RESTPost` stays without `parent`. A registered
hierarchical custom type already emits it through `*int64`, including `0`, and a
flat type omits it. No code change for either; Requirement 7 pins all three with
tests so a later "make it consistent" edit fails.

**Golden regeneration.** The built-in golden files
(`cmd/grimoire/testdata/builtin_golden/with_registry.json`, `without_registry.json`)
contain page objects. Top-level page objects lack `parent` today; nested ones
already carry it (`"parent": 1`). The regeneration is its own commit, run as
`go test ./cmd/grimoire -run TestBuiltinGolden -update` after the change, and
the diff check proves the shape of the change:

```sh
# removed lines: none
git diff -U0 main -- cmd/grimoire/testdata/builtin_golden | grep -E '^-[^-]' && exit 1
# added lines: only "parent": 0
git diff -U0 main -- cmd/grimoire/testdata/builtin_golden \
  | grep -E '^\+[^+]' | grep -Ev '^\+[[:space:]]+"parent": 0,?$' && exit 1
# the added count equals the number of top-level page objects in the pre-change golden
```

The third check is a count: `git show main:<file> | jq` counts page objects
(objects with `"type": "page"` that lack `parent`) and the number must equal the
number of added lines for that file. The task states the expected count after
running it, from the pre-change file, before regenerating. Any other added or
removed line is a stop (Requirement 12.3).

**Existing assertions.** Handler and mapper tests that assert the absence of
`parent` on a top-level page (written for the M18 `omitempty` choice) change in
this PR, narrowly, and the commit says so.

## `sticky_posts` on delete (Requirement 8)

### `php` helper

`internal/php/serialize.go` gains `case []int:` in `serializeValue`, writing
`a:N:{i:0;i:v0;…}` with integer keys and integer values. It is additive: no
existing type changes. Tests: output equals `a:2:{i:0;i:5;i:1;i:9;}` for
`[]int{5, 9}`, `a:0:{}` for an empty slice, and `Unserialize(Serialize(x))`
returns a map whose keys `"0".."n-1"` hold the same values in order.

### Service

```go
// WithStickyOptions wires the option ports PostWriteService.Delete uses to
// remove a deleted post's id from sticky_posts. Either nil skips the step.
func WithStickyOptions(r domain.OptionRepository, w domain.OptionWriter) PostWriteOption
```

`Delete`, after the term detach and before the purger:

```go
if cur.Type == "post" && s.optionsR != nil && s.optionsW != nil {
    if err := s.unstick(ctx, cur.ID); err != nil { return err }
}
```

`unstick(ctx, id)`:

1. `raw, err := optionsR.Get(ctx, "sticky_posts")`. `ErrNotFound` → return nil
   (the option is never created). Any other error → return it.
2. `php.Unserialize(raw)`. A parse failure, a non-array, or a value that is not an
   integer → log at warn, return nil, write nothing (a malformed option set by a
   plugin must not block a delete or be rewritten blindly).
3. Collect the integer values ordered by integer key, drop `id`. When nothing was
   dropped, return nil without writing (Requirement 8.2).
4. `optionsW.Set(ctx, "sticky_posts", php.Serialize(remaining))`, with the empty
   case writing `a:0:{}`. A `Set` error is returned.

**Idempotence and ordering.** Step 3 makes a repeat a no-op, so a retry after a
later step fails converges. The step sits after the term detach and before the
purge so that all the sequence's reads of grimoire-owned rows come before the
two destructive purges, and it is the first step that touches a table outside
`posts`/`term_*`; a failure still leaves the row and everything earlier already
idempotent. Read-modify-write is not atomic against a concurrent WordPress
`stick_post`; the window is the length of two queries and is recorded as a
known limit (Finding 4).

**Why `post` only.** WordPress's sticky list is meaningful for the `post` type
only, and a page or custom-type id in `sticky_posts` is not produced by core.
Skipping them avoids a read of the options table on every page delete.

**No new port, no options-table change.** `repos.Options` and
`repos.OptionWriter` are already in `storage.Set`.

## Menu-item cleanup (Requirements 9-11)

### Port

```go
// internal/domain/repository.go (additive)

// PostMenuItemPurger deletes the nav-menu items that point at a post,
// mirroring WordPress's _wp_delete_post_menu_item for the parts the existing
// ports cannot reach. It never deletes the post row itself.
type PostMenuItemPurger interface {
    // PurgeMenuItemsOf, in one transaction, deletes every nav_menu_item row whose
    // _menu_item_type is "post_type", _menu_item_object equals objectType and
    // _menu_item_object_id equals postID, together with each item's postmeta and
    // term_relationships rows, then recomputes term_taxonomy.count of every
    // nav_menu term taxonomy the items belonged to. No matching items is not an
    // error and the post row need not exist. It changes no other row, including
    // other menu items' _menu_item_menu_item_parent.
    PurgeMenuItemsOf(ctx context.Context, postID int64, objectType string) error
}
```

`wprepo.PostRepo` implements it in `internal/storage/wprepo/postmenuitems.go`
with `RunInTx` and the raw-SQL style `PurgePostDependents` and `NavMenuRepo` use,
so one implementation serves MySQL, PostgreSQL and SQLite. `storage.Set` gains
`PostMenuItems domain.PostMenuItemPurger`, set to the same `posts` repo as
`PostDependents`. The compile-time assertion sits beside the others in
`wprepo/writers.go`.

### Statements

Match, in one `SELECT` inside the transaction (identifiers prefixed, the `ID`
column wrapped with `rebind.Ident(vendor, "ID")` as `DeleteItem` does):

```sql
SELECT p.ID FROM {p}posts p
  JOIN {p}postmeta o ON o.post_id = p.ID AND o.meta_key = '_menu_item_object_id' AND o.meta_value = ?
  JOIN {p}postmeta t ON t.post_id = p.ID AND t.meta_key = '_menu_item_type'      AND t.meta_value = 'post_type'
  JOIN {p}postmeta j ON j.post_id = p.ID AND j.meta_key = '_menu_item_object'    AND j.meta_value = ?
 WHERE p.post_type = 'nav_menu_item'
```

The first bind is `strconv.FormatInt(postID, 10)` and the second is `objectType`.
`meta_value` is `longtext` on MySQL, `text` on PostgreSQL and `TEXT` on SQLite;
comparing it with a string parameter is valid on all three, and `postmeta` has no
unique key on `(post_id, meta_key)`, so the join can match duplicate rows;
the Go side de-duplicates ids with a set. The `nav_menu_item` row-type predicate
stops a non-menu post that carries a stray `_menu_item_object_id` meta key from
being deleted.

For the matched ids, collected into a Go slice, per id: read the item's
`term_taxonomy_id` values for taxonomy `nav_menu` (needed for the recount);
`DELETE FROM {p}postmeta WHERE post_id = ?`;
`DELETE FROM {p}term_relationships WHERE object_id = ?`; and
`DELETE FROM {p}posts WHERE ID = ?`. Then, for each distinct recorded
`term_taxonomy_id`:

```sql
UPDATE {p}term_taxonomy SET count =
  (SELECT COUNT(*) FROM {p}term_relationships WHERE term_taxonomy_id = ?)
 WHERE term_taxonomy_id = ?
```

The subquery reads `term_relationships` while the update writes `term_taxonomy`,
which MySQL accepts (the restriction is on a subquery over the **updated**
table). Per-id statements keep every statement trivially portable and avoid
`IN (…)` list limits. Menus have tens of items, not thousands. The relationship
delete uses only `object_id` because a menu item has relationships in the
`nav_menu` taxonomy only; deleting all of them is what `wp_delete_post` does.

**WordPress's count.** WordPress's `nav_menu` taxonomy counts through
`_update_menu_item_count`, which counts published and draft menu items; since
grimoire creates menu items as `publish` (`insertItemPost`), the plain count of
relationships and WordPress's count agree for rows grimoire wrote. For imported
items in other statuses the recount can differ from WordPress by the items WordPress
excludes. This is recorded as Finding 3; the count is advisory and WordPress
recomputes it on its next menu edit.

**What storage allows, and what is left out.** The statements above reach
everything the brief named. They do not re-parent child menu items (D10): that
is a second `UPDATE postmeta` keyed on `_menu_item_menu_item_parent`, portable
and small, but `NavMenuRepo.DeleteItem` does not do it either and a half-fix
would make the two delete paths disagree. It is recorded as a gap.

### Contract

`RunPostMenuItemPurgerContract(t, newRepos NewReposWithPrefixFunc)` in
`internal/storage/storagetest/postmenuitems_contract.go`, registered in the three
vendor test files beside `RunPostDependentsPurgerContract`
(`sqlite_test.go:107`, `mysql_test.go:80`, `postgres_test.go:80`). It reads
`vendor` and `prefix` from `newRepos`, builds its fixtures with the vendor-aware
prefixed insert helpers `postdependents_contract.go` uses, and never writes a
table name or placeholder literal. The fixture and assertions are in task 9.1.

**Cross-vendor verification (Requirement 11.4).** The M18 contract first
hardcoded a prefix and passed locally on SQLite only. Before PR 3 opens:

```sh
docker run -d --name g19-mysql -e MYSQL_ROOT_PASSWORD=grimoire_ci -e MYSQL_DATABASE=grimoire_test -p 3306:3306 mysql:8.0
docker run -d --name g19-pg -e POSTGRES_PASSWORD=grimoire_ci -e POSTGRES_USER=grimoire -e POSTGRES_DB=grimoire_test -p 5432:5432 postgres:16
# wait for both to accept connections, then the CI DSNs from .github/workflows/ci.yml:
GRIMOIRE_TEST_MYSQL_DSN='root:grimoire_ci@tcp(127.0.0.1:3306)/grimoire_test?parseTime=true&multiStatements=true' \
GRIMOIRE_TEST_POSTGRES_DSN='postgres://grimoire:grimoire_ci@127.0.0.1:5432/grimoire_test?sslmode=disable' \
go test ./internal/storage/storagetest/ -count=1 -v -run 'PostMenuItem|PostDependents'
docker rm -f g19-mysql g19-pg
```

The output must show the contract subtests passing under `mysql` and `postgres`,
not skipped. Port 3306 or 5432 already in use means another container is
running; the task picks free ports and adjusts the DSNs instead of reusing it.
The M18 contract is re-run in the same command to prove PR 3 did not disturb it.

### Service

```go
func WithPostMenuItemPurger(p domain.PostMenuItemPurger) PostWriteOption   // nil: skip
```

`Delete` calls `PurgeMenuItemsOf(ctx, cur.ID, cur.Type)` after the dependents
purge and before the row delete, for every writable type. `cur.Type` is the
stored type after the empty-type default, so a `post` row passes `post`, a page
`page`, a registered row its registry name. Failure returns before the row
delete (Requirement 10.7).

**Why before the row delete and after the dependents purge.** The row delete is
last in every M18 and M19 sequence so a failed cleanup leaves a retryable post.
The menu-item purge touches different rows from the dependents purge, so their
relative order does not matter for correctness; placing it second keeps the
rule "port calls in the order they were added" and keeps each PR's insertion a
single line.

**Idempotence.** A repeat finds no matching items and recounts nothing.

## Components and Interfaces

| Package | Change | PR |
|---|---|---|
| `internal/content/typegate.go` | `ManagedTaxonomy` | 1 |
| `internal/content/writeservices.go` | `TermWriteService.Update/Delete` gate, `Create` uses helper | 1 |
| `internal/content/postterms.go` | `SetPostTerms` taxonomy gate, `checkMembership` | 1 |
| `internal/content/rest.go` | `RESTPage.Parent` tag | 2 |
| `cmd/grimoire/testdata/builtin_golden/*.json` | regenerated, `"parent": 0` only | 2 |
| `internal/php/serialize.go` | `[]int` case | 2 |
| `internal/content/writeservices.go` | `WithStickyOptions`, `unstick`, delete step | 2 |
| `cmd/grimoire/assembly.go` | `content.WithStickyOptions(repos.Options, repos.OptionWriter)` | 2 |
| `internal/domain/repository.go` | `PostMenuItemPurger` | 3 |
| `internal/storage/wprepo/postmenuitems.go` (new), `writers.go`, `internal/storage/factory.go` | implementation, assertion, `Set.PostMenuItems` | 3 |
| `internal/storage/storagetest/postmenuitems_contract.go` (new), `sqlite_test.go`, `mysql_test.go`, `postgres_test.go` | contract and registration | 3 |
| `internal/content/writeservices.go` | `WithPostMenuItemPurger`, delete step | 3 |
| `cmd/grimoire/assembly.go` | `content.WithPostMenuItemPurger(repos.PostMenuItems)` | 3 |

Web handlers, routes and the scheduler keep their shape. Constructors gain
variadic options only.

## Data Models

No schema change and no migration. Rows read or written, by this milestone:

| Table.column | Operation | Where |
|---|---|---|
| `{prefix}terms`, `{prefix}term_taxonomy` | read (stored taxonomy) | `TermWriteService.Update/Delete`, PR 1 |
| `{prefix}term_relationships` | no write for an unmanaged taxonomy | `SetPostTerms` gate, PR 1 |
| `{prefix}options` `sticky_posts` | read, conditional write | `unstick`, PR 2 |
| `{prefix}postmeta`, `{prefix}term_relationships`, `{prefix}posts` | delete for matched menu items | `PurgeMenuItemsOf`, PR 3 |
| `{prefix}term_taxonomy.count` | recompute for `nav_menu` terms | `PurgeMenuItemsOf`, PR 3 |

JSON: REST page bodies always carry `parent`. No other body changes.

## Findings

1. **The widest term-side hole was post-term assignment, not term update.** The
   brief named update and delete. Reading `SetPostTerms` showed the admin
   `termIds` map writes relationships in any taxonomy for built-in rows. PR 1
   closes both.
2. **A caller-supplied taxonomy on term update was never checked.** `Update`
   trusted `t.Taxonomy`. The gate uses the stored taxonomy and overwrites the
   caller's.
3. **`nav_menu` counts differ slightly from WordPress for imported items.**
   WordPress counts only published and draft items; the recount counts
   relationships. Matches for rows grimoire creates; advisory otherwise.
4. **`sticky_posts` is read-modify-write.** A concurrent WordPress `stick_post`
   between grimoire's read and write can be lost. The window is two queries.
   The alternative, an atomic `UPDATE` on a serialized string, is not portable.
5. **`NavMenuRepo.DeleteItem` does not recount `term_taxonomy.count`.** The new
   purger does. They now disagree. Aligning them is a one-statement follow-up in
   the menu editor and is not part of this milestone.
6. **Deleting a post leaves relationships in unmanaged taxonomies.** A `post`
   attached to a `product_cat` term keeps the relationship on delete and the
   term count stays stale. Recorded as a gap; fixing it means detaching taxonomies
   grimoire does not manage, which this milestone's rule forbids.
7. **Admin term update validates the body before the service gate.** A request
   with no `name`, `slug` or `parent` answers `400` before the `403` for a term
   of an unmanaged taxonomy. The gate tests send a valid body.
8. **The media delete path is a fifth post-delete path** that bypasses
   `PostWriteService.Delete`, so deleting an attachment does not clean menu items
   that point at it. Recorded as a gap with the M18 media note.

## Error Handling

No new status codes, error codes or envelopes. One new service-level condition
reuses an existing sentinel.

| Condition | REST | Admin API |
|---|---|---|
| Admin term update or delete of an unmanaged taxonomy | (not reachable; 404 by taxonomy check) | `403 forbidden` |
| Term create naming an unmanaged taxonomy | `400 rest_invalid_param` (existing) | `400 bad_request` (existing) |
| Post-term assignment naming an unmanaged taxonomy | n/a | `partial` entry on a post write; `ErrTaxonomyUnknown` from the service |
| Built-in assignment with a term id of another taxonomy | `400 rest_invalid_param` | `partial` entry |
| REST term write in another taxonomy | `404 rest_term_invalid_id` | n/a |
| Term read for update or delete fails | `500` | `500` |
| `sticky_posts` read or write fails on delete | `500 rest_delete_failed` | `500` |
| `sticky_posts` malformed | delete continues; warning logged | delete continues |
| Menu-item purge fails | `500 rest_delete_failed` | `500` |
| Caller lacks capability | `403 rest_cannot_*` | `403` |

## Decisions awaiting sign-off

Defaults are implemented as written; each is local to the task named.

1. **`403` for admin term update and delete of an unmanaged taxonomy (D2).**
   Alternative: `404`. `403` matches M18's id-addressed answer and WordPress's
   `do_not_allow`. Reverse in task 2.2.
2. **Name-addressed writes stay `400` (D2).** Term create keeps its shipped `400`;
   post-term assignment adopts `ErrTaxonomyUnknown`. Alternative: `403` for
   assignment. Task 2.2.
3. **Built-in term membership check on assignment (Requirement 2.4).** New
   `400` for a term id of another taxonomy under `category` or `post_tag`.
   Alternative: gate the taxonomy only. Task 2.2.
4. **Posts keep no `parent`; custom types unchanged (D5).** Alternative: emit
   `parent` on posts, which WordPress does not. Task 6.2.
5. **Unstick for `post` rows only, malformed option left alone (D7).**
   Alternative: all types, or fail the delete on a malformed option. Task 5.2.
6. **Order: unstick before the dependents purge (D8).** Task 5.2.
7. **Sibling port `PostMenuItemPurger` (D9).** Alternative: a second method on
   `PostDependentsPurger`, which breaks every fake. Task 9.2.
8. **Child menu items are not re-parented and the menu term count is
   recomputed (D10).** Alternative: also re-parent children, which would make the
   two menu delete paths disagree. Task 9.2.
9. **Cross-vendor verification is by throwaway containers on the author's
   machine, plus CI (Requirement 11.4).** Task 9.1 and the PR 3 gate.

## Testing strategy

Pure Go over SQLite, no new dependency, no new `GRIMOIRE_TEST_*` variable;
`gofmt -l .`, `go vet ./...`, `go build ./...` and `go test ./...` green after
every phase. Storage tests also run on MySQL and PostgreSQL as described above.

**No Correctness Properties section, deliberately.** The behaviors are
enumerable branches over fixed fixtures: a closed set of taxonomies (Glossary),
three term write paths, five sticky states, one struct tag, and a menu-item
matching matrix of seven rows. There is no wide input space and no parser of
grimoire's own. The one serializer change, the `[]int` list, gets an exact
expected-string table and a round-trip test against the existing `Unserialize`
rather than a generated property. This matches the no-PBT decisions in M13, M17
and M18.

**Layers.**

- **Content services**, over the existing fakes plus a recording fake option
  store, a recording fake `PostMenuItemPurger` and a recording term writer: the
  gate for each unmanaged taxonomy on update, delete and assignment with zero
  writes recorded; stored-taxonomy-wins over a forged `t.Taxonomy`; the
  membership check; read-failure paths; each sticky branch; delete order with
  failure injected at each step.
- **`php`**: serialize table and round trip.
- **Storage contract**: the menu-item purger on every vendor, with the M18
  purger contract re-run alongside.
- **Handlers (`internal/web`)**: the `RESTPage` shape for `parent` 0 and non-zero
  on list, single, create and update; posts and custom-type pins.
- **Assembly (`cmd/grimoire`)**: through `buildHandler` over migrated SQLite with
  raw-seeded `nav_menu`, `product_cat` and `link_category` terms and menu items:
  admin term `PUT`/`DELETE` answer `403` with rows byte-identical; `termIds` with
  an unmanaged taxonomy lands in `partial`; REST term pins; menu editor still
  works; a sticky post delete updates the option; a page delete removes its menu
  items and recounts. **Wiring proof:** each option added to `buildHandler` is
  removed once and the named test must fail; each service-only change is
  reverted once and the named test must fail; the task records the result.
- **Built-in golden**: `TestBuiltinGolden` runs in every phase gate. It is
  unchanged in PR 1 and PR 3, and in PR 2 it changes once, in its own commit,
  proven by the diff check.

**Each RED test must fail for the stated reason** (`PUT` returns `204`, the
option still lists the id, the `parent` key is absent, the menu item row remains),
checked before the implementation task starts.

## Delivery

| PR | Phases | What a reviewer can use afterwards |
|---|---|---|
| 1 (security) | 1-4 | Term update, delete and post-term assignment refuse unmanaged taxonomies; REST pinned; docs and gate |
| 2 (parity) | 5-8 | `parent` always on REST pages with a bounded golden diff; deleting a sticky post unsticks it; docs and gate |
| 3 (nav menus) | 9-12 | Deleting a post, page or custom-type row deletes its menu items, recounts, proven on three databases; docs and gate |

PR 2 branches from `main` after PR 1 merges and PR 3 after PR 2, because they
edit `writeservices.go` and `assembly.go` in turn.

## Traceability

| Design element | Requirements |
|---|---|
| `ManagedTaxonomy`, `TermWriteService.Update/Delete` gate | 1 |
| `Create` helper, `SetPostTerms` gate and membership | 2 |
| REST characterization | 3 |
| Term inventory, `nav_menu` coexistence | 4 |
| PR 1 wiring | 5 |
| `RESTPage.Parent`, golden regeneration and diff check | 6 |
| Posts and custom-type pins | 7 |
| `php` list, `WithStickyOptions`, `unstick` | 8 |
| `PostMenuItemPurger`, `PostRepo.PurgeMenuItemsOf` | 9 |
| Delete step, matching matrix, recount | 10 |
| Contract and container verification | 11 |
| Golden handling, compatibility | 12 |
| `buildHandler` options and wiring proofs | 13 |
| Phases, RED/GREEN pairs, gates | 14 |
| Docs tasks | 15 |
