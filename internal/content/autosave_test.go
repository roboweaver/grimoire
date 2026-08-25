package content

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/domain"
)

// --- content.AutosaveService (task 2.4) --------------------------------------
//
// AutosaveService.Save upserts a single autosave row per (post, author) via
// domain.RevisionWriter's AutosaveFor/UpdateAutosave/CreateRevision(autosave
// = true) -- and never touches the parent post's own row (Req 3.2-3.3).
// AutosaveService.Newer surfaces "is there an autosave newer than the post
// itself" for the editor's restore prompt (Req 3.4).

func TestAutosaveSaveCreatesRevisionWhenNoneExistsForAuthor(t *testing.T) {
	posts := &fakePostWriter{store: map[int64]domain.Post{
		5: {ID: 5, Type: "post", Status: "draft", Author: 9},
	}}
	rw := &fakeRevisionWriter{autosaveFound: false, nextRevisionID: 77}
	svc := NewAutosaveService(rw, posts)

	fields := AutosaveFields{Title: "T", Content: "C", Excerpt: "E"}
	if _, err := svc.Save(context.Background(), actor(auth.RoleAuthor, 9), 5, fields); err != nil {
		t.Fatalf("Save() error = %v, want nil", err)
	}

	if len(rw.createRevisionCalls) != 1 {
		t.Fatalf("CreateRevision calls = %d, want 1", len(rw.createRevisionCalls))
	}
	got := rw.createRevisionCalls[0]
	if got.parentID != 5 || got.authorID != 9 || !got.autosave {
		t.Fatalf("CreateRevision call = %+v, want parentID=5 authorID=9 autosave=true", got)
	}
	if got.snapshot.Title != "T" || got.snapshot.Content != "C" || got.snapshot.Excerpt != "E" {
		t.Fatalf("CreateRevision snapshot = %+v, want fields applied", got.snapshot)
	}
	if rw.lastUpdateAutosave.revisionID != 0 {
		t.Fatalf("UpdateAutosave was called (revisionID=%d), want CreateRevision path only", rw.lastUpdateAutosave.revisionID)
	}
	if posts.created != nil || posts.updated != nil {
		t.Fatalf("autosave touched the parent post's own row: created=%v updated=%v", posts.created, posts.updated)
	}
}

func TestAutosaveSaveUpdatesExistingAutosaveRowInPlace(t *testing.T) {
	posts := &fakePostWriter{store: map[int64]domain.Post{
		5: {ID: 5, Type: "post", Status: "draft", Author: 9},
	}}
	rw := &fakeRevisionWriter{
		autosaveFound: true,
		autosavePost:  domain.Post{ID: 100, ParentID: 5, Author: 9},
	}
	svc := NewAutosaveService(rw, posts)

	fields := AutosaveFields{Title: "T2", Content: "C2", Excerpt: "E2"}
	if _, err := svc.Save(context.Background(), actor(auth.RoleAuthor, 9), 5, fields); err != nil {
		t.Fatalf("Save() error = %v, want nil", err)
	}

	if len(rw.createRevisionCalls) != 0 {
		t.Fatalf("CreateRevision calls = %d, want 0 (existing autosave should be updated, not duplicated)", len(rw.createRevisionCalls))
	}
	if rw.lastUpdateAutosave.revisionID != 100 {
		t.Fatalf("UpdateAutosave revisionID = %d, want 100", rw.lastUpdateAutosave.revisionID)
	}
	if rw.lastUpdateAutosave.snapshot.Title != "T2" || rw.lastUpdateAutosave.snapshot.Content != "C2" || rw.lastUpdateAutosave.snapshot.Excerpt != "E2" {
		t.Fatalf("UpdateAutosave snapshot = %+v, want fields applied", rw.lastUpdateAutosave.snapshot)
	}
	if posts.created != nil || posts.updated != nil {
		t.Fatalf("autosave touched the parent post's own row: created=%v updated=%v", posts.created, posts.updated)
	}
}

func TestAutosaveSaveReturnsNotFoundWhenParentMissing(t *testing.T) {
	posts := &fakePostWriter{store: map[int64]domain.Post{}}
	rw := &fakeRevisionWriter{}
	svc := NewAutosaveService(rw, posts)

	_, err := svc.Save(context.Background(), actor(auth.RoleAuthor, 9), 5, AutosaveFields{})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Save() error = %v, want domain.ErrNotFound", err)
	}
	if len(rw.createRevisionCalls) != 0 || rw.lastUpdateAutosave.revisionID != 0 {
		t.Fatalf("autosave storage touched despite missing parent")
	}
}

func TestAutosaveSaveReturnsNotFoundWhenUnauthorized(t *testing.T) {
	posts := &fakePostWriter{store: map[int64]domain.Post{
		5: {ID: 5, Type: "post", Status: "draft", Author: 1},
	}}
	rw := &fakeRevisionWriter{}
	svc := NewAutosaveService(rw, posts)

	// A different, non-editor author may not autosave someone else's draft.
	_, err := svc.Save(context.Background(), actor(auth.RoleContributor, 9), 5, AutosaveFields{})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Save() error = %v, want domain.ErrNotFound", err)
	}
	if len(rw.createRevisionCalls) != 0 || rw.lastUpdateAutosave.revisionID != 0 {
		t.Fatalf("autosave storage touched despite unauthorized actor")
	}
}

func TestAutosaveNewerReturnsFalseWhenNoAutosaveExists(t *testing.T) {
	posts := &fakePostWriter{store: map[int64]domain.Post{
		5: {ID: 5, Type: "post", Status: "draft", Author: 9},
	}}
	rw := &fakeRevisionWriter{autosaveFound: false}
	svc := NewAutosaveService(rw, posts)

	got, ok, err := svc.Newer(context.Background(), actor(auth.RoleAuthor, 9), 5)
	if err != nil {
		t.Fatalf("Newer() error = %v, want nil", err)
	}
	if ok || got != (domain.Post{}) {
		t.Fatalf("Newer() = (%+v, %v), want (Post{}, false)", got, ok)
	}
}

func TestAutosaveNewerReturnsFalseWhenAutosaveNotNewerThanParent(t *testing.T) {
	parentModified := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)
	posts := &fakePostWriter{store: map[int64]domain.Post{
		5: {ID: 5, Type: "post", Status: "draft", Author: 9, Modified: parentModified},
	}}
	rw := &fakeRevisionWriter{
		autosaveFound: true,
		// Equal, not strictly after -- must not count as newer.
		autosavePost: domain.Post{ID: 100, ParentID: 5, Modified: parentModified},
	}
	svc := NewAutosaveService(rw, posts)

	got, ok, err := svc.Newer(context.Background(), actor(auth.RoleAuthor, 9), 5)
	if err != nil {
		t.Fatalf("Newer() error = %v, want nil", err)
	}
	if ok || got != (domain.Post{}) {
		t.Fatalf("Newer() = (%+v, %v), want (Post{}, false) when autosave is not strictly newer", got, ok)
	}
}

func TestAutosaveNewerReturnsTrueWhenAutosaveNewerThanParent(t *testing.T) {
	parentModified := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)
	autosaveModified := parentModified.Add(time.Hour)
	posts := &fakePostWriter{store: map[int64]domain.Post{
		5: {ID: 5, Type: "post", Status: "draft", Author: 9, Modified: parentModified},
	}}
	autosave := domain.Post{ID: 100, ParentID: 5, Modified: autosaveModified}
	rw := &fakeRevisionWriter{autosaveFound: true, autosavePost: autosave}
	svc := NewAutosaveService(rw, posts)

	got, ok, err := svc.Newer(context.Background(), actor(auth.RoleAuthor, 9), 5)
	if err != nil {
		t.Fatalf("Newer() error = %v, want nil", err)
	}
	if !ok || got != autosave {
		t.Fatalf("Newer() = (%+v, %v), want (%+v, true)", got, ok, autosave)
	}
}

func TestAutosaveNewerReturnsNotFoundWhenUnauthorized(t *testing.T) {
	posts := &fakePostWriter{store: map[int64]domain.Post{
		5: {ID: 5, Type: "post", Status: "draft", Author: 1},
	}}
	rw := &fakeRevisionWriter{autosaveFound: true, autosavePost: domain.Post{ID: 100, ParentID: 5}}
	svc := NewAutosaveService(rw, posts)

	_, _, err := svc.Newer(context.Background(), actor(auth.RoleContributor, 9), 5)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Newer() error = %v, want domain.ErrNotFound", err)
	}
}
