package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/domain"
)

type fakeSessions struct {
	p auth.Principal
	s domain.Session
}

func (f fakeSessions) Login(context.Context, string, string) (string, auth.Principal, error) {
	return "", auth.Principal{}, auth.ErrInvalidCredentials
}
func (f fakeSessions) Authenticate(context.Context, string) (auth.Principal, domain.Session, error) {
	return f.p, f.s, nil
}
func (f fakeSessions) Logout(context.Context, string) error { return nil }

type fakeCommentAdmin struct {
	items     []domain.Comment
	total     int
	listErr   error
	statusOps []struct {
		action string
		id     int64
	}
	statusErr error
}

func (f *fakeCommentAdmin) List(context.Context, domain.CommentFilter) ([]domain.Comment, int, error) {
	return f.items, f.total, f.listErr
}
func (f *fakeCommentAdmin) Approve(context.Context, int64) error   { return f.set("approve") }
func (f *fakeCommentAdmin) Unapprove(context.Context, int64) error { return f.set("unapprove") }
func (f *fakeCommentAdmin) Trash(context.Context, int64) error     { return f.set("trash") }
func (f *fakeCommentAdmin) Untrash(context.Context, int64) error   { return f.set("untrash") }
func (f *fakeCommentAdmin) MarkSpam(context.Context, int64) error  { return f.set("spam") }
func (f *fakeCommentAdmin) NotSpam(context.Context, int64) error   { return f.set("not-spam") }
func (f *fakeCommentAdmin) set(a string) error {
	f.statusOps = append(f.statusOps, struct {
		action string
		id     int64
	}{a, 1})
	return f.statusErr
}

type fakeMediaAdmin struct {
	items     []domain.Media
	total     int
	stored    domain.Media
	storeErr  error
	get       domain.Media
	getErr    error
	deleteErr error
}

func (f *fakeMediaAdmin) List(context.Context, domain.MediaFilter) ([]domain.Media, int, error) {
	return f.items, f.total, nil
}
func (f *fakeMediaAdmin) Store(context.Context, *bytes.Reader, content.MediaUpload) (domain.Media, error) {
	return f.stored, f.storeErr
}
func (f *fakeMediaAdmin) Get(context.Context, int64) (domain.Media, error) { return f.get, f.getErr }
func (f *fakeMediaAdmin) Delete(context.Context, int64) error              { return f.deleteErr }

type fakeMenusAdmin struct {
	menus []domain.NavMenu
	menu  domain.NavMenu
}

func (f *fakeMenusAdmin) List(context.Context) ([]domain.NavMenu, error) { return f.menus, nil }
func (f *fakeMenusAdmin) ByLocation(context.Context, string) (domain.NavMenu, error) {
	return domain.NavMenu{}, nil
}
func (f *fakeMenusAdmin) BySlug(context.Context, string) (domain.NavMenu, error) { return f.menu, nil }

func TestAdminCommentEndpointsRequireHeaderCSRFAndReturnEnvelope(t *testing.T) {
	srv := NewServer(nil, nil, nil, nil, nil)
	srv.comments = nil
	srv.auth = fakeSessions{p: auth.NewPrincipal(1, "ed", []string{auth.RoleEditor, auth.RoleAdministrator}), s: domain.Session{CSRFToken: "token"}}
	r := srv.SessionMiddleware(srv.adminAPIRouter())
	req := httptest.NewRequest(http.MethodPost, "/comments/1/approve", strings.NewReader(`{"status":"approve"}`))
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "x"})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d", rec.Code)
	}
	var payload map[string]map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("body not JSON envelope: %v (%s)", err, rec.Body.String())
	}
	if payload["error"]["code"] == "" {
		t.Fatalf("missing error.code in envelope: %s", rec.Body.String())
	}
}

func TestAdminMediaUploadOversizeReturns413Envelope(t *testing.T) {
	srv := NewServer(nil, nil, nil, nil, nil)
	srv.auth = fakeSessions{p: auth.NewPrincipal(1, "author", []string{auth.RoleAuthor, auth.RoleAdministrator}), s: domain.Session{CSRFToken: "token"}}
	srv.media = content.NewMediaService(stubMediaRepo{}, stubMediaWriter{}, content.MediaConfig{UploadsDir: t.TempDir(), BaseURL: "/wp-content/uploads", MaxUploadSize: 512})

	r := srv.SessionMiddleware(srv.adminAPIRouter())
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "big.txt")
	fw.Write(bytes.Repeat([]byte("a"), 1024))
	mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/media", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-CSRF-Token", "token")
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "x"})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("body not JSON envelope: %v (%s)", err, rec.Body.String())
	}
	if payload["error"]["code"] == "" {
		t.Fatalf("missing error.code in envelope: %s", rec.Body.String())
	}
}

func TestJSONErrorEnvelopeHelper(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSONError(rec, http.StatusBadRequest, "bad_request", "oops")
	var payload map[string]map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["error"]["code"] != "bad_request" {
		t.Fatalf("payload=%v", payload)
	}
}

var _ = errors.New

type stubMediaRepo struct{}

func (stubMediaRepo) List(context.Context, domain.MediaFilter) ([]domain.Media, error) {
	return nil, nil
}
func (stubMediaRepo) Count(context.Context, domain.MediaFilter) (int, error) { return 0, nil }
func (stubMediaRepo) ByID(context.Context, int64) (domain.Media, error) {
	return domain.Media{}, domain.ErrNotFound
}

type stubMediaWriter struct{}

func (stubMediaWriter) Create(context.Context, domain.Media) (int64, error) { return 0, nil }
func (stubMediaWriter) SetParent(context.Context, int64, int64) error       { return nil }
