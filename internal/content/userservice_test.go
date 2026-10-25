package content

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/auth/password"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/sanitize"
)

// fakeUserRepo / fakeMetaRepo back the UserService tests.

type fakeUserRepo struct {
	byID   map[int64]domain.User
	nextID int64

	// updateProfileCalls records every UpdateProfile that reached the port, in
	// order, so tests can assert partial-update (only supplied fields) and the
	// no-call case (a description-only update must NOT touch the users table).
	updateProfileCalls []updateProfileCall

	deleteCalls []deleteUserCall
	deleteErr   error
}

// updateProfileCall captures a single UserRepository.UpdateProfile invocation:
// the id it targeted and the exact UserProfileFields it received.
type updateProfileCall struct {
	id     int64
	fields domain.UserProfileFields
}

// deleteUserCall captures a single UserRepository.Delete invocation.
type deleteUserCall struct {
	id, reassignTo int64
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

// ByNicename mirrors the repository's lowest-ID-wins resolution (Req 7.6). The
// UserService never calls it; it is here to satisfy domain.UserRepository.
func (f *fakeUserRepo) ByNicename(_ context.Context, nicename string) (domain.User, error) {
	var found domain.User
	ok := false
	for _, u := range f.byID {
		if u.Nicename != nicename {
			continue
		}
		if !ok || u.ID < found.ID {
			found, ok = u, true
		}
	}
	if !ok {
		return domain.User{}, domain.ErrNotFound
	}
	return found, nil
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

// UpdateProfile records the call and applies only the non-nil fields to the
// stored row, so a subsequent ByID re-read reflects exactly what was written
// (partial update). It returns domain.ErrNotFound for an unknown id, matching
// the UpdatePass/port contract.
func (f *fakeUserRepo) UpdateProfile(_ context.Context, id int64, fields domain.UserProfileFields) error {
	f.updateProfileCalls = append(f.updateProfileCalls, updateProfileCall{id: id, fields: fields})
	u, ok := f.byID[id]
	if !ok {
		return domain.ErrNotFound
	}
	if fields.DisplayName != nil {
		u.DisplayName = *fields.DisplayName
	}
	if fields.Email != nil {
		u.Email = *fields.Email
	}
	if fields.URL != nil {
		u.URL = *fields.URL
	}
	if fields.Nicename != nil {
		u.Nicename = *fields.Nicename
	}
	f.byID[id] = u
	return nil
}

// ByEmail mirrors ByLogin: first match by user_email, else domain.ErrNotFound.
// The UserService never calls it; it is here to satisfy domain.UserRepository.
func (f *fakeUserRepo) ByEmail(_ context.Context, email string) (domain.User, error) {
	for _, u := range f.byID {
		if u.Email == email {
			return u, nil
		}
	}
	return domain.User{}, domain.ErrNotFound
}
func (f *fakeUserRepo) List(_ context.Context, limit, offset int) ([]domain.User, error) {
	return nil, nil
}
func (f *fakeUserRepo) Count(_ context.Context) (int64, error) {
	return int64(len(f.byID)), nil
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

// ---------------------------------------------------------------------------
// Policy seam + UpdateProfile unit tests (task 4.1). These are the failing-first
// (TDD red) tests for the UserService policy field, WithUserContentPolicy, the
// content.UserProfileUpdate value object, and the UpdateProfile method — none of
// which exist yet (task 4.2 implements them). They compile-fail on those
// symbols; that is the expected red.
//
// The service shape under test (design.md "content.UserService additions"):
//
//	type UserService struct { ...; policy *sanitize.Policy }       // never nil
//	type UserServiceOption func(*UserService)
//	func WithUserContentPolicy(p *sanitize.Policy) UserServiceOption
//	func NewUserService(u, m, prefix, opts ...UserServiceOption) *UserService
//	type UserProfileUpdate struct { Name, Email, URL, Slug, Description *string }
//	func (s *UserService) UpdateProfile(ctx, actor, id int64, upd UserProfileUpdate) (domain.User, error)
//
// Every sanitize assertion pins behavior against the REAL sanitize.Policy (the
// oracle). Tier facts used below (internal/auth/roles.go): RoleAuthor does NOT
// hold unfiltered_html (tier B, markup stripped) and does NOT hold edit_users;
// RoleEditor holds unfiltered_html (tier C, byte-identical) but NOT edit_users;
// RoleAdministrator holds edit_users. So an editor is the tier-C writer whose
// own-record edit needs no edit_users, and an author editing another's record
// is the forbidden case.

// strptr (defined in mediawriteservice_test.go) builds the pointer-valued
// UserProfileUpdate fields in this file; nil = omitted / leave the stored value
// unchanged.

// stageStoredUser seeds the fakeUserRepo behind the service with a stored user
// at id (so UpdateProfile's ByID load returns it and the self-vs-others gate
// runs against this stored id versus actor.UserID, never a body value).
func stageStoredUser(ur *fakeUserRepo, id int64, u domain.User) {
	u.ID = id
	ur.byID[id] = u
	if id > ur.nextID {
		ur.nextID = id
	}
}

// TestNewUserServiceDefaultsPolicyNeverNil asserts the variadic constructor
// defaults policy to sanitize.New() and never leaves it nil (fail closed), and
// that the existing three-argument NewUserService(users, meta, prefix) call site
// still compiles and gets that default (Req 11.1, 11.3).
func TestNewUserServiceDefaultsPolicyNeverNil(t *testing.T) {
	ur := newFakeUserRepo()
	mr := newFakeMetaRepo()
	// Three-argument call site must still compile unchanged (variadic option).
	svc := NewUserService(ur, mr, "wp_")
	if svc.policy == nil {
		t.Fatal("NewUserService default policy is nil; want sanitize.New() (fail closed, never nil)")
	}
}

// TestWithUserContentPolicyOverridesDefault asserts WithUserContentPolicy wires
// the supplied *sanitize.Policy, overriding the constructor default (Req 4.2,
// 11.1). The exact instance passed must be the one the service holds, so the
// process-wide shared policy reaches UserService.
func TestWithUserContentPolicyOverridesDefault(t *testing.T) {
	ur := newFakeUserRepo()
	mr := newFakeMetaRepo()
	pol := sanitize.New()
	svc := NewUserService(ur, mr, "wp_", WithUserContentPolicy(pol))
	if svc.policy != pol {
		t.Fatalf("WithUserContentPolicy did not override the default: policy = %p, want %p", svc.policy, pol)
	}
}

// TestUpdateProfileSelfRecordAllowedWithoutEditUsers asserts a user may edit
// their OWN record without edit_users (Req 5.3): an editor (no edit_users) whose
// UserID equals the {id} is authorized, the supplied field reaches
// UserRepository.UpdateProfile, and the self-vs-others decision is evaluated
// against the stored id versus actor.UserID (Req 5.5, 11.2).
func TestUpdateProfileSelfRecordAllowedWithoutEditUsers(t *testing.T) {
	svc, ur, _ := newUserService()
	const uid = int64(7)
	stageStoredUser(ur, uid, domain.User{Login: "self", DisplayName: "old"})

	// Editor holds no edit_users; this is the own-record branch.
	if _, err := svc.UpdateProfile(context.Background(), actor(auth.RoleEditor, uid), uid, UserProfileUpdate{
		Name: strptr("New Name"),
	}); err != nil {
		t.Fatalf("self-record edit without edit_users: %v", err)
	}
	if len(ur.updateProfileCalls) != 1 {
		t.Fatalf("authorized self edit should write once: updateProfileCalls = %+v", ur.updateProfileCalls)
	}
	if got := ur.byID[uid].DisplayName; got != "New Name" {
		t.Errorf("display_name = %q, want %q", got, "New Name")
	}
}

// TestUpdateProfileOtherRecordRequiresEditUsers asserts editing ANOTHER user's
// record requires edit_users (Req 5.4): an author (no edit_users) editing a
// record whose id != actor.UserID is rejected with content.ErrForbidden and
// writes NOTHING; an administrator (edit_users) may edit it. The gate keys off
// the {id} vs actor.UserID, never a body value (Req 5.5, 11.2).
func TestUpdateProfileOtherRecordRequiresEditUsers(t *testing.T) {
	t.Run("author without edit_users is forbidden on another's record", func(t *testing.T) {
		svc, ur, mr := newUserService()
		stageStoredUser(ur, 10, domain.User{Login: "victim", DisplayName: "orig"})

		_, err := svc.UpdateProfile(context.Background(), actor(auth.RoleAuthor, 7), 10, UserProfileUpdate{
			Name: strptr("Hacked"),
		})
		if !errors.Is(err, ErrForbidden) {
			t.Fatalf("author editing another's record: err = %v, want ErrForbidden", err)
		}
		if len(ur.updateProfileCalls) != 0 {
			t.Fatalf("forbidden edit must not write users table: %+v", ur.updateProfileCalls)
		}
		if len(mr.m[10]) != 0 {
			t.Fatalf("forbidden edit must not write meta: %+v", mr.m[10])
		}
		if got := ur.byID[10].DisplayName; got != "orig" {
			t.Errorf("stored display_name mutated to %q, want unchanged %q", got, "orig")
		}
	})

	t.Run("administrator with edit_users may edit another's record", func(t *testing.T) {
		svc, ur, _ := newUserService()
		stageStoredUser(ur, 10, domain.User{Login: "victim", DisplayName: "orig"})

		if _, err := svc.UpdateProfile(context.Background(), actor(auth.RoleAdministrator, 1), 10, UserProfileUpdate{
			Name: strptr("By Admin"),
		}); err != nil {
			t.Fatalf("admin editing another's record: %v", err)
		}
		if len(ur.updateProfileCalls) != 1 {
			t.Fatalf("authorized edit should write once: %+v", ur.updateProfileCalls)
		}
		if got := ur.byID[10].DisplayName; got != "By Admin" {
			t.Errorf("display_name = %q, want %q", got, "By Admin")
		}
	})
}

// TestUpdateProfileMissingIDReturnsNotFound asserts an unknown {id} bubbles up
// domain.ErrNotFound from the fake's ByID before any write (Req 7.4).
func TestUpdateProfileMissingIDReturnsNotFound(t *testing.T) {
	svc, ur, mr := newUserService()
	// No user staged at id 404.

	_, err := svc.UpdateProfile(context.Background(), actor(auth.RoleAdministrator, 1), 404, UserProfileUpdate{
		Name: strptr("Nobody"),
	})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing id: err = %v, want domain.ErrNotFound", err)
	}
	if len(ur.updateProfileCalls) != 0 {
		t.Fatalf("not-found update must not write users table: %+v", ur.updateProfileCalls)
	}
	if len(mr.m[404]) != 0 {
		t.Fatalf("not-found update must not write meta: %+v", mr.m[404])
	}
}

// TestUpdateProfilePartialUpdateOnlySuppliedFieldsReachPort asserts only the
// supplied (non-nil) fields reach UserRepository.UpdateProfile (Req 7.2, 7.3):
// supplying only Email and Slug leaves DisplayName/URL nil in the recorded
// UserProfileFields, so omitted columns are untouched. Slug maps to Nicename
// (Req 7.3).
func TestUpdateProfilePartialUpdateOnlySuppliedFieldsReachPort(t *testing.T) {
	svc, ur, _ := newUserService()
	const uid = int64(7)
	stageStoredUser(ur, uid, domain.User{Login: "self", DisplayName: "keep", URL: "https://keep.example"})

	if _, err := svc.UpdateProfile(context.Background(), actor(auth.RoleEditor, uid), uid, UserProfileUpdate{
		Email: strptr("new@example.com"),
		Slug:  strptr("new-slug"),
	}); err != nil {
		t.Fatalf("partial update: %v", err)
	}
	if len(ur.updateProfileCalls) != 1 {
		t.Fatalf("partial update should write once: %+v", ur.updateProfileCalls)
	}
	f := ur.updateProfileCalls[0].fields
	if f.Email == nil || *f.Email != "new@example.com" {
		t.Errorf("Email field = %v, want pointer to %q", f.Email, "new@example.com")
	}
	if f.Nicename == nil || *f.Nicename != "new-slug" {
		t.Errorf("Nicename (slug) field = %v, want pointer to %q", f.Nicename, "new-slug")
	}
	if f.DisplayName != nil {
		t.Errorf("DisplayName should be nil (omitted), got pointer to %q", *f.DisplayName)
	}
	if f.URL != nil {
		t.Errorf("URL should be nil (omitted), got pointer to %q", *f.URL)
	}
}

// TestUpdateProfileDescriptionOnlyIssuesNoUsersTableUpdate asserts the
// fields.any() guard: a description-only update makes NO users-table
// UpdateProfile call (no users-table columns supplied), and persists the
// description via UserMetaRepository.Set under the prefixless "description" key
// (Req 3.4, 7.2, D7).
func TestUpdateProfileDescriptionOnlyIssuesNoUsersTableUpdate(t *testing.T) {
	svc, ur, mr := newUserService()
	const uid = int64(7)
	stageStoredUser(ur, uid, domain.User{Login: "self"})

	if _, err := svc.UpdateProfile(context.Background(), actor(auth.RoleEditor, uid), uid, UserProfileUpdate{
		Description: strptr("just a bio"),
	}); err != nil {
		t.Fatalf("description-only update: %v", err)
	}
	if len(ur.updateProfileCalls) != 0 {
		t.Fatalf("description-only update must NOT call users.UpdateProfile (fields.any() guard): %+v", ur.updateProfileCalls)
	}
	if got := mr.m[uid]["description"]; got != "just a bio" {
		t.Errorf("meta description = %q, want %q (prefixless key, D7)", got, "just a bio")
	}
}

// TestUpdateProfileDescriptionSanitizedAtPostContentTierB asserts a tier-B
// writer's description is sanitized at sanitize.PostContent before it reaches
// meta.Set: an author's <script> is stripped, and the persisted value matches
// the real policy oracle at (PostContent, author) (Req 4.1, D8, Finding 2).
func TestUpdateProfileDescriptionSanitizedAtPostContentTierB(t *testing.T) {
	svc, ur, mr := newUserService()
	const uid = int64(7)
	stageStoredUser(ur, uid, domain.User{Login: "self"})

	const descIn = `bio <script>alert(1)</script> end`
	pol := sanitize.New()
	want, err := pol.Sanitize(sanitize.PostContent, sanitize.For(actor(auth.RoleAuthor, uid)), descIn)
	if err != nil {
		t.Fatalf("oracle description: %v", err)
	}

	if _, err := svc.UpdateProfile(context.Background(), actor(auth.RoleAuthor, uid), uid, UserProfileUpdate{
		Description: strptr(descIn),
	}); err != nil {
		t.Fatalf("sanitizing update: %v", err)
	}
	got := mr.m[uid]["description"]
	if strings.Contains(got, "<script>") {
		t.Errorf("tier-B description still contains <script>: %q", got)
	}
	if got != want {
		t.Errorf("persisted description = %q, want policy oracle %q (PostContent, tier B)", got, want)
	}
}

// TestUpdateProfileDescriptionUnfilteredHTMLSurvivesVerbatim asserts a tier-C
// writer (unfiltered_html; editor) has their description persisted
// byte-identically. The input is one a tier-B writer demonstrably alters
// (<script>), proving the pass-through is the tier-C bypass and not a
// coincidence (Req 4.2, D8).
func TestUpdateProfileDescriptionUnfilteredHTMLSurvivesVerbatim(t *testing.T) {
	svc, ur, mr := newUserService()
	const uid = int64(7)
	stageStoredUser(ur, uid, domain.User{Login: "self"})

	const descIn = `keep <script>alert(1)</script> me`

	// Precondition: a tier-B writer alters this input, so verbatim survival at
	// tier C is meaningful.
	pol := sanitize.New()
	if tierB, _ := pol.Sanitize(sanitize.PostContent, sanitize.For(actor(auth.RoleAuthor, uid)), descIn); tierB == descIn {
		t.Fatalf("precondition: tier B must alter %q for this test to be meaningful", descIn)
	}

	if _, err := svc.UpdateProfile(context.Background(), actor(auth.RoleEditor, uid), uid, UserProfileUpdate{
		Description: strptr(descIn),
	}); err != nil {
		t.Fatalf("unfiltered_html update: %v", err)
	}
	if got := mr.m[uid]["description"]; got != descIn {
		t.Errorf("unfiltered_html description = %q, want verbatim %q (tier C)", got, descIn)
	}
}

func (f *fakeUserRepo) Delete(_ context.Context, id int64, reassignTo int64) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deleteCalls = append(f.deleteCalls, deleteUserCall{id: id, reassignTo: reassignTo})
	delete(f.byID, id)
	return nil
}
