package storagetest

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/storage"
	"github.com/roboweaver/grimoire/internal/storage/rebind"
)

// Fixture ids for the media delete contract, far above SeedFixtures.
const (
	mdA     int64 = 9600 // attachment deleted
	mdB     int64 = 9601 // another attachment
	mdC     int64 = 9602 // child attachment, post_parent = A
	mdG     int64 = 9603 // page: not an attachment
	mdP1    int64 = 9604 // post with _thumbnail_id = A
	mdP2    int64 = 9605 // post with _thumbnail_id = B
	mdMI    int64 = 9606 // menu item pointing at A
	mdMIKid int64 = 9607 // custom child of MI
	mdMIB   int64 = 9608 // menu item pointing at B
	mdCm1   int64 = 9610 // approved comment on A
	mdCm2   int64 = 9611 // pending comment on A
	mdCmG   int64 = 9612 // comment on G

	mdTTCat  int64 = 9620 // category
	mdTTPC   int64 = 9621 // product_cat
	mdTTPF   int64 = 9622 // post_format
	mdTTLnk  int64 = 9623 // link_category
	mdTTMenu int64 = 9624 // nav_menu
)

// RunMediaDeleteContract covers MediaWriter.Delete: the attachment, its
// postmeta, relationships, comments and commentmeta are removed, a menu item
// pointing at it is removed with its children re-parented, _thumbnail_id
// references to it are removed, term counts are recomputed, and a link_category
// row sharing its id, child attachments and other posts are kept. A page id
// and an unknown id return domain.ErrNotFound with nothing changed (Req 8, 9,
// 12.3). Atomicity is covered on SQLite in
// internal/storage/wprepo/media_write_test.go.
//
// The vendor and table prefix come from newRepos; the contract holds no
// literal prefix and no vendor-specific placeholder (Req 5.1).
func RunMediaDeleteContract(t *testing.T, newRepos NewReposWithPrefixFunc) {
	t.Helper()
	ctx := context.Background()

	setup := func(t *testing.T) (db *sql.DB, vendor, prefix string, del func(id int64) error, cleanup func()) {
		t.Helper()
		var repos *storage.Repositories
		repos, vendor, prefix, cleanup = newRepos(t)
		db = repos.DB()
		seedMediaDelete(ctx, t, db, vendor, prefix)
		del = func(id int64) error { return repos.MediaWriter.Delete(ctx, id) }
		return db, vendor, prefix, del, cleanup
	}

	t.Run("removes the attachment with its meta, relationships and comments", func(t *testing.T) {
		db, vendor, prefix, del, cleanup := setup(t)
		defer cleanup()
		if err := del(mdA); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if n := miPostRows(ctx, t, db, vendor, prefix, mdA); n != 0 {
			t.Errorf("attachment rows = %d, want 0", n)
		}
		if n := miMetaRows(ctx, t, db, vendor, prefix, mdA); n != 0 {
			t.Errorf("attachment postmeta rows = %d, want 0", n)
		}
		for name, tt := range map[string]int64{"category": mdTTCat, "product_cat": mdTTPC, "post_format": mdTTPF} {
			if n := trRel(ctx, t, db, vendor, prefix, mdA, tt); n != 0 {
				t.Errorf("attachment relationship in %s = %d, want 0", name, n)
			}
		}
		if n := pdCommentCount(ctx, t, db, vendor, prefix, mdA); n != 0 {
			t.Errorf("comments of the attachment = %d, want 0 (every approval state)", n)
		}
		if n := pdCommentMetaCount(ctx, t, db, vendor, prefix, mdA); n != 0 {
			t.Errorf("commentmeta of the attachment's comments = %d, want 0", n)
		}
		if n := pdCount(ctx, t, db, vendor, `SELECT COUNT(*) FROM `+prefix+`commentmeta WHERE comment_id IN (?, ?)`, mdCm1, mdCm2); n != 0 {
			t.Errorf("commentmeta rows for the deleted comments = %d, want 0", n)
		}
	})

	t.Run("removes the menu item of the attachment and re-parents its child", func(t *testing.T) {
		db, vendor, prefix, del, cleanup := setup(t)
		defer cleanup()
		if err := del(mdA); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if n := miPostRows(ctx, t, db, vendor, prefix, mdMI); n != 0 {
			t.Errorf("MI post rows = %d, want 0", n)
		}
		if n := miMetaRows(ctx, t, db, vendor, prefix, mdMI); n != 0 {
			t.Errorf("MI postmeta rows = %d, want 0", n)
		}
		if n := miRelRows(ctx, t, db, vendor, prefix, mdMI); n != 0 {
			t.Errorf("MI term_relationships rows = %d, want 0", n)
		}
		if got := miMetaValue(ctx, t, db, vendor, prefix, mdMIKid, "_menu_item_menu_item_parent"); got != "0" {
			t.Errorf("MI child parent = %q, want \"0\" (MI's own parent)", got)
		}
		if n := miPostRows(ctx, t, db, vendor, prefix, mdMIB); n != 1 {
			t.Errorf("non-matching menu item rows = %d, want 1", n)
		}
		if n := miMetaRows(ctx, t, db, vendor, prefix, mdMIB); n != 4 {
			t.Errorf("non-matching menu item postmeta rows = %d, want 4", n)
		}
	})

	t.Run("removes _thumbnail_id references to the attachment only", func(t *testing.T) {
		db, vendor, prefix, del, cleanup := setup(t)
		defer cleanup()
		if err := del(mdA); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		thumb := func(post int64) int {
			return pdCount(ctx, t, db, vendor, `SELECT COUNT(*) FROM `+prefix+`postmeta WHERE post_id = ? AND meta_key = '_thumbnail_id'`, post)
		}
		if n := thumb(mdP1); n != 0 {
			t.Errorf("P1 _thumbnail_id rows = %d, want 0", n)
		}
		if n := thumb(mdP2); n != 1 {
			t.Errorf("P2 _thumbnail_id rows = %d, want 1 (points at B)", n)
		}
		for name, id := range map[string]int64{"P1": mdP1, "P2": mdP2} {
			if n := miPostRows(ctx, t, db, vendor, prefix, id); n != 1 {
				t.Errorf("%s post rows = %d, want 1", name, n)
			}
		}
	})

	t.Run("keeps child attachments, other attachments and the link_category row", func(t *testing.T) {
		db, vendor, prefix, del, cleanup := setup(t)
		defer cleanup()
		if err := del(mdA); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		for name, id := range map[string]int64{"B": mdB, "C": mdC, "G": mdG} {
			if n := miPostRows(ctx, t, db, vendor, prefix, id); n != 1 {
				t.Errorf("%s post rows = %d, want 1", name, n)
			}
		}
		if got := pdParent(ctx, t, db, vendor, prefix, mdC); got != mdA {
			t.Errorf("C post_parent = %d, want %d (unchanged)", got, mdA)
		}
		if n := trRel(ctx, t, db, vendor, prefix, mdA, mdTTLnk); n != 1 {
			t.Errorf("link_category relationship with object_id = A rows = %d, want 1", n)
		}
	})

	t.Run("recomputes the counts of the affected terms", func(t *testing.T) {
		db, vendor, prefix, del, cleanup := setup(t)
		defer cleanup()
		if err := del(mdA); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		for name, tt := range map[string]int64{"category": mdTTCat, "product_cat": mdTTPC, "post_format": mdTTPF, "nav_menu": mdTTMenu} {
			want := pdCount(ctx, t, db, vendor, `SELECT COUNT(*) FROM `+prefix+`term_relationships WHERE term_taxonomy_id = ?`, tt)
			if got := miCount(ctx, t, db, vendor, prefix, tt); got != want {
				t.Errorf("%s count = %d, want %d (recounted)", name, got, want)
			}
		}
		if got := miCount(ctx, t, db, vendor, prefix, mdTTLnk); got != prStale {
			t.Errorf("link_category count = %d, want %d (unchanged)", got, prStale)
		}
	})

	for name, id := range map[string]int64{"a page id": mdG, "an unknown id": 999999} {
		id := id
		t.Run("Delete of "+name+" returns ErrNotFound and changes nothing", func(t *testing.T) {
			db, vendor, prefix, del, cleanup := setup(t)
			defer cleanup()
			before := mdSnapshot(ctx, t, db, vendor, prefix)
			if err := del(id); !errors.Is(err, domain.ErrNotFound) {
				t.Fatalf("Delete(%d) = %v, want domain.ErrNotFound", id, err)
			}
			if after := mdSnapshot(ctx, t, db, vendor, prefix); !equalInts(before, after) {
				t.Errorf("row snapshot after refused delete = %v, want %v", after, before)
			}
			if n := miPostRows(ctx, t, db, vendor, prefix, mdG); n != 1 {
				t.Errorf("G post rows = %d, want 1", n)
			}
			if n := miMetaRows(ctx, t, db, vendor, prefix, mdG); n != 2 {
				t.Errorf("G postmeta rows = %d, want 2", n)
			}
			if n := pdCommentCount(ctx, t, db, vendor, prefix, mdG); n != 1 {
				t.Errorf("G comments = %d, want 1", n)
			}
		})
	}
}

// mdSnapshot counts every table the delete may touch plus the stale term
// counts, so a refused delete that changed anything shows as a difference.
func mdSnapshot(ctx context.Context, t *testing.T, db *sql.DB, vendor, prefix string) []int {
	t.Helper()
	out := []int{}
	for _, tbl := range []string{"posts", "postmeta", "comments", "commentmeta", "term_relationships", "terms", "term_taxonomy"} {
		out = append(out, pdCount(ctx, t, db, vendor, `SELECT COUNT(*) FROM `+prefix+tbl))
	}
	out = append(out, pdCount(ctx, t, db, vendor, `SELECT COALESCE(SUM(count), 0) FROM `+prefix+`term_taxonomy`))
	return out
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// seedMediaDelete inserts the fixture of design.md "Media delete / Contract".
func seedMediaDelete(ctx context.Context, t *testing.T, db *sql.DB, vendor, prefix string) {
	t.Helper()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, rebind.Rebind(vendor, q), args...); err != nil {
			t.Fatalf("seed %q: %v", q, err)
		}
	}
	post := func(id int64, slug, ptype string, parent int64, mime string) {
		t.Helper()
		exec(postInsert(vendor, prefix), postArgs(id, slug, "T "+slug, ptype, "publish", "2000-01-01 00:00:00", "open", parent, mime, 0)...)
	}
	meta := func(postID int64, key, value string) {
		t.Helper()
		exec(`INSERT INTO `+prefix+`postmeta (post_id, meta_key, meta_value) VALUES (?, ?, ?)`, postID, key, value)
	}
	rel := func(obj, tt int64) {
		t.Helper()
		exec(`INSERT INTO `+prefix+`term_relationships (object_id, term_taxonomy_id, term_order) VALUES (?, ?, ?)`, obj, tt, 0)
	}
	id := itoa64

	post(mdA, "md-a", "attachment", 0, "image/png")
	post(mdB, "md-b", "attachment", 0, "image/png")
	post(mdC, "md-c", "attachment", mdA, "image/png")
	post(mdG, "md-g", "page", 0, "")
	post(mdP1, "md-p1", "post", 0, "")
	post(mdP2, "md-p2", "post", 0, "")
	meta(mdA, "_wp_attached_file", "2024/05/md-a.png")
	meta(mdA, "_wp_attachment_image_alt", "alt")
	meta(mdG, "md_g_a", "v")
	meta(mdG, "md_g_b", "v")
	meta(mdP1, "_thumbnail_id", id(mdA))
	meta(mdP2, "_thumbnail_id", id(mdB))

	for _, x := range []struct {
		tt       int64
		taxonomy string
	}{
		{mdTTCat, "category"}, {mdTTPC, "product_cat"}, {mdTTPF, "post_format"},
		{mdTTLnk, "link_category"}, {mdTTMenu, "nav_menu"},
	} {
		seedTermTaxonomy(ctx, t, db, vendor, prefix, x.tt, x.taxonomy, prStale)
	}
	rel(mdA, mdTTCat)
	rel(mdA, mdTTPC)
	rel(mdA, mdTTPF)
	rel(mdA, mdTTLnk) // link_category: object_id equals A, must stay
	rel(mdP1, mdTTCat)

	comment := func(cid, postID int64, approved string) {
		t.Helper()
		exec(commentInsert(vendor, prefix), cid, postID, "A", "a@example.test", "", "", "2000-01-01 00:00:00", "2000-01-01 00:00:00", "c", approved, "", 0, 0)
		exec(`INSERT INTO `+prefix+`commentmeta (comment_id, meta_key, meta_value) VALUES (?, ?, ?)`, cid, "md_key", "md_val")
	}
	comment(mdCm1, mdA, "1")
	comment(mdCm2, mdA, "0")
	comment(mdCmG, mdG, "1")

	item := func(itemID int64, typ, object string, objectID int64, parent string) {
		t.Helper()
		post(itemID, "md-item-"+id(itemID), "nav_menu_item", 0, "")
		meta(itemID, "_menu_item_type", typ)
		meta(itemID, "_menu_item_object", object)
		meta(itemID, "_menu_item_object_id", id(objectID))
		meta(itemID, "_menu_item_menu_item_parent", parent)
		rel(itemID, mdTTMenu)
	}
	item(mdMI, "post_type", "attachment", mdA, "0")
	item(mdMIKid, "custom", "custom", 0, id(mdMI))
	item(mdMIB, "post_type", "attachment", mdB, "0")
}
