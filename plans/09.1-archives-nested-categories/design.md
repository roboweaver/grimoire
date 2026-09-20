# M9b — Archives & Nested Categories: Design

## Overview

Three facts about the code as it stands set this milestone's shape.

**One archive route exists.** `internal/web/router.go` registers

```go
r.Method(http.MethodGet, "/category/{slug}", s.handler(s.category))   // router.go
```

with a hard-coded base segment and a single slug parameter, and
`internal/web/handlers.go`'s `category` reads that one parameter:

```go
slug := chi.URLParam(r, "slug")
term, posts, pg, err := s.terms.CategoryPage(ctx, slug, page, content.DefaultPerPage)
```

**The hierarchy is invisible.** `domain.Term` carries `ID`, `Name`, `Slug`,
`Taxonomy` and nothing else, and `termRow` in
`internal/storage/wprepo/repo.go` selects `t.term_id, t.name, t.slug,
tt.taxonomy` — `term_taxonomy.parent` is joined over and never read, on all
three vendors.

**The bases are resolved but inert.** `routing.Parse` already produces
`Structure.CategoryBase` and `Structure.TagBase`, and
`cmd/grimoire/permalinks.go` already logs both at startup. No route consumes
either, which is why `cmd/grimoire-cli/permalinks.go` carries a comment
explaining that printing them would tell an operator their override is in effect
when it is not.

This design adds four archive routes (nested category, tag, date, author), a
descendant-aware published-post query, a `ParentID` on `domain.Term`, and — the
structural core of the milestone — **one classifier in `internal/routing` that
decides what a path addresses**, so route precedence stops depending on chi
registration order. No schema change.

## Architecture

```
                         startup (unchanged from M9a)
{prefix}options ─► OptionService.Get ─► routing.Parse ─► routing.Structure
                                                             │
                                          ┌──────────────────┴───────────────────┐
                                          │                                      │
router.go registers, all to ONE handler:  │            Structure.Classify(path)  │
  structure patterns (both slash forms)   │                     │                │
  /{slug}                                 │      ┌──────────────┼──────────┬─────┴────┐
  date patterns (1/2/3 segments)          │   KindPost     KindCategory  KindTag  KindDate
  /{CategoryBase}/*                       │      │              │          │     KindAuthor
  /{TagBase}/{slug}                       │      ▼              ▼          ▼         ▼
  /author/{nicename}                      │   single     categoryArchive  tag…    date…/author…
                                          └──────────────────────────────────────────────┘
                                                             │
                                   compare r.URL.Path to Structure.<Kind>Path(...)
                                        equal ─► 200 render        differ ─► 301
```

### The structural idea: one handler, one classifier

M9a already discovered, and recorded in `router.go`'s route-registration
comment, that chi treats the two trailing-slash forms as distinct routes and
that a single-segment permalink structure collides with `/{slug}` **at chi's
parameter node, which chi resolves silently in favour of whichever pattern was
registered last rather than panicking**. M9a's answer was to stop reading chi's
parameter names and derive components from `r.URL.Path` through
`Structure.ParamsFromPath`.

M9b makes that collision far worse. Date archives are 1, 2 and 3 bare segments;
a "Month and name" structure (`/%year%/%monthnum%/%postname%/`) is also 3
segments of the same parameter shape; the flat `/{slug}` route is 1 segment;
and nested category paths have a segment count no chi pattern can express at
all.

Rather than fight for a registration order that produces the right winner, this
design removes the incentive to care: **every ambiguous pattern is registered to
the same handler.** If two patterns collide at a chi parameter node, both point
at `s.resolve`, so which one chi silently picks has no observable effect.
`s.resolve` then calls `Structure.Classify(r.URL.Path)` once and delegates.
Precedence lives in one pure function with a documented table and a unit test
per row, rather than in the order of eight `r.Method` calls.

The static-first-segment routes (`/{CategoryBase}/*`, `/{TagBase}/{slug}`,
`/author/{nicename}`) are unambiguous — chi prefers a static node over a
parameter node deterministically — but they are registered to `s.resolve` as
well, so there is exactly one implementation of precedence rather than one for
the ambiguous cases and another for the clear ones.

Routes registered *before* the dispatcher (`/healthz`, `/login`, `/comment`,
`/`, `/wp-content/uploads/*`, `/admin/*`, `/wp-json/*`, the theme/static
handlers) all have static first segments and continue to win in chi. `Classify`
therefore does not need to know about them, and Requirement 9.6's "no existing
route changes behavior" is a property of chi's static-over-parameter preference,
not of anything this design adds.

### Why `internal/routing` grows rather than a new package appearing

The brief's constraint and M9a's design agree: `Structure.Canonical` is
deliberately the single construction site for a post permalink, so the redirect
target and the REST `link` field cannot diverge. Archive URLs need exactly the
same property — a category's canonical path is consumed by the `301` handler,
the theme's pagination links **and** `restTermLink`, and those three disagreeing
is the bug. So the archive path constructors go next to `Canonical`, in the same
pure, no-DB, no-HTTP package, and `Classify` goes there too because it is the
inverse operation on the same grammar.

## `internal/routing` additions

```go
// Kind is what a request path addresses.
type Kind int

const (
    KindNone Kind = iota // no archive or post interpretation; the caller 404s
    KindPost
    KindCategory
    KindTag
    KindAuthor
    KindDate
)

// DateRef is a date archive at year, year/month or year/month/day granularity.
// Month and Day are 0 when the path did not carry them.
type DateRef struct{ Year, Month, Day int }

// Range returns the half-open [start, end) interval the archive covers, and
// false when the components do not form a real calendar date.
//
// The interval is built with time.Date(..., time.UTC), which is what "no
// timezone conversion" requires here: formatTS is
// `return t.UTC().Format(tsLayout)` (internal/storage/wprepo/helpers.go:27), so
// a UTC-constructed bound passes through it unchanged, and post_date itself is
// UTC-based wall-clock time because parseTS reads it with
// time.ParseInLocation(layout, s, time.UTC) followed by .UTC() (helpers.go:52).
// Building the interval in time.Local instead would shift every bound by the
// host's offset: on a UTC-7 host a May 2024 archive would query
// 2024-05-01 07:00:00 .. 2024-06-01 07:00:00, dropping the first seven hours of
// May 1 and including the last seven of April 30.
func (d DateRef) Range() (start, end time.Time, ok bool)

// Target is the classified meaning of one request path.
type Target struct {
    Kind Kind
    // Post is set when Kind is KindPost.
    Post Ref
    // Segments is the category path as given, base segment excluded, root
    // first. Set when Kind is KindCategory. The handler walks it through the
    // taxonomy graph (see "Category archives resolve by walking the segment
    // path"); a walk that fails is what produces the 301 or the 404, so the
    // classifier makes no claim about whether these segments name anything.
    Segments []string
    // Slug is the tag slug or the author nicename, per Kind.
    Slug string
    // Date is set when Kind is KindDate.
    Date DateRef
    // TrailingSlash records the form the request arrived in, so the handler can
    // canonicalise it without re-parsing the path.
    TrailingSlash bool
}

// Classify decides what path addresses, applying a fixed precedence order (see
// the table below). It is pure and total: an unrecognised path yields KindNone.
func (s Structure) Classify(path string) Target

// Archive path constructors. These are the only places an archive URL is built,
// for the same reason Canonical is the only place a permalink is built. Each
// applies the front per the table in "The front" below, so no caller decides
// whether a front applies and no two callers can decide differently.
//
// ancestry is root-first and includes the category's own slug, so a top-level
// category is a one-element slice and its path equals the flat path.
func (s Structure) CategoryPath(ancestry []string) string
func (s Structure) TagPath(slug string) string
func (s Structure) AuthorPath(nicename string) string

// DatePath builds the archive path at the granularity d carries. It prepends
// the front (always, for dates) and then WordPress's "date/" disambiguation
// segment when the structure needs it -- see "Date disambiguation" below.
// Returns "" for a Flat structure, which serves no date archives at all (Req
// 6.8).
func (s Structure) DatePath(d DateRef) string

// ArchivePatterns returns every chi pattern the archive routes need, both slash
// forms, derived from the resolved bases and the structure's front. Returned as
// a slice for the same reason ChiPatterns is: the caller registers all of them
// to one handler. A Flat structure yields no date patterns (Req 6.8); its
// category, tag and author patterns are unchanged, since grimoire already serves
// /category/{slug} under plain permalinks.
func (s Structure) ArchivePatterns() []string
```

`AuthorBase` is the literal `author` and is not configurable, because WordPress
stores no `author_base` option — it is a `WP_Rewrite` property, not a row in
`{prefix}options`. `Structure` exposes it as a constant (`routing.AuthorBase`)
so the classifier, the constructor and the collision check in `Parse` all read
the same value.

### The front

The structure's **front** is its leading literal segment(s) — `blog` in
`/blog/%year%/%monthnum%/%postname%/`. `Parse` already records literal segments
(`segment{tok: tokLiteral}`), so the front is derivable today: it is the maximal
run of leading literals before the first token. Nothing consumes it yet.

WordPress applies the front to archives **asymmetrically**, and grimoire
replicates that asymmetry (Requirement 4.7–4.9) because the point of the
milestone is that an existing site's published URLs keep working:

| Archive kind | Front applied? | WordPress source of the rule |
|---|---|---|
| date | **always** | `WP_Rewrite::get_date_permastruct()`: `$this->date_structure = $front . $date_endian` |
| author | **always** | `WP_Rewrite::get_author_permastruct()`: `$this->author_structure = $this->front . $this->author_base . '/%author%'` |
| category | only when `category_base` is unset | `create_initial_taxonomies()`: `'with_front' => ! get_option( 'category_base' ) \|\| $wp_rewrite->using_index_permalinks()` |
| tag | only when `tag_base` is unset | same call, `! get_option( 'tag_base' )` |

**On the author archive specifically** — stated explicitly rather than picked
silently, since it is the one kind whose rule is not obvious from the option
table: WordPress treats the author base the same way it treats the date
structure. `get_author_permastruct()` concatenates
`$this->front` unconditionally, with no `with_front` flag and no option lookup —
`author_base` is a `WP_Rewrite` property (default `author`), not a row in
`{prefix}options`, so there is no option whose presence could switch the front
off. The asymmetry exists only for the two taxonomies, whose bases *are*
options. grimoire therefore applies the front to author archives always, and
`AuthorBase` stays a constant.

**The `using_index_permalinks()` disjunct is deliberately not implemented.** It
restores the front for `/index.php/...`-style permalinks; grimoire has never
served index permalinks — M9a's token set and `ChiPatterns` have no such form —
so the condition reduces to `! get_option( '{taxonomy}_base' )`. Stated here
rather than omitted silently (Requirement 4.10).

#### `Parse` must record whether a base was *provided*, not just what it resolved to

WordPress's test is the **truthiness of the option value**, not the presence of
the row. So `category_base` set explicitly to the string `category` — the same
value as the default — is *set*, and drops the front. `routing.Parse` cannot
express that today:

```go
// today: the empty case and the explicit-default case collapse, and which one
// occurred is unrecoverable from the returned Structure.
s := Structure{
    CategoryBase: firstNonEmpty(categoryBase, DefaultCategoryBase),
    TagBase:      firstNonEmpty(tagBase, DefaultTagBase),
}
```

The fix needs **no signature change**, which is worth stating because the
alternative (an options struct, or `*string` parameters) would touch all
twenty-odd `routing.Parse` call sites across `cmd/`, `internal/web`,
`internal/content`, `internal/routing` and `test/e2e` for no behavioral gain.
`content.OptionService` already maps an absent option to the empty string — its
own doc comment says so, and `cmd/grimoire/permalinks.go` relies on it — so the
empty string arriving at `Parse` *is* WordPress's falsy `get_option()` result.
`Parse` keeps `(structure, categoryBase, tagBase string)` and records two
booleans alongside the resolved values:

```go
type Structure struct {
    // ... Raw, Flat, TrailingSlash, CategoryBase, TagBase unchanged ...

    // CategoryBaseSet and TagBaseSet report that the corresponding option
    // arrived non-empty, i.e. WordPress's get_option() would have been truthy.
    // They are NOT "differs from the default": an option explicitly set to
    // "category" is set, and WordPress drops the front for it, so a comparison
    // against DefaultCategoryBase would get that case wrong.
    //
    // These exist only to decide whether the front applies to the category and
    // tag archive paths (Req 4.8, 4.9). Nothing else reads them.
    CategoryBaseSet bool
    TagBaseSet      bool

    front []string // leading literal segments, derived during Parse
}
```

Every existing call site keeps compiling, and the many tests that pass `"", ""`
keep meaning "neither base configured" — which is what they mean today.

#### `Parse` must also normalize the base, with one emptiness test

`firstNonEmpty` (`internal/routing/routing.go:351`) trims whitespace and nothing
else:

```go
func firstNonEmpty(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}
```

That was inert while no route consumed a base. M9b makes the base the seat of
chi pattern registration, `Classify`'s segment comparison, path construction and
the REST `link` field, so two representable values now break all four:

- `/topics` — **realistic, not hypothetical.** WordPress's
  `options-permalink.php` prefixes the submitted base with `/` before
  `update_option`, so `get_option( 'category_base' )` plausibly returns `/topics`
  on any site where the base was set through the admin UI. Unnormalized, it emits
  `//topics/*` patterns and `Classify`'s first-segment comparison never matches.
- `topics/news` — WordPress's permalink settings page accepts a multi-segment
  base. Unnormalized, `CategoryPath` advertises `/topics/news/local` while
  `Classify` compares one segment and never matches, so every category archive
  `404`s against a link the REST API publishes.

So `Parse` normalizes each base — trim whitespace, trim leading and trailing
slashes, collapse repeated internal slashes (Req 4.11) — and a base that
normalizes to more than one segment gets a `Notes()` entry while still being used
consistently everywhere (Req 4.12). The normalized multi-segment value is
carried as segments, so the patterns, the classifier and the constructors all
agree by construction rather than by three implementations happening to match.

The emptiness test is **shared** (Req 4.13): one predicate, applied to the
normalized value, decides both whether the base falls back to its default and
whether `CategoryBaseSet`/`TagBaseSet` is true. With two tests a base of `" "`
resolves to `category` *and* counts as provided, which drops the front for a base
that was effectively unset — the one combination neither behavior intends.

The live reference database stores **both bases empty** (verified), and the
in-tree tests use `"sections"` (`cmd/grimoire/permalinks_test.go:162`), so
nothing in the tree or on the reference site exercises either shape today. That is
the argument for specifying it in Phase 1 rather than finding it in Phase 7:
normalization is a few lines here and a revisit of `Classify`, `ArchivePatterns`
and all four constructors there.

### Base collisions are a diagnostic, not an error

```go
// Notes carries non-fatal diagnostics about a parsed Structure: base-segment
// collisions (with each other, with "author", or with a leading literal of the
// permalink structure) and a base that normalizes to more than one segment.
// Empty when nothing is wrong.
//
// Deliberately NOT reported through Parse's error return. M9a gives that error
// exactly one meaning at every call site -- "unsupported structure, fall back to
// the flat route" -- and a site that renamed its category base has given no
// reason to stop serving permalinks. Reusing the error channel here would turn a
// cosmetic option clash into a site-wide permalink outage.
func (s Structure) Notes() []string
```

`cmd/grimoire/permalinks.go` logs each note at `WARN` next to the existing
resolved-structure `INFO`, and `cmd/grimoire-cli/permalinks.go` prints them in
the `-check` report. When `category_base` and `tag_base` collide, or either
collides with `author`, the colliding segment keeps its **category** meaning —
`Classify`'s precedence table decides that, and the note says so.

`migrate -check` therefore gains three things in this milestone, all read-only
and all reached only after `migrate.Preflight` reports a clean schema:

| Report | Source | Requirement |
|---|---|---|
| the resolved `category_base` and `tag_base` | `Structure.CategoryBase`/`TagBase` | 4.6 |
| base-segment collisions | `Structure.Notes()` | 4.6 |
| duplicate `user_nicename` values | `NicenameAuditor.DuplicateNicenames` | 7.8 |

The first two are pure — they add no query, since `reportPermalinks` already
reads the three permalink options. The third adds **one** aggregate query over
`{prefix}users`, which is why it belongs here and not on the request path (see
"Duplicate `user_nicename`"). It reports nothing when there are no duplicates,
so the common case is one extra silent query in a command an operator runs by
hand.

### Precedence table (`Classify`)

Evaluated top to bottom; the first match wins.

Each archive row carries its own **expected prefix**, because the front applies
per kind rather than globally (see "The front"). The row matches only if the path
starts with that prefix; the prefix is then consumed and the rest of the row's
condition is evaluated against the remaining segments. A single
strip-the-front-first step would be wrong: with front `blog` and `category_base`
set to `sections`, `/sections/news` is a category path and `/blog/sections/news`
is not, while `/blog/2024` *is* a date archive in the same structure.

| # | Expected prefix | Path shape (after the prefix) | Kind | Note |
|---|---|---|---|---|
| 1 | — | empty, or `/` | `KindNone` | the `/` route is registered earlier and owns it |
| 2 | front if `!CategoryBaseSet` | first segment == `CategoryBase`, ≥1 further segment | `KindCategory` | `Segments` = the rest |
| 3 | front if `!CategoryBaseSet` | first segment == `CategoryBase`, nothing further | `KindNone` | Req 2.8 — no category index |
| 4 | front if `!TagBaseSet` | first segment == `TagBase`, exactly 1 further segment | `KindTag` | more segments → `KindNone`; tags are flat |
| 5 | front (always) | first segment == `AuthorBase`, exactly 1 further segment | `KindAuthor` | |
| 6 | front (always) + `date/` when `%post_id%` is among the first three tokens | 1–3 segments, widths 4/2/2, all digits, forming a real date | `KindDate` | **before** posts, matching WordPress's rewrite-rule order |
| 7 | — | matches the structure's shape (`ParamsFromPath` + `Match`) | `KindPost` | unchanged M9a resolution; the structure already carries its own front |
| 8 | — | exactly 1 segment | `KindPost` with `Ref{Slug: seg}` | the flat route, which is what makes M9a's flat→canonical `301` reachable |
| 9 | — | anything else | `KindNone` | `404` |

Every prefix in the table is computed by the same unexported helper the
constructors use, so `Classify` and `CategoryPath`/`TagPath`/`AuthorPath`/
`DatePath` cannot disagree about whether a front applies — which is what makes
the round-trip property in Requirement 2.4 hold for every structure, not only
for front-less ones.

When the structure is `Flat` there is no front (a flat `Structure` has no
segments at all), so every prefix is empty and the table reduces to today's
behavior byte for byte.

### Date disambiguation: WordPress's `date/` segment

Row 6's prefix carries an extra `date/` segment when `%post_id%` appears among
the structure's **first three tokens** (Requirement 6.7). This is
`WP_Rewrite::get_date_permastruct()`'s rule verbatim: it walks the structure's
tokens with a 1-based index and, on finding `%post_id%` at index ≤ 3, sets
`$front = $front . 'date/'` before appending the date endian.

So a bare `/%post_id%/` structure serves date archives at `/date/2024`,
`/date/2024/05`, `/date/2024/05/17`, and `/2024` stays available as post 2024's
canonical permalink. A `/%postname%/` or
`/%year%/%monthnum%/%day%/%postname%/` structure gets no `date/` segment and
keeps serving date archives at their bare paths, exactly as WordPress does — the
common cases are untouched.

The token-index rule is copied rather than reasoned about because its edges are
WordPress's, not ours: `/%year%/%monthnum%/%post_id%/` has `%post_id%` at index 3
and *does* get the prefix, while `/%year%/%monthnum%/%day%/%post_id%/` has it at
index 4 and does not. That second case genuinely is ambiguous in WordPress too,
and diverging to "fix" it would break URL compatibility with the site grimoire is
reading. `DatePath` is the only place the rule is evaluated, and it is evaluated
once per `Structure`.

**The consequences of row 6 and rows 2–5 are real and must be documented, not
discovered:**

- Under a `/%postname%/` structure, a post whose slug is a bare 4-digit number
  is unreachable at `/2024` — the year archive wins. WordPress behaves the same
  way.
- Under `/%year%/%monthnum%/%postname%/`, a post slugged with a bare 2-digit
  number is unreachable at the day position. WordPress behaves the same way, for
  the same reason.
- Under a single-segment structure, a post slugged exactly `category`, `tag` or
  `author` (or the configured base) is unreachable. chi's static-over-parameter
  preference would produce this even without `Classify`; the table makes it
  explicit.
- Row 6 is why the `date/` segment above is not optional. Under a bare
  `/%post_id%/` structure, `/2024` is both a valid post id and a valid year, and
  row 6 without the prefix would make post 2024 unreachable **by its own
  canonical permalink**. WordPress's `date/` prefix exists precisely to prevent
  that, and grimoire adopts it (Req 6.7). Note that WordPress's own "Numeric"
  preset is `/archives/%post_id%`, whose literal front sidesteps the collision
  on its own — the bare form is a custom structure, not a preset — but the
  `date/` rule fires for both, because WordPress keys it on the token index
  rather than on whether a front happens to exist.
- A front does **not** create a new collision class. `/blog/2024` is a date
  archive and `/blog/%postname%/`'s post `2024` would live at `/blog/2024`
  too — the same shape clash row 6 already resolves in favour of the date, one
  segment deeper. It is listed here so the front is not mistaken for a way out
  of that consequence.

### Trailing slash

Archive paths follow the rule M9a set for posts: the canonical form is decided
by `Structure.TrailingSlash`, the other form `301`s to it, and when the structure
is `Flat` the canonical archive path carries **no** trailing slash — which
preserves today's `/category/{slug}` byte for byte.

Nested-vs-flat canonicalisation, by contrast, applies **regardless of
`permalink_structure`**, because it is a statement about taxonomy shape rather
than about permalink structure. A site on plain permalinks still gets
`/category/local` → `301` → `/category/news/local`.

## `internal/domain` additions

```go
// entities.go
type Term struct {
    ID       int64
    Name     string
    Slug     string
    Taxonomy string
    // ParentID backs term_taxonomy.parent; 0 means no parent, matching
    // WordPress's sentinel and the zero-value-means-unset convention already
    // used by Post.ParentID and MediaFilter.ParentID. Read-only in this
    // milestone: no write path sets it.
    ParentID int64
}

// repository.go -- one filter type covering all four archives, following
// AdminPostFilter/MediaFilter's established zero-value-means-unfiltered
// convention. post_status='publish' is NOT a field: it is applied
// unconditionally by every implementation, so no caller can turn it off.
type ArchiveFilter struct {
    // Taxonomy and TermIDs select by term. TermIDs is a set (a category plus
    // its descendants), so an empty TermIDs with a non-empty Taxonomy means
    // "no terms" and matches nothing -- it is NOT unfiltered.
    Taxonomy string
    TermIDs  []int64
    AuthorID int64 // 0 = unfiltered
    // Start and End are a half-open range on post_date: [Start, End). Zero
    // means unbounded on that side.
    //
    // They are deliberately NOT named After/Before. domain.MediaFilter, in this
    // same package, already has After/Before, and its Before is an inclusive
    // *day-end*: mediaWhere compares against formatTS(f.Before.AddDate(0, 0, 1))
    // (internal/storage/wprepo/media.go:79-83), so MediaFilter's convention is
    // [After, Before+1day). Two filter types in internal/domain with
    // identically-named date fields meaning opposite things is a trap for
    // whoever writes the third, so this one names its bounds for what they are.
    Start time.Time
    End   time.Time
    Types []string // empty defaults to {"post"}
}

// PostRepository gains:
PublishedArchive(ctx context.Context, f ArchiveFilter, limit, offset int) ([]Post, error)
CountPublishedArchive(ctx context.Context, f ArchiveFilter) (int, error)

// UserRepository gains ByNicename -- and only ByNicename. It has a request-path
// caller (the author archive route), so it belongs on the interface every
// request path already holds.

// ByNicename resolves a user by user_nicename, returning ErrNotFound when
// absent. When more than one row shares the nicename it resolves to the lowest
// ID -- see "Duplicate user_nicename" below. Used by the author archive route.
ByNicename(ctx context.Context, nicename string) (User, error)

// NicenameConflict is one duplicated user_nicename.
type NicenameConflict struct {
    Nicename string
    Count    int   // rows sharing it, always >= 2
    WinnerID int64 // the ID ByNicename resolves to
}

// NicenameAuditor is the operator-only read, declared as its own narrow
// interface rather than added to UserRepository -- following this package's
// established opt-in pattern (TermReader, PostCounter, MediaWriter are all
// declared alongside the wide interface they narrow).
//
// UserRepository is held by auth.Sessions, auth.ApplicationPasswords,
// content.UserService and the REST users path -- every request path. Putting an
// operator-only diagnostic there would force both existing fakes
// (internal/auth/session_test.go:30, internal/content/userservice_test.go:22) to
// grow a method neither ever calls, to satisfy an interface neither needs it
// from. The only consumer is "grimoire-cli migrate -check", so the only thing
// that needs to name this method is the interface that command depends on.
type NicenameAuditor interface {
    // DuplicateNicenames reports every user_nicename shared by more than one
    // row. Read-only, one aggregate query, called once by
    // "grimoire-cli migrate -check" and by nothing on the request path.
    DuplicateNicenames(ctx context.Context) ([]NicenameConflict, error)
}
```

`*wprepo.UserRepo` satisfies both `UserRepository` and `NicenameAuditor`, so the
split costs no new concrete type — it only narrows what each caller has to know.

Two methods rather than six (`ByTag`/`CountByTag`/`ByAuthor`/`CountByAuthor`/
`ByDate`/`CountByDate`): the three archive kinds differ only in which predicate
they add, the shared parts are the publish filter, the type filter, the ordering
and the limit/offset, and the category case needs a term **set** rather than a
slug anyway. The existing `ByTermSlug`/`CountPublishedByTermSlug` pair is left in
place untouched — it is the single-slug, non-descendant read, and this milestone
adds a superset rather than rewriting a tested method.

## `internal/storage/wprepo`

### Populating `ParentID`

`termRow` gains `ParentID int64 \`bun:"parent"\`` and all three term reads select
`tt.parent`: `TermRepo.BySlug`, `TermRepo.ListByTaxonomy` and
`TermRepo.TermsByIDs`. All three already join `term_taxonomy`, so this is one
extra column per query and no new join.

### The archive query uses a semi-join, not `DISTINCT`

```sql
SELECT <postColumns> FROM {prefix}posts p
WHERE p.post_status = 'publish'
  AND p.post_type IN (...)
  [AND EXISTS (SELECT 1 FROM {prefix}term_relationships tr
                 JOIN {prefix}term_taxonomy tt ON tt.term_taxonomy_id = tr.term_taxonomy_id
                WHERE tr.object_id = p."ID" AND tt.taxonomy = ? AND tt.term_id IN (...))]
  [AND p.post_author = ?]
  [AND p.post_date >= ?/* Start */ AND p.post_date < ?/* End */]
ORDER BY p.post_date DESC, p."ID" DESC
LIMIT ? OFFSET ?
```

The `ID` column is **quoted**, and must be built with `bun.Ident("ID")` rather
than interpolated as bare `p.ID`: Postgres declares the column as `"ID"`
(`internal/storage/migrations/postgres/0001_init.up.sql:6`), so an unquoted
`p.ID` folds to `p.id` and errors at runtime on that vendor only. Every existing
read in `internal/storage/wprepo` already goes through `bun.Ident("ID")` (29 call
sites). This is a live trap, not a theoretical one — the repo has already shipped
two Postgres identifier-quoting defects
([#40](https://github.com/roboweaver/grimoire/issues/40),
[#43](https://github.com/roboweaver/grimoire/issues/43)).

`EXISTS` is a semi-join, so it yields one row per post **by construction**. That
matters more than it looks: the existing `ByTermSlug` joins the term tables
directly and is correct only because it filters on a single `t.slug`, which can
match at most one `term_taxonomy` row per post. Once the predicate becomes
`term_id IN (parent, child, grandchild)`, a post filed under both a parent and
its child matches twice — and a join would list it twice and, worse, count it
twice, breaking the pagination totals M8 established. `EXISTS` avoids needing
`DISTINCT` over a `LONGTEXT` `post_content` column, which is the alternative and
is both slower and differently supported across the three vendors.

`CountPublishedArchive` is the identical predicate under `COUNT(*)`, so the count
and the listing cannot describe different sets.

The date bound is a half-open range `[Start, End)` on `post_date` formatted by
the existing `formatTS` helper — the same *formatting* route `MediaFilter` takes
in `internal/storage/wprepo/media.go`, though not the same *semantics* (see
`ArchiveFilter`'s field comment: `MediaFilter.Before` is an inclusive day-end).
No `YEAR()`/`EXTRACT`/`strftime`, so one statement serves MySQL, Postgres and
SQLite (where `DATETIME` is ISO-8601 text and orders lexicographically).

Guard: a filter with a non-empty `Taxonomy` and an empty `TermIDs` returns an
empty slice and `0` without issuing a query, because `IN ()` is not valid SQL on
any of the three vendors and "no terms" must not degrade to "all posts".

### Duplicate `user_nicename`

```sql
-- ByNicename
SELECT <userColumns> FROM {prefix}users
WHERE user_nicename = ? ORDER BY ID ASC LIMIT 1

-- DuplicateNicenames (migrate -check only)
SELECT user_nicename, COUNT(*) AS n, MIN(ID) AS winner
FROM {prefix}users GROUP BY user_nicename HAVING COUNT(*) > 1
ORDER BY user_nicename
```

**The schema permits duplicates, so the read has to decide something.** The
facts, in the order they matter:

- WordPress prevents duplicates **on write**. `wp_insert_user()` appends numeric
  suffixes — `alice`, then `alice-2` — so a nicename collision cannot be created
  through the normal user-creation path. (Trac #44921; changeset 34218.)
- But `user_nicename` carries a **non-unique** index. Verified directly against
  the live WordPress database this project develops against: `Non_unique = 1`.
  Duplicates are permitted by the schema, and arise from every path that
  bypasses `wp_insert_user`: direct SQL, imports and migrations, multisite
  merges, pre-suffix-era WordPress, and plugins.
- **On read WordPress is arbitrary.** `WP_User::get_data_by( 'slug', ... )`
  issues `SELECT * FROM $wpdb->users WHERE user_nicename = %s LIMIT 1` with **no
  `ORDER BY`**, behind an object cache (`wp_cache_get( $value, 'userslugs' )`).
  So the winner can look stable for months and then flip after a cache flush,
  with nothing in the database having changed.

grimoire **deliberately diverges**: `ORDER BY ID ASC LIMIT 1`. Three reasons,
none of them "WordPress is wrong":

1. WordPress's nondeterminism is not reproducible anyway. grimoire has no shared
   object cache, so there is no behavior to faithfully copy — copying the missing
   `ORDER BY` would reproduce the *absence* of a rule, not the rule.
2. grimoire runs on three vendors. Unordered row order diverges far more between
   SQLite, MySQL and Postgres than it ever does inside WordPress's MySQL-only
   world, so "whatever the engine returns" is a much weaker guarantee here than
   there.
3. Requirement 12.3's cross-vendor contract test needs a stable assertion. A
   contract test that cannot state which row wins cannot test this at all.

**The impact is narrow, and identical to WordPress's either way:** one
`/author/{nicename}` URL can only ever surface one of the colliding authors, so
the other's posts are unreachable by that route. No error, no wrong data — one
author invisible at that URL. Recorded in `docs/compatibility.md` (Req 13.5).

`migrate -check` reports the condition (Req 7.8) so it is discoverable before
the site serves, the same posture M9a took for unsupported permalink structures:
`-check` is the surface an operator runs first, and the alternative is an author
who is silently missing from a site nobody knew to look at.

**No per-request check.** The author archive handler does not count matching
rows. That would add a `COUNT` to every author archive hit to detect a condition
that is nearly always absent — a permanent cost for a one-off diagnostic — and
the handler has nothing useful to do with the answer anyway: it must still serve
one of the two authors. `DuplicateNicenames` is called once, from `-check`, and
is reachable from no request path. That trade-off is the reason the reporting
lives where it does — and the reason the method is declared on the narrow
`domain.NicenameAuditor` rather than on `UserRepository`.

## Hierarchy resolution

Both directions come from **one** read. Once `ParentID` is populated,
`TermReader.ListByTaxonomy(ctx, "category")` returns the complete
`(id, parent, slug, name)` graph for the taxonomy, from which:

- **Ancestry** (for the canonical path) is a walk from the term up through
  `ParentID` until `0`, then reversed. A `ParentID` pointing at a term not in the
  set terminates the walk — imported databases contain orphans, and a shorter
  path is a better outcome than a `500` (Req 1.4). A visited set terminates a
  cycle (Req 1.5).
- **Descendants** (for the listing and the count) are a breadth-first expansion
  down a child index built from the same slice, with the same visited set.

One query per category-archive request, returning one row per category in the
taxonomy — 33 rows on the reference database. No cache: unlike M9a's permalink
options, terms are mutable at runtime through M6's `TermWriteService`, so a cache
would need an invalidation story this milestone does not need to own. That
trade-off is stated in Requirement 1.6 rather than left implicit.

## `internal/content`

```go
// archive.go -- the shape every archive read returns.
type Archive struct {
    Heading  string       // term name, author display_name, or the formatted date
    Term     domain.Term  // zero value for date and author archives
    Ancestry []string     // root-first category slugs incl. its own; nil otherwise
    Posts    []domain.Post
    Page     Page
}

// TermService gains an opt-in hierarchy dependency, following
// PostService.WithCounter's established pattern rather than changing a
// constructor signature.
func (s *TermService) WithHierarchy(r domain.TermReader) *TermService
func (s *TermService) CategoryArchive(ctx context.Context, segments []string, page, perPage int) (Archive, error)
func (s *TermService) TagArchive(ctx context.Context, slug string, page, perPage int) (Archive, error)

// PostService gains the two non-taxonomy archives. Author resolution needs a
// user read, opted into the same way.
func (s *PostService) WithAuthors(u domain.UserRepository) *PostService
func (s *PostService) AuthorArchive(ctx context.Context, nicename string, page, perPage int) (Archive, error)
func (s *PostService) DateArchive(ctx context.Context, d routing.DateRef, page, perPage int) (Archive, error)
```

Ownership split deliberately, not incidentally: category and tag archives are
taxonomy reads and belong with the service that already resolves terms; date and
author archives are post listings with an extra predicate and belong with the
service that already owns `RecentPage`. The alternative — a fourth service
owning all four — was considered and rejected because it would need
`TermRepository`, `TermReader`, `PostRepository` and `UserRepository` and would
leave `TermService` holding a single method.

`TermService.CategoryPage` is superseded by `CategoryArchive` and removed along
with the already-unused `TermService.Category`; `web.category` is its only
production caller. Every archive read reuses `clamp`/`newPage` from
`pagination.go`, so there is one pagination shape (Req 8.1).

`Archive.Heading` for a date archive is formatted in `content`, not in the
template: `2024`, `May 2024`, `17 May 2024` by granularity.

## `internal/web`

- **`router.go`** — the hard-coded `/category/{slug}` registration is replaced by
  `Structure.ArchivePatterns()`, and every pattern from `ChiPatterns()`,
  `ArchivePatterns()` and the flat `/{slug}` is registered to one handler,
  `s.resolve`. The existing long comment block explaining chi's collision
  behavior is updated rather than deleted: it is still the reason the design
  looks like this, and it now has more cases to name.
- **`handlers.go`** — new `resolve` dispatcher:

  ```go
  func (s *Server) resolve(w http.ResponseWriter, r *http.Request) error {
      switch t := s.permalinks.Classify(r.URL.Path); t.Kind {
      case routing.KindPost:     return s.single(w, r, t)
      case routing.KindCategory: return s.categoryArchive(w, r, t)
      case routing.KindTag:      return s.tagArchive(w, r, t)
      case routing.KindAuthor:   return s.authorArchive(w, r, t)
      case routing.KindDate:     return s.dateArchive(w, r, t)
      default:                   return domain.ErrNotFound
      }
  }
  ```

  `single` keeps its M9a behavior verbatim; it now receives the already-classified
  `Target` instead of re-deriving one, so `refFrom` folds into `Classify` and the
  two cannot disagree about what a path means.

  Each archive handler follows the same four steps, which is what makes the
  status-code table uniform: resolve the entity (`404` if absent) → compute the
  canonical path from `Structure` → `301` if the request path differs, preserving
  the query string → read the page and `404` if out of range → render.
- **`view.go`/`rest_terms.go`** — `restTermLink` becomes a method on `*Server` so
  it can reach the structure and, for a nested category, the ancestry. The term
  graph is resolved once per REST request and reused across every term in the
  response, so a `/wp-json/wp/v2/categories` listing does not issue one graph
  read per row. `restTerm.Parent` carries `Term.ParentID`.

## Status codes

| Condition | Status |
|---|---|
| Category path equals the canonical nested path | `200` |
| Category path resolves but ancestry differs (incl. the flat path) | `301` + `Location` |
| Category path resolves, wrong trailing-slash form | `301` + `Location` |
| Final category segment resolves to no term | `404` |
| Category exists, zero published posts | `200`, empty archive |
| Bare base segment (`/category`, `/category/`) | `404` |
| Tag/author slug resolves, canonical form | `200` |
| Tag/author slug resolves, wrong trailing-slash form | `301` + `Location` |
| Tag/author slug resolves to nothing | `404` |
| Tag/author exists, zero published posts | `200`, empty archive |
| Date path is a real date, canonical form | `200` (empty archive if nothing matched) |
| Date path is not a real calendar date | `404`, no query issued |
| Any archive, `?page=` past the last page while `Total > 0` | `404` |
| Everything else in the M9a table | unchanged |

`301` rather than `302` throughout, and the query string is preserved, matching
M9a and WordPress's `redirect_canonical`.

## `internal/render` and the default theme

```go
// ArchiveData backs the category, tag, author and date templates.
type ArchiveData struct {
    SiteTitle  string
    Tagline    string
    Kind       string // "category" | "tag" | "author" | "date"
    Heading    string
    BaseURL    string // canonical archive path; pagination links are built from it
    Term       TermView
    Posts      []PostView
    Pagination content.Page
}

// CategoryData is retained as an alias so existing themes and call sites keep
// working (Req 11.2).
type CategoryData = ArchiveData
```

The alias is load-bearing rather than cosmetic: `html/template` resolves field
names at **execution** time, so renaming or dropping a field that
`category.tmpl` references would turn a working page into a `500` that no
compile step catches. Keeping `Term` and adding fields is additive for every
template.

`themes/default/templates/category.tmpl` currently builds its pagination links
as `/category/{{.Term.Slug}}?page=…`, which is wrong for a nested category and
wrong for an overridden `category_base`; it switches to `.BaseURL`.
`archive.tmpl` — today a literal "Archive" heading, a post grid and no
pagination at all — gains `.Heading` and the same pagination block, because
`tag`, `author` and `date` all fall back to it and none of the three has its own
template in the default theme.

The `hierarchy` map in `internal/render/engine.go` is **not** touched: M9a
registered `tag`, `author` and `date` (each `{kind}` → `archive` → `index`)
ahead of the routes that need them, which is exactly the plumbing this milestone
expected to find already done.

## Migrations

**None.** `term_taxonomy.parent` exists in every vendor's
`internal/storage/migrations/<vendor>/0001_init.up.sql` (`parent INTEGER NOT
NULL DEFAULT 0` on SQLite, with the MySQL/Postgres equivalents) and is populated
by WordPress itself in any real database. This milestone reads one additional
column and adds no column, table or index. The task list verifies no file
appears under any `migrations` directory, mirroring M8's and M9a's checks.

## Security considerations

- **Open redirect.** Every `Location` is produced by a `Structure` path
  constructor from database-resolved rows — a term's ancestry, a user's
  nicename, a validated date — never by echoing a request path. A visitor cannot
  steer a redirect target.
- **Redirect loops.** Each archive kind's canonical path is a pure function of
  the resolved entity, so `Path(entity)` is a fixed point and a request already
  at it renders. The nested-category case is the one worth testing explicitly
  (Req 2.4), because the redirect target is derived through a graph walk rather
  than a direct field read, and a walk that returned a different ancestry on the
  second pass would loop on every category URL.
- **Disclosure.** Archive reads filter `post_status='publish'`
  unconditionally — it is not a field on `ArchiveFilter`, so no call site can
  disable it. The author archive renders `display_name`, never `user_login`, so
  it does not publish a login name the site had not already exposed.
- **Enumeration.** `/author/{nicename}` lets a visitor enumerate nicenames, but
  a nicename is already public in any WordPress site's own author links and the
  archive lists only published posts. Unchanged from WordPress.
- **Unbounded traversal.** The ancestry and descendant walks are bounded by a
  visited set over a finite in-memory slice, so a corrupted `parent` cycle
  terminates rather than hanging a request or exhausting the stack.
- **Query fan-out.** `TermIDs` comes from the term graph, not from the URL, so a
  visitor cannot force a large `IN` list by crafting a path.
- **`DuplicateNicenames` is operator-only.** It is called from
  `grimoire-cli migrate -check`, writes to that command's stdout, and is
  reachable from no HTTP route. It reports nicenames and row counts — never
  `user_login`, `user_email` or `user_pass` — so the report exposes nothing a
  WordPress site's own author links do not.

## SEO considerations

The same posture as M9a, extended to archives: exactly one path per archive
returns `200`, every other recognised form `301`s to it, and the theme's own
pagination links point at the canonical path rather than at a form that
redirects. Nested paths being canonical means grimoire advertises the same
category URLs the WordPress site published, which is the whole point of the
inherited decision.

## Testing strategy

1. **Pure unit tests, `internal/routing`** — the bulk of it, no database.
   Table-driven over every row of the precedence table, each ambiguous shape
   named in Requirement 9, `DateRef.Range` including `2024/02/30` and
   `2024/13/01`, the four path constructors, both trailing-slash forms, and the
   canonical fixed-point property for a nested ancestry. The front's asymmetry
   gets its own table (Req 12.9): a structure with a front produces
   front-prefixed date and author paths; the same structure produces a
   front-prefixed category path with `category_base` empty and an unprefixed one
   with `category_base` **explicitly set to `category`** — that last case is the
   one an implementation comparing against `DefaultCategoryBase` gets wrong, so
   it is the case that has to be asserted. The `date/` prefix gets a table over
   token positions 1–4, since WordPress's rule is an index comparison rather
   than a "does a front exist" test.
2. **Cross-vendor contract tests, `internal/storage/storagetest`** —
   `ParentID` on all three term reads; `PublishedArchive`/
   `CountPublishedArchive` for the term-set, author and date-range predicates;
   the duplicate-assignment case; the unpublished-exclusion case;
   `UserRepository.ByNicename`, including two users sharing a nicename so the
   lowest-`ID` rule is asserted on all three vendors rather than on whichever one
   happened to return it first; and `DuplicateNicenames` reporting that pair.
   `SeedFixtures` currently inserts every `term_taxonomy` row with `parent` `0`,
   so it gains a parent/child/grandchild chain — without which a `ParentID`
   assertion would pass against a repository that hard-coded `0`.
3. **Service tests, `internal/content`** — ancestry and descendant resolution
   against a fake `TermReader`, including the orphaned-parent and cycle cases,
   which are cheap to construct with a fake and awkward to seed in SQL.
4. **Handler tests, `internal/web`** — exact status and `Location` per row of the
   status-code table, plus an explicit assertion that `/`, `/login`,
   `/wp-content/uploads/*`, `/admin/api/...`, `/wp-json/...` and the M9a
   permalink/flat routes still behave identically (Req 9.6).
5. **e2e, `test/e2e`** — one test booting the stack with a nested category and a
   non-default `category_base`, asserting the nested path renders, the flat path
   `301`s, and a tag/date/author archive each render.
6. **Real WordPress database, env-gated** — extends
   `internal/routing/realdb_test.go` and
   `test/e2e/m9_permalinks_realdb_test.go` rather than adding a third mechanism,
   so one `GRIMOIRE_TEST_WP_DSN` still enables every real-database check. Reads
   the site's own `category_base` and hierarchy, derives expected paths
   independently of the constructor under test (as M9a's checks do, so an
   implementation cannot merely agree with itself), and **skips** with a
   precondition message when the target has no nested category rather than
   reporting a success it never checked.

## Traceability

| Requirement | Components |
|---|---|
| 1 — read hierarchy | `domain.Term.ParentID`, `wprepo.termRow`, `TermRepo.BySlug`/`ListByTaxonomy`/`TermsByIDs`, `content` ancestry/descendant walks |
| 2 — nested category routes | `routing.Structure.CategoryPath`, `Classify`, `web.categoryArchive`, `router.go` |
| 3 — descendant inclusion | `domain.ArchiveFilter`, `PostRepository.PublishedArchive`/`CountPublishedArchive`, `content.TermService.CategoryArchive` |
| 4 — bases take effect | `routing.Structure.CategoryBase`/`TagBase`/`AuthorBase`, `Structure.CategoryBaseSet`/`TagBaseSet`, `Structure.front`, the four archive path constructors, `Classify`'s per-row prefix, `ArchivePatterns`, `Structure.Notes`, `cmd/grimoire/permalinks.go`, `cmd/grimoire-cli/permalinks.go` |
| 5 — tag archives | `routing.Structure.TagPath`, `content.TermService.TagArchive`, `web.tagArchive` |
| 6 — date archives | `routing.DateRef`, `DatePath` (incl. the `date/` disambiguation prefix), `content.PostService.DateArchive`, `web.dateArchive` |
| 7 — author archives | `routing.Structure.AuthorPath`, `domain.UserRepository.ByNicename` (`ORDER BY ID ASC LIMIT 1`), `domain.NicenameConflict`/`domain.NicenameAuditor.DuplicateNicenames`, `content.PostService.AuthorArchive`, `web.authorArchive`, `content.rest.go` `userLink`, `cmd/grimoire-cli/permalinks.go` |
| 8 — pagination | `content.Page`, `clamp`/`newPage`, `web.pageParam`, `ArchiveData.BaseURL`, `category.tmpl`/`archive.tmpl` |
| 9 — precedence | `routing.Structure.Classify` + the precedence table, `web.resolve`, `router.go` |
| 10 — REST parity | `web.rest_terms.go` `restTermLink`/`termToREST`, `content.rest.go` `userLink` |
| 11 — theme data | `render.ArchiveData` (+ `CategoryData` alias), `themes/default/templates/{category,archive}.tmpl` |
| 12 — coverage | `internal/routing` unit tests, `storagetest` contract + `SeedFixtures`, `internal/content` service tests, `internal/web` handler tests, `test/e2e`, the two env-gated real-DB checks |
| 13 — docs | `docs/compatibility.md` (incl. the duplicate-`user_nicename` divergence), root `README.md`, `cmd/grimoire-cli/permalinks.go`, `../README.md`, `../wordpress-core-parity-roadmap/tasks.md` |
