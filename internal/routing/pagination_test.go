package routing_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/roboweaver/grimoire/internal/routing"
)

// M16 task 1.1: the failing routing tests for path-form pagination. They are
// written before any production code and fail to build until task 1.2 adds
// ParsePageNumber, MaxPageNumber, KindHome, Target.Page, Structure.PaginatedPath,
// Structure.PageLinks and Structure.PaginationPatterns. See design.md,
// "Disambiguation matrix" and "internal/routing/routing.go".

// Structures of Requirement 13.1. Spelled here so this file depends on no
// constant declared in another test file.
const (
	pgFlat        = ""
	pgPostName    = "/%postname%/"
	pgPostNameNS  = "/%postname%"
	pgDateName    = "/%year%/%monthnum%/%postname%/"
	pgBlogDate    = "/blog/%year%/%monthnum%/%postname%/"
	pgNameAndID   = "/%postname%/%post_id%/"
	pgPostIDOnly  = "/%post_id%/"
	pgBlogNameNS  = "/blog/%postname%"
	pgBlogNameTS  = "/blog/%postname%/"
	pgPageBaseOpt = "page"
)

// pgTypes and pgTaxes are the registry the custom rows run against: a custom
// type with an archive, a hierarchical custom type without one, and a
// hierarchical custom taxonomy. All three carry the front so the front-bearing
// structures exercise CustomBase's with_front rule.
var (
	pgTypes = []routing.TypeRoute{
		{Name: "book", Slug: "book", ArchiveSlug: "books", WithFront: true},
		{Name: "film", Slug: "films", Hierarchical: true, WithFront: true},
	}
	pgTaxes = []routing.TaxRoute{
		{Name: "genre", Slug: "genre", Hierarchical: true, WithFront: true},
	}
)

func pgParse(t *testing.T, structure, catBase, tagBase string, custom bool) routing.Structure {
	t.Helper()
	s, err := routing.Parse(structure, catBase, tagBase)
	if err != nil {
		t.Fatalf("Parse(%q, %q, %q) error: %v", structure, catBase, tagBase, err)
	}
	if custom {
		s = s.WithCustomTypes(pgTypes, pgTaxes)
	}
	return s
}

// ---------------------------------------------------------------------------
// (a) ParsePageNumber
// ---------------------------------------------------------------------------

// Requirement 2: a page number is ^[1-9][0-9]{0,8}$.
func TestParsePageNumber(t *testing.T) {
	if routing.MaxPageNumber != 999_999_999 {
		t.Errorf("MaxPageNumber = %d, want 999999999", routing.MaxPageNumber)
	}
	accept := map[string]int{"1": 1, "2": 2, "10": 10, "999999999": 999_999_999}
	for in, want := range accept {
		got, ok := routing.ParsePageNumber(in)
		if !ok || got != want {
			t.Errorf("ParsePageNumber(%q) = %d, %v; want %d, true", in, got, ok, want)
		}
	}
	for _, in := range []string{
		"", "0", "02", "+2", "-2", "2a", " 2", "1000000000", "99999999999999999999",
	} {
		if got, ok := routing.ParsePageNumber(in); ok {
			t.Errorf("ParsePageNumber(%q) = %d, true; want rejection", in, got)
		}
	}
}

// ---------------------------------------------------------------------------
// (b) The disambiguation matrix
// ---------------------------------------------------------------------------

// pgWant is the expected Target. TrailingSlash is not listed: each row runs in
// both slash forms and TrailingSlash must equal the form of the request path.
type pgWant struct {
	kind     routing.Kind
	segments []string
	slug     string
	date     routing.DateRef
	typeName string
	page     int
}

type pgRow struct {
	name      string
	structure string
	catBase   string
	tagBase   string
	custom    bool
	path      string // written without a trailing slash; both forms are run
	want      pgWant
}

func pgSegs(s ...string) []string { return s }

func TestClassifyPaginationMatrix(t *testing.T) {
	rows := []pgRow{
		// Home.
		{name: "home page 2", structure: pgPostName, path: "/page/2",
			want: pgWant{kind: routing.KindHome, page: 2}},
		{name: "home page 1 is still classified, the handler redirects it",
			structure: pgPostName, path: "/page/1",
			want: pgWant{kind: routing.KindHome, page: 1}},
		{name: "home max page", structure: pgPostName, path: "/page/999999999",
			want: pgWant{kind: routing.KindHome, page: 999_999_999}},
		{name: "home page under flat", structure: pgFlat, path: "/page/2",
			want: pgWant{kind: routing.KindHome, page: 2}},
		{name: "home page shadows a post id under /%postname%/%post_id%/",
			structure: pgNameAndID, path: "/page/2",
			want: pgWant{kind: routing.KindHome, page: 2}},
		{name: "home page under /%post_id%/", structure: pgPostIDOnly, path: "/page/2",
			want: pgWant{kind: routing.KindHome, page: 2}},

		// Not a page number: the path keeps its pre-M16 meaning.
		{name: "page/0 stays a page walk", structure: pgPostName, path: "/page/0",
			want: pgWant{kind: routing.KindPage, segments: pgSegs("page", "0")}},
		{name: "page/02 stays a page walk", structure: pgPostName, path: "/page/02",
			want: pgWant{kind: routing.KindPage, segments: pgSegs("page", "02")}},
		{name: "page/+2 stays a page walk", structure: pgPostName, path: "/page/+2",
			want: pgWant{kind: routing.KindPage, segments: pgSegs("page", "+2")}},
		{name: "page/x stays a page walk", structure: pgPostName, path: "/page/x",
			want: pgWant{kind: routing.KindPage, segments: pgSegs("page", "x")}},
		{name: "page/1234567890 stays a page walk", structure: pgPostName,
			path: "/page/1234567890",
			want: pgWant{kind: routing.KindPage, segments: pgSegs("page", "1234567890")}},
		{name: "bare page is a post slugged page", structure: pgPostName, path: "/page",
			want: pgWant{kind: routing.KindPost}},
		{name: "page/2/3 keeps the page walk, the last pair is 2/3",
			structure: pgPostName, path: "/page/2/3",
			want: pgWant{kind: routing.KindPage, segments: pgSegs("page", "2", "3")}},
		{name: "about/page/2 is a nested page, /about is not paginatable",
			structure: pgPostName, path: "/about/page/2",
			want: pgWant{kind: routing.KindPage, segments: pgSegs("about", "page", "2")}},

		// Categories.
		{name: "category page 2", structure: pgPostName, path: "/category/news/page/2",
			want: pgWant{kind: routing.KindCategory, segments: pgSegs("news"), page: 2}},
		{name: "nested category page 3", structure: pgPostName,
			path: "/category/news/local/page/3",
			want: pgWant{kind: routing.KindCategory, segments: pgSegs("news", "local"), page: 3}},
		{name: "category page 1 is classified", structure: pgPostName,
			path: "/category/news/page/1",
			want: pgWant{kind: routing.KindCategory, segments: pgSegs("news"), page: 1}},
		{name: "category with a trailing page segment and no number",
			structure: pgPostName, path: "/category/news/page",
			want: pgWant{kind: routing.KindCategory, segments: pgSegs("news", "page")}},
		{name: "category page followed by a non-number",
			structure: pgPostName, path: "/category/news/page/archive",
			want: pgWant{kind: routing.KindCategory, segments: pgSegs("news", "page", "archive")}},
		{name: "category page/2 under a bare base is a category named page then 2",
			structure: pgPostName, path: "/category/page/2",
			want: pgWant{kind: routing.KindCategory, segments: pgSegs("page", "2")}},
		{name: "category named page, page 2", structure: pgPostName,
			path: "/category/page/page/2",
			want: pgWant{kind: routing.KindCategory, segments: pgSegs("page"), page: 2}},
		{name: "bare category base is nothing", structure: pgPostName, path: "/category",
			want: pgWant{kind: routing.KindNone}},
		{name: "category page 2 under flat", structure: pgFlat,
			path: "/category/news/page/2",
			want: pgWant{kind: routing.KindCategory, segments: pgSegs("news"), page: 2}},
		{name: "renamed category base", structure: pgPostName, catBase: "sections",
			path: "/sections/news/page/2",
			want: pgWant{kind: routing.KindCategory, segments: pgSegs("news"), page: 2}},
		{name: "renamed category base under a front drops the front",
			structure: pgBlogDate, catBase: "sections", path: "/sections/news/page/2",
			want: pgWant{kind: routing.KindCategory, segments: pgSegs("news"), page: 2}},
		{name: "category under a front", structure: pgBlogDate,
			path: "/blog/category/news/page/2",
			want: pgWant{kind: routing.KindCategory, segments: pgSegs("news"), page: 2}},

		// Tags and authors.
		{name: "tag page 2", structure: pgPostName, path: "/tag/x/page/2",
			want: pgWant{kind: routing.KindTag, slug: "x", page: 2}},
		{name: "tag page 2 under flat", structure: pgFlat, path: "/tag/x/page/2",
			want: pgWant{kind: routing.KindTag, slug: "x", page: 2}},
		{name: "tag base followed by page/2 is nothing", structure: pgPostName,
			path: "/tag/page/2", want: pgWant{kind: routing.KindNone}},
		{name: "tag named page, page 2", structure: pgPostName, path: "/tag/page/page/2",
			want: pgWant{kind: routing.KindTag, slug: "page", page: 2}},
		{name: "renamed tag base", structure: pgPostName, tagBase: "topics",
			path: "/topics/x/page/2",
			want: pgWant{kind: routing.KindTag, slug: "x", page: 2}},
		{name: "tag under a front", structure: pgBlogDate, path: "/blog/tag/x/page/2",
			want: pgWant{kind: routing.KindTag, slug: "x", page: 2}},
		{name: "author page 2", structure: pgPostName, path: "/author/jane/page/2",
			want: pgWant{kind: routing.KindAuthor, slug: "jane", page: 2}},
		{name: "author under a front", structure: pgBlogDate,
			path: "/blog/author/jane/page/2",
			want: pgWant{kind: routing.KindAuthor, slug: "jane", page: 2}},

		// Dates.
		{name: "year page 2", structure: pgPostName, path: "/2024/page/2",
			want: pgWant{kind: routing.KindDate, date: routing.DateRef{Year: 2024}, page: 2}},
		{name: "month page 2", structure: pgPostName, path: "/2024/05/page/2",
			want: pgWant{kind: routing.KindDate, date: routing.DateRef{Year: 2024, Month: 5}, page: 2}},
		{name: "day page 2", structure: pgPostName, path: "/2024/05/17/page/2",
			want: pgWant{kind: routing.KindDate,
				date: routing.DateRef{Year: 2024, Month: 5, Day: 17}, page: 2}},
		{name: "year page 2 under a dated structure", structure: pgDateName,
			path: "/2024/page/2",
			want: pgWant{kind: routing.KindDate, date: routing.DateRef{Year: 2024}, page: 2}},
		{name: "year page 2 under flat serves no date archive", structure: pgFlat,
			path: "/2024/page/2",
			want: pgWant{kind: routing.KindPage, segments: pgSegs("2024", "page", "2")}},
		{name: "month 13 is not a date", structure: pgPostName, path: "/2024/13/page/2",
			want: pgWant{kind: routing.KindPage, segments: pgSegs("2024", "13", "page", "2")}},
		{name: "date under a front", structure: pgBlogDate, path: "/blog/2024/page/2",
			want: pgWant{kind: routing.KindDate, date: routing.DateRef{Year: 2024}, page: 2}},
		{name: "date without the front is not a date under a front", structure: pgBlogDate,
			path: "/2024/page/2",
			want: pgWant{kind: routing.KindPage, segments: pgSegs("2024", "page", "2")}},
		{name: "date behind the date/ disambiguation prefix", structure: pgPostIDOnly,
			path: "/date/2024/05/page/2",
			want: pgWant{kind: routing.KindDate, date: routing.DateRef{Year: 2024, Month: 5}, page: 2}},
		{name: "date behind the date/ prefix under postname and id", structure: pgNameAndID,
			path: "/date/2024/page/2",
			want: pgWant{kind: routing.KindDate, date: routing.DateRef{Year: 2024}, page: 2}},

		// Custom archive, custom post type and custom taxonomy.
		{name: "custom archive page 2", structure: pgPostName, custom: true,
			path: "/books/page/2",
			want: pgWant{kind: routing.KindCustomArchive, typeName: "book", page: 2}},
		{name: "custom archive under a front", structure: pgBlogNameTS, custom: true,
			path: "/blog/books/page/2",
			want: pgWant{kind: routing.KindCustomArchive, typeName: "book", page: 2}},
		{name: "custom type without an archive keeps its meaning", structure: pgPostName,
			custom: true, path: "/films/page/2",
			want: pgWant{kind: routing.KindCustomPost, typeName: "film",
				segments: pgSegs("page", "2")}},
		{name: "custom term page 2", structure: pgPostName, custom: true,
			path: "/genre/rock/page/2",
			want: pgWant{kind: routing.KindCustomTerm, typeName: "genre",
				segments: pgSegs("rock"), page: 2}},
		{name: "nested custom term page 2", structure: pgPostName, custom: true,
			path: "/genre/music/rock/page/2",
			want: pgWant{kind: routing.KindCustomTerm, typeName: "genre",
				segments: pgSegs("music", "rock"), page: 2}},
		{name: "custom term without a page suffix", structure: pgPostName, custom: true,
			path: "/genre/rock",
			want: pgWant{kind: routing.KindCustomTerm, typeName: "genre",
				segments: pgSegs("rock")}},
	}
	for _, row := range rows {
		for _, slash := range []bool{false, true} {
			path := row.path
			if slash {
				path += "/"
			}
			t.Run(fmt.Sprintf("%s [%s]", row.name, path), func(t *testing.T) {
				s := pgParse(t, row.structure, row.catBase, row.tagBase, row.custom)
				got := s.Classify(path)
				pgAssertTarget(t, path, got, row.want, slash)
			})
		}
	}
}

func pgAssertTarget(t *testing.T, path string, got routing.Target, want pgWant, slash bool) {
	t.Helper()
	if got.Kind != want.kind {
		t.Fatalf("Classify(%q).Kind = %v, want %v (target %+v)", path, got.Kind, want.kind, got)
	}
	if !slices.Equal(got.Segments, want.segments) {
		t.Errorf("Classify(%q).Segments = %q, want %q", path, got.Segments, want.segments)
	}
	if got.Slug != want.slug {
		t.Errorf("Classify(%q).Slug = %q, want %q", path, got.Slug, want.slug)
	}
	if got.Date != want.date {
		t.Errorf("Classify(%q).Date = %+v, want %+v", path, got.Date, want.date)
	}
	if got.TypeName != want.typeName {
		t.Errorf("Classify(%q).TypeName = %q, want %q", path, got.TypeName, want.typeName)
	}
	if got.Page != want.page {
		t.Errorf("Classify(%q).Page = %d, want %d", path, got.Page, want.page)
	}
	if got.TrailingSlash != slash {
		t.Errorf("Classify(%q).TrailingSlash = %v, want %v", path, got.TrailingSlash, slash)
	}
}

// "/" is still KindNone, and a path with no page/{n} suffix has Page == 0.
func TestClassifyHomeStaysNoneAndUnpagedHasPageZero(t *testing.T) {
	s := pgParse(t, pgPostName, "", "", true)
	if got := s.Classify("/"); got.Kind != routing.KindNone || got.Page != 0 {
		t.Errorf("Classify(%q) = %+v, want KindNone with Page 0", "/", got)
	}
	for _, p := range []string{
		"/category/news", "/category/news/", "/tag/x", "/author/jane", "/2024/05",
		"/books", "/genre/rock", "/hello-world", "/about/team",
	} {
		if got := s.Classify(p); got.Page != 0 {
			t.Errorf("Classify(%q).Page = %d, want 0", p, got.Page)
		}
	}
}

// ---------------------------------------------------------------------------
// (c) The fixed-point loop
// ---------------------------------------------------------------------------

type pgKind struct {
	name string
	want routing.Kind
	// build returns the page-1 path of a known entity ("" when the structure
	// does not serve that archive).
	build func(routing.Structure) string
	// rebuild builds the page-1 path from the classified Target alone.
	rebuild func(routing.Structure, routing.Target) string
}

func pgKinds() []pgKind {
	return []pgKind{
		{"category", routing.KindCategory,
			func(s routing.Structure) string { return s.CategoryPath([]string{"news"}) },
			func(s routing.Structure, t routing.Target) string { return s.CategoryPath(t.Segments) }},
		{"nested category", routing.KindCategory,
			func(s routing.Structure) string { return s.CategoryPath([]string{"news", "local", "towns"}) },
			func(s routing.Structure, t routing.Target) string { return s.CategoryPath(t.Segments) }},
		{"tag", routing.KindTag,
			func(s routing.Structure) string { return s.TagPath("go") },
			func(s routing.Structure, t routing.Target) string { return s.TagPath(t.Slug) }},
		{"author", routing.KindAuthor,
			func(s routing.Structure) string { return s.AuthorPath("alice") },
			func(s routing.Structure, t routing.Target) string { return s.AuthorPath(t.Slug) }},
		{"year", routing.KindDate,
			func(s routing.Structure) string { return s.DatePath(routing.DateRef{Year: 2024}) },
			func(s routing.Structure, t routing.Target) string { return s.DatePath(t.Date) }},
		{"month", routing.KindDate,
			func(s routing.Structure) string { return s.DatePath(routing.DateRef{Year: 2024, Month: 5}) },
			func(s routing.Structure, t routing.Target) string { return s.DatePath(t.Date) }},
		{"day", routing.KindDate,
			func(s routing.Structure) string {
				return s.DatePath(routing.DateRef{Year: 2024, Month: 5, Day: 17})
			},
			func(s routing.Structure, t routing.Target) string { return s.DatePath(t.Date) }},
		{"custom archive", routing.KindCustomArchive,
			func(s routing.Structure) string { return s.CustomArchivePath("book") },
			func(s routing.Structure, t routing.Target) string { return s.CustomArchivePath(t.TypeName) }},
		{"custom term", routing.KindCustomTerm,
			func(s routing.Structure) string { return s.CustomTermPath("genre", []string{"rock"}) },
			func(s routing.Structure, t routing.Target) string {
				return s.CustomTermPath(t.TypeName, t.Segments)
			}},
		{"nested custom term", routing.KindCustomTerm,
			func(s routing.Structure) string {
				return s.CustomTermPath("genre", []string{"music", "rock"})
			},
			func(s routing.Structure, t routing.Target) string {
				return s.CustomTermPath(t.TypeName, t.Segments)
			}},
	}
}

type pgConfig struct {
	name      string
	structure string
	catBase   string
	tagBase   string
}

// pgConfigs are the structures of Requirement 13.1, with and without a trailing
// slash, plus a renamed category_base.
func pgConfigs() []pgConfig {
	return []pgConfig{
		{name: "flat", structure: pgFlat},
		{name: "postname slash", structure: pgPostName},
		{name: "postname no slash", structure: pgPostNameNS},
		{name: "year month postname", structure: pgDateName},
		{name: "blog year month postname", structure: pgBlogDate},
		{name: "blog postname slash", structure: pgBlogNameTS},
		{name: "blog postname no slash", structure: pgBlogNameNS},
		{name: "postname and id", structure: pgNameAndID},
		{name: "post id only", structure: pgPostIDOnly},
		{name: "renamed category base", structure: pgBlogDate, catBase: "sections"},
		{name: "renamed both bases", structure: pgBlogDate, catBase: "sections", tagBase: "topics"},
	}
}

func pgFlipSlash(p string) string {
	if strings.HasSuffix(p, "/") {
		return strings.TrimSuffix(p, "/")
	}
	return p + "/"
}

// Requirement 13: every canonical paginated path is a fixed point of
// construct -> Classify -> construct, with the same entity as its page-1 path.
func TestPaginatedPathsAreClassifyFixedPoints(t *testing.T) {
	for _, cfg := range pgConfigs() {
		s := pgParse(t, cfg.structure, cfg.catBase, cfg.tagBase, true)
		for _, k := range pgKinds() {
			base := k.build(s)
			if base == "" {
				if k.want == routing.KindDate && s.Flat {
					continue // plain permalinks serve no date archive
				}
				t.Fatalf("%s/%s: constructor returned no path", cfg.name, k.name)
			}
			baseTarget := s.Classify(base)
			if baseTarget.Kind != k.want {
				t.Fatalf("%s/%s: Classify(%q).Kind = %v, want %v (test fixture is wrong)",
					cfg.name, k.name, base, baseTarget.Kind, k.want)
			}
			for _, n := range []int{2, 3, 10, 999_999_999} {
				t.Run(fmt.Sprintf("%s/%s/page %d", cfg.name, k.name, n), func(t *testing.T) {
					p := s.PaginatedPath(base, n)
					got := s.Classify(p)
					pgSameEntity(t, p, got, baseTarget)
					if got.Page != n {
						t.Errorf("Classify(%q).Page = %d, want %d", p, got.Page, n)
					}
					if baseTarget.Page != 0 {
						t.Errorf("Classify(%q).Page = %d, want 0", base, baseTarget.Page)
					}
					if got.TrailingSlash != s.TrailingSlash {
						t.Errorf("Classify(%q).TrailingSlash = %v, want %v",
							p, got.TrailingSlash, s.TrailingSlash)
					}
					if rebuilt := s.PaginatedPath(k.rebuild(s, got), got.Page); rebuilt != p {
						t.Errorf("rebuilt %q from Classify(%q), want %q", rebuilt, p, p)
					}
					// The non-canonical slash form classifies to the same entity
					// and page, which is what lets the handler 301 it.
					alt := pgFlipSlash(p)
					gotAlt := s.Classify(alt)
					pgSameEntity(t, alt, gotAlt, baseTarget)
					if gotAlt.Page != n {
						t.Errorf("Classify(%q).Page = %d, want %d", alt, gotAlt.Page, n)
					}
					if gotAlt.TrailingSlash == s.TrailingSlash {
						t.Errorf("Classify(%q).TrailingSlash = %v, want the non-canonical form",
							alt, gotAlt.TrailingSlash)
					}
				})
			}
		}
		// The home page: "/" is KindNone, its paginated form is KindHome.
		for _, n := range []int{2, 3, 10, 999_999_999} {
			t.Run(fmt.Sprintf("%s/home/page %d", cfg.name, n), func(t *testing.T) {
				p := s.PaginatedPath("/", n)
				got := s.Classify(p)
				if got.Kind != routing.KindHome || got.Page != n {
					t.Fatalf("Classify(%q) = %+v, want KindHome page %d", p, got, n)
				}
				if got.TrailingSlash != s.TrailingSlash {
					t.Errorf("Classify(%q).TrailingSlash = %v, want %v",
						p, got.TrailingSlash, s.TrailingSlash)
				}
				if again := s.PaginatedPath("/", got.Page); again != p {
					t.Errorf("rebuilt %q, want %q", again, p)
				}
			})
		}
	}
}

func pgSameEntity(t *testing.T, path string, got, base routing.Target) {
	t.Helper()
	if got.Kind != base.Kind {
		t.Fatalf("Classify(%q).Kind = %v, want %v (the page-1 kind)", path, got.Kind, base.Kind)
	}
	if !slices.Equal(got.Segments, base.Segments) || got.Slug != base.Slug ||
		got.Date != base.Date || got.TypeName != base.TypeName {
		t.Errorf("Classify(%q) entity = %+v, want the page-1 entity %+v", path, got, base)
	}
}

// ---------------------------------------------------------------------------
// (d) PaginatedPath vectors
// ---------------------------------------------------------------------------

func TestPaginatedPathVectors(t *testing.T) {
	withSlash := pgParse(t, pgPostName, "", "", false)
	noSlash := pgParse(t, pgPostNameNS, "", "", false)
	flat := pgParse(t, pgFlat, "", "", false)
	if !withSlash.TrailingSlash || noSlash.TrailingSlash || flat.TrailingSlash {
		t.Fatalf("fixture slash policy wrong: %v %v %v",
			withSlash.TrailingSlash, noSlash.TrailingSlash, flat.TrailingSlash)
	}
	cases := []struct {
		name string
		s    routing.Structure
		base string
		n    int
		want string
	}{
		// With a trailing slash the result always has one, whatever form base has.
		{"slash: base with slash, n 2", withSlash, "/category/news/", 2, "/category/news/page/2/"},
		{"slash: base without slash, n 2", withSlash, "/category/news", 2, "/category/news/page/2/"},
		{"slash: n 10", withSlash, "/category/news/", 10, "/category/news/page/10/"},
		{"slash: n 0 returns base", withSlash, "/category/news/", 0, "/category/news/"},
		{"slash: n 1 returns base", withSlash, "/category/news/", 1, "/category/news/"},
		{"slash: n 1 returns base as given", withSlash, "/category/news", 1, "/category/news"},
		{"slash: n 0 returns base as given", withSlash, "/category/news", 0, "/category/news"},
		{"slash: home n 2", withSlash, "/", 2, "/page/2/"},
		{"slash: home n 1", withSlash, "/", 1, "/"},
		{"slash: home n 0", withSlash, "/", 0, "/"},
		// Without a trailing slash the result never has one.
		{"no slash: base with slash, n 2", noSlash, "/category/news/", 2, "/category/news/page/2"},
		{"no slash: base without slash, n 2", noSlash, "/category/news", 2, "/category/news/page/2"},
		{"no slash: n 0 returns base", noSlash, "/category/news", 0, "/category/news"},
		{"no slash: n 1 returns base", noSlash, "/category/news", 1, "/category/news"},
		{"no slash: home n 2", noSlash, "/", 2, "/page/2"},
		{"no slash: home n 1", noSlash, "/", 1, "/"},
		{"flat: tag n 3", flat, "/tag/go", 3, "/tag/go/page/3"},
		{"flat: home n 2", flat, "/", 2, "/page/2"},
		// An empty base addresses nothing.
		{"slash: empty base n 2", withSlash, "", 2, ""},
		{"slash: empty base n 0", withSlash, "", 0, ""},
		{"no slash: empty base n 2", noSlash, "", 2, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.s.PaginatedPath(tc.base, tc.n); got != tc.want {
				t.Errorf("PaginatedPath(%q, %d) = %q, want %q", tc.base, tc.n, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// (e) PageLinks
// ---------------------------------------------------------------------------

func TestPageLinks(t *testing.T) {
	for _, slash := range []bool{true, false} {
		structure, suffix := pgPostName, "/"
		if !slash {
			structure, suffix = pgPostNameNS, ""
		}
		s := pgParse(t, structure, "", "", false)
		base := "/category/news" + suffix
		p := func(n int) string { return "/category/news/page/" + fmt.Sprint(n) + suffix }
		cases := []struct {
			name             string
			base             string
			page, total      int
			wantPrev, wantNx string
		}{
			{"first page", base, 1, 3, "", p(2)},
			{"page 2 links back to the base, never page 1", base, 2, 3, base, p(3)},
			{"middle page", base, 3, 5, p(2), p(4)},
			{"last page", base, 3, 3, p(2), ""},
			{"single page", base, 1, 1, "", ""},
			{"no pages", base, 1, 0, "", ""},
			{"page zero, no pages", base, 0, 0, "", ""},
			{"empty base", "", 2, 5, "", ""},
			{"empty base first page", "", 1, 5, "", ""},
			{"home page 2 links back to /", "/", 2, 3, "/", "/page/3" + suffix},
			{"home first page", "/", 1, 3, "", "/page/2" + suffix},
		}
		for _, tc := range cases {
			t.Run(fmt.Sprintf("slash=%v/%s", slash, tc.name), func(t *testing.T) {
				prev, next := s.PageLinks(tc.base, tc.page, tc.total)
				if prev != tc.wantPrev || next != tc.wantNx {
					t.Errorf("PageLinks(%q, %d, %d) = (%q, %q), want (%q, %q)",
						tc.base, tc.page, tc.total, prev, next, tc.wantPrev, tc.wantNx)
				}
				if strings.Contains(prev, "/page/1") || strings.Contains(next, "/page/1/") ||
					strings.HasSuffix(next, "/page/1") {
					t.Errorf("PageLinks(%q, %d, %d) emitted a /page/1 form: (%q, %q)",
						tc.base, tc.page, tc.total, prev, next)
				}
			})
		}
	}
}

// ---------------------------------------------------------------------------
// (f) PaginationPatterns
// ---------------------------------------------------------------------------

// pgBothForms returns each pattern and its trailing-slash twin.
func pgBothForms(pats ...string) []string {
	var out []string
	for _, p := range pats {
		out = append(out, p, p+"/")
	}
	return out
}

func pgAssertPatternSet(t *testing.T, got, want []string) {
	t.Helper()
	seen := map[string]bool{}
	for _, p := range got {
		if seen[p] {
			t.Errorf("PaginationPatterns() returned %q more than once: %v", p, got)
		}
		seen[p] = true
		if !strings.HasPrefix(p, "/") {
			t.Errorf("PaginationPatterns() returned %q, not a rooted chi pattern", p)
		}
		if !strings.Contains(p, "/page/{pagenum}") {
			t.Errorf("PaginationPatterns() returned %q, which is not a pagination pattern", p)
		}
		if strings.Contains(p, "*") {
			t.Errorf("PaginationPatterns() returned wildcard %q: category and custom-term "+
				"wildcards already reach the dispatcher", p)
		}
	}
	g, w := slices.Clone(got), slices.Clone(want)
	slices.Sort(g)
	slices.Sort(w)
	if !slices.Equal(g, w) {
		t.Errorf("PaginationPatterns() =\n\t%s\nwant (order-insensitive)\n\t%s",
			strings.Join(g, "\n\t"), strings.Join(w, "\n\t"))
	}
}

func TestPaginationPatterns(t *testing.T) {
	const pg = "/page/{pagenum}"
	dateTails := func(prefix string) []string {
		return []string{
			prefix + "/{year}" + pg,
			prefix + "/{year}/{monthnum}" + pg,
			prefix + "/{year}/{monthnum}/{day}" + pg,
		}
	}
	cases := []struct {
		name      string
		structure string
		catBase   string
		tagBase   string
		custom    []routing.TypeRoute
		want      []string
	}{
		{name: "flat has no date patterns", structure: pgFlat,
			want: pgBothForms(pg, "/tag/{slug}"+pg, "/author/{nicename}"+pg)},
		{name: "postname", structure: pgPostName,
			want: pgBothForms(append([]string{pg, "/tag/{slug}" + pg, "/author/{nicename}" + pg},
				dateTails("")...)...)},
		{name: "postname without trailing slash registers both forms too", structure: pgPostNameNS,
			want: pgBothForms(append([]string{pg, "/tag/{slug}" + pg, "/author/{nicename}" + pg},
				dateTails("")...)...)},
		{name: "year month postname", structure: pgDateName,
			want: pgBothForms(append([]string{pg, "/tag/{slug}" + pg, "/author/{nicename}" + pg},
				dateTails("")...)...)},
		{name: "front applies to tag, author and date", structure: pgBlogDate,
			want: pgBothForms(append([]string{pg, "/blog/tag/{slug}" + pg, "/blog/author/{nicename}" + pg},
				dateTails("/blog")...)...)},
		{name: "postname and id moves dates behind date/", structure: pgNameAndID,
			want: pgBothForms(append([]string{pg, "/tag/{slug}" + pg, "/author/{nicename}" + pg},
				dateTails("/date")...)...)},
		{name: "post id only moves dates behind date/", structure: pgPostIDOnly,
			want: pgBothForms(append([]string{pg, "/tag/{slug}" + pg, "/author/{nicename}" + pg},
				dateTails("/date")...)...)},
		{name: "renamed category base adds no category pattern", structure: pgBlogDate,
			catBase: "sections",
			want: pgBothForms(append([]string{pg, "/blog/tag/{slug}" + pg, "/blog/author/{nicename}" + pg},
				dateTails("/blog")...)...)},
		{name: "renamed tag base drops the front for tags only", structure: pgBlogDate,
			catBase: "sections", tagBase: "topics",
			want: pgBothForms(append([]string{pg, "/topics/{slug}" + pg, "/blog/author/{nicename}" + pg},
				dateTails("/blog")...)...)},
		{name: "custom archives add one pattern each", structure: pgBlogNameTS,
			custom: []routing.TypeRoute{
				{Name: "book", Slug: "book", ArchiveSlug: "books", WithFront: true},
				{Name: "show", Slug: "show", ArchiveSlug: "shows"},
				{Name: "film", Slug: "films", Hierarchical: true, WithFront: true},
			},
			// The structure has segments, so date patterns are present (design.md:
			// PaginationPatterns lists them whenever servesDateArchives()).
			want: pgBothForms(append([]string{pg, "/blog/tag/{slug}" + pg,
				"/blog/author/{nicename}" + pg, "/blog/books" + pg, "/shows" + pg},
				dateTails("/blog")...)...)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := pgParse(t, tc.structure, tc.catBase, tc.tagBase, false)
			if tc.custom != nil {
				// A taxonomy is registered too: it must add nothing.
				s = s.WithCustomTypes(tc.custom, pgTaxes)
			}
			pgAssertPatternSet(t, s.PaginationPatterns(), tc.want)
		})
	}

	// A registry with taxonomies only adds no pattern.
	t.Run("custom taxonomy adds nothing", func(t *testing.T) {
		plain := pgParse(t, pgPostName, "", "", false)
		withTax := plain.WithCustomTypes(nil, pgTaxes)
		pgAssertPatternSet(t, withTax.PaginationPatterns(), plain.PaginationPatterns())
	})
	// ArchivePatterns and CustomPatterns keep their exact contracts.
	t.Run("ArchivePatterns and CustomPatterns do not contain pagination patterns", func(t *testing.T) {
		s := pgParse(t, pgPostName, "", "", true)
		for _, p := range slices.Concat(s.ArchivePatterns(), s.CustomPatterns()) {
			if strings.Contains(p, "{pagenum}") {
				t.Errorf("pattern %q must come from PaginationPatterns only", p)
			}
		}
	})
}

// ---------------------------------------------------------------------------
// (g) Notes
// ---------------------------------------------------------------------------

// Requirement 9.6-9.8: a base resolving to "page" makes a term slugged by a page
// number unreachable, and the operator is told once, naming the option.
func TestNotesNamePageBase(t *testing.T) {
	for _, tc := range []struct {
		name, catBase, tagBase, option string
	}{
		{"category_base", pgPageBaseOpt, "", "category_base"},
		{"category_base normalized from slashes", "/page/", "", "category_base"},
		{"tag_base", "", pgPageBaseOpt, "tag_base"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := pgParse(t, pgPostName, tc.catBase, tc.tagBase, false)
			notes := s.Notes()
			if len(notes) != 1 {
				t.Fatalf("Notes() = %q, want exactly one note", notes)
			}
			if !strings.Contains(notes[0], tc.option) || !strings.Contains(notes[0], "/page/") {
				t.Errorf("note %q must name %s and the /page/{n} path", notes[0], tc.option)
			}
		})
	}
	t.Run("both bases add one note each", func(t *testing.T) {
		// The two bases also collide with each other, which is the existing note.
		s := pgParse(t, pgPostName, pgPageBaseOpt, pgPageBaseOpt, false)
		var cat, tag, collision int
		for _, n := range s.Notes() {
			switch {
			case strings.Contains(n, "both resolve"):
				collision++
			case strings.Contains(n, "category_base"):
				cat++
			case strings.Contains(n, "tag_base"):
				tag++
			}
		}
		if cat != 1 || tag != 1 || collision != 1 {
			t.Errorf("Notes() = %q, want one category_base, one tag_base and one collision note", s.Notes())
		}
	})
}

// Requirement 9.8: a custom entry slugged "page" adds exactly one CustomNotes entry.
func TestCustomNotesNamePageSlug(t *testing.T) {
	cases := []struct {
		name string
		typ  routing.TypeRoute
	}{
		{"single slug", routing.TypeRoute{Name: "doc", Slug: "page"}},
		{"archive slug", routing.TypeRoute{Name: "doc", Slug: "docs", ArchiveSlug: "page"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := pgParse(t, pgBlogNameTS, "", "", false)
			notes := s.WithCustomTypes([]routing.TypeRoute{tc.typ}, nil).CustomNotes()
			if len(notes) != 1 {
				t.Fatalf("CustomNotes() = %q, want exactly one note", notes)
			}
			if !strings.Contains(notes[0], "doc") || !strings.Contains(notes[0], "page") {
				t.Errorf("note %q must name the entry %q and its page slug", notes[0], "doc")
			}
		})
	}
}

// Every other configuration's notes are unchanged: no page note appears for a
// base that merely contains the word, and the pre-existing notes stay exactly
// one entry each.
func TestNotesUnchangedElsewhere(t *testing.T) {
	for _, tc := range []struct {
		name, catBase, tagBase string
		wantNotes              int
	}{
		{"defaults", "", "", 0},
		{"renamed bases", "sections", "topics", 0},
		{"base containing page", "pages", "pagelist", 0},
		{"base ending in page", "mypage", "", 0},
		{"base collision only", "same", "same", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := pgParse(t, pgPostName, tc.catBase, tc.tagBase, false)
			notes := s.Notes()
			if len(notes) != tc.wantNotes {
				t.Fatalf("Notes() = %q, want %d note(s)", notes, tc.wantNotes)
			}
			for _, n := range notes {
				if strings.Contains(n, "/page/{n}") {
					t.Errorf("unexpected page note: %q", n)
				}
			}
		})
	}
	t.Run("custom entries without a page slug", func(t *testing.T) {
		s := pgParse(t, pgBlogNameTS, "", "", false)
		got := s.WithCustomTypes([]routing.TypeRoute{
			{Name: "book", Slug: "book", ArchiveSlug: "books", WithFront: true},
			{Name: "doc", Slug: "pages", ArchiveSlug: "pagelist"},
		}, pgTaxes).CustomNotes()
		// The "book" archive /blog/books equals the /blog/%postname%/ post shape, so
		// the pre-existing shadow note fires; only a page note must be absent.
		for _, n := range got {
			if strings.Contains(n, "/page/{n}") {
				t.Errorf("unexpected page note: %q", n)
			}
		}
		if len(got) != 1 {
			t.Errorf("CustomNotes() = %q, want only the pre-existing shadow note", got)
		}
		if n := s.WithCustomTypes(pgTypes, pgTaxes).Notes(); !slices.Equal(n, s.Notes()) {
			t.Errorf("Notes() changed after WithCustomTypes: %q vs %q", n, s.Notes())
		}
	})
}
