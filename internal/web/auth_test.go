package web_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/render"
	"github.com/roboweaver/grimoire/internal/storage"
	"github.com/roboweaver/grimoire/internal/storage/migrate"
	"github.com/roboweaver/grimoire/internal/storage/storagetest"
	"github.com/roboweaver/grimoire/internal/web"
)

// fakeSessions is an in-memory Sessions implementation for handler tests.
type fakeSessions struct {
	loginToken     string
	loginPrincipal auth.Principal
	loginErr       error
	gotLoginUser   string
	gotLoginPw     string

	authPrincipal auth.Principal
	authSession   domain.Session
	authErr       error

	loggedOut string
}

func (f *fakeSessions) Login(_ context.Context, username, pw string) (string, auth.Principal, error) {
	f.gotLoginUser = username
	f.gotLoginPw = pw
	if f.loginErr != nil {
		return "", auth.Principal{}, f.loginErr
	}
	return f.loginToken, f.loginPrincipal, nil
}

func (f *fakeSessions) Authenticate(_ context.Context, token string) (auth.Principal, domain.Session, error) {
	if f.authErr != nil {
		return auth.Principal{}, domain.Session{}, f.authErr
	}
	return f.authPrincipal, f.authSession, nil
}

func (f *fakeSessions) Logout(_ context.Context, token string) error {
	f.loggedOut = token
	return nil
}

func newAuthServer(t *testing.T, fake web.Sessions) *web.Server {
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
	srv := web.NewServer(
		content.NewPostService(repos.Posts),
		content.NewTermService(repos.Terms, repos.Posts),
		content.NewOptionService(repos.Options),
		eng,
		nil,
	).WithAuth(fake, web.AuthConfig{})
	return srv
}

// getLoginCSRF performs GET /login and returns the CSRF cookie value the server
// issued (which equals the token embedded in the form).
func getLoginCSRF(t *testing.T, h http.Handler) (string, *http.Cookie) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /login = %d, want 200", rec.Code)
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == "grimoire_csrf" {
			return c.Value, c
		}
	}
	t.Fatalf("GET /login did not set a CSRF cookie")
	return "", nil
}

func cookieByName(cookies []*http.Cookie, name string) *http.Cookie {
	// Last-wins: multiple Set-Cookie headers for the same name may appear on one
	// response (e.g. session middleware refreshes the cookie, then the logout
	// handler clears it); a browser applies them in order, so the last wins.
	var found *http.Cookie
	for _, c := range cookies {
		if c.Name == name {
			found = c
		}
	}
	return found
}

func TestLoginGETRendersFormWithCSRF(t *testing.T) {
	h := newAuthServer(t, &fakeSessions{}).Routes()
	csrf, cookie := getLoginCSRF(t, h)
	if csrf == "" {
		t.Fatal("empty CSRF token")
	}
	if !cookie.HttpOnly {
		t.Error("CSRF cookie not HttpOnly")
	}
	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, `name="csrf_token"`) {
		t.Errorf("login form missing csrf_token field:\n%s", body)
	}
	if !strings.Contains(body, `name="log"`) || !strings.Contains(body, `name="pwd"`) {
		t.Errorf("login form missing credential fields:\n%s", body)
	}
}

func postLogin(t *testing.T, h http.Handler, user, pw, csrf string, csrfCookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{"log": {user}, "pwd": {pw}, "csrf_token": {csrf}, "redirect": {"/"}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if csrfCookie != nil {
		req.AddCookie(csrfCookie)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestLoginPOSTSuccessSetsCookieAndRedirects(t *testing.T) {
	fake := &fakeSessions{loginToken: "session-token-123", loginPrincipal: auth.NewPrincipal(1, "admin", []string{"administrator"})}
	h := newAuthServer(t, fake).Routes()
	csrf, cookie := getLoginCSRF(t, h)

	rec := postLogin(t, h, "admin", "correct-horse", csrf, cookie)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /login = %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/" {
		t.Errorf("redirect Location = %q, want /", loc)
	}
	sc := cookieByName(rec.Result().Cookies(), "grimoire_session")
	if sc == nil {
		t.Fatal("no session cookie set on success")
	}
	if sc.Value != "session-token-123" {
		t.Errorf("session cookie value = %q", sc.Value)
	}
	if !sc.HttpOnly {
		t.Error("session cookie not HttpOnly")
	}
	if sc.SameSite != http.SameSiteLaxMode {
		t.Errorf("session cookie SameSite = %v, want Lax", sc.SameSite)
	}
	if fake.gotLoginUser != "admin" || fake.gotLoginPw != "correct-horse" {
		t.Errorf("Login got (%q,%q)", fake.gotLoginUser, fake.gotLoginPw)
	}
}

func TestLoginPOSTNoUserEnumeration(t *testing.T) {
	// Unknown user and wrong password both yield ErrInvalidCredentials; the
	// handler must produce byte-identical responses (same status, same body).
	respFor := func(user, pw string) (int, string) {
		fake := &fakeSessions{loginErr: auth.ErrInvalidCredentials}
		h := newAuthServer(t, fake).Routes()
		csrf, cookie := getLoginCSRF(t, h)
		rec := postLogin(t, h, user, pw, csrf, cookie)
		// Strip the CSRF token (which is random per request) so bodies compare.
		body := rec.Body.String()
		return rec.Code, stripCSRF(body)
	}
	code1, body1 := respFor("ghost", "whatever")
	code2, body2 := respFor("admin", "wrong-password")
	if code1 != http.StatusUnauthorized || code2 != http.StatusUnauthorized {
		t.Fatalf("codes = %d, %d, want 401,401", code1, code2)
	}
	if body1 != body2 {
		t.Errorf("enumeration: bodies differ\nunknown: %s\nwrongpw: %s", body1, body2)
	}
	if !strings.Contains(body1, "Invalid username or password") {
		t.Errorf("missing generic error message: %s", body1)
	}
}

// stripCSRF removes the random csrf_token hidden-input value so two failed-login
// bodies can be compared for enumeration leaks.
func stripCSRF(body string) string {
	const marker = `name="csrf_token" value="`
	i := strings.Index(body, marker)
	if i < 0 {
		return body
	}
	start := i + len(marker)
	end := strings.IndexByte(body[start:], '"')
	if end < 0 {
		return body
	}
	return body[:start] + body[start+end:]
}

func TestLoginPOSTFailureNoSessionCookie(t *testing.T) {
	fake := &fakeSessions{loginErr: auth.ErrInvalidCredentials}
	h := newAuthServer(t, fake).Routes()
	csrf, cookie := getLoginCSRF(t, h)
	rec := postLogin(t, h, "admin", "nope", csrf, cookie)
	if sc := cookieByName(rec.Result().Cookies(), "grimoire_session"); sc != nil && sc.Value != "" && sc.MaxAge >= 0 {
		t.Errorf("failed login must not set a live session cookie: %+v", sc)
	}
}

func TestLoginPOSTBadCSRFForbidden(t *testing.T) {
	fake := &fakeSessions{loginToken: "tok"}
	h := newAuthServer(t, fake).Routes()
	_, cookie := getLoginCSRF(t, h)
	// Wrong CSRF field value vs the cookie.
	rec := postLogin(t, h, "admin", "pw", "not-the-right-token", cookie)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("bad CSRF = %d, want 403", rec.Code)
	}
	if fake.gotLoginUser != "" {
		t.Error("Login must not be called when CSRF fails")
	}
}

func TestLoginPOSTMissingCSRFCookieForbidden(t *testing.T) {
	fake := &fakeSessions{}
	h := newAuthServer(t, fake).Routes()
	// No CSRF cookie at all.
	rec := postLogin(t, h, "admin", "pw", "sometoken", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF cookie = %d, want 403", rec.Code)
	}
}

func TestLogoutRevokesAndClearsCookie(t *testing.T) {
	fake := &fakeSessions{
		authPrincipal: auth.NewPrincipal(1, "admin", []string{"administrator"}),
		authSession:   domain.Session{ID: "sid", UserID: 1, CSRFToken: "sync-token"},
	}
	h := newAuthServer(t, fake).Routes()

	form := url.Values{"csrf_token": {"sync-token"}}
	req := httptest.NewRequest(http.MethodPost, "/logout", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "grimoire_session", Value: "raw-token"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /logout = %d, want 303", rec.Code)
	}
	if fake.loggedOut != "raw-token" {
		t.Errorf("Logout got %q, want raw-token", fake.loggedOut)
	}
	sc := cookieByName(rec.Result().Cookies(), "grimoire_session")
	if sc == nil || sc.MaxAge >= 0 {
		t.Errorf("logout must clear the session cookie, got %+v", sc)
	}
}

func TestLogoutBadCSRFForbidden(t *testing.T) {
	fake := &fakeSessions{
		authPrincipal: auth.NewPrincipal(1, "admin", []string{"administrator"}),
		authSession:   domain.Session{ID: "sid", UserID: 1, CSRFToken: "sync-token"},
	}
	h := newAuthServer(t, fake).Routes()

	form := url.Values{"csrf_token": {"wrong"}}
	req := httptest.NewRequest(http.MethodPost, "/logout", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "grimoire_session", Value: "raw-token"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("logout bad CSRF = %d, want 403", rec.Code)
	}
	if fake.loggedOut != "" {
		t.Error("Logout must not run when CSRF fails")
	}
}

func TestSessionMiddlewareInjectsPrincipal(t *testing.T) {
	fake := &fakeSessions{
		authPrincipal: auth.NewPrincipal(7, "editor", []string{"editor"}),
		authSession:   domain.Session{ID: "sid", UserID: 7, CSRFToken: "x"},
	}
	srv := newAuthServer(t, fake)

	var gotLogin string
	var gotOK bool
	probe := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := web.PrincipalFrom(r.Context())
		gotOK = ok
		gotLogin = p.Login
		w.WriteHeader(http.StatusOK)
	})
	h := srv.SessionMiddleware(probe)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "grimoire_session", Value: "raw-token"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if !gotOK || gotLogin != "editor" {
		t.Fatalf("principal not injected: ok=%v login=%q", gotOK, gotLogin)
	}
}

func TestRequireCapability(t *testing.T) {
	fake := &fakeSessions{
		authPrincipal: auth.NewPrincipal(2, "author", []string{"author"}),
		authSession:   domain.Session{ID: "sid", UserID: 2},
	}
	srv := newAuthServer(t, fake)
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	// author lacks manage_options → 403.
	guard := srv.RequireCapability("manage_options")
	h := srv.SessionMiddleware(guard(ok))
	req := httptest.NewRequest(http.MethodPost, "/admin", nil)
	req.AddCookie(&http.Cookie{Name: "grimoire_session", Value: "raw-token"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("author manage_options = %d, want 403", rec.Code)
	}

	// author holds edit_posts → allowed.
	guard2 := srv.RequireCapability("edit_posts")
	h2 := srv.SessionMiddleware(guard2(ok))
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/admin", nil)
	req2.AddCookie(&http.Cookie{Name: "grimoire_session", Value: "raw-token"})
	h2.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("author edit_posts = %d, want 200", rec2.Code)
	}
}

func TestRequireCapabilityAnonymousRedirects(t *testing.T) {
	srv := newAuthServer(t, &fakeSessions{authErr: auth.ErrNoSession})
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	guard := srv.RequireCapability("manage_options")
	h := srv.SessionMiddleware(guard(ok))
	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("anonymous GET guarded = %d, want 303 to login", rec.Code)
	}
}
