package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"testing"

	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/storage"
)

// M20 task 7.3: RED assembly tests for the delete gap closure of PR 2 (Req 7-11,
// 13.1, 13.4; D4-D8). Every assertion goes through buildHandler as main wires it.
//
// Post subtests fail until task 7.4 passes content.WithPostRelationshipPurger to
// NewPostWriteService: the unmanaged-taxonomy (product_cat, post_format)
// relationships and their counts survive a delete today. Media subtests fail until
// task 6.2 extends MediaRepo.Delete: comments, the menu item, the _thumbnail_id
// reference and the relationship survive today. The failing-purger test swaps the
// port in through reflection, as M18/M19 did, so the file compiles before the
// PostRelationships field exists.

// relCountOf counts term_relationships rows of object id.
func relCountOf(t *testing.T, env customWriteEnv, id int64) int {
	t.Helper()
	return hardeningCount(t, env, "SELECT COUNT(*) FROM "+env.Prefix+"term_relationships WHERE object_id = ?", id)
}

// relCountOfTerm counts term_relationships rows that name term (term_taxonomy_id).
func relCountOfTerm(t *testing.T, env customWriteEnv, term int64) int {
	t.Helper()
	return hardeningCount(t, env, "SELECT COUNT(*) FROM "+env.Prefix+"term_relationships WHERE term_taxonomy_id = ?", term)
}

// hasRel reports whether the (object, term) relationship exists.
func hasRel(t *testing.T, env customWriteEnv, object, term int64) bool {
	t.Helper()
	return hardeningCount(t, env, "SELECT COUNT(*) FROM "+env.Prefix+"term_relationships WHERE object_id = ? AND term_taxonomy_id = ?", object, term) == 1
}

func setTermCount(t *testing.T, env customWriteEnv, term int64, n int) {
	t.Helper()
	if _, err := env.Repos.DB().ExecContext(context.Background(),
		"UPDATE "+env.Prefix+"term_taxonomy SET count = ? WHERE term_taxonomy_id = ?", n, term); err != nil {
		t.Fatalf("set count of %d: %v", term, err)
	}
}

// cleanupTerms is the term fixture shared by the post and media subtests.
type cleanupTerms struct {
	category, productCat, postFormat, linkCat int64
}

// seedCleanupTerms creates one term per taxonomy, each with a stale count of 2.
func seedCleanupTerms(t *testing.T, env customWriteEnv) cleanupTerms {
	t.Helper()
	c := cleanupTerms{
		category:   seedRawTerm(t, env, "CleanCat", "clean-cat", "category", 0),
		productCat: seedRawTerm(t, env, "CleanProduct", "clean-product", "product_cat", 0),
		postFormat: seedRawTerm(t, env, "CleanFormat", "clean-format", "post_format", 0),
		linkCat:    seedRawTerm(t, env, "CleanLinks", "clean-links", "link_category", 0),
	}
	for _, term := range []int64{c.category, c.productCat, c.postFormat, c.linkCat} {
		setTermCount(t, env, term, 2)
	}
	return c
}

func (c cleanupTerms) counted() []int64 { return []int64{c.category, c.productCat, c.postFormat} }

// failingRelPurger is a PostTermRelationshipPurger that always fails.
type failingRelPurger struct{}

func (failingRelPurger) PurgeTermRelationshipsOf(context.Context, int64) error {
	return errors.New("relationship purge unavailable")
}

// TestBuildHandlerDeleteCleanupPosts (Req 10.2, 11.1, 11.2, 11.5): a REST delete of a
// page, a post and a registered-type row removes every relationship of the deleted id
// (managed and unmanaged taxonomies), leaves another post's relationships and a
// link_category row with the same object_id, and recounts each affected term.
func TestBuildHandlerDeleteCleanupPosts(t *testing.T) {
	for _, c := range menuDeleteCases()[2:] { // the three REST cases
		t.Run(c.name, func(t *testing.T) {
			env := newCustomWriteHandler(t, true)
			terms := seedCleanupTerms(t, env)
			actor := c.actor(env)
			subject := createRESTRow(t, env, actor, c.createPath, "Subject")
			other := createRESTRow(t, env, actor, c.createPath, "Other")
			for _, term := range terms.counted() {
				seedRawTermRelationship(t, env, subject, term)
				seedRawTermRelationship(t, env, other, term)
			}
			// A link whose id equals the post id owns a link_category relationship.
			seedRawTermRelationship(t, env, subject, terms.linkCat)

			rec := doJSON(t, env.Handler, actor, http.MethodDelete, c.deletePath(subject), "")
			if rec.Code != http.StatusOK {
				t.Fatalf("DELETE %s: status = %d, want 200; body %s", c.deletePath(subject), rec.Code, rec.Body.String())
			}
			if rowExists(t, env, subject) {
				t.Errorf("row %d still exists", subject)
			}
			for _, term := range terms.counted() {
				if hasRel(t, env, subject, term) {
					t.Errorf("relationship (%d, term %d) survived the delete", subject, term)
				}
				if !hasRel(t, env, other, term) {
					t.Errorf("other post's relationship (%d, term %d) was removed", other, term)
				}
				if got, want := hardeningTermCount(t, env, term), relCountOfTerm(t, env, term); got != want {
					t.Errorf("term %d count = %d, want %d (remaining relationships)", term, got, want)
				}
			}
			if !hasRel(t, env, subject, terms.linkCat) {
				t.Errorf("link_category relationship with object_id %d was removed", subject)
			}
			if got := hardeningTermCount(t, env, terms.linkCat); got != 2 {
				t.Errorf("link_category count = %d, want 2 (untouched)", got)
			}
		})
	}
}

// mediaFixture is the scenario for the media subtests.
type mediaFixture struct {
	menu, att, otherAtt, child, post, otherPost int64
	item, itemChild, otherItem                  int64
	comments                                    []int64
	terms                                       cleanupTerms
}

// seedAttachment inserts an attachment row with the _wp_attached_file meta the
// media reader requires; the file itself does not exist (the unlink is non-fatal).
func seedAttachment(t *testing.T, env customWriteEnv, parent int64) int64 {
	t.Helper()
	id := seedRawPost(t, env, "attachment", "inherit", parent)
	seedRawMeta(t, env, id, "_wp_attached_file", fmt.Sprintf("2024/05/cleanup-%d.png", id))
	return id
}

func seedMediaFixture(t *testing.T, env customWriteEnv) mediaFixture {
	t.Helper()
	f := mediaFixture{}
	f.terms = seedCleanupTerms(t, env)
	f.menu = createMenu(t, env, "Media Menu", "media-menu")
	f.att = seedAttachment(t, env, 0)
	f.otherAtt = seedAttachment(t, env, 0)
	f.child = seedAttachment(t, env, f.att)
	seedRawMeta(t, env, f.att, "_wp_attachment_image_alt", "alt")
	f.comments = []int64{seedRawComment(t, env, f.att, "1"), seedRawComment(t, env, f.att, "0")}
	f.item = seedRawMenuItem(t, env, f.menu, "attachment", f.att)
	f.itemChild = customLink(t, env, f.menu, f.item, 1)
	f.otherItem = seedRawMenuItem(t, env, f.menu, "attachment", f.otherAtt)
	f.post = seedRawPost(t, env, "post", "publish", 0)
	f.otherPost = seedRawPost(t, env, "post", "publish", 0)
	seedRawMeta(t, env, f.post, "_thumbnail_id", fmt.Sprint(f.att))
	seedRawMeta(t, env, f.otherPost, "_thumbnail_id", fmt.Sprint(f.otherAtt))
	for _, obj := range []int64{f.att, f.otherAtt} {
		seedRawTermRelationship(t, env, obj, f.terms.productCat)
	}
	seedRawTermRelationship(t, env, f.att, f.terms.linkCat)
	return f
}

// TestBuildHandlerDeleteCleanupMedia (Req 7, 8, 9, 13.1, 13.4): an administrator's
// forced media delete removes the attachment's comments, commentmeta, menu item,
// featured-image reference and relationships, re-parents the item's child, and leaves
// another attachment's rows and a child attachment's post_parent alone.
func TestBuildHandlerDeleteCleanupMedia(t *testing.T) {
	env := newCustomWriteHandler(t, true)
	f := seedMediaFixture(t, env)

	rec := doJSON(t, env.Handler, &env.Admin, http.MethodDelete, fmt.Sprintf("/wp-json/wp/v2/media/%d?force=true", f.att), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE media: status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	if rowExists(t, env, f.att) {
		t.Errorf("attachment %d still exists", f.att)
	}
	if n := hardeningCount(t, env, "SELECT COUNT(*) FROM "+env.Prefix+"comments WHERE comment_post_ID = ?", f.att); n != 0 {
		t.Errorf("comments left for the attachment: %d, want 0", n)
	}
	for _, c := range f.comments {
		if n := hardeningCount(t, env, "SELECT COUNT(*) FROM "+env.Prefix+"commentmeta WHERE comment_id = ?", c); n != 0 {
			t.Errorf("commentmeta left for comment %d: %d, want 0", c, n)
		}
	}
	if rowExists(t, env, f.item) {
		t.Errorf("menu item %d of the attachment still exists", f.item)
	}
	if n := hardeningCount(t, env, "SELECT COUNT(*) FROM "+env.Prefix+"postmeta WHERE meta_key = '_thumbnail_id' AND meta_value = ?", fmt.Sprint(f.att)); n != 0 {
		t.Errorf("_thumbnail_id references to the attachment left: %d, want 0", n)
	}
	if n := hardeningCount(t, env, "SELECT COUNT(*) FROM "+env.Prefix+"postmeta WHERE post_id = ? AND meta_key = '_thumbnail_id'", f.otherPost); n != 1 {
		t.Errorf("other attachment's _thumbnail_id reference rows = %d, want 1", n)
	}
	if hasRel(t, env, f.att, f.terms.productCat) {
		t.Errorf("product_cat relationship of the attachment survived")
	}
	if got, want := hardeningTermCount(t, env, f.terms.productCat), relCountOfTerm(t, env, f.terms.productCat); got != want {
		t.Errorf("product_cat count = %d, want %d (remaining relationships)", got, want)
	}
	if !hasRel(t, env, f.otherAtt, f.terms.productCat) {
		t.Errorf("other attachment's relationship was removed")
	}
	if !hasRel(t, env, f.att, f.terms.linkCat) {
		t.Errorf("link_category relationship with object_id %d was removed", f.att)
	}
	if !rowExists(t, env, f.otherItem) {
		t.Errorf("another attachment's menu item was removed")
	}
	// The item's child is re-parented to the item's parent (top level) and shown.
	if got := metaValue(t, env, f.itemChild, "_menu_item_menu_item_parent"); got != "0" {
		t.Errorf("child item parent = %q, want \"0\"", got)
	}
	if ids := menuItemIDs(t, env, env.Handler, f.menu); !ids[f.itemChild] || ids[f.item] || !ids[f.otherItem] {
		t.Errorf("menu read lists %v, want the child and the other attachment's item and not %d", ids, f.item)
	}
	if !rowExists(t, env, f.child) || hardeningParent(t, env, f.child) != f.att {
		t.Errorf("child attachment %d lost its row or its post_parent (want %d)", f.child, f.att)
	}
}

// TestBuildHandlerDeleteCleanupPurgeFailure (Req 10.8): a failing relationship purger
// answers 500 on a post delete, the row and its relationships remain, and a retry with
// the working purger succeeds. The purger is swapped in through reflection so the
// test compiles before the PostRelationships port exists.
func TestBuildHandlerDeleteCleanupPurgeFailure(t *testing.T) {
	var original reflect.Value
	env := newCustomWriteHandlerWith(t, true, func(repos *storage.Repositories) {
		f := reflect.ValueOf(repos).Elem().FieldByName("PostRelationships")
		if !f.IsValid() {
			t.Fatalf("storage.Repositories has no PostRelationships field (design: PR 2 task 6.1)")
		}
		if !reflect.TypeOf(failingRelPurger{}).AssignableTo(f.Type()) {
			t.Fatalf("PostRelationships has type %s, which failingRelPurger does not implement", f.Type())
		}
		original = reflect.New(f.Type()).Elem()
		original.Set(f)
		f.Set(reflect.ValueOf(failingRelPurger{}))
	})
	terms := seedCleanupTerms(t, env)
	victim := seedRawPost(t, env, "post", "publish", 0)
	seedRawTermRelationship(t, env, victim, terms.productCat)
	seedRawTermRelationship(t, env, victim, terms.postFormat)
	path := fmt.Sprintf("/wp-json/wp/v2/posts/%d", victim)

	rec := doJSON(t, env.Handler, &env.Admin, http.MethodDelete, path, "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("DELETE with failing purger: status = %d, want 500; body %s", rec.Code, rec.Body.String())
	}
	if !rowExists(t, env, victim) {
		t.Fatalf("post row removed although the relationship purger failed")
	}
	if !hasRel(t, env, victim, terms.productCat) || !hasRel(t, env, victim, terms.postFormat) {
		t.Errorf("relationships removed although the relationship purger failed")
	}

	reflect.ValueOf(env.Repos).Elem().FieldByName("PostRelationships").Set(original)
	h, _ := assemblyHandler(t, env.Repos, config.DatabaseConfig{Vendor: "sqlite", TablePrefix: env.Prefix}, customWriteRegistry(t))
	rec = doJSON(t, h, &env.Admin, http.MethodDelete, path, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("retry DELETE: status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	if rowExists(t, env, victim) {
		t.Errorf("post row still exists after retry")
	}
	if n := relCountOf(t, env, victim); n != 0 {
		t.Errorf("relationships left after retry: %d, want 0", n)
	}
}

// TestBuildHandlerDeleteCleanupDenied (Req 7.4, 10.9 neighbors): a forbidden actor and an
// unknown media id answer 403 and 404 and change no row.
func TestBuildHandlerDeleteCleanupDenied(t *testing.T) {
	env := newCustomWriteHandler(t, true)
	f := seedMediaFixture(t, env)
	snap := func() string {
		return fmt.Sprintf("%s|%s|rels=%d|comments=%d|items=%v|thumb=%d",
			rowSnapshot(t, env, f.att), rowSnapshot(t, env, f.post), relCountOf(t, env, f.att),
			hardeningCount(t, env, "SELECT COUNT(*) FROM "+env.Prefix+"comments WHERE comment_post_ID = ?", f.att),
			menuItemIDs(t, env, env.Handler, f.menu),
			hardeningCount(t, env, "SELECT COUNT(*) FROM "+env.Prefix+"postmeta WHERE meta_key = '_thumbnail_id'"))
	}
	before := snap()
	victim := seedRawPost(t, env, "post", "publish", 0)
	seedRawTermRelationship(t, env, victim, f.terms.productCat)

	for _, tc := range []struct {
		name   string
		actor  *customWriteActor
		path   string
		status int
	}{
		{"contributor media", &env.Contrib, fmt.Sprintf("/wp-json/wp/v2/media/%d?force=true", f.att), http.StatusForbidden},
		{"unknown media", &env.Admin, "/wp-json/wp/v2/media/999999?force=true", http.StatusNotFound},
		{"contributor post", &env.Contrib, fmt.Sprintf("/wp-json/wp/v2/posts/%d", victim), http.StatusForbidden},
	} {
		if rec := doJSON(t, env.Handler, tc.actor, http.MethodDelete, tc.path, ""); rec.Code != tc.status {
			t.Errorf("%s: status = %d, want %d; body %s", tc.name, rec.Code, tc.status, rec.Body.String())
		}
	}
	if after := snap(); after != before {
		t.Errorf("rows changed by denied deletes\nbefore: %s\nafter:  %s", before, after)
	}
	if !rowExists(t, env, victim) || !hasRel(t, env, victim, f.terms.productCat) {
		t.Errorf("forbidden post delete changed the post or its relationships")
	}
}
