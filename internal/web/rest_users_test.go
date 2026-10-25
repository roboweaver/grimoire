package web_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/roboweaver/grimoire/internal/admin"
	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/render"
	"github.com/roboweaver/grimoire/internal/sanitize"
	"github.com/roboweaver/grimoire/internal/storage"
	"github.com/roboweaver/grimoire/internal/storage/migrate"
	"github.com/roboweaver/grimoire/internal/storage/storagetest"
	"github.com/roboweaver/grimoire/internal/web"
)

// Task 5.1 — REST user CREATE transport tests (handleRESTUserCreate),
// end-to-end through the router with a REAL content.UserService wired via the
// assumed WithUserWrites builder (Req 1.1-1.7, 2.1, 2.2, 5.1, 5.6, 6.1, 7.5,
// 10.1, 10.2; Finding 1). The request flows through the live chi router, the
// live UserService (which owns the create_users check, the bcrypt provision,
// the role write, and the description Policy), and the live SQLite user ports.
// Each assertion is the behavior documented in requirements.md and tasks.md's
// task 5.2 implementation note (the error-code table).
//
// Written RED first (task 5.1): the Server does not yet carry a userWrite
// field or a WithUserWrites builder (task 6.1), and handleRESTUserCreate does
// not exist (task 5.2). The package therefore does not compile until
// WithUserWrites + the s.userWrite field exist, and once they do these POST
// tests fail against the current restNotImplemented 501 stub for POST /users
// until 5.2/5.5 land. The harness is written to the task-6.1 wiring shape:
//
//	.WithContentFeatures(comments, nil, nil).
//	WithUserWrites(userWrite).
//	WithREST(mapper, repos.AdminPosts, repos.PostWriter, repos.Posts, repos.Media, repos.Users, 0)
//
// so that WithUserWrites (+ the s.userWrite field it sets) and
// handleRESTUserCreate are the ONLY missing symbols — every other symbol the
// test uses already resolves. WithUserWrites mirrors WithMediaWrites: a
// non-breaking builder method, NOT a new positional parameter on
// WithContentFeatures (which would break ~8 existing call sites).

// Seeded/duplicate fixture facts (storagetest.SeedFixtures):
//   - exactly ONE user is seeded: ID 1, user_login "admin", user_nicename
//     "admin", display_name "Admin", and NO user_email.
//
// Because the seed has no user carrying an email, the harness provisions a
// second, known user ("dupe" / "dupe@example.com") through the REAL
// UserService.Create as the administrator, so the duplicate-login
// (existing_user_login) and duplicate-email (existing_user_email) checks have
// genuinely-stored values to collide with — ByLogin/ByEmail find real rows a
// transport that bypassed the service could not have created.
const (
	dupeLogin = "dupe"
	dupeEmail = "dupe@example.com"
)

// userCreateCSRFToken is the synchronizer token every session-authenticated
// user-write request in this file carries, matching the fake's session
// CSRFToken so the M4 REST CSRF check (requireSessionCSRFREST) passes and the
// request reaches the capability/validation logic rather than being rejected
// 403 rest_forbidden first (Req 10.2).
const userCreateCSRFToken = "user-write-token"

// newRESTUserWriteRouter builds the wp-json router with the REAL UserService
// wired as s.userWrite (via the assumed WithUserWrites builder) and the REST
// user surface wired via WithREST, returning the router and the seeded
// Repositories so a test can read a persisted user/usermeta back through
// repos.Users / repos.UserMeta. The UserService is the real
// content.NewUserService over the live SQLite user ports, handed the shared
// sanitize policy via WithUserContentPolicy so a submitted description is
// sanitized before it reaches the store (Req 1.7, 4.1). A known duplicate user
// ("dupe"/"dupe@example.com") is provisioned through that same service so the
// duplicate-login/duplicate-email cases collide with real rows (Finding 1).
func newRESTUserWriteRouter(t *testing.T, fake *fakeSessions) (http.Handler, *storage.Repositories) {
	t.Helper()
	ctx := context.Background()
	dsn := filepath.Join(t.TempDir(), "grimoire.db")
	cfg := config.DatabaseConfig{Vendor: "sqlite", DSN: dsn, TablePrefix: "wp_"}
	repos, err := storage.New(cfg)
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	t.Cleanup(func() { repos.Close() })

	migFS, err := storage.MigrationsFS(cfg.Vendor)
	if err != nil {
		t.Fatalf("MigrationsFS: %v", err)
	}
	if _, err := migrate.Apply(ctx, repos.DB(), migFS, cfg.Vendor, cfg.TablePrefix); err != nil {
		t.Fatalf("migrate.Apply: %v", err)
	}
	if err := storagetest.SeedFixtures(ctx, repos.DB(), cfg.Vendor, cfg.TablePrefix); err != nil {
		t.Fatalf("SeedFixtures: %v", err)
	}

	eng, err := render.Load(filepath.Join("..", "..", "themes"), "default")
	if err != nil {
		t.Fatalf("render.Load: %v", err)
	}
	adminSvc := content.NewAdminService(
		repos.AdminPosts, repos.PostWriter, repos.PostCounter,
		repos.UserCounter, repos.TermCounter, repos.Users,
	)
	mapper := content.NewRESTMapper(repos.PostTerms, repos.PostMeta, repos.UserMeta, "wp_")

	// The REAL UserService, over the live SQLite user ports, handed the shared
	// sanitize policy (fail closed), matching production wiring. This is both
	// s.userWrite and the service the harness itself uses to provision the
	// duplicate fixture — one service, one write path.
	userWrite := content.NewUserService(repos.Users, repos.UserMeta, "wp_", content.WithUserContentPolicy(sanitize.New()))

	// Provision the known duplicate user through the real service so ByLogin /
	// ByEmail find a genuinely-stored row (Finding 1). The admin principal
	// holds create_users.
	admin1 := auth.NewPrincipal(1, "admin", []string{auth.RoleAdministrator})
	if _, err := userWrite.Create(ctx, admin1, domain.User{
		Login:    dupeLogin,
		Nicename: dupeLogin,
		Email:    dupeEmail,
	}, "dupe-pass"); err != nil {
		t.Fatalf("seed duplicate user via UserService.Create: %v", err)
	}

	comments := content.NewCommentService(repos.Comments, repos.CommentWriter, repos.CommentMeta, repos.PostWriter, content.NewBasicCommentSpamFilter(content.BasicCommentSpamFilterConfig{}), sanitize.New())

	srv := web.NewServer(
		content.NewPostService(repos.Posts).WithAuthors(repos.Users),
		content.NewTermService(repos.Terms, repos.Posts).WithHierarchy(repos.TermReader),
		content.NewOptionService(repos.Options),
		eng,
		nil,
	).WithAuth(fake, web.AuthConfig{}).
		WithAdmin(admin.Handler("/admin"), adminSvc).
		WithContentFeatures(comments, nil, nil).
		WithUserWrites(userWrite).
		WithREST(mapper, repos.AdminPosts, repos.PostWriter, repos.Posts, repos.Media, repos.Users, 0)
	return srv.Routes(), repos
}

// adminUserFake authenticates as an administrator (holds create_users,
// edit_users, and promote_users). Its session carries the matching CSRF token
// so a session-authenticated write clears the M4 synchronizer check and
// reaches the capability/validation logic (Req 10.2).
func adminUserFake() *fakeSessions {
	return &fakeSessions{
		authPrincipal: auth.NewPrincipal(1, "admin", []string{auth.RoleAdministrator}),
		authSession:   domain.Session{ID: "s1", UserID: 1, CSRFToken: userCreateCSRFToken},
	}
}

// createOnlyUserFake authenticates as a principal holding ONLY the explicit
// create_users capability (no role expansion, so NO unfiltered_html). Because
// auth.NewPrincipal retains any key that is not a known role name as an
// explicit capability grant, []string{"create_users"} yields a tier-B writer
// who can create users but whose content is sanitized (tier B), not passed
// through byte-identically (tier C). No default role is both a creator and a
// tier-B writer (administrator — the only default creator — also holds
// unfiltered_html), so this explicit-grant principal is required to exercise
// description sanitization on create (Req 4.2/11.2, design D8).
func createOnlyUserFake() *fakeSessions {
	return &fakeSessions{
		authPrincipal: auth.NewPrincipal(3, "creator", []string{"create_users"}),
		authSession:   domain.Session{ID: "s3", UserID: 3, CSRFToken: userCreateCSRFToken},
	}
}

// authorUserFake authenticates as an author (lacks create_users), used for the
// 403 rest_cannot_create case (Req 5.1).
func authorUserFake() *fakeSessions {
	return &fakeSessions{
		authPrincipal: auth.NewPrincipal(2, "author", []string{auth.RoleAuthor}),
		authSession:   domain.Session{ID: "s2", UserID: 2, CSRFToken: userCreateCSRFToken},
	}
}

// userCreateReq builds a POST /users JSON request authenticated as a session
// with the matching CSRF token "user-write-token".
func userCreateReq(jsonBody string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/wp-json/wp/v2/users", strings.NewReader(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "grimoire_session", Value: "anything"})
	req.Header.Set("X-CSRF-Token", userCreateCSRFToken)
	return req
}

// countUsers returns the current user row count, so a "no write" assertion can
// prove a rejected create persisted nothing.
func countUsers(t *testing.T, repos *storage.Repositories) int64 {
	t.Helper()
	n, err := repos.Users.Count(context.Background())
	if err != nil {
		t.Fatalf("repos.Users.Count: %v", err)
	}
	return n
}

// TestRESTUserCreateSuccessReturns201EditEnvelopeAndStores is the Req 1.1/1.2
// proof: an administrator (holds create_users) POSTs a valid
// username/email/password and the endpoint responds 201 with the same
// edit-context envelope GET /users/{id} returns (via mapRESTUser →
// RESTUserEdit) — id, email, and _links.self/collection — and the user reached
// storage (read back through repos.Users).
func TestRESTUserCreateSuccessReturns201EditEnvelopeAndStores(t *testing.T) {
	h, repos := newRESTUserWriteRouter(t, adminUserFake())

	body := `{"username":"newbie","email":"newbie@example.com","password":"s3cret-pass"}`
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, userCreateReq(body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body=%s)", rec.Code, rec.Body.String())
	}

	var env map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode 201 envelope %q: %v", rec.Body.String(), err)
	}
	idF, ok := env["id"].(float64)
	if !ok || idF <= 0 {
		t.Fatalf("envelope id = %v, want a positive user ID (envelope=%v)", env["id"], env)
	}
	// The edit-context envelope carries email (RESTUserEdit), the shape
	// GET /users/{id} returns in edit context — a view-context body would omit
	// it.
	if env["email"] != "newbie@example.com" {
		t.Errorf("envelope email = %v, want newbie@example.com (edit context)", env["email"])
	}
	links, ok := env["_links"].(map[string]any)
	if !ok {
		t.Fatalf("envelope missing _links; got %v", env)
	}
	if _, ok := links["self"]; !ok {
		t.Errorf("envelope _links missing self; got %v", links)
	}
	if _, ok := links["collection"]; !ok {
		t.Errorf("envelope _links missing collection; got %v", links)
	}

	// The user must actually be in storage, readable through the GET port — a
	// transport that bypassed UserService/the user writer could not satisfy
	// this.
	id := int64(idF)
	stored, err := repos.Users.ByID(context.Background(), id)
	if err != nil {
		t.Fatalf("repos.Users.ByID(%d): %v (the create never reached storage)", id, err)
	}
	if stored.Login != "newbie" {
		t.Errorf("stored Login = %q, want newbie", stored.Login)
	}
	if stored.Email != "newbie@example.com" {
		t.Errorf("stored Email = %q, want newbie@example.com", stored.Email)
	}
	// Password must be stored as a bcrypt hash, never the plaintext (Req 2.1,
	// 11.4).
	if stored.Pass == "" || stored.Pass == "s3cret-pass" {
		t.Errorf("stored Pass = %q, want a bcrypt hash (never plaintext)", stored.Pass)
	}
}

// TestRESTUserCreateMissingRequiredField400 — Req 1.3: a request omitting
// username, email, or password is rejected 400 rest_missing_callback_param and
// persists no user.
func TestRESTUserCreateMissingRequiredField400(t *testing.T) {
	cases := map[string]string{
		"missing username": `{"email":"x@example.com","password":"p"}`,
		"missing email":    `{"username":"x","password":"p"}`,
		"missing password": `{"username":"x","email":"x@example.com"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			h, repos := newRESTUserWriteRouter(t, adminUserFake())
			before := countUsers(t, repos)

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, userCreateReq(body))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
			}
			if code := decodeRESTErrCode(t, rec); code != "rest_missing_callback_param" {
				t.Errorf("error code = %q, want rest_missing_callback_param", code)
			}
			if after := countUsers(t, repos); after != before {
				t.Errorf("user count changed %d -> %d; a missing-field create must persist nothing", before, after)
			}
		})
	}
}

// TestRESTUserCreateDuplicateLogin400 — Req 1.4: a username that already exists
// is rejected 400 existing_user_login and persists no user.
func TestRESTUserCreateDuplicateLogin400(t *testing.T) {
	h, repos := newRESTUserWriteRouter(t, adminUserFake())
	before := countUsers(t, repos)

	body := `{"username":"` + dupeLogin + `","email":"fresh@example.com","password":"p"}`
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, userCreateReq(body))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
	}
	if code := decodeRESTErrCode(t, rec); code != "existing_user_login" {
		t.Errorf("error code = %q, want existing_user_login", code)
	}
	if after := countUsers(t, repos); after != before {
		t.Errorf("user count changed %d -> %d; a duplicate-login create must persist nothing", before, after)
	}
}

// TestRESTUserCreateDuplicateEmail400 — Req 1.5: an email that already exists
// (with a NON-duplicate username) is rejected 400 existing_user_email and
// persists no user.
func TestRESTUserCreateDuplicateEmail400(t *testing.T) {
	h, repos := newRESTUserWriteRouter(t, adminUserFake())
	before := countUsers(t, repos)

	body := `{"username":"fresh","email":"` + dupeEmail + `","password":"p"}`
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, userCreateReq(body))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
	}
	if code := decodeRESTErrCode(t, rec); code != "existing_user_email" {
		t.Errorf("error code = %q, want existing_user_email", code)
	}
	if after := countUsers(t, repos); after != before {
		t.Errorf("user count changed %d -> %d; a duplicate-email create must persist nothing", before, after)
	}
}

// TestRESTUserCreateDuplicateLoginAndEmailReportsLoginFirst — Req 1.4
// (ordering): a request carrying BOTH a duplicate username AND a duplicate
// email reports ONLY existing_user_login (username checked first), never
// existing_user_email, and persists no user.
func TestRESTUserCreateDuplicateLoginAndEmailReportsLoginFirst(t *testing.T) {
	h, repos := newRESTUserWriteRouter(t, adminUserFake())
	before := countUsers(t, repos)

	body := `{"username":"` + dupeLogin + `","email":"` + dupeEmail + `","password":"p"}`
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, userCreateReq(body))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
	}
	if code := decodeRESTErrCode(t, rec); code != "existing_user_login" {
		t.Errorf("error code = %q, want existing_user_login (login is checked first, Req 1.4)", code)
	}
	if after := countUsers(t, repos); after != before {
		t.Errorf("user count changed %d -> %d; a duplicate create must persist nothing", before, after)
	}
}

// TestRESTUserCreateLacksCreateUsers403 — Req 5.1: a caller without
// create_users (an author) is rejected 403 rest_cannot_create and persists no
// user.
func TestRESTUserCreateLacksCreateUsers403(t *testing.T) {
	h, repos := newRESTUserWriteRouter(t, authorUserFake())
	before := countUsers(t, repos)

	body := `{"username":"nope","email":"nope@example.com","password":"p"}`
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, userCreateReq(body))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body=%s)", rec.Code, rec.Body.String())
	}
	if code := decodeRESTErrCode(t, rec); code != "rest_cannot_create" {
		t.Errorf("error code = %q, want rest_cannot_create", code)
	}
	if after := countUsers(t, repos); after != before {
		t.Errorf("user count changed %d -> %d; a forbidden create must persist nothing", before, after)
	}
}

// TestRESTUserCreateAnonymous403 — Req 5.6: an unauthenticated caller (no
// session, no application password) holds no capability and is rejected 403
// rest_cannot_create for create, and persists no user.
func TestRESTUserCreateAnonymous403(t *testing.T) {
	h, repos := newRESTUserWriteRouter(t, &fakeSessions{})
	before := countUsers(t, repos)

	// No session cookie, no CSRF token, no Basic auth.
	req := httptest.NewRequest(http.MethodPost, "/wp-json/wp/v2/users",
		strings.NewReader(`{"username":"anon","email":"anon@example.com","password":"p"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body=%s)", rec.Code, rec.Body.String())
	}
	if code := decodeRESTErrCode(t, rec); code != "rest_cannot_create" {
		t.Errorf("error code = %q, want rest_cannot_create", code)
	}
	if after := countUsers(t, repos); after != before {
		t.Errorf("user count changed %d -> %d; an anonymous create must persist nothing", before, after)
	}
}

// TestRESTUserCreateRolesWithoutEditAndPromote403 — Req 6.1: a create carrying
// roles requires edit_users AND promote_users; a caller lacking either is
// rejected 403 rest_cannot_edit_roles and persists no user. An author holds
// neither. (The administrator holds both, so this gate is specifically the
// non-admin-with-roles case.)
func TestRESTUserCreateRolesWithoutEditAndPromote403(t *testing.T) {
	h, repos := newRESTUserWriteRouter(t, authorUserFake())
	before := countUsers(t, repos)

	body := `{"username":"withroles","email":"withroles@example.com","password":"p","roles":["editor"]}`
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, userCreateReq(body))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body=%s)", rec.Code, rec.Body.String())
	}
	if code := decodeRESTErrCode(t, rec); code != "rest_cannot_edit_roles" {
		t.Errorf("error code = %q, want rest_cannot_edit_roles", code)
	}
	if after := countUsers(t, repos); after != before {
		t.Errorf("user count changed %d -> %d; a forbidden-roles create must persist nothing", before, after)
	}
}

// TestRESTUserCreateMalformedJSON400 — Req 7.5: a body that is not valid JSON
// is rejected 400 rest_invalid_param by the handler's decode step.
func TestRESTUserCreateMalformedJSON400(t *testing.T) {
	h, _ := newRESTUserWriteRouter(t, adminUserFake())

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, userCreateReq(`{"username":`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
	}
	if code := decodeRESTErrCode(t, rec); code != "rest_invalid_param" {
		t.Errorf("error code = %q, want rest_invalid_param", code)
	}
}

// TestRESTUserCreateSanitizesDescriptionBeforeStore — Req 1.7/11.1: a create
// carrying a description with markup must have that value sanitized through the
// Policy BEFORE it reaches the store. This asserts the PERSISTED state: the
// description read back from {prefix}usermeta under the prefixless key
// "description" (D7) must not contain the inert <script>, proving the store saw
// sanitized content rather than merely an escaped response. The real
// service/real storage path makes this a persisted-state assertion, so a
// transport that bypassed the UserService Policy could not pass.
//
// The caller authenticates via createOnlyUserFake() — a principal holding ONLY
// the explicit create_users grant, so it is a tier-B writer (no
// unfiltered_html). An administrator would hold unfiltered_html and therefore
// get sanitize tier C = byte-identical passthrough (Req 4.2/11.2, design D8),
// which correctly preserves the markup — so this sanitization assertion must
// be made against a tier-B creator, not the admin.
func TestRESTUserCreateSanitizesDescriptionBeforeStore(t *testing.T) {
	h, repos := newRESTUserWriteRouter(t, createOnlyUserFake())

	const descIn = "Hello <script>alert('xss')</script> world"
	body := `{"username":"biouser","email":"biouser@example.com","password":"p",` +
		`"description":"Hello <script>alert('xss')</script> world"}`
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, userCreateReq(body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body=%s)", rec.Code, rec.Body.String())
	}

	var env map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	id := int64(env["id"].(float64))

	stored, err := repos.UserMeta.Get(context.Background(), id, "description")
	if err != nil {
		t.Fatalf("repos.UserMeta.Get(%d, description): %v (the description never reached the store)", id, err)
	}
	if strings.Contains(stored, "<script") || strings.Contains(stored, "</script>") {
		t.Errorf("stored description retained inert markup: %q — the Policy must sanitize before persist", stored)
	}
	// Strengthen to the real policy oracle: the stored value must equal exactly
	// what the shared sanitize policy produces for this tier-B creator, so the
	// test tracks the real tiering rather than a weaker "no <script>" check.
	want, err := sanitize.New().Sanitize(sanitize.PostContent, sanitize.For(auth.NewPrincipal(3, "creator", []string{"create_users"})), descIn)
	if err != nil {
		t.Fatalf("oracle sanitize: %v", err)
	}
	if stored != want {
		t.Errorf("stored description = %q, want %q (the UserService Policy output for a tier-B creator)", stored, want)
	}
}

// Task 5.3 — REST user UPDATE transport tests (handleRESTUserUpdate),
// end-to-end through the router with a REAL content.UserService wired via
// WithUserWrites (Req 2.1, 5.3, 5.4, 5.5, 5.6, 6.1, 6.2, 7.1, 7.2, 7.4, 7.5,
// 7.6, 10.1, 10.2). Each case flows through the live chi router → the live
// UserService (which owns the self-vs-others decision against the stored row,
// the bcrypt password path, the role path, and the description Policy) → the
// live SQLite user ports, so a transport that bypassed the service could not
// pass.
//
// Written RED first (task 5.3): handleRESTUserUpdate does not exist yet
// (task 5.4), so registerRESTUsers still answers POST/PUT/PATCH /users/{id}
// with the restNotImplemented 501 stub. Every assertion below therefore fails
// on a status mismatch (got 501) or an envelope-decode failure, NOT a compile
// error — the harness and the helpers added here all resolve against symbols
// that already exist.

// userUpdateReq builds a method /users/{id} request with a JSON body,
// authenticated as a session carrying the matching CSRF token
// (userCreateCSRFToken), mirroring mediaUpdateReq. method is PUT/PATCH/POST per
// the WordPress single-item update verbs.
func userUpdateReq(method string, id int64, jsonBody string) *http.Request {
	target := "/wp-json/wp/v2/users/" + strconv.FormatInt(id, 10)
	req := httptest.NewRequest(method, target, strings.NewReader(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "grimoire_session", Value: "anything"})
	req.Header.Set("X-CSRF-Token", userCreateCSRFToken)
	return req
}

// userUpdateReqRaw is userUpdateReq with an arbitrary {id} path segment (e.g.
// "0" or "abc"), so the invalid-id cases can exercise non-numeric and
// non-positive ids that would not fit an int64 parameter, mirroring
// mediaUpdateReqRaw.
func userUpdateReqRaw(method, idSeg, jsonBody string) *http.Request {
	target := "/wp-json/wp/v2/users/" + idSeg
	req := httptest.NewRequest(method, target, strings.NewReader(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "grimoire_session", Value: "anything"})
	req.Header.Set("X-CSRF-Token", userCreateCSRFToken)
	return req
}

// selfEditFake authenticates as a plain-role user (RoleAuthor — holds NONE of
// create_users/edit_users/promote_users) whose resolved UserID equals id, so
// the self-edit path (id == actor.UserID) is exercised against a principal that
// could not pass the other-user edit_users gate. The matching session CSRF
// token clears the M4 synchronizer check. The caller MUST ensure a stored user
// row with this id exists (UpdateProfile does users.ByID first → 404 if
// absent); newRESTUserWriteRouterWithUser provisions exactly that.
func selfEditFake(id int64) *fakeSessions {
	return &fakeSessions{
		authPrincipal: auth.NewPrincipal(id, "selfedit", []string{auth.RoleAuthor}),
		authSession:   domain.Session{ID: "sself", UserID: id, CSRFToken: userCreateCSRFToken},
	}
}

// newRESTUserWriteRouterWithUser is newRESTUserWriteRouter plus a known,
// updatable NON-admin user provisioned through the SAME real UserService
// (seedLogin/seedEmail), returning the router, the Repositories, and the
// seeded user's id. It exists for the update matrix cases that need a stored
// target whose id is known up front — chiefly the self-edit-without-edit_users
// case, where the authenticating principal's UserID must equal a real stored
// row (UpdateProfile loads users.ByID before the self-vs-others decision, so a
// non-existent id would 404 before the self-path is reached). The seeded user
// is created as the administrator (holds create_users) through the live write
// path, so the row and its usermeta are genuinely persisted.
func newRESTUserWriteRouterWithUser(t *testing.T, fake *fakeSessions, seedLogin, seedEmail string) (http.Handler, *storage.Repositories, int64) {
	t.Helper()
	h, repos := newRESTUserWriteRouter(t, fake)
	ctx := context.Background()

	// Provision the known target through the real service, exactly as the
	// harness provisions the duplicate fixture, so the row is a genuine stored
	// row (not a transport artifact). The admin principal holds create_users.
	admin1 := auth.NewPrincipal(1, "admin", []string{auth.RoleAdministrator})
	userWrite := content.NewUserService(repos.Users, repos.UserMeta, "wp_", content.WithUserContentPolicy(sanitize.New()))
	id, err := userWrite.Create(ctx, admin1, domain.User{
		Login:       seedLogin,
		Nicename:    seedLogin,
		Email:       seedEmail,
		DisplayName: seedLogin,
	}, "seed-pass")
	if err != nil {
		t.Fatalf("seed target user via UserService.Create: %v", err)
	}
	return h, repos, id
}

// TestRESTUserUpdateSuccessReturns200EditEnvelopeAndStores — Req 7.1/7.6: a
// stored user edits their own name/description via PUT /users/{id} and the
// endpoint responds 200 with the same edit-context envelope GET /users/{id}
// returns (via mapRESTUser → RESTUserEdit) — id, email present (edit context),
// _links.self/collection — and the change reaches storage, read back through
// repos.Users.ByID / repos.UserMeta.Get.
func TestRESTUserUpdateSuccessReturns200EditEnvelopeAndStores(t *testing.T) {
	// The admin edits their OWN record (id 1); admin holds edit_users so this
	// is the plain success path. The self-edit-WITHOUT-edit_users guarantee is
	// a separate test below.
	h, repos := newRESTUserWriteRouter(t, adminUserFake())

	body := `{"name":"Updated Admin","description":"A fresh bio"}`
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, userUpdateReq(http.MethodPut, 1, body))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}

	var env map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode 200 envelope %q: %v", rec.Body.String(), err)
	}
	if idF, ok := env["id"].(float64); !ok || int64(idF) != 1 {
		t.Fatalf("envelope id = %v, want 1", env["id"])
	}
	// Edit-context envelope carries email (RESTUserEdit); a view-context body
	// would omit it.
	if _, ok := env["email"]; !ok {
		t.Errorf("envelope missing email; an edit-context body must carry it (got %v)", env)
	}
	links, ok := env["_links"].(map[string]any)
	if !ok {
		t.Fatalf("envelope missing _links; got %v", env)
	}
	if _, ok := links["self"]; !ok {
		t.Errorf("envelope _links missing self; got %v", links)
	}
	if _, ok := links["collection"]; !ok {
		t.Errorf("envelope _links missing collection; got %v", links)
	}

	// The change must be in storage — a transport that bypassed the service
	// could not satisfy this.
	stored, err := repos.Users.ByID(context.Background(), 1)
	if err != nil {
		t.Fatalf("repos.Users.ByID(1): %v", err)
	}
	if stored.DisplayName != "Updated Admin" {
		t.Errorf("stored DisplayName = %q, want Updated Admin", stored.DisplayName)
	}
	desc, err := repos.UserMeta.Get(context.Background(), 1, "description")
	if err != nil {
		t.Fatalf("repos.UserMeta.Get(1, description): %v (the description never reached the store)", err)
	}
	if desc != "A fresh bio" {
		t.Errorf("stored description = %q, want A fresh bio", desc)
	}
}

// TestRESTUserUpdateInvalidID404 — Req 7.4: an invalid/non-positive {id} ("0")
// and a non-numeric {id} ("abc") are both rejected 404 rest_user_invalid_id
// via the raw-segment request path, matching handleRESTUserSingle.
func TestRESTUserUpdateInvalidID404(t *testing.T) {
	cases := map[string]string{
		"zero id":        "0",
		"non-numeric id": "abc",
	}
	for name, seg := range cases {
		t.Run(name, func(t *testing.T) {
			h, _ := newRESTUserWriteRouter(t, adminUserFake())
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, userUpdateReqRaw(http.MethodPut, seg, `{"name":"x"}`))
			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404 (body=%s)", rec.Code, rec.Body.String())
			}
			if code := decodeRESTErrCode(t, rec); code != "rest_user_invalid_id" {
				t.Errorf("error code = %q, want rest_user_invalid_id", code)
			}
		})
	}
}

// TestRESTUserUpdateUnknownUser404 — Req 7.4: a well-formed but unknown {id}
// (domain.ErrNotFound from the store) is rejected 404 rest_user_invalid_id.
func TestRESTUserUpdateUnknownUser404(t *testing.T) {
	h, _ := newRESTUserWriteRouter(t, adminUserFake())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, userUpdateReq(http.MethodPut, 999999, `{"name":"ghost"}`))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body=%s)", rec.Code, rec.Body.String())
	}
	if code := decodeRESTErrCode(t, rec); code != "rest_user_invalid_id" {
		t.Errorf("error code = %q, want rest_user_invalid_id", code)
	}
}

// TestRESTUserUpdateMalformedJSON400 — Req 7.5: a body that is not valid JSON
// is rejected 400 rest_invalid_param by the handler's decode step.
func TestRESTUserUpdateMalformedJSON400(t *testing.T) {
	h, _ := newRESTUserWriteRouter(t, adminUserFake())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, userUpdateReq(http.MethodPut, 1, `{"name":`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
	}
	if code := decodeRESTErrCode(t, rec); code != "rest_invalid_param" {
		t.Errorf("error code = %q, want rest_invalid_param", code)
	}
}

// TestRESTUserUpdatePartialLeavesOmittedUnchanged — Req 7.2: an update that
// supplies ONLY name must leave the user's email/url/nicename/description
// unchanged. The target is a provisioned non-admin user with known stored
// values; the admin (edit_users) performs the partial update, and the
// before/after snapshot via repos proves the omitted fields were untouched.
func TestRESTUserUpdatePartialLeavesOmittedUnchanged(t *testing.T) {
	h, repos, id := newRESTUserWriteRouterWithUser(t, adminUserFake(), "partial", "partial@example.com")
	ctx := context.Background()

	before, err := repos.Users.ByID(ctx, id)
	if err != nil {
		t.Fatalf("repos.Users.ByID(%d) before: %v", id, err)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, userUpdateReq(http.MethodPatch, id, `{"name":"Only Name Changed"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}

	after, err := repos.Users.ByID(ctx, id)
	if err != nil {
		t.Fatalf("repos.Users.ByID(%d) after: %v", id, err)
	}
	if after.DisplayName != "Only Name Changed" {
		t.Errorf("DisplayName = %q, want Only Name Changed (the one supplied field)", after.DisplayName)
	}
	if after.Email != before.Email {
		t.Errorf("Email changed %q -> %q; an omitted field must be left unchanged", before.Email, after.Email)
	}
	if after.URL != before.URL {
		t.Errorf("URL changed %q -> %q; an omitted field must be left unchanged", before.URL, after.URL)
	}
	if after.Nicename != before.Nicename {
		t.Errorf("Nicename changed %q -> %q; an omitted field must be left unchanged", before.Nicename, after.Nicename)
	}
}

// TestRESTUserUpdateSelfEditWithoutEditUsers200 — Req 5.3: a plain-role user
// (RoleAuthor, holds NO edit_users) may update their OWN profile. The target
// is provisioned first, and the request authenticates AS that same user id, so
// the self-edit path (id == actor.UserID) is exercised by a principal that
// could not pass the other-user edit_users gate.
func TestRESTUserUpdateSelfEditWithoutEditUsers200(t *testing.T) {
	// Build the router first to learn the target id, then re-wire a router
	// authenticated as that same id. Because newRESTUserWriteRouterWithUser's
	// seeded id is deterministic given the single seeded fixture + the harness
	// dupe + this seed, we provision the target through a throwaway admin
	// router to discover the id, then stand up the real self-edit router with a
	// principal whose UserID matches.
	//
	// Simpler and robust: provision as part of the SAME router we will exercise,
	// by seeding the target through newRESTUserWriteRouterWithUser with the
	// self-edit fake — the seed uses an independent admin principal (not the
	// router's fake), so the fake can be the self-editor whose UserID is the
	// seeded id. We can't know the id before seeding, so seed with a
	// placeholder fake, then assert the principal id matches via a second,
	// correctly-authenticated router over the SAME seeded login.
	//
	// Cleanest: seed once to discover the id, then build the self-edit router.
	discoverH, _, id := newRESTUserWriteRouterWithUser(t, adminUserFake(), "selfedit", "selfedit@example.com")
	_ = discoverH // only needed its seeded id

	h, repos, sameID := newRESTUserWriteRouterWithUser(t, selfEditFake(id), "selfedit", "selfedit@example.com")
	if sameID != id {
		t.Fatalf("seeded id drift: first=%d second=%d (the self-edit principal must match the stored row)", id, sameID)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, userUpdateReq(http.MethodPut, sameID, `{"name":"Self Updated","description":"my own bio"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — a self-edit must be permitted without edit_users (body=%s)", rec.Code, rec.Body.String())
	}

	stored, err := repos.Users.ByID(context.Background(), sameID)
	if err != nil {
		t.Fatalf("repos.Users.ByID(%d): %v", sameID, err)
	}
	if stored.DisplayName != "Self Updated" {
		t.Errorf("stored DisplayName = %q, want Self Updated", stored.DisplayName)
	}
}

// TestRESTUserUpdateOtherUserWithoutEditUsers403 — Req 5.4/5.5: an author
// (id 2, lacks edit_users) editing ANOTHER user's record (id 1, a real stored
// row) is rejected 403 rest_cannot_edit. The self-vs-others decision is made
// against the {id} path segment and the resolved principal, never a body value.
func TestRESTUserUpdateOtherUserWithoutEditUsers403(t *testing.T) {
	h, _ := newRESTUserWriteRouter(t, authorUserFake())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, userUpdateReq(http.MethodPut, 1, `{"name":"hijack"}`))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body=%s)", rec.Code, rec.Body.String())
	}
	if code := decodeRESTErrCode(t, rec); code != "rest_cannot_edit" {
		t.Errorf("error code = %q, want rest_cannot_edit", code)
	}
}

// TestRESTUserUpdatePasswordOnOtherWithoutEditUsers403 — Req 2.1/5.4: an author
// (id 2, lacks edit_users) setting a password on ANOTHER user's record (id 1)
// is rejected 403 rest_cannot_edit; no password is changed.
func TestRESTUserUpdatePasswordOnOtherWithoutEditUsers403(t *testing.T) {
	h, _ := newRESTUserWriteRouter(t, authorUserFake())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, userUpdateReq(http.MethodPut, 1, `{"password":"x"}`))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body=%s)", rec.Code, rec.Body.String())
	}
	if code := decodeRESTErrCode(t, rec); code != "rest_cannot_edit" {
		t.Errorf("error code = %q, want rest_cannot_edit", code)
	}
}

// TestRESTUserUpdateRolesWithoutEditAndPromote403 — Req 6.1: a caller lacking
// edit_users AND promote_users (an author) sending roles for ANOTHER user is
// rejected 403 rest_cannot_edit_roles. The role gate runs before any profile
// write, so this is distinct from the self-vs-others edit gate.
func TestRESTUserUpdateRolesWithoutEditAndPromote403(t *testing.T) {
	h, _ := newRESTUserWriteRouter(t, authorUserFake())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, userUpdateReq(http.MethodPut, 1, `{"roles":["editor"]}`))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body=%s)", rec.Code, rec.Body.String())
	}
	if code := decodeRESTErrCode(t, rec); code != "rest_cannot_edit_roles" {
		t.Errorf("error code = %q, want rest_cannot_edit_roles", code)
	}
}

// TestRESTUserUpdateSelfPromoteRejected403 — Req 6.2: a self-promote (roles on
// the caller's OWN record) is rejected 403 rest_cannot_edit_roles EVEN for an
// administrator who holds both edit_users and promote_users — no caller may
// change their own roles through REST.
func TestRESTUserUpdateSelfPromoteRejected403(t *testing.T) {
	h, _ := newRESTUserWriteRouter(t, adminUserFake()) // admin is id 1, holds edit_users+promote_users
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, userUpdateReq(http.MethodPut, 1, `{"roles":["administrator"]}`))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 — a self-promote must be rejected even for a promote_users holder (body=%s)", rec.Code, rec.Body.String())
	}
	if code := decodeRESTErrCode(t, rec); code != "rest_cannot_edit_roles" {
		t.Errorf("error code = %q, want rest_cannot_edit_roles", code)
	}
}

// TestRESTUserUpdatePOSTRoutesToUpdateNotCreate — Req 7.1: a POST to
// /users/{existing id} with a profile body behaves as an UPDATE (200 with the
// edit-context envelope for that id), NOT a create (which would be 201 and a
// new row). WordPress treats POST on a single item as an update.
func TestRESTUserUpdatePOSTRoutesToUpdateNotCreate(t *testing.T) {
	h, repos := newRESTUserWriteRouter(t, adminUserFake())
	before := countUsers(t, repos)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, userUpdateReq(http.MethodPost, 1, `{"name":"Via POST"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — POST /users/{id} must route to update, not create (body=%s)", rec.Code, rec.Body.String())
	}

	var env map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope %q: %v", rec.Body.String(), err)
	}
	if idF, ok := env["id"].(float64); !ok || int64(idF) != 1 {
		t.Fatalf("envelope id = %v, want 1 (an update returns the existing id, not a new one)", env["id"])
	}
	if after := countUsers(t, repos); after != before {
		t.Errorf("user count changed %d -> %d; a POST update must not create a new row", before, after)
	}
}

// Task 5.5 — registerRESTUsers nil-gate and 501-stub verification. The nil-gate
// layout itself landed with its sibling handler tasks (5.2 wired POST /users and
// POST/PUT/PATCH /users/{id} to the real handlers under `if s.userWrite != nil`,
// falling back to restNotImplemented stubs when nil; DELETE /users,
// DELETE /users/{id}, and the collection PUT/PATCH /users stay 501 regardless).
// These tests prove that wiring: the nil-fallback test proves Req 9.3 (a Server
// built without WithUserWrites keeps serving the 501 stubs rather than
// panicking), and the DELETE/collection test proves Req 8 (DELETE and the
// collection write verbs are never reinterpreted as writes, even with the
// service wired).

// newRESTUserNoWriteRouter mirrors newRESTUserWriteRouter EXACTLY, minus the
// .WithUserWrites(...) call, so the resulting Server has a nil s.userWrite. It
// builds the same real repos/mapper/admin surface and seeds the same fixtures,
// but — because no duplicate fixture is needed for the nil-gate cases and
// provisioning one would itself require a UserService — it skips the dupe seed.
// The REST user surface is still wired via WithREST, so the GET routes resolve
// and every write verb falls through to the registerRESTUsers nil branch
// (restNotImplemented → 501).
func newRESTUserNoWriteRouter(t *testing.T, fake *fakeSessions) http.Handler {
	t.Helper()
	ctx := context.Background()
	dsn := filepath.Join(t.TempDir(), "grimoire.db")
	cfg := config.DatabaseConfig{Vendor: "sqlite", DSN: dsn, TablePrefix: "wp_"}
	repos, err := storage.New(cfg)
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	t.Cleanup(func() { repos.Close() })

	migFS, err := storage.MigrationsFS(cfg.Vendor)
	if err != nil {
		t.Fatalf("MigrationsFS: %v", err)
	}
	if _, err := migrate.Apply(ctx, repos.DB(), migFS, cfg.Vendor, cfg.TablePrefix); err != nil {
		t.Fatalf("migrate.Apply: %v", err)
	}
	if err := storagetest.SeedFixtures(ctx, repos.DB(), cfg.Vendor, cfg.TablePrefix); err != nil {
		t.Fatalf("SeedFixtures: %v", err)
	}

	eng, err := render.Load(filepath.Join("..", "..", "themes"), "default")
	if err != nil {
		t.Fatalf("render.Load: %v", err)
	}
	adminSvc := content.NewAdminService(
		repos.AdminPosts, repos.PostWriter, repos.PostCounter,
		repos.UserCounter, repos.TermCounter, repos.Users,
	)
	mapper := content.NewRESTMapper(repos.PostTerms, repos.PostMeta, repos.UserMeta, "wp_")
	comments := content.NewCommentService(repos.Comments, repos.CommentWriter, repos.CommentMeta, repos.PostWriter, content.NewBasicCommentSpamFilter(content.BasicCommentSpamFilterConfig{}), sanitize.New())

	// Identical to newRESTUserWriteRouter EXCEPT the WithUserWrites call is
	// omitted, so s.userWrite stays nil and registerRESTUsers takes its
	// restNotImplemented fallback branch for every write verb (Req 9.3).
	srv := web.NewServer(
		content.NewPostService(repos.Posts).WithAuthors(repos.Users),
		content.NewTermService(repos.Terms, repos.Posts).WithHierarchy(repos.TermReader),
		content.NewOptionService(repos.Options),
		eng,
		nil,
	).WithAuth(fake, web.AuthConfig{}).
		WithAdmin(admin.Handler("/admin"), adminSvc).
		WithContentFeatures(comments, nil, nil).
		WithREST(mapper, repos.AdminPosts, repos.PostWriter, repos.Posts, repos.Media, repos.Users, 0)
	return srv.Routes()
}

// TestRESTUserWriteVerbsNilServiceFallBackTo501 — Req 9.3: a Server built
// WITHOUT WithUserWrites (s.userWrite == nil) keeps serving the existing
// restNotImplemented 501 stub for every single-item write verb — POST /users,
// POST/PUT/PATCH /users/{id} — rather than panicking or routing to a real
// handler. The caller is an authenticated administrator (adminUserFake, holds
// create_users/edit_users/promote_users with a matching CSRF token), so a 501 is
// unambiguously the nil-fallback stub and NOT an auth rejection (which would be
// 403) or a CSRF rejection.
func TestRESTUserWriteVerbsNilServiceFallBackTo501(t *testing.T) {
	cases := []struct {
		name string
		req  *http.Request
	}{
		{"POST /users", userCreateReq(`{"username":"x","email":"x@example.com","password":"p"}`)},
		{"POST /users/{id}", userUpdateReq(http.MethodPost, 1, `{"name":"x"}`)},
		{"PUT /users/{id}", userUpdateReq(http.MethodPut, 1, `{"name":"x"}`)},
		{"PATCH /users/{id}", userUpdateReq(http.MethodPatch, 1, `{"name":"x"}`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newRESTUserNoWriteRouter(t, adminUserFake())
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, tc.req)
			if rec.Code != http.StatusNotImplemented {
				t.Fatalf("status = %d, want 501 — a nil userWrite must keep the restNotImplemented stub (body=%s)", rec.Code, rec.Body.String())
			}
		})
	}
}

// TestRESTUserCollectionWriteStubsStay501 — even with the User_Write_Service
// FULLY WIRED (newRESTUserWriteRouter + adminUserFake), the COLLECTION write
// verbs DELETE/PUT/PATCH /users stay restNotImplemented 501 — there is no
// collection-level user write. (The single-item DELETE /users/{id} is live as
// of M11 — see rest_users_delete_test.go; it is no longer a stub and is not
// asserted here.)
func TestRESTUserCollectionWriteStubsStay501(t *testing.T) {
	deleteReq := func(target string) *http.Request {
		req := httptest.NewRequest(http.MethodDelete, target, nil)
		req.AddCookie(&http.Cookie{Name: "grimoire_session", Value: "anything"})
		req.Header.Set("X-CSRF-Token", userCreateCSRFToken)
		return req
	}
	collectionReq := func(method string) *http.Request {
		req := httptest.NewRequest(method, "/wp-json/wp/v2/users", strings.NewReader(`{"name":"x"}`))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: "grimoire_session", Value: "anything"})
		req.Header.Set("X-CSRF-Token", userCreateCSRFToken)
		return req
	}
	cases := []struct {
		name string
		req  *http.Request
	}{
		{"DELETE /users", deleteReq("/wp-json/wp/v2/users")},
		{"PUT /users (collection)", collectionReq(http.MethodPut)},
		{"PATCH /users (collection)", collectionReq(http.MethodPatch)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, _ := newRESTUserWriteRouter(t, adminUserFake())
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, tc.req)
			if rec.Code != http.StatusNotImplemented {
				t.Fatalf("status = %d, want 501 — DELETE and the collection write verbs must stay stubs even with the service wired (body=%s)", rec.Code, rec.Body.String())
			}
		})
	}
}

// Task 7.1 — the capability-matrix transport test for the REST user write
// surface, table-driven in one place (Req 5.1-5.6, 6.1, 6.2, 10.1, 10.2, 12.1).
// Every principal is built through the REAL auth.CapabilitiesForRoles /
// auth.NewPrincipal resolution, the same source of truth the live UserService
// consults, so a cell cannot pass by a transport that disagrees with the
// capability model. The matrix exercises the INTERESTING in-between cells that
// a pure default-role set cannot reach: because create_users / edit_users /
// promote_users are administrator-ONLY by default, a default-role matrix is
// trivially "admin yes, everyone else no". To make the gates meaningful we also
// build explicit-capability principals via auth.NewPrincipal with raw cap keys
// (any non-role key is retained as an explicit grant): a create_users-only
// holder, an edit_users-only holder, and an edit_users+promote_users holder.

// userMatrixFake wraps a matrix principal in the fakeSessions shape the user
// write request helpers (userCreateReq / userUpdateReq) expect: a session
// carrying the shared userCreateCSRFToken, so the request clears the M4
// synchronizer check (requireSessionCSRFREST) and reaches the capability
// decision rather than being rejected 403 rest_forbidden first (Req 10.2). It
// is the user analogue of matrixFake, differing only in the CSRF token the
// user request helpers set. An anonymous case uses a bare &fakeSessions{}
// (callers pass nil and the test substitutes it).
func userMatrixFake(p auth.Principal) *fakeSessions {
	return &fakeSessions{
		authPrincipal: p,
		authSession:   domain.Session{ID: "sm", UserID: p.UserID, CSRFToken: userCreateCSRFToken},
	}
}

// TestRESTUserCreateCapabilityMatrix — Req 5.1, 5.6, 12.1: create is permitted
// EXACTLY for create_users holders and 403 rest_cannot_create otherwise.
// anonymous and the default subscriber/contributor/author roles lack
// create_users → 403; an explicit create_users holder and an administrator →
// 201. Each principal is built through the real auth resolution; the explicit
// create_users principal is the in-between cell no default non-admin role can
// provide.
func TestRESTUserCreateCapabilityMatrix(t *testing.T) {
	cases := []struct {
		name     string
		fake     *fakeSessions // nil → anonymous (bare &fakeSessions{})
		body     string
		wantCode int
		wantErr  string // "" when wantCode is 201
	}{
		{"anonymous", nil,
			`{"username":"m_anon","email":"m_anon@example.com","password":"p"}`,
			http.StatusForbidden, "rest_cannot_create"},
		{"subscriber", userMatrixFake(auth.NewPrincipal(10, "sub", []string{auth.RoleSubscriber})),
			`{"username":"m_sub","email":"m_sub@example.com","password":"p"}`,
			http.StatusForbidden, "rest_cannot_create"},
		{"contributor", userMatrixFake(auth.NewPrincipal(11, "contrib", []string{auth.RoleContributor})),
			`{"username":"m_contrib","email":"m_contrib@example.com","password":"p"}`,
			http.StatusForbidden, "rest_cannot_create"},
		{"author", userMatrixFake(auth.NewPrincipal(12, "author", []string{auth.RoleAuthor})),
			`{"username":"m_author","email":"m_author@example.com","password":"p"}`,
			http.StatusForbidden, "rest_cannot_create"},
		{"editor", userMatrixFake(auth.NewPrincipal(13, "editor", []string{auth.RoleEditor})),
			`{"username":"m_editor","email":"m_editor@example.com","password":"p"}`,
			http.StatusForbidden, "rest_cannot_create"},
		{"create_users holder", userMatrixFake(auth.NewPrincipal(14, "creator", []string{"create_users"})),
			`{"username":"m_creator","email":"m_creator@example.com","password":"p"}`,
			http.StatusCreated, ""},
		{"administrator", userMatrixFake(auth.NewPrincipal(1, "admin", []string{auth.RoleAdministrator})),
			`{"username":"m_admin","email":"m_admin@example.com","password":"p"}`,
			http.StatusCreated, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := tc.fake
			if fake == nil {
				fake = &fakeSessions{}
			}
			h, repos := newRESTUserWriteRouter(t, fake)
			before := countUsers(t, repos)

			var req *http.Request
			if tc.fake == nil {
				// Anonymous: no session cookie, no CSRF token.
				req = httptest.NewRequest(http.MethodPost, "/wp-json/wp/v2/users", strings.NewReader(tc.body))
				req.Header.Set("Content-Type", "application/json")
			} else {
				req = userCreateReq(tc.body)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d (body=%s)", rec.Code, tc.wantCode, rec.Body.String())
			}
			if tc.wantErr != "" {
				if code := decodeRESTErrCode(t, rec); code != tc.wantErr {
					t.Errorf("error code = %q, want %q", code, tc.wantErr)
				}
				// Fail closed: a rejected create persisted nothing.
				if after := countUsers(t, repos); after != before {
					t.Errorf("user count changed %d -> %d; a forbidden create must persist nothing", before, after)
				}
			}
		})
	}
}

// TestRESTUserUpdateSelfVsOtherMatrix — Req 5.3, 5.4, 5.5, 12.1: the
// self-vs-others decision is made against the STORED {id} path segment versus
// the resolved principal's user ID, never a body value. A self-update
// (id == actor.UserID) is permitted WITHOUT edit_users; an other-user update
// requires edit_users (403 rest_cannot_edit otherwise). The "self without
// edit_users" cell uses a provisioned user authenticated as themselves
// (newRESTUserWriteRouterWithUser + selfEditFake, a plain RoleAuthor principal
// holding none of create/edit/promote_users). The other-user cells target the
// seeded admin row (id 1): a principal lacking edit_users → 403; an explicit
// edit_users holder and an administrator → 200.
func TestRESTUserUpdateSelfVsOtherMatrix(t *testing.T) {
	t.Run("self without edit_users 200", func(t *testing.T) {
		// Discover the deterministic seeded id, then stand up a router
		// authenticated AS that same id so id == actor.UserID exercises the
		// self path with a principal that could not pass the other-user gate.
		_, _, id := newRESTUserWriteRouterWithUser(t, adminUserFake(), "m_self", "m_self@example.com")
		h, repos, sameID := newRESTUserWriteRouterWithUser(t, selfEditFake(id), "m_self", "m_self@example.com")
		if sameID != id {
			t.Fatalf("seeded id drift: first=%d second=%d", id, sameID)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, userUpdateReq(http.MethodPut, sameID, `{"name":"Self Matrix","description":"self bio"}`))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 — a self-edit must be permitted without edit_users (body=%s)", rec.Code, rec.Body.String())
		}
		stored, err := repos.Users.ByID(context.Background(), sameID)
		if err != nil {
			t.Fatalf("repos.Users.ByID(%d): %v", sameID, err)
		}
		if stored.DisplayName != "Self Matrix" {
			t.Errorf("stored DisplayName = %q, want Self Matrix", stored.DisplayName)
		}
	})

	// Other-user cells, all targeting the seeded admin row (id 1), with a
	// principal whose own UserID differs from 1 so the edit is unambiguously an
	// "other user" edit.
	otherCases := []struct {
		name     string
		fake     *fakeSessions
		wantCode int
		wantErr  string // "" when wantCode is 200
	}{
		{"other without edit_users 403",
			userMatrixFake(auth.NewPrincipal(20, "nobody", []string{auth.RoleAuthor})),
			http.StatusForbidden, "rest_cannot_edit"},
		{"other with edit_users 200",
			userMatrixFake(auth.NewPrincipal(21, "editor_user", []string{"edit_users"})),
			http.StatusOK, ""},
		{"administrator other 200",
			userMatrixFake(auth.NewPrincipal(22, "admin2", []string{auth.RoleAdministrator})),
			http.StatusOK, ""},
	}
	for _, tc := range otherCases {
		t.Run(tc.name, func(t *testing.T) {
			h, _ := newRESTUserWriteRouter(t, tc.fake)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, userUpdateReq(http.MethodPut, 1, `{"name":"Edited By Other"}`))
			if rec.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d (body=%s)", rec.Code, tc.wantCode, rec.Body.String())
			}
			if tc.wantErr != "" {
				if code := decodeRESTErrCode(t, rec); code != tc.wantErr {
					t.Errorf("error code = %q, want %q", code, tc.wantErr)
				}
			}
		})
	}
}

// TestRESTUserRoleAssignmentMatrix — Req 6.1, 6.2, 12.1: role assignment
// requires edit_users AND promote_users (403 rest_cannot_edit_roles when either
// is missing), and a self-promote is rejected EVEN for a holder of both. The
// permitted cell (both caps, on ANOTHER user) returns 200 AND actually assigns
// the role, verified by reading the target's {prefix}capabilities back and
// decoding it. Each principal is built through the real auth resolution; the
// edit_users-only and edit_users+promote_users holders are the in-between cells
// no default non-admin role can provide.
func TestRESTUserRoleAssignmentMatrix(t *testing.T) {
	// Forbidden cells: roles supplied for ANOTHER user (the seeded admin row,
	// id 1) by a principal lacking edit_users and/or promote_users.
	forbidden := []struct {
		name string
		fake *fakeSessions
	}{
		{"neither edit nor promote (author)",
			userMatrixFake(auth.NewPrincipal(30, "p_author", []string{auth.RoleAuthor}))},
		{"edit_users only (no promote)",
			userMatrixFake(auth.NewPrincipal(31, "p_edit", []string{"edit_users"}))},
		{"promote_users only (no edit)",
			userMatrixFake(auth.NewPrincipal(32, "p_promote", []string{"promote_users"}))},
	}
	for _, tc := range forbidden {
		t.Run(tc.name, func(t *testing.T) {
			h, _ := newRESTUserWriteRouter(t, tc.fake)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, userUpdateReq(http.MethodPut, 1, `{"roles":["editor"]}`))
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403 (body=%s)", rec.Code, rec.Body.String())
			}
			if code := decodeRESTErrCode(t, rec); code != "rest_cannot_edit_roles" {
				t.Errorf("error code = %q, want rest_cannot_edit_roles", code)
			}
		})
	}

	// Self-promote rejected even for a holder of BOTH caps: the administrator
	// (id 1) assigning roles to their OWN record (id 1). No caller may change
	// their own roles through REST (Req 6.2).
	t.Run("self-promote rejected even for both-caps holder", func(t *testing.T) {
		h, _ := newRESTUserWriteRouter(t, adminUserFake()) // admin id 1 holds edit_users+promote_users
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, userUpdateReq(http.MethodPut, 1, `{"roles":["administrator"]}`))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403 — a self-promote must be rejected even for a promote_users holder (body=%s)", rec.Code, rec.Body.String())
		}
		if code := decodeRESTErrCode(t, rec); code != "rest_cannot_edit_roles" {
			t.Errorf("error code = %q, want rest_cannot_edit_roles", code)
		}
	})

	// Permitted cell: a principal holding BOTH edit_users AND promote_users
	// (whose own UserID differs from the target) assigns a role to ANOTHER user
	// (the seeded admin row, id 1) → 200, and the role is actually assigned.
	t.Run("both caps on another user 200 and assigns role", func(t *testing.T) {
		h, repos := newRESTUserWriteRouter(t, userMatrixFake(auth.NewPrincipal(33, "p_both", []string{"edit_users", "promote_users"})))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, userUpdateReq(http.MethodPut, 1, `{"roles":["editor"]}`))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
		// Verify the role actually landed in the target's capabilities meta.
		caps, err := repos.UserMeta.Get(context.Background(), 1, "wp_capabilities")
		if err != nil {
			t.Fatalf("repos.UserMeta.Get(1, wp_capabilities): %v (SetRoles never persisted)", err)
		}
		roles, err := auth.ParseCapabilities(caps)
		if err != nil {
			t.Fatalf("ParseCapabilities(%q): %v", caps, err)
		}
		if len(roles) != 1 || roles[0] != auth.RoleEditor {
			t.Errorf("assigned roles = %v, want [editor]", roles)
		}
	})
}

// newRESTUserWriteAppPasswordRouter is newRESTUserWriteRouter's sibling that
// ALSO wires Application Password auth (WithApplicationPasswords, requireTLS
// false so the transport gate is disabled for the test) and seeds user 1's
// {prefix}capabilities as administrator so a Verify of that user's Application
// Password resolves a principal holding create_users. It mirrors
// newRESTMediaWriteAppPasswordRouter and returns the ApplicationPasswords
// manager so a test can mint a real secret via mintAppPassword. This is the
// harness the AppPassword-skips-CSRF auth-matrix cell uses (Req 10.1): the base
// fixtures create the "admin" user row but no capabilities meta, so without the
// seed an AppPassword create would correctly 403 for lack of create_users —
// which would not exercise the CSRF-skip cell this is here to prove.
func newRESTUserWriteAppPasswordRouter(t *testing.T, fake *fakeSessions) (http.Handler, *auth.ApplicationPasswords) {
	t.Helper()
	ctx := context.Background()
	dsn := filepath.Join(t.TempDir(), "grimoire.db")
	cfg := config.DatabaseConfig{Vendor: "sqlite", DSN: dsn, TablePrefix: "wp_"}
	repos, err := storage.New(cfg)
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	t.Cleanup(func() { repos.Close() })

	migFS, err := storage.MigrationsFS(cfg.Vendor)
	if err != nil {
		t.Fatalf("MigrationsFS: %v", err)
	}
	if _, err := migrate.Apply(ctx, repos.DB(), migFS, cfg.Vendor, cfg.TablePrefix); err != nil {
		t.Fatalf("migrate.Apply: %v", err)
	}
	if err := storagetest.SeedFixtures(ctx, repos.DB(), cfg.Vendor, cfg.TablePrefix); err != nil {
		t.Fatalf("SeedFixtures: %v", err)
	}
	// Seed user 1's role so the Application-Password-resolved principal holds
	// create_users (administrator → create_users + edit_users + promote_users).
	if err := repos.UserMeta.Set(ctx, 1, "wp_capabilities", `a:1:{s:13:"administrator";b:1;}`); err != nil {
		t.Fatalf("seed wp_capabilities: %v", err)
	}

	eng, err := render.Load(filepath.Join("..", "..", "themes"), "default")
	if err != nil {
		t.Fatalf("render.Load: %v", err)
	}
	adminSvc := content.NewAdminService(
		repos.AdminPosts, repos.PostWriter, repos.PostCounter,
		repos.UserCounter, repos.TermCounter, repos.Users,
	)
	mapper := content.NewRESTMapper(repos.PostTerms, repos.PostMeta, repos.UserMeta, "wp_")
	userWrite := content.NewUserService(repos.Users, repos.UserMeta, "wp_", content.WithUserContentPolicy(sanitize.New()))
	comments := content.NewCommentService(repos.Comments, repos.CommentWriter, repos.CommentMeta, repos.PostWriter, content.NewBasicCommentSpamFilter(content.BasicCommentSpamFilterConfig{}), sanitize.New())
	ap := &auth.ApplicationPasswords{Users: repos.Users, Meta: repos.UserMeta, Prefix: "wp_"}

	srv := web.NewServer(
		content.NewPostService(repos.Posts).WithAuthors(repos.Users),
		content.NewTermService(repos.Terms, repos.Posts).WithHierarchy(repos.TermReader),
		content.NewOptionService(repos.Options),
		eng,
		nil,
	).WithAuth(fake, web.AuthConfig{}).
		WithAdmin(admin.Handler("/admin"), adminSvc).
		WithContentFeatures(comments, nil, nil).
		WithUserWrites(userWrite).
		WithREST(mapper, repos.AdminPosts, repos.PostWriter, repos.Posts, repos.Media, repos.Users, 0).
		WithApplicationPasswords(ap, false, "")
	return srv.Routes(), ap
}

// TestRESTUserWriteAuthMatrix — Req 10.1, 10.2, 12.1: the AppPassword-vs-session
// CSRF distinction on the user write surface, mirroring
// TestRESTMediaWriteAuthMatrix.
//
//   - Application Password auth skips CSRF: a POST /users authenticated with a
//     valid Application Password (user 1, an administrator holding create_users)
//     and NO X-CSRF-Token succeeds 201. isAppPasswordAuth short-circuits the
//     handler's CSRF branch.
//   - Session auth without a valid CSRF token → 403 rest_forbidden: the same
//     POST authenticated by a session cookie but with a missing (then
//     mismatched) X-CSRF-Token is rejected by requireSessionCSRFREST before any
//     capability or service work.
func TestRESTUserWriteAuthMatrix(t *testing.T) {
	t.Run("AppPasswordSkipsCSRF", func(t *testing.T) {
		// No session principal: auth is resolved purely via the Application
		// Password, so the request is CSRF-exempt.
		h, ap := newRESTUserWriteAppPasswordRouter(t, &fakeSessions{})
		secret := mintAppPassword(t, context.Background(), ap)

		body := `{"username":"viaapp","email":"viaapp@example.com","password":"s3cret-pass"}`
		req := httptest.NewRequest(http.MethodPost, "/wp-json/wp/v2/users", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.SetBasicAuth("admin", secret)
		req.RemoteAddr = "203.0.113.9:4242" // non-loopback; requireTLS=false so the gate is off
		// Deliberately NO X-CSRF-Token and NO session cookie.
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("AppPassword create status = %d, want 201 (CSRF must be skipped for App Password auth); body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("SessionWithoutCSRFForbidden", func(t *testing.T) {
		// A valid session principal (administrator, holds create_users) but no
		// valid CSRF token: the request must be rejected at the CSRF gate,
		// before any capability or service work.
		h, _ := newRESTUserWriteAppPasswordRouter(t, adminUserFake())

		body := `{"username":"nocsrf","email":"nocsrf@example.com","password":"p"}`

		// Missing X-CSRF-Token → 403 rest_forbidden.
		missing := httptest.NewRequest(http.MethodPost, "/wp-json/wp/v2/users", strings.NewReader(body))
		missing.Header.Set("Content-Type", "application/json")
		missing.AddCookie(&http.Cookie{Name: "grimoire_session", Value: "anything"})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, missing)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("session missing CSRF status = %d, want 403; body=%s", rec.Code, rec.Body.String())
		}
		if code := decodeRESTErrCode(t, rec); code != "rest_forbidden" {
			t.Errorf("session missing CSRF error code = %q, want rest_forbidden", code)
		}

		// Mismatched X-CSRF-Token → 403 rest_forbidden.
		mismatch := httptest.NewRequest(http.MethodPost, "/wp-json/wp/v2/users", strings.NewReader(body))
		mismatch.Header.Set("Content-Type", "application/json")
		mismatch.AddCookie(&http.Cookie{Name: "grimoire_session", Value: "anything"})
		mismatch.Header.Set("X-CSRF-Token", "wrong-token")
		rec2 := httptest.NewRecorder()
		h.ServeHTTP(rec2, mismatch)
		if rec2.Code != http.StatusForbidden {
			t.Fatalf("session mismatched CSRF status = %d, want 403; body=%s", rec2.Code, rec2.Body.String())
		}
		if code := decodeRESTErrCode(t, rec2); code != "rest_forbidden" {
			t.Errorf("session mismatched CSRF error code = %q, want rest_forbidden", code)
		}
	})
}

// Task 7.2 — content-safety and observability end-to-end tests for the REST
// user write surface (Req 4.1, 4.2, 7.6, 11.1, 12.2). These complement the
// create-path sanitization proof TestRESTUserCreateSanitizesDescriptionBeforeStore
// (a tier-B creator) by covering the UPDATE path, the write→read round-trip,
// and the tier-C byte-identity guarantee. Every case flows through the live chi
// router → the live content.UserService (which owns the description Policy and
// the self-vs-others decision) → the live SQLite user ports, so each assertion
// is on PERSISTED state, not merely an escaped response body — a transport that
// bypassed the UserService Policy could not pass.

// TestRESTUserUpdateDescriptionSanitizedBeforeWriter — Req 4.1, 11.1: a tier-B
// writer updating a description that carries markup must have that value
// sanitized through the Policy BEFORE it reaches the store. The actor is a
// provisioned user self-editing (selfEditFake — RoleAuthor, holds NO
// unfiltered_html, so tier B) via PUT /users/{id}; the submitted description
// contains an inert <script>. The assertion is on the PERSISTED value
// (repos.UserMeta.Get(id,"description")): it must contain no <script> and must
// equal exactly what the real shared policy produces for this tier-B actor
// (sanitize.New().Sanitize(PostContent, For(actor), descIn)). Asserting on
// stored state — not on the escaped response — is what proves the writer saw
// the sanitized value (Req 11.1).
func TestRESTUserUpdateDescriptionSanitizedBeforeWriter(t *testing.T) {
	// Discover the deterministic seeded id via a throwaway admin router, then
	// stand up the self-edit router over the same login so id == actor.UserID
	// exercises the tier-B self-edit path (the pattern the self-edit tests above
	// use).
	_, _, id := newRESTUserWriteRouterWithUser(t, adminUserFake(), "tierb", "tierb@example.com")
	h, repos, sameID := newRESTUserWriteRouterWithUser(t, selfEditFake(id), "tierb", "tierb@example.com")
	if sameID != id {
		t.Fatalf("seeded id drift: first=%d second=%d (the self-edit principal must match the stored row)", id, sameID)
	}

	const descIn = "Hello <script>alert(1)</script> world"
	body := `{"description":"Hello <script>alert(1)</script> world"}`
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, userUpdateReq(http.MethodPut, sameID, body))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}

	stored, err := repos.UserMeta.Get(context.Background(), sameID, "description")
	if err != nil {
		t.Fatalf("repos.UserMeta.Get(%d, description): %v (the description never reached the store)", sameID, err)
	}
	if strings.Contains(stored, "<script") || strings.Contains(stored, "</script>") {
		t.Errorf("stored description retained inert markup: %q — the Policy must sanitize before persist", stored)
	}
	// Strengthen to the real policy oracle: the stored value must equal exactly
	// what the shared sanitize policy produces for this tier-B self-editor, so
	// the test tracks the real tiering rather than a weaker "no <script>" check.
	// The actor principal is the one selfEditFake authenticates as.
	actor := auth.NewPrincipal(sameID, "selfedit", []string{auth.RoleAuthor})
	want, err := sanitize.New().Sanitize(sanitize.PostContent, sanitize.For(actor), descIn)
	if err != nil {
		t.Fatalf("oracle sanitize: %v", err)
	}
	if stored != want {
		t.Errorf("stored description = %q, want %q (the UserService Policy output for a tier-B writer)", stored, want)
	}
}

// TestRESTUserCreateUpdateGetRoundTripReflectsSanitized — Req 7.6: a
// create → update description → read round-trip returns the sanitized stored
// value, so the read path reflects exactly what the write path persisted.
//
// Note on the read shape: content.RESTUserEdit (the GET /users/{id} edit-context
// envelope) carries id/name/slug/link/email/url/roles and does NOT surface
// description — 10.C kept the read path unchanged (Req 9.1) and did not add
// description to RESTUserEdit. The round-trip is therefore asserted against the
// persisted store (repos.UserMeta.Get), which is the authoritative "what the
// read path would reflect" value, and the GET /users/{id} is additionally
// asserted to still succeed (200, not 500) and carry the unchanged edit-context
// shape (id/email/_links). The write (a tier-B self-editor PATCHing markup) and
// the subsequent read observe the SAME sanitized value — consistency across
// write→read.
func TestRESTUserCreateUpdateGetRoundTripReflectsSanitized(t *testing.T) {
	// A provisioned user (created through the real UserService during harness
	// seeding — the "create" leg of the round-trip) self-edits their own
	// description (tier B). Discover the id, then re-wire as that same user.
	_, _, id := newRESTUserWriteRouterWithUser(t, adminUserFake(), "roundtrip", "roundtrip@example.com")
	h, repos, sameID := newRESTUserWriteRouterWithUser(t, selfEditFake(id), "roundtrip", "roundtrip@example.com")
	if sameID != id {
		t.Fatalf("seeded id drift: first=%d second=%d", id, sameID)
	}

	const descIn = "Bio with <script>evil()</script> markup"
	patch := `{"description":"Bio with <script>evil()</script> markup"}`
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, userUpdateReq(http.MethodPatch, sameID, patch))
	if rec.Code != http.StatusOK {
		t.Fatalf("update status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}

	// The sanitized value the write persisted, per the real policy oracle.
	actor := auth.NewPrincipal(sameID, "selfedit", []string{auth.RoleAuthor})
	want, err := sanitize.New().Sanitize(sanitize.PostContent, sanitize.For(actor), descIn)
	if err != nil {
		t.Fatalf("oracle sanitize: %v", err)
	}

	// Round-trip assertion against stored state: a subsequent read observes the
	// SAME sanitized value the write persisted (Req 7.6).
	stored, err := repos.UserMeta.Get(context.Background(), sameID, "description")
	if err != nil {
		t.Fatalf("repos.UserMeta.Get(%d, description): %v", sameID, err)
	}
	if stored != want {
		t.Errorf("round-trip stored description = %q, want %q (read must reflect the sanitized write)", stored, want)
	}

	// GET /users/{id} THROUGH THE ROUTER must still succeed and carry the
	// unchanged edit-context shape. RESTUserEdit does not expose description, so
	// the body is not asserted to contain it; the GET's job here is to prove the
	// read path is intact (200, not 500) after the write (Req 9.1).
	getReq := httptest.NewRequest(http.MethodGet, "/wp-json/wp/v2/users/"+strconv.FormatInt(sameID, 10), nil)
	getReq.AddCookie(&http.Cookie{Name: "grimoire_session", Value: "anything"})
	getRec := httptest.NewRecorder()
	h.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET /users/%d status = %d, want 200 (body=%s)", sameID, getRec.Code, getRec.Body.String())
	}
	var env map[string]any
	if err := json.Unmarshal(getRec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode GET envelope %q: %v", getRec.Body.String(), err)
	}
	if idF, ok := env["id"].(float64); !ok || int64(idF) != sameID {
		t.Fatalf("GET envelope id = %v, want %d (the edit-context read shape must be unchanged)", env["id"], sameID)
	}
}

// TestRESTUserUpdateDescriptionUnfilteredHTMLSurvivesVerbatim — Req 4.2: a
// tier-C writer (an administrator holding unfiltered_html, editing their OWN
// record id 1) PUTs a description carrying markup the Policy demonstrably alters
// at tier B; the STORED description must be byte-identical to the submitted
// input. A precondition asserts a tier-B writer WOULD alter that same input, so
// the byte-identity at tier C is meaningful and not a coincidence — mirroring
// the content-package test TestUpdateProfileDescriptionUnfilteredHTMLSurvivesVerbatim.
func TestRESTUserUpdateDescriptionUnfilteredHTMLSurvivesVerbatim(t *testing.T) {
	h, repos := newRESTUserWriteRouter(t, adminUserFake()) // admin is id 1, holds unfiltered_html → tier C

	const descIn = "keep <script>alert(1)</script> me"

	// Precondition: a tier-B writer alters this input, so verbatim survival at
	// tier C is a meaningful assertion rather than a value the Policy leaves
	// untouched for everyone.
	tierB, err := sanitize.New().Sanitize(sanitize.PostContent, sanitize.For(auth.NewPrincipal(2, "tierb", []string{auth.RoleAuthor})), descIn)
	if err != nil {
		t.Fatalf("precondition oracle sanitize: %v", err)
	}
	if tierB == descIn {
		t.Fatalf("precondition: tier B must alter %q for this test to be meaningful", descIn)
	}

	body := `{"description":"keep <script>alert(1)</script> me"}`
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, userUpdateReq(http.MethodPut, 1, body))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}

	stored, err := repos.UserMeta.Get(context.Background(), 1, "description")
	if err != nil {
		t.Fatalf("repos.UserMeta.Get(1, description): %v", err)
	}
	if stored != descIn {
		t.Errorf("tier-C stored description = %q, want verbatim %q (unfiltered_html must pass through byte-identically)", stored, descIn)
	}
}
