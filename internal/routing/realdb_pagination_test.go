package routing_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/routing"
	"github.com/roboweaver/grimoire/internal/storage"
)

// TestRealWordPressPagination validates M16's path-form pagination against the
// same restored, REAL WordPress database (read-only) the other realdb tests use,
// under the same single gate: GRIMOIRE_TEST_WP_DSN and GRIMOIRE_TEST_WP_PREFIX.
// No environment variable is added (Req 14.5).
//
//	GRIMOIRE_TEST_WP_DSN='wordpress:PASS@tcp(127.0.0.1:3306)/wordpress' \
//	GRIMOIRE_TEST_WP_PREFIX=accuweaver \
//	go test ./internal/routing/ -run TestRealWordPressPagination -v -count=1
//
// Paths are derived from the raw rows with string operations, not through the
// path constructors. For each one the test asserts that Classify returns the
// kind and entity it returned before M16 with Page == 0, that the path + "/page/2"
// classifies to the same kind and entity with Page == 2, and that PaginatedPath
// rebuilds that path.
//
// It then runs the shadow census of Requirement 3.4 over the real rows and fails
// with the URL list when any shadowed shape exists (design sign-off item 2: the
// shadowed URLs are documented, not fixed, provided the real data has none).
//
// Every statement issued is a SELECT.
func TestRealWordPressPagination(t *testing.T) {
	dsn := os.Getenv("GRIMOIRE_TEST_WP_DSN")
	if dsn == "" {
		t.Skip("set GRIMOIRE_TEST_WP_DSN to run the real WordPress pagination validation")
	}
	prefix := os.Getenv("GRIMOIRE_TEST_WP_PREFIX")
	if prefix == "" {
		prefix = "accuweaver"
	}
	if !regexp.MustCompile(`^[A-Za-z0-9_]+$`).MatchString(prefix) {
		t.Fatalf("GRIMOIRE_TEST_WP_PREFIX %q is not a plain table prefix", prefix)
	}
	ctx := context.Background()
	repos, err := storage.New(config.DatabaseConfig{Vendor: "mysql", DSN: dsn, TablePrefix: prefix})
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	defer repos.Close()

	structure, err := repos.Options.Get(ctx, "permalink_structure")
	if err != nil {
		t.Fatalf("read permalink_structure: %v", err)
	}
	rawCategoryBase, _ := repos.Options.Get(ctx, "category_base")
	rawTagBase, _ := repos.Options.Get(ctx, "tag_base")
	t.Logf("site permalink_structure = %q category_base = %q tag_base = %q",
		structure, rawCategoryBase, rawTagBase)
	s, parseErr := routing.Parse(structure, rawCategoryBase, rawTagBase)
	if parseErr != nil {
		t.Logf("structure %q is unsupported (%v); validating against the flat "+
			"fallback the server would use", structure, parseErr)
	}
	usable := parseErr == nil && strings.TrimSpace(structure) != ""
	front := realWPFront(structure, usable)
	trailingSlash := usable && strings.HasSuffix(strings.TrimSpace(structure), "/")

	catBaseSegs := realWPSegments(rawCategoryBase)
	catBase := strings.Join(catBaseSegs, "/")
	catFront := front
	if catBase != "" {
		catFront = nil
	} else {
		catBase = routing.DefaultCategoryBase
	}
	tagBaseSegs := realWPSegments(rawTagBase)
	tagBase := strings.Join(tagBaseSegs, "/")
	tagFront := front
	if tagBase != "" {
		tagFront = nil
	} else {
		tagBase = routing.DefaultTagBase
	}
	// A base equal to the structure's front keeps the post meaning (Req 4.4b),
	// so the category round trip does not apply there, as in the nested test.
	catRoundTrip := !(len(front) > 0 && (catBase == front[0] || catBase == strings.Join(front, "/")))

	// A path the walker derives, and the entity Classify must return for it.
	type subject struct {
		name  string
		path  string
		check func(routing.Target) string // "" when the entity matches
		kind  routing.Kind
	}
	var subjects []subject

	join := func(front []string, mid string, tail ...string) string {
		segs := append([]string(nil), front...)
		segs = append(segs, realWPSegments(mid)...)
		segs = append(segs, tail...)
		p := "/" + strings.Join(segs, "/")
		if trailingSlash {
			p += "/"
		}
		return p
	}

	// Categories.
	cats, err := repos.TermReader.ListByTaxonomy(ctx, "category")
	if err != nil {
		t.Fatalf("ListByTaxonomy(category): %v", err)
	}
	catByID := make(map[int64]domain.Term, len(cats))
	for _, c := range cats {
		catByID[c.ID] = c
	}
	if catRoundTrip {
		for _, c := range cats {
			ancestry, ok := realWPAncestry(catByID, c)
			if !ok {
				continue
			}
			want := slices.Clone(ancestry)
			subjects = append(subjects, subject{
				name: "category/" + strings.Join(ancestry, "-"),
				path: join(catFront, catBase, ancestry...),
				kind: routing.KindCategory,
				check: func(tg routing.Target) string {
					if !slices.Equal(tg.Segments, want) {
						return fmt.Sprintf("Segments = %v, want %v", tg.Segments, want)
					}
					return ""
				},
			})
		}
	} else {
		t.Logf("category base %q collides with front %q; category round trip not applicable",
			catBase, strings.Join(front, "/"))
	}

	// Tags.
	tags, err := repos.TermReader.ListByTaxonomy(ctx, "post_tag")
	if err != nil {
		t.Fatalf("ListByTaxonomy(post_tag): %v", err)
	}
	for _, tg := range tags {
		if !realWPPathToken(tg.Slug) {
			continue
		}
		slug := tg.Slug
		subjects = append(subjects, subject{
			name: "tag/" + slug,
			path: join(tagFront, tagBase, slug),
			kind: routing.KindTag,
			check: func(got routing.Target) string {
				if got.Slug != slug {
					return fmt.Sprintf("Slug = %q, want %q", got.Slug, slug)
				}
				return ""
			},
		})
	}

	// Authors, read raw: the repositories expose no list of every nicename.
	rows, err := repos.DB().QueryContext(ctx, "SELECT user_nicename FROM "+prefix+"users")
	if err != nil {
		t.Fatalf("select user_nicename: %v", err)
	}
	nicenames, err := scanStrings(rows)
	if err != nil {
		t.Fatalf("scan user_nicename: %v", err)
	}
	for _, n := range nicenames {
		if !realWPPathToken(n) {
			continue
		}
		nice := n
		subjects = append(subjects, subject{
			name: "author/" + nice,
			path: join(front, routing.AuthorBase, nice),
			kind: routing.KindAuthor,
			check: func(got routing.Target) string {
				if got.Slug != nice {
					return fmt.Sprintf("Slug = %q, want %q", got.Slug, nice)
				}
				return ""
			},
		})
	}

	// Date archives: the year and month of the 25 newest posts. A Flat structure
	// serves no date archives (Req 6.8), so there is nothing to derive.
	posts, err := repos.Posts.RecentPosts(ctx, 25, 0)
	if err != nil {
		t.Fatalf("RecentPosts: %v", err)
	}
	if usable && !s.Flat {
		dateFront := slices.Clone(front)
		if realWPDatePrefixed(structure) {
			dateFront = append(dateFront, "date")
		}
		seen := map[string]bool{}
		for _, p := range posts {
			y, m := p.Date.Year(), int(p.Date.Month())
			path := join(dateFront, "", fmt.Sprintf("%04d", y), fmt.Sprintf("%02d", m))
			if seen[path] {
				continue
			}
			seen[path] = true
			want := routing.DateRef{Year: y, Month: m}
			subjects = append(subjects, subject{
				name: "date/" + fmt.Sprintf("%04d-%02d", y, m),
				path: path,
				kind: routing.KindDate,
				check: func(got routing.Target) string {
					if got.Date != want {
						return fmt.Sprintf("Date = %+v, want %+v", got.Date, want)
					}
					return ""
				},
			})
		}
	} else {
		t.Logf("structure serves no date archives; date paths not checked")
	}

	// Nested pages, derived over post_parent with the helpers the nested-page
	// test uses.
	pages, err := realWPListAllPages(ctx, repos.PageReader)
	if err != nil {
		t.Fatalf("ListPages: %v", err)
	}
	pageByID := make(map[int64]domain.PageNode, len(pages))
	for _, p := range pages {
		pageByID[p.ID] = p
	}
	for _, p := range pages {
		if p.Status != "publish" {
			continue
		}
		ancestry, nested, pathSafe := realWPNestedPageCandidate(pageByID, p)
		if !pathSafe || !nested {
			continue
		}
		if collides, base := realWPPageFirstSegmentCollides(ancestry, s, front); collides {
			t.Logf("page %v begins with %q, claimed by an earlier row; skipped", ancestry, base)
			continue
		}
		want := slices.Clone(ancestry)
		subjects = append(subjects, subject{
			name: "page/" + strings.Join(ancestry, "-"),
			path: join(nil, "", ancestry...),
			kind: routing.KindPage,
			check: func(got routing.Target) string {
				if !slices.Equal(got.Segments, want) {
					return fmt.Sprintf("Segments = %v, want %v", got.Segments, want)
				}
				return ""
			},
		})
	}

	if len(subjects) == 0 {
		t.Skip("the target database yielded no archive or page path to check")
	}
	kinds := map[routing.Kind]int{}
	for _, sub := range subjects {
		kinds[sub.kind]++
		t.Run(sub.name, func(t *testing.T) {
			base := s.Classify(sub.path)
			if base.Kind != sub.kind {
				t.Fatalf("Classify(%q).Kind = %v, want %v", sub.path, base.Kind, sub.kind)
			}
			if base.Page != 0 {
				t.Errorf("Classify(%q).Page = %d, want 0", sub.path, base.Page)
			}
			if msg := sub.check(base); msg != "" {
				t.Fatalf("Classify(%q): %s", sub.path, msg)
			}
			paged := strings.TrimSuffix(sub.path, "/") + "/page/2"
			if trailingSlash {
				paged += "/"
			}
			if got := s.PaginatedPath(sub.path, 2); got != paged {
				t.Errorf("PaginatedPath(%q, 2) = %q, want %q", sub.path, got, paged)
			}
			p2 := s.Classify(paged)
			if p2.Kind != sub.kind {
				t.Fatalf("Classify(%q).Kind = %v, want %v", paged, p2.Kind, sub.kind)
			}
			if p2.Page != 2 {
				t.Errorf("Classify(%q).Page = %d, want 2", paged, p2.Page)
			}
			if msg := sub.check(p2); msg != "" {
				t.Errorf("Classify(%q): %s", paged, msg)
			}
		})
	}
	t.Logf("validated %d real paths and their page/2 forms: %d categories, %d tags, "+
		"%d authors, %d dates, %d pages", len(subjects), kinds[routing.KindCategory],
		kinds[routing.KindTag], kinds[routing.KindAuthor], kinds[routing.KindDate],
		kinds[routing.KindPage])

	// Shadow census (Req 3.4, design sign-off item 2).
	pageNum := regexp.MustCompile(`^[1-9][0-9]{0,8}$`)
	var shadows []string

	// 1. A term two levels below a parent slugged "page" whose slug is a page
	// number, under a real ancestor: /category/{anc}/page/{n}. The parent must
	// itself have a parent, otherwise the remainder is a bare base (Req 3.3).
	q := "SELECT c.slug, p.slug, ctt.taxonomy, ptt.parent FROM " + prefix + "term_taxonomy ctt" +
		" JOIN " + prefix + "terms c ON c.term_id = ctt.term_id" +
		" JOIN " + prefix + "term_taxonomy ptt ON ptt.term_id = ctt.parent AND ptt.taxonomy = ctt.taxonomy" +
		" JOIN " + prefix + "terms p ON p.term_id = ptt.term_id" +
		" WHERE ctt.parent <> 0 AND p.slug = 'page'"
	trows, err := repos.DB().QueryContext(ctx, q)
	if err != nil {
		t.Fatalf("shadow census (terms): %v", err)
	}
	termShadows := 0
	for trows.Next() {
		var child, parent, taxonomy string
		var grand int64
		if err := trows.Scan(&child, &parent, &taxonomy, &grand); err != nil {
			trows.Close()
			t.Fatalf("scan shadow census (terms): %v", err)
		}
		if grand != 0 && pageNum.MatchString(child) {
			termShadows++
			shadows = append(shadows, fmt.Sprintf("term %s: .../page/%s (grandparent term_id %d)", taxonomy, child, grand))
		}
	}
	if err := trows.Err(); err != nil {
		trows.Close()
		t.Fatalf("shadow census (terms): %v", err)
	}
	trows.Close()

	// 2. A top-level page slugged "page" with a child slugged by a page number:
	// /page/{n}.
	q = "SELECT c.post_name FROM " + prefix + "posts c JOIN " + prefix + "posts p ON p.ID = c.post_parent" +
		" WHERE c.post_type = 'page' AND p.post_type = 'page' AND p.post_name = 'page'" +
		" AND p.post_parent = 0 AND c.post_status NOT IN ('trash','auto-draft')"
	prows, err := repos.DB().QueryContext(ctx, q)
	if err != nil {
		t.Fatalf("shadow census (pages): %v", err)
	}
	children, err := scanStrings(prows)
	if err != nil {
		t.Fatalf("scan shadow census (pages): %v", err)
	}
	pageShadows := 0
	for _, c := range children {
		if pageNum.MatchString(c) {
			pageShadows++
			shadows = append(shadows, "page: /page/"+c)
		}
	}

	// 3. A post whose canonical path under the structure is exactly /page/{n}.
	q = "SELECT ID, post_name, YEAR(post_date), MONTH(post_date), DAY(post_date) FROM " + prefix + "posts" +
		" WHERE post_type = 'post' AND post_name = 'page' AND post_status NOT IN ('trash','auto-draft')"
	qrows, err := repos.DB().QueryContext(ctx, q)
	if err != nil {
		t.Fatalf("shadow census (posts): %v", err)
	}
	postShadows := 0
	for qrows.Next() {
		var id int64
		var slug string
		var y, m, d int
		if err := qrows.Scan(&id, &slug, &y, &m, &d); err != nil {
			qrows.Close()
			t.Fatalf("scan shadow census (posts): %v", err)
		}
		canon := s.Canonical(domain.Post{
			ID: id, Slug: slug, Type: "post",
			Date: time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC),
		})
		segs := realWPSegments(canon)
		if len(segs) == 2 && segs[0] == "page" && pageNum.MatchString(segs[1]) {
			postShadows++
			shadows = append(shadows, fmt.Sprintf("post %d: %s", id, canon))
		}
	}
	if err := qrows.Err(); err != nil {
		qrows.Close()
		t.Fatalf("shadow census (posts): %v", err)
	}
	qrows.Close()

	t.Logf("shadow census: %d shadowed terms, %d shadowed pages, %d shadowed posts (%d total)",
		termShadows, pageShadows, postShadows, len(shadows))
	if len(shadows) > 0 {
		t.Errorf("the real data has %d shadowed URL(s) that now mean pagination "+
			"(Req 3.4); stop and report before merging:\n  %s",
			len(shadows), strings.Join(shadows, "\n  "))
	}
}

// scanStrings drains a single-column result and closes it.
func scanStrings(rows *sql.Rows) ([]string, error) {
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// realWPDatePrefixed mirrors WordPress's rule, read off the raw option: date
// archives carry a "date" segment when %post_id% is among the structure's first
// three placeholders.
func realWPDatePrefixed(structure string) bool {
	index := 0
	for _, seg := range realWPSegments(structure) {
		if !(strings.HasPrefix(seg, "%") && strings.HasSuffix(seg, "%")) {
			continue
		}
		index++
		if index > 3 {
			return false
		}
		if seg == "%post_id%" {
			return true
		}
	}
	return false
}
