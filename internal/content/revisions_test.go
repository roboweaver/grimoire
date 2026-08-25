package content

import (
	"context"
	"errors"
	"testing"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/domain"
)

// --- RevisionWriteService.Snapshot (task 2.1) --------------------------------

// TestRevisionSnapshotCreatesRevisionFromPreUpdateState asserts Snapshot
// creates a (non-autosave) revision row from the given pre-update post and
// actor ID, with no pruning when no max is configured (Req 1.1-1.3).
func TestRevisionSnapshotCreatesRevisionFromPreUpdateState(t *testing.T) {
	rw := &fakeRevisionWriter{}
	svc := NewRevisionWriteService(rw, nil, -1) // unset/unlimited

	cur := domain.Post{ID: 9, Title: "Before", Content: "Old content", Excerpt: "Old excerpt"}
	if err := svc.Snapshot(context.Background(), cur, 5); err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	if len(rw.createRevisionCalls) != 1 {
		t.Fatalf("CreateRevision calls = %d, want 1", len(rw.createRevisionCalls))
	}
	call := rw.createRevisionCalls[0]
	if call.parentID != 9 {
		t.Errorf("parentID = %d, want 9", call.parentID)
	}
	if call.authorID != 5 {
		t.Errorf("authorID = %d, want 5", call.authorID)
	}
	if call.autosave {
		t.Error("autosave = true, want false for a manual-save snapshot")
	}
	if call.snapshot.Title != "Before" || call.snapshot.Content != "Old content" || call.snapshot.Excerpt != "Old excerpt" {
		t.Errorf("snapshot = %+v, want the pre-update cur", call.snapshot)
	}
}

// TestRevisionSnapshotPrunesOldestWhenMaxExceeded asserts that when a
// positive maxPerPost is configured, Snapshot creates the new revision and
// then prunes via PruneRevisions(ctx, parentID, maxPerPost) so the count
// never exceeds the configured max (Req 5.1-5.2).
func TestRevisionSnapshotPrunesOldestWhenMaxExceeded(t *testing.T) {
	rw := &fakeRevisionWriter{}
	svc := NewRevisionWriteService(rw, nil, 3)

	cur := domain.Post{ID: 9}
	if err := svc.Snapshot(context.Background(), cur, 5); err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	if len(rw.pruneRevisionsCalls) != 1 {
		t.Fatalf("PruneRevisions calls = %d, want 1", len(rw.pruneRevisionsCalls))
	}
	prune := rw.pruneRevisionsCalls[0]
	if prune.parentID != 9 || prune.keep != 3 {
		t.Errorf("prune call = %+v, want {parentID:9 keep:3}", prune)
	}
}

// TestRevisionSnapshotZeroMaxDisablesRevisioning asserts that a maxPerPost of
// exactly 0 disables revisioning entirely for future saves: Snapshot must not
// call CreateRevision (or PruneRevisions) at all (Req 5.3).
func TestRevisionSnapshotZeroMaxDisablesRevisioning(t *testing.T) {
	rw := &fakeRevisionWriter{}
	svc := NewRevisionWriteService(rw, nil, 0)

	if err := svc.Snapshot(context.Background(), domain.Post{ID: 9}, 5); err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	if len(rw.createRevisionCalls) != 0 {
		t.Errorf("CreateRevision calls = %d, want 0 when maxPerPost=0", len(rw.createRevisionCalls))
	}
	if len(rw.pruneRevisionsCalls) != 0 {
		t.Errorf("PruneRevisions calls = %d, want 0 when maxPerPost=0", len(rw.pruneRevisionsCalls))
	}
}

// TestRevisionSnapshotNegativeMaxNeverPrunes asserts that an unset/negative
// maxPerPost still creates the revision but never prunes (unlimited
// retention).
func TestRevisionSnapshotNegativeMaxNeverPrunes(t *testing.T) {
	rw := &fakeRevisionWriter{}
	svc := NewRevisionWriteService(rw, nil, -1)

	if err := svc.Snapshot(context.Background(), domain.Post{ID: 9}, 5); err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	if len(rw.createRevisionCalls) != 1 {
		t.Errorf("CreateRevision calls = %d, want 1", len(rw.createRevisionCalls))
	}
	if len(rw.pruneRevisionsCalls) != 0 {
		t.Errorf("PruneRevisions calls = %d, want 0 when maxPerPost is negative/unset", len(rw.pruneRevisionsCalls))
	}
}

// --- RevisionWriteService.List/Get/Restore (task 2.2) -----------------------
//
// Req 2.5: List/Get/Restore must never leak whether a permission failure or a
// missing/mismatched record caused the rejection -- both collapse to the same
// domain.ErrNotFound so internal/web maps them uniformly to 404.

// TestRevisionListReturnsRevisionsWhenAuthorized asserts List loads the
// parent post for the auth check and, once authorized, returns whatever
// ListRevisions reports.
func TestRevisionListReturnsRevisionsWhenAuthorized(t *testing.T) {
	posts := &fakePostWriter{store: map[int64]domain.Post{
		9: {ID: 9, Type: "post", Status: "draft", Author: 5},
	}}
	want := []domain.RevisionMeta{{ID: 10, Author: 5}}
	rw := &fakeRevisionWriter{listRevisions: want}
	svc := NewRevisionWriteService(rw, posts, -1)

	got, err := svc.List(context.Background(), actor(auth.RoleAuthor, 5), 9)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].ID != 10 {
		t.Errorf("List() = %+v, want %+v", got, want)
	}
}

// TestRevisionListReturnsNotFoundWhenParentMissing asserts a missing parent
// post yields domain.ErrNotFound (existence is not leaked to unauthenticated
// callers via a different error shape).
func TestRevisionListReturnsNotFoundWhenParentMissing(t *testing.T) {
	posts := &fakePostWriter{store: map[int64]domain.Post{}}
	rw := &fakeRevisionWriter{}
	svc := NewRevisionWriteService(rw, posts, -1)

	_, err := svc.List(context.Background(), actor(auth.RoleAuthor, 5), 9)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("List() err = %v, want domain.ErrNotFound", err)
	}
}

// TestRevisionListReturnsNotFoundWhenUnauthorized asserts that failing
// auth.CanEditPost against the parent post returns the SAME domain.ErrNotFound
// as a missing parent -- never a distinct forbidden error (Req 2.5).
func TestRevisionListReturnsNotFoundWhenUnauthorized(t *testing.T) {
	posts := &fakePostWriter{store: map[int64]domain.Post{
		9: {ID: 9, Type: "post", Status: "publish", Author: 5},
	}}
	rw := &fakeRevisionWriter{}
	svc := NewRevisionWriteService(rw, posts, -1)

	// Contributor may edit their own drafts but not another author's
	// published post.
	_, err := svc.List(context.Background(), actor(auth.RoleContributor, 999), 9)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("List() err = %v, want domain.ErrNotFound", err)
	}
}

// TestRevisionGetReturnsRevisionWhenAuthorized asserts Get returns the full
// revision snapshot once the parent-post auth check passes.
func TestRevisionGetReturnsRevisionWhenAuthorized(t *testing.T) {
	posts := &fakePostWriter{store: map[int64]domain.Post{
		9: {ID: 9, Type: "post", Status: "draft", Author: 5},
	}}
	want := domain.Post{ID: 10, ParentID: 9, Title: "Old title"}
	rw := &fakeRevisionWriter{revisionByID: want}
	svc := NewRevisionWriteService(rw, posts, -1)

	got, err := svc.Get(context.Background(), actor(auth.RoleAuthor, 5), 9, 10)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Title != "Old title" {
		t.Errorf("Get() = %+v, want %+v", got, want)
	}
}

// TestRevisionGetReturnsNotFoundWhenUnauthorized mirrors the List case.
func TestRevisionGetReturnsNotFoundWhenUnauthorized(t *testing.T) {
	posts := &fakePostWriter{store: map[int64]domain.Post{
		9: {ID: 9, Type: "post", Status: "publish", Author: 5},
	}}
	rw := &fakeRevisionWriter{revisionByID: domain.Post{ID: 10, ParentID: 9}}
	svc := NewRevisionWriteService(rw, posts, -1)

	_, err := svc.Get(context.Background(), actor(auth.RoleContributor, 999), 9, 10)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Get() err = %v, want domain.ErrNotFound", err)
	}
}

// TestRevisionGetReturnsNotFoundWhenRevisionBelongsToDifferentParent asserts
// that a revisionId which does not belong to the given parentId returns
// domain.ErrNotFound -- indistinguishable from "no such revision" (Req 2.5).
func TestRevisionGetReturnsNotFoundWhenRevisionBelongsToDifferentParent(t *testing.T) {
	posts := &fakePostWriter{store: map[int64]domain.Post{
		9: {ID: 9, Type: "post", Status: "draft", Author: 5},
	}}
	// revision 10 actually belongs to parent 77, not 9.
	rw := &fakeRevisionWriter{revisionByID: domain.Post{ID: 10, ParentID: 77}}
	svc := NewRevisionWriteService(rw, posts, -1)

	_, err := svc.Get(context.Background(), actor(auth.RoleAuthor, 5), 9, 10)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Get() err = %v, want domain.ErrNotFound", err)
	}
}

// TestRevisionRestoreSnapshotsCurrentStateBeforeApplyingRevision asserts
// Restore (a) snapshots the post's current pre-restore state as a new
// revision via Snapshot -- reusing Phase 1's create+prune logic rather than
// duplicating it -- and (b) only after that persists the named revision's
// title/content/excerpt onto the post (Req 2.3).
func TestRevisionRestoreSnapshotsCurrentStateBeforeApplyingRevision(t *testing.T) {
	posts := &fakePostWriter{store: map[int64]domain.Post{
		9: {ID: 9, Type: "post", Status: "draft", Author: 5, Title: "Current title", Content: "Current content", Excerpt: "Current excerpt"},
	}}
	rw := &fakeRevisionWriter{
		revisionByID: domain.Post{ID: 10, ParentID: 9, Title: "Old title", Content: "Old content", Excerpt: "Old excerpt"},
	}
	svc := NewRevisionWriteService(rw, posts, 3)

	got, err := svc.Restore(context.Background(), actor(auth.RoleAuthor, 5), 9, 10)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}

	// (a) the CURRENT state was snapshotted before any mutation.
	if len(rw.createRevisionCalls) != 1 {
		t.Fatalf("CreateRevision calls = %d, want 1", len(rw.createRevisionCalls))
	}
	snap := rw.createRevisionCalls[0]
	if snap.snapshot.Title != "Current title" || snap.snapshot.Content != "Current content" || snap.snapshot.Excerpt != "Current excerpt" {
		t.Errorf("pre-restore snapshot = %+v, want the CURRENT post state", snap.snapshot)
	}
	if snap.authorID != 5 {
		t.Errorf("pre-restore snapshot authorID = %d, want 5 (the restoring actor)", snap.authorID)
	}
	// Snapshot's own pruning logic (task 2.1) is reused, not duplicated.
	if len(rw.pruneRevisionsCalls) != 1 {
		t.Errorf("PruneRevisions calls = %d, want 1 (via Snapshot's existing maxPerPost logic)", len(rw.pruneRevisionsCalls))
	}

	// (b) the named revision's fields were applied and persisted.
	if posts.updated == nil {
		t.Fatal("posts.Update was never called")
	}
	if posts.updated.Title != "Old title" || posts.updated.Content != "Old content" || posts.updated.Excerpt != "Old excerpt" {
		t.Errorf("persisted post = %+v, want the restored revision's fields", posts.updated)
	}
	if got.Title != "Old title" {
		t.Errorf("Restore() returned = %+v, want the restored post", got)
	}
}

// TestRevisionRestoreReturnsNotFoundWhenUnauthorized mirrors List/Get.
func TestRevisionRestoreReturnsNotFoundWhenUnauthorized(t *testing.T) {
	posts := &fakePostWriter{store: map[int64]domain.Post{
		9: {ID: 9, Type: "post", Status: "publish", Author: 5},
	}}
	rw := &fakeRevisionWriter{revisionByID: domain.Post{ID: 10, ParentID: 9}}
	svc := NewRevisionWriteService(rw, posts, -1)

	_, err := svc.Restore(context.Background(), actor(auth.RoleContributor, 999), 9, 10)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Restore() err = %v, want domain.ErrNotFound", err)
	}
	if len(rw.createRevisionCalls) != 0 {
		t.Error("Restore must not snapshot/mutate anything when unauthorized")
	}
	if posts.updated != nil {
		t.Error("Restore must not persist anything when unauthorized")
	}
}

// TestRevisionRestoreReturnsNotFoundWhenRevisionBelongsToDifferentParent
// mirrors the Get case: a revisionId that isn't a revision of parentId is
// indistinguishable from "no such revision" (Req 2.5).
func TestRevisionRestoreReturnsNotFoundWhenRevisionBelongsToDifferentParent(t *testing.T) {
	posts := &fakePostWriter{store: map[int64]domain.Post{
		9: {ID: 9, Type: "post", Status: "draft", Author: 5},
	}}
	rw := &fakeRevisionWriter{revisionByID: domain.Post{ID: 10, ParentID: 77}}
	svc := NewRevisionWriteService(rw, posts, -1)

	_, err := svc.Restore(context.Background(), actor(auth.RoleAuthor, 5), 9, 10)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Restore() err = %v, want domain.ErrNotFound", err)
	}
	if len(rw.createRevisionCalls) != 0 {
		t.Error("Restore must not snapshot/mutate anything on a parent mismatch")
	}
}
