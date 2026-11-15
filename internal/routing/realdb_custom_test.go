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

	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/registry"
	"github.com/roboweaver/grimoire/internal/routing"
	"github.com/roboweaver/grimoire/internal/storage"
)

// TestRealWordPressCustomTypes validates the custom-type half of the router
// (M12) against the same restored, REAL WordPress database (read-only) the
// permalink, nested-category and nested-page checks run on, under the same
// single gate: GRIMOIRE_TEST_WP_DSN and GRIMOIRE_TEST_WP_PREFIX enable it, and
// no new variable is introduced (Req 14.6; design D10).
//
//	GRIMOIRE_TEST_WP_DSN='wordpress:PASS@tcp(127.0.0.1:3306)/wordpress?parseTime=true' \
//	GRIMOIRE_TEST_WP_PREFIX=accuweaver \
//	go test ./internal/routing/ -run TestRealWordPressCustomTypes -v -count=1
//
// The test DISCOVERS the first non-reserved post_type and the first
// non-reserved taxonomy in the database and declares each as a
// routing.TypeRoute / routing.TaxRoute (package routing cannot import
// registry's Registry, so the routes are built by hand; with_front is false and
// the base is the type name). Hierarchy is taken from the raw
// post_parent / term_taxonomy.parent census.
//
// # Expectations are derived, not borrowed
//
// Each expected path is assembled here by walking the raw parent column and
// joining slugs with plain string operations, never through
// content.PageHierarchy or content.TermHierarchy. The path is then round-tripped
// through Classify (Kind, TypeName, Segments) and compared to the Structure's
// own constructor, so a constructor and a classifier that disagreed would
// surface here rather than as a redirect loop in production.
//
// # Read-only
//
// storage.New opens the pool without migrating and every statement is a SELECT.
func TestRealWordPressCustomTypes(t *testing.T) {
	dsn := os.Getenv("GRIMOIRE_TEST_WP_DSN")
	if dsn == "" {
		t.Skip("set GRIMOIRE_TEST_WP_DSN to run the real WordPress custom-type validation")
	}
	prefix := os.Getenv("GRIMOIRE_TEST_WP_PREFIX")
	if prefix == "" {
		prefix = "accuweaver"
	}
	if !regexp.MustCompile(`^[A-Za-z0-9_]*$`).MatchString(prefix) {
		t.Fatalf("GRIMOIRE_TEST_WP_PREFIX %q is not a plain identifier fragment", prefix)
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
	categoryBase, _ := repos.Options.Get(ctx, "category_base")
	tagBase, _ := repos.Options.Get(ctx, "tag_base")
	t.Logf("site permalink_structure = %q", structure)
	base, parseErr := routing.Parse(structure, categoryBase, tagBase)
	if parseErr != nil {
		t.Logf("structure %q is unsupported (%v); validating against the flat "+
			"fallback the server would use", structure, parseErr)
	}
	front := realWPPageFront(structure, parseErr == nil && strings.TrimSpace(structure) != "")

	db := repos.DB()
	postType, posts, err := realWPFirstCustomPostType(ctx, db, prefix)
	if err != nil {
		t.Fatalf("discover custom post type: %v", err)
	}
	if postType == "" {
		t.Skip("the target database has no non-reserved post_type with rows; the " +
			"custom-type assertions need at least one custom post type (CPT) in " +
			prefix + "posts")
	}
	taxName, terms, err := realWPFirstCustomTaxonomy(ctx, db, prefix)
	if err != nil {
		t.Fatalf("discover custom taxonomy: %v", err)
	}
	if taxName == "" {
		t.Skip("the target database has no non-reserved taxonomy with rows; the " +
			"custom-type assertions need at least one custom taxonomy (CT) in " +
			prefix + "term_taxonomy")
	}

	postHier := false
	postByID := make(map[int64]realWPCustomNode, len(posts))
	for _, p := range posts {
		postByID[p.ID] = p
	}
	for _, p := range posts {
		if _, ok := postByID[p.ParentID]; p.ParentID != 0 && ok {
			postHier = true
		}
	}
	termHier := false
	termByID := make(map[int64]realWPCustomNode, len(terms))
	for _, tm := range terms {
		termByID[tm.ID] = tm
	}
	for _, tm := range terms {
		if _, ok := termByID[tm.ParentID]; tm.ParentID != 0 && ok {
			termHier = true
		}
	}
	t.Logf("custom post type %q: %d rows, hierarchical=%v; custom taxonomy %q: "+
		"%d terms, hierarchical=%v", postType, len(posts), postHier, taxName,
		len(terms), termHier)

	s := base.WithCustomTypes(
		[]routing.TypeRoute{{Name: postType, Slug: postType, ArchiveSlug: postType, Hierarchical: postHier}},
		[]routing.TaxRoute{{Name: taxName, Slug: taxName, Hierarchical: termHier}},
	)

	if collides, owner := realWPCustomBaseCollides(postType, s, front); collides {
		t.Skipf("custom post type %q collides with %q, which an earlier Classify "+
			"row claims on this site; the round trip does not apply", postType, owner)
	}
	if collides, owner := realWPCustomBaseCollides(taxName, s, front); collides {
		t.Skipf("custom taxonomy %q collides with %q, which an earlier Classify "+
			"row claims on this site; the round trip does not apply", taxName, owner)
	}

	trail := ""
	if s.TrailingSlash {
		trail = "/"
	}

	// The CPT archive: the bare base, round-tripped to KindCustomArchive.
	t.Run("archive/"+postType, func(t *testing.T) {
		want := "/" + postType + trail
		if got := s.CustomArchivePath(postType); got != want {
			t.Fatalf("CustomArchivePath(%q) = %q, want %q", postType, got, want)
		}
		target := s.Classify(want)
		if target.Kind != routing.KindCustomArchive || target.TypeName != postType {
			t.Fatalf("Classify(%q) = {Kind:%v TypeName:%q}, want KindCustomArchive %q",
				want, target.Kind, target.TypeName, postType)
		}
	})

	checkedPosts := 0
	for _, p := range posts {
		if p.Status != "publish" {
			continue // only a published row serves a 200
		}
		ancestry, ok := realWPCustomAncestry(postByID, p)
		if !ok {
			t.Logf("skipping %s %d (%q): a slug in its ancestry is not a plain "+
				"URL token", postType, p.ID, p.Slug)
			continue
		}
		if !postHier && len(ancestry) > 1 {
			continue // cannot happen: postHier is false only with no resolvable parent
		}
		checkedPosts++
		t.Run(fmt.Sprintf("post/%s/%d", postType, p.ID), func(t *testing.T) {
			want := "/" + postType + "/" + strings.Join(ancestry, "/") + trail
			if got := s.CustomPostPath(postType, ancestry); got != want {
				t.Fatalf("CustomPostPath(%q, %v) = %q, want %q (derived from post_parent)",
					postType, ancestry, got, want)
			}
			target := s.Classify(want)
			if target.Kind != routing.KindCustomPost || target.TypeName != postType {
				t.Fatalf("Classify(%q) = {Kind:%v TypeName:%q}, want KindCustomPost %q",
					want, target.Kind, target.TypeName, postType)
			}
			if !slices.Equal(target.Segments, ancestry) {
				t.Fatalf("Classify(%q).Segments = %v, want %v", want, target.Segments, ancestry)
			}
			if target.TrailingSlash != s.TrailingSlash {
				t.Errorf("Classify(%q).TrailingSlash = %v, want %v",
					want, target.TrailingSlash, s.TrailingSlash)
			}
		})
	}

	checkedTerms := 0
	for _, tm := range terms {
		ancestry, ok := realWPCustomAncestry(termByID, tm)
		if !ok {
			t.Logf("skipping %s term %d (%q): a slug in its ancestry is not a "+
				"plain URL token", taxName, tm.ID, tm.Slug)
			continue
		}
		checkedTerms++
		t.Run(fmt.Sprintf("term/%s/%d", taxName, tm.ID), func(t *testing.T) {
			want := "/" + taxName + "/" + strings.Join(ancestry, "/") + trail
			if got := s.CustomTermPath(taxName, ancestry); got != want {
				t.Fatalf("CustomTermPath(%q, %v) = %q, want %q (derived from "+
					"term_taxonomy.parent)", taxName, ancestry, got, want)
			}
			target := s.Classify(want)
			if target.Kind != routing.KindCustomTerm || target.TypeName != taxName {
				t.Fatalf("Classify(%q) = {Kind:%v TypeName:%q}, want KindCustomTerm %q",
					want, target.Kind, target.TypeName, taxName)
			}
			if !slices.Equal(target.Segments, ancestry) {
				t.Fatalf("Classify(%q).Segments = %v, want %v", want, target.Segments, ancestry)
			}
			if target.TrailingSlash != s.TrailingSlash {
				t.Errorf("Classify(%q).TrailingSlash = %v, want %v",
					want, target.TrailingSlash, s.TrailingSlash)
			}
		})
	}

	// A check that asserted nothing about a claim is worse than a skip.
	if checkedPosts == 0 {
		t.Skipf("custom post type %q has no PUBLISHED row whose whole ancestry is a "+
			"plain URL token ([A-Za-z0-9_-]); the single-path assertions need one", postType)
	}
	if checkedTerms == 0 {
		t.Skipf("custom taxonomy %q has no term whose whole ancestry is a plain URL "+
			"token ([A-Za-z0-9_-]); the term-path assertions need one", taxName)
	}
	t.Logf("validated %d %q single paths and %d %q term paths against structure %q",
		checkedPosts, postType, checkedTerms, taxName, structure)
}

// realWPCustomNode is one raw row of a custom post type (ID, post_name,
// post_parent, post_status) or custom taxonomy term (term_id, slug,
// term_taxonomy.parent; Status is always "publish").
type realWPCustomNode struct {
	ID, ParentID int64
	Slug, Status string
}

// realWPCustomAncestry walks a node up its parent column over the raw rows and
// returns its slugs root-first, ending in its own. An absent parent ends the
// walk (orphan rule) and an already-visited node ends it instead of hanging
// (cycle rule). It returns false when any slug cannot sit in a request path
// unescaped. It never calls the implementation's hierarchy types.
func realWPCustomAncestry(byID map[int64]realWPCustomNode, n realWPCustomNode) ([]string, bool) {
	var reversed []string
	visited := map[int64]bool{}
	for cur := n; ; {
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

// realWPCustomBaseCollides reports whether a custom base (a single segment, no
// front) is claimed by an earlier Classify row: an archive base or the
// structure's front.
func realWPCustomBaseCollides(name string, s routing.Structure, front []string) (bool, string) {
	for _, seq := range s.ReservedBases() {
		if len(seq) > 0 && seq[0] == name {
			return true, name
		}
	}
	if len(front) > 0 && front[0] == name {
		return true, name
	}
	return false, ""
}

// realWPFirstCustomPostType returns the alphabetically first post_type that is
// neither one of the registry's reserved names nor a WordPress-internal wp_*
// type, with all its rows (any status). It returns "" when there is none.
func realWPFirstCustomPostType(ctx context.Context, db *sql.DB, prefix string) (string, []realWPCustomNode, error) {
	reserved := registry.ReservedPostTypes()
	rows, err := db.QueryContext(ctx,
		"SELECT DISTINCT post_type FROM `"+prefix+"posts` ORDER BY post_type")
	if err != nil {
		return "", nil, err
	}
	var name string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return "", nil, err
		}
		if name == "" && !slices.Contains(reserved, v) && !strings.HasPrefix(v, "wp_") && realWPPathToken(v) {
			name = v
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return "", nil, err
	}
	rows.Close()
	if name == "" {
		return "", nil, nil
	}
	rs, err := db.QueryContext(ctx,
		"SELECT ID, post_name, post_parent, post_status FROM `"+prefix+
			"posts` WHERE post_type = ? ORDER BY ID", name)
	if err != nil {
		return "", nil, err
	}
	defer rs.Close()
	var out []realWPCustomNode
	for rs.Next() {
		var n realWPCustomNode
		if err := rs.Scan(&n.ID, &n.Slug, &n.ParentID, &n.Status); err != nil {
			return "", nil, err
		}
		out = append(out, n)
	}
	return name, out, rs.Err()
}

// realWPFirstCustomTaxonomy returns the alphabetically first non-reserved
// taxonomy with its terms. Parent is a term_id (term_taxonomy.parent), so nodes
// are keyed by term_id. It returns "" when there is none.
func realWPFirstCustomTaxonomy(ctx context.Context, db *sql.DB, prefix string) (string, []realWPCustomNode, error) {
	reserved := registry.ReservedTaxonomies()
	rows, err := db.QueryContext(ctx,
		"SELECT DISTINCT taxonomy FROM `"+prefix+"term_taxonomy` ORDER BY taxonomy")
	if err != nil {
		return "", nil, err
	}
	var name string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return "", nil, err
		}
		if name == "" && !slices.Contains(reserved, v) && !strings.HasPrefix(v, "wp_") && realWPPathToken(v) {
			name = v
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return "", nil, err
	}
	rows.Close()
	if name == "" {
		return "", nil, nil
	}
	rs, err := db.QueryContext(ctx,
		"SELECT t.term_id, t.slug, tt.parent FROM `"+prefix+"term_taxonomy` tt JOIN `"+
			prefix+"terms` t ON t.term_id = tt.term_id WHERE tt.taxonomy = ? ORDER BY t.term_id", name)
	if err != nil {
		return "", nil, err
	}
	defer rs.Close()
	var out []realWPCustomNode
	for rs.Next() {
		n := realWPCustomNode{Status: "publish"}
		if err := rs.Scan(&n.ID, &n.Slug, &n.ParentID); err != nil {
			return "", nil, err
		}
		out = append(out, n)
	}
	return name, out, rs.Err()
}
