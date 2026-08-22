package content

import (
	"context"
	"strconv"
	"testing"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/auth/password"
	"github.com/roboweaver/grimoire/internal/domain"
)

// fakeUserRepo / fakeMetaRepo back the UserService tests.

type fakeUserRepo struct {
	byID   map[int64]domain.User
	nextID int64
}

func newFakeUserRepo() *fakeUserRepo { return &fakeUserRepo{byID: map[int64]domain.User{}} }

func (f *fakeUserRepo) ByLogin(_ context.Context, login string) (domain.User, error) {
	for _, u := range f.byID {
		if u.Login == login {
			return u, nil
		}
	}
	return domain.User{}, domain.ErrNotFound
}
func (f *fakeUserRepo) ByID(_ context.Context, id int64) (domain.User, error) {
	u, ok := f.byID[id]
	if !ok {
		return domain.User{}, domain.ErrNotFound
	}
	return u, nil
}
func (f *fakeUserRepo) Create(_ context.Context, u domain.User) (int64, error) {
	f.nextID++
	u.ID = f.nextID
	f.byID[u.ID] = u
	return u.ID, nil
}
func (f *fakeUserRepo) UpdatePass(_ context.Context, id int64, passHash string) error {
	u, ok := f.byID[id]
	if !ok {
		return domain.ErrNotFound
	}
	u.Pass = passHash
	f.byID[id] = u
	return nil
}

type fakeMetaRepo struct {
	m map[int64]map[string]string
}

func newFakeMetaRepo() *fakeMetaRepo { return &fakeMetaRepo{m: map[int64]map[string]string{}} }

func (f *fakeMetaRepo) Get(_ context.Context, userID int64, key string) (string, error) {
	if v, ok := f.m[userID][key]; ok {
		return v, nil
	}
	return "", domain.ErrNotFound
}
func (f *fakeMetaRepo) Set(_ context.Context, userID int64, key, value string) error {
	if f.m[userID] == nil {
		f.m[userID] = map[string]string{}
	}
	f.m[userID][key] = value
	return nil
}
func (f *fakeMetaRepo) ByUser(_ context.Context, userID int64) (map[string]string, error) {
	out := map[string]string{}
	for k, v := range f.m[userID] {
		out[k] = v
	}
	return out, nil
}

func newUserService() (*UserService, *fakeUserRepo, *fakeMetaRepo) {
	ur := newFakeUserRepo()
	mr := newFakeMetaRepo()
	return NewUserService(ur, mr, "wp_"), ur, mr
}

func TestUserServiceBootstrapCreatesAdminWithHashedPassAndCaps(t *testing.T) {
	svc, ur, mr := newUserService()
	id, err := svc.Bootstrap(context.Background(),
		domain.User{Login: "admin", Email: "a@example.com"}, "s3cret", auth.RoleAdministrator)
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	u := ur.byID[id]
	if u.Pass == "" || u.Pass == "s3cret" {
		t.Fatalf("password not hashed: %q", u.Pass)
	}
	if ok, _ := password.Verify("s3cret", u.Pass); !ok {
		t.Error("stored hash does not verify against the password")
	}
	caps := mr.m[id]["wp_capabilities"]
	roles, err := auth.ParseCapabilities(caps)
	if err != nil {
		t.Fatalf("parse caps %q: %v", caps, err)
	}
	if len(roles) != 1 || roles[0] != auth.RoleAdministrator {
		t.Errorf("roles = %v, want [administrator]", roles)
	}
	if got := mr.m[id]["wp_user_level"]; got != strconv.Itoa(auth.UserLevel(auth.RoleAdministrator)) {
		t.Errorf("user_level = %q, want %d", got, auth.UserLevel(auth.RoleAdministrator))
	}
}

func TestUserServiceCreateRequiresCapability(t *testing.T) {
	svc, ur, _ := newUserService()
	// Editor lacks create_users.
	_, err := svc.Create(context.Background(), actor(auth.RoleEditor, 1),
		domain.User{Login: "bob"}, "pw", auth.RoleAuthor)
	if err != ErrForbidden {
		t.Fatalf("editor create user: err = %v, want ErrForbidden", err)
	}
	if len(ur.byID) != 0 {
		t.Error("user created despite denial")
	}

	// Administrator can.
	id, err := svc.Create(context.Background(), actor(auth.RoleAdministrator, 1),
		domain.User{Login: "bob"}, "pw", auth.RoleAuthor)
	if err != nil {
		t.Fatalf("admin create user: %v", err)
	}
	if _, ok := ur.byID[id]; !ok {
		t.Error("user not created")
	}
}

func TestUserServiceSetRolesRequiresEditUsers(t *testing.T) {
	svc, ur, mr := newUserService()
	id, _ := svc.Bootstrap(context.Background(), domain.User{Login: "bob"}, "pw", auth.RoleSubscriber)

	if err := svc.SetRoles(context.Background(), actor(auth.RoleAuthor, 2), id, auth.RoleEditor); err != ErrForbidden {
		t.Fatalf("author set roles: err = %v, want ErrForbidden", err)
	}
	if err := svc.SetRoles(context.Background(), actor(auth.RoleAdministrator, 1), id, auth.RoleEditor); err != nil {
		t.Fatalf("admin set roles: %v", err)
	}
	roles, _ := auth.ParseCapabilities(mr.m[id]["wp_capabilities"])
	if len(roles) != 1 || roles[0] != auth.RoleEditor {
		t.Errorf("roles = %v, want [editor]", roles)
	}
	if mr.m[id]["wp_user_level"] != strconv.Itoa(auth.UserLevel(auth.RoleEditor)) {
		t.Errorf("user_level not updated")
	}
	_ = ur
}

func TestUserServiceSetPasswordAuthz(t *testing.T) {
	svc, ur, _ := newUserService()
	id, _ := svc.Bootstrap(context.Background(), domain.User{Login: "bob"}, "old", auth.RoleAuthor)

	// A different non-privileged user cannot change bob's password.
	if err := svc.SetPassword(context.Background(), actor(auth.RoleAuthor, 999), id, "new"); err != ErrForbidden {
		t.Fatalf("other author set password: err = %v, want ErrForbidden", err)
	}
	// The user may change their own password.
	if err := svc.SetPassword(context.Background(), actor(auth.RoleAuthor, id), id, "self"); err != nil {
		t.Fatalf("self set password: %v", err)
	}
	if ok, _ := password.Verify("self", ur.byID[id].Pass); !ok {
		t.Error("self password not updated")
	}
	// An admin may change anyone's password.
	if err := svc.SetPassword(context.Background(), actor(auth.RoleAdministrator, 1), id, "byadmin"); err != nil {
		t.Fatalf("admin set password: %v", err)
	}
	if ok, _ := password.Verify("byadmin", ur.byID[id].Pass); !ok {
		t.Error("admin password change not applied")
	}
}
