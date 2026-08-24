# Tasks — M6: Admin CRUD Editor

Implementation checklist for M6, following strict TDD. Tasks are ordered so
each builds on the last and references the requirements it satisfies. Keep
`gofmt -l .` empty, `go vet ./...`, `go build ./...`, and `go test ./...`
green after every phase (SQLite unconditional; MySQL/Postgres contract runs
gated on `GRIMOIRE_TEST_MYSQL_DSN` / `GRIMOIRE_TEST_POSTGRES_DSN`, same
convention as every prior milestone). **M6 adds no new migration** — see
`design.md`'s Migrations section; do not add one. Every backend addition is
either a new Go interface method over an existing table, or newly writing to
columns M5's `0004` already added. Commit in logical increments with the
trailer `Co-authored-by: Copilot App <223556219+Copilot@users.noreply.github.com>`.

Requirement acceptance-criteria counts in `requirements.md` (for citation
accuracy below): Req 1 has 8, Req 2 has 6, Req 3 has 5, Req 4 has 2, Req 5
has 4, Req 6 has 7, Req 7 has 5, Req 8 has 4, Req 9 has 5.

## Phase 0 — Spec
- [x] 0.1 Write `requirements.md`, `design.md`, `tasks.md` and update the `plans/README.md` milestone index (row 06 → finalized scope, status "🚧 In progress").
  - _Acceptance:_ Three Kiro spec files exist matching M1–M5 style with Mermaid diagrams; README row 06 links to this directory and no longer shows the one-line placeholder. _(All requirements)_
- [ ] 0.2 Open the spec PR and obtain user/reviewer approval (GPT-5.5 + Kimi K3 review cycle) before coding.
  - _Acceptance:_ PR open against `main`, not merged; creator session notified; any review feedback addressed with follow-up commits before Phase 1 begins.

## Phase 1 — Domain/storage additions (`internal/domain`, `internal/storage/wprepo`)
- [ ] 1.1 Write failing unit tests for `TermRepo.Update`: renames `name`/`slug` for an existing `term_id`; returns `domain.ErrNotFound` for a nonexistent one; leaves other terms' rows untouched.
- [ ] 1.2 Add `Update(ctx context.Context, t Term) error` to the `TermWriter` interface in `internal/domain/repository.go`; implement in `wprepo/terms.go` (or wherever `TermRepo.Create`/`Delete` live), reusing the existing `errNotFoundIfZero`-style helper and vendor rebind pattern.
  - _Acceptance:_ `TermRepo.Update` contract test passes on SQLite unconditionally and MySQL/Postgres when DSNs are set. _(Req 2.3, 2.5)_
- [ ] 1.3 Write failing contract tests for `SetPostTerms` in `storagetest`: assigning `[]int64{1,2}` for `category` on a post creates exactly two `term_relationships` rows and bumps both terms' `term_taxonomy.count`; re-calling with `[]int64{2}` removes the relationship to term 1, decrements its count, and leaves term 2's count unchanged; assigning `post_tag` terms on the same post does not disturb its existing `category` relationships; assigning an empty slice clears the taxonomy entirely; a nonexistent `postID` or `termID` returns a clear error (not a silent no-op).
- [ ] 1.4 Add `PostTermsWriter` interface (`SetPostTerms(ctx, postID int64, taxonomy string, termIDs []int64) error`) to `internal/domain/repository.go`; implement `wprepo/postterms.go`'s `SetPostTerms` per `design.md`'s three-step transaction (delete-then-insert-then-recount), `rebind.Rebind`-wrapped; add compile-time `var _ domain.PostTermsWriter = (*PostTermsRepo)(nil)`; wire into `storage.Set`/`storage.New`.
  - _Acceptance:_ `SetPostTerms` contract suite passes on SQLite unconditionally and MySQL/Postgres when DSNs are set, identical results across vendors. _(Req 2.1, 2.6)_
- [ ] 1.5 Write failing unit tests for `PostRepo.Create`/`Update`: both set `post_modified`/`post_modified_gmt` to (approximately) now on every write; `Update` also persists `comment_status`; a second `Update` call bumps `Modified` again to a strictly later value than the first.
- [ ] 1.6 Extend `PostRepo.Create`/`Update` in `internal/storage/wprepo/writers.go` to set `Modified`/`ModifiedGMT` (UTC) on every write and to persist `CommentStatus`, per `design.md`.
  - _Acceptance:_ Updated contract tests pass on SQLite unconditionally and MySQL/Postgres when DSNs are set. _(Req 1.1, 1.2, 4.1)_
- [ ] 1.7 Write failing unit tests for `content.TermWriteService.Update`: succeeds when the actor has `manage_categories`; returns `content.ErrForbidden` otherwise (mirroring the existing `Create`/`Delete` capability-check pattern).
- [ ] 1.8 Add `Update(ctx, actor auth.Principal, t domain.Term) error` to `content.TermWriteService` in `internal/content/writeservices.go`, authorized via the existing `auth.CanManageTerms`.
  - _Acceptance:_ Unit tests pass; `TermWriteService` interface used by `internal/web` compiles unchanged elsewhere. _(Req 2.3)_
- [ ] 1.9 Write failing unit tests for a new `content.PostTermsWriteService`: authorizes via `auth.CanEditPost` against the **target post's current stored record** (loaded via the existing `PostWriter.ByID`, same pattern `PostWriteService.Update` already uses); succeeds when authorized; returns `content.ErrForbidden` for a nonexistent post or a post the actor cannot edit; propagates a `SetPostTerms` taxonomy/type mismatch (Req 2.6) as a `badRequestError`-compatible error.
- [ ] 1.10 Implement `content.PostTermsWriteService` wrapping `domain.PostTermsWriter`, per 1.9.
  - _Acceptance:_ Unit tests pass. _(Req 2.2, 2.6)_
- [ ] 1.11 Write failing contract tests for `TermRepo.ListByTaxonomy(ctx, taxonomy string) ([]domain.Term, error)` and `TermRepo.TermsByIDs(ctx, ids []int64) ([]domain.Term, error)`: `ListByTaxonomy` returns only terms attached to that taxonomy, sorted by name; `TermsByIDs` returns full `{id,name,slug}` objects for a given ID slice, preserving no particular order requirement, empty slice in → empty slice out (not an error); both are read-only (no rows mutated).
- [ ] 1.12 Add a `TermReader` interface (`ListByTaxonomy`, `TermsByIDs`) to `internal/domain/repository.go`, per `design.md`; implement both methods in `wprepo/terms.go` using `sqlx.In`/`rebind.Rebind` for `TermsByIDs`'s variable-length `IN` clause; add compile-time `var _ domain.TermReader = (*TermRepo)(nil)`.
  - _Acceptance:_ 1.11's contract suite passes on SQLite unconditionally and MySQL/Postgres when DSNs are set, identical results across vendors — this closes the read-port gap needed by Req 2.4 (`GET /admin/api/terms`) and Req 4.1 (resolving a post's `terms` detail field to `{id,name,slug}` objects), neither of which any existing port (`TermRepository.BySlug`, `TermRepo.CountTerms`, `PostTermsRepository.TermsForPost`) can serve. _(Req 2.4, 4.1)_
- [ ] 1.13 Write failing unit tests for `content.PostWriteService.Update`'s new concurrency parameter: given a matching `expectedModified`, the update proceeds; given a mismatched one, it returns a `*content.ConflictError` with `CurrentModified` set to the stored value, without writing; given a zero `expectedModified`, the check is skipped entirely (REST's "no `If-Unmodified-Since`" case); the existing not-found → `ErrForbidden` and capability-check behavior are unchanged for both branches; the authorization check runs *before* the `Modified` comparison (an unauthorized caller must get `ErrForbidden`, never a `409`, even when its submitted `expectedModified` is stale).
- [ ] 1.14 Change `content.PostWriteService.Update`'s signature to accept `expectedModified time.Time` and add `type ConflictError struct{ CurrentModified time.Time }` (implementing `error`), per `design.md`; update every existing internal caller (none yet outside `internal/web`, which Phase 2/3 will update).
  - _Acceptance:_ All of Phase 1's new/updated unit and contract tests pass; `go build ./...` succeeds repo-wide (temporary compile breaks in `internal/web` from the signature change are expected until Phase 2 lands — do not leave the tree broken between commits within this phase; land 1.13/1.14 together with the minimal `internal/web` call-site fixups needed to keep `go build ./...` green, without yet adding Phase 2's new endpoints). _(Req 3.1–3.3)_

## Phase 2 — Admin JSON API writes (`internal/web`)
- [ ] 2.1 Write failing HTTP tests for `POST /admin/api/posts`: 201 + full detail shape (Req 4.1) on a valid body; 400 for empty `title`, invalid `type`, invalid `status`, and `status:"future"` with a past/current `date` (Req 5.1, 5.2); defaults `status` to `draft` and `type` to `post` when omitted (Req 1.8); 403 for missing `edit_posts`/`publish_posts` per `auth.CanCreatePost`'s existing rules; 403 for missing/invalid `X-CSRF-Token`.
- [ ] 2.2 Implement `adminapi_posts.go`'s `adminPostCreate` handler and its shared request-body/detail-shape mapping (Req 4), wired via `PostWriteService.Create` then `PostTermsWriteService.SetPostTerms` per submitted `termIds` map key (Req 2.2).
- [ ] 2.3 Write failing HTTP tests for `PUT /admin/api/posts/{id}`: 200 + updated detail shape on a valid body with matching `modified`; 409 with `currentModified` in the body when `modified` is stale (Req 3.2); 403 for a nonexistent ID (matching the existing `PostWriteService.Update` no-existence-leak behavior — same generic `forbidden` response as an authorization failure, not `404`); 400 for a missing/malformed `modified` field (Req 3.1 — admin API requires it, unlike REST); same validation/authorization matrix as 2.1 for the update-specific capability rules (`edit_others_posts`, `edit_published_posts`, `edit_private_posts`).
- [ ] 2.4 Implement `adminPostUpdate`, threading `modified` into `PostWriteService.Update`'s new `expectedModified` parameter, authorizing *before* comparing `Modified` (see `design.md`'s ordering note — this is required to avoid leaking a post's current `Modified` value to an unauthorized caller via the 409 body), and mapping `*content.ConflictError` (via `errors.As`) to a `409` body containing `currentModified`.
- [ ] 2.5 Write failing HTTP tests for `DELETE /admin/api/posts/{id}`: 204 on success; 403 for a nonexistent ID (same no-existence-leak rationale as 2.3); 403 for missing `delete_posts`/CSRF.
- [ ] 2.6 Implement `adminPostDelete`.
  - _Acceptance:_ 2.1–2.6's HTTP suite passes; `internal/web` compiles cleanly against Phase 1's new `PostWriteService.Update` signature. _(Req 1.1–1.8, 3.1–3.3, 4.1, 4.2, 5.1, 5.2, 5.4)_
- [ ] 2.7 Write failing HTTP tests for `GET /admin/api/terms?taxonomy=category|post_tag`: returns `{id,name,slug}` entries sorted by name; 400 for an unrecognized `taxonomy` query value.
- [ ] 2.8 Write failing HTTP tests for `POST /admin/api/terms`, `PUT /admin/api/terms/{id}`, `DELETE /admin/api/terms/{id}`: 201/200/204 happy paths; 404 on `PUT`/`DELETE` for a nonexistent ID (Req 2.5); 403 for missing `manage_categories`/CSRF.
- [ ] 2.9 Implement `adminapi_terms.go` (`adminTerms`, `adminTermCreate`, `adminTermUpdate`, `adminTermDelete`), wiring `adminTerms` to the new `TermReader.ListByTaxonomy` (Req 2.4) and the writes to `TermWriteService`; resolve `PostDetail.terms` (Req 4.1) via `TermReader.TermsByIDs` in the shared post-detail mapping from 2.2.
- [ ] 2.10 Add the new routes to `adminroutes.go` per `design.md`'s route table, including the new `csrfJSONMiddleware` chi-middleware adapter around the existing M4 `requireSessionCSRFJSON` helper (`authmiddleware.go:115`, unchanged logic/signature — the adapter is the only new code), applied at the group level for the posts and terms write groups instead of per-handler.
  - _Acceptance:_ 2.7–2.10's HTTP suite passes; existing M3/M4 admin API HTTP tests (comments, media, menus, read-only posts) still pass unchanged, confirming the `csrfJSONMiddleware` adapter didn't change `requireSessionCSRFJSON`'s underlying behavior. _(Req 2.1, 2.3–2.5, 8.1–8.4)_

## Phase 3 — REST API writes (`internal/web/rest_posts.go`)
- [ ] 3.1 Write failing HTTP tests for `POST /wp-json/wp/v2/posts` and `/pages`: 201 + WordPress-shaped body on a valid request via Application Password auth; 403 (`rest_cannot_create`) for insufficient capability; replaces (does not merely wrap) M5's `501` stub for this route/verb — assert the response is no longer `501` for exactly these two resources.
- [ ] 3.2 Write failing HTTP tests for `PUT`/`PATCH /wp-json/wp/v2/posts/{id}` and `/pages/{id}`: 200 on a normal update; 409 (`rest_conflict`) when `If-Unmodified-Since` is present and stale (mapping `*content.ConflictError` via `errors.As`, same as the admin API); 200 (last-write-wins, no conflict raised) when `If-Unmodified-Since` is **omitted** even though the record has changed since an earlier read (Req 6.5 — the deliberate WP-parity gap vs. the admin API).
- [ ] 3.3 Write failing HTTP tests for `DELETE /wp-json/wp/v2/posts/{id}` and `/pages/{id}`: 200 with `{deleted:true, previous:{...}}` per Req 6.3.
- [ ] 3.4 Write a failing regression test asserting every **other** `wp/v2` write verb (media, users, categories, tags, and any comment verb beyond M5's existing create) still returns M5's exact `501` body, unchanged (Req 6.6).
- [ ] 3.5 Implement the `posts`/`pages` write handlers in `rest_posts.go`, mapping WP-shaped request bodies to `domain.Post` (via `PostWriteService.Create`/`Update`/`Delete` only — **no** term-ID assignment; REST-created/updated posts have no category/tag relationships in M6, per Req 6.1/6.8, since term writes stay admin-API-only) and parsing `If-Unmodified-Since` as an HTTP-date into `PostWriteService.Update`'s `expectedModified` (zero value when absent), reusing the existing single-item response mapping `handleRESTPostSingle` already has for `GET`.
- [ ] 3.6 Enforce the existing M5 TLS/loopback-only Application Password posture on the new write routes (no relaxation) — extend the existing enforcement test to cover a write verb, not just `GET`.
  - _Acceptance:_ 3.1–3.6's HTTP suite passes; the full `internal/web` REST test suite (M5's existing read tests + M6's new write tests) is green together. _(Req 6.1–6.7)_

## Phase 4 — Rich-text editor and Spectrum views (`web/admin`)
- [ ] 4.1 Add `@tiptap/react`, `@tiptap/starter-kit`, `@tiptap/extension-link`, `@tiptap/extension-image` to `web/admin/package.json`.
- [ ] 4.2 Write failing component tests for `RichTextEditor.tsx`: renders initial HTML content; `onChange` fires with updated HTML after a simulated toolbar action (e.g. toggling bold on selected text); toolbar buttons reflect `editor.isActive('bold')`/`isActive('italic')`/etc. state; loading a new `content` prop calls `setContent` and replaces the surface's content (round-trip fidelity for a fixture HTML string, Req 7.1).
- [ ] 4.3 Implement `components/RichTextEditor.tsx`: `useEditor()` wrapper, Spectrum `ActionButton`/`ToggleButton`/`Picker` toolbar per `design.md`'s component tree, image-insert action opening the new `MediaPicker` dialog (see 4.4a/4.4b below) in a `DialogTrigger` and calling `setImage({src})` on selection (Req 7.1, 7.2, 7.3).
  - _Acceptance:_ 4.2's component suite passes. _(Req 7.1–7.4)_
- [ ] 4.3a Write failing component tests for `MediaPicker.tsx`: fetches and lists existing media from `GET /admin/api/media` (the existing M4 endpoint); selecting an item invokes the provided `onSelect(mediaUrl)` callback and closes the dialog; empty-library and loading states render without error (Req 7.1).
- [ ] 4.3b Implement `components/MediaPicker.tsx` as a new Spectrum `Dialog` — there is no existing reusable picker to extend (M4's `Media.tsx` is a standalone full-page list view with no selection callback), so this is net-new UI backed entirely by the existing M4 media-list API, not new backend work.
  - _Acceptance:_ 4.3a's suite passes. _(Req 7.1)_
- [ ] 4.4 Write failing component tests for `TermPicker.tsx`: lists existing terms fetched from `GET /admin/api/terms`; selecting entries updates the parent's term-ID state; submitting a new term name calls `POST /admin/api/terms` and adds the result to the selection (Req 2.2).
- [ ] 4.5 Implement `components/TermPicker.tsx`.
  - _Acceptance:_ 4.4's suite passes. _(Req 2.2)_
- [ ] 4.6 Write failing component tests for `ConflictDialog.tsx`: renders on a mocked `409` response body; "reload latest" action calls the provided refetch callback; "keep editing" dismisses without discarding local unsaved changes (Req 9.3).
- [ ] 4.7 Implement `components/ConflictDialog.tsx`.
  - _Acceptance:_ 4.6's suite passes. _(Req 9.3)_
- [ ] 4.8 Extend `api/client.ts`/`api/types.ts` with `createPost`/`updatePost`/`deletePost`/`listTerms`/`createTerm`/`updateTerm`/`deleteTerm` and the extended `PostDetail`/`TermSummary` types matching `design.md`'s detail shape (Req 4.1).
- [ ] 4.9 Write failing component tests for `PostEditor.tsx`: loads an existing post's fields into `RichTextEditor`/`TermPicker`/status `Picker`; "Save" calls `createPost` when no existing `id` is present and `updatePost` (with the loaded `modified` value) otherwise; a `409` response opens `ConflictDialog`; a `403` response surfaces a Spectrum error notification (Req 9.4) without navigating away.
- [ ] 4.10 Implement `views/PostEditor.tsx`, per `design.md`'s Toolbar/Surface/save-flow diagram.
- [ ] 4.11 Implement `views/PageEditor.tsx` as a thin `PostEditor` wrapper with `type="page"` and no term picker (pages have no categories/tags), plus its list-view wiring, per Req 9.5.
- [ ] 4.12 Extend `views/PostsList.tsx` with "New post"/"Edit"/"Delete" actions routing to `PostEditor` and calling `deletePost`, per Req 9.1.
  - _Acceptance:_ 4.9–4.12's component suites pass; `npm run build` (or the project's existing frontend build command) succeeds for `web/admin`. _(Req 9.1, 9.2, 9.4, 9.5)_

## Phase 5 — End-to-end wiring and documentation
- [ ] 5.1 Extend the existing `admine2e_test.go`-style suite (or add a sibling) with a full-stack scenario: log in → create a post with a newly-created inline category → confirm it appears in `PostsList` → edit it (change title, transition `draft` → `publish`) → confirm the public site now renders it (reusing M1/M2's public-route rendering) → delete it → confirm `404` on both the public route and the admin detail endpoint.
- [ ] 5.2 Add an equivalent REST-only E2E scenario using an Application Password: create → update (with a correct `If-Unmodified-Since`) → conflict (stale `If-Unmodified-Since`) → delete, asserting response shapes match `design.md`'s status-code table.
- [ ] 5.3 Update `README.md`'s feature summary (if it enumerates completed milestones' capabilities, matching the pattern from M3/M4/M5's entries) to mention post/page admin editing and REST write parity.
- [ ] 5.4 Final full-repo pass: `gofmt -l .` empty, `go vet ./...`, `go build ./...`, `go test ./...` (SQLite unconditional; MySQL/Postgres when DSNs set) green; frontend build/test green; confirm `plans/README.md` row 06 accurately reflects the shipped scope (no drift from what Phase 1–4 actually built).
  - _Acceptance:_ Full test suite green across all three DB vendors where applicable; E2E scenarios from 5.1/5.2 pass; milestone considered complete and ready for the next spec (M7 candidates: revisions, scheduled-publish execution, REST categories/tags). _(All requirements)_
