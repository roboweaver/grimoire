package storagetest

import (
	"context"
	"database/sql"
	"testing"
)

// newSeededSQLite builds a migrated SQLite database carrying both fixture sets,
// so the nested chain is exercised the way contract cases will use it: layered
// on top of SeedFixtures rather than instead of it.
func newSeededSQLite(t *testing.T) *sql.DB {
	t.Helper()
	repos, cleanup := newSQLiteRepos(t, SeedNestedCategories)
	t.Cleanup(cleanup)
	return repos.DB()
}

// TestSeedNestedCategoriesChain reads the seeded rows back with plain SQL rather
// than through a repository, because the repositories do not expose
// term_taxonomy.parent yet -- the point of this fixture is to give that read
// something other than 0 to return once it does.
func TestSeedNestedCategoriesChain(t *testing.T) {
	ctx := context.Background()
	db := newSeededSQLite(t)

	cases := []struct {
		termID     int64
		slug       string
		name       string
		wantParent int64
	}{
		{NestedParentTermID, NestedParentTermSlug, NestedParentTermName, 0},
		{NestedChildTermID, NestedChildTermSlug, NestedChildTermName, NestedParentTermID},
		{NestedGrandchildTermID, NestedGrandchildTermSlug, NestedGrandchildTermName, NestedChildTermID},
	}
	for _, c := range cases {
		var (
			slug, name, taxonomy string
			parent               int64
		)
		err := db.QueryRowContext(ctx,
			`SELECT t.slug, t.name, tt.taxonomy, tt.parent
			   FROM wp_terms t JOIN wp_term_taxonomy tt ON tt.term_id = t.term_id
			  WHERE t.term_id = ?`, c.termID).Scan(&slug, &name, &taxonomy, &parent)
		if err != nil {
			t.Fatalf("term %d: %v", c.termID, err)
		}
		if slug != c.slug || name != c.name {
			t.Errorf("term %d = (%q, %q), want (%q, %q)", c.termID, slug, name, c.slug, c.name)
		}
		if taxonomy != "category" {
			t.Errorf("term %d taxonomy = %q, want category", c.termID, taxonomy)
		}
		if parent != c.wantParent {
			t.Errorf("term %d parent = %d, want %d", c.termID, parent, c.wantParent)
		}
	}
}

// TestSeedNestedCategoriesPosts pins one published post per level and its
// relationship, so a descendant-inclusive read of the parent can be told apart
// from one that returns only the parent's own post.
func TestSeedNestedCategoriesPosts(t *testing.T) {
	ctx := context.Background()
	db := newSeededSQLite(t)

	cases := []struct {
		postID int64
		slug   string
		date   string
		termID int64
	}{
		{NestedParentPostID, NestedParentPostSlug, "2024-05-15 00:00:00", NestedParentTermID},
		{NestedChildPostID, NestedChildPostSlug, "2024-05-16 00:00:00", NestedChildTermID},
		{NestedGrandchildPostID, NestedGrandchildPostSlug, "2024-05-17 00:00:00", NestedGrandchildTermID},
	}
	for _, c := range cases {
		var slug, status, ptype, date string
		var termID int64
		err := db.QueryRowContext(ctx,
			`SELECT p.post_name, p.post_status, p.post_type, p.post_date, tt.term_id
			   FROM wp_posts p
			   JOIN wp_term_relationships tr ON tr.object_id = p."ID"
			   JOIN wp_term_taxonomy tt ON tt.term_taxonomy_id = tr.term_taxonomy_id
			  WHERE p."ID" = ?`, c.postID).Scan(&slug, &status, &ptype, &date, &termID)
		if err != nil {
			t.Fatalf("post %d: %v", c.postID, err)
		}
		if slug != c.slug {
			t.Errorf("post %d slug = %q, want %q", c.postID, slug, c.slug)
		}
		if status != "publish" || ptype != "post" {
			t.Errorf("post %d = (%q, %q), want (publish, post)", c.postID, status, ptype)
		}
		if date != c.date {
			t.Errorf("post %d date = %q, want %q", c.postID, date, c.date)
		}
		if termID != c.termID {
			t.Errorf("post %d category term = %d, want %d", c.postID, termID, c.termID)
		}
	}
}

// TestSeedNestedCategoriesIsAdditive records that the chain is layered on the
// existing fixtures and collides with none of their ids -- the property that
// lets every other contract case keep the absolute counts it was written
// against, simply by not calling this helper.
func TestSeedNestedCategoriesIsAdditive(t *testing.T) {
	ctx := context.Background()
	db := newSeededSQLite(t)

	var cats int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM wp_term_taxonomy WHERE taxonomy = 'category'`).Scan(&cats); err != nil {
		t.Fatalf("count categories: %v", err)
	}
	if cats != 6 {
		t.Errorf("category terms = %d, want 6 (3 from SeedFixtures + 3 nested)", cats)
	}

	// The nested ids must sit past everything SeedFixtures pins, so a writer
	// running after both seeds still generates keys clear of them.
	var maxPostID, maxTermID, maxTaxID int64
	if err := db.QueryRowContext(ctx, `SELECT MAX("ID") FROM wp_posts`).Scan(&maxPostID); err != nil {
		t.Fatalf("max post id: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT MAX(term_id) FROM wp_terms`).Scan(&maxTermID); err != nil {
		t.Fatalf("max term id: %v", err)
	}
	if err := db.QueryRowContext(ctx,
		`SELECT MAX(term_taxonomy_id) FROM wp_term_taxonomy`).Scan(&maxTaxID); err != nil {
		t.Fatalf("max term_taxonomy id: %v", err)
	}
	if maxPostID != NestedGrandchildPostID {
		t.Errorf("max post id = %d, want %d (the nested band must be the highest)",
			maxPostID, NestedGrandchildPostID)
	}
	if maxTermID != NestedGrandchildTermID {
		t.Errorf("max term id = %d, want %d", maxTermID, NestedGrandchildTermID)
	}
	if maxTaxID != 62 {
		t.Errorf("max term_taxonomy id = %d, want 62", maxTaxID)
	}
}
