package web_test

import (
	"context"
	"database/sql"
	"net/http"
	"testing"

	"github.com/roboweaver/grimoire/internal/storage/storagetest"
)

// The archive fixtures these tests rely on, layered on storagetest.SeedFixtures
// by seedArchives below. Paths are named as constants rather than spelled inline
// per case so a wrong Location and a wrong expectation cannot look alike.
//
//	categories (term id, parent)
//	  news       10, 0        -> hello-2, hello-3 published (+ 1 draft)
//	  zeta       11, 0        -> hello-1
//	  alpha      12, 0        -> hello-1
//	  tech       50, 0        -> tech-post, tech-page (a page), dual-post
//	   └─ go     51, 50       -> go-post, dual-post, hidden-go-post (a draft)
//	   │   └─ generics 52, 51 -> generics-post
//	   └─ idle   53, 50       -> nothing, at any status
//
//	tags        golang (post 1), unused (nothing)
//	authors     admin (7 published posts), archivist (1), ghost (none)
//
//	post dates  hello-1..3 2024-01-01..03, tech/go/generics-post 2024-05-15..17,
//	            dual-post 2024-05-18, archivist-post 2024-05-21
//
// Under structDayAndName the structure's trailing slash is canonical for
// archives too (Req 2.7), so every canonical path below carries one; the flat
// structure's canonical forms are asserted separately.
const (
	catNewsCanonical     = "/category/news/"
	catTechCanonical     = "/category/tech/"
	catGoCanonical       = "/category/tech/go/"
	catGenericsCanonical = "/category/tech/go/generics/"
	catIdleCanonical     = "/category/tech/idle/"

	tagGolangCanonical = "/tag/golang/"
	tagUnusedCanonical = "/tag/unused/"

	authorAdminCanonical = "/author/admin/"
	authorGhostCanonical = "/author/ghost/"
)

// newArchiveServer builds a test server carrying the nested category chain, the
// archive extras and the empty-archive targets, so every row of design.md's
// status-code table has a fixture that can distinguish it from its neighbours.
func newArchiveServer(t *testing.T, structure string) http.Handler {
	t.Helper()
	return newTestServerSeeded(t, structure, seedArchives)
}

func seedArchives(ctx context.Context, db *sql.DB, vendor, prefix string) error {
	if err := storagetest.SeedArchiveFixtures(ctx, db, vendor, prefix); err != nil {
		return err
	}
	return storagetest.SeedEmptyArchiveTargets(ctx, db, vendor, prefix)
}

// archiveCase is one row of the status-code table: a request path, the exact
// status it must produce and, for a redirect, the exact Location. An empty
// wantLocation asserts the absence of the header, so a 200 that also redirects
// cannot pass.
type archiveCase struct {
	name         string
	path         string
	wantStatus   int
	wantLocation string
}

func runArchiveCases(t *testing.T, srv http.Handler, cases []archiveCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := get(t, srv, tc.path)
			if rec.Code != tc.wantStatus {
				t.Fatalf("GET %s = %d, want %d (body: %.200s)",
					tc.path, rec.Code, tc.wantStatus, rec.Body.String())
			}
			if got := rec.Header().Get("Location"); got != tc.wantLocation {
				t.Errorf("GET %s Location = %q, want %q", tc.path, got, tc.wantLocation)
			}
		})
	}
}

// TestCategoryArchiveStatusCodes covers the category rows of design.md's
// status-code table against a trailing-slash structure (Req 2.3-2.8).
func TestCategoryArchiveStatusCodes(t *testing.T) {
	srv := newArchiveServer(t, structDayAndName)
	runArchiveCases(t, srv, []archiveCase{
		{
			// Req 2.4: a top-level category's canonical path is the flat path,
			// so this renders rather than redirecting to itself.
			name:       "top-level category renders at its flat path",
			path:       catNewsCanonical,
			wantStatus: http.StatusOK,
		},
		{
			name:       "nested category renders at its canonical path",
			path:       catGoCanonical,
			wantStatus: http.StatusOK,
		},
		{
			// Three levels, because a walk that handles one hop and stops would
			// pass the two-level case.
			name:       "grandchild category renders at its canonical path",
			path:       catGenericsCanonical,
			wantStatus: http.StatusOK,
		},
		{
			// Req 2.3: the flat path is the zero-ancestor instance of a failed
			// walk, and it is the URL the site published before this milestone.
			name:         "flat path redirects to the nested canonical path",
			path:         "/category/go/",
			wantStatus:   http.StatusMovedPermanently,
			wantLocation: catGoCanonical,
		},
		{
			name:         "flat path of a grandchild redirects to its full ancestry",
			path:         "/category/generics/",
			wantStatus:   http.StatusMovedPermanently,
			wantLocation: catGenericsCanonical,
		},
		{
			// Req 2.3/2.11: "go" is not a child of "news", so the walk fails and
			// recovery on the final segment supplies the canonical target.
			name:         "wrong-ancestor path redirects to the canonical path",
			path:         "/category/news/go/",
			wantStatus:   http.StatusMovedPermanently,
			wantLocation: catGoCanonical,
		},
		{
			// A skipped level is the same failure: generics is tech's
			// grandchild, not its child.
			name:         "path skipping a level redirects to the canonical path",
			path:         "/category/tech/generics/",
			wantStatus:   http.StatusMovedPermanently,
			wantLocation: catGenericsCanonical,
		},
		{
			// Req 2.7: the structure ends in "/", so the slashless form is not
			// canonical and must not serve the same archive at a second URL.
			name:         "nested path missing the trailing slash redirects",
			path:         "/category/tech/go",
			wantStatus:   http.StatusMovedPermanently,
			wantLocation: catGoCanonical,
		},
		{
			name:         "top-level path missing the trailing slash redirects",
			path:         "/category/news",
			wantStatus:   http.StatusMovedPermanently,
			wantLocation: catNewsCanonical,
		},
		{
			// Req 2.5: the final segment names no category anywhere in the
			// taxonomy, so neither the walk nor the recovery can resolve it.
			name:       "unknown final segment 404s",
			path:       "/category/nope/",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "unknown final segment under a valid ancestor 404s",
			path:       "/category/tech/nope/",
			wantStatus: http.StatusNotFound,
		},
		{
			// Req 2.6: the term resolved, so nothing is absent — an empty
			// archive is a 200 rather than a 404.
			name:       "existing category with no posts renders empty",
			path:       catIdleCanonical,
			wantStatus: http.StatusOK,
		},
		{
			// Req 2.8: WordPress serves no category index, in either slash form.
			name:       "bare base segment 404s",
			path:       "/category",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "bare base segment with a trailing slash 404s",
			path:       "/category/",
			wantStatus: http.StatusNotFound,
		},
	})
}

// TestArchiveStatusCodesFlatStructure pins Req 2.7's second half: with
// permalink_structure empty the canonical archive path carries **no** trailing
// slash, so today's /category/{slug} keeps working byte for byte and the
// slash-carrying form is the one that redirects.
func TestArchiveStatusCodesFlatStructure(t *testing.T) {
	srv := newArchiveServer(t, "")
	runArchiveCases(t, srv, []archiveCase{
		{
			name:       "top-level category renders without a trailing slash",
			path:       "/category/news",
			wantStatus: http.StatusOK,
		},
		{
			name:       "nested category renders without a trailing slash",
			path:       "/category/tech/go",
			wantStatus: http.StatusOK,
		},
		{
			name:         "trailing-slash form redirects to the slashless canonical path",
			path:         "/category/tech/go/",
			wantStatus:   http.StatusMovedPermanently,
			wantLocation: "/category/tech/go",
		},
		{
			// Req 2.7's last sentence: the flat-to-nested redirect is a
			// statement about taxonomy shape, not about permalink structure, so
			// it still applies on a plain-permalink site.
			name:         "flat path redirects to the nested path under a flat structure",
			path:         "/category/go",
			wantStatus:   http.StatusMovedPermanently,
			wantLocation: "/category/tech/go",
		},
		{
			name:       "tag renders without a trailing slash",
			path:       "/tag/golang",
			wantStatus: http.StatusOK,
		},
		{
			name:         "tag trailing-slash form redirects",
			path:         "/tag/golang/",
			wantStatus:   http.StatusMovedPermanently,
			wantLocation: "/tag/golang",
		},
		{
			name:       "author renders without a trailing slash",
			path:       "/author/admin",
			wantStatus: http.StatusOK,
		},
		{
			name:         "author trailing-slash form redirects",
			path:         "/author/admin/",
			wantStatus:   http.StatusMovedPermanently,
			wantLocation: "/author/admin",
		},
	})
}

// TestTagAndAuthorArchiveStatusCodes covers the tag and author rows of the
// status-code table (Req 5.1, 5.3, 5.4, 7.1, 7.3). The two kinds share a table
// because the table shares their rows: both resolve a single slug, so their
// status codes are the same four cases.
func TestTagAndAuthorArchiveStatusCodes(t *testing.T) {
	srv := newArchiveServer(t, structDayAndName)
	runArchiveCases(t, srv, []archiveCase{
		{
			name:       "tag renders at its canonical path",
			path:       tagGolangCanonical,
			wantStatus: http.StatusOK,
		},
		{
			name:         "tag missing the trailing slash redirects",
			path:         "/tag/golang",
			wantStatus:   http.StatusMovedPermanently,
			wantLocation: tagGolangCanonical,
		},
		{
			// Req 5.4: the term resolved, so the archive is empty rather than
			// absent.
			name:       "existing tag with no posts renders empty",
			path:       tagUnusedCanonical,
			wantStatus: http.StatusOK,
		},
		{
			// Req 5.3.
			name:       "unknown tag 404s",
			path:       "/tag/nope/",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "author renders at its canonical path",
			path:       authorAdminCanonical,
			wantStatus: http.StatusOK,
		},
		{
			name:         "author missing the trailing slash redirects",
			path:         "/author/admin",
			wantStatus:   http.StatusMovedPermanently,
			wantLocation: authorAdminCanonical,
		},
		{
			name:       "second author renders at its own path",
			path:       "/author/archivist/",
			wantStatus: http.StatusOK,
		},
		{
			// Req 7.3's second clause: a user who published nothing is an empty
			// archive, not a missing one.
			name:       "existing author with no posts renders empty",
			path:       authorGhostCanonical,
			wantStatus: http.StatusOK,
		},
		{
			// Req 7.3's first clause.
			name:       "unknown nicename 404s",
			path:       "/author/nobody/",
			wantStatus: http.StatusNotFound,
		},
	})
}

// TestDateArchiveStatusCodes covers the date rows of the status-code table at
// all three granularities (Req 6.1, 6.3, 6.5).
func TestDateArchiveStatusCodes(t *testing.T) {
	srv := newArchiveServer(t, structDayAndName)
	runArchiveCases(t, srv, []archiveCase{
		{
			name:       "year archive renders",
			path:       "/2024/",
			wantStatus: http.StatusOK,
		},
		{
			name:       "year and month archive renders",
			path:       "/2024/05/",
			wantStatus: http.StatusOK,
		},
		{
			name:       "year, month and day archive renders",
			path:       "/2024/05/17/",
			wantStatus: http.StatusOK,
		},
		{
			// Req 6.5: there is no entity to be absent, so a real date matching
			// nothing is an empty archive rather than a 404.
			name:       "real date matching no posts renders empty",
			path:       "/2023/",
			wantStatus: http.StatusOK,
		},
		{
			name:         "year archive missing the trailing slash redirects",
			path:         "/2024",
			wantStatus:   http.StatusMovedPermanently,
			wantLocation: "/2024/",
		},
		{
			// Req 6.3: February has no 30th, so this is not a date archive at
			// all and no query is issued for it.
			name:       "impossible day 404s",
			path:       "/2024/02/30/",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "impossible month 404s",
			path:       "/2024/13/01/",
			wantStatus: http.StatusNotFound,
		},
		{
			// A non-leap year's 29 February, which a month-length table alone
			// would accept.
			name:       "non-leap 29 February 404s",
			path:       "/2023/02/29/",
			wantStatus: http.StatusNotFound,
		},
	})
}

// TestArchiveOutOfRangePageReturns404 covers Req 8.3 on every archive kind: a
// page past the last one, while Total > 0, is a 404 — the same rule the home and
// category routes already apply. Page 1 of each archive is asserted alongside it
// so a 404 caused by an unreachable route cannot be mistaken for the guard
// firing.
func TestArchiveOutOfRangePageReturns404(t *testing.T) {
	srv := newArchiveServer(t, structDayAndName)
	for _, tc := range []struct {
		name      string
		canonical string
	}{
		{"category", catGoCanonical},
		{"nested category with descendants", catTechCanonical},
		{"tag", tagGolangCanonical},
		{"author", authorAdminCanonical},
		{"date", "/2024/"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if rec := get(t, srv, tc.canonical); rec.Code != http.StatusOK {
				t.Fatalf("GET %s = %d, want 200 (body: %.200s)",
					tc.canonical, rec.Code, rec.Body.String())
			}
			path := tc.canonical + "?page=999"
			if rec := get(t, srv, path); rec.Code != http.StatusNotFound {
				t.Errorf("GET %s = %d, want 404", path, rec.Code)
			}
		})
	}
}
