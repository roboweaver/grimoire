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

// M18 task 13.1: RED assembly tests for post delete cleanup (Req 7-10, 17.2,
// 17.3). Every assertion goes through buildHandler as main wires it. They fail
// until task 13.2 passes content.WithPostDependentsPurger(repos.PostDependents)
// to NewPostWriteService: term rows are detached already (WithTermDetacher is
// wired), but postmeta, comments, commentmeta and the parent re-point are not.

// hardeningCount runs a COUNT(*) query and returns the result.
func hardeningCount(t *testing.T, env customWriteEnv, query string, args ...any) int {
	t.Helper()
	var n int
	if err := env.Repos.DB().QueryRowContext(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return n
}

// hardeningParent returns post_parent of row id.
func hardeningParent(t *testing.T, env customWriteEnv, id int64) int64 {
	t.Helper()
	var p int64
	if err := env.Repos.DB().QueryRowContext(context.Background(),
		"SELECT post_parent FROM "+env.Prefix+"posts WHERE "+postsIDColumn+" = ?", id).Scan(&p); err != nil {
		t.Fatalf("post_parent of %d: %v", id, err)
	}
	return p
}

// hardeningTermCount returns term_taxonomy.count of term id.
func hardeningTermCount(t *testing.T, env customWriteEnv, id int64) int {
	t.Helper()
	return hardeningCount(t, env, "SELECT count FROM "+env.Prefix+"term_taxonomy WHERE term_taxonomy_id = ?", id)
}

// hardeningDeps is the dependent rows seeded for one post.
type hardeningDeps struct {
	comments []int64
}

// seedDependents gives postID three postmeta rows and one comment in each of
// the four states, each with commentmeta.
func seedDependents(t *testing.T, env customWriteEnv, postID int64) hardeningDeps {
	t.Helper()
	for i := 0; i < 3; i++ {
		seedRawMeta(t, env, postID, fmt.Sprintf("m18_key_%d", i), "v")
	}
	var d hardeningDeps
	for _, state := range []string{"1", "0", "spam", "trash"} {
		d.comments = append(d.comments, seedRawComment(t, env, postID, state))
	}
	return d
}

// assertNoDependents fails when any postmeta, comment or commentmeta of postID
// (or of its comments) remains.
func assertNoDependents(t *testing.T, env customWriteEnv, postID int64, d hardeningDeps) {
	t.Helper()
	if n := hardeningCount(t, env, "SELECT COUNT(*) FROM "+env.Prefix+"postmeta WHERE post_id = ?", postID); n != 0 {
		t.Errorf("postmeta rows left for %d: %d, want 0", postID, n)
	}
	if n := hardeningCount(t, env, "SELECT COUNT(*) FROM "+env.Prefix+"comments WHERE comment_post_ID = ?", postID); n != 0 {
		t.Errorf("comments left for %d: %d, want 0", postID, n)
	}
	for _, c := range d.comments {
		if n := hardeningCount(t, env, "SELECT COUNT(*) FROM "+env.Prefix+"commentmeta WHERE comment_id = ?", c); n != 0 {
			t.Errorf("commentmeta left for comment %d: %d, want 0", c, n)
		}
	}
}

// assertDependents fails unless postID still has its 3 postmeta rows and 4
// comments (each with one commentmeta row).
func assertDependents(t *testing.T, env customWriteEnv, postID int64, d hardeningDeps) {
	t.Helper()
	if n := hardeningCount(t, env, "SELECT COUNT(*) FROM "+env.Prefix+"postmeta WHERE post_id = ?", postID); n != 3 {
		t.Errorf("postmeta rows for %d: %d, want 3", postID, n)
	}
	if n := hardeningCount(t, env, "SELECT COUNT(*) FROM "+env.Prefix+"comments WHERE comment_post_ID = ?", postID); n != 4 {
		t.Errorf("comments for %d: %d, want 4", postID, n)
	}
	for _, c := range d.comments {
		if n := hardeningCount(t, env, "SELECT COUNT(*) FROM "+env.Prefix+"commentmeta WHERE comment_id = ?", c); n != 1 {
			t.Errorf("commentmeta for comment %d: %d, want 1", c, n)
		}
	}
}

// hardeningDeleteSurface is one way to delete a row: the request path and the
// status a successful delete answers.
type hardeningDeleteSurface struct {
	name string
	path func(rest string, id int64) string
	want int
}

var hardeningDeleteSurfaces = []hardeningDeleteSurface{
	{"admin", func(_ string, id int64) string { return fmt.Sprintf("/admin/api/posts/%d", id) }, http.StatusNoContent},
	{"rest", func(rest string, id int64) string { return fmt.Sprintf("/wp-json/wp/v2/%s/%d", rest, id) }, http.StatusOK},
}

// TestBuildHandlerHardeningDeletePost (Req 7.1, 7.4, 8, 9.2): deleting a built-in
// post removes its term rows, corrects the counts, removes postmeta, comments
// and commentmeta, re-points its attachment, and leaves a second post alone.
func TestBuildHandlerHardeningDeletePost(t *testing.T) {
	for _, s := range hardeningDeleteSurfaces {
		t.Run(s.name, func(t *testing.T) {
			env := newCustomWriteHandler(t, true)
			holder := seedRawPost(t, env, "page", "publish", 0)
			cat := hardeningSeedTerm(t, env, "Cat", "cat", "category")
			tag := hardeningSeedTerm(t, env, "Tag", "tag", "post_tag")

			victim := seedRawPost(t, env, "post", "publish", holder)
			other := seedRawPost(t, env, "post", "publish", holder)
			attachment := seedRawPost(t, env, "attachment", "inherit", victim)
			for _, id := range []int64{victim, other} {
				seedRawTermRelationship(t, env, id, cat)
				seedRawTermRelationship(t, env, id, tag)
			}
			for _, term := range []int64{cat, tag} {
				if _, err := env.Repos.DB().ExecContext(context.Background(),
					"UPDATE "+env.Prefix+"term_taxonomy SET count = 2 WHERE term_taxonomy_id = ?", term); err != nil {
					t.Fatalf("seed count: %v", err)
				}
			}
			vd := seedDependents(t, env, victim)
			od := seedDependents(t, env, other)

			rec := doJSON(t, env.Handler, &env.Admin, http.MethodDelete, s.path("posts", victim), "")
			if rec.Code != s.want {
				t.Fatalf("DELETE: status = %d, want %d; body %s", rec.Code, s.want, rec.Body.String())
			}

			if rowSnapshot(t, env, victim) != "<missing>" {
				t.Errorf("post row %d still exists", victim)
			}
			if n := hardeningCountRelationships(t, env, victim); n != 0 {
				t.Errorf("term_relationships left for %d: %d, want 0", victim, n)
			}
			for _, term := range []int64{cat, tag} {
				if got := hardeningTermCount(t, env, term); got != 1 {
					t.Errorf("term %d count = %d, want 1", term, got)
				}
			}
			assertNoDependents(t, env, victim, vd)
			if got := hardeningParent(t, env, attachment); got != holder {
				t.Errorf("attachment post_parent = %d, want %d (the deleted post's parent)", got, holder)
			}

			// The second post is untouched.
			if n := hardeningCountRelationships(t, env, other); n != 2 {
				t.Errorf("other post term_relationships = %d, want 2", n)
			}
			assertDependents(t, env, other, od)
		})
	}
}

// TestBuildHandlerHardeningDeletePage (Req 9.1, 9.3): deleting a page re-points
// its child pages to the page's parent; a child post keeps its parent.
func TestBuildHandlerHardeningDeletePage(t *testing.T) {
	for _, s := range hardeningDeleteSurfaces {
		t.Run(s.name, func(t *testing.T) {
			env := newCustomWriteHandler(t, true)
			top := seedRawPost(t, env, "page", "publish", 0)
			victim := seedRawPost(t, env, "page", "publish", top)
			child1 := seedRawPost(t, env, "page", "publish", victim)
			child2 := seedRawPost(t, env, "page", "draft", victim)
			childPost := seedRawPost(t, env, "post", "publish", victim)
			vd := seedDependents(t, env, victim)

			rec := doJSON(t, env.Handler, &env.Admin, http.MethodDelete, s.path("pages", victim), "")
			if rec.Code != s.want {
				t.Fatalf("DELETE: status = %d, want %d; body %s", rec.Code, s.want, rec.Body.String())
			}
			if rowSnapshot(t, env, victim) != "<missing>" {
				t.Errorf("page row %d still exists", victim)
			}
			assertNoDependents(t, env, victim, vd)
			for _, c := range []int64{child1, child2} {
				if got := hardeningParent(t, env, c); got != top {
					t.Errorf("child page %d post_parent = %d, want %d", c, got, top)
				}
			}
			if got := hardeningParent(t, env, childPost); got != victim {
				t.Errorf("child post post_parent = %d, want %d (unchanged)", got, victim)
			}
		})
	}
}

// TestBuildHandlerHardeningDeleteGuide (Req 9.1): the same re-point for a
// registered hierarchical type.
func TestBuildHandlerHardeningDeleteGuide(t *testing.T) {
	for _, s := range hardeningDeleteSurfaces {
		t.Run(s.name, func(t *testing.T) {
			env := newCustomWriteHandler(t, true)
			top := seedRawPost(t, env, "guide", "publish", 0)
			victim := seedRawPost(t, env, "guide", "publish", top)
			child := seedRawPost(t, env, "guide", "publish", victim)
			attachment := seedRawPost(t, env, "attachment", "inherit", victim)
			vd := seedDependents(t, env, victim)

			rec := doJSON(t, env.Handler, &env.Curator, http.MethodDelete, s.path("guide", victim), "")
			if rec.Code != s.want {
				t.Fatalf("DELETE: status = %d, want %d; body %s", rec.Code, s.want, rec.Body.String())
			}
			if rowSnapshot(t, env, victim) != "<missing>" {
				t.Errorf("guide row %d still exists", victim)
			}
			assertNoDependents(t, env, victim, vd)
			if got := hardeningParent(t, env, child); got != top {
				t.Errorf("child guide post_parent = %d, want %d", got, top)
			}
			if got := hardeningParent(t, env, attachment); got != top {
				t.Errorf("attachment post_parent = %d, want %d", got, top)
			}
		})
	}
}

// failingPurger is a PostDependentsPurger that always fails.
type failingPurger struct{}

func (failingPurger) PurgePostDependents(context.Context, int64, int64, string) error {
	return errors.New("purge unavailable")
}

// TestBuildHandlerHardeningDeletePurgeFailure (Req 10.2, 10.3): a failing purger
// answers 500 and leaves the row, and a retry with the working purger succeeds.
// The purger is swapped in through reflection so the test compiles before the
// PostDependents port exists; it fails with a clear message until it does.
func TestBuildHandlerHardeningDeletePurgeFailure(t *testing.T) {
	var original reflect.Value
	env := newCustomWriteHandlerWith(t, true, func(repos *storage.Repositories) {
		f := reflect.ValueOf(repos).Elem().FieldByName("PostDependents")
		if !f.IsValid() {
			t.Fatalf("storage.Repositories has no PostDependents field (design: PR 2 phase 8)")
		}
		if !reflect.TypeOf(failingPurger{}).AssignableTo(f.Type()) {
			t.Fatalf("PostDependents has type %s, which failingPurger does not implement", f.Type())
		}
		original = reflect.New(f.Type()).Elem()
		original.Set(f)
		f.Set(reflect.ValueOf(failingPurger{}))
	})

	victim := seedRawPost(t, env, "post", "publish", 0)
	deps := seedDependents(t, env, victim)
	path := fmt.Sprintf("/admin/api/posts/%d", victim)

	rec := doJSON(t, env.Handler, &env.Admin, http.MethodDelete, path, "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("DELETE with failing purger: status = %d, want 500; body %s", rec.Code, rec.Body.String())
	}
	if rowSnapshot(t, env, victim) == "<missing>" {
		t.Fatalf("post row removed although the purger failed")
	}

	// Retry through a handler built over the same database with the working purger.
	reflect.ValueOf(env.Repos).Elem().FieldByName("PostDependents").Set(original)
	h, _ := assemblyHandler(t, env.Repos, config.DatabaseConfig{Vendor: "sqlite", TablePrefix: env.Prefix}, customWriteRegistry(t))
	rec = doJSON(t, h, &env.Admin, http.MethodDelete, path, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("retry DELETE: status = %d, want 204; body %s", rec.Code, rec.Body.String())
	}
	if rowSnapshot(t, env, victim) != "<missing>" {
		t.Errorf("post row still exists after retry")
	}
	assertNoDependents(t, env, victim, deps)
}
