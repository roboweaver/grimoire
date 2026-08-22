package content

import (
	"context"
	"testing"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/domain"
)

// --- fakes for the writer ports ---------------------------------------------

type fakePostWriter struct {
	created, updated *domain.Post
	deleted          int64
	nextID           int64
}

func (f *fakePostWriter) Create(_ context.Context, p domain.Post) (int64, error) {
	cp := p
	f.created = &cp
	if f.nextID == 0 {
		f.nextID = 42
	}
	return f.nextID, nil
}
func (f *fakePostWriter) Update(_ context.Context, p domain.Post) error {
	cp := p
	f.updated = &cp
	return nil
}
func (f *fakePostWriter) Delete(_ context.Context, id int64) error {
	f.deleted = id
	return nil
}

type fakeTermWriter struct {
	created *domain.Term
	deleted int64
}

func (f *fakeTermWriter) Create(_ context.Context, t domain.Term) (int64, error) {
	ct := t
	f.created = &ct
	return 7, nil
}
func (f *fakeTermWriter) Delete(_ context.Context, id int64) error {
	f.deleted = id
	return nil
}

type fakeOptionWriter struct {
	set     map[string]string
	deleted string
}

func (f *fakeOptionWriter) Set(_ context.Context, name, value string) error {
	if f.set == nil {
		f.set = map[string]string{}
	}
	f.set[name] = value
	return nil
}
func (f *fakeOptionWriter) Delete(_ context.Context, name string) error {
	f.deleted = name
	return nil
}

// --- helpers ----------------------------------------------------------------

func actor(role string, id int64) auth.Principal {
	return auth.NewPrincipal(id, "u", []string{role})
}

// --- PostWriteService -------------------------------------------------------

func TestPostWriteCreateAllowedDefaultsAuthorAndType(t *testing.T) {
	w := &fakePostWriter{}
	svc := NewPostWriteService(w)
	id, err := svc.Create(context.Background(), actor(auth.RoleAuthor, 5),
		domain.Post{Title: "Hi", Status: "publish"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if id != 42 {
		t.Errorf("id = %d, want 42", id)
	}
	if w.created == nil {
		t.Fatal("writer not called")
	}
	if w.created.Author != 5 {
		t.Errorf("author defaulted to %d, want 5", w.created.Author)
	}
	if w.created.Type != "post" {
		t.Errorf("type defaulted to %q, want post", w.created.Type)
	}
}

func TestPostWriteCreateDeniedReturnsForbiddenAndSkipsWriter(t *testing.T) {
	w := &fakePostWriter{}
	svc := NewPostWriteService(w)
	// Contributor cannot publish.
	_, err := svc.Create(context.Background(), actor(auth.RoleContributor, 5),
		domain.Post{Title: "Hi", Status: "publish"})
	if err != ErrForbidden {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
	if w.created != nil {
		t.Error("writer must not be called on denial")
	}
}

func TestPostWriteUpdateAndDeleteEnforceOwnership(t *testing.T) {
	w := &fakePostWriter{}
	svc := NewPostWriteService(w)
	othersPublished := domain.Post{ID: 3, Author: 999, Type: "post", Status: "publish"}

	// Author cannot edit/delete someone else's published post.
	if err := svc.Update(context.Background(), actor(auth.RoleAuthor, 5), othersPublished); err != ErrForbidden {
		t.Errorf("author update others: err = %v, want ErrForbidden", err)
	}
	if err := svc.Delete(context.Background(), actor(auth.RoleAuthor, 5), othersPublished); err != ErrForbidden {
		t.Errorf("author delete others: err = %v, want ErrForbidden", err)
	}
	if w.updated != nil || w.deleted != 0 {
		t.Error("writer must not be called on denial")
	}

	// Editor can.
	if err := svc.Update(context.Background(), actor(auth.RoleEditor, 5), othersPublished); err != nil {
		t.Errorf("editor update: %v", err)
	}
	if err := svc.Delete(context.Background(), actor(auth.RoleEditor, 5), othersPublished); err != nil {
		t.Errorf("editor delete: %v", err)
	}
	if w.deleted != 3 {
		t.Errorf("deleted id = %d, want 3", w.deleted)
	}
}

// --- TermWriteService -------------------------------------------------------

func TestTermWriteAuthz(t *testing.T) {
	w := &fakeTermWriter{}
	svc := NewTermWriteService(w)
	term := domain.Term{Name: "News", Slug: "news", Taxonomy: "category"}

	if _, err := svc.Create(context.Background(), actor(auth.RoleAuthor, 1), term); err != ErrForbidden {
		t.Errorf("author create term: err = %v, want ErrForbidden", err)
	}
	if w.created != nil {
		t.Error("writer called on denial")
	}
	if _, err := svc.Create(context.Background(), actor(auth.RoleEditor, 1), term); err != nil {
		t.Errorf("editor create term: %v", err)
	}
	if err := svc.Delete(context.Background(), actor(auth.RoleEditor, 1), 7); err != nil {
		t.Errorf("editor delete term: %v", err)
	}
	if w.deleted != 7 {
		t.Errorf("deleted = %d, want 7", w.deleted)
	}
}

// --- OptionWriteService -----------------------------------------------------

func TestOptionWriteAuthz(t *testing.T) {
	w := &fakeOptionWriter{}
	svc := NewOptionWriteService(w)

	if err := svc.Set(context.Background(), actor(auth.RoleEditor, 1), "blogname", "X"); err != ErrForbidden {
		t.Errorf("editor set option: err = %v, want ErrForbidden", err)
	}
	if len(w.set) != 0 {
		t.Error("writer called on denial")
	}
	if err := svc.Set(context.Background(), actor(auth.RoleAdministrator, 1), "blogname", "X"); err != nil {
		t.Errorf("admin set option: %v", err)
	}
	if w.set["blogname"] != "X" {
		t.Errorf("option not written: %v", w.set)
	}
	if err := svc.Delete(context.Background(), actor(auth.RoleAdministrator, 1), "blogname"); err != nil {
		t.Errorf("admin delete option: %v", err)
	}
	if w.deleted != "blogname" {
		t.Errorf("deleted = %q, want blogname", w.deleted)
	}
}
