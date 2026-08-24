# Design — M6: Admin CRUD Editor

## Overview

M6 turns grimoire's read-only admin (M3) and read-mostly REST API (M5) into a
full write path for posts and pages. It is deliberately a **wiring and
gap-filling** milestone, not a from-scratch write layer: `internal/content`
already has `PostWriteService`/`TermWriteService` from M2, already enforcing
WordPress's own meta-capability rules. What M6 adds:

1. **Admin JSON API writes** — `POST`/`PUT`/`DELETE /admin/api/posts[/{id}]`
   and `/admin/api/terms[/{id}]`, CSRF-protected exactly like M4's comment
   moderation and media upload.
2. **REST API writes** — the same underlying services exposed at
   `/wp-json/wp/v2/posts`/`/pages`, replacing M5's `501` stubs.
3. **Three small, genuinely new backend capabilities** the write services
   don't have today: a `PostTermsWriter` port (no term-relationship write
   path exists at all before M6), `TermWriter.Update` (rename; only
   Create/Delete exist), and `Modified`-timestamp maintenance + an
   optimistic-concurrency check (today's `PostRepo.Update` doesn't touch
   `post_modified` at all).
4. **A TipTap-based rich-text editor** in the Spectrum SPA, plus new
   PostEditor/PageEditor views wired to the above.

**No new migration.** Every new capability above is additive Go
code — new interface methods, new SQL over *existing* tables
(`{prefix}posts`, `{prefix}term_relationships`, `{prefix}term_taxonomy`) — or
uses columns M5's `0004` migration already added (`post_modified`,
`post_modified_gmt`). This is a deliberate scoping choice (see
`requirements.md`'s options analysis): revisions would need genuinely new
*behavior* (a revision browser, retention) even though WordPress stores them
in the same table, so they're deferred to M7 rather than folded in here.

## Architecture

### Component view

```mermaid
flowchart TB
  subgraph Client
    SPA["Spectrum admin SPA<br/>PostEditor / PageEditor<br/>(TipTap rich-text editor)"]
    RESTClient["WP REST client<br/>(Application Password)"]
  end

  subgraph Web["internal/web"]
    AdminRoutes["adminroutes.go<br/>/admin/api/posts, /admin/api/terms (new)"]
    AdminAPI["adminapi.go / adminapi_posts.go (new) / adminapi_terms.go (new)"]
    MW["Auth + CSRF middleware<br/>SessionMiddleware · requireCapabilityJSON · requireSessionCSRF (unchanged)"]
    RESTPosts["rest_posts.go<br/>(M5 GET handlers + new write handlers)"]
  end

  subgraph Content["internal/content"]
    PWS["PostWriteService<br/>(M2, unchanged authz)"]
    TWS["TermWriteService<br/>(M2 + new Update)"]
    PTWS["PostTermsWriteService (new)"]
  end

  subgraph Domain["internal/domain"]
    PW["PostWriter<br/>(unchanged interface)"]
    TW["TermWriter<br/>+ Update (new)"]
    PTW["PostTermsWriter (new port)"]
  end

  subgraph Storage["internal/storage/wprepo"]
    PostRepo["PostRepo<br/>Create/Update: now maintain<br/>post_modified/post_modified_gmt/comment_status"]
    TermRepo["TermRepo<br/>+ Update (new)"]
    PostTermsRepo["postterms.go<br/>+ SetPostTerms (new, additive)"]
  end

  SPA -->|"credentials + X-CSRF-Token"| AdminRoutes
  RESTClient -->|"Basic auth (App Password)<br/>+ optional If-Unmodified-Since"| RESTPosts
  AdminRoutes --> MW --> AdminAPI
  AdminAPI --> PWS
  AdminAPI --> TWS
  AdminAPI --> PTWS
  RESTPosts --> PWS
  RESTPosts --> PTWS
  PWS --> PW --> PostRepo
  TWS --> TW --> TermRepo
  PTWS --> PTW --> PostTermsRepo
```

### Sequence — create a post via the admin API

```mermaid
sequenceDiagram
  participant SPA as Spectrum SPA (PostEditor)
  participant MW as web: auth + CSRF middleware
  participant H as adminapi_posts.go
  participant PWS as content.PostWriteService
  participant PTWS as content.PostTermsWriteService
  participant DB as wprepo (PostRepo / PostTermsRepo)

  SPA->>MW: POST /admin/api/posts (X-CSRF-Token, cookie, body incl. termIds)
  MW->>MW: requireLoginJSON, requireCapabilityJSON(edit_posts), requireSessionCSRF
  MW->>H: validated request
  H->>H: parse + validate body (Req 1.7): title/type/status shape
  H->>PWS: Create(ctx, actor, domain.Post{...})
  PWS->>PWS: auth.CanCreatePost(actor, type, status, author)
  alt forbidden
    PWS-->>H: ErrForbidden
    H-->>SPA: 403
  else authorized
    PWS->>DB: PostRepo.Create (sets Modified/ModifiedGMT/DateGMT = now)
    DB-->>PWS: new post ID
    PWS-->>H: id
    H->>PTWS: SetPostTerms(ctx, actor, id, taxonomy, ids) per termIds key
    PTWS->>DB: replace term_relationships for (post, taxonomy); adjust term_taxonomy.count
    H->>H: reload full detail (Req 4 shape)
    H-->>SPA: 201 {id, title, ..., modified, terms}
  end
```

### Sequence — update with optimistic-concurrency conflict

```mermaid
sequenceDiagram
  participant SPA as Spectrum SPA (PostEditor)
  participant H as adminapi_posts.go
  participant PWS as content.PostWriteService
  participant DB as wprepo.PostRepo

  SPA->>H: PUT /admin/api/posts/42 {..., modified:"2026-08-24T10:00:00Z"}
  H->>PWS: Update(ctx, actor, post, expectedModified)
  PWS->>DB: ByID(42)
  DB-->>PWS: cur (Modified = 2026-08-24T10:05:00Z, i.e. changed since load)
  PWS->>PWS: auth.CanEditPost(...) OK
  PWS->>PWS: cur.Modified != expectedModified
  PWS-->>H: ErrConflict{current: cur.Modified}
  H-->>SPA: 409 {error:{code:"conflict", message:"...", currentModified:"2026-08-24T10:05:00Z"}}
  SPA->>SPA: show reconcile dialog (Req 9.3): reload latest or keep editing
```

### Sequence — REST write with Application Password auth

```mermaid
sequenceDiagram
  participant Client as WP REST client
  participant RM as web: ApplicationPasswordAuth (M5, unchanged)
  participant H as rest_posts.go (new write handlers)
  participant PWS as content.PostWriteService

  Client->>RM: POST /wp-json/wp/v2/posts (Basic auth)<br/>optional If-Unmodified-Since
  RM->>RM: verify Application Password, resolve auth.Principal
  RM->>H: authenticated request
  H->>H: map WP-shaped body -> domain.Post
  H->>PWS: Create(ctx, actor, post)
  PWS-->>H: id or error
  alt success
    H-->>Client: 201 {id, title:{rendered}, content:{rendered}, ..., _links}
  else forbidden
    H-->>Client: 403 {code:"rest_cannot_create", ...}
  else conflict (If-Unmodified-Since mismatch, update path only)
    H-->>Client: 409 {code:"rest_conflict", data:{status:409}}
  end
```

## New and changed components

### `internal/domain` — new/changed ports

```go
// repository.go — TermWriter gains Update (new).
type TermWriter interface {
    Create(ctx context.Context, t Term) (int64, error)
    Update(ctx context.Context, t Term) error // new: rename name/slug by term_id
    Delete(ctx context.Context, id int64) error
}

// repository.go — new port. No write path for term_relationships existed
// before M6; PostTermsRepository (M5) is read-only (TermsForPost).
type PostTermsWriter interface {
    // SetPostTerms replaces postID's term relationships for taxonomy with
    // exactly termIDs (empty slice clears that taxonomy), maintaining each
    // affected term_taxonomy.count. Vendor-neutral: pure additive SQL over
    // existing term_relationships/term_taxonomy tables.
    SetPostTerms(ctx context.Context, postID int64, taxonomy string, termIDs []int64) error
}
```

`domain.Post` is unchanged (already has `Modified`/`ModifiedGMT`/
`CommentStatus` from M5). No new entity fields.

### `internal/content` — write-service changes

- `TermWriteService` gains `Update(ctx, actor, t domain.Term) error`,
  authorized by the existing `auth.CanManageTerms(actor)` — identical
  capability check to `Create`/`Delete`, just a new method.
- A new, small `PostTermsWriteService` wraps `PostTermsWriter`, authorized by
  `auth.CanEditPost` against the **target post's** current stored
  record (loaded via the existing `PostWriter.ByID`) — assigning terms is
  part of editing the post, not a separate `manage_categories` action, so it
  uses the same capability the post edit itself already required.
- `PostWriteService.Update` signature changes to accept an expected-modified
  value and return a distinct `ErrConflict` sentinel (parallel to the
  existing `ErrForbidden`) when the stored `Modified` doesn't match:

  ```go
  var ErrConflict = errors.New("content: post modified since last read")

  func (s *PostWriteService) Update(ctx context.Context, actor auth.Principal,
      p domain.Post, expectedModified time.Time) error {
      cur, err := s.w.ByID(ctx, p.ID)
      // ... existing ErrNotFound -> ErrForbidden mapping, unchanged ...
      if !expectedModified.IsZero() && !cur.Modified.Equal(expectedModified) {
          return ErrConflict
      }
      // ... existing authorize + field-merge logic, unchanged ...
      return s.w.Update(ctx, cur) // PostRepo.Update now bumps Modified itself
  }
  ```

  `expectedModified.IsZero()` is the escape hatch REST callers use when they
  omit `If-Unmodified-Since` (Req 6.5) — the admin API always supplies a
  non-zero value (Req 3.1 makes `modified` a required field), so it always
  gets the strict check.

### `internal/storage/wprepo` — write-adapter changes

- `PostRepo.Create`/`Update` (`writers.go`) are extended to set
  `post_modified`/`post_modified_gmt` to `time.Now()` (UTC for the `_gmt`
  columns) on every write, and to persist `comment_status` (already a
  `domain.Post` field, never previously written). `Update`'s `WHERE` clause
  is unchanged (`ID = ?`); the caller (`PostWriteService`) is responsible for
  the conflict check *before* calling `Update`, so `Update` itself stays a
  simple unconditional write — no compare-and-swap SQL needed because the
  service layer already loaded and checked the authoritative row a moment
  earlier in the same request. (A theoretical race between that check and
  the `Update` call is a known, accepted narrow window — see "Concurrency
  window" below — consistent with the "lightweight guard, not a hard lock"
  framing in `requirements.md`.)
- `TermRepo.Update` (new) updates `name`/`slug` by `term_id`, returning
  `ErrNotFound` via the existing `errNotFoundIfZero` helper.
- `wprepo/postterms.go` gains `SetPostTerms`, run in one transaction:
  1. `DELETE FROM {prefix}term_relationships WHERE object_id = ? AND
     term_taxonomy_id IN (SELECT term_taxonomy_id FROM {prefix}term_taxonomy
     WHERE taxonomy = ?)` — removes only this taxonomy's existing
     relationships for the post, leaving other taxonomies (e.g. clearing
     `post_tag` doesn't touch `category`).
  2. For each `termID`, `INSERT INTO {prefix}term_relationships (object_id,
     term_taxonomy_id, term_order) VALUES (?, ?, 0)`, resolving
     `term_taxonomy_id` from `(term_id, taxonomy)`.
  3. Recompute `term_taxonomy.count` for every taxonomy row touched (removed
     from or added to) via `UPDATE {prefix}term_taxonomy SET count = (SELECT
     COUNT(*) FROM {prefix}term_relationships WHERE term_taxonomy_id = ?)
     WHERE term_taxonomy_id = ?` — matching WordPress's own `count` semantics
     (number of published-or-relevant object relationships; grimoire counts
     all relationships, same simplification M1–M5 already made elsewhere for
     `count`-adjacent fields).

  All vendor-neutral (`rebind.Rebind`, same pattern as `TermRepo.Delete`).

### `internal/web` — admin API

New files `adminapi_posts.go` (create/update/delete + shared detail-shape
mapping) and `adminapi_terms.go` (term CRUD), following the existing
`adminapi_comments.go`/`adminapi_media.go` file-per-resource convention.
Routes added to `adminroutes.go`'s existing `edit_posts`-gated group (for
posts) and a new `manage_categories`-gated group (for terms):

```go
r.Group(func(gr chi.Router) {
    gr.Use(s.requireCapabilityJSON("edit_posts"))
    gr.Method(http.MethodGet, "/posts", s.jsonHandler(s.adminPosts))       // M3, unchanged
    gr.Method(http.MethodGet, "/posts/{id}", s.jsonHandler(s.adminPost))  // M3, unchanged
    gr.Group(func(wgr chi.Router) {
        wgr.Use(s.requireSessionCSRFJSON) // new: applies X-CSRF-Token check to writes only
        wgr.Method(http.MethodPost, "/posts", s.jsonHandler(s.adminPostCreate))
        wgr.Method(http.MethodPut, "/posts/{id}", s.jsonHandler(s.adminPostUpdate))
        wgr.Method(http.MethodDelete, "/posts/{id}", s.jsonHandler(s.adminPostDelete))
    })
})
r.Group(func(gr chi.Router) {
    gr.Use(s.requireCapabilityJSON("edit_posts")) // read: term list only needs edit_posts
    gr.Method(http.MethodGet, "/terms", s.jsonHandler(s.adminTerms))
})
r.Group(func(gr chi.Router) {
    gr.Use(s.requireCapabilityJSON("manage_categories"))
    gr.Use(s.requireSessionCSRFJSON)
    gr.Method(http.MethodPost, "/terms", s.jsonHandler(s.adminTermCreate))
    gr.Method(http.MethodPut, "/terms/{id}", s.jsonHandler(s.adminTermUpdate))
    gr.Method(http.MethodDelete, "/terms/{id}", s.jsonHandler(s.adminTermDelete))
})
```

`requireSessionCSRFJSON` is the existing M4 `requireSessionCSRF` header check
(unchanged logic), factored out as reusable middleware rather than an inline
per-handler call, since M6 is the first milestone with two capability-gated
groups (posts, terms) each needing the same CSRF gate layered on top —
purely a code-organization change, not a contract change (Req 8.4).

### `internal/web` — REST API

`rest_posts.go`'s `registerRESTPosts` currently registers a `501` stub for
every write verb (see M5). M6 replaces those stubs, for `post`/`page` only,
with real handlers reusing `PostWriteService`/`PostTermsWriteService`, the
same view-model mapping `handleRESTPostSingle` already uses for the response
body (title/content wrapped as `{rendered: "..."}`, `_links`, etc.). The
`If-Unmodified-Since` header (Req 6.4/6.5) is parsed as an HTTP-date; when
present it becomes the REST write's `expectedModified` argument; when absent,
the zero `time.Time` is passed, which `PostWriteService.Update` treats as
"skip the check" (matching real WordPress's lack of native REST
concurrency).

## Status codes

| Condition | Admin API | REST API |
| --- | --- | --- |
| Success (create) | `201` | `201` |
| Success (update) | `200` | `200` |
| Success (delete) | `204` | `200` (`{deleted:true, previous:{...}}`, matching WP) |
| Validation error (Req 1.7, 5.1, 5.2) | `400` | `400` (`rest_invalid_param`) |
| Missing/invalid CSRF (admin only) | `403` | n/a (Application Password requests carry no session) |
| Missing capability | `403` | `403` (`rest_cannot_create`/`rest_cannot_edit`, reusing M5's existing codes) |
| Target post/term not found | `404` | `404` (`rest_post_invalid_id`, existing M5 code) |
| Concurrency conflict (Req 3.2, 6.4) | `409` | `409` (`rest_conflict`) |
| Unauthenticated | `401` | `401` |

## Concurrency window (documented, accepted limitation)

`PostWriteService.Update` loads the current row, compares `Modified`, then
issues an unconditional `UPDATE ... WHERE ID = ?`. Two concurrent requests
that both pass the compare step before either writes could both succeed,
each unaware of the other (a classic check-then-act race). This is
explicitly accepted, not silently ignored: the requirement (Req 3) frames
this as a **lightweight guard against the common case** ("I loaded this five
minutes ago, someone else already saved a change") rather than a hard
database-level lock (`SELECT ... FOR UPDATE` / a `WHERE post_modified = ?`
compare-and-swap in the `UPDATE` itself). A true compare-and-swap `UPDATE
... WHERE ID = ? AND post_modified = ?` was considered and rejected for this
milestone: it would require every vendor's `Update` to report "0 rows
affected" ambiguity (not-found vs. conflict) distinctly, adding real
complexity for a race window that in practice is milliseconds wide in a
single-editor-at-a-time admin UI with no autosave. If genuine multi-editor
collision becomes a real problem, a compare-and-swap `Update` variant is a
natural, backwards-compatible follow-up — not blocked by anything in M6's
design.

## Rich-text editor (frontend)

`web/admin/package.json` gains `@tiptap/react`, `@tiptap/starter-kit`,
`@tiptap/extension-link`, and `@tiptap/extension-image` (all MIT-licensed,
no server-side dependency — TipTap is purely a client-side editing surface;
grimoire's Go backend only ever sees the resulting HTML string, exactly like
it already does for the M1 theme's `post_content` rendering).

```mermaid
flowchart LR
  subgraph PostEditor["PostEditor.tsx (new)"]
    Toolbar["Spectrum toolbar<br/>ActionButton/ToggleButton/Picker<br/>reflects editor.isActive(...)"]
    Surface["TipTap EditorContent<br/>(contentEditable surface)"]
  end
  useEditor["useEditor() hook<br/>@tiptap/react"] --> Surface
  Toolbar -->|"editor.chain().focus().toggleBold().run()"| useEditor
  Surface -->|"onUpdate"| LocalState["local content state"]
  LocalState -->|"Save: editor.getHTML()"| API["POST/PUT /admin/api/posts"]
  API -->|"Load: setContent(post.content)"| useEditor
```

- **Why TipTap over Lexical/Slate** (see `requirements.md` intro for the
  full rationale): TipTap's core content model is HTML in/out
  (`getHTML()`/`setContent()`), matching `post_content`'s trusted-raw-HTML
  storage exactly with no custom serialization layer. Lexical and Slate are
  both JSON-document-native; using either would require writing and
  maintaining a bespoke, lossless HTML export/import layer to preserve WP
  compatibility — extra surface with no compensating benefit for grimoire's
  use case.
- **No Spectrum-native rich-text component exists** (confirmed against the
  `@adobe/react-spectrum` component set already vendored in
  `web/admin/package.json`), so the toolbar is hand-built from ordinary
  Spectrum primitives (`ActionButton` for bold/italic/link/image,
  `ToggleButton` for list toggles, `Picker` for heading level) driven by
  `editor.isActive('bold')` etc. — this keeps the *chrome* Spectrum-native
  even though the editing surface itself (TipTap's `EditorContent`) is a
  plain `contentEditable` region TipTap manages directly.
- Image insertion (Req 7.1) opens the **existing** M4 media-library picker
  component (already built for the Media view) in a Spectrum `DialogTrigger`,
  and on selection calls `editor.chain().focus().setImage({src:
  mediaUrl}).run()` — no new upload flow.

New/changed frontend files:

```
web/admin/src/
  views/
    PostEditor.tsx        # new — create/edit posts, type="post"
    PageEditor.tsx         # new — thin wrapper around PostEditor with type="page" (Req 9.5)
    PostsList.tsx          # + New/Edit/Delete actions (Req 9.1)
  components/
    RichTextEditor.tsx     # new — TipTap wrapper + Spectrum toolbar (Req 7)
    TermPicker.tsx         # new — category/tag multi-select + inline create (Req 2)
    ConflictDialog.tsx     # new — 409 reconcile dialog (Req 9.3)
  api/
    client.ts              # + createPost/updatePost/deletePost/listTerms/createTerm/updateTerm/deleteTerm
    types.ts                # + PostDetail.modified/commentStatus/terms, TermSummary
```

## Migrations

**None.** Every new capability in this milestone is either:
- a new Go interface method over an existing table
  (`TermWriter.Update`, `PostTermsWriter.SetPostTerms`), or
- newly *writing* to columns that already exist
  (`comment_status` — present since M1's base schema; `post_modified`/
  `post_modified_gmt` — added by M5's `0004_rest_post_fields`, only ever
  *read* before M6).

This preserves the additive-only, vendor-asymmetry-aware posture M4's
`0003` and M5's `0004` established, simply by not needing a migration at
all — confirmed against all three vendor schemas
(`internal/storage/migrations/{sqlite,mysql,postgres}`), none of which are
touched by this milestone.

## Security

- **CSRF**: reused byte-for-byte from M4 (Req 8). No new middleware logic,
  only new routes wired through the existing `requireSessionCSRF` check
  (refactored into `requireSessionCSRFJSON` middleware for reuse across two
  capability groups — a pure code-organization change).
- **Authorization**: every new admin-API and REST write handler calls into
  `PostWriteService`/`TermWriteService`/`PostTermsWriteService`, which own
  100% of the capability logic; handlers never inline a capability check
  themselves (mirrors the existing M2–M5 layering — `internal/web` is thin).
- **No existence leakage**: `PostWriteService.Update`/`Delete` continue
  returning the generic `ErrForbidden` for a missing record before any
  capability check runs (existing M2 behavior, unchanged) — an
  unauthenticated-for-that-post caller cannot distinguish "doesn't exist"
  from "exists but you can't touch it" via the admin API. The REST layer
  (matching WordPress's own REST behavior of returning `404` for a missing
  ID before an ownership check) uses the existing M5 `rest_post_invalid_id`
  `404` mapping for a genuinely-missing ID, and the `403` `rest_cannot_edit`
  mapping once the record is confirmed to exist — this is an
  **intentional, pre-existing REST/admin-API asymmetry** already present in
  M5 for reads (`GET` 404s a missing post) and is not changed by M6.
- **Content trust boundary unchanged**: `post_content` remains trusted raw
  HTML end-to-end (author-supplied, rendered via `template.HTML` on the
  public side, per M1/M2/M4) — TipTap's `getHTML()` output is sent and
  stored as-is, with no new sanitization added or removed by this milestone.
  This is a deliberate continuity, not an oversight: grimoire's authoring
  model (like WordPress's own) trusts its authenticated authors, unlike the
  anonymous comment path (M4), which is untrusted and escaped/sanitized.
- **No secrets in errors**: every new handler uses the existing JSON error
  envelope (`writeJSONError`/`writeRESTError`); no SQL, driver errors, or
  internal IDs beyond what the response shape already exposes.

## Testing strategy

- **Unit (`internal/content`)**: `PostWriteService.Update` conflict-detection
  (matching `Modified` → success; mismatched → `ErrConflict`; zero
  `expectedModified` → check skipped); `TermWriteService.Update` capability
  gate; `PostTermsWriteService` capability gate (reuses `CanEditPost` against
  the target post, same as the pre-existing pattern for `Update`/`Delete`).
- **Contract (`storagetest`)**, vendor-parameterized (SQLite unconditional;
  MySQL/Postgres gated on DSNs, same convention as every prior milestone):
  `PostRepo.Create`/`Update` populate `post_modified`/`post_modified_gmt`/
  `comment_status`; `TermRepo.Update` renames and 404s a missing ID;
  `SetPostTerms` replaces one taxonomy's relationships without touching
  another's, clears with an empty slice, and updates `term_taxonomy.count`
  correctly across add/remove; identical behavior across all three vendors.
- **HTTP (`internal/web`)**: admin API — 201/200/204 happy paths; 400 for
  each Req 1.7 validation case and Req 5.2 (future-in-the-past); 403 for
  missing capability, missing/bad CSRF, and each `auth.Can*Post` denial
  case (edit another's post without `edit_others_posts`, publish without
  `publish_posts`); 404 for a nonexistent post/term on update; 409 for a
  stale `modified` value. REST API — same matrix via Application Password
  auth, plus: `If-Unmodified-Since` omitted → succeeds without a conflict
  check even when stale (Req 6.5); every other `wp/v2` write verb (media,
  users, categories, tags, comments-beyond-create) still `501`s, unchanged
  from M5 (regression guard — Req 6.6).
- **Frontend**: `RichTextEditor` renders TipTap content, toolbar buttons
  reflect `editor.isActive(...)` state, `getHTML()`/`setContent()` round-trip
  a fixture HTML string unchanged; `PostEditor` save flow calls the create
  vs. update endpoint correctly based on presence of an existing post `id`;
  `ConflictDialog` appears on a mocked `409` response and its "reload
  latest" action refetches the post.
- **E2E** (extending M3/M4's `admine2e_test.go` pattern): log in → create a
  post with a new inline category → verify it appears in `PostsList` →
  edit it (title + status → publish) → verify the public site now renders
  it → delete it → verify `404` on the public route and admin list.

## Traceability

| Requirement | Design section |
| --- | --- |
| Req 1 (post CRUD) | Admin API section, `adminapi_posts.go` |
| Req 2 (terms) | `PostTermsWriter`, `TermWriteService.Update`, `adminapi_terms.go` |
| Req 3 (concurrency) | `PostWriteService.Update` conflict check, "Concurrency window" |
| Req 4 (detail shape) | Admin API section, detail-shape table |
| Req 5 (status lifecycle) | Status codes table, validation in `adminapi_posts.go` |
| Req 6 (REST parity) | REST API section, sequence diagram |
| Req 7 (rich editor) | "Rich-text editor (frontend)" section |
| Req 8 (CSRF reuse) | Security section, `requireSessionCSRFJSON` |
| Req 9 (Spectrum views) | Frontend file list, `ConflictDialog` |
