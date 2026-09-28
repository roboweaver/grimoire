package routing_test

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/routing"
	"github.com/roboweaver/grimoire/internal/storage"
)

// TestRealWordPressPermalinks validates the permalink resolver against a
// restored, REAL WordPress database (read-only), mirroring the gating of
// internal/storage/storagetest's TestRealWordPressDB so CI without the database
// still passes.
//
//	GRIMOIRE_TEST_WP_DSN='wordpress:PASS@tcp(127.0.0.1:3306)/wordpress?parseTime=true' \
//	GRIMOIRE_TEST_WP_PREFIX=accuweaver \
//	go test ./internal/routing/ -run TestRealWordPressPermalinks -v -count=1
//
// Why this exists as a committed test rather than a one-off check: the whole
// point of M9a is that a real site's existing URLs keep resolving. Synthetic
// fixtures prove the grammar is implemented, but only real rows prove it is
// implemented the way WordPress actually wrote them — the zero-padding, the
// stored-date timezone, and the exact trailing-slash form all come from data,
// not from the spec.
//
// The database is strictly READ-ONLY here: the test never migrates and never
// writes. It reads the site's own permalink_structure option rather than
// assuming one, so it validates against whatever the target site is configured
// for.
func TestRealWordPressPermalinks(t *testing.T) {
	dsn := os.Getenv("GRIMOIRE_TEST_WP_DSN")
	if dsn == "" {
		t.Skip("set GRIMOIRE_TEST_WP_DSN to run the real WordPress permalink validation")
	}
	prefix := os.Getenv("GRIMOIRE_TEST_WP_PREFIX")
	if prefix == "" {
		prefix = "accuweaver"
	}

	ctx := context.Background()
	repos, err := storage.New(config.DatabaseConfig{Vendor: "mysql", DSN: dsn, TablePrefix: prefix})
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	defer repos.Close()

	// Read the site's real configuration instead of assuming a structure.
	structure, err := repos.Options.Get(ctx, "permalink_structure")
	if err != nil {
		t.Fatalf("read permalink_structure: %v", err)
	}
	categoryBase, _ := repos.Options.Get(ctx, "category_base")
	tagBase, _ := repos.Options.Get(ctx, "tag_base")
	t.Logf("site permalink_structure = %q", structure)

	s, err := routing.Parse(structure, categoryBase, tagBase)
	if err != nil {
		t.Fatalf("Parse(%q): %v — this site's structure is not yet supported", structure, err)
	}
	if s.Flat {
		t.Skipf("site uses plain permalinks (%q); nothing to validate", structure)
	}

	posts, err := repos.Posts.RecentPosts(ctx, 25, 0)
	if err != nil {
		t.Fatalf("RecentPosts: %v", err)
	}
	if len(posts) == 0 {
		t.Skip("no published posts in the target database")
	}

	for _, p := range posts {
		t.Run(p.Slug, func(t *testing.T) {
			got := s.Canonical(p)

			// Derive the expectation independently of Canonical, so this is a
			// real cross-check rather than the implementation agreeing with
			// itself. Only the dated presets are derived here; anything else
			// falls through to the round-trip assertions below.
			switch structure {
			case "/%year%/%monthnum%/%day%/%postname%/":
				want := fmt.Sprintf("/%04d/%02d/%02d/%s/",
					p.Date.Year(), int(p.Date.Month()), p.Date.Day(), p.Slug)
				if got != want {
					t.Fatalf("Canonical = %q, want %q", got, want)
				}
			case "/%year%/%monthnum%/%postname%/":
				want := fmt.Sprintf("/%04d/%02d/%s/",
					p.Date.Year(), int(p.Date.Month()), p.Slug)
				if got != want {
					t.Fatalf("Canonical = %q, want %q", got, want)
				}
			}

			// The canonical path must match the structure it came from, and
			// resolve back to this same post. If it did not, the handler would
			// redirect a canonical URL again — a redirect loop on every post.
			params, ok := s.ParamsFromPath(got)
			if !ok {
				t.Fatalf("ParamsFromPath rejected the path Canonical produced: %q", got)
			}
			ref, ok := s.Match(params)
			if !ok {
				t.Fatalf("Match rejected the path Canonical produced: %q (params %v)", got, params)
			}
			if ref.Slug != "" && ref.Slug != p.Slug {
				t.Errorf("round-tripped slug = %q, want %q", ref.Slug, p.Slug)
			}
			if ref.ID != 0 && ref.ID != p.ID {
				t.Errorf("round-tripped id = %d, want %d", ref.ID, p.ID)
			}
			// Date components must agree with the stored date, which is the
			// check that catches a timezone shift on real data.
			if ref.HasDate() {
				if ref.Year != p.Date.Year() {
					t.Errorf("round-tripped year = %d, want %d", ref.Year, p.Date.Year())
				}
				if ref.Month != 0 && ref.Month != int(p.Date.Month()) {
					t.Errorf("round-tripped month = %d, want %d", ref.Month, int(p.Date.Month()))
				}
				if ref.Day != 0 && ref.Day != p.Date.Day() {
					t.Errorf("round-tripped day = %d, want %d", ref.Day, p.Date.Day())
				}
			}
		})
	}
	t.Logf("validated %d real permalinks against structure %q", len(posts), structure)
}

// TestRealWordPressNestedCategories validates the nested-category half of the
// resolver against the same restored, REAL WordPress database (read-only), under
// the same gating as TestRealWordPressPermalinks above — one DSN enables every
// real-database check in the repo, and no new mechanism is introduced (Req 12.6).
//
//	GRIMOIRE_TEST_WP_DSN='wordpress:PASS@tcp(127.0.0.1:3306)/wordpress?parseTime=true' \
//	GRIMOIRE_TEST_WP_PREFIX=accuweaver \
//	go test ./internal/routing/ -run TestRealWordPressNestedCategories -v -count=1
//
// Why it is worth committing: the premise of M9b is that a real site's taxonomy
// is mostly nested — the reference database has 33 categories, 28 of them with
// term_taxonomy.parent <> 0 — so synthetic fixtures prove the grammar while only
// real rows prove the hierarchy is read and joined the way WordPress wrote it.
//
// # Expectations are derived, not borrowed
//
// Every expected path is assembled here from the raw rows and the raw options:
// the ancestry is walked up term_taxonomy.parent inside this test, the base is
// normalized from the site's own category_base, the front is read off the raw
// permalink_structure, and the segments are joined with plain string operations.
// CategoryPath is never consulted to build an expectation, only to be checked
// against one — otherwise the implementation would merely be agreeing with
// itself. Each derived path is then fed back through Classify, so a constructor
// and a classifier that disagreed would surface here as a failed round trip
// rather than as a redirect loop in production.
//
// # Read-only
//
// storage.New opens the pool without migrating and every call below is a read.
func TestRealWordPressNestedCategories(t *testing.T) {
	dsn := os.Getenv("GRIMOIRE_TEST_WP_DSN")
	if dsn == "" {
		t.Skip("set GRIMOIRE_TEST_WP_DSN to run the real WordPress nested-category validation")
	}
	prefix := os.Getenv("GRIMOIRE_TEST_WP_PREFIX")
	if prefix == "" {
		prefix = "accuweaver"
	}

	ctx := context.Background()
	repos, err := storage.New(config.DatabaseConfig{Vendor: "mysql", DSN: dsn, TablePrefix: prefix})
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	defer repos.Close()

	// The site's own configuration, again rather than an assumption: a renamed
	// category_base moves every path this test derives.
	structure, err := repos.Options.Get(ctx, "permalink_structure")
	if err != nil {
		t.Fatalf("read permalink_structure: %v", err)
	}
	rawCategoryBase, _ := repos.Options.Get(ctx, "category_base")
	rawTagBase, _ := repos.Options.Get(ctx, "tag_base")
	t.Logf("site permalink_structure = %q category_base = %q", structure, rawCategoryBase)

	s, parseErr := routing.Parse(structure, rawCategoryBase, rawTagBase)
	if parseErr != nil {
		// Parse returns a usable flat Structure alongside its error, and that
		// fallback still serves /{CategoryBase}/{slug} — which is exactly what
		// cmd/grimoire does on this path. So an unsupported structure is not a
		// reason to stop checking category archives; it only means there is no
		// front and no trailing slash to honor.
		t.Logf("structure %q is unsupported (%v); validating categories against "+
			"the flat fallback the server would use", structure, parseErr)
	}
	// "Usable" is the only fact about the structure the derivation needs. An
	// empty option (WordPress's plain permalinks) and an unsupported one both
	// yield a flat Structure, which carries neither a front nor a canonical
	// trailing slash, so today's /category/{slug} form is preserved byte for
	// byte (Req 2.7).
	usable := parseErr == nil && strings.TrimSpace(structure) != ""
	front := realWPFront(structure, usable)
	trailingSlash := usable && strings.HasSuffix(strings.TrimSpace(structure), "/")

	// The base, normalized from the raw option the way WordPress's admin UI
	// could have stored it ("/topics"), and whether it was provided at all —
	// which is what decides if the front applies to a category path (Req 4.8).
	baseSegs := realWPSegments(rawCategoryBase)
	base := strings.Join(baseSegs, "/")
	baseProvided := base != ""
	if !baseProvided {
		base = routing.DefaultCategoryBase
	}
	categoryFront := front
	if baseProvided {
		categoryFront = nil
	}

	terms, err := repos.TermReader.ListByTaxonomy(ctx, "category")
	if err != nil {
		t.Fatalf("ListByTaxonomy(category): %v", err)
	}
	if len(terms) == 0 {
		t.Skip("no category terms in the target database")
	}
	byID := make(map[int64]domain.Term, len(terms))
	for _, term := range terms {
		byID[term.ID] = term
	}
	// Taken from the raw rows before a single assertion runs, so the Req 12.7
	// skip below can name the precondition that actually failed rather than the
	// one that is merely most likely.
	census := realWPCensus(terms)
	t.Logf("category census: %d terms, %d with a resolvable parent, %d yielding a usable nested path",
		census.Terms, census.Nested, census.Usable)

	// A base that collides with a leading literal of the permalink structure
	// keeps the post meaning (Req 4.4b/9.7), so on such a site CategoryPath
	// advertises a path Classify deliberately declines. That is the documented
	// exception in Req 10.2, not a defect, so the round trip is skipped and
	// said out loud rather than asserted.
	roundTrip := true
	if len(front) > 0 && (base == front[0] || base == strings.Join(front, "/")) {
		roundTrip = false
		t.Logf("category base %q collides with the structure's front %q; the "+
			"post meaning wins for that segment (Req 4.4b), so the Classify "+
			"round trip does not apply on this site", base, strings.Join(front, "/"))
	}

	nested, checked := 0, 0
	for _, term := range terms {
		ancestry, isNested, pathSafe := realWPNestedCandidate(byID, term)
		if !pathSafe {
			t.Logf("skipping category %d (%q): a slug in its ancestry is not a "+
				"plain URL token", term.ID, term.Slug)
			continue
		}
		checked++
		if isNested {
			nested++
		}

		t.Run(strings.Join(ancestry, "-"), func(t *testing.T) {
			// Derived with plain string joining from the rows and the options,
			// never from the constructor under test.
			want := realWPDerivedCategoryPath(categoryFront, base, ancestry, trailingSlash)
			got := s.CategoryPath(ancestry)
			if got != want {
				t.Fatalf("CategoryPath(%v) = %q, want %q (derived from "+
					"term_taxonomy.parent and category_base=%q)",
					ancestry, got, want, rawCategoryBase)
			}
			if !roundTrip {
				return
			}
			// The derived path must classify back to this same category, with
			// the same segments in the same order. If it did not, the handler
			// would redirect a canonical category URL again — a redirect loop on
			// every nested category on the site.
			target := s.Classify(want)
			if target.Kind != routing.KindCategory {
				t.Fatalf("Classify(%q).Kind = %v, want KindCategory", want, target.Kind)
			}
			if !slices.Equal(target.Segments, ancestry) {
				t.Fatalf("Classify(%q).Segments = %v, want %v", want, target.Segments, ancestry)
			}
			if target.TrailingSlash != trailingSlash {
				t.Errorf("Classify(%q).TrailingSlash = %v, want %v",
					want, target.TrailingSlash, trailingSlash)
			}
		})
	}

	// Req 12.7: a check that reports success for a claim it never exercised is
	// worse than no check, so a run that asserted nothing about nesting skips
	// naming the precondition rather than passing.
	//
	// `nested` is what the skip is decided on, so it has to be worth deciding on.
	// The census counts nesting from term_taxonomy.parent while the loop counts it
	// from the length of the walked ancestry, and the two agreeing is what makes
	// either number evidence: a `nested` that over-counted — incremented before
	// the nesting test rather than after it, say — would make the skip
	// unreachable on an all-top-level site, which is the exact failure Req 12.7
	// exists to prevent. So the disagreement is fatal rather than logged.
	if nested != census.Usable {
		t.Fatalf("the loop counted %d nested categories and the census counted %d "+
			"usable among %d terms — one of them is wrong, so neither can be "+
			"trusted as evidence that the nested claim was exercised",
			nested, census.Usable, census.Terms)
	}
	if nested == 0 {
		t.Skip(census.precondition())
	}
	t.Logf("validated %d real category paths (%d nested) against base %q and structure %q",
		checked, nested, base, structure)
}

// realWPAncestry walks a term up term_taxonomy.parent and returns its slugs
// root-first, ending in the term's own slug — the shape CategoryPath consumes.
//
// The walk is done here, over the rows, rather than through content.Archive, so
// the expectation does not inherit the implementation's idea of the hierarchy.
// It degrades the two ways real, imported databases require: a parent that is
// absent from the taxonomy is treated as no parent from that point up (Req 1.4),
// and an already-visited term ends the walk instead of hanging it (Req 1.5).
//
// It returns false when any slug in the chain cannot sit in a request path
// unescaped. WordPress percent-encodes slugs derived from non-Latin names, and
// those raise a URL-escaping question this test is not about.
func realWPAncestry(byID map[int64]domain.Term, term domain.Term) ([]string, bool) {
	var reversed []string
	visited := map[int64]bool{}
	for cur := term; ; {
		if visited[cur.ID] {
			break
		}
		visited[cur.ID] = true
		if !realWPPathToken(cur.Slug) {
			return nil, false
		}
		reversed = append(reversed, cur.Slug)
		parent, ok := byID[cur.ParentID]
		if cur.ParentID == 0 || !ok {
			break
		}
		cur = parent
	}
	slices.Reverse(reversed)
	return reversed, true
}

// realWPNestedCandidate classifies one term for the nested-category assertions.
//
// It returns the walked ancestry, whether that ancestry is genuinely nested —
// more than one segment, so the path carries an ancestor and not just the term's
// own slug — and whether every slug in it can sit in a request path unescaped.
//
// This is one function rather than two expressions in the loop because the same
// decision has to be made in three places that must not drift: the loop that
// runs the assertions, the census that decides whether Req 12.7's skip fires,
// and the database-free tests in realdb_census_test.go that are the only
// demonstration anyone can run of the skip firing when it should.
func realWPNestedCandidate(byID map[int64]domain.Term, term domain.Term) (ancestry []string, nested, pathSafe bool) {
	ancestry, pathSafe = realWPAncestry(byID, term)
	return ancestry, pathSafe && len(ancestry) > 1, pathSafe
}

// realWPNestedCensus records what a category taxonomy offers the
// nested-category assertions, counted straight off the rows.
//
// It exists because Req 12.7's skip has to name the precondition an operator can
// act on, and "this site has no nested category" and "this site's nested
// categories carry slugs that cannot sit in a request path" are different facts
// that need different fixes. A single message asserting the first would send
// whoever reads it to the wrong place on a site where the second is true.
type realWPNestedCensus struct {
	// Terms is every term read from the category taxonomy.
	Terms int
	// Nested is the terms whose parent <> 0 and whose parent is present in the
	// set — the literal precondition Req 12.7 names. A parent absent from the
	// taxonomy is treated as no parent (Req 1.4), so it does not count.
	Nested int
	// Usable is the subset of Nested that yields an ancestry the assertions can
	// actually drive: at least two segments, every one of them a plain URL
	// token. A percent-encoded slug anywhere in the chain, or a parent chain
	// that closes a cycle (Req 1.5), leaves a term counted in Nested and not
	// here.
	Usable int
}

// realWPCensus counts the taxonomy without asserting on it.
func realWPCensus(terms []domain.Term) realWPNestedCensus {
	byID := make(map[int64]domain.Term, len(terms))
	for _, term := range terms {
		byID[term.ID] = term
	}
	census := realWPNestedCensus{Terms: len(terms)}
	for _, term := range terms {
		if _, ok := byID[term.ParentID]; term.ParentID == 0 || !ok {
			continue
		}
		census.Nested++
		if _, nested, _ := realWPNestedCandidate(byID, term); nested {
			census.Usable++
		}
	}
	return census
}

// precondition states, in terms an operator can act on, why the taxonomy
// exercised no nested-category assertion.
func (c realWPNestedCensus) precondition() string {
	if c.Nested == 0 {
		return fmt.Sprintf("no category in the target database has a resolvable "+
			"parent (%d category terms, all top-level); the nested-category "+
			"assertions need at least one term_taxonomy row with parent <> 0 "+
			"pointing at an existing category", c.Terms)
	}
	return fmt.Sprintf("%d of %d category terms have parent <> 0 pointing at an "+
		"existing category, but none yields a usable nested path: every one was "+
		"rejected because a slug in its ancestry is not a plain URL token "+
		"([A-Za-z0-9_-]) or its parent chain closes a cycle; the nested-category "+
		"assertions need one nested category whose whole ancestry is a plain URL "+
		"token", c.Nested, c.Terms)
}

// realWPDerivedCategoryPath assembles the expected archive path by plain string
// joining: the front where it applies, then the normalized base segments, then
// the ancestry, then the trailing slash the structure implies. This is the
// independent half of the cross-check, so it must not call CategoryPath or any
// other exported constructor.
func realWPDerivedCategoryPath(front []string, base string, ancestry []string, trailingSlash bool) string {
	segs := make([]string, 0, len(front)+len(ancestry)+2)
	segs = append(segs, front...)
	segs = append(segs, realWPSegments(base)...)
	segs = append(segs, ancestry...)
	path := "/" + strings.Join(segs, "/")
	if trailingSlash {
		path += "/"
	}
	return path
}

// realWPFront reads the structure's front off the raw option: the maximal run of
// leading literal segments, which is empty for a structure that yields a flat
// Structure because such a site serves no archive under a front at all.
func realWPFront(structure string, usable bool) []string {
	if !usable {
		return nil
	}
	var out []string
	for _, seg := range realWPSegments(structure) {
		if strings.HasPrefix(seg, "%") && strings.HasSuffix(seg, "%") {
			break
		}
		out = append(out, seg)
	}
	return out
}

// realWPSegments reduces a raw option value to its bare segments: surrounding
// whitespace trimmed, leading and trailing slashes dropped, repeated internal
// slashes collapsed. WordPress's options-permalink.php stores a submitted base
// with a leading slash, so "/topics" is a realistic value rather than a
// hypothetical one.
func realWPSegments(v string) []string {
	parts := strings.Split(strings.TrimSpace(v), "/")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	return out
}

// realWPPathToken reports whether a slug can be placed in a request path
// unescaped.
func realWPPathToken(slug string) bool {
	if slug == "" {
		return false
	}
	for _, r := range slug {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}
