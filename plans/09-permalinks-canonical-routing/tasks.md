# Tasks — M9a: Permalinks & Canonical Routing

Implementation checklist for M9a (roadmap groups 9.A, 9.B, 9.F). Tasks are
ordered so each builds on the last, and each references the requirement it
satisfies. Keep `gofmt -l .` empty and `go vet ./...`, `go build ./...`,
`go test ./...` green after every phase.

Strict TDD: within each phase the failing test is written before the
implementation, matching how M5–M7 were run.

> **Scope note.** Roadmap groups 9.C (tag/date/author archives) and 9.D (nested
> categories) are **not** in this milestone — see `requirements.md` "Out of
> scope", which also records the two design decisions already resolved on the
> follow-on's behalf. Task 4.1 lands 9.C's template kinds only, so the follow-on
> adds route handlers with no template plumbing left to discover.

## Phase 0 — Spec

- [ ] 0.1 Write `requirements.md`, `design.md`, `tasks.md` and add the row to the
      `plans/README.md` milestone index (status → Specified).
  - _Acceptance:_ All three files exist and the index links them.
- [ ] 0.2 Open the spec PR and obtain review approval before writing code.
  - _Acceptance:_ PR open, CI green, approved.

## Phase 1 — `internal/routing` package (pure, no DB, no HTTP)

- [ ] 1.1 Write failing table-driven tests for `routing.Parse`: the three
      WordPress presets ("Day and name" `/%year%/%monthnum%/%day%/%postname%/`,
      "Month and name" `/%year%/%monthnum%/%postname%/`, "Post name"
      `/%postname%/`); empty structure → `Flat`; `%post_id%`-only; a structure
      with an unsupported token (`%category%`, `%author%`) → `ErrUnsupported`
      naming the token; a structure with no identifying token → `ErrUnsupported`;
      `category_base`/`tag_base` defaulting to `category`/`tag` when empty and
      overriding when set. _(Req 1.3, 1.4, 1.5, 2.1, 2.4, 4.1)_
- [ ] 1.2 Implement `Token`, `Structure`, `Ref`, `Parse` and `ErrUnsupported`.
      `Parse` records `TrailingSlash` from whether the raw structure ends in `/`.
      _(Req 1.1, 2.1, 3.3, 4.1)_
- [ ] 1.3 Write failing tests for `Structure.ChiPattern`: each preset maps to the
      expected chi pattern; `Flat` yields `""`. _(Req 2.1)_
- [ ] 1.4 Implement `ChiPattern`. _(Req 2.1)_
- [ ] 1.5 Write failing tests for `Structure.Match`: extracts slug/id and date
      parts; rejects a 1-digit month or day and a non-4-digit year; rejects a
      non-numeric `%post_id%`. _(Req 2.2, 2.4)_
- [ ] 1.6 Implement `Match`. _(Req 2.2, 2.4)_
- [ ] 1.7 Write failing tests for `Structure.Canonical`: correct zero-padded
      output per preset; trailing slash present/absent per structure; and the
      **fixed-point property** — `Canonical` applied to a post, then matched and
      re-canonicalised, yields the identical string. _(Req 2.2, 3.3, 3.5)_
- [ ] 1.8 Implement `Canonical` as the single construction site for a permalink,
      so the redirect target and the REST `link` field cannot diverge.
      _(Req 3.1, 3.3, 6.1)_

## Phase 2 — Published-post-by-id read path

- [ ] 2.1 Write a failing `storagetest` contract case for
      `PostRepository.ByID`: returns a published post by id; returns
      `domain.ErrNotFound` for an unpublished or absent id; honors the
      `types...` default of `{"post","page"}`. Runs across all three vendors,
      mirroring the existing `BySlug` contract case. _(Req 2.4, 7.3)_
- [ ] 2.2 Add `ByID(ctx, id int64, types ...string) (Post, error)` to
      `domain.PostRepository` and implement it in `internal/storage/wprepo`,
      reusing `BySlug`'s published-only/type-defaulting semantics. Do **not**
      reach through the write-side `PostWriter.ByID` from the public read path.
      _(Req 2.4)_

## Phase 3 — Web wiring: route registration, resolution, canonical redirects

- [ ] 3.1 Write failing handler tests covering every row of `design.md`'s
      status-code table: canonical path → `200`; non-canonical but matching →
      `301` with exact `Location`; flat `/{slug}` while a structure is configured
      → `301`; date components contradicting the post → `404`; matching path with
      no such published post → `404`; unmatched path → `404`.
      _(Req 2.3, 2.5, 2.6, 3.1, 3.2)_
- [ ] 3.2 Write a failing test asserting a request already at the canonical path
      issues **no** redirect, guarding against a redirect loop.
      _(Req 3.5)_
- [ ] 3.3 Write a failing test asserting the query string survives a canonical
      redirect. _(Req 3.4)_
- [ ] 3.4 Write a failing test asserting that when `permalink_structure` is
      empty, no canonical redirect is issued and `/{slug}` renders `200` exactly
      as before this milestone. _(Req 1.2, 3.6)_
- [ ] 3.5 Register `Structure.ChiPattern()` in `router.go` when not `Flat`,
      ahead of the existing `/{slug}` route, leaving the relative order of
      `/category/{slug}`, `/`, `/login`, `/comment` and
      `/wp-content/uploads/*` unchanged. _(Req 2.1, 2.6)_
- [ ] 3.6 Extend `single` in `handlers.go`: build a `Ref`, resolve by slug or id,
      verify date components against `post.Date`, compare the request path to
      `Canonical(post)`, and `301` on mismatch preserving the query string.
      Flat behavior must be byte-for-byte unchanged. _(Req 2.3, 2.5, 3.1-3.4)_

## Phase 4 — Template hierarchy (9.F)

- [ ] 4.1 Write a failing test asserting `render.Render` resolves the `tag`,
      `author` and `date` kinds through `{kind}` → `archive` → `index`, and that
      a kind with no candidate template present still falls back to `index`
      rather than erroring. _(Req 5.2, 5.3, 5.5)_
- [ ] 4.2 Add the three entries to the existing `hierarchy` map in
      `internal/render/engine.go`. No new resolution mechanism, and no
      slug-specific candidates. _(Req 5.1, 5.2, 5.4)_

## Phase 5 — REST `link` field

- [ ] 5.1 Write a failing test asserting `postLink` returns the canonical
      permalink for a configured structure and `/{slug}` when `Flat`, and that
      `commentLink`/`userLink` are unchanged. _(Req 6.1, 6.2, 6.4)_
- [ ] 5.2 Give the REST mapper a `routing.Structure` at construction and route
      `postLink` through `Structure.Canonical`, rather than threading a structure
      argument through every call site. _(Req 6.1)_
- [ ] 5.3 Confirm the web layer still resolves the relative `link` to an absolute
      URL from the request scheme/host, with no change required.
      _(Req 6.3)_

## Phase 6 — Startup wiring and loud fallback

- [ ] 6.1 Write a failing test asserting that an unsupported structure yields a
      `Flat` fallback plus an error naming the offending token(s), and that
      construction still succeeds. _(Req 4.1, 4.2, 4.3)_
- [ ] 6.2 Wire `cmd/grimoire/main.go`: read `permalink_structure`,
      `category_base` and `tag_base` once via `OptionService.Get`, call
      `routing.Parse`, log `INFO` with the resolved structure on success and
      `WARN` on fallback — the warning naming the unsupported token(s) **and**
      stating that published URLs will not resolve while the fallback is active.
      Pass the `Structure` to the server and the REST mapper. _(Req 1.1, 1.6,
      4.2, 4.4)_
- [ ] 6.3 Extend `grimoire-cli migrate -check`'s report with the resolved
      permalink structure and whether it is supported. Read-only; no change to
      any migration path. _(Req 4.5)_

## Phase 7 — End-to-end, real-database fixture, and docs

- [ ] 7.1 Add a `test/e2e` test booting the stack with a dated structure:
      a published post's canonical URL renders `200`, its flat URL `301`s to the
      canonical, and an unrelated path `404`s. Build the DSN with the existing
      `testDSN` helper so it inherits the SQLite `busy_timeout` setting from #34.
      _(Req 7.2)_
- [ ] 7.2 Add an env-var-gated test against a real WordPress database, skipped
      unless the DSN variable is set, mirroring
      `../02.1-wp-hash-real-db`'s gating so CI stays hermetic. It must exercise a
      dated permalink structure **and** a non-default table prefix — the podman
      stack in `accuweaverllc/scripts` provides both
      (`accuweaver` prefix, `/%year%/%monthnum%/%day%/%postname%/`).
      _(Req 7.4)_
- [ ] 7.3 Update `docs/compatibility.md` to move permalinks from the known-gaps
      list into "What's implemented", and update the root `README.md` Status
      section, which currently names the permalink gap and links #23.
      _(Req 3, 6)_
- [ ] 7.4 Update `plans/README.md`: this milestone → Implemented, and mark
      roadmap groups 9.A/9.B/9.F complete in
      `../wordpress-core-parity-roadmap/tasks.md`, leaving 9.C/9.D open.
      **Tick the boxes in this file as the work lands** — the whole reason
      #29 existed was that four milestones shipped with their checkboxes
      untouched.
- [ ] 7.5 Confirm no new migration file was added anywhere in the repo
      (`git status` shows no new file under any `migrations`-style directory) —
      this milestone must ship with zero schema changes. _(Req 7.5)_
- [ ] 7.6 Full gate sweep: `gofmt -l .` empty, `go vet ./...`,
      `go build ./...`, `go test ./...` all green; MySQL/Postgres contract runs
      if DSNs are available.
- [ ] 7.7 Close [#23](https://github.com/roboweaver/grimoire/issues/23) from the
      implementation PR.
