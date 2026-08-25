package content

import (
	"context"
	"testing"

	"github.com/roboweaver/grimoire/internal/domain"
)

// --- RevisionWriteService.Snapshot (task 2.1) --------------------------------

// TestRevisionSnapshotCreatesRevisionFromPreUpdateState asserts Snapshot
// creates a (non-autosave) revision row from the given pre-update post and
// actor ID, with no pruning when no max is configured (Req 1.1-1.3).
func TestRevisionSnapshotCreatesRevisionFromPreUpdateState(t *testing.T) {
	rw := &fakeRevisionWriter{}
	svc := NewRevisionWriteService(rw, -1) // unset/unlimited

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
	svc := NewRevisionWriteService(rw, 3)

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
	svc := NewRevisionWriteService(rw, 0)

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
	svc := NewRevisionWriteService(rw, -1)

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
