# M8 Content Browsing Parity — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement M8 from `plans/wordpress-core-parity-roadmap/{requirements,design,tasks}.md`: shared pagination contract, public home/category pagination with correct out-of-range 404s, admin post search/status/author filters, media filter+grid/list parity, and the reusable Spectrum pagination/filter controls that back all of it — with zero schema changes.

**Architecture:** Every read path (public HTML, admin JSON, media JSON) converges on one `content.Page{Page,PerPage,Total,TotalPages}` contract (`TotalPages=0` when `Total=0`, matching the existing `AdminService.List` convention). Existing service constructors are never broken: `PostService`/`AdminService`/`MediaService` gain new methods and an additive `WithCounter` setter rather than changed signatures. Repository interfaces widen additively (`TermRepository.CountPublishedByTermSlug`, `AdminPostRepository.Authors`, `MediaFilter{Search,Type,After,Before}`). Frontend consolidates pagination into one `PaginationBar` component (extracted from `PostsList.tsx`'s existing inline block, not new) reused by `PostsList.tsx` and the rebuilt `Media.tsx`.

**Tech Stack:** Go 1.22, `chi` router, `uptrace/bun` (SQLite/MySQL/Postgres via `internal/storage/wprepo`), Go `html/template` (`internal/render`), React 18 + TypeScript, Adobe React Spectrum, Vitest + Testing Library.

**Scope:** M8 only. M9 (routing/taxonomy) and M10 (REST write/content safety) are explicitly out of scope and untouched by every task below.

**Accessibility/responsive scope:** Automated coverage in this plan is Testing Library role/label queries inside Vitest (keyboard-operable controls, correct accessible names) — this is what "accessibility tests" means in every task below. Full manual side-by-side desktop/mobile visual review against WordPress (per Req 9) is a separate, explicit step in Task 9 and is not automatable; it is not claimed as covered by any unit test.

**Migration safety:** No task modifies `internal/storage/migrations/**` (the
actual per-vendor SQL files, under `mysql/`, `postgres/`, `sqlite/`) or
`internal/storage/migrate/**` (the Go migration-runner package) — no task
adds an SQL migration file. Task 9 verifies this with `git diff
<merge-base>..HEAD -- internal/storage/migrations internal/storage/migrate`
returning empty, run against the actual merge-base with `main` (`ac92650`),
not a `git status` snapshot of the working tree (which is uninformative once
everything is committed).

---

## File Responsibility Map

| File | Responsibility | Task |
|---|---|---|
| `internal/content/pagination.go` | Widen `clamp` to return clamped page; add shared `Page` struct + `newPage` helper | 1 |
| `internal/content/pagination_test.go` | New file — tests for `newPage`/`Page`/`TotalPages` | 1 |
| `internal/content/post.go` (+ `post_test.go`, append) | Add `PostService.WithCounter` + `RecentPage`; `Recent` unchanged | 1 |
| `internal/content/term.go` (+ `term_test.go`, append) | Add `TermService.CategoryPage`; `Category`/constructor unchanged | 1 |
| `internal/domain/repository.go` | Add `TermRepository.CountPublishedByTermSlug`; add `AdminPostFilter.Author`, `AdminPostRepository.Authors`, `AuthorOption`; widen `MediaFilter` | 1, 4A, 6 |
| `internal/storage/wprepo/repo.go` | Implement `TermRepo.CountPublishedByTermSlug` | 1 |
| `internal/storage/factory.go` | No signature change; compile-gate citations only (`:149` Terms, `:170` AdminPosts) | 1, 4A |
| `internal/render/view.go` | Add named `Pagination content.Page` field to `IndexData`/`CategoryData` | 2, 3 |
| `internal/web/handlers.go` (+ `handlers_test.go`) | `home`/`category` call `RecentPage`/`CategoryPage`; return `domain.ErrNotFound` when out of range; new pagination/404 tests | 2, 3 |
| `themes/default/templates/index.tmpl`, `category.tmpl` | Add pagination nav markup | 2, 3 |
| `internal/render/testdata/golden/index.html`, `category.html` | Regenerated via `-update` after template edits (existing golden fixtures render with `TotalPages==0`, so bytes only shift by template whitespace) | 2, 3 |
| `internal/content/adminread.go` | Task 1: mechanical `clamp` call-site fix only, no behavior change; Task 4: `AdminListFilter` struct + `AdminService.List` forwards full filter; Task 4A: `AdminService.Authors` delegation | 1, 4, 4A |
| `internal/content/adminread_test.go` | Existing file (213 lines, 7 tests) — Modify, never recreate. Task 4 migrates the 3 existing old-signature `svc.List` call sites to `AdminListFilter` and appends 2 new filter-forwarding tests on the existing `fakeAdminData`/`newAdminService` helpers; Task 4A additively widens `fakeAdminData` with an `authors` closure + `Authors` method and appends 2 more tests. All 7 pre-existing tests and helpers are preserved unchanged. | 4, 4A |
| `internal/render/engine.go` (+ `engine_test.go`, append) | Add `templateFuncs` `template.FuncMap` (`add`/`sub`) and wire via `.Funcs(templateFuncs)` before `ParseFiles`, so the pagination nav markup (`{{add .Pagination.Page 1}}`) parses | 2 |
| `internal/storage/wprepo/adminreads.go` | Inline `Author` predicate in `ListForAdmin`/`CountForAdmin`; new `PostRepo.Authors` query | 4A |
| `internal/web/adminapi.go` | `adminReader.List`/`.Authors` signatures; `adminPosts` parses `author`; new `adminAuthors` handler | 4, 4A |
| `internal/web/adminapi_test.go` | Update `fakeAdmin.List`/`Authors`; new author-filter test | 4, 4A |
| `internal/web/adminapi_terms_test.go` | `fakeAdmin{list: ...}` closure signature only (compile fix, no behavior/test-outcome change) | 4 |
| `internal/web/adminroutes.go` | Register `GET /admin/api/authors` in the existing `edit_posts` group | 4A |
| `internal/domain/adminrepo_test.go` | `adminFake.Authors` method | 4A |
| `internal/storage/storagetest/admin_contract.go` | Existing file (`runAdminContract`); add search-filtered + author-filtered regression subtests | 4, 4A |
| `web/admin/src/test-utils.tsx` | New file — shared `renderWithSpectrum` helper used by every frontend test below | 5 |
| `web/admin/src/components/PaginationBar.tsx` (+ `.test.tsx`) | New shared pagination control | 5 |
| `internal/storage/wprepo/media.go` | Shared `mediaWhere` predicate helper used by `listQuery` and `Count`; title-or-filename search | 6 |
| `internal/web/adminapi_media.go` | Parse `search/type/after/before`; replace manual `TotalPages` clamp-to-1 with `content.TotalPages` (zero-result = 0) | 6 |
| `internal/web/adminapi_media_test.go` | New file — handler-level filter/400/clamp tests (no such file exists today) | 6 |
| `internal/storage/storagetest/media_contract.go` | Existing file (`runMediaContract`); add filtered-totals regression subtests | 6 |
| `web/admin/src/api/client.ts`, `types.ts` | Extend `posts`/`media` param+response shapes | 4, 6, 7 |
| `web/admin/src/views/PostsList.tsx` (extend existing) + `.test.tsx` (new file — no existing file) | Add search/status/author filters; consume `PaginationBar` | 7 |
| `web/admin/src/views/Media.tsx` (+ `.test.tsx`, new) | Add search/type/date-range/parent-post filters, mutually-exclusive grid/list toggle (URL-persisted per Req 6.4), `PaginationBar` | 7 |
| `docs/compatibility.md`, `docs/wordpress-compatibility-tour.md` | Mark M8-covered gaps resolved | 8 |

---
## Task 1: Shared pagination contract + additive PostService/TermService changes

**Requirements:** Req 8.1 (Page/PerPage/Total/TotalPages shape, TotalPages=0 when Total=0), Req 8.2 (no breaking signature changes)

**Files:**
- Modify: `internal/content/pagination.go` (current: 23 lines, `clamp(page,perPage int)(limit,offset int)`)
- Modify: `internal/content/post.go:10-24` (current, verbatim above)
- Modify: `internal/content/term.go:13-37` (current, verbatim above)
- Modify: `internal/domain/repository.go` (add to `TermRepository` interface)
- Modify: `internal/storage/wprepo/repo.go` (add method near `TermRepo.BySlug`, line 170)
- Modify: `internal/content/adminread.go:73-76` (mechanical `clamp` call-site fix only, so this task's commit compiles standalone; `AdminListFilter`/`Authors` land later in Task 4/4A)
- Test: `internal/content/pagination_test.go` (new), `internal/content/post_test.go` (append), `internal/content/term_test.go` (append)

### Step 1: Add the shared `Page` struct and widen `clamp`

`clamp` currently returns `(limit, offset int)`. `AdminService.List` (`internal/content/adminread.go:72-76`) separately re-derives a 1-based `page` via its own `if page < 1 { page = 1 }`. Fold that into `clamp` so every caller gets the clamped page for free, and add the shared `Page`/`newPage` that Req 8.1 requires everywhere (public home/category and admin/media alike).

Write `internal/content/pagination.go` in full:

```go
package content

// Pagination defaults for post listings.
const (
	DefaultPerPage = 10
	MaxPerPage     = 100
)

// clamp converts a possibly out-of-range page and a requested per-page size
// into a SQL limit/offset plus the clamped 1-based page. page < 1 is treated
// as page 1. perPage <= 0 defaults to DefaultPerPage; perPage > MaxPerPage is
// capped at MaxPerPage.
func clamp(page, perPage int) (limit, offset, clampedPage int) {
	if page < 1 {
		page = 1
	}
	switch {
	case perPage <= 0:
		perPage = DefaultPerPage
	case perPage > MaxPerPage:
		perPage = MaxPerPage
	}
	return perPage, (page - 1) * perPage, page
}

// Page is the pagination contract shared by every paginated read path: public
// home/category, admin post list, and admin media list (Req 8.1). TotalPages
// is 0 when Total is 0 (an empty result set has no pages), matching
// AdminService.List's existing convention -- never clamped up to 1.
type Page struct {
	Page       int
	PerPage    int
	Total      int
	TotalPages int
}

// newPage builds a Page from a clamped page/limit and a total row count.
func newPage(page, perPage, total int) Page {
	return Page{Page: page, PerPage: perPage, Total: total, TotalPages: TotalPages(total, perPage)}
}

// TotalPages computes the page count for a total row count and page size,
// matching newPage's convention: 0 when total is 0 or perPage is
// non-positive, never rounded up to 1. Exported for callers that already
// hold Total/PerPage from elsewhere (e.g. an admin JSON handler after a
// repository Count call) and only need the page count, not a full Page.
func TotalPages(total, perPage int) int {
	if perPage <= 0 || total <= 0 {
		return 0
	}
	return (total + perPage - 1) / perPage
}
```

Update the three existing `clamp` callers to accept the new third return value:

`internal/content/post.go:22` — `limit, offset := clamp(page, perPage)` → `limit, offset, _ := clamp(page, perPage)` (the clamped page isn't needed by `Recent`, which stays behavior-identical).

`internal/content/term.go:31` — same edit: `limit, offset, _ := clamp(page, perPage)`.

`internal/content/adminread.go:73` — `limit, offset := clamp(page, perPage)` → `limit, offset, page := clamp(page, perPage)` and delete the now-redundant `if page < 1 { page = 1 }` on the following line. This mechanical signature-compatibility edit **is made in this task's own Step 1c below**, to keep this commit green on its own without waiting for Task 4; Task 4 Step 1 edits these same lines again afterward to replace the whole filter-construction block with `AdminListFilter`.

- [ ] **Step 1a: Write the failing test**

Create `internal/content/pagination_test.go`:

```go
package content

import "testing"

func TestClampReturnsClampedPage(t *testing.T) {
	cases := []struct {
		name                        string
		page, perPage               int
		wantLimit, wantOffset, wantPage int
	}{
		{"defaults", 0, 0, 10, 0, 1},
		{"negative page clamps to 1", -5, 5, 5, 0, 1},
		{"page 3", 3, 10, 10, 20, 3},
		{"perPage capped", 1, 500, 100, 0, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			limit, offset, page := clamp(tc.page, tc.perPage)
			if limit != tc.wantLimit || offset != tc.wantOffset || page != tc.wantPage {
				t.Fatalf("clamp(%d,%d) = (%d,%d,%d), want (%d,%d,%d)",
					tc.page, tc.perPage, limit, offset, page, tc.wantLimit, tc.wantOffset, tc.wantPage)
			}
		})
	}
}

func TestNewPageZeroTotalHasZeroTotalPages(t *testing.T) {
	p := newPage(1, 10, 0)
	if p.TotalPages != 0 {
		t.Fatalf("TotalPages = %d, want 0 for Total=0", p.TotalPages)
	}
	if p.Page != 1 || p.PerPage != 10 || p.Total != 0 {
		t.Fatalf("unexpected Page fields: %+v", p)
	}
}

func TestNewPageComputesTotalPages(t *testing.T) {
	p := newPage(2, 10, 25)
	if p.TotalPages != 3 {
		t.Fatalf("TotalPages = %d, want 3 for Total=25 PerPage=10", p.TotalPages)
	}
}

func TestTotalPagesZeroWhenTotalZero(t *testing.T) {
	if got := TotalPages(0, 10); got != 0 {
		t.Fatalf("TotalPages(0, 10) = %d, want 0", got)
	}
}

func TestTotalPagesComputes(t *testing.T) {
	if got := TotalPages(25, 10); got != 3 {
		t.Fatalf("TotalPages(25, 10) = %d, want 3", got)
	}
}
```

- [ ] **Step 1b: Run test to verify it fails (function doesn't exist yet with the new signature)**

Run: `go vet ./internal/content/...`
Expected: FAIL — `clamp(tc.page, tc.perPage)` returns 2 values, assignment mismatch; `newPage`/`TotalPages` undefined.

- [ ] **Step 1c: Apply the `pagination.go` rewrite above, and update the two non-adminread.go callers**

Edit `internal/content/post.go:22`:
```go
	limit, offset, _ := clamp(page, perPage)
```

Edit `internal/content/term.go:31`:
```go
	limit, offset, _ := clamp(page, perPage)
```

`internal/content/adminread.go:73` also calls `clamp`, so widening its signature would leave this file failing to compile until Task 4 lands, if left untouched here. Fix the call site now, in this same commit, without changing `AdminService.List`'s observable behavior (Task 4 Step 1 edits these same lines again later to add `AdminListFilter`):

Edit `internal/content/adminread.go:73-76`:
```go
	limit, offset, page := clamp(page, perPage)
	f := domain.AdminPostFilter{Limit: limit, Offset: offset}
```
(deletes the redundant `if page < 1 { page = 1 }` two lines below — `clamp` now does that.)

- [ ] **Step 1d: Run tests to verify they pass**

Run: `go vet ./internal/content/... && go test ./internal/content/... -run 'TestClamp|TestNewPage|TestTotalPages' -v`
Expected: PASS, all five new tests green.

- [ ] **Step 1e: Commit**

```bash
git add internal/content/pagination.go internal/content/pagination_test.go internal/content/post.go internal/content/term.go internal/content/adminread.go
git commit -m "content: widen clamp and add shared Page pagination contract"
```

### Step 2: Additive `PostService.WithCounter` + `RecentPage`

`domain.PostCounter` already exists (`CountByStatus(ctx, typ, status string) (int, error)`), already wired at `internal/storage/factory.go:171` as `PostCounter: posts,`. `PostService` just needs an optional field and a new method — `NewPostService`'s signature and all 20 existing call sites are untouched.

- [ ] **Step 2a: Write the failing test**

Append to `internal/content/post_test.go` (reusing the existing `fakePostRepo`; add a tiny local counter fake):

```go
type fakePostCounter struct {
	typ, status string
	count       int
	err         error
}

func (f *fakePostCounter) CountByStatus(ctx context.Context, typ, status string) (int, error) {
	f.typ, f.status = typ, status
	return f.count, f.err
}

func TestPostServiceRecentPageReturnsPageWithTotal(t *testing.T) {
	repo := &fakePostRepo{recentPosts: []domain.Post{{ID: 1}, {ID: 2}}}
	counter := &fakePostCounter{count: 25}
	svc := NewPostService(repo).WithCounter(counter)

	posts, page, err := svc.RecentPage(context.Background(), 2, 10)
	if err != nil {
		t.Fatalf("RecentPage: %v", err)
	}
	if len(posts) != 2 {
		t.Fatalf("posts = %d, want 2", len(posts))
	}
	if page.Page != 2 || page.PerPage != 10 || page.Total != 25 || page.TotalPages != 3 {
		t.Fatalf("page = %+v, want {2 10 25 3}", page)
	}
	if repo.recentLimit != 10 || repo.recentOffset != 10 {
		t.Fatalf("repo called with limit=%d offset=%d, want 10,10", repo.recentLimit, repo.recentOffset)
	}
	if counter.typ != "post" || counter.status != "publish" {
		t.Fatalf("counter called with typ=%q status=%q, want post/publish", counter.typ, counter.status)
	}
}

func TestPostServiceRecentPageZeroTotal(t *testing.T) {
	repo := &fakePostRepo{}
	counter := &fakePostCounter{count: 0}
	svc := NewPostService(repo).WithCounter(counter)
	_, page, err := svc.RecentPage(context.Background(), 1, 10)
	if err != nil {
		t.Fatalf("RecentPage: %v", err)
	}
	if page.TotalPages != 0 {
		t.Fatalf("TotalPages = %d, want 0", page.TotalPages)
	}
}
```

- [ ] **Step 2b: Run test to verify it fails**

Run: `go vet ./internal/content/...`
Expected: FAIL — `WithCounter`/`RecentPage` undefined on `*PostService`.

- [ ] **Step 2c: Implement**

Edit `internal/content/post.go`, replacing the whole file:

```go
package content

import (
	"context"

	"github.com/roboweaver/grimoire/internal/domain"
)

// PostService orchestrates post/page reads for the web layer.
type PostService struct {
	posts domain.PostRepository
	pc    domain.PostCounter // optional; set via WithCounter for RecentPage
}

// NewPostService constructs a PostService over a PostRepository. Unchanged
// signature -- all 20 existing call sites are untouched.
func NewPostService(p domain.PostRepository) *PostService {
	return &PostService{posts: p}
}

// WithCounter opts a PostService into RecentPage support by attaching a
// PostCounter. Returns the receiver so it can be chained at construction time
// (e.g. content.NewPostService(repos.Posts).WithCounter(repos.PostCounter)).
func (s *PostService) WithCounter(pc domain.PostCounter) *PostService {
	s.pc = pc
	return s
}

// Recent returns published posts for a 1-based page, clamping the page size to
// [1, MaxPerPage] with DefaultPerPage when unset. Unchanged behavior.
func (s *PostService) Recent(ctx context.Context, page, perPage int) ([]domain.Post, error) {
	limit, offset, _ := clamp(page, perPage)
	return s.posts.RecentPosts(ctx, limit, offset)
}

// RecentPage is Recent plus a Page pagination contract (Req 8.1), for callers
// that need total/out-of-range information (the public home page). Requires
// WithCounter to have been called; panics on a nil pc, matching Go's normal
// nil-pointer-dereference behavior for an unwired dependency rather than
// silently returning a zero Page.
func (s *PostService) RecentPage(ctx context.Context, page, perPage int) ([]domain.Post, Page, error) {
	limit, offset, clampedPage := clamp(page, perPage)
	posts, err := s.posts.RecentPosts(ctx, limit, offset)
	if err != nil {
		return nil, Page{}, err
	}
	total, err := s.pc.CountByStatus(ctx, "post", "publish")
	if err != nil {
		return nil, Page{}, err
	}
	return posts, newPage(clampedPage, limit, total), nil
}

// BySlug resolves a single published post or page by slug. domain.ErrNotFound
// is propagated for unknown or non-published slugs.
func (s *PostService) BySlug(ctx context.Context, slug string) (domain.Post, error) {
	return s.posts.BySlug(ctx, slug, "post", "page")
}
```

- [ ] **Step 2d: Run tests to verify they pass**

Run: `go vet ./internal/content/... && go test ./internal/content/... -run TestPostServiceRecentPage -v`
Expected: PASS.

- [ ] **Step 2e: Commit**

```bash
git add internal/content/post.go internal/content/post_test.go
git commit -m "content: add PostService.WithCounter and RecentPage"
```

### Step 3: `TermRepository.CountPublishedByTermSlug` + `TermService.CategoryPage`

No `TermService` constructor change. Widen `domain.TermRepository` (currently only `BySlug`), implement in `wprepo.TermRepo` by mirroring `PostRepo.ByTermSlug`'s join chain (`internal/storage/wprepo/repo.go:130-151`), and add `TermService.CategoryPage` using the already-held `terms`/`posts` fields.

- [ ] **Step 3a: Write the failing interface + repo test**

Edit `internal/domain/repository.go`, in the `TermRepository` interface (currently only `BySlug`):

```go
// TermRepository resolves taxonomy terms.
type TermRepository interface {
	// BySlug returns the term for a taxonomy/slug pair, or ErrNotFound.
	BySlug(ctx context.Context, taxonomy, slug string) (Term, error)
	// CountPublishedByTermSlug returns the number of published posts related
	// to a taxonomy term (Req 8.1's Total for the category page). Pure
	// COUNT(*); no writes.
	CountPublishedByTermSlug(ctx context.Context, taxonomy, termSlug string) (int, error)
}
```

`internal/content/term_test.go`'s `fakeTermRepo` is passed to `NewTermService(t domain.TermRepository, ...)` (`internal/content/term.go:19`), so it must implement the widened interface too, or `go vet ./...` below fails on the `internal/content` package, not just `internal/storage`. Widen it in this same step, before running `go vet`:

```go
type fakeTermRepo struct {
	tax, slug      string
	term           domain.Term
	err            error
	called         bool
	countPublished int
}
```

```go
func (f *fakeTermRepo) CountPublishedByTermSlug(ctx context.Context, taxonomy, termSlug string) (int, error) {
	return f.countPublished, f.err
}
```

This widens an interface implemented by `wprepo.TermRepo` and referenced via `storage.Set.Terms domain.TermRepository` (`internal/storage/factory.go:27`, constructed at `factory.go:149` `Terms: terms,`). Before implementing `wprepo.TermRepo`'s new method, confirm the exact expected compile failure:

Run: `go vet ./internal/storage/...`
Expected: FAIL at `internal/storage/factory.go:149:3: cannot use terms (variable of type *wprepo.TermRepo) as domain.TermRepository value in struct literal: *wprepo.TermRepo does not implement domain.TermRepository (missing method CountPublishedByTermSlug)`.

- [ ] **Step 3b: Implement `wprepo.TermRepo.CountPublishedByTermSlug`**

Add to `internal/storage/wprepo/repo.go`, directly after `TermRepo.BySlug` (ends line 187):

```go
// CountPublishedByTermSlug returns the number of published posts related to
// a taxonomy term, mirroring PostRepo.ByTermSlug's join chain without the
// post columns/limit/offset. Pure COUNT(*); no writes.
func (r *TermRepo) CountPublishedByTermSlug(ctx context.Context, taxonomy, termSlug string) (int, error) {
	return r.db.NewSelect().
		TableExpr("? AS p", bun.Ident(r.prefix+"posts")).
		Join("JOIN ? AS tr ON tr.object_id = p.?", bun.Ident(r.prefix+"term_relationships"), bun.Ident("ID")).
		Join("JOIN ? AS tt ON tt.term_taxonomy_id = tr.term_taxonomy_id", bun.Ident(r.prefix+"term_taxonomy")).
		Join("JOIN ? AS t ON t.term_id = tt.term_id", bun.Ident(r.prefix+"terms")).
		Where("tt.taxonomy = ?", taxonomy).
		Where("t.slug = ?", termSlug).
		Where("p.post_status = ?", "publish").
		Count(ctx)
}
```

Add the compile-time check next to the existing ones in `internal/storage/wprepo/adminreads.go:12-17`:
```go
	_ domain.TermRepository = (*TermRepo)(nil)
```

- [ ] **Step 3c: Run to verify compile is fixed**

Run: `go vet ./...`
Expected: PASS (no output).

- [ ] **Step 3d: Add `wprepo` contract coverage**

Append to `internal/storage/storagetest/contract.go`'s `RunContract(t *testing.T, newRepos NewReposFunc)` function, directly after the existing `repos.Terms.BySlug` subtest (`t.Run("TermRepository BySlug ...")`, around line 270 — confirm via `grep -n 'repos.Terms.BySlug\|func RunContract' internal/storage/storagetest/contract.go`; there is no separate `runTermReaderContract` helper, unlike `runMediaContract`/`runAdminContract` — term-repository coverage lives inline in `RunContract` itself):

```go
	t.Run("CountPublishedByTermSlug counts only published posts in the taxonomy", func(t *testing.T) {
		repos, cleanup := newRepos(t)
		defer cleanup()
		n, err := repos.Terms.CountPublishedByTermSlug(ctx, "category", "news")
		if err != nil {
			t.Fatalf("CountPublishedByTermSlug: %v", err)
		}
		if n == 0 {
			t.Fatalf("want at least one published post in the news category fixture, got 0")
		}
		if _, err := repos.Terms.CountPublishedByTermSlug(ctx, "category", "no-such-slug"); err != nil {
			t.Fatalf("unknown slug should return (0, nil), got err: %v", err)
		}
	})
```

- [ ] **Step 3e: Run cross-vendor contract tests**

Run: `go test ./internal/storage/storagetest/... -run 'TestSQLiteContract/CountPublishedByTermSlug' -v`
Expected: PASS. (`CountPublishedByTermSlug` is a subtest name, not a top-level `func Test...` — `go test -run`'s pattern is matched per `/`-separated depth level, so an unslashed `-run CountPublishedByTermSlug` would check only the top-level test name and match zero tests; the top-level func name must be included, e.g. `TestMySQLContract/CountPublishedByTermSlug` and `TestPostgresContract/CountPublishedByTermSlug` for the other vendors if enabled locally.)

- [ ] **Step 3f: Add `TermService.CategoryPage`**

Edit `internal/content/term.go`, appending after `Category` (no changes to the constructor or `Category` itself):

```go
// CategoryPage is Category plus a Page pagination contract (Req 8.1) for
// callers that need total/out-of-range information (the public category
// page). domain.ErrNotFound behaves identically to Category: returned before
// any post query when the term itself doesn't exist.
func (s *TermService) CategoryPage(ctx context.Context, slug string, page, perPage int) (domain.Term, []domain.Post, Page, error) {
	term, err := s.terms.BySlug(ctx, TaxonomyCategory, slug)
	if err != nil {
		return domain.Term{}, nil, Page{}, err
	}
	limit, offset, clampedPage := clamp(page, perPage)
	posts, err := s.posts.ByTermSlug(ctx, TaxonomyCategory, slug, limit, offset)
	if err != nil {
		return domain.Term{}, nil, Page{}, err
	}
	total, err := s.terms.CountPublishedByTermSlug(ctx, TaxonomyCategory, slug)
	if err != nil {
		return domain.Term{}, nil, Page{}, err
	}
	return term, posts, newPage(clampedPage, limit, total), nil
}
```

- [ ] **Step 3g: Write the failing service test, then verify it passes**

Append to `internal/content/term_test.go` (`fakeTermRepo` and its `countPublished`/`CountPublishedByTermSlug` were already widened in Step 3a):

```go
func TestTermServiceCategoryPageReturnsTotal(t *testing.T) {
	terms := &fakeTermRepo{term: domain.Term{ID: 10, Slug: "news"}, countPublished: 25}
	posts := &fakePostRepo{termPosts: []domain.Post{{ID: 1}, {ID: 2}}}
	svc := NewTermService(terms, posts)

	_, got, page, err := svc.CategoryPage(context.Background(), "news", 2, 10)
	if err != nil {
		t.Fatalf("CategoryPage: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("posts = %d, want 2", len(got))
	}
	if page.Page != 2 || page.PerPage != 10 || page.Total != 25 || page.TotalPages != 3 {
		t.Fatalf("page = %+v, want {2 10 25 3}", page)
	}
}

func TestTermServiceCategoryPageNotFoundSkipsCount(t *testing.T) {
	terms := &fakeTermRepo{err: domain.ErrNotFound}
	posts := &fakePostRepo{}
	svc := NewTermService(terms, posts)
	_, _, _, err := svc.CategoryPage(context.Background(), "missing", 1, 10)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}
```

Run: `go vet ./internal/content/... && go test ./internal/content/... -run TestTermServiceCategoryPage -v`
Expected: PASS.

- [ ] **Step 3h: Commit**

```bash
git add internal/domain/repository.go internal/storage/wprepo/repo.go internal/storage/wprepo/adminreads.go internal/storage/storagetest/contract.go internal/content/term.go internal/content/term_test.go
git commit -m "content: add TermService.CategoryPage and CountPublishedByTermSlug"
```

## Task 2: Public home pagination

**Requirements:** Req 1.1-1.4 (home pagination + totals), Req 2.1-2.2 (out-of-range → 404 only when the site has posts), Req 8.1

**Files:**
- Modify: `internal/render/view.go:32-36` (`IndexData`)
- Modify: `internal/web/handlers.go:32-41` (`home`)
- Modify: `themes/default/templates/index.tmpl` (add pagination nav)
- Modify: `internal/render/engine.go` (add `templateFuncs` `FuncMap` with `add`/`sub`, wire via `.Funcs()`)
- Test: `internal/web/handlers_test.go` (append), `internal/web/handlers_test.go`'s `newTestServer` (wire `WithCounter`), `internal/render/engine_test.go` (append)

### Step 1: Wire `newTestServer` to support `RecentPage`

`internal/web/handlers_test.go`'s `newTestServer(t)` currently builds `content.NewPostService(repos.Posts)` with no counter. Find the exact construction line via `grep -n 'NewPostService' internal/web/handlers_test.go` and change it to:

```go
posts := content.NewPostService(repos.Posts).WithCounter(repos.PostCounter)
```

(`repos.PostCounter` already exists on the `storage.Set` returned by `storage.New`/`SeedFixtures`-backed test setup — no new fixture needed.)

- [ ] **Step 1a: Run to confirm no regression yet**

Run: `go test ./internal/web/... -run TestHome -v`
Expected: PASS (behavior-identical; `WithCounter` doesn't change `Recent`).

- [ ] **Step 1b: Commit**

```bash
git add internal/web/handlers_test.go
git commit -m "web: wire PostService.WithCounter in test server"
```

### Step 2: `IndexData` gains pagination fields; `home` uses `RecentPage` and returns 404 out-of-range

Edit `internal/render/view.go:32-36`:

```go
// IndexData backs the home page template.
type IndexData struct {
	SiteTitle  string
	Tagline    string
	Posts      []PostView
	Pagination content.Page
}
```

(A named field, not an anonymous embed: embedding `content.Page` anonymously would make `IndexData`'s own embedded-field name `Page` — Go names an anonymous field by its type's unqualified name — which sits at shallower depth than the *promoted* `content.Page.Page int` field, so Go's selector rules resolve `.Page` to the *whole embedded struct value*, not the int. `{{if gt .Page 1}}` would then try to compare a struct against `1` and fail at template-render time, only surfacing once a test actually exercises `TotalPages > 1`. Naming the field `Pagination` avoids the collision entirely: `.Pagination.Page`, `.Pagination.TotalPages`, etc., unambiguous in both Go code and templates.) Add the import: `"github.com/roboweaver/grimoire/internal/content"` to `internal/render/view.go`'s import block.

Edit `internal/web/handlers.go:32-41`, replacing `home` in full:

```go
func (s *Server) home(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	title, tagline := s.options.SiteInfo(ctx)
	page := pageParam(r)
	posts, pg, err := s.posts.RecentPage(ctx, page, content.DefaultPerPage)
	if err != nil {
		return err
	}
	if page > 1 && pg.Total > 0 && page > pg.TotalPages {
		return domain.ErrNotFound
	}
	data := render.IndexData{SiteTitle: title, Tagline: tagline, Posts: postViews(ctx, posts, s.options.BaseURLs(ctx), s.featured), Pagination: pg}
	return s.renderHTML(w, r, "index", data)
}
```

`domain.ErrNotFound` is already imported (`internal/web/handlers.go:12`) and already mapped to a 404 by `internal/web/middleware.go`'s `s.handler` wrapper for every public route -- no new helper. A zero-post site (`pg.Total == 0`) never satisfies `page > 1 && pg.Total > 0`, so `?page=999` on an empty site renders the existing "No posts yet." empty state at 200, not a 404 (Req 2.2).

- [ ] **Step 2a: Write the failing tests**

Append to `internal/web/handlers_test.go`:

```go
func TestHomeOutOfRangePageReturns404(t *testing.T) {
	srv := newTestServer(t) // SeedFixtures seeds 3 posts, well under page 999's range
	rec := get(t, srv, "/?page=999")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestHomeSinglePageSiteOmitsPaginationNav(t *testing.T) {
	srv := newTestServer(t)
	rec := get(t, srv, "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	// SeedFixtures seeds 3 posts against DefaultPerPage=10, so TotalPages==1
	// and the nav's `{{if gt .Pagination.TotalPages 1}}` guard must render
	// nothing — this is the only page-count claim this fixture can make
	// truthfully without seeding >10 posts.
	if strings.Contains(rec.Body.String(), "theme-pagination") {
		t.Fatalf("single-page site rendered pagination nav: %s", rec.Body.String())
	}
}
```

(`get` is the existing test helper (`internal/web/handlers_test.go:57`) that builds the request/recorder and calls `h.ServeHTTP` directly — `newTestServer` returns `http.Handler`, not a `*Server`, so there is no `.router` field to reach through.) Add `"strings"` to the test file's imports if not already present.

- [ ] **Step 2b: Run test to verify it fails**

Run: `go vet ./internal/web/...`
Expected: FAIL — `s.posts.RecentPage` undefined until Step 2's edit lands; apply the edit above, then:

Run: `go test ./internal/web/... -run TestHomeOutOfRangePageReturns404 -v`
Expected: initially FAIL (200 not 404) before the `home` handler edit; PASS after.

- [ ] **Step 2c: Wire `add`/`sub` template functions, then add pagination nav to `index.tmpl`**

The nav below calls `{{add .Pagination.Page 1}}`/`{{sub .Pagination.Page 1}}`. Go's `html/template` resolves function names at *parse* time, not render time, so `index.tmpl` cannot parse at all once these calls are added unless the functions are registered first. `internal/render/engine.go` has exactly one template-parsing call site (`grep -n 'ParseFiles\|template.New' internal/render/engine.go` confirms line 67, inside `Load`): `tmpl, err := template.New(baseTemplate).ParseFiles(files...)`, with no `FuncMap` anywhere in the package. Both edits below therefore land in this same step/commit.

Edit `internal/render/engine.go`, adding a package-level var above `Load` (after the existing `hierarchy` map/const block, before the `Load` func signature):

```go
// templateFuncs are helper functions available to every parsed template.
var templateFuncs = template.FuncMap{
	"add": func(a, b int) int { return a + b },
	"sub": func(a, b int) int { return a - b },
}
```

Change line 67 from `template.New(baseTemplate).ParseFiles(files...)` to:

```go
tmpl, err := template.New(baseTemplate).Funcs(templateFuncs).ParseFiles(files...)
```

Append to `internal/render/engine_test.go`:

```go
func TestTemplateFuncsAddSub(t *testing.T) {
	root, theme := writeTheme(t, map[string]string{
		"base.tmpl":  miniBase,
		"index.tmpl": `{{define "content"}}{{add 1 2}}/{{sub 5 3}}{{end}}`,
	})
	e, err := Load(root, theme)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	var buf bytes.Buffer
	if err := e.Render(&buf, "index", IndexData{SiteTitle: "grimoire"}); err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(buf.String(), "3/2") {
		t.Fatalf("want add/sub template funcs to render 3/2, got %q", buf.String())
	}
}
```

Run: `go test ./internal/render/... -run TestTemplateFuncsAddSub -v`
Expected: FAIL before the `engine.go` edit (`Load` returns an error containing `function "add" not defined`); apply the edit, then PASS.

Now edit `themes/default/templates/index.tmpl`, appending inside `{{define "content"}}` after the `</div>` that closes `theme-card-grid`:

```html
{{if gt .Pagination.TotalPages 1}}
<nav class="theme-pagination" aria-label="Posts pagination">
<p class="spectrum-Body spectrum-Body--sizeS">Page {{.Pagination.Page}} of {{.Pagination.TotalPages}}</p>
{{if gt .Pagination.Page 1}}<a href="/?page={{sub .Pagination.Page 1}}" class="spectrum-Link">Previous</a>{{end}}
{{if lt .Pagination.Page .Pagination.TotalPages}}<a href="/?page={{add .Pagination.Page 1}}" class="spectrum-Link">Next</a>{{end}}
</nav>
{{end}}
```

- [ ] **Step 2c-i: Regenerate the index golden file**

`internal/render/golden_test.go`'s `TestGoldenIndex` renders `index.tmpl` byte-for-byte against `internal/render/testdata/golden/index.html` and fails on any diff, including whitespace — the new `{{if gt .Pagination.TotalPages 1}}...{{end}}` block changes the template's bytes even though `TestGoldenIndex`'s fixture (`IndexData{}` with a zero-value `Pagination`, so `TotalPages == 0`) never renders the nav itself.

Run: `go test ./internal/render/... -run TestGoldenIndex -v`
Expected: FAIL — golden mismatch (the template changed).

Run: `go test ./internal/render/... -run TestGoldenIndex -update -v` then `git diff internal/render/testdata/golden/index.html`
Expected: PASS; diff shows only whitespace from the new (empty, since `TotalPages==0`) `{{if}}...{{end}}` block boundaries — no `<nav class="theme-pagination">` markup, since the fixture's `TotalPages` is 0. If the diff shows anything else, stop and inspect `index.tmpl` before continuing.

- [ ] **Step 2d: Run tests to verify they pass**

Run: `go vet ./... && go test ./internal/web/... ./internal/render/... -v`
Expected: PASS (unfiltered `internal/render` run so `TestGoldenIndex`/`TestGoldenSingle`/`TestGoldenCategory` are all exercised, not just name-filtered subsets).

- [ ] **Step 2e: Commit**

```bash
git add internal/render/view.go internal/render/engine.go internal/render/engine_test.go internal/web/handlers.go internal/web/handlers_test.go internal/render/testdata/golden/index.html themes/default/templates/index.tmpl
git commit -m "web: paginate public home page with 404 out-of-range"
```

## Task 3: Public category pagination

**Requirements:** Req 1.1-1.4 (category totals/pagination mirrors home), Req 2.1-2.2, Req 8.1

**Files:**
- Modify: `internal/render/view.go:51-56` (`CategoryData`)
- Modify: `internal/web/handlers.go:91-101` (`category`)
- Modify: `themes/default/templates/category.tmpl` (add pagination nav, same markup as `index.tmpl`)
- Test: `internal/web/handlers_test.go` (append)

### Step 1: `CategoryData` gains pagination fields; `category` uses `CategoryPage`

Edit `internal/render/view.go:51-56`:

```go
// CategoryData backs the category archive template.
type CategoryData struct {
	SiteTitle  string
	Tagline    string
	Term       TermView
	Posts      []PostView
	Pagination content.Page
}
```

Edit `internal/web/handlers.go:91-101`, replacing `category` in full:

```go
func (s *Server) category(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	slug := chi.URLParam(r, "slug")
	page := pageParam(r)
	term, posts, pg, err := s.terms.CategoryPage(ctx, slug, page, content.DefaultPerPage)
	if err != nil {
		return err
	}
	if page > 1 && pg.Total > 0 && page > pg.TotalPages {
		return domain.ErrNotFound
	}
	title, tagline := s.options.SiteInfo(ctx)
	data := render.CategoryData{SiteTitle: title, Tagline: tagline, Term: termView(term), Posts: postViews(ctx, posts, s.options.BaseURLs(ctx), s.featured), Pagination: pg}
	return s.renderHTML(w, r, "category", data)
}
```

- [ ] **Step 1a: Write the failing tests**

Append to `internal/web/handlers_test.go`:

```go
func TestCategoryOutOfRangePageReturns404(t *testing.T) {
	srv := newTestServer(t)
	rec := get(t, srv, "/category/news?page=999")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestCategoryUnknownSlugStillReturns404(t *testing.T) {
	srv := newTestServer(t)
	rec := get(t, srv, "/category/does-not-exist")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (unknown term, unrelated to pagination)", rec.Code)
	}
}
```

- [ ] **Step 1b: Run test to verify it fails, then apply the handler/view edits above and re-run**

Run: `go vet ./internal/web/... && go test ./internal/web/... -run TestCategory -v`
Expected: FAIL before edits (`s.terms.CategoryPage` undefined), PASS after.

- [ ] **Step 1c: Add pagination nav to `category.tmpl`**

Edit `themes/default/templates/category.tmpl`, same block as Task 2 Step 2c, appended after the post-grid `</div>`:

```html
{{if gt .Pagination.TotalPages 1}}
<nav class="theme-pagination" aria-label="Posts pagination">
<p class="spectrum-Body spectrum-Body--sizeS">Page {{.Pagination.Page}} of {{.Pagination.TotalPages}}</p>
{{if gt .Pagination.Page 1}}<a href="/category/{{.Term.Slug}}?page={{sub .Pagination.Page 1}}" class="spectrum-Link">Previous</a>{{end}}
{{if lt .Pagination.Page .Pagination.TotalPages}}<a href="/category/{{.Term.Slug}}?page={{add .Pagination.Page 1}}" class="spectrum-Link">Next</a>{{end}}
</nav>
{{end}}
```

(Verify `TermView` has a `Slug` field via `grep -n 'type TermView' -A 6 internal/render/view.go`; if the field is named differently, use that name instead.)

- [ ] **Step 1c-i: Regenerate the category golden file**

Same mechanism as Task 2 Step 2c-i: `TestGoldenCategory`'s fixture builds a `CategoryData{}` with a zero-value `Pagination` (`TotalPages == 0`), so the new nav block never renders, but the template's raw bytes still changed.

Run: `go test ./internal/render/... -run TestGoldenCategory -v`
Expected: FAIL — golden mismatch.

Run: `go test ./internal/render/... -run TestGoldenCategory -update -v` then `git diff internal/render/testdata/golden/category.html`
Expected: PASS; diff is whitespace-only (no `<nav class="theme-pagination">` markup, since `TotalPages` is 0 for this fixture).

- [ ] **Step 1d: Run full package tests**

Run: `go vet ./... && go test ./internal/web/... ./internal/render/... ./internal/content/... -v`
Expected: PASS, no regressions in existing `TestHome`/`TestCategory`/`TestSingle`/`TestGolden*` tests.

- [ ] **Step 1e: Commit**

```bash
git add internal/render/view.go internal/web/handlers.go internal/web/handlers_test.go internal/render/testdata/golden/category.html themes/default/templates/category.tmpl
git commit -m "web: paginate public category page with 404 out-of-range"
```

## Task 4: Admin post search/status filters (forward the full filter to `CountForAdmin`)

**Requirements:** Req 4.1-4.4 (status/search filters + pagination), Req 4.5 (missing/empty filter = unfiltered), Req 8.1, Req 8.3

**Files:**
- Modify: `internal/content/adminread.go:32-39,67-103` (`AdminList`, `AdminService.List`)
- Modify: `internal/web/adminapi.go:20-25` (`adminReader`), `:196-230`ish (`adminPosts` — locate via `grep -n 'func (s \*Server) adminPosts'`)
- Modify: `internal/web/adminapi_test.go:100-166` (`fakeAdmin.list` signature, `TestAdminPostsPagination`)
- Modify: `internal/web/adminapi_terms_test.go:272` (`fakeAdmin{list: ...}` closure signature, no behavior change)
- Modify: `internal/content/adminread_test.go` (migrate 3 existing `svc.List` call sites to `AdminListFilter`; append 2 new tests — **do not delete, redefine, or rename** `fakeAdminData`, `fakeUserReader`, `newAdminService`, or any of the 7 existing tests)
- Test: `internal/web/adminapi_test.go` (append)

**Ground truth (verified against current code, not assumed):** `internal/storage/wprepo/adminreads.go`'s `applyAdminSearch`/`ListForAdmin`/`CountForAdmin` **already** apply `AdminPostFilter.Search` identically to both list and count queries, and `internal/storage/storagetest/admin_contract.go` **already** has passing contract tests for it (`"ListForAdmin Search matches title and content, case-insensitively"`, `"CountForAdmin honors Search"`). The bug is one layer up: `AdminService.List` (`internal/content/adminread.go:88`) rebuilds the count filter from scratch — `domain.AdminPostFilter{Types: f.Types, Statuses: f.Statuses}` — silently dropping `Search` (and any future field) from the *count* query while it stays in the *list* query. Today this is latent (the handler never sets `Search`), but Task 4 must not repeat the mistake when wiring it through. This task adds **no new repository-layer search code and no new repository-layer search test** — only the service/handler/UI plumbing plus a service-level regression test that would have caught the bug.

`internal/content/adminread_test.go` **already exists** (213 lines, 7 passing tests: `TestAdminServiceListClampsAndPaginates`, `TestAdminServiceListTotalPagesRoundsUp`, `TestAdminServiceListEmptyFiltersLeaveTypesUnset`, `TestAdminServiceDetailPropagatesNotFound`, `TestAdminServiceDetailReturnsPost`, `TestAdminServiceStatsAggregates`, `TestAdminServiceDisplayName`) with existing helpers `fakeAdminData` (closure-per-method fake implementing `domain.AdminPostRepository`/`PostCounter`/`UserCounter`/`TermCounter`/`postByID`), `fakeUserReader`, and `newAdminService(data, users)`. Step 1 below **modifies** this file in place — migrating the 3 real call sites that use the old `(ctx, page, perPage, typ, status)` signature and appending 2 new tests that reuse `fakeAdminData` — it does not recreate or replace it.

### Step 1: `AdminService.List` takes an `AdminListFilter` and forwards it whole to both calls

- [ ] **Step 1a: Modify `internal/content/adminread_test.go` — migrate 3 call sites, append 2 tests**

**This file already exists with 7 passing tests. Do not delete, redefine, or rename anything in it.** Make exactly these changes:

First, migrate the three existing calls to `svc.List` from the old `(ctx, page, perPage, typ, status)` signature to the new `(ctx, page, perPage, AdminListFilter)` signature Step 1c introduces. No other line in these three tests changes — every existing assertion (on `gotFilter.Types`/`Statuses`/`Limit`/`Offset`, `got.Page`/`PerPage`/`Total`/`TotalPages`) still holds, because Step 1c's implementation builds the same internal `domain.AdminPostFilter` from `AdminListFilter`'s `Type`/`Status` fields:

`internal/content/adminread_test.go:63` (inside `TestAdminServiceListClampsAndPaginates`):
```diff
-	got, err := svc.List(context.Background(), 0, 1000, "", "")
+	got, err := svc.List(context.Background(), 0, 1000, AdminListFilter{})
```

`internal/content/adminread_test.go:98` (inside `TestAdminServiceListTotalPagesRoundsUp`):
```diff
-	got, err := svc.List(context.Background(), 2, 10, "post", "draft")
+	got, err := svc.List(context.Background(), 2, 10, AdminListFilter{Type: "post", Status: "draft"})
```

`internal/content/adminread_test.go:129` (inside `TestAdminServiceListEmptyFiltersLeaveTypesUnset`):
```diff
-	if _, err := svc.List(context.Background(), 1, 10, "", ""); err != nil {
+	if _, err := svc.List(context.Background(), 1, 10, AdminListFilter{}); err != nil {
```

Then append two new tests at the end of the file (after `TestAdminServiceDisplayName`), reusing the existing `fakeAdminData`/`fakeUserReader`/`newAdminService` helpers — no new fake type is introduced:

```go
func TestAdminServiceListForwardsSearchToCount(t *testing.T) {
	var gotListFilter, gotCountFilter domain.AdminPostFilter
	data := &fakeAdminData{
		list: func(f domain.AdminPostFilter) ([]domain.Post, error) {
			gotListFilter = f
			if f.Search == "hello" {
				return []domain.Post{{ID: 1, Title: "hello"}}, nil
			}
			return []domain.Post{{ID: 1, Title: "hello"}, {ID: 2, Title: "other"}}, nil
		},
		count: func(f domain.AdminPostFilter) (int, error) {
			gotCountFilter = f
			if f.Search == "hello" {
				return 1, nil
			}
			return 2, nil
		},
	}
	svc := newAdminService(data, &fakeUserReader{})

	all, err := svc.List(context.Background(), 1, 10, AdminListFilter{})
	if err != nil {
		t.Fatalf("List (unfiltered): %v", err)
	}
	if all.Total != 2 || all.TotalPages != 1 {
		t.Fatalf("unfiltered Total/TotalPages = %d/%d, want 2/1", all.Total, all.TotalPages)
	}

	filtered, err := svc.List(context.Background(), 1, 10, AdminListFilter{Search: "hello"})
	if err != nil {
		t.Fatalf("List (Search=hello): %v", err)
	}
	if len(filtered.Items) != 1 {
		t.Fatalf("filtered Items = %d, want 1", len(filtered.Items))
	}
	// The regression this test guards: before the fix, List rebuilt the count
	// filter from only Types/Statuses, so a Search-filtered CountForAdmin call
	// never saw Search and Total stayed 2 (the unfiltered count) instead of 1.
	if filtered.Total != 1 || filtered.TotalPages != 1 {
		t.Fatalf("Search-filtered Total/TotalPages = %d/%d, want 1/1 (Search must reach CountForAdmin)", filtered.Total, filtered.TotalPages)
	}
	if gotCountFilter.Search != "hello" {
		t.Fatalf("CountForAdmin did not receive Search: %+v", gotCountFilter)
	}
	if gotListFilter.Search != "hello" {
		t.Fatalf("ListForAdmin did not receive Search: %+v", gotListFilter)
	}
}

func TestAdminServiceListMissingFilterFieldsMeansUnfiltered(t *testing.T) {
	data := &fakeAdminData{
		list:  func(domain.AdminPostFilter) ([]domain.Post, error) { return []domain.Post{{ID: 1}, {ID: 2}}, nil },
		count: func(domain.AdminPostFilter) (int, error) { return 2, nil },
	}
	svc := newAdminService(data, &fakeUserReader{})
	got, err := svc.List(context.Background(), 1, 10, AdminListFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got.Items) != 2 || got.Total != 2 {
		t.Fatalf("zero-value AdminListFilter should return all posts, got %d items / total %d", len(got.Items), got.Total)
	}
}
```

- [ ] **Step 1b: Run test to verify it fails**

Run: `go vet ./internal/content/... && go test ./internal/content/... -run TestAdminService -v`
Expected: FAIL to compile — `AdminListFilter` doesn't exist yet and `svc.List` still takes `(ctx, page, perPage int, typ, status string)`, so all 5 `List`-calling tests (the 3 migrated in Step 1a plus the 2 new ones) fail to build. Go reports a compile failure for the whole package, so `TestAdminServiceDetailPropagatesNotFound`/`TestAdminServiceDetailReturnsPost`/`TestAdminServiceStatsAggregates`/`TestAdminServiceDisplayName` also show as failed even though they never call `List` — this is expected and resolves once Step 1c lands.

- [ ] **Step 1c: Implement `AdminListFilter` and the new `List` signature**

Edit `internal/content/adminread.go`. Replace the `AdminList` struct's doc comment and add `AdminListFilter` directly above it (after line 30's `Stats` struct):

```go
// AdminListFilter narrows the admin content list (Req 4.1-4.4). All fields are
// optional; the zero value matches every post/page regardless of type,
// status, or search term (Req 4.5).
type AdminListFilter struct {
	Type   string
	Status string
	Search string
}

// AdminList is a page of admin content plus pagination metadata.
type AdminList struct {
	Items      []domain.Post
	Page       int
	PerPage    int
	Total      int
	TotalPages int
}
```

Replace `List` in full (lines 67-103):

```go
// List returns a page of content (posts and pages, including drafts) matching
// f, with pagination metadata. page is 1-based; perPage is clamped to
// [1, MaxPerPage] with DefaultPerPage when unset. f's Type/Status become
// single-element domain filters; a zero-value f matches everything (Req
// 4.5). The same filter (minus paging) is forwarded to CountForAdmin so
// Total/TotalPages always reflect the filtered result set, never the
// unfiltered count (this is the bug this task exists to fix: an earlier
// version rebuilt a fresh, narrower filter for the count call).
func (s *AdminService) List(ctx context.Context, page, perPage int, f AdminListFilter) (AdminList, error) {
	limit, offset, page := clamp(page, perPage)
	af := domain.AdminPostFilter{Limit: limit, Offset: offset, Search: f.Search}
	if f.Type != "" {
		af.Types = []string{f.Type}
	}
	if f.Status != "" {
		af.Statuses = []string{f.Status}
	}
	items, err := s.posts.ListForAdmin(ctx, af)
	if err != nil {
		return AdminList{}, err
	}
	countFilter := af
	countFilter.Limit, countFilter.Offset = 0, 0
	total, err := s.posts.CountForAdmin(ctx, countFilter)
	if err != nil {
		return AdminList{}, err
	}
	p := newPage(page, limit, total)
	return AdminList{Items: items, Page: p.Page, PerPage: p.PerPage, Total: p.Total, TotalPages: p.TotalPages}, nil
}
```

(`countFilter := af` copies the struct by value — `domain.AdminPostFilter` has no slice mutated in place here, so this is a safe shallow copy; `Types`/`Statuses`/`Search` all carry through untouched.)

- [ ] **Step 1d: Run test to verify it passes**

Run: `go vet ./internal/content/... && go test ./internal/content/... -run TestAdminService -v`
Expected: PASS

- [ ] **Step 1e: Commit**

```bash
git add internal/content/adminread.go internal/content/adminread_test.go
git commit -m "content: AdminService.List forwards full filter to CountForAdmin"
```

### Step 2: Wire `adminReader`/`adminPosts` to the new filter and add `search=`

- [ ] **Step 2a: Write the failing handler test**

`internal/web/adminapi_test.go`'s `fakeAdmin` struct has a `list func(page, perPage int, typ, status string) (content.AdminList, error)` field and a `List` method that calls it; `TestAdminPostsPagination` (lines ~126-166) constructs `fakeAdmin{list: func(page, perPage int, typ, status string) (content.AdminList, error) { ... }}`. Update both for the new filter-struct shape:

```go
type fakeAdmin struct {
	list        func(page, perPage int, f content.AdminListFilter) (content.AdminList, error)
	detail      func(id int64) (domain.Post, error)
	stats       func() (content.Stats, error)
	displayName func(userID int64) (string, error)
}

func (f *fakeAdmin) List(_ context.Context, page, perPage int, filter content.AdminListFilter) (content.AdminList, error) {
	return f.list(page, perPage, filter)
}
```

(Keep `Detail`/`Stats`/`DisplayName` methods unchanged — only `list`'s field type and `List`'s signature change.) In `TestAdminPostsPagination`, change the closure signature from `func(page, perPage int, typ, status string) (content.AdminList, error)` to `func(page, perPage int, f content.AdminListFilter) (content.AdminList, error)` and read `f.Type`/`f.Status` instead of `typ`/`status` in its body (same logic, renamed params).

`fakeAdmin` is shared package-wide, so every other literal that sets its `list` field must also be updated to the new signature or the package fails to compile. A repo-wide search (`grep -rn "fakeAdmin{list:" internal/web/*.go`) turns up exactly one other site: `internal/web/adminapi_terms_test.go:272`, inside `TestAdminAPIRouterOmitsWriteRoutesWhenDepsNil`:

```go
// internal/web/adminapi_terms_test.go:272 — before
srv.admin = &fakeAdmin{list: func(int, int, string, string) (content.AdminList, error) {
	return content.AdminList{}, nil
}}
```

```go
// internal/web/adminapi_terms_test.go:272 — after
srv.admin = &fakeAdmin{list: func(int, int, content.AdminListFilter) (content.AdminList, error) {
	return content.AdminList{}, nil
}}
```

(All other `fakeAdmin{...}` literals in `adminapi_autosave_test.go`, `adminapi_posts_test.go`, and `adminapi_revisions_test.go` use `&fakeAdmin{}` or only set `detail`, so they don't reference `list` and are unaffected by this widening.)

Append a new test to `internal/web/adminapi_test.go`:

```go
func TestAdminPostsSearchQueryParamForwarded(t *testing.T) {
	var gotFilter content.AdminListFilter
	admin := &fakeAdmin{
		list: func(_, _ int, f content.AdminListFilter) (content.AdminList, error) {
			gotFilter = f
			return content.AdminList{Page: 1, PerPage: 10, Total: 1, TotalPages: 1}, nil
		},
	}
	srv := testAdminServer(admin)
	req := httptest.NewRequest(http.MethodGet, "/admin/api/posts?search=hello", nil)
	req = req.WithContext(principalCtx("edit_posts"))
	rec := httptest.NewRecorder()
	srv.jsonHandler(srv.adminPosts).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if gotFilter.Search != "hello" {
		t.Fatalf("adminReader.List got Search=%q, want %q", gotFilter.Search, "hello")
	}
}

func TestAdminPostsMissingFilterParamsReturnAll(t *testing.T) {
	var gotFilter content.AdminListFilter
	admin := &fakeAdmin{
		list: func(_, _ int, f content.AdminListFilter) (content.AdminList, error) {
			gotFilter = f
			return content.AdminList{Page: 1, PerPage: 10, Total: 3, TotalPages: 1}, nil
		},
	}
	srv := testAdminServer(admin)
	req := httptest.NewRequest(http.MethodGet, "/admin/api/posts", nil)
	req = req.WithContext(principalCtx("edit_posts"))
	rec := httptest.NewRecorder()
	srv.jsonHandler(srv.adminPosts).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if gotFilter != (content.AdminListFilter{}) {
		t.Fatalf("no query params should mean zero-value filter, got %+v", gotFilter)
	}
	var body postsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Total != 3 {
		t.Fatalf("Total = %d, want 3 (unfiltered)", body.Total)
	}
}
```

(Use whichever of `testAdminServer`/`principalCtx` names the existing file already defines — confirmed present via the earlier grep of `adminapi_test.go`: `testAdminServer(a adminReader) *Server` takes one argument, and `principalCtx(caps ...string) context.Context` takes capability strings, not a context; adjust only if a rename is discovered.)

- [ ] **Step 2b: Run to verify it fails**

Run: `go vet ./internal/web/... && go test ./internal/web/... -run TestAdminPosts -v`
Expected: FAIL — `adminReader.List` still takes `(typ, status string)`, and `adminPosts` never parses `search`.

- [ ] **Step 2c: Update `adminReader` and `adminPosts`**

Edit `internal/web/adminapi.go:20-25` (`adminReader` interface), changing only the `List` method:

```go
type adminReader interface {
	List(ctx context.Context, page, perPage int, f content.AdminListFilter) (content.AdminList, error)
	Detail(ctx context.Context, id int64) (domain.Post, error)
	Stats(ctx context.Context) (content.Stats, error)
	DisplayName(ctx context.Context, userID int64) (string, error)
}
```

Locate `adminPosts` via `grep -n 'func (s \*Server) adminPosts' internal/web/adminapi.go` and replace its body's filter construction (the line building the `s.admin.List(...)` call) with:

```go
func (s *Server) adminPosts(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	page := atoiDefault(q.Get("page"), 1)
	perPage := atoiDefault(q.Get("perPage"), 0)
	f := content.AdminListFilter{Type: q.Get("type"), Status: q.Get("status"), Search: q.Get("search")}
	list, err := s.admin.List(r.Context(), page, perPage, f)
	if err != nil {
		return err
	}
	items := make([]postListItem, 0, len(list.Items))
	for _, p := range list.Items {
		items = append(items, postListItem{
			ID:     p.ID,
			Title:  p.Title,
			Slug:   p.Slug,
			Type:   p.Type,
			Status: p.Status,
			Author: p.Author,
			Date:   p.Date.UTC().Format("2006-01-02T15:04:05Z07:00"),
		})
	}
	return writeJSON(w, http.StatusOK, postsResponse{
		Items:      items,
		Page:       list.Page,
		PerPage:    list.PerPage,
		Total:      list.Total,
		TotalPages: list.TotalPages,
	})
}
```

(Only the parameter-gathering — the new `f := content.AdminListFilter{...}` line replacing the old inline `q.Get("type")`/`q.Get("status")` call args — and the `List` call itself change; the `items` loop and `postsResponse` construction below are copied unchanged from the file's current body at `internal/web/adminapi.go:215-233`.)

- [ ] **Step 2d: Run to verify it passes**

Run: `go vet ./internal/web/... && go test ./internal/web/... -run TestAdminPosts -v`
Expected: PASS

- [ ] **Step 2e: Run the full web + content packages to confirm no regressions**

Run: `go vet ./... && go test ./internal/web/... ./internal/content/... -v`
Expected: PASS (all existing `TestAdminPosts*`, `TestAdminService*`, `TestHome`, `TestCategory` tests green)

- [ ] **Step 2f: Add the failing invalid-`status` 400 test (Req 4.1)**

Append to `internal/web/adminapi_test.go`:

```go
func TestAdminPostsInvalidStatusReturns400(t *testing.T) {
	admin := &fakeAdmin{list: func(_, _ int, _ content.AdminListFilter) (content.AdminList, error) {
		t.Fatal("List should not be called for an invalid status")
		return content.AdminList{}, nil
	}}
	srv := testAdminServer(admin)
	req := httptest.NewRequest(http.MethodGet, "/admin/api/posts?status=bogus", nil)
	req = req.WithContext(principalCtx("edit_posts"))
	rec := httptest.NewRecorder()
	srv.jsonHandler(srv.adminPosts).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Error.Code == "" || body.Error.Message == "" {
		t.Fatalf("expected non-empty error code/message, got %+v", body.Error)
	}
}

func TestAdminPostsMissingStatusReturns200(t *testing.T) {
	admin := &fakeAdmin{list: func(_, _ int, _ content.AdminListFilter) (content.AdminList, error) {
		return content.AdminList{Page: 1, PerPage: 10, Total: 3, TotalPages: 1}, nil
	}}
	srv := testAdminServer(admin)
	req := httptest.NewRequest(http.MethodGet, "/admin/api/posts", nil)
	req = req.WithContext(principalCtx("edit_posts"))
	rec := httptest.NewRecorder()
	srv.jsonHandler(srv.adminPosts).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (Req 4.5: absent status must not 400): %s", rec.Code, rec.Body.String())
	}
}
```

- [ ] **Step 2g: Run to verify it fails**

Run: `go vet ./internal/web/... && go test ./internal/web/... -run TestAdminPostsInvalidStatus -v`
Expected: FAIL — `adminPosts` accepts any `status` value today (only `AdminPostFilter.Statuses` filters silently, per `applyAdminSearch`'s WHERE-IN semantics — an unrecognized value simply matches zero rows instead of 400ing).

- [ ] **Step 2h: Validate `status` before building the filter**

Add above the `f := content.AdminListFilter{...}` line inside `adminPosts`:

```go
	status := q.Get("status")
	if status != "" {
		switch status {
		case "publish", "draft", "pending", "private", "future":
		default:
			writeJSONError(w, http.StatusBadRequest, "invalid_status", "status must be one of publish, draft, pending, private, future")
			return nil
		}
	}
	f := content.AdminListFilter{Type: q.Get("type"), Status: status, Search: q.Get("search")}
```

- [ ] **Step 2i: Run to verify it passes**

Run: `go vet ./internal/web/... && go test ./internal/web/... -run TestAdminPosts -v`
Expected: PASS (including the two new tests)

- [ ] **Step 2j: Commit**

```bash
git add internal/web/adminapi.go internal/web/adminapi_test.go internal/web/adminapi_terms_test.go
git commit -m "web: admin posts endpoint accepts search filter"
```

## Task 4A: Author filter (`domain.AuthorOption`, narrow `Authors()` port, capability-gated endpoint)

**Requirements:** Req 4.3 (author filter), Req 8.1

**Files:**
- Modify: `internal/domain/repository.go:40-57` (`AdminPostFilter`), `:60-66`ish (`AdminPostRepository` interface — locate via `grep -n 'AdminPostRepository interface'`)
- Modify: `internal/domain/adminrepo_test.go` (`adminFake`)
- Modify: `internal/content/adminread_test.go` (`fakeAdminData` — widened additively in Step 1c, in the same commit unit as `adminFake`, since both directly implement `domain.AdminPostRepository`; 2 new tests appended in Step 3a)
- Modify: `internal/storage/wprepo/adminreads.go` (add `Author` predicate + `PostRepo.Authors`)
- Modify: `internal/storage/storagetest/admin_contract.go` (append 2 subtests, test-local fixtures only)
- Modify: `internal/content/adminread.go` (`AdminListFilter.Author`, `AdminService.Authors`)
- Modify: `internal/web/adminapi.go` (`adminReader.Authors`, `adminPosts` parses+validates `author=`, new `adminAuthors` handler)
- Modify: `internal/web/adminroutes.go:87` area (register `GET /admin/api/authors`)
- Modify: `internal/web/adminapi_test.go` (`fakeAdmin.authors`, 3 new tests)
- Test: same files, TDD per step

**Design notes (why this shape):** `Authors()` returns only users who have authored at least one post/page — never the full user table — so the endpoint cannot be used to enumerate every account (a narrow, privacy-conscious read, not a general user directory). It is gated by the same `edit_posts` capability as `/admin/api/posts` (registered in that route group, not the separate `upload_files`-gated media group). The frontend never renders raw numeric IDs (Req 4.3, finding I1): `AuthorOption{ID, DisplayName}` gives the UI a name to show; `PostListItem.author` stays a plain ID and Task 7's filter UI resolves it against this list.

### Step 1: Domain contract widening

- [ ] **Step 1a: Write the failing compile check**

Run: `go vet ./... 2>&1 | tee /tmp/vet-before.txt` — record output as the "before" baseline (should be clean). This step has no new test file; the failing signal is the compile-time interface witness added in Step 1c.

- [ ] **Step 1b: Add `AuthorOption`, `AdminPostFilter.Author`, and the `Authors` method**

Edit `internal/domain/repository.go`. In `AdminPostFilter` (lines 40-57), add one field after `Search`:

```go
	Search string
	Author int64 // 0 means unfiltered (Req 4.5)
```

Directly above `AdminPostRepository`'s interface block, add:

```go
// AuthorOption is a minimal, privacy-conscious author identity for admin
// filter UIs: an ID plus a display name only (no email, login, or role).
type AuthorOption struct {
	ID          int64
	DisplayName string
}
```

Add one method to the `AdminPostRepository` interface:

```go
	// Authors returns the distinct set of users who have authored at least
	// one post or page, ordered by display name. It never returns users with
	// no authored content, so it cannot be used as a general user directory.
	Authors(ctx context.Context) ([]AuthorOption, error)
```

- [ ] **Step 1c: Update `adminFake` (`internal/domain/adminrepo_test.go`) and `fakeAdminData` (`internal/content/adminread_test.go`)**

`adminFake` is a value-receiver type with compile-time witnesses (`var _ AdminPostRepository = adminFake{}`). Add:

```go
func (adminFake) Authors(_ context.Context) ([]AuthorOption, error) {
	return []AuthorOption{{ID: 1, DisplayName: "Admin"}}, nil
}
```

`fakeAdminData` (Task 4's closure-per-method fake, already implementing `domain.AdminPostRepository`) must widen in this same step, not later: `go vet ./...` compiles every package's test files together, so the moment `AdminPostRepository` gains `Authors` above, `fakeAdminData` stops satisfying it until it is widened too — deferring this to Step 3 would make Step 1d's/Step 2d's `go vet ./...` fail for the wrong reason (a second unimplemented fake, not just the pending production implementer). Add one field to the `fakeAdminData` struct declaration:

```go
	authors    func() ([]domain.AuthorOption, error)
```

Add one delegating method next to the others:

```go
func (f *fakeAdminData) Authors(_ context.Context) ([]domain.AuthorOption, error) {
	return f.authors()
}
```

- [ ] **Step 1d: Run `go vet` to confirm the interface is satisfied everywhere**

Run: `go vet ./...`
Expected: FAIL — `internal/storage/wprepo.PostRepo` (the only production implementer of `AdminPostRepository`) has no `Authors` method yet, so `go vet ./...` fails at the `wprepo` package until Step 2 adds it. `internal/domain` (`adminFake`) and `internal/content` (`fakeAdminData`) already satisfy the widened interface after Step 1c, so those two packages individually compile clean; it is specifically `wprepo` (and therefore the whole-module `go vet ./...`) that stays red until Step 2.

- [ ] **Step 1e: Commit** (deferred to Step 2e — Steps 1 and 2 form one green unit since `go vet ./...` cannot pass with the interface only half-implemented)

### Step 2: `PostRepo.Authors` and `Author` filtering in `ListForAdmin`/`CountForAdmin`

- [ ] **Step 2a: Write the failing contract test**

Append to `internal/storage/storagetest/admin_contract.go` (inside `runAdminContract`, after the existing `"CountForAdmin honors Search"` subtest):

```go
	t.Run("Authors returns distinct post authors with display names", func(t *testing.T) {
		repos, cleanup := newRepos(t)
		defer cleanup()
		// Test-local fixture: SeedFixtures gives every post author 1 ("Admin").
		// Add one more author here rather than editing SeedFixtures, so this
		// test's data need stays isolated from every other contract/handler
		// test that assumes a single seeded author.
		editorID, err := repos.Users.Create(ctx, domain.User{Login: "editor", Nicename: "editor", DisplayName: "Editor Two"})
		if err != nil {
			t.Fatalf("create editor user: %v", err)
		}
		if _, err := repos.PostWriter.Create(ctx, domain.Post{
			Author: editorID, Title: "Local", Slug: "authors-test-local", Type: "post",
			Status: "publish", Date: time.Now(), CommentStatus: "open",
		}); err != nil {
			t.Fatalf("create local post: %v", err)
		}
		authors, err := repos.AdminPosts.Authors(ctx)
		if err != nil {
			t.Fatalf("Authors: %v", err)
		}
		if len(authors) != 2 {
			t.Fatalf("want 2 authors (Admin + Editor Two), got %d: %+v", len(authors), authors)
		}
		names := map[string]bool{}
		for _, a := range authors {
			names[a.DisplayName] = true
		}
		if !names["Admin"] || !names["Editor Two"] {
			t.Fatalf("authors missing expected names: %+v", authors)
		}
	})

	t.Run("ListForAdmin and CountForAdmin honor Author", func(t *testing.T) {
		repos, cleanup := newRepos(t)
		defer cleanup()
		editorID, err := repos.Users.Create(ctx, domain.User{Login: "editor2", Nicename: "editor2", DisplayName: "Editor Filter"})
		if err != nil {
			t.Fatalf("create editor user: %v", err)
		}
		if _, err := repos.PostWriter.Create(ctx, domain.Post{
			Author: editorID, Title: "By Editor", Slug: "by-editor", Type: "post",
			Status: "publish", Date: time.Now(), CommentStatus: "open",
		}); err != nil {
			t.Fatalf("create editor post: %v", err)
		}
		posts, err := repos.AdminPosts.ListForAdmin(ctx, domain.AdminPostFilter{Author: editorID})
		if err != nil {
			t.Fatalf("ListForAdmin: %v", err)
		}
		if len(posts) != 1 || posts[0].Slug != "by-editor" {
			t.Fatalf("Author-filtered ListForAdmin = %+v, want exactly [by-editor]", posts)
		}
		count, err := repos.AdminPosts.CountForAdmin(ctx, domain.AdminPostFilter{Author: editorID})
		if err != nil {
			t.Fatalf("CountForAdmin: %v", err)
		}
		if count != 1 {
			t.Fatalf("Author-filtered CountForAdmin = %d, want 1", count)
		}
	})
```

Add `"time"` to the file's import block if not already present (`grep -n '"time"' internal/storage/storagetest/admin_contract.go` first).

- [ ] **Step 2b: Run to verify it fails**

Run: `go vet ./... && go test ./internal/storage/... -run TestSQLite -v 2>&1 | grep -A5 Authors`
Expected: FAIL — `domain.AdminPostFilter` has no `Author` field yet, `AdminPostRepository` has no `Authors` method on `*PostRepo`; overall `go vet ./...` fails until this step's implementation lands (Step 1 alone left `*PostRepo` non-conforming).

- [ ] **Step 2c: Implement `Author` filtering and `Authors` in `internal/storage/wprepo/adminreads.go`**

Add one line to each of `ListForAdmin` and `CountForAdmin`, immediately after the existing `if len(f.Statuses) > 0 { ... }` block in each:

```go
	if f.Author != 0 {
		q = q.Where("post_author = ?", f.Author)
	}
```

(`post_author` is unaliased here because neither query gives the posts table an alias — confirmed by `TableExpr("?", bun.Ident(r.prefix+"posts"))` with no `AS p`.)

Append a new method:

```go
// Authors returns the distinct set of users who have authored at least one
// post or page (post_type IN ('post','page')), ordered by display name. A
// user with no authored posts never appears, so this cannot enumerate the
// full user table.
func (r *PostRepo) Authors(ctx context.Context) ([]domain.AuthorOption, error) {
	var rows []struct {
		ID          int64  `bun:"ID"`
		DisplayName string `bun:"display_name"`
	}
	err := r.db.NewSelect().
		TableExpr("? AS p", bun.Ident(r.prefix+"posts")).
		ColumnExpr("DISTINCT u.?, u.display_name", bun.Ident("ID")).
		Join("JOIN ? AS u ON u.? = p.post_author", bun.Ident(r.prefix+"users"), bun.Ident("ID")).
		Where("p.post_type IN (?)", bun.In([]string{"post", "page"})).
		OrderExpr("u.display_name ASC").
		Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}
	out := make([]domain.AuthorOption, len(rows))
	for i, row := range rows {
		out[i] = domain.AuthorOption{ID: row.ID, DisplayName: row.DisplayName}
	}
	return out, nil
}
```

(`bun.Ident("ID")` is used for every reference to the quoted `"ID"` column — the same pattern `media.go`'s `listQuery` already uses for `p.?`/`bun.Ident("ID")` — so both Postgres's case-sensitive quoting and SQLite/MySQL's unquoted `ID` resolve correctly across all three vendors.)

- [ ] **Step 2d: Run to verify it passes on all three vendors**

Run: `go vet ./... && go test ./internal/storage/... -run TestSQLite -v`
Expected: PASS. Then, if Postgres/MySQL are available locally (env-gated per `postgres_test.go`/`mysql_test.go`): `GRIMOIRE_TEST_PG=1 go test ./internal/storage/... -run TestPostgres -v` and `GRIMOIRE_TEST_MYSQL=1 go test ./internal/storage/... -run TestMySQL -v`. Expected: PASS on whichever vendors are configured in the dev environment; SQLite alone is sufficient to merge, matching this repo's existing env-gating convention.

- [ ] **Step 2e: Commit**

```bash
git add internal/domain/repository.go internal/domain/adminrepo_test.go internal/content/adminread_test.go internal/storage/wprepo/adminreads.go internal/storage/storagetest/admin_contract.go
git commit -m "storage: author filter and Authors() admin port"
```

### Step 3: Service + handler wiring

- [ ] **Step 3a: Write the failing tests**

`fakeAdminData`'s `authors` field and `Authors` method (`internal/content/adminread_test.go`) were already added in Step 1c, in the same commit as the `domain.AdminPostRepository` interface widening — this step only appends the two behavioral tests that exercise them, reusing `fakeAdminData`/`newAdminService` exactly as Task 4's tests do:

```go
func TestAdminServiceAuthorsDelegates(t *testing.T) {
	data := &fakeAdminData{
		authors: func() ([]domain.AuthorOption, error) {
			return []domain.AuthorOption{{ID: 1, DisplayName: "Admin"}}, nil
		},
	}
	svc := newAdminService(data, &fakeUserReader{})
	got, err := svc.Authors(context.Background())
	if err != nil {
		t.Fatalf("Authors: %v", err)
	}
	if len(got) != 1 || got[0].DisplayName != "Admin" {
		t.Fatalf("Authors = %+v", got)
	}
}

func TestAdminServiceListForwardsAuthor(t *testing.T) {
	var gotFilter domain.AdminPostFilter
	data := &fakeAdminData{
		list: func(f domain.AdminPostFilter) ([]domain.Post, error) {
			gotFilter = f
			return []domain.Post{{ID: 1, Author: 7, Title: "a"}}, nil
		},
		count: func(domain.AdminPostFilter) (int, error) { return 1, nil },
	}
	svc := newAdminService(data, &fakeUserReader{})
	if _, err := svc.List(context.Background(), 1, 10, AdminListFilter{Author: 7}); err != nil {
		t.Fatalf("List: %v", err)
	}
	if gotFilter.Author != 7 {
		t.Fatalf("ListForAdmin did not receive Author: %+v", gotFilter)
	}
}
```

In `internal/web/adminapi_test.go`, add `authors func() ([]domain.AuthorOption, error)` to `fakeAdmin` and:

```go
func (f *fakeAdmin) Authors(_ context.Context) ([]domain.AuthorOption, error) {
	return f.authors()
}
```

Add tests:

```go
func TestAdminAuthorsEndpoint(t *testing.T) {
	admin := &fakeAdmin{authors: func() ([]domain.AuthorOption, error) {
		return []domain.AuthorOption{{ID: 1, DisplayName: "Admin"}}, nil
	}}
	srv := testAdminServer(admin)
	req := httptest.NewRequest(http.MethodGet, "/admin/api/authors", nil)
	req = req.WithContext(principalCtx("edit_posts"))
	rec := httptest.NewRecorder()
	srv.jsonHandler(srv.adminAuthors).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"displayName":"Admin"`) {
		t.Fatalf("body missing displayName: %s", rec.Body.String())
	}
}

func TestAdminPostsInvalidAuthorReturns400(t *testing.T) {
	admin := &fakeAdmin{list: func(_, _ int, _ content.AdminListFilter) (content.AdminList, error) {
		t.Fatal("list should not be called for an invalid author param")
		return content.AdminList{}, nil
	}}
	srv := testAdminServer(admin)
	req := httptest.NewRequest(http.MethodGet, "/admin/api/posts?author=not-a-number", nil)
	req = req.WithContext(principalCtx("edit_posts"))
	rec := httptest.NewRecorder()
	srv.jsonHandler(srv.adminPosts).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code"`) {
		t.Fatalf("expected {\"error\":{\"code\":...}} envelope, got %s", rec.Body.String())
	}
}

func TestAdminPostsAuthorFilterForwarded(t *testing.T) {
	var gotFilter content.AdminListFilter
	admin := &fakeAdmin{list: func(_, _ int, f content.AdminListFilter) (content.AdminList, error) {
		gotFilter = f
		return content.AdminList{Page: 1, PerPage: 10, Total: 1, TotalPages: 1}, nil
	}}
	srv := testAdminServer(admin)
	req := httptest.NewRequest(http.MethodGet, "/admin/api/posts?author=42", nil)
	req = req.WithContext(principalCtx("edit_posts"))
	rec := httptest.NewRecorder()
	srv.jsonHandler(srv.adminPosts).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if gotFilter.Author != 42 {
		t.Fatalf("Author = %d, want 42", gotFilter.Author)
	}
}
```

Add `"strings"` to imports if not already present.

- [ ] **Step 3b: Run to verify failure**

Run: `go vet ./... && go test ./internal/content/... ./internal/web/... -run 'Author' -v`
Expected: FAIL — `content.AdminListFilter` has no `Author` field, `AdminService` has no `Authors` method, `adminReader` has no `Authors` method, `/admin/api/authors` is unrouted (404), `adminPosts` doesn't validate `author=`.

- [ ] **Step 3c: Implement**

`internal/content/adminread.go`: add `Author int64` to `AdminListFilter` (after `Search`); in `List`, set `af.Author = f.Author` alongside the existing `af.Search = f.Search` (both live directly on `domain.AdminPostFilter{}` construction — adjust the struct literal from Task 4 Step 1c to include `Author: f.Author`). Add:

```go
// Authors returns every user who has authored at least one post or page,
// for admin filter UIs (Req 4.3). See domain.AuthorOption's doc comment for
// the privacy rationale.
func (s *AdminService) Authors(ctx context.Context) ([]domain.AuthorOption, error) {
	return s.posts.Authors(ctx)
}
```

`internal/web/adminapi.go`: add `Authors(ctx context.Context) ([]domain.AuthorOption, error)` to the `adminReader` interface. In `adminPosts`, replace the `f := content.AdminListFilter{...}` line with author parsing that 400s on a malformed value:

```go
	f := content.AdminListFilter{Type: q.Get("type"), Status: status, Search: q.Get("search")}
	if raw := q.Get("author"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid_author", "author must be a numeric user ID")
			return nil
		}
		f.Author = id
	}
```

(`status` is the already-validated local variable from Task 4 Step 2h, still in scope above this line — not `q.Get("status")` directly, since that validation must keep running for every request.)

(Use whichever error-code string convention the file's other `writeJSONError` calls already follow — confirmed present via the earlier `writeJSONError` grep; match it exactly rather than inventing a new one.) Add a new handler next to `adminPosts`:

```go
type authorDTO struct {
	ID          int64  `json:"id"`
	DisplayName string `json:"displayName"`
}

func (s *Server) adminAuthors(w http.ResponseWriter, r *http.Request) error {
	authors, err := s.admin.Authors(r.Context())
	if err != nil {
		return err
	}
	out := make([]authorDTO, len(authors))
	for i, a := range authors {
		out[i] = authorDTO{ID: a.ID, DisplayName: a.DisplayName}
	}
	return writeJSON(w, http.StatusOK, struct {
		Authors []authorDTO `json:"authors"`
	}{Authors: out})
}
```

`internal/web/adminroutes.go`: inside the existing `edit_posts`-gated `gr` group (the same group registering `/posts` at line 86 and `/posts/{id}` at line 87 — `gr.Use(s.requireCapabilityJSON("edit_posts"))` at line 84), add:

```go
		gr.Method(http.MethodGet, "/authors", s.jsonHandler(s.adminAuthors))
```

(Match the file's real convention exactly: routes in this group use `gr.Method(http.MethodGet, path, s.jsonHandler(handler))`, not `r.Get`/`s.wrap` — neither of which exists in this file.)

- [ ] **Step 3d: Run to verify it passes**

Run: `go vet ./... && go test ./internal/content/... ./internal/web/... -v`
Expected: PASS (all tests in both packages, including every pre-existing test)

- [ ] **Step 3e: Commit**

```bash
git add internal/content/adminread.go internal/content/adminread_test.go internal/web/adminapi.go internal/web/adminapi_test.go internal/web/adminroutes.go
git commit -m "web: expose GET /admin/api/authors and author post filter"
```

## Task 5: `PaginationBar` shared component

**Requirements:** Req 8.1 (shared pagination contract surfaces identically everywhere it's rendered), Req 5 (admin list pagination), Req 6 (media pagination)

**Files:**
- Create: `web/admin/src/test-utils.tsx` (shared Spectrum render helper — new, used by this and every later frontend test in this plan)
- Create: `web/admin/src/components/PaginationBar.tsx`
- Test: `web/admin/src/components/PaginationBar.test.tsx`

**Design note:** `PostsList.tsx:170-197` (quoted in Task 4/7's grounding above) is today's only pagination UI; it is *not* touched in this task. `PaginationBar` is built and tested standalone here; Task 7 deletes `PostsList.tsx`'s inline block and `Media.tsx`'s (currently nonexistent) pagination and replaces both with this component in one pass, once the filter UI around them is also ready. This avoids a component with no real second caller yet (YAGNI) while still keeping the extraction and its consumption in separate, reviewable commits.

- [ ] **Step 1: Write the shared test wrapper**

Create `web/admin/src/test-utils.tsx`:

```tsx
import { Provider, defaultTheme } from "@adobe/react-spectrum";
import { render } from "@testing-library/react";
import type { ReactElement } from "react";

// renderWithSpectrum wraps ui in the <Provider theme={defaultTheme}> every
// Spectrum component needs (theming, portals, RSP context). Component tests
// should use this instead of repeating the wrapper inline.
export function renderWithSpectrum(ui: ReactElement) {
  return render(<Provider theme={defaultTheme}>{ui}</Provider>);
}
```

This step has no separate red/green cycle (it's a helper, not behavior) — it will be exercised by Step 2's test.

- [ ] **Step 2: Write the failing test**

Create `web/admin/src/components/PaginationBar.test.tsx`:

```tsx
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { renderWithSpectrum } from "../test-utils";
import { PaginationBar } from "./PaginationBar";

describe("PaginationBar", () => {
  it("shows the page/total-pages/item-count summary", () => {
    renderWithSpectrum(
      <PaginationBar page={2} totalPages={5} total={42} itemLabel="item" onPageChange={vi.fn()} />,
    );
    expect(screen.getByText("Page 2 of 5 · 42 items")).toBeInTheDocument();
  });

  it("disables Previous on page 1 and Next on the last page", () => {
    renderWithSpectrum(
      <PaginationBar page={1} totalPages={1} total={3} itemLabel="post" onPageChange={vi.fn()} />,
    );
    expect(screen.getByRole("button", { name: /previous page/i })).toBeDisabled();
    expect(screen.getByRole("button", { name: /next page/i })).toBeDisabled();
  });

  it("calls onPageChange with page +/- 1", async () => {
    const user = userEvent.setup();
    const onPageChange = vi.fn();
    renderWithSpectrum(
      <PaginationBar page={2} totalPages={5} total={42} itemLabel="item" onPageChange={onPageChange} />,
    );
    await user.click(screen.getByRole("button", { name: /previous page/i }));
    await user.click(screen.getByRole("button", { name: /next page/i }));
    expect(onPageChange).toHaveBeenNthCalledWith(1, 1);
    expect(onPageChange).toHaveBeenNthCalledWith(2, 3);
  });

  it("treats a 0-item, 0-page result as showing 1 disabled page (zero-post empty state, Req 3.4/8.1)", () => {
    renderWithSpectrum(
      <PaginationBar page={1} totalPages={0} total={0} itemLabel="item" onPageChange={vi.fn()} />,
    );
    expect(screen.getByText("Page 1 of 1 · 0 items")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /next page/i })).toBeDisabled();
  });
});
```

(`/previous page/i`/`/next page/i` match by accessible name via a case-insensitive regex rather than an exact string, so the assertion survives minor Spectrum `ActionButton` DOM/label changes — the resilience the reviewer asked for.)

- [ ] **Step 3: Run to verify it fails**

Run: `cd web/admin && npm test -- PaginationBar --run`
Expected: FAIL — `./PaginationBar` module doesn't exist.

- [ ] **Step 4: Implement `PaginationBar`**

Create `web/admin/src/components/PaginationBar.tsx`:

```tsx
import { ActionButton, Flex, Text } from "@adobe/react-spectrum";
import ChevronLeft from "@spectrum-icons/workflow/ChevronLeft";
import ChevronRight from "@spectrum-icons/workflow/ChevronRight";

export interface PaginationBarProps {
  /** 1-based current page. */
  page: number;
  /** From the shared Page/TotalPages contract (Task 1); 0 means an empty result set. */
  totalPages: number;
  total: number;
  /** Singular noun pluralized with a trailing "s" (e.g. "item", "post"). */
  itemLabel: string;
  onPageChange: (next: number) => void;
}

// PaginationBar is the one pagination control used by every admin list (Req
// 8.1). It renders "Page X of Y · N items" plus Previous/Next controls that
// disable at the ends. A 0-item result (totalPages === 0) displays as "Page 1
// of 1" rather than "Page 1 of 0" so a genuinely empty list still reads as a
// complete, non-broken page.
export function PaginationBar({ page, totalPages, total, itemLabel, onPageChange }: PaginationBarProps) {
  const displayTotalPages = Math.max(1, totalPages);
  return (
    <Flex direction="row" alignItems="center" justifyContent="space-between">
      <Text>
        Page {page} of {displayTotalPages} ·{" "}
        {total} {itemLabel}
        {total === 1 ? "" : "s"}
      </Text>
      <Flex gap="size-100">
        <ActionButton isDisabled={page <= 1} onPress={() => onPageChange(page - 1)} aria-label="Previous page">
          <ChevronLeft />
          <Text>Previous</Text>
        </ActionButton>
        <ActionButton isDisabled={page >= displayTotalPages} onPress={() => onPageChange(page + 1)} aria-label="Next page">
          <Text>Next</Text>
          <ChevronRight />
        </ActionButton>
      </Flex>
    </Flex>
  );
}
```

- [ ] **Step 5: Run to verify it passes**

Run: `cd web/admin && npm test -- PaginationBar --run`
Expected: PASS (all 4 tests)

- [ ] **Step 5b: Typecheck/build gate**

Run: `cd web/admin && npm run build`
Expected: PASS — a new component with unused-import or type errors must not reach the commit; this gate runs before every remaining frontend commit in this plan (Tasks 5, 7, 9), not only at the milestone boundary.

- [ ] **Step 6: Commit**

```bash
git add web/admin/src/test-utils.tsx web/admin/src/components/PaginationBar.tsx web/admin/src/components/PaginationBar.test.tsx
git commit -m "web/admin: PaginationBar shared component"
```


---

## Task 6: Media filter backend (`domain.MediaFilter` + shared predicate helper)

**Traceability:** Requirement 5 (Media Library Filtering, ACs 1-5), Requirement
4 (ACs 3-4: invalid `type`/`parentId` → 400), Requirement 8 (shared
`Total`/`TotalPages` contract, zero-result = 0), Requirement 9 (cross-vendor
contract tests + handler tests for valid/invalid/missing/zero-result cases).

**Ground truth (verified against current source, not assumed):**
- `internal/storage/wprepo/media.go` today: `listQuery` (lines 55-74) and
  `Count` (88-97) each independently build a `bun.SelectQuery` with
  `TableExpr`/`Join`/`Where("p.post_type = 'attachment'")` plus an `if
  f.ParentID != 0 { q.Where("p.post_parent = ?", f.ParentID) }` — the only
  filter predicate today, duplicated (not shared) between the two functions.
  IM1 requires this duplication end: one helper, called identically by both.
- `internal/domain/repository.go:120-125` today:
  ```go
  type MediaFilter struct {
      ParentID int64
      Limit    int
      Offset   int
  }
  ```
- `internal/content/media.go:51-61` (`MediaService.List`) already has the
  signature `List(ctx context.Context, filter domain.MediaFilter) ([]domain.Media, int, error)`
  and already forwards `filter` unchanged to both `s.repo.List(ctx, filter)`
  and `s.repo.Count(ctx, filter)` — **no service-layer change is needed**;
  only the `MediaFilter` struct and the repo-layer query functions change.
- `internal/web/adminapi_media.go:65-93` (`adminMediaList`) today only reads
  `page`/`perPage`/`parentId` via `atoiDefault(s, def)`, which silently
  returns `def` (0) on a non-numeric string — so `?parentId=abc` is silently
  treated as "no parent filter" today instead of a 400. It also has:
  ```go
  totalPages := (total + filter.Limit - 1) / filter.Limit
  if totalPages < 1 {
      totalPages = 1
  }
  ```
  which clamps a genuine zero-result page count to `1`. Per the unified
  Requirement 8.1 contract (already implemented in `AdminService.List`),
  zero results must report `TotalPages: 0`.
- Seed fixtures (`internal/storage/storagetest/contract.go`, read exactly):
  attachment id 201: title `"Photo"`, slug `"photo"`, `post_mime_type`
  `"image/jpeg"`, `post_date` `"2024-01-06 00:00:00"`, `post_parent = 1`,
  `_wp_attached_file` postmeta value `"2024/01/photo.jpg"`. Attachment id
  202: title `"Asset"`, slug `"asset"`, `post_mime_type` `"image/png"`,
  `post_date` `"2024-01-07 00:00:00"`, `post_parent = 0`,
  `_wp_attached_file` `"2024/01/asset.png"`. Both `post_status = "inherit"`.
  These exact values ground every filter assertion below — no invented data.

**Files:**
- Modify: `internal/domain/repository.go:120-125` (`MediaFilter` struct)
- Modify: `internal/storage/wprepo/media.go` (new `mediaWhere` helper; add
  `"strings"` import; `listQuery`/`Count` call the helper)
- Modify: `internal/storage/storagetest/media_contract.go` (new filter
  subtests in `runMediaContract`)
- Modify: `internal/web/adminapi_media.go:65-93` (`adminMediaList`: parse and
  validate `search`/`type`/`after`/`before`/`parentId`; fix `TotalPages`
  clamp)
- Create: `internal/web/adminapi_media_test.go` (handler-level filter/400
  tests — no such file exists today)

- [ ] **Step 1: Write the failing cross-vendor contract test**

Extend `internal/storage/storagetest/media_contract.go`'s real signature
`runMediaContract(t *testing.T, newRepos NewReposFunc)` (confirmed against
the file: every existing subtest calls `repos, cleanup := newRepos(t);
defer cleanup()` at its own top — there is no shared `repos` parameter) with
a new subtest appended after the existing "MediaRepository list count by-id
and parent filter" subtest (do not remove the existing one):

```go
t.Run("MediaRepository filters by search, type, and date range", func(t *testing.T) {
    repos, cleanup := newRepos(t)
    defer cleanup()
    ctx := context.Background()
    cases := []struct {
        name   string
        filter domain.MediaFilter
        wantID []int64 // expected IDs, in the order List should return them
    }{
        {
            name:   "search matches filename case-insensitively",
            filter: domain.MediaFilter{Search: "jpg", Limit: 10},
            wantID: []int64{201},
        },
        {
            name:   "search matches title case-insensitively",
            filter: domain.MediaFilter{Search: "ASSET", Limit: 10},
            wantID: []int64{202},
        },
        {
            name:   "type image matches both jpeg and png",
            filter: domain.MediaFilter{Type: "image", Limit: 10},
            wantID: []int64{202, 201}, // newest first
        },
        {
            name:   "type video matches nothing",
            filter: domain.MediaFilter{Type: "video", Limit: 10},
            wantID: []int64{},
        },
        {
            name:   "after excludes the earlier attachment",
            filter: domain.MediaFilter{After: mustParseDate(t, "2024-01-06T12:00:00Z"), Limit: 10},
            wantID: []int64{202},
        },
        {
            name:   "before excludes the later attachment",
            filter: domain.MediaFilter{Before: mustParseDate(t, "2024-01-06T00:00:00Z"), Limit: 10},
            wantID: []int64{201},
        },
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            list, err := repos.Media.List(ctx, tc.filter)
            if err != nil {
                t.Fatalf("List: %v", err)
            }
            count, err := repos.Media.Count(ctx, tc.filter)
            if err != nil {
                t.Fatalf("Count: %v", err)
            }
            if len(list) != count {
                t.Fatalf("len(list)=%d != count=%d — listQuery and Count predicates diverged", len(list), count)
            }
            gotID := make([]int64, len(list))
            for i, m := range list {
                gotID[i] = m.ID
            }
            if !reflect.DeepEqual(gotID, tc.wantID) {
                t.Fatalf("got IDs %v, want %v", gotID, tc.wantID)
            }
        })
    }
})
```

Add the `mustParseDate` helper (below `runMediaContract` in the same file):

```go
func mustParseDate(t *testing.T, s string) time.Time {
    t.Helper()
    ts, err := time.Parse(time.RFC3339, s)
    if err != nil {
        t.Fatalf("mustParseDate(%q): %v", s, err)
    }
    return ts
}
```

Add `"reflect"` and `"time"` to this file's import block if not already
present (confirm via the file's existing imports before editing — `context`
and `domain`/`storage` are already imported for the existing subtest;
`reflect`/`time` are new).

- [ ] **Step 2: Run to verify it fails (compile error — new fields don't exist yet)**

Run: `go vet ./internal/storage/...`
Expected: FAIL — `domain.MediaFilter` has no field `Search`/`Type`/`After`/`Before`.

- [ ] **Step 3: Widen `domain.MediaFilter`**

`internal/domain/repository.go:120-125`, replace:

```go
type MediaFilter struct {
	ParentID int64
	Limit    int
	Offset   int
}
```

with:

```go
type MediaFilter struct {
	ParentID int64
	Search   string
	Type     string // "", "image", "video", "audio", or "document"
	After    time.Time
	Before   time.Time
	Limit    int
	Offset   int
}
```

`internal/domain/repository.go` already imports `"time"` for other fields in
this file (`Session.ExpiresAt` etc.) — confirm before adding a duplicate
import; if it is not already imported, add it to the existing import block.

- [ ] **Step 4: Run to verify it compiles but the new subtest still fails on assertions**

Run: `go vet ./... && go test ./internal/storage/... -run TestSQLiteContract -v`
Expected: compiles; the new "filters by search, type, and date range" subtest
FAILs (predicates not implemented yet — `listQuery`/`Count` ignore the new
fields, so every case returns both attachments unfiltered).

- [ ] **Step 5: Add the shared `mediaWhere` predicate helper**

`internal/storage/wprepo/media.go` — add `"strings"` to the import block
(the file currently imports `"context"`, `"database/sql"`, `"errors"`, the
domain package, `rebind`, and `bun`; `"strings"` is a new addition here).
Add this helper above `listQuery`:

```go
// mediaWhere applies every domain.MediaFilter predicate identically to both
// listQuery and Count, so filtered item and count results can never diverge
// (Requirement 8, Requirement 5 ACs 1-4). Search matches the attachment
// title (p.post_title) or its WordPress-standard stored filename
// (_wp_attached_file postmeta), both case-insensitively, so a search for a
// file extension or generated filename works the same as a title search
// (Requirement 5.1). Type maps to a post_mime_type prefix family; "document"
// is defined as "not image/video/audio" since WordPress has no reserved
// document/* MIME prefix. After/Before compare against post_date using the
// same formatTS format already used by post/term reads in this package.
func mediaWhere(q *bun.SelectQuery, f domain.MediaFilter) *bun.SelectQuery {
	if f.ParentID != 0 {
		q = q.Where("p.post_parent = ?", f.ParentID)
	}
	if f.Search != "" {
		like := "%" + strings.ToLower(f.Search) + "%"
		q = q.Where("(LOWER(p.post_title) LIKE ? OR LOWER(pm.meta_value) LIKE ?)", like, like)
	}
	switch f.Type {
	case "image", "video", "audio":
		q = q.Where("p.post_mime_type LIKE ?", f.Type+"/%")
	case "document":
		q = q.Where("p.post_mime_type NOT LIKE ? AND p.post_mime_type NOT LIKE ? AND p.post_mime_type NOT LIKE ?",
			"image/%", "video/%", "audio/%")
	}
	if !f.After.IsZero() {
		q = q.Where("p.post_date >= ?", formatTS(f.After))
	}
	if !f.Before.IsZero() {
		// Before is the inclusive end of a calendar day (Requirement 5.3
		// treats after/before as ISO-8601 dates), so compare against the
		// start of the following day rather than excluding same-day rows.
		q = q.Where("p.post_date < ?", formatTS(f.Before.AddDate(0, 0, 1)))
	}
	return q
}
```

Update `listQuery` (lines 55-74) to call the helper instead of its inline
`ParentID` check — replace:

```go
	if f.ParentID != 0 {
		q = q.Where("p.post_parent = ?", f.ParentID)
	}
```

with:

```go
	q = mediaWhere(q, f)
```

in both `listQuery` and `Count` (the same one-line replacement in each
function, at the point where each previously had its own inline `ParentID`
check). Confirmed (`internal/storage/wprepo/media.go:60,91`): both queries
already `Join("JOIN ? AS pm ON pm.post_id = p.? AND pm.meta_key = ?", ...,
"_wp_attached_file")` — a plain (inner) `JOIN`, not `LEFT JOIN` — so the
`pm` alias `mediaWhere`'s `Search` clause reads is already present in both
functions with no change needed to the join itself. This inner join means
every attachment row requires a matching `_wp_attached_file` postmeta row to
appear at all (pre-existing behavior, confirmed true of both fixture rows
201/202 and unrelated to this task — do not change the join type here).

- [ ] **Step 6: Run to verify the contract test passes**

Run: `go vet ./... && go test ./internal/storage/... -run TestSQLiteContract -v`
Expected: PASS (all subtests in the SQLite contract run, including the 6 new
filter cases).

Run: `go test ./internal/storage/... -run TestMySQLContract -v` and
`go test ./internal/storage/... -run TestPostgresContract -v` (confirmed
real top-level test names: `internal/storage/storagetest/mysql_test.go` and
`postgres_test.go`; both call the same `RunContract`, so they exercise the
same new subtest). These require Docker-backed vendor test databases per
existing conventions in this repo and may be skipped locally if unavailable,
matching how the pre-existing media contract subtest is already run.
Expected: PASS on all three vendors before this task is considered done.

- [ ] **Step 7: Commit**

```bash
git add internal/domain/repository.go internal/storage/wprepo/media.go internal/storage/storagetest/media_contract.go
git commit -m "storage: media filters share one predicate across list and count"
```

- [ ] **Step 8: Write the failing handler test for filter forwarding**

Create `internal/web/adminapi_media_test.go`:

```go
package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/domain"
)

// capturingMediaRepo records the last domain.MediaFilter it received from
// MediaService, so handler tests can assert adminMediaList parses query
// parameters into the filter correctly without a real database.
type capturingMediaRepo struct {
	gotFilter domain.MediaFilter
	items     []domain.Media
	count     int
}

func (r *capturingMediaRepo) List(_ context.Context, f domain.MediaFilter) ([]domain.Media, error) {
	r.gotFilter = f
	return r.items, nil
}
func (r *capturingMediaRepo) Count(_ context.Context, f domain.MediaFilter) (int, error) {
	r.gotFilter = f
	return r.count, nil
}
func (r *capturingMediaRepo) ByID(context.Context, int64) (domain.Media, error) {
	return domain.Media{}, domain.ErrNotFound
}

func newMediaTestServer(t *testing.T, repo *capturingMediaRepo) *Server {
	t.Helper()
	srv := NewServer(nil, nil, nil, nil, nil)
	srv.auth = fakeSessions{
		p: auth.NewPrincipal(1, "editor", []string{auth.RoleEditor, auth.RoleAdministrator}),
		s: domain.Session{CSRFToken: "token"},
	}
	srv.media = content.NewMediaService(repo, stubMediaWriter{}, content.MediaConfig{UploadsDir: t.TempDir()})
	return srv
}

func doMediaListRequest(t *testing.T, srv *Server, query string) *httptest.ResponseRecorder {
	t.Helper()
	r := srv.SessionMiddleware(srv.adminAPIRouter())
	req := httptest.NewRequest(http.MethodGet, "/media"+query, nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "x"})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestAdminMediaListForwardsSearchTypeDateFilters(t *testing.T) {
	repo := &capturingMediaRepo{items: []domain.Media{{ID: 201}}, count: 1}
	srv := newMediaTestServer(t, repo)
	rec := doMediaListRequest(t, srv, "?search=jpg&type=image&after=2024-01-01&before=2024-02-01&parentId=1")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if repo.gotFilter.Search != "jpg" || repo.gotFilter.Type != "image" || repo.gotFilter.ParentID != 1 {
		t.Fatalf("filter not forwarded: %+v", repo.gotFilter)
	}
	if repo.gotFilter.After.IsZero() || repo.gotFilter.Before.IsZero() {
		t.Fatalf("after/before not parsed: %+v", repo.gotFilter)
	}
}
```

- [ ] **Step 9: Run to verify it fails**

Run: `go test ./internal/web/... -run TestAdminMediaListForwardsSearchTypeDateFilters -v`
Expected: FAIL — `adminMediaList` does not read `search`/`type`/`after`/
`before` from the query string yet, so `repo.gotFilter` has zero values.

- [ ] **Step 10: Write the failing 400-validation tests**

Add `"encoding/json"` to the file's import block (now used by the
`json.Unmarshal` calls below), then add to the same file:

```go
func TestAdminMediaListInvalidTypeReturns400(t *testing.T) {
	srv := newMediaTestServer(t, &capturingMediaRepo{})
	rec := doMediaListRequest(t, srv, "?type=spreadsheet")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("body not JSON envelope: %v (%s)", err, rec.Body.String())
	}
	if payload["error"]["code"] == "" {
		t.Fatalf("missing error.code: %s", rec.Body.String())
	}
}

func TestAdminMediaListInvalidParentIDReturns400(t *testing.T) {
	srv := newMediaTestServer(t, &capturingMediaRepo{})
	rec := doMediaListRequest(t, srv, "?parentId=not-a-number")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

// TestAdminMediaListMissingFiltersReturns200 is the Requirement 4.5 negative
// case: no filter query parameters at all must succeed, not 400.
func TestAdminMediaListMissingFiltersReturns200(t *testing.T) {
	srv := newMediaTestServer(t, &capturingMediaRepo{items: []domain.Media{{ID: 201}, {ID: 202}}, count: 2})
	rec := doMediaListRequest(t, srv, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

// TestAdminMediaListZeroResultsTotalPagesZero locks in the Requirement 8.1
// unified contract: an empty result set reports TotalPages 0, matching
// AdminService.List's existing behavior (I7) — not clamped to 1.
func TestAdminMediaListZeroResultsTotalPagesZero(t *testing.T) {
	srv := newMediaTestServer(t, &capturingMediaRepo{items: nil, count: 0})
	rec := doMediaListRequest(t, srv, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		TotalPages int `json:"totalPages"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("body not JSON: %v (%s)", err, rec.Body.String())
	}
	if payload.TotalPages != 0 {
		t.Fatalf("totalPages=%d, want 0", payload.TotalPages)
	}
}

// TestAdminMediaListClampsExcessivePerPage is the I3 regression: a
// `perPage` above content.MaxPerPage must be clamped consistently — the
// filter forwarded to the repository, the reported `perPage`, and
// `totalPages` must all agree on the clamped value, never the raw query
// input.
func TestAdminMediaListClampsExcessivePerPage(t *testing.T) {
	repo := &capturingMediaRepo{items: []domain.Media{{ID: 201}}, count: 250}
	srv := newMediaTestServer(t, repo)
	rec := doMediaListRequest(t, srv, "?perPage=99999")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if repo.gotFilter.Limit != content.MaxPerPage {
		t.Fatalf("filter.Limit=%d, want clamped %d", repo.gotFilter.Limit, content.MaxPerPage)
	}
	var payload struct {
		PerPage    int `json:"perPage"`
		TotalPages int `json:"totalPages"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("body not JSON: %v (%s)", err, rec.Body.String())
	}
	if payload.PerPage != content.MaxPerPage {
		t.Fatalf("perPage=%d, want clamped %d", payload.PerPage, content.MaxPerPage)
	}
	wantTotalPages := content.TotalPages(250, content.MaxPerPage)
	if payload.TotalPages != wantTotalPages {
		t.Fatalf("totalPages=%d, want %d (computed against the clamped perPage, not the raw 99999)", payload.TotalPages, wantTotalPages)
	}
}
```

- [ ] **Step 11: Run to verify these fail**

Run: `go test ./internal/web/... -run TestAdminMediaList -v`
Expected: `InvalidType`/`InvalidParentID` FAIL with 200 instead of 400
(`atoiDefault` silently defaults instead of rejecting; no `type` allow-list
exists yet); `ZeroResultsTotalPagesZero` FAILs with `totalPages: 1` (today's
clamp). `ClampsExcessivePerPage` and `MissingFiltersReturns200` already
PASS today — the former guards Step 12's `totalPages` swap as a non-
regression, the latter confirms Requirement 4.5 stays unbroken.

- [ ] **Step 12: Implement the handler changes**

`internal/web/adminapi_media.go`, replace the current `adminMediaList` body
(lines 67-93 today — confirmed real source above). Keep the first line
exactly as-is (`clampPage`'s single-destructure call, which already
overwrites `perPage` itself with the clamped value — do not introduce a
second local like `limit` that diverges from `perPage`, since the response
below still reports `perPage` and must match the value actually used for
`filter.Limit`/`TotalPages`, per Requirement 8 consistency):

```go
	q := r.URL.Query()
	page, perPage, offset := clampPage(atoiDefault(q.Get("page"), 1), atoiDefault(q.Get("perPage"), 0))
	filter := domain.MediaFilter{Limit: perPage, Offset: offset}

	if raw := q.Get("parentId"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id < 0 {
			writeJSONError(w, http.StatusBadRequest, "invalid_parent_id", "parentId must be a non-negative integer")
			return nil
		}
		filter.ParentID = id
	}

	filter.Search = q.Get("search")

	if typ := q.Get("type"); typ != "" {
		switch typ {
		case "image", "video", "audio", "document":
			filter.Type = typ
		default:
			writeJSONError(w, http.StatusBadRequest, "invalid_type", "type must be one of image, video, audio, document")
			return nil
		}
	}

	if raw := q.Get("after"); raw != "" {
		ts, err := time.Parse("2006-01-02", raw)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid_after", "after must be an ISO-8601 date (YYYY-MM-DD)")
			return nil
		}
		filter.After = ts
	}
	if raw := q.Get("before"); raw != "" {
		ts, err := time.Parse("2006-01-02", raw)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid_before", "before must be an ISO-8601 date (YYYY-MM-DD)")
			return nil
		}
		filter.Before = ts
	}

	items, total, err := s.media.List(r.Context(), filter)
	if err != nil {
		return err
	}
```

Add `"strconv"` and `"time"` to this file's import block if not already
present (confirm current imports first — `net/http`/`content`/`domain` are
already imported; `strconv`/`time` are likely new here since the current
handler only calls `atoiDefault`, which lives in `adminapi.go`).

Then replace the existing clamp (confirmed real source,
`internal/web/adminapi_media.go:82-85`):

```go
	totalPages := (total + perPage - 1) / perPage
	if totalPages < 1 {
		totalPages = 1
	}
```

with a call to the shared helper introduced in Task 1 (`content` is already
imported in this file — no new import needed):

```go
	totalPages := content.TotalPages(total, perPage)
```

The response construction below (`mediaListResponse{..., PerPage: perPage,
Total: total, TotalPages: totalPages, ...}`) is unchanged and now reports
the *clamped* `perPage` — Step 12's first line already overwrote the local
`perPage` with `clampPage`'s clamped return, so `?perPage=99999` reports
`perPage: 100` and a `TotalPages` computed against that same clamped value
(Requirement 8 consistency, I3).

- [ ] **Step 13: Run to verify all handler tests pass**

Run: `go vet ./... && go test ./internal/web/... -run TestAdminMediaList -v`
Expected: PASS (all 6 tests: `ForwardsSearchTypeDateFilters`,
`InvalidTypeReturns400`, `InvalidParentIDReturns400`,
`MissingFiltersReturns200`, `ZeroResultsTotalPagesZero`,
`ClampsExcessivePerPage`).

Run: `go test ./internal/web/... -v` (full package)
Expected: PASS — confirms this change did not regress
`TestAdminMediaUploadOversizeReturns413Envelope` or
`TestAdminMediaUploadDisallowedMIMEReturns415Envelope`, which construct
`srv.media` the same way and are unaffected by the list-path changes.

- [ ] **Step 14: Commit**

```bash
git add internal/web/adminapi_media.go internal/web/adminapi_media_test.go
git commit -m "web: admin media endpoint accepts search/type/date filters"
```

---

## Task 7: Frontend filters, `PaginationBar` adoption, media grid/list toggle

**Traceability:** Requirement 3 (admin search/status/author filters),
Requirement 4.5 (missing filters still load), Requirement 5 (media filters),
Requirement 6 (grid/list toggle, mutually exclusive), Requirement 7 (media
pagination), Requirement 8.1 (shared pagination display), Requirement 9.3
(React interaction + accessibility tests).

**Ground truth confirmed this task (current files re-read in full):**
`PostsList.tsx` already reads `page` from `useSearchParams()` and has an
inline Page/Prev/Next block (lines 170-197) with the exact same shape
`PaginationBar` renders — this task replaces that block with
`<PaginationBar>`, it does not duplicate it. `api.posts()` already accepts
`status`/`type` (`client.ts:162-173`) but not `search`/`author`. `api.media()`
only forwards `page`/`perPage` (`client.ts:191-197`) even though the backend
(Task 6) now accepts `parentId`/`search`/`type`/`after`/`before`. `Media.tsx`
renders `Grid` and `TableView` simultaneously with no filters and no
pagination (confirmed, current file read in full). `Comments.tsx` already
has a working `Picker`+`Item` status-filter convention (lines 32-38) — new
Pickers in this task follow that exact pattern for consistency, not a new
one. `PostList`/`MediaList` (`types.ts`) already have the exact
`{items, page, perPage, total, totalPages}` shape `PaginationBar` expects.

**Files:**
- Modify: `web/admin/src/api/client.ts` (`posts()` gains `search`/`author`;
  `media()` gains `parentId`/`search`/`type`/`after`/`before`; new
  `authors()`)
- Modify: `web/admin/src/api/types.ts` (new `AuthorOption` interface)
- Modify: `web/admin/src/views/PostsList.tsx` (search/status/author controls,
  URL-backed, `PaginationBar` adoption)
- Rewrite: `web/admin/src/views/Media.tsx` (search/type/date-range/parent-post
  filters, URL-persisted grid/list toggle, `PaginationBar` adoption)
- Test: `web/admin/src/views/PostsList.test.tsx` (create — no existing file)
- Test: `web/admin/src/views/Media.test.tsx` (create — no existing file)

- [ ] **Step 1: Extend `client.ts` and `types.ts`**

`web/admin/src/api/types.ts` — add after `PostListItem`:

```ts
export interface AuthorOption {
  id: number;
  displayName: string;
}
```

`web/admin/src/api/client.ts` — replace the `posts` entry with:

```ts
  posts: (
    params: { page?: number; perPage?: number; type?: string; status?: string; search?: string; author?: number },
    signal?: AbortSignal,
  ) => {
    const q = new URLSearchParams();
    if (params.page) q.set("page", String(params.page));
    if (params.perPage) q.set("perPage", String(params.perPage));
    if (params.type) q.set("type", params.type);
    if (params.status) q.set("status", params.status);
    if (params.search) q.set("search", params.search);
    if (params.author) q.set("author", String(params.author));
    const qs = q.toString();
    return get<PostList>(`/posts${qs ? `?${qs}` : ""}`, signal);
  },
  authors: (signal?: AbortSignal) => get<{ authors: AuthorOption[] }>("/authors", signal),
```

replace the `media` entry with:

```ts
  media: (
    params: { page?: number; perPage?: number; parentId?: number; search?: string; type?: string; after?: string; before?: string },
    signal?: AbortSignal,
  ) => {
    const q = new URLSearchParams();
    if (params.page) q.set("page", String(params.page));
    if (params.perPage) q.set("perPage", String(params.perPage));
    if (params.parentId) q.set("parentId", String(params.parentId));
    if (params.search) q.set("search", params.search);
    if (params.type) q.set("type", params.type);
    if (params.after) q.set("after", params.after);
    if (params.before) q.set("before", params.before);
    const qs = q.toString();
    return get<MediaList>(`/media${qs ? `?${qs}` : ""}`, signal);
  },
```

Add `AuthorOption` to the existing `import type { ... } from "./types"` block
at the top of `client.ts`.

- [ ] **Step 2: Write the failing `PostsList` filter test**

Create `web/admin/src/views/PostsList.test.tsx` (no existing file):

```tsx
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";
import { renderWithSpectrum } from "../test-utils";

vi.mock("../api/client", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/client")>();
  return {
    ...actual,
    api: { ...actual.api, posts: vi.fn(), authors: vi.fn() },
  };
});

import { api } from "../api/client";
import { PostsList } from "./PostsList";

describe("PostsList filters", () => {
  it("sends the search and status query params typed into the filter bar", async () => {
    vi.mocked(api.posts).mockResolvedValue({ items: [], page: 1, perPage: 10, total: 0, totalPages: 0 });
    vi.mocked(api.authors).mockResolvedValue({ authors: [{ id: 1, displayName: "Ed Author" }] });
    const user = userEvent.setup();
    renderWithSpectrum(
      <MemoryRouter initialEntries={["/posts"]}>
        <PostsList />
      </MemoryRouter>,
    );
    await waitFor(() => expect(api.posts).toHaveBeenCalled());

    await user.type(screen.getByLabelText(/search/i), "hello");
    await waitFor(() =>
      expect(api.posts).toHaveBeenLastCalledWith(
        expect.objectContaining({ search: "hello" }),
        expect.anything(),
      ),
    );

    await user.click(screen.getByRole("button", { name: /status/i }));
    await user.click(await screen.findByRole("option", { name: /draft/i }));
    await waitFor(() =>
      expect(api.posts).toHaveBeenLastCalledWith(
        expect.objectContaining({ status: "draft" }),
        expect.anything(),
      ),
    );
  });

  it("loads with no filters applied and does not error (Req 4.5)", async () => {
    vi.mocked(api.posts).mockResolvedValue({ items: [], page: 1, perPage: 10, total: 0, totalPages: 0 });
    vi.mocked(api.authors).mockResolvedValue({ authors: [] });
    renderWithSpectrum(
      <MemoryRouter initialEntries={["/posts"]}>
        <PostsList />
      </MemoryRouter>,
    );
    await waitFor(() =>
      expect(api.posts).toHaveBeenCalledWith(
        expect.not.objectContaining({ search: expect.anything() }),
        expect.anything(),
      ),
    );
  });
});
```

- [ ] **Step 3: Run to verify it fails**

Run: `cd web/admin && npm test -- PostsList --run`
Expected: FAIL — `PostsList` has no search field, no status `Picker`, and
never calls `api.authors` (the client function itself already exists from
Step 1; the component just doesn't use it yet).

- [ ] **Step 4: Rewrite `PostsList.tsx`'s filter bar and pagination**

Add imports (`Picker`, `Item`, `SearchField` to the existing
`@adobe/react-spectrum` import list; `PaginationBar` from
`../components/PaginationBar`) **and remove `Text` from that same list** —
after this step, `Text`/`ChevronLeft`/`ChevronRight` are only used inside the
inline Page/Prev/Next block being deleted below, so leaving them imported
trips `noUnusedLocals` (`web/admin/tsconfig.json:15`) and fails `tsc --noEmit`
(the first half of the `build` script, `web/admin/package.json:9`):

```tsx
import {
  Heading,
  Flex,
  TableView,
  TableHeader,
  TableBody,
  Column,
  Row,
  Cell,
  ActionButton,
  Button,
  DialogTrigger,
  AlertDialog,
  StatusLight,
  Item,
  Picker,
  SearchField,
} from "@adobe/react-spectrum";
import Delete from "@spectrum-icons/workflow/Delete";
import Edit from "@spectrum-icons/workflow/Edit";
import { PaginationBar } from "../components/PaginationBar";
```

Delete the two now-unused icon import lines entirely:

```tsx
import ChevronLeft from "@spectrum-icons/workflow/ChevronLeft";
import ChevronRight from "@spectrum-icons/workflow/ChevronRight";
```

Add state and data-fetching (replace the existing `page`/`state` block):

```tsx
  const page = Math.max(1, Number(params.get("page") || "1") || 1);
  const search = params.get("search") || "";
  const status = params.get("status") || "";
  const author = params.get("author") || "";

  const authorsState = useAsync((signal) => api.authors(signal), []);
  const authorOptions =
    authorsState.status === "success" ? authorsState.data.authors : [];

  const state = useAsync(
    (signal) =>
      api.posts(
        {
          page,
          perPage: 10,
          type,
          status: status || undefined,
          search: search || undefined,
          author: author ? Number(author) : undefined,
        },
        signal,
      ),
    [page, type, status, search, author, reloadToken],
  );

  function setFilter(key: string, value: string) {
    setParams((prev) => {
      const p = new URLSearchParams(prev);
      if (value) p.set(key, value);
      else p.delete(key);
      p.set("page", "1");
      return p;
    });
  }
```

`authorsState` reuses the shared `useAsync` hook (`web/admin/src/hooks.ts:13`) already used for `state` two lines below — it owns its own `AbortController`, aborts on unmount/re-run, and differentiates a real abort (silently ignored) from a `ForbiddenError` (mapped to `status: "forbidden"`, which this view intentionally treats the same as an empty list — a non-`edit_others_posts` caller simply sees no author filter options, matching Req 8.3's capability-gating without a separate error banner for a secondary filter) from any other error (`status: "error"`, also rendered as an empty options list here since a failed author-list fetch must not block posts from loading). No new `useEffect`/`useState`/cancellation-flag boilerplate is needed; do not add `useEffect` to the `react` import.

Add the filter bar JSX directly below the `Heading`/`Button` row:

```tsx
      <Flex direction="row" gap="size-200" alignItems="end">
        <SearchField
          label="Search"
          value={search}
          onChange={(v) => setFilter("search", v)}
          width="size-3000"
        />
        <Picker
          label="Status"
          selectedKey={status || "all"}
          onSelectionChange={(key) => setFilter("status", key === "all" ? "" : String(key))}
        >
          <Item key="all">All</Item>
          <Item key="publish">Published</Item>
          <Item key="draft">Draft</Item>
          <Item key="pending">Pending</Item>
          <Item key="private">Private</Item>
          <Item key="future">Scheduled</Item>
        </Picker>
        <Picker
          label="Author"
          selectedKey={author || "all"}
          onSelectionChange={(key) => setFilter("author", key === "all" ? "" : String(key))}
        >
          <Item key="all">All authors</Item>
          {authorOptions.map((a) => (
            <Item key={String(a.id)}>{a.displayName}</Item>
          ))}
        </Picker>
      </Flex>
```

Replace the inline Page/Prev/Next `Flex` block (the one this task's ground
truth cites at lines 170-197) with:

```tsx
            <PaginationBar
              page={state.data.page}
              totalPages={state.data.totalPages}
              total={state.data.total}
              itemLabel={label}
              onPageChange={goToPage}
            />
```

- [ ] **Step 5: Run to verify it passes**

Run: `cd web/admin && npm test -- PostsList --run`
Expected: PASS.

- [ ] **Step 5a: Typecheck/build gate**

Run: `cd web/admin && npm run build`
Expected: PASS. This runs `tsc --noEmit` (per `package.json:9`) ahead of the
Vite bundle step, so a leftover unused import (e.g. `Text`, `ChevronLeft`,
`ChevronRight` from Step 4, or any stray import introduced by this task)
fails the build with a `noUnusedLocals` diagnostic instead of silently
passing — Vitest alone does not type-check unused-import errors, so this
step is required at every task boundary that touches `web/admin/src/**` and
cannot be skipped.

- [ ] **Step 6: Commit**

```bash
git add web/admin/src/api/client.ts web/admin/src/api/types.ts web/admin/src/views/PostsList.tsx web/admin/src/views/PostsList.test.tsx
git commit -m "web/admin: post list search/status/author filters"
```

- [ ] **Step 7: Write the failing `Media` filter and grid/list toggle test**

Create `web/admin/src/views/Media.test.tsx`:

```tsx
import { fireEvent, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";
import { renderWithSpectrum } from "../test-utils";

vi.mock("../api/client", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/client")>();
  return {
    ...actual,
    api: { ...actual.api, media: vi.fn(), posts: vi.fn() },
  };
});

import { api } from "../api/client";
import { Media } from "./Media";

const ITEM = { id: 201, title: "Photo", filename: "photo.jpg", url: "/u/photo.jpg", mimeType: "image/jpeg", date: "2024-01-06T00:00:00Z", parentId: 1 };
const PARENT_POST = { id: 1, title: "Hello world", slug: "hello-world", type: "post", status: "publish", author: 1, date: "2024-01-01T00:00:00Z" };

function mockDefaults() {
  vi.mocked(api.media).mockResolvedValue({ items: [ITEM], page: 1, perPage: 20, total: 1, totalPages: 1 });
  vi.mocked(api.posts).mockResolvedValue({ items: [PARENT_POST], page: 1, perPage: 100, total: 1, totalPages: 1 });
}

describe("Media filters and view toggle", () => {
  it("sends search and type query params typed into the filter bar", async () => {
    mockDefaults();
    const user = userEvent.setup();
    renderWithSpectrum(
      <MemoryRouter initialEntries={["/media"]}>
        <Media />
      </MemoryRouter>,
    );
    await waitFor(() => expect(api.media).toHaveBeenCalled());

    await user.type(screen.getByLabelText(/search/i), "jpg");
    await waitFor(() =>
      expect(api.media).toHaveBeenLastCalledWith(expect.objectContaining({ search: "jpg" }), expect.anything()),
    );

    await user.click(screen.getByRole("button", { name: /type/i }));
    await user.click(await screen.findByRole("option", { name: /^image$/i }));
    await waitFor(() =>
      expect(api.media).toHaveBeenLastCalledWith(expect.objectContaining({ type: "image" }), expect.anything()),
    );
  });

  it("sends after/before/parentId query params from the date-range and parent-post filters (Req 5.6)", async () => {
    mockDefaults();
    const user = userEvent.setup();
    renderWithSpectrum(
      <MemoryRouter initialEntries={["/media"]}>
        <Media />
      </MemoryRouter>,
    );
    await waitFor(() => expect(api.posts).toHaveBeenCalled());

    // `fireEvent.change`, not `user.type`: jsdom's native `<input type="date">`
    // parses/validates its `value` as a whole string on each set and does not
    // support the segmented keystroke-by-keystroke editing that
    // `@testing-library/user-event`'s `.type()` simulates for text inputs
    // (each intermediate keystroke — "2", "20", "202"... — is an invalid
    // partial date and gets silently reset to empty), so `user.type` never
    // reaches the final value. `fireEvent.change` sets the whole string in
    // one shot, which is the documented workaround for native date inputs.
    fireEvent.change(screen.getByLabelText(/from date/i), { target: { value: "2024-01-01" } });
    await waitFor(() =>
      expect(api.media).toHaveBeenLastCalledWith(expect.objectContaining({ after: "2024-01-01" }), expect.anything()),
    );

    fireEvent.change(screen.getByLabelText(/to date/i), { target: { value: "2024-01-31" } });
    await waitFor(() =>
      expect(api.media).toHaveBeenLastCalledWith(expect.objectContaining({ before: "2024-01-31" }), expect.anything()),
    );

    await user.click(screen.getByRole("button", { name: /parent post/i }));
    await user.click(await screen.findByRole("option", { name: /hello world/i }));
    await waitFor(() =>
      expect(api.media).toHaveBeenLastCalledWith(expect.objectContaining({ parentId: 1 }), expect.anything()),
    );
  });

  it("shows exactly one of grid or list view, toggled via the keyboard, never both (Req 6, Req 9.3)", async () => {
    mockDefaults();
    const user = userEvent.setup();
    renderWithSpectrum(
      <MemoryRouter initialEntries={["/media"]}>
        <Media />
      </MemoryRouter>,
    );
    await waitFor(() => expect(screen.getByText("Photo")).toBeInTheDocument());

    // Default view is grid: the list container is absent entirely, not
    // merely hidden, so both views can never render at once (Req 6.1/6.2).
    expect(screen.getByTestId("media-grid-view")).toBeInTheDocument();
    expect(screen.queryByTestId("media-list-view")).not.toBeInTheDocument();

    const listToggle = screen.getByRole("button", { name: /list view/i });
    listToggle.focus();
    await user.keyboard("{Enter}");
    expect(await screen.findByTestId("media-list-view")).toBeInTheDocument();
    expect(screen.queryByTestId("media-grid-view")).not.toBeInTheDocument();
  });

  it("persists the list view across a reload via the URL (Req 6.4)", async () => {
    mockDefaults();
    renderWithSpectrum(
      <MemoryRouter initialEntries={["/media?view=list"]}>
        <Media />
      </MemoryRouter>,
    );
    expect(await screen.findByTestId("media-list-view")).toBeInTheDocument();
    expect(screen.queryByTestId("media-grid-view")).not.toBeInTheDocument();
  });
});
```

- [ ] **Step 8: Run to verify it fails**

Run: `cd web/admin && npm test -- Media --run`
Expected: FAIL — no search/type/date/parent controls, no view toggle, both
grid and table always render together, `api.posts` is never called.

- [ ] **Step 9: Rewrite `Media.tsx`**

Replace the entire file:

```tsx
import { ActionButton, Cell, Column, Content, Dialog, DialogContainer, Divider, Flex, Grid, Heading, Image, Item, Picker, Row, SearchField, TableBody, TableHeader, TableView, Text, ToggleButton, View } from "@adobe/react-spectrum";
import Add from "@spectrum-icons/workflow/Add";
import GridIcon from "@spectrum-icons/workflow/ClassicGridView";
import ImageIcon from "@spectrum-icons/workflow/Image";
import ListIcon from "@spectrum-icons/workflow/ViewList";
import { useState } from "react";
import { useSearchParams } from "react-router-dom";
import { api } from "../api/client";
import { PaginationBar } from "../components/PaginationBar";
import { Empty, ErrorState, Forbidden, Loading } from "../components/States";
import { useAsync } from "../hooks";

// Media renders the admin media library (Req 5-8). Grid and list are
// mutually exclusive (Req 6) — exactly one renders at a time, toggled by a
// Spectrum ToggleButton pair, not simultaneously as the pre-M8 view did. The
// active view is a URL query param (`view`), so it survives a reload
// (Req 6.4) the same way `page`/`search`/`type` already do.
//
// The from/to date filters use a native `<input type="date">` rather than
// Spectrum's `DatePicker`: `mediaWhere` (Task 6) parses `after`/`before` as
// plain `YYYY-MM-DD` calendar dates, which is exactly the native date
// input's `.value` format with zero conversion, and it keeps the filter's
// test interaction a single `user.type` call instead of the multi-segment
// spinbutton sequence `DatePicker` requires (see PostEditor.test.tsx's
// schedule-date test for how much heavier that is). `DatePicker` remains
// the right choice for authoring a specific post's schedule date; this is
// a coarser filter control, not authored content.
export function Media() {
  const [params, setParams] = useSearchParams();
  const page = Math.max(1, Number(params.get("page") || "1") || 1);
  const search = params.get("search") || "";
  const type = params.get("type") || "";
  const after = params.get("after") || "";
  const before = params.get("before") || "";
  const parentId = params.get("parentId") || "";
  const view = params.get("view") === "list" ? "list" : "grid";
  const [reload, setReload] = useState(0);
  const [message, setMessage] = useState<string | null>(null);

  const parentOptionsState = useAsync(
    (signal) => api.posts({ perPage: 100 }, signal),
    [],
  );
  const parentOptions =
    parentOptionsState.status === "success" ? parentOptionsState.data.items : [];
  // `parentOptionsState` reuses the shared `useAsync` hook rather than a
  // bespoke `useEffect` + cancelled-flag: it owns its own `AbortController`,
  // aborts on unmount, and already differentiates an aborted fetch (ignored)
  // from a `ForbiddenError` or other error (both rendered here as an empty
  // parent-picker list, since a failed/forbidden parent-options fetch must
  // not block the media grid/list itself from loading).

  const state = useAsync(
    (signal) =>
      api.media(
        {
          page,
          perPage: 20,
          search: search || undefined,
          type: type || undefined,
          after: after || undefined,
          before: before || undefined,
          parentId: parentId ? Number(parentId) : undefined,
        },
        signal,
      ),
    [page, search, type, after, before, parentId, reload],
  );

  function setFilter(key: string, value: string) {
    setParams((prev) => {
      const p = new URLSearchParams(prev);
      if (value) p.set(key, value);
      else p.delete(key);
      p.set("page", "1");
      return p;
    });
  }

  // setView deliberately does not reset page: switching how results are
  // displayed is not a new query, unlike every other filter above.
  function setView(next: "grid" | "list") {
    setParams((prev) => {
      const p = new URLSearchParams(prev);
      p.set("view", next);
      return p;
    });
  }

  function goToPage(next: number) {
    setParams((prev) => {
      const p = new URLSearchParams(prev);
      p.set("page", String(next));
      return p;
    });
  }

  async function onPick(ev: React.ChangeEvent<HTMLInputElement>) {
    const file = ev.target.files?.[0];
    if (!file) return;
    try {
      const uploaded = await api.uploadMedia(file);
      setMessage(`Uploaded ${uploaded.filename}`);
      setReload((n) => n + 1);
    } catch (err) {
      setMessage((err as Error).message);
    } finally {
      ev.target.value = "";
    }
  }

  return (
    <Flex direction="column" gap="size-300">
      <Flex justifyContent="space-between" alignItems="center">
        <Heading level={1}>Media</Heading>
        <Flex alignItems="center" gap="size-100">
          <ActionButton onPress={() => document.getElementById("media-upload-input")?.click()}><Add /><Text>Upload</Text></ActionButton>
          <input id="media-upload-input" type="file" accept="image/*" onChange={onPick} style={{ display: "none" }} />
        </Flex>
      </Flex>

      <Flex direction="row" gap="size-200" alignItems="end" wrap justifyContent="space-between">
        <Flex direction="row" gap="size-200" alignItems="end" wrap>
          <SearchField label="Search" value={search} onChange={(v) => setFilter("search", v)} width="size-3000" />
          <Picker label="Type" selectedKey={type || "all"} onSelectionChange={(key) => setFilter("type", key === "all" ? "" : String(key))}>
            <Item key="all">All types</Item>
            <Item key="image">Image</Item>
            <Item key="video">Video</Item>
            <Item key="audio">Audio</Item>
            <Item key="document">Document</Item>
          </Picker>
          <label htmlFor="media-after-input">From date</label>
          <input
            id="media-after-input"
            type="date"
            value={after}
            onChange={(ev) => setFilter("after", ev.target.value)}
          />
          <label htmlFor="media-before-input">To date</label>
          <input
            id="media-before-input"
            type="date"
            value={before}
            onChange={(ev) => setFilter("before", ev.target.value)}
          />
          <Picker
            label="Parent post"
            selectedKey={parentId || "all"}
            onSelectionChange={(key) => setFilter("parentId", key === "all" ? "" : String(key))}
          >
            <Item key="all">Any post</Item>
            {parentOptions.map((p) => (
              <Item key={String(p.id)}>{p.title}</Item>
            ))}
          </Picker>
        </Flex>
        <Flex gap="size-100">
          <ToggleButton isSelected={view === "grid"} onChange={() => setView("grid")} aria-label="Grid view">
            <GridIcon />
          </ToggleButton>
          <ToggleButton isSelected={view === "list"} onChange={() => setView("list")} aria-label="List view">
            <ListIcon />
          </ToggleButton>
        </Flex>
      </Flex>

      {state.status === "loading" && <Loading label="Loading media" />}
      {state.status === "forbidden" && <Forbidden />}
      {state.status === "error" && <ErrorState message={state.message} />}
      {state.status === "success" && (state.data.items.length === 0 ? (
        <Empty heading="No media" message="Upload files to build the library, or clear filters." />
      ) : (
        <>
          {view === "grid" ? (
            <div data-testid="media-grid-view">
              <Grid columns="repeat(auto-fill, minmax(size-2000, 1fr))" gap="size-200">
                {state.data.items.map((item) => (
                  <View key={item.id} borderWidth="thin" borderColor="gray-300" borderRadius="medium" padding="size-200">
                    <Flex direction="column" gap="size-100">
                      {item.mimeType.startsWith("image/") ? (
                        <Image src={item.url} alt={item.title || item.filename} objectFit="cover" height="size-1600" />
                      ) : (
                        <ImageIcon aria-label="Media item" />
                      )}
                      <Text>{item.title || item.filename}</Text>
                      <Text>{item.mimeType}</Text>
                    </Flex>
                  </View>
                ))}
              </Grid>
            </div>
          ) : (
            <div data-testid="media-list-view">
              <TableView aria-label="Media list">
                <TableHeader>
                  <Column key="title">Title</Column>
                  <Column key="filename">Filename</Column>
                  <Column key="mime" width={180}>MIME</Column>
                  <Column key="url">URL</Column>
                </TableHeader>
                <TableBody>
                  {state.data.items.map((item) => (
                    <Row key={item.id}>
                      <Cell>{item.title || "(untitled)"}</Cell>
                      <Cell>{item.filename}</Cell>
                      <Cell>{item.mimeType}</Cell>
                      <Cell>{item.url}</Cell>
                    </Row>
                  ))}
                </TableBody>
              </TableView>
            </div>
          )}
          <PaginationBar page={state.data.page} totalPages={state.data.totalPages} total={state.data.total} itemLabel="item" onPageChange={goToPage} />
        </>
      ))}
      <DialogContainer onDismiss={() => setMessage(null)}>
        {message ? (
          <Dialog>
            <Heading>Media upload</Heading>
            <Divider />
            <Content>{message}</Content>
          </Dialog>
        ) : null}
      </DialogContainer>
    </Flex>
  );
}
```

`Media.tsx` gains a `react-router-dom` dependency it did not have before
(for `useSearchParams`, URL-backed pagination per Requirement 8.1) — this
import already exists in `package.json` since `PostsList.tsx` uses it.
`App.tsx` already renders `<Media />` inside its top-level `<Routes>` tree
(`web/admin/src/App.tsx:43`, `<Route path="media" element={<Media />} />`,
nested under the same `<Routes>` ancestor as `PostsList` at line 36), so no
app-level routing change is needed here — only the standalone
`Media.test.tsx` unit test needs its own `MemoryRouter` ancestor, which
Step 7 already added.

- [ ] **Step 10: Run to verify it passes**

Run: `cd web/admin && npm test -- Media --run`
Expected: PASS.

- [ ] **Step 10a: Typecheck/build gate**

Run: `cd web/admin && npm run build`
Expected: PASS — confirms the fully rewritten `Media.tsx` has no
`noUnusedLocals`/type errors (e.g. the corrected `ViewList` icon import from
C1 above) before committing.

- [ ] **Step 11: Commit**

```bash
git add web/admin/src/views/Media.tsx web/admin/src/views/Media.test.tsx
git commit -m "web/admin: media filters, pagination, and grid/list toggle"
```

---

## Task 8: Documentation update

**Traceability:** M8 scope note in `requirements.md`/`design.md` — docs must
describe only what M8 actually changed, not aspirational future work.

**Files:**
- Modify: `docs/compatibility.md`
- Modify: `docs/wordpress-compatibility-tour.md`

- [ ] **Step 1: Update `docs/compatibility.md`**

Find the pagination/filtering rows in the existing gap table (they currently
describe public home/category pagination and admin/media filtering as
missing or partial). Update each to reflect the M8 state: public home and
category pages now paginate with WordPress-equivalent total-page navigation
and return 404 for an out-of-range page on a non-empty site; admin post
list supports `search`/`status`/`author` filters with pagination; media
library supports `search`/`type`/`after`/`before`/`parentId` filters with a
grid/list toggle and pagination. Do not mark routing/taxonomy items (nested
categories, permalink tokens) or REST write/`content.rendered` items as
resolved — those remain M9/M10 scope, unchanged by this plan.

- [ ] **Step 2: Update `docs/wordpress-compatibility-tour.md`**

Find the walkthrough section(s) describing browsing the public home page,
admin post list, and media library. Add one short paragraph per section
noting the new pagination controls / filter bar / grid-list toggle now
present, using the same second-person walkthrough voice as the surrounding
text. Do not add new sections; extend the existing ones in place.

- [ ] **Step 3: Commit**

```bash
git add docs/compatibility.md docs/wordpress-compatibility-tour.md
git commit -m "docs: reflect M8 content browsing parity"
```

---

## Task 9: Cross-vendor validation, migration-safety check, manual review

**Traceability:** Requirement 9 (cross-vendor + accessibility + visual
review), migration-safety principle from `design.md`.

**Files:** none changed — this task only runs commands and records results.

- [ ] **Step 1: Full backend test run (all three vendor backends)**

Run: `go vet ./... && go test ./...`
Expected: PASS. `storagetest`'s contract suite (Task 6, Step 1) runs against
sqlite/postgres/mysql fixtures already wired into `internal/storage/*_test.go`
per-vendor drivers — no new vendor wiring is needed for this plan; if any
vendor is skipped in the local environment (e.g. no Postgres/MySQL
available), note which vendor(s) ran and re-run the skipped ones in CI.

- [ ] **Step 2: Full frontend test run**

Run: `cd web/admin && npm test -- --run`
Expected: PASS, including every test written in Tasks 5 and 7.

- [ ] **Step 2a: Full frontend typecheck/build gate**

Run: `cd web/admin && npm run build`
Expected: PASS. This is the final, whole-project confirmation that
`tsc --noEmit` (per `package.json:9`) has no `noUnusedLocals`/type errors
anywhere touched by Tasks 5-7 — Vitest alone does not catch unused-import or
type-mismatch regressions, so a green `npm test` run is not sufficient proof
the frontend still builds.

- [ ] **Step 3: Migration-safety check**

M8 makes no schema changes (per `design.md`). Verify no migration files were
touched:

Run: `git diff ac92650383f275a6b89ef7ba0dc1198565bd53e7..HEAD -- internal/storage/migrations internal/storage/migrate`
Expected: empty output. Do **not** use a working-tree diff (`git diff` with
no refs) for this check — it only catches uncommitted changes and would
silently pass even if an earlier commit in this plan had added a migration.
If output is non-empty, stop and reconcile with `design.md`'s no-schema-
change contract before proceeding.

- [ ] **Step 4: Manual accessibility and responsive review (not automated)**

The tests in Tasks 5, 6, and 7 automatically check: `PaginationBar`'s
`aria-label`s render, Spectrum `Picker`/`SearchField` controls are reachable
via `getByLabelText`/`getByRole`, and grid/list views are mutually exclusive.
They do **not** automatically check color contrast, focus-visible order
across the full page, or real mobile-viewport layout — those require a
human pass:

1. Start the admin app (`cd web/admin && npm run dev`) and the backend
   (existing project run command) against seeded fixture data.
2. At desktop width (~1280px) and a mobile width (~390px), for the home
   page, a category page, the post list, and the media library: tab through
   every filter control and pagination button with only the keyboard;
   confirm visible focus rings and a logical tab order.
3. Confirm the media grid/list toggle never shows both views at once at
   either width, and that filter controls wrap sensibly rather than
   overflowing at 390px.
4. Record any defects found as follow-up issues — this plan does not include
   fixing visual defects discovered during manual review; that is scoped to
   whatever follow-up the reviewer files.

- [ ] **Step 5: Final commit checkpoint**

No new commit is created by this task. Confirm `git log --oneline` shows a
clean, entirely-green sequence of commits from Task 1 through Task 8 with no
fixup/WIP commits, and `git status --short` is empty.

---

## Self-Review

Resolves an independent `superpowers:code-reviewer` NOT-READY verdict
(Critical C1-C8, Important I1-I6). Each item verified against actual plan
text and, where a repo fact is asserted, the real source — not carried over
from an earlier self-review.

| # | Defect | Resolution + evidence |
|---|---|---|
| C1 | `Pagination` field shadowing | Named field `Pagination content.Page` on `IndexData`/`CategoryData` (Task 2), not an anonymous embed; every reference is `.Pagination.Page`/`.Pagination.TotalPages` (`grep "\.Pagination\."` across Tasks 2-3, all hits qualified). `TestHomeSinglePageSiteOmitsPaginationNav` + the `TestGoldenIndex -update` step exercise a real template render so a shadow/runtime error can't compile-green. |
| C2 | Invented `srv.router.ServeHTTP` | Zero matches for `router.ServeHTTP`/`srv.router` in the finished plan. Public tests use `get(t, h, path)`; admin tests use `testAdminServer(admin)` or `SessionMiddleware(adminAPIRouter())`. |
| C3 | Untrue pagination-nav assertion | Task 2 no longer claims a multi-page nav against the 3-post `SeedFixtures` default. `TestHomeOutOfRangePageReturns404` and `TestHomeSinglePageSiteOmitsPaginationNav` (nav absent when `TotalPages<=1`) are both true of the seeded data. |
| C4 | Wrong `testAdminServer`/`principalCtx` arity | Every call site in Tasks 4/4A/6 uses `testAdminServer(admin)` (1 arg) and `principalCtx("edit_posts")` (variadic caps) — confirmed via grep, no other arity appears. |
| C5 | Wrong `runMediaContract` signature | Matches real `func runMediaContract(t *testing.T, newRepos NewReposFunc)` (`media_contract.go:12`); every new subtest opens `repos, cleanup := newRepos(t); defer cleanup()`. |
| C6 | `-run` pattern matches 0 tests | Audited all 27 `-run` occurrences: slashed patterns correctly prefix the real top-level func before `/`; unslashed substrings each match ≥1 real `func Test...` — either one of this plan's 30 new tests or a pre-existing repo test (`TestHome`/`TestCategory` — `internal/web/handlers_test.go:73,111`; `TestGoldenIndex`/`TestGoldenCategory` — `internal/render/golden_test.go:52,82`; `TestSQLiteContract`/`TestMySQLContract`/`TestPostgresContract` — `internal/storage/storagetest/{sqlite,mysql,postgres}_test.go:10-13`). The original `TestSQLiteMediaContract` mismatch is corrected to `TestSQLiteContract`/`TestMySQLContract`/`TestPostgresContract` throughout. |
| C7 | Invented `s.wrap` | Author route reads `gr.Method(http.MethodGet, "/authors", s.jsonHandler(s.adminAuthors))` inside the existing `edit_posts` group, with an inline note that `r.Get`/`s.wrap` don't exist. |
| C8 | Invented `internal/pagination` package | Task 1 exports `content.TotalPages(total, perPage int) int` (own tests) as the one shared helper; zero matches for an `internal/pagination"` import; Task 6's handler calls `content.TotalPages` directly (`content` already imported by `adminapi_media.go`). |
| I1 | Golden fixtures untouched by plan | Tasks 2/3 each include `-run TestGoldenIndex -update`/`-run TestGoldenCategory -update` against the real byte-for-byte comparison, followed by a `git diff` sanity check on the regenerated fixture; both fixtures are in the File Responsibility Map and each task's `git add` list. |
| I2 | `media_contract.go` mislabeled new | Map and Task 6 Files list both say "Existing file (`runMediaContract`)" / "Modify"; only `adminapi_media_test.go` is labeled new. |
| I3 | Unclamped `perPage` in response | `adminMediaList` reuses `perPage` from `page, perPage, offset := clampPage(...)` for `MediaFilter.Limit`, the response `PerPage`, and `content.TotalPages(total, perPage)` alike. `TestAdminMediaListClampsExcessivePerPage` asserts all three agree for `?perPage=99999` (`content.MaxPerPage == 100`). |
| I4 | Migration diff misses real dirs | Confirmed `internal/storage/migrations/{mysql,postgres,sqlite}` and `internal/storage/migrate` are distinct real dirs; Task 9 Step 3 and the header both diff `<merge-base>..HEAD` against both paths, not a working-tree snapshot. |
| I5 | `Media.test.tsx` missing router context | `App.tsx` already renders `Media` under the same top-level `<Routes>` as `PostsList` (runtime is fine); the isolated unit test still needs its own context, so both `it()` blocks unconditionally wrap in `<MemoryRouter initialEntries={["/media"]}>`, no hedged conditional language. |
| I6 | File Responsibility Map incomplete | Map now lists every file a task body actually creates/modifies, including test-only and golden files previously omitted (`pagination_test.go`, `handlers_test.go`, `adminapi_media_test.go`, `test-utils.tsx`, `PostsList.test.tsx`/`Media.test.tsx`); no row disagrees with its task's own Files list. |

**Spec coverage.** Task 1 → Req 8 (shared `Page`/`TotalPages=0` contract).
Task 2 → Req 2 (home pagination, 404 out-of-range). Task 3 → Req 2
(category pagination). Task 4 → Req 3/4 (admin search/status, invalid-status
400, missing-filter regression). Task 4A → Req 3 (author filter, capability-
gated endpoint). Task 5 → Req 8.1 (shared `PaginationBar`). Task 6 → Req 4/5
(media search/type/date/parent, filtered-total parity, title-or-filename
search). Task 7 → Req 3/5/6/7/8.1/9.3 (frontend filters, grid/list toggle,
interaction + a11y tests). Task 8 → docs accuracy. Task 9 → cross-vendor,
migration-safety, manual review. No Requirement 1-9 in `requirements.md` is
left without a task.

**Placeholder scan.** Grepped for `TODO`, `TBD`, `fill in`, `similar to
Task`, "add appropriate": none found; every code-changing step shows the
complete changed code.

**Type/signature consistency.** `PaginationBar` props defined once (Task 5),
used identically in `PostsList`/`Media` (Task 7). `WithCounter`/`RecentPage`
(Task 1) are the only counter identifiers, used by those exact names in
Tasks 2/9. `MediaFilter{Search,Type,After,Before}` (Task 6) match what
`adminapi_media.go` parses and the contract subtest exercises. `AuthorOption`
shares `id`/`displayName` on both sides of the Task 4A/7 JSON boundary.

**Commit-boundary greenness.** Every task's commits are additive-first
(new method/field before its consumer) so no commit is followed by a later
commit fixing a break it introduced: Task 1's three commits only add
methods/types; Tasks 2/3 pair each template/handler edit with its golden
regeneration and test in one commit; Task 4's three commits order forwarding
fix → search wiring → regression tests, each safe because `AdminListFilter`
only gains fields; Task 4A adds `Authors()` + route, then fixtures/tests;
Task 6 lands the storage predicate (with contract test) before the handler
(with its own test, including the I3 clamp regression); Task 7 pairs each
UI change with its own test. Every `go vet ./...` gate precedes rather than
follows its implementation step.

**Compilation-fake fallout audited.** `adminrepo_test.go`'s `adminFake`
gains `Authors` in Task 4A's own commit. No other `domain.PostRepository`/
`TermRepository`/`AdminRepository` fake exists beyond what Tasks 1, 4, 4A,
6 enumerate and update in the same commit as the widening interface change.
Every `go vet`/`go test`/`npm test` invocation matches this repo's
established commands; no invented flags or paths.

**Line budget.** `wc -l` on this file is 3,023 lines — modestly above the
requested 2600-2900 target after multiple correctness-driven expansions
(exact test code/pseudocode for every new behavior, per-defect fix
evidence). Compression applied: file:line anchors + diff hunks instead of
re-quoted whole files, one shared `renderWithSpectrum` test helper (Task 5)
across every frontend test, and a table-based (not prose) Self-Review.
Further cuts were judged unsafe: remaining content is either exact test
code/pseudocode this plan's own conventions require, or the ground-truth
citations that prior review rounds required to prevent invented
signatures/paths. No task body was shortened at the cost of executability.

## Self-Review (Round 4)

Resolves a second independent `superpowers:code-reviewer` NOT-READY verdict
(Critical 1-4, Important 5-7, plus minors) found after Round 3's C1-C8/I1-I6
were already applied. Each item re-verified empirically against the plan
text as it stands now (not against the prior round's self-review claims).

| # | Defect | Resolution + evidence |
|---|---|---|
| C1 | Contradictory `Before` contract fixture | Task 6's `Before` test input changed from noon to `2024-01-06T00:00:00Z`; `AddDate(+1 day)` widening now yields an inclusive-day boundary of exactly `2024-01-07T00:00:00Z`, excluding fixture 202 (`2024-01-07 00:00:00`) and including only fixture 201 — `wantID:[201]` is now true of the stated predicate, not merely asserted. |
| C2 | `sub`/`add` template funcs with no `FuncMap` | Task 2 now adds `templateFuncs` (`add`, `sub`) and rewrites the single real parse site, `internal/render/engine.go:67`, to `template.New(baseTemplate).Funcs(templateFuncs).ParseFiles(files...)`, with its own test; `engine.go` is in the File Responsibility Map, Task 2's Files list, and its `git add`. |
| C3 | `Media.test.tsx` asserted `role="radio"` | Spectrum `ToggleButton` renders `role="button"`; every toggle assertion now uses `getByRole("button", {name: /list view/i})`/`getByRole("button", {name: /grid view/i})`, matching `Media.tsx`'s real `aria-label`s. |
| C4 | Req 5.6/6.4 ACs unimplemented | `Media.tsx` gained native `after`/`before` date inputs, a `parentId` `Picker` backed by `parentOptions` (`useEffect` + `api.posts({perPage:100})`), and URL-param-backed `view` state (`?view=grid|list`, default grid, survives reload, does not reset `page`). `Media.test.tsx` gained dedicated test cases for date/parent filter propagation and for view persistence via `initialEntries={["/media?view=list"]}`. |
| I5 | Unproven frontend test idioms | `PostsList.test.tsx`/`Media.test.tsx` now both mock via `importOriginal` factories (preserving the real module's other exports, e.g. `ForbiddenError`) instead of bare automocks, drive all interaction through `userEvent`, assert grid/list exclusivity via `data-testid` rather than an unverified `role="grid"`, and add a keyboard-operability test (`.focus()` + `user.keyboard("{Enter}")`) for Req 9.3. |
| I6 | Task 1/map said `adminread.go` untouched | Task 1's Files list and the File Responsibility Map both now state the true scope: Task 1 only does the mechanical `clamp` call-site fix at `adminread.go:73-76` (kept compiling standalone), and Task 4 edits the same lines again later to add the full `AdminListFilter`. No row claims the file is untouched. |
| I7 | `PostsList.tsx` snippet used undefined `page` | The Task 7 Step 4 state-replacement snippet now begins with `const page = Math.max(1, Number(params.get("page") || "1") || 1);` before referencing `page` anywhere else in the block. |
| minor | False `runTermReaderContract` rationale | Task 1/Step 3d prose now states plainly that no separate `runTermReaderContract` helper exists (unlike `runMediaContract`/`runAdminContract`) and that term-repository coverage lives inline in `RunContract` itself, next to `repos.Terms.BySlug`. |
| minor | Wrong `api.authors` expected-FAIL reason | Task 7 Step 3's expected-FAIL text no longer claims `api.authors` doesn't exist (it does, from Step 1); it now says `PostsList` simply never calls it yet. |
| minor | `encoding/json` imported before used | `adminapi_media_test.go`'s Step 8 code block no longer imports `encoding/json` (Step 8's own test never calls `json.Unmarshal`); Step 10's prose now explicitly adds the import there, alongside the first `json.Unmarshal` call, so no intermediate commit/step has an unused import. |
| minor | "Create" vs "extend" labeling | `PostsList.test.tsx`/`Media.test.tsx` Files-list entries correctly say "Create" (both are new files), matching the actual Task 7 steps. |

**Empirical re-verification performed this round (not carried over):**
- Re-derived the `Before` boundary arithmetic by hand against the two seed
  fixture timestamps rather than trusting the prior claim.
- Grepped the whole file for `role="radio"`/`getByRole\("radio"` — zero
  hits after the C3 fix; confirmed `Media.tsx`'s real `aria-label`
  strings ("Grid view"/"List view") match the test's regex names.
- Grepped for `api.posts` and cross-checked its call shape
  (`{perPage: 100}`) against the real `client.ts` signature reproduced in
  Task 7 Step 1 (`page?, perPage?, type?, status?, search?, author?`).
- Grepped for `encoding/json` and `json\.(Unmarshal|Marshal)` together to
  confirm the import now appears in the same step as its first use.
- Re-read Task 1's `adminread.go` prose and the File Responsibility Map row
  side by side to confirm they now agree (mechanical fix now, full filter
  in Task 4).

**Line budget (updated).** `wc -l` is now 3,253 lines after the C4 filter-UI
additions (date/parent controls, four new Media test cases) — over the
2600-2900 target, as in Round 3. The overage is entirely load-bearing:
two previously-missing acceptance criteria (Req 5.6, Req 6.4) required new
production code and new tests, not narrative. No speculative or duplicate
content was added; nothing further was judged safe to cut without losing
executability.

## Self-Review (Round 5)

Resolves a third independent review's findings: 3 Criticals (invalid Spectrum
icon import, a missed `fakeAdmin.list` call site, unused imports that would
break the real build) and 3 Importants (a false-green `go vet` ordering,
missing `AbortController`/error differentiation on two async fetches, no
frontend typecheck/build gate), plus minors. Every item below was checked
against real source in this repo or a real published package listing, not
against the prior round's self-review claims.

| # | Defect | Resolution + evidence |
|---|---|---|
| C1 | `@spectrum-icons/workflow/ListView` does not exist | Fetched `unpkg.com/@spectrum-icons/workflow@4.2.0/?meta` (the version pinned in `web/admin/package.json`) and confirmed the file list contains `ViewList.js` and no `ListView.js`. Task 7's Media.tsx import and JSX now use `ViewList`, `ClassicGridView` unchanged. |
| C2 | Missed `fakeAdmin.list` call site | `grep -rn "fakeAdmin{"` across `internal/web` found two literal sites setting the `list` field: `adminapi_test.go` (already updated) and `internal/web/adminapi_terms_test.go:272` (missed). Task 4 now shows that file's before (`list: func(int,int,string,string)...`) and after (`func(int, int, content.AdminListFilter)`) rewrite, adds it to Task 4's Files list, the File Responsibility Map, and the Task 4 `git add` line. |
| C3 | `Text`/`ChevronLeft`/`ChevronRight` become unused after the `PaginationBar` swap | Viewed the real current `PostsList.tsx` to confirm exactly which imports the inline Page/Prev/Next block was the last user of. Task 7 Step 4 now explicitly deletes all three from the import list (with the `noUnusedLocals`/`tsc --noEmit` rationale inline) instead of leaving them for the reader to notice. Added `cd web/admin && npm run build` gates after Task 7's PostsList step and after its Media step, so a stray unused import fails the plan's own boundary, not just CI. |
| I4 | `go vet ./...` at Task 1 Step 3c expected PASS before `fakeTermRepo` was widened | Read `internal/content/term_test.go` (`fakeTermRepo` fields: `tax, slug, term, err, called` — no `countPublished`) and `internal/content/term.go:19` (`NewTermService(t domain.TermRepository, p domain.PostRepository)`). Since `fakeTermRepo` is passed as the `TermRepository` argument, widening that interface without widening the fake fails `internal/content` compilation, so `go vet ./...` cannot pass with the fake still unwidened. Moved the `fakeTermRepo` widening (new `countPublished` field, `CountPublishedByTermSlug` method) from Step 3g into Step 3a, immediately next to the interface widening it must accompany; Step 3g now just notes the fake was already widened. |
| I5 | Raw `useEffect`+`cancelled`-flag fetches, no `AbortController`/error differentiation | Found the repo's existing shared idiom at `web/admin/src/hooks.ts:13`, `useAsync<T>(load, deps)`, already used by `App.tsx`, `Dashboard.tsx`, `Comments.tsx`, `Menus.tsx`, `RevisionsPanel.tsx`, `TermPicker.tsx`, `MediaPicker.tsx`: it owns its own `AbortController`, aborts on unmount/dep-change, and distinguishes an aborted fetch (silently ignored) from `ForbiddenError` (→ `status:"forbidden"`) from any other error (→ `status:"error"`). PostsList's `api.authors()` fetch and Media's `api.posts({perPage:100})` (parent-post options) fetch are both rewritten to call `useAsync`, deriving `authorOptions`/`parentOptions` from `status === "success"`; both forbidden and error degrade to an empty options list without blocking the primary posts/media fetch. Removed the now-unneeded `useEffect` react import (PostsList already imports `useAsync`/`useState`, not `useEffect`) and Media's `import { useEffect, useState }` → `import { useState }`. Also removed Media's now-unused `import type { PostListItem }` (the explicit `useState<PostListItem[]>` annotation it typed no longer exists), the same `noUnusedLocals` failure mode as C3. Verified via `grep` for stray `setAuthors`/`setParentOptions`/`const [authors]`/`[authors, setAuthors]` across the file: zero matches. |
| I6 | No frontend typecheck/build gate anywhere in the plan | Confirmed `web/admin/package.json`'s `build` script is `"tsc --noEmit && vite build"` (the same command C3's gates use) and `web/admin/tsconfig.json:15` sets `"noUnusedLocals": true`. Added Task 9 Step 2a, a full `npm run build` run after the frontend test suite, so type/unused-import regressions surface before the plan calls the milestone done, in addition to the two Task-7 gates from C3. |
| minor | `user.type` on native `<input type="date">` is not reliable in jsdom | jsdom's `type="date"` value setter parses/validates the whole string atomically rather than supporting `user-event`'s per-keystroke segmented editing (each intermediate partial string, e.g. `"2"`, `"20"`, is an invalid date and is rejected/reset), so simulated typing never lands the intended final value. Task 7's `Media.test.tsx` date-filter assertions now use `fireEvent.change(input, {target:{value:"2024-01-01"}})`, with `fireEvent` added to the test file's `@testing-library/react` import and an inline comment explaining why (Media.tsx's real date inputs are plain `<input type="date" onChange={(ev) => setFilter(..., ev.target.value)}>`, so a whole-string `change` event is exactly what the component listens for). |

**Empirical re-verification performed this round (not carried over):**
- Fetched the real `@spectrum-icons/workflow@4.2.0` package file listing over
  HTTP (no local `node_modules` in this worktree) rather than guessing or
  trusting a web search.
- Grepped every `fakeAdmin{` construction site in `internal/web` to confirm
  exactly two set the `list` field, and that both are now updated.
- Read `internal/content/term_test.go` and `internal/content/term.go` line
  19 directly to derive, rather than assume, why `go vet ./...` needs
  `fakeTermRepo` widened before Step 3c.
- Read `web/admin/src/hooks.ts` in full to confirm `useAsync`'s exact
  `AbortController`/forbidden/error/success behavior before citing it as the
  fix, rather than inventing a bespoke pattern.
- Read `web/admin/tsconfig.json` and `web/admin/package.json` to quote the
  real `noUnusedLocals` flag and `build` script rather than assuming them.
- Read `Media.tsx`'s real date-input JSX (plain `onChange`, no Spectrum
  wrapper) to confirm `fireEvent.change` is the correct, not merely
  convenient, fix for those two fields.
- Grepped the whole file for `ListView\b`, `user.type.*date`, and stray
  `setAuthors`/`setParentOptions` references after each fix: zero
  remaining hits in every case.

**Line budget.** `wc -l` is 3,387 lines after this round's fixes (net +134
over Round 4's 3,253): the `fakeTermRepo`/`useAsync` rewrites are a wash
(replaced code, not added); the growth is the new File Responsibility Map
row and Task 4 rewrite for `adminapi_terms_test.go:272` (C2), the two Task
7/Task 9 build-gate steps (C3/I6), the date-input `fireEvent.change` fix's
inline rationale comment, and this Self-Review section itself. No narrative
padding was added; every added line documents an actual code, test, or gate
change required by a verified defect.

## Self-Review (Round 6)

Resolves a fourth independent review's findings: 1 Critical (Task 4 wrongly
framed `internal/content/adminread_test.go` as a new file to create,
which would have deleted 213 lines of real, existing tests and helpers)
and 1 Important (the frontend build gate only appeared at Tasks 7/9, not
after Task 5's new component), plus a cosmetic File Responsibility Map
mislabel. Verified by reading the real file end to end, not by trusting
this plan's own prior description of it.

| # | Defect | Resolution + evidence |
|---|---|---|
| C1 | Task 4 said "Create `internal/content/adminread_test.go`" | Read the real file in full: 213 lines, 7 existing tests (`TestAdminServiceListClampsAndPaginates`, `TestAdminServiceListTotalPagesRoundsUp`, `TestAdminServiceListEmptyFiltersLeaveTypesUnset`, `TestAdminServiceDetailPropagatesNotFound`, `TestAdminServiceDetailReturnsPost`, `TestAdminServiceStatsAggregates`, `TestAdminServiceDisplayName`), a `fakeAdminData` closure-per-method fake (`list`/`count`/`byStatus`/`countUsers`/`countTerms`/`postByID`/`userByID` fields), a separate `fakeUserReader`, and a `newAdminService(data, users)` helper (`NewAdminService(data, data, data, data, data, users)`). Task 4's Files list now says "Modify (existing file — do not delete, redefine, or rename any existing test, helper, or type)" with the shape above spelled out. Task 4 Step 1 no longer invents a `fakeAdminPosts` type; it shows the exact before/after diff for the three real old-signature call sites (`svc.List(ctx, page, perPage, typ, status)` at real lines 63, 98, 129) migrating to `svc.List(ctx, page, perPage, AdminListFilter{...})`, then appends two new tests (`TestAdminServiceListForwardsSearchToCount`, `TestAdminServiceListMissingFilterFieldsMeansUnfiltered`) built on the real `fakeAdminData`/`newAdminService` helpers. Task 4A's Step 3a — which the same defect had left referencing the now-deleted `fakeAdminPosts` — is rewritten to widen the real `fakeAdminData` additively (`authors func() ([]domain.AuthorOption, error)` field, `Authors(ctx)` method) and its two tests (`TestAdminServiceAuthorsDelegates`, `TestAdminServiceListForwardsAuthor`) now construct `&fakeAdminData{...}` + `newAdminService(data, &fakeUserReader{})` instead of `NewAdminService(posts, nil, nil, nil, nil, nil)` on the invented type. `grep -n "fakeAdminPosts"` over the whole plan now returns zero matches — no orphaned references remain. The File Responsibility Map gained a dedicated `internal/content/adminread_test.go` row describing this Modify-not-Create shape, matching both Task 4's and Task 4A's `git add` lines (both already listed the file; only the prose and code were wrong). |
| I1 | `npm run build` gate only followed Tasks 7 and 9 | Task 5 (the first task to add new frontend source, `PaginationBar.tsx`) had no typecheck/build gate before its commit — a stray unused import or type error there would not surface until Task 7. Added Step 5b, `cd web/admin && npm run build`, between Task 5's test-pass step and its commit step, with a note that this gate now runs before every remaining frontend commit in the plan (5, 7, 9), not only at task or milestone boundaries. |
| minor | File Responsibility Map row conflated "extend existing" across two files | The `PostsList.tsx` row read `(+ .test.tsx, extend existing)`, implying both the modified `.tsx` and the brand-new `.test.tsx` (Task 7 Step-confirmed "create — no existing file") were being extended. Split into `PostsList.tsx (extend existing) + .test.tsx (new file — no existing file)`, matching the adjacent `Media.tsx` row's already-correct `(+ .test.tsx, new)` pattern. |

**Empirical re-verification performed this round (not carried over):**
- Read `internal/content/adminread_test.go` in full (213 lines) before
  writing any fix, confirming line numbers, test names, and fake shapes
  against the plan's claims rather than the plan's prior self-review.
- Read `internal/content/adminread.go` in full (153 lines) to confirm the
  current pre-Task-4 `NewAdminService` and `List` signatures the migration
  diffs must match.
- Read `internal/domain/adminrepo_test.go` in full (76 lines) to confirm
  `adminFake` (domain package, value receiver) is a distinct fake from
  `fakeAdminData` (content package, pointer receiver) and that Task 4A's
  Step 1c widening of `adminFake` needed no change.
- `grep -n "fakeAdminPosts"` across the whole plan after the rewrite:
  zero remaining hits.
- `grep -n "PostsList.test.tsx"` and `grep -in "extend existing"` across
  the whole plan to confirm no other file carries the same mislabel and
  that every other `PostsList.test.tsx`/`Media.test.tsx` reference already
  said "create".
- Confirmed Task 4's and Task 4A's existing `git add` lines already list
  `internal/content/adminread_test.go` — only the prose/code above them
  needed correction, not the commit boundary itself.

**Line budget.** `wc -l` is expected to grow modestly over Round 5's 3,387
(the Task 4/4A rewrites replace roughly as much invented code as they add
real diffs for; net growth is the new File Responsibility Map row, Task
5's build-gate step, and this Self-Review section). No narrative padding
was added; every added line documents an actual code, test, or gate change
required by a verified defect.
