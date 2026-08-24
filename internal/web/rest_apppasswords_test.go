package web_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/domain"
)

func TestRESTAppPasswordsListNeverIncludesSecret(t *testing.T) {
	fake := &fakeSessions{
		authPrincipal: auth.Principal{UserID: 1, Login: "admin"},
		authSession:   domain.Session{ID: "s1", CSRFToken: "tok"},
	}
	h, _, ap := newAppPasswordRESTRouter(t, fake, false, "")
	if _, _, err := ap.Create(context.Background(), 1, "existing"); err != nil {
		t.Fatalf("seed Create: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/wp-json/wp/v2/users/me/application-passwords", nil)
	req.AddCookie(&http.Cookie{Name: "grimoire_session", Value: "anything"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "$generic$") || strings.Contains(rec.Body.String(), "\"hash\"") || strings.Contains(rec.Body.String(), "\"password\"") {
		t.Fatalf("list response leaked hash/secret: %s", rec.Body.String())
	}
	var out []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("len(out) = %d, want 1", len(out))
	}
	if out[0]["name"] != "existing" {
		t.Errorf("name = %v, want existing", out[0]["name"])
	}
	if _, ok := out[0]["uuid"]; !ok {
		t.Errorf("missing uuid field: %v", out[0])
	}
}

func TestRESTAppPasswordsCreateReturnsSecretOnceAndItVerifies(t *testing.T) {
	fake := &fakeSessions{
		authPrincipal: auth.Principal{UserID: 1, Login: "admin"},
		authSession:   domain.Session{ID: "s1", CSRFToken: "tok"},
	}
	h, _, ap := newAppPasswordRESTRouter(t, fake, false, "")

	body := `{"name":"my new client"}`
	req := httptest.NewRequest(http.MethodPost, "/wp-json/wp/v2/users/me/application-passwords", strings.NewReader(body))
	req.AddCookie(&http.Cookie{Name: "grimoire_session", Value: "anything"})
	req.Header.Set("X-CSRF-Token", "tok")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		UUID     string `json:"uuid"`
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Name != "my new client" {
		t.Errorf("name = %q, want %q", out.Name, "my new client")
	}
	if out.Password == "" {
		t.Fatalf("password missing from create response")
	}

	// The returned secret verifies as a real Application Password for the
	// caller.
	if _, err := ap.Verify(context.Background(), "admin", out.Password, "127.0.0.1"); err != nil {
		t.Fatalf("Verify with returned secret: %v", err)
	}

	// It never appears again in a subsequent list.
	listReq := httptest.NewRequest(http.MethodGet, "/wp-json/wp/v2/users/me/application-passwords", nil)
	listReq.AddCookie(&http.Cookie{Name: "grimoire_session", Value: "anything"})
	listRec := httptest.NewRecorder()
	h.ServeHTTP(listRec, listReq)
	if strings.Contains(listRec.Body.String(), out.Password) {
		t.Fatalf("list response leaked the plaintext secret: %s", listRec.Body.String())
	}
}

func TestRESTAppPasswordsRevokeInvalidatesOldSecret(t *testing.T) {
	fake := &fakeSessions{
		authPrincipal: auth.Principal{UserID: 1, Login: "admin"},
		authSession:   domain.Session{ID: "s1", CSRFToken: "tok"},
	}
	h, _, ap := newAppPasswordRESTRouter(t, fake, false, "")
	rec0, secret, err := ap.Create(context.Background(), 1, "to be revoked")
	if err != nil {
		t.Fatalf("seed Create: %v", err)
	}
	if _, err := ap.Verify(context.Background(), "admin", secret, "127.0.0.1"); err != nil {
		t.Fatalf("sanity Verify before revoke: %v", err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/wp-json/wp/v2/users/me/application-passwords/"+rec0.UUID, nil)
	req.AddCookie(&http.Cookie{Name: "grimoire_session", Value: "anything"})
	req.Header.Set("X-CSRF-Token", "tok")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	if _, err := ap.Verify(context.Background(), "admin", secret, "127.0.0.1"); err == nil {
		t.Fatalf("revoked secret still verifies")
	}
}

func TestRESTAppPasswordsAllThreeRequireSessionNotAppPassword(t *testing.T) {
	h, _, ap := newAppPasswordRESTRouter(t, &fakeSessions{}, false, "")
	secret := mintAppPassword(t, context.Background(), ap)

	cases := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"list", http.MethodGet, "/wp-json/wp/v2/users/me/application-passwords", ""},
		{"create", http.MethodPost, "/wp-json/wp/v2/users/me/application-passwords", `{"name":"x"}`},
		{"revoke", http.MethodDelete, "/wp-json/wp/v2/users/me/application-passwords/nonexistent-uuid", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var req *http.Request
			if tc.body != "" {
				req = httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			} else {
				req = httptest.NewRequest(tc.method, tc.path, nil)
			}
			req.SetBasicAuth("admin", secret)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("%s: status = %d, want 401; body=%s", tc.name, rec.Code, rec.Body.String())
			}
			if code := decodeRESTErrCode(t, rec); code != "rest_not_logged_in" {
				t.Errorf("%s: error code = %q, want rest_not_logged_in", tc.name, code)
			}
		})
	}
}

func TestRESTAppPasswordsAnonymousRejected(t *testing.T) {
	h, _, _ := newAppPasswordRESTRouter(t, &fakeSessions{}, false, "")
	req := httptest.NewRequest(http.MethodGet, "/wp-json/wp/v2/users/me/application-passwords", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body.String())
	}
}

func TestRESTAppPasswordsCreateAndRevokeRequireCSRF(t *testing.T) {
	fake := &fakeSessions{
		authPrincipal: auth.Principal{UserID: 1, Login: "admin"},
		authSession:   domain.Session{ID: "s1", CSRFToken: "tok"},
	}
	h, _, ap := newAppPasswordRESTRouter(t, fake, false, "")
	rec0, _, err := ap.Create(context.Background(), 1, "csrf-target")
	if err != nil {
		t.Fatalf("seed Create: %v", err)
	}

	createReq := httptest.NewRequest(http.MethodPost, "/wp-json/wp/v2/users/me/application-passwords", strings.NewReader(`{"name":"x"}`))
	createReq.AddCookie(&http.Cookie{Name: "grimoire_session", Value: "anything"})
	createRec := httptest.NewRecorder()
	h.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusForbidden {
		t.Fatalf("create without CSRF: status = %d, want 403; body=%s", createRec.Code, createRec.Body.String())
	}

	revokeReq := httptest.NewRequest(http.MethodDelete, "/wp-json/wp/v2/users/me/application-passwords/"+rec0.UUID, nil)
	revokeReq.AddCookie(&http.Cookie{Name: "grimoire_session", Value: "anything"})
	revokeRec := httptest.NewRecorder()
	h.ServeHTTP(revokeRec, revokeReq)
	if revokeRec.Code != http.StatusForbidden {
		t.Fatalf("revoke without CSRF: status = %d, want 403; body=%s", revokeRec.Code, revokeRec.Body.String())
	}
}

func TestRESTAppPasswordsRevokeUnknownUUID404(t *testing.T) {
	fake := &fakeSessions{
		authPrincipal: auth.Principal{UserID: 1, Login: "admin"},
		authSession:   domain.Session{ID: "s1", CSRFToken: "tok"},
	}
	h, _, _ := newAppPasswordRESTRouter(t, fake, false, "")
	req := httptest.NewRequest(http.MethodDelete, "/wp-json/wp/v2/users/me/application-passwords/00000000-0000-0000-0000-000000000000", nil)
	req.AddCookie(&http.Cookie{Name: "grimoire_session", Value: "anything"})
	req.Header.Set("X-CSRF-Token", "tok")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

func TestRESTAppPasswordsRevokeAnotherUsersUUID404(t *testing.T) {
	ctx := context.Background()
	fake := &fakeSessions{
		authPrincipal: auth.Principal{UserID: 1, Login: "admin"},
		authSession:   domain.Session{ID: "s1", CSRFToken: "tok"},
	}
	h, repos, ap := newAppPasswordRESTRouter(t, fake, false, "")

	// Create a second user, then create an application password owned by
	// that user (not the caller, user #1).
	otherID, err := repos.Users.Create(ctx, domain.User{Login: "other", Email: "other@example.com", DisplayName: "Other"})
	if err != nil {
		t.Fatalf("create other user: %v", err)
	}
	otherRec, _, err := ap.Create(ctx, otherID, "other's password")
	if err != nil {
		t.Fatalf("create other user's app password: %v", err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/wp-json/wp/v2/users/me/application-passwords/"+otherRec.UUID, nil)
	req.AddCookie(&http.Cookie{Name: "grimoire_session", Value: "anything"})
	req.Header.Set("X-CSRF-Token", "tok")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}

	// And it still verifies: the caller's attempt on another user's UUID
	// had no effect.
	if _, err := ap.Verify(ctx, "other", "", ""); err == nil {
		t.Fatalf("unexpected success verifying with an empty secret")
	}
	if _, err := ap.List(ctx, otherID); err != nil {
		t.Fatalf("List other user's passwords: %v", err)
	}
}

func TestRESTAppPasswordsNotShadowedByUsersCatchAll(t *testing.T) {
	fake := &fakeSessions{
		authPrincipal: auth.Principal{UserID: 1, Login: "admin"},
		authSession:   domain.Session{ID: "s1", CSRFToken: "tok"},
	}
	h, _, _ := newAppPasswordRESTRouter(t, fake, false, "")
	req := httptest.NewRequest(http.MethodGet, "/wp-json/wp/v2/users/me/application-passwords", nil)
	req.AddCookie(&http.Cookie{Name: "grimoire_session", Value: "anything"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (must not be shadowed by /users 501 catch-all); body=%s", rec.Code, rec.Body.String())
	}
}
