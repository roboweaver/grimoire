package web_test

import (
	"bytes"
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

// Task 6.1 — REST media create transport tests (handleRESTMediaCreate),
// end-to-end through the router with a REAL content.MediaWriteService wired
// (Req 1.1, 1.2, 1.5, 1.6, 5.1, 5.6, 6.1, 6.4, 7.1, 7.2). The request goes
// through the live router, the live MediaWriteService (which owns the
// upload_files/MIME/unfiltered_upload decisions), and the live MediaService
// (which streams the body to a temp UploadsDir and inserts the attachment via
// the real SQLite MediaWriter). Each assertion is the behavior documented in
// design.md's "handleRESTMediaCreate" section and its error-code table.
//
// Written RED first (task 6.1): the Server does not yet carry a mediaWrite
// field or a WithMediaWrites builder (task 7.1), and registerRESTMedia still
// registers the restNotImplemented 501 stub for POST /media (task 6.2/6.5).
// The package therefore does not compile until WithMediaWrites exists, and
// once it does the POST tests fail against the 501 stub until 6.2 lands. The
// harness is written to the task-7.1 wiring shape:
//
//	.WithContentFeatures(comments, media, menus).
//	WithMediaWrites(mediaWrite).
//	WithREST(mapper, repos.AdminPosts, repos.PostWriter, repos.Posts, repos.Media, repos.Users, 0)
//
// so that WithMediaWrites (+ the s.mediaWrite field it sets) and
// handleRESTMediaCreate are the ONLY missing symbols — every other symbol the
// test uses already resolves. WithMediaWrites mirrors WithAdminWrites: a
// non-breaking builder method, NOT a new positional parameter on
// WithContentFeatures (which would break ~8 existing call sites).

// pngAllowedMIME is the single MIME this harness allows, so the "disallowed
// MIME" case can send bytes that sniff to text/plain while claiming
// Content-Type: image/png (Req 6.4) and still be rejected.
const pngAllowedMIME = "image/png"

// smallMaxUpload is a deliberately tiny MaxUploadSize so the 413 case can post
// a body comfortably over the cap without allocating anything large. It must
// stay above the ~69-byte text payload the MIME cases send (so those reach the
// server-sniff/allow-list gate rather than tripping the size cap first) while
// remaining far below the 413 case's body (pngBytes + smallMaxUpload*4).
const smallMaxUpload = 128

// pngBytes is a minimal real PNG: the 8-byte signature followed by an IHDR
// chunk. http.DetectContentType recognizes the signature and returns
// "image/png", so this is the body for the success cases (sniffs to the one
// allowed type). Kept under smallMaxUpload for the 413 cap to be about size,
// not type.
var pngBytes = []byte{
	0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, // PNG signature
	0x00, 0x00, 0x00, 0x0D, 'I', 'H', 'D', 'R',
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1F, 0x15, 0xC4,
	0x89,
}

// newRESTMediaWriteRouter builds the wp-json router with the REAL
// MediaWriteService wired as s.mediaWrite (via the assumed WithMediaWrites
// builder) and the REST media surface wired via WithREST, returning the router
// and the seeded Repositories so a test can read a persisted attachment back
// through repos.Media. It mirrors newRESTPostWriteRouter but exercises the
// media-write path: the MediaService is built over a temp UploadsDir with
// AllowedMIMEs = [image/png] and a tiny MaxUploadSize, and the MediaWriteService
// is the real content.NewMediaWriteService over that MediaService and the live
// repos.Media/repos.MediaWriter ports, handed the shared sanitize policy.
func newRESTMediaWriteRouter(t *testing.T, fake *fakeSessions) (http.Handler, *storage.Repositories, string) {
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

	uploadsDir := t.TempDir()
	media := content.NewMediaService(repos.Media, repos.MediaWriter, content.MediaConfig{
		UploadsDir:    uploadsDir,
		AllowedMIMEs:  []string{pngAllowedMIME},
		MaxUploadSize: smallMaxUpload,
	})
	// The REAL MediaWriteService, over the live SQLite media ports. Its policy
	// is the shared sanitize.New() (fail closed), matching production wiring.
	mediaWrite := content.NewMediaWriteService(media, repos.Media, repos.MediaWriter, content.WithMediaContentPolicy(sanitize.New()))

	comments := content.NewCommentService(repos.Comments, repos.CommentWriter, repos.CommentMeta, repos.PostWriter, content.NewBasicCommentSpamFilter(content.BasicCommentSpamFilterConfig{}), sanitize.New())

	srv := web.NewServer(
		content.NewPostService(repos.Posts).WithAuthors(repos.Users),
		content.NewTermService(repos.Terms, repos.Posts).WithHierarchy(repos.TermReader),
		content.NewOptionService(repos.Options),
		eng,
		nil,
	).WithAuth(fake, web.AuthConfig{}).
		WithAdmin(admin.Handler("/admin"), adminSvc).
		WithContentFeatures(comments, media, nil).
		WithMediaWrites(mediaWrite).
		WithREST(mapper, repos.AdminPosts, repos.PostWriter, repos.Posts, repos.Media, repos.Users, 0)
	return srv.Routes(), repos, uploadsDir
}

// uploaderFake builds a session fake authenticating as an author (holds
// upload_files but NOT unfiltered_upload). UserID 1 matches the seeded
// attachments' author. The REST write path enforces the M4 CSRF synchronizer
// token for session auth, so a session request must carry both the session
// cookie and the matching X-CSRF-Token "uploader-token".
func uploaderFake() *fakeSessions {
	return &fakeSessions{
		authPrincipal: auth.NewPrincipal(1, "author", []string{auth.RoleAuthor}),
		authSession:   domain.Session{ID: "s1", UserID: 1, CSRFToken: "uploader-token"},
	}
}

// adminFake authenticates as an administrator (holds upload_files AND
// unfiltered_upload), used for the MIME-bypass case.
func adminFake() *fakeSessions {
	return &fakeSessions{
		authPrincipal: auth.NewPrincipal(1, "admin", []string{auth.RoleAdministrator}),
		authSession:   domain.Session{ID: "s1", UserID: 1, CSRFToken: "uploader-token"},
	}
}

// subscriberFake authenticates as a subscriber (lacks upload_files), used for
// the 403 rest_cannot_create case.
func subscriberFake() *fakeSessions {
	return &fakeSessions{
		authPrincipal: auth.NewPrincipal(2, "sub", []string{auth.RoleSubscriber}),
		authSession:   domain.Session{ID: "s2", UserID: 2, CSRFToken: "uploader-token"},
	}
}

// mediaUploadReq builds a POST /media raw-binary upload request with the given
// Content-Type and Content-Disposition, authenticated as a session with the
// matching CSRF token.
func mediaUploadReq(contentType, contentDisposition string, body []byte) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/wp-json/wp/v2/media", bytes.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if contentDisposition != "" {
		req.Header.Set("Content-Disposition", contentDisposition)
	}
	req.AddCookie(&http.Cookie{Name: "grimoire_session", Value: "anything"})
	req.Header.Set("X-CSRF-Token", "uploader-token")
	return req
}

// TestRESTMediaCreateSuccessReturns201EnvelopeAndStores is the Req 1.1/1.2 proof:
// an upload_files holder POSTs a raw PNG body with a Content-Disposition
// filename, the endpoint responds 201 with the GET /media/{id} envelope shape
// (id, source_url, _links.self), and the attachment reached storage (read back
// through repos.Media).
func TestRESTMediaCreateSuccessReturns201EnvelopeAndStores(t *testing.T) {
	h, repos, _ := newRESTMediaWriteRouter(t, uploaderFake())

	req := mediaUploadReq(pngAllowedMIME, `attachment; filename="hero.png"`, pngBytes)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body=%s)", rec.Code, rec.Body.String())
	}

	var env map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode 201 envelope %q: %v", rec.Body.String(), err)
	}
	// The envelope must carry the same media fields GET /media/{id} returns.
	idF, ok := env["id"].(float64)
	if !ok || idF <= 0 {
		t.Fatalf("envelope id = %v, want a positive media ID (envelope=%v)", env["id"], env)
	}
	if _, ok := env["source_url"].(string); !ok {
		t.Errorf("envelope missing source_url; got %v", env)
	}
	links, ok := env["_links"].(map[string]any)
	if !ok {
		t.Fatalf("envelope missing _links; got %v", env)
	}
	if _, ok := links["self"]; !ok {
		t.Errorf("envelope _links missing self; got %v", links)
	}

	// The attachment must actually be in storage, readable through the GET
	// read port — a transport that bypassed MediaWriteService/MediaService
	// could not satisfy this.
	id := int64(idF)
	stored, err := repos.Media.ByID(context.Background(), id)
	if err != nil {
		t.Fatalf("repos.Media.ByID(%d): %v (the create never reached storage)", id, err)
	}
	if stored.MimeType != pngAllowedMIME {
		t.Errorf("stored MimeType = %q, want %q", stored.MimeType, pngAllowedMIME)
	}
	// The uploading principal (user 1) must be the attachment's author (D7).
	if stored.AuthorID != 1 {
		t.Errorf("stored AuthorID = %d, want 1 (the uploading principal)", stored.AuthorID)
	}
}

// TestRESTMediaCreateSanitizesHostileFilename proves a hostile filename cannot
// escape MediaService.Store's sanitization: the stored Filename must not
// contain the traversal sequence, so the raw body cannot be written outside
// the uploads tree.
func TestRESTMediaCreateSanitizesHostileFilename(t *testing.T) {
	h, repos, _ := newRESTMediaWriteRouter(t, uploaderFake())

	req := mediaUploadReq(pngAllowedMIME, `attachment; filename="../../etc/passwd.png"`, pngBytes)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body=%s)", rec.Code, rec.Body.String())
	}
	var env map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	id := int64(env["id"].(float64))
	stored, err := repos.Media.ByID(context.Background(), id)
	if err != nil {
		t.Fatalf("repos.Media.ByID(%d): %v", id, err)
	}
	if strings.Contains(stored.Filename, "..") || strings.Contains(stored.Filename, "etc/passwd") {
		t.Errorf("hostile filename escaped Store sanitization; stored Filename = %q", stored.Filename)
	}
}

// TestRESTMediaCreateMissingContentDisposition400 — Req 1.5.
func TestRESTMediaCreateMissingContentDisposition400(t *testing.T) {
	h, _, _ := newRESTMediaWriteRouter(t, uploaderFake())

	// Content-Type present, Content-Disposition entirely absent.
	req := mediaUploadReq(pngAllowedMIME, "", pngBytes)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
	}
	if code := decodeRESTErrCode(t, rec); code != "rest_upload_no_content_disposition" {
		t.Errorf("error code = %q, want rest_upload_no_content_disposition", code)
	}
}

// TestRESTMediaCreateMissingDispositionFilename400 — Req 1.5: a
// Content-Disposition header that carries no filename parameter is also
// rejected with rest_upload_no_content_disposition.
func TestRESTMediaCreateMissingDispositionFilename400(t *testing.T) {
	h, _, _ := newRESTMediaWriteRouter(t, uploaderFake())

	req := mediaUploadReq(pngAllowedMIME, "attachment", pngBytes)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
	}
	if code := decodeRESTErrCode(t, rec); code != "rest_upload_no_content_disposition" {
		t.Errorf("error code = %q, want rest_upload_no_content_disposition", code)
	}
}

// TestRESTMediaCreateEmptyBody400 — Req 1.6.
func TestRESTMediaCreateEmptyBody400(t *testing.T) {
	h, _, _ := newRESTMediaWriteRouter(t, uploaderFake())

	req := mediaUploadReq(pngAllowedMIME, `attachment; filename="x.png"`, []byte{})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
	}
	if code := decodeRESTErrCode(t, rec); code != "rest_upload_no_data" {
		t.Errorf("error code = %q, want rest_upload_no_data", code)
	}
}

// TestRESTMediaCreateBodyTooBig413 — Req 7.1/7.2: a body over the configured
// MaxUploadSize is rejected via http.MaxBytesReader with
// rest_upload_file_too_big.
func TestRESTMediaCreateBodyTooBig413(t *testing.T) {
	h, _, _ := newRESTMediaWriteRouter(t, uploaderFake())

	// A body comfortably over the smallMaxUpload cap. Lead with the PNG
	// signature so, were the cap not enforced, it would otherwise be an
	// allowed type — isolating the failure to the size gate.
	big := append(append([]byte{}, pngBytes...), bytes.Repeat([]byte("A"), smallMaxUpload*4)...)
	req := mediaUploadReq(pngAllowedMIME, `attachment; filename="big.png"`, big)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413 (body=%s)", rec.Code, rec.Body.String())
	}
	if code := decodeRESTErrCode(t, rec); code != "rest_upload_file_too_big" {
		t.Errorf("error code = %q, want rest_upload_file_too_big", code)
	}
}

// TestRESTMediaCreateDisallowedMIME415UsesSniffedType — Req 6.1 + 6.4: the MIME
// decision uses the SERVER-sniffed type (http.DetectContentType), not the
// client Content-Type header. The body is plain text (sniffs to text/plain)
// while the request claims Content-Type: image/png (an allowed type). Because
// text/plain is not in the allow-list and the author lacks unfiltered_upload,
// the upload is still rejected 415.
func TestRESTMediaCreateDisallowedMIME415UsesSniffedType(t *testing.T) {
	h, repos, _ := newRESTMediaWriteRouter(t, uploaderFake())

	textBody := []byte("this is plainly not a png, it is ascii text that sniffs to text/plain")
	req := mediaUploadReq(pngAllowedMIME, `attachment; filename="trojan.png"`, textBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want 415 (sniffed type must beat the client Content-Type); body=%s", rec.Code, rec.Body.String())
	}
	if code := decodeRESTErrCode(t, rec); code != "rest_upload_unsupported_media_type" {
		t.Errorf("error code = %q, want rest_upload_unsupported_media_type", code)
	}
	// Nothing was persisted (fail closed): the seed has attachments 201/202
	// only; a disallowed upload must not add a new one.
	assertNoNewAttachment(t, repos)
}

// TestRESTMediaCreateUnfilteredUploadBypassesMIME — Req 6.2: an administrator
// (holds unfiltered_upload) may upload a body whose sniffed type is outside the
// allow-list. The same text/plain body that 415s for the author is accepted
// 201 for the admin.
func TestRESTMediaCreateUnfilteredUploadBypassesMIME(t *testing.T) {
	h, _, _ := newRESTMediaWriteRouter(t, adminFake())

	textBody := []byte("this is plainly not a png, it is ascii text that sniffs to text/plain")
	req := mediaUploadReq(pngAllowedMIME, `attachment; filename="note.png"`, textBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (unfiltered_upload bypasses the MIME allow-list); body=%s", rec.Code, rec.Body.String())
	}
}

// TestRESTMediaCreateLacksUploadFiles403 — Req 5.1: a caller without
// upload_files (subscriber) is rejected 403 rest_cannot_create.
func TestRESTMediaCreateLacksUploadFiles403(t *testing.T) {
	h, repos, _ := newRESTMediaWriteRouter(t, subscriberFake())

	req := mediaUploadReq(pngAllowedMIME, `attachment; filename="x.png"`, pngBytes)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body=%s)", rec.Code, rec.Body.String())
	}
	if code := decodeRESTErrCode(t, rec); code != "rest_cannot_create" {
		t.Errorf("error code = %q, want rest_cannot_create", code)
	}
	assertNoNewAttachment(t, repos)
}

// TestRESTMediaCreateAnonymous403 — Req 5.6: an unauthenticated caller (no
// session, no application password) holds no capability and is rejected 403
// rest_cannot_create for create.
func TestRESTMediaCreateAnonymous403(t *testing.T) {
	h, repos, _ := newRESTMediaWriteRouter(t, &fakeSessions{})

	// No session cookie, no CSRF token, no Basic auth.
	req := httptest.NewRequest(http.MethodPost, "/wp-json/wp/v2/media", bytes.NewReader(pngBytes))
	req.Header.Set("Content-Type", pngAllowedMIME)
	req.Header.Set("Content-Disposition", `attachment; filename="x.png"`)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body=%s)", rec.Code, rec.Body.String())
	}
	if code := decodeRESTErrCode(t, rec); code != "rest_cannot_create" {
		t.Errorf("error code = %q, want rest_cannot_create", code)
	}
	assertNoNewAttachment(t, repos)
}

// assertNoNewAttachment fails if any attachment beyond the two seeded ones
// (201 photo, 202 asset) exists — proving a rejected create persisted nothing.
func assertNoNewAttachment(t *testing.T, repos *storage.Repositories) {
	t.Helper()
	items, err := repos.Media.List(context.Background(), domain.MediaFilter{Limit: 100})
	if err != nil {
		t.Fatalf("repos.Media.List: %v", err)
	}
	for _, m := range items {
		if m.ID != 201 && m.ID != 202 {
			t.Errorf("a rejected create persisted an attachment: id=%d title=%q", m.ID, m.Title)
		}
	}
}

// Task 6.3 — REST media UPDATE transport tests (handleRESTMediaUpdate),
// end-to-end through the router with the REAL content.MediaWriteService wired
// (Req 2.1-2.6, 4.7, 5.3-5.6). The request goes through the live router, the
// live MediaWriteService (which loads the stored row, authorizes against its
// post_author, sanitizes each supplied field, and persists through the writer
// port) and the live SQLite media ports. Each assertion is the behavior
// documented in design.md's "handleRESTMediaUpdate" section and its error-code
// table (design.md lines 1216-1221).
//
// Written RED first (task 6.3): handleRESTMediaUpdate does not exist yet (task
// 6.4), and registerRESTMedia still registers the restNotImplemented 501 stub
// for PUT/PATCH/POST /media/{id} (task 6.5). So every assertion below fails
// against that 501 stub (or a wrong code) until 6.4/6.5 land. These tests do
// NOT implement the handler.
//
// Ownership staging: the seed fixtures (storagetest.SeedFixtures) create
// attachments 201 ("photo", image/jpeg) and 202 ("asset", image/png), BOTH
// authored by user 1 (postArgs always sets post_author=1). uploaderFake is user
// 1 (RoleAuthor), so uploaderFake editing 201/202 is an OWN edit — RoleAuthor
// holds edit_posts, and the stored status "inherit" keeps the published/private
// branches of auth.CanEditPost from firing, so the own edit succeeds. For the
// "edited by someone else" 403 case, otherAuthorFake authenticates as a
// DIFFERENT user id (7) with RoleAuthor: editing 201 (stored author 1) needs
// edit_others_posts, which RoleAuthor LACKS, so the service returns
// content.ErrForbidden → 403 rest_cannot_edit. The anonymous case uses a bare
// fakeSessions (no principal, no caps) → also 403.

// otherAuthorFake authenticates as an author (RoleAuthor, holds edit_posts but
// NOT edit_others_posts) whose UserID (7) differs from the seeded attachments'
// author (1). It therefore fails the stored-author ownership check in
// auth.CanEditPost when editing attachment 201/202 (Req 5.3-5.5). It carries
// the matching CSRF token so the request clears the M4 synchronizer check and
// reaches the capability decision rather than being rejected for CSRF.
func otherAuthorFake() *fakeSessions {
	return &fakeSessions{
		authPrincipal: auth.NewPrincipal(7, "other", []string{auth.RoleAuthor}),
		authSession:   domain.Session{ID: "s7", UserID: 7, CSRFToken: "uploader-token"},
	}
}

// mediaUpdateReq builds a method /media/{id} request with a JSON metadata body,
// authenticated as a session with the matching CSRF token "uploader-token" (the
// token every media fake in this file uses). method is PUT/PATCH/POST per the
// WordPress update verbs.
func mediaUpdateReq(method string, id int64, jsonBody string) *http.Request {
	target := "/wp-json/wp/v2/media/" + strconv.FormatInt(id, 10)
	req := httptest.NewRequest(method, target, strings.NewReader(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "grimoire_session", Value: "anything"})
	req.Header.Set("X-CSRF-Token", "uploader-token")
	return req
}

// mediaUpdateReqRaw is mediaUpdateReq with an arbitrary path segment for {id}
// (e.g. "0" or "abc"), so the invalid-id cases can exercise non-numeric and
// non-positive ids that would not fit an int64 parameter.
func mediaUpdateReqRaw(method, idSeg, jsonBody string) *http.Request {
	target := "/wp-json/wp/v2/media/" + idSeg
	req := httptest.NewRequest(method, target, strings.NewReader(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "grimoire_session", Value: "anything"})
	req.Header.Set("X-CSRF-Token", "uploader-token")
	return req
}

// TestRESTMediaUpdateSuccessReturns200EnvelopeAndStores is the Req 2.1/10.3
// proof: user 1 (the stored author of attachment 201) PUTs a JSON metadata body
// and the endpoint responds 200 with the GET /media/{id} envelope shape, whose
// title/caption/alt_text/description reflect the sanitized stored values. The
// values are read back through repos.Media to confirm persistence (a transport
// that bypassed MediaWriteService/the writer could not satisfy this).
func TestRESTMediaUpdateSuccessReturns200EnvelopeAndStores(t *testing.T) {
	h, repos, _ := newRESTMediaWriteRouter(t, uploaderFake())

	body := `{"title":"New Title","caption":"New caption","alt_text":"New alt","description":"New description"}`
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, mediaUpdateReq(http.MethodPut, 201, body))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}

	var env map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode 200 envelope %q: %v", rec.Body.String(), err)
	}
	// The envelope must carry the same media fields GET /media/{id} returns:
	// id, the rendered title/caption/description sub-objects, alt_text, and
	// _links.self.
	if idF, ok := env["id"].(float64); !ok || int64(idF) != 201 {
		t.Fatalf("envelope id = %v, want 201 (envelope=%v)", env["id"], env)
	}
	if title, ok := env["title"].(map[string]any); !ok || title["rendered"] != "New Title" {
		t.Errorf("envelope title = %v, want rendered=New Title", env["title"])
	}
	if caption, ok := env["caption"].(map[string]any); !ok || caption["rendered"] != "New caption" {
		t.Errorf("envelope caption = %v, want rendered=New caption", env["caption"])
	}
	if env["alt_text"] != "New alt" {
		t.Errorf("envelope alt_text = %v, want New alt", env["alt_text"])
	}
	if desc, ok := env["description"].(map[string]any); !ok || desc["rendered"] != "New description" {
		t.Errorf("envelope description = %v, want rendered=New description", env["description"])
	}
	links, ok := env["_links"].(map[string]any)
	if !ok {
		t.Fatalf("envelope missing _links; got %v", env)
	}
	if _, ok := links["self"]; !ok {
		t.Errorf("envelope _links missing self; got %v", links)
	}

	// The update must actually be in storage, readable through the GET port.
	stored, err := repos.Media.ByID(context.Background(), 201)
	if err != nil {
		t.Fatalf("repos.Media.ByID(201): %v (the update never reached storage)", err)
	}
	if stored.Title != "New Title" {
		t.Errorf("stored Title = %q, want %q", stored.Title, "New Title")
	}
	if stored.Caption != "New caption" {
		t.Errorf("stored Caption = %q, want %q", stored.Caption, "New caption")
	}
	if stored.AltText != "New alt" {
		t.Errorf("stored AltText = %q, want %q", stored.AltText, "New alt")
	}
	if stored.Description != "New description" {
		t.Errorf("stored Description = %q, want %q", stored.Description, "New description")
	}
}

// TestRESTMediaUpdateInvalidIDReturns404 — Req 2.5: a non-positive or
// non-numeric {id} is rejected 404 rest_post_invalid_id before any storage
// lookup.
func TestRESTMediaUpdateInvalidIDReturns404(t *testing.T) {
	for _, idSeg := range []string{"0", "abc"} {
		t.Run(idSeg, func(t *testing.T) {
			h, _, _ := newRESTMediaWriteRouter(t, uploaderFake())
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, mediaUpdateReqRaw(http.MethodPut, idSeg, `{"title":"x"}`))
			if rec.Code != http.StatusNotFound {
				t.Fatalf("id=%q status = %d, want 404 (body=%s)", idSeg, rec.Code, rec.Body.String())
			}
			if code := decodeRESTErrCode(t, rec); code != "rest_post_invalid_id" {
				t.Errorf("id=%q error code = %q, want rest_post_invalid_id", idSeg, code)
			}
		})
	}
}

// TestRESTMediaUpdateUnknownAttachmentReturns404 — Req 2.5: a well-formed but
// unknown attachment id (not in storage) surfaces domain.ErrNotFound from the
// service, mapped to 404 rest_post_invalid_id.
func TestRESTMediaUpdateUnknownAttachmentReturns404(t *testing.T) {
	h, _, _ := newRESTMediaWriteRouter(t, uploaderFake())

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, mediaUpdateReq(http.MethodPut, 999999, `{"title":"x"}`))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body=%s)", rec.Code, rec.Body.String())
	}
	if code := decodeRESTErrCode(t, rec); code != "rest_post_invalid_id" {
		t.Errorf("error code = %q, want rest_post_invalid_id", code)
	}
}

// TestRESTMediaUpdateMalformedJSON400 — a body that is not valid JSON is
// rejected 400 rest_invalid_param by the handler's json.Decode step.
func TestRESTMediaUpdateMalformedJSON400(t *testing.T) {
	h, _, _ := newRESTMediaWriteRouter(t, uploaderFake())

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, mediaUpdateReq(http.MethodPut, 201, `{"title":`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
	}
	if code := decodeRESTErrCode(t, rec); code != "rest_invalid_param" {
		t.Errorf("error code = %q, want rest_invalid_param", code)
	}
}

// TestRESTMediaUpdateNegativeParent400 — Req 2.6: a negative "post" parent is
// rejected 400 rest_invalid_param by parseRESTMediaUpdate. (A non-numeric
// "post" is a JSON type error decoding into *int64 and also yields 400
// rest_invalid_param, exercised as the second subcase.)
func TestRESTMediaUpdateNegativeParent400(t *testing.T) {
	t.Run("negative", func(t *testing.T) {
		h, _, _ := newRESTMediaWriteRouter(t, uploaderFake())
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, mediaUpdateReq(http.MethodPut, 201, `{"post":-1}`))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
		}
		if code := decodeRESTErrCode(t, rec); code != "rest_invalid_param" {
			t.Errorf("error code = %q, want rest_invalid_param", code)
		}
	})
	t.Run("non-numeric", func(t *testing.T) {
		h, _, _ := newRESTMediaWriteRouter(t, uploaderFake())
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, mediaUpdateReq(http.MethodPut, 201, `{"post":"notanumber"}`))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
		}
		if code := decodeRESTErrCode(t, rec); code != "rest_invalid_param" {
			t.Errorf("error code = %q, want rest_invalid_param", code)
		}
	})
}

// TestRESTMediaUpdatePartialLeavesOmittedFieldsUnchanged — Req 2.3: a PUT that
// supplies ONLY "title" must leave caption/alt_text/description at their stored
// values. The stored values are snapshotted before the update (via repos.Media)
// and compared after, proving the pointer-field partial-update semantics.
func TestRESTMediaUpdatePartialLeavesOmittedFieldsUnchanged(t *testing.T) {
	h, repos, _ := newRESTMediaWriteRouter(t, uploaderFake())

	before, err := repos.Media.ByID(context.Background(), 201)
	if err != nil {
		t.Fatalf("repos.Media.ByID(201) before: %v", err)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, mediaUpdateReq(http.MethodPut, 201, `{"title":"Only Title Changed"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}

	after, err := repos.Media.ByID(context.Background(), 201)
	if err != nil {
		t.Fatalf("repos.Media.ByID(201) after: %v", err)
	}
	if after.Title != "Only Title Changed" {
		t.Errorf("stored Title = %q, want %q (the supplied field)", after.Title, "Only Title Changed")
	}
	if after.Caption != before.Caption {
		t.Errorf("omitted caption changed: before=%q after=%q", before.Caption, after.Caption)
	}
	if after.AltText != before.AltText {
		t.Errorf("omitted alt_text changed: before=%q after=%q", before.AltText, after.AltText)
	}
	if after.Description != before.Description {
		t.Errorf("omitted description changed: before=%q after=%q", before.Description, after.Description)
	}
}

// TestRESTMediaUpdateOtherAuthor403 — Req 5.3-5.5: an author (edit_posts but
// NOT edit_others_posts) editing an attachment authored by someone else is
// rejected 403 rest_cannot_edit. Attachment 201 is authored by user 1;
// otherAuthorFake is user 7, so the stored-author ownership check fails.
func TestRESTMediaUpdateOtherAuthor403(t *testing.T) {
	h, repos, _ := newRESTMediaWriteRouter(t, otherAuthorFake())

	before, err := repos.Media.ByID(context.Background(), 201)
	if err != nil {
		t.Fatalf("repos.Media.ByID(201) before: %v", err)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, mediaUpdateReq(http.MethodPut, 201, `{"title":"Hijacked"}`))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body=%s)", rec.Code, rec.Body.String())
	}
	if code := decodeRESTErrCode(t, rec); code != "rest_cannot_edit" {
		t.Errorf("error code = %q, want rest_cannot_edit", code)
	}
	// Fail closed: a rejected edit must not have persisted anything.
	after, err := repos.Media.ByID(context.Background(), 201)
	if err != nil {
		t.Fatalf("repos.Media.ByID(201) after: %v", err)
	}
	if after.Title != before.Title {
		t.Errorf("a 403 edit still persisted a title change: before=%q after=%q", before.Title, after.Title)
	}
}

// TestRESTMediaUpdateAnonymous403 — Req 5.6: an unauthenticated caller (no
// session, no application password) holds no capability and is rejected 403
// rest_cannot_edit for an update.
func TestRESTMediaUpdateAnonymous403(t *testing.T) {
	h, repos, _ := newRESTMediaWriteRouter(t, &fakeSessions{})

	// No session cookie, no CSRF token, no Basic auth.
	req := httptest.NewRequest(http.MethodPut, "/wp-json/wp/v2/media/201", strings.NewReader(`{"title":"Anon"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body=%s)", rec.Code, rec.Body.String())
	}
	if code := decodeRESTErrCode(t, rec); code != "rest_cannot_edit" {
		t.Errorf("error code = %q, want rest_cannot_edit", code)
	}
	stored, err := repos.Media.ByID(context.Background(), 201)
	if err != nil {
		t.Fatalf("repos.Media.ByID(201): %v", err)
	}
	if stored.Title == "Anon" {
		t.Errorf("an anonymous edit persisted a title change; stored Title = %q", stored.Title)
	}
}

// TestRESTMediaUpdatePolicyErrorMapsTo400 — Req 4.7: a title whose nested
// entity encoding never converges surfaces sanitize.ErrTitleNotConverging from
// the policy inside MediaWriteService.Update; the transport must map it to 400
// rest_invalid_param, NEVER 500. The input "&" + "amp;"*20 + "lt;" is a
// pathologically nested encoding that exceeds titleMaxPasses and so fails
// closed.
func TestRESTMediaUpdatePolicyErrorMapsTo400(t *testing.T) {
	h, _, _ := newRESTMediaWriteRouter(t, uploaderFake())

	nonConverging := "&" + strings.Repeat("amp;", 20) + "lt;"
	body, err := json.Marshal(map[string]any{"title": nonConverging})
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, mediaUpdateReq(http.MethodPut, 201, string(body)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (policy error must never be 500); body=%s", rec.Code, rec.Body.String())
	}
	if code := decodeRESTErrCode(t, rec); code != "rest_invalid_param" {
		t.Errorf("error code = %q, want rest_invalid_param", code)
	}
}

// TestRESTMediaUpdatePOSTToItemRoutesToUpdate proves the WordPress
// POST-as-update convention: a POST to /media/{id} (an item route, not the
// collection) updates the attachment rather than creating one, returning 200
// with the updated metadata. POST /media creates; POST /media/{id} updates.
func TestRESTMediaUpdatePOSTToItemRoutesToUpdate(t *testing.T) {
	h, repos, _ := newRESTMediaWriteRouter(t, uploaderFake())

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, mediaUpdateReq(http.MethodPost, 201, `{"title":"Via POST"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /media/201 status = %d, want 200 (POST-as-update); body=%s", rec.Code, rec.Body.String())
	}
	var env map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope %q: %v", rec.Body.String(), err)
	}
	if idF, ok := env["id"].(float64); !ok || int64(idF) != 201 {
		t.Fatalf("POST-as-update envelope id = %v, want 201 (not a newly created id)", env["id"])
	}
	stored, err := repos.Media.ByID(context.Background(), 201)
	if err != nil {
		t.Fatalf("repos.Media.ByID(201): %v", err)
	}
	if stored.Title != "Via POST" {
		t.Errorf("POST to /media/201 did not update the title; stored Title = %q", stored.Title)
	}
}

// Task 6.5 — stub-layout + nil-fallback tests (Req 9.1, 9.2, 9.3).
//
// These pin the registerRESTMedia route layout that tasks 6.2/6.4 produced:
//   - DELETE /media and DELETE /media/{id} ALWAYS answer 501, even with a
//     fully-wired MediaWriteService — DELETE is never reinterpreted as a write
//     and never invokes the service (Req 9.2, 9.3).
//   - The non-create write verbs on the /media COLLECTION (PUT/PATCH) always
//     answer 501 (Req 9.1).
//   - When s.mediaWrite is nil (a deployment/harness that omits
//     WithMediaWrites), every write verb falls back to the restNotImplemented
//     501 stub rather than nil-panicking (Req 9.3).
//
// Since the layout is already correct, these are green-on-write (they pin
// existing behavior). A failure here reveals a layout regression in
// registerRESTMedia.

// newRESTMediaNilWriteRouter builds the wp-json router with the REST media
// surface wired (WithREST) but WITHOUT WithMediaWrites, so s.mediaWrite stays
// nil and the write verbs must fall back to the restNotImplemented 501 stub.
// It mirrors newRESTMediaWriteRouter exactly except for the omitted
// .WithMediaWrites(...) step.
func newRESTMediaNilWriteRouter(t *testing.T, fake *fakeSessions) http.Handler {
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

	uploadsDir := t.TempDir()
	media := content.NewMediaService(repos.Media, repos.MediaWriter, content.MediaConfig{
		UploadsDir:    uploadsDir,
		AllowedMIMEs:  []string{pngAllowedMIME},
		MaxUploadSize: smallMaxUpload,
	})
	comments := content.NewCommentService(repos.Comments, repos.CommentWriter, repos.CommentMeta, repos.PostWriter, content.NewBasicCommentSpamFilter(content.BasicCommentSpamFilterConfig{}), sanitize.New())

	srv := web.NewServer(
		content.NewPostService(repos.Posts).WithAuthors(repos.Users),
		content.NewTermService(repos.Terms, repos.Posts).WithHierarchy(repos.TermReader),
		content.NewOptionService(repos.Options),
		eng,
		nil,
	).WithAuth(fake, web.AuthConfig{}).
		WithAdmin(admin.Handler("/admin"), adminSvc).
		WithContentFeatures(comments, media, nil).
		// NB: no WithMediaWrites — s.mediaWrite stays nil (Req 9.3).
		WithREST(mapper, repos.AdminPosts, repos.PostWriter, repos.Posts, repos.Media, repos.Users, 0)
	return srv.Routes()
}

// TestRESTMediaDeleteCollectionStays501 — Req 9.2/9.3: DELETE on the /media
// COLLECTION is always a 501 stub, even with a fully-wired MediaWriteService.
// DELETE must not be reinterpreted as a write.
func TestRESTMediaDeleteCollectionStays501(t *testing.T) {
	h, _, _ := newRESTMediaWriteRouter(t, uploaderFake())

	req := httptest.NewRequest(http.MethodDelete, "/wp-json/wp/v2/media", nil)
	req.AddCookie(&http.Cookie{Name: "grimoire_session", Value: "anything"})
	req.Header.Set("X-CSRF-Token", "uploader-token")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("DELETE /media status = %d, want 501 (body=%s)", rec.Code, rec.Body.String())
	}
	if code := decodeRESTErrCode(t, rec); code != "rest_cannot_edit" {
		t.Errorf("error code = %q, want rest_cannot_edit", code)
	}
}

// TestRESTMediaDeleteItemStays501WithWiredService — Req 9.2/9.3: DELETE on the
// single-item route /media/{id} is ALWAYS a 501 stub, even with a fully-wired
// MediaWriteService authenticated as an upload_files holder. The attachment
// must be unchanged (still present) afterward — the DELETE must not invoke the
// service nor remove the row.
func TestRESTMediaDeleteItemStays501WithWiredService(t *testing.T) {
	h, repos, _ := newRESTMediaWriteRouter(t, uploaderFake())

	// Seeded attachment 201 exists before the DELETE attempt.
	before, err := repos.Media.ByID(context.Background(), 201)
	if err != nil {
		t.Fatalf("repos.Media.ByID(201) before: %v", err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/wp-json/wp/v2/media/201", nil)
	req.AddCookie(&http.Cookie{Name: "grimoire_session", Value: "anything"})
	req.Header.Set("X-CSRF-Token", "uploader-token")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("DELETE /media/201 status = %d, want 501 (body=%s)", rec.Code, rec.Body.String())
	}
	if code := decodeRESTErrCode(t, rec); code != "rest_cannot_edit" {
		t.Errorf("error code = %q, want rest_cannot_edit", code)
	}

	// No service effect: the attachment still exists, unchanged.
	after, err := repos.Media.ByID(context.Background(), 201)
	if err != nil {
		t.Fatalf("repos.Media.ByID(201) after: %v (a 501 DELETE must not remove the attachment)", err)
	}
	if after.ID != before.ID || after.Title != before.Title {
		t.Errorf("a 501 DELETE mutated the attachment: before=%+v after=%+v", before, after)
	}
}

// TestRESTMediaCollectionWriteStubsStay501 — Req 9.1: PUT and PATCH on the
// /media COLLECTION route stay 501 stubs even with a wired MediaWriteService
// (only POST /media is the live create verb; the collection has no PUT/PATCH
// update semantics).
func TestRESTMediaCollectionWriteStubsStay501(t *testing.T) {
	for _, method := range []string{http.MethodPut, http.MethodPatch} {
		t.Run(method, func(t *testing.T) {
			h, _, _ := newRESTMediaWriteRouter(t, uploaderFake())

			req := httptest.NewRequest(method, "/wp-json/wp/v2/media", strings.NewReader(`{"title":"x"}`))
			req.Header.Set("Content-Type", "application/json")
			req.AddCookie(&http.Cookie{Name: "grimoire_session", Value: "anything"})
			req.Header.Set("X-CSRF-Token", "uploader-token")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusNotImplemented {
				t.Fatalf("%s /media status = %d, want 501 (body=%s)", method, rec.Code, rec.Body.String())
			}
			if code := decodeRESTErrCode(t, rec); code != "rest_cannot_edit" {
				t.Errorf("%s /media error code = %q, want rest_cannot_edit", method, code)
			}
		})
	}
}

// TestRESTMediaNilWriteServiceCreateFallsBackTo501 — Req 9.3: with
// s.mediaWrite == nil (a server built WITHOUT WithMediaWrites), POST /media
// falls back to the restNotImplemented 501 stub rather than nil-panicking.
func TestRESTMediaNilWriteServiceCreateFallsBackTo501(t *testing.T) {
	h := newRESTMediaNilWriteRouter(t, uploaderFake())

	req := mediaUploadReq(pngAllowedMIME, `attachment; filename="hero.png"`, pngBytes)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("POST /media (nil mediaWrite) status = %d, want 501 (body=%s)", rec.Code, rec.Body.String())
	}
	if code := decodeRESTErrCode(t, rec); code != "rest_cannot_create" {
		t.Errorf("error code = %q, want rest_cannot_create", code)
	}
}

// TestRESTMediaNilWriteServiceUpdateFallsBackTo501 — Req 9.3: with
// s.mediaWrite == nil, every item-route write verb (PUT/PATCH/POST
// /media/{id}) falls back to the restNotImplemented 501 stub.
func TestRESTMediaNilWriteServiceUpdateFallsBackTo501(t *testing.T) {
	for _, method := range []string{http.MethodPut, http.MethodPatch, http.MethodPost} {
		t.Run(method, func(t *testing.T) {
			h := newRESTMediaNilWriteRouter(t, uploaderFake())

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, mediaUpdateReq(method, 201, `{"title":"x"}`))
			if rec.Code != http.StatusNotImplemented {
				t.Fatalf("%s /media/201 (nil mediaWrite) status = %d, want 501 (body=%s)", method, rec.Code, rec.Body.String())
			}
			if code := decodeRESTErrCode(t, rec); code != "rest_cannot_edit" {
				t.Errorf("%s /media/201 error code = %q, want rest_cannot_edit", method, code)
			}
		})
	}
}

// Task 8.1 — capability-matrix transport test for the REST media write surface
// (one place, table-driven), end-to-end through the router with the REAL
// content.MediaWriteService wired (Req 5.1–5.6, 6.1, 6.2, 8.1, 8.2). Every
// principal below is built through the real auth capability resolution
// (auth.CapabilitiesForRoles via auth.NewPrincipal), so the matrix exercises
// the production role→capability mapping rather than hand-picked cap sets:
//
//   - CREATE (handleRESTMediaCreate) is permitted exactly for upload_files
//     holders — author, editor, administrator — and 403 rest_cannot_create for
//     everyone else (anonymous, subscriber, contributor), because contributor
//     holds edit_posts but NOT upload_files (Req 5.1, 5.6).
//   - UPDATE (handleRESTMediaUpdate) authorizes against the STORED attachment
//     author (the seed sets post_author=1 on 201/202). Editing an OWN
//     attachment needs edit_posts; editing ANOTHER author's needs
//     edit_others_posts; otherwise 403 rest_cannot_edit (Req 5.3–5.6). The
//     stored status is "inherit", so auth.CanEditPost's published/private
//     branches never fire — ownership + edit_posts/edit_others_posts is the
//     whole decision.
//   - The MIME allow-list is bypassed ONLY for the unfiltered_upload holder
//     (administrator): the same text/plain body that 415s for an author is
//     accepted 201 for the admin (Req 6.1, 6.2).
//   - Auth matrix: an Application-Password-authenticated create skips the M4
//     CSRF synchronizer entirely (succeeds with no X-CSRF-Token), whereas a
//     session-authenticated create WITHOUT a valid CSRF token is rejected
//     403 rest_forbidden (Req 8.1, 8.2).

// capRoles is the capability resolution the matrix leans on, kept here so the
// expectations below read against the SAME source of truth the service uses.
// anonymous (no roles) resolves to the empty capability set.
func newRESTMediaMatrixPrincipal(userID int64, login string, roles ...string) auth.Principal {
	return auth.NewPrincipal(userID, login, roles)
}

// matrixFake wraps a matrix principal in the fakeSessions shape every media
// request helper in this file expects: a session carrying the shared
// "uploader-token" CSRF token, so a request built with mediaUploadReq /
// mediaUpdateReq (which always set X-CSRF-Token: uploader-token) clears the
// M4 synchronizer check and reaches the capability decision. An empty roles
// set with userID 0 is the anonymous case: callers pass a bare &fakeSessions{}
// directly for that instead.
func matrixFake(userID int64, login string, roles ...string) *fakeSessions {
	return &fakeSessions{
		authPrincipal: newRESTMediaMatrixPrincipal(userID, login, roles...),
		authSession:   domain.Session{ID: "sm", UserID: userID, CSRFToken: "uploader-token"},
	}
}

// TestRESTMediaCreateCapabilityMatrix — Req 5.1, 5.6: create is permitted
// exactly for upload_files holders (author/editor/administrator → 201) and
// 403 rest_cannot_create for everyone else (anonymous/subscriber/contributor).
// Each role is built through the real auth.CapabilitiesForRoles/NewPrincipal.
func TestRESTMediaCreateCapabilityMatrix(t *testing.T) {
	cases := []struct {
		name     string
		fake     *fakeSessions // nil → anonymous (bare &fakeSessions{})
		wantCode int
		wantErr  string // "" when wantCode is 201
	}{
		{"anonymous", nil, http.StatusForbidden, "rest_cannot_create"},
		{"subscriber", matrixFake(2, "sub", auth.RoleSubscriber), http.StatusForbidden, "rest_cannot_create"},
		{"contributor", matrixFake(3, "contrib", auth.RoleContributor), http.StatusForbidden, "rest_cannot_create"},
		{"author", matrixFake(1, "author", auth.RoleAuthor), http.StatusCreated, ""},
		{"editor", matrixFake(4, "editor", auth.RoleEditor), http.StatusCreated, ""},
		{"administrator", matrixFake(1, "admin", auth.RoleAdministrator), http.StatusCreated, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := tc.fake
			if fake == nil {
				fake = &fakeSessions{}
			}
			h, repos, _ := newRESTMediaWriteRouter(t, fake)

			var req *http.Request
			if tc.fake == nil {
				// Anonymous: no session cookie, no CSRF token.
				req = httptest.NewRequest(http.MethodPost, "/wp-json/wp/v2/media", bytes.NewReader(pngBytes))
				req.Header.Set("Content-Type", pngAllowedMIME)
				req.Header.Set("Content-Disposition", `attachment; filename="x.png"`)
			} else {
				req = mediaUploadReq(pngAllowedMIME, `attachment; filename="x.png"`, pngBytes)
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
				// Fail closed: a rejected create persisted nothing beyond the
				// two seeded attachments (201/202).
				assertNoNewAttachment(t, repos)
			}
		})
	}
}

// TestRESTMediaUpdateCapabilityMatrix — Req 5.3–5.6: update is authorized
// against the STORED attachment author (user 1 on attachment 201). Editing an
// OWN attachment is permitted for edit_posts holders (contributor/author/
// editor/administrator when the caller is user 1); editing ANOTHER author's
// attachment is permitted only for edit_others_posts holders (editor/
// administrator as user 7); otherwise 403 rest_cannot_edit. Each role is built
// through the real auth capability resolution.
func TestRESTMediaUpdateCapabilityMatrix(t *testing.T) {
	cases := []struct {
		name     string
		fake     *fakeSessions // nil → anonymous
		wantCode int
		wantErr  string // "" when wantCode is 200
	}{
		// Anonymous holds no capability → 403.
		{"anonymous", nil, http.StatusForbidden, "rest_cannot_edit"},
		// Subscriber lacks edit_posts entirely → 403 even as the stored author.
		{"subscriber-owner", matrixFake(1, "sub", auth.RoleSubscriber), http.StatusForbidden, "rest_cannot_edit"},
		// OWN edit (caller is user 1, the stored author): edit_posts suffices.
		{"contributor-owner", matrixFake(1, "contrib", auth.RoleContributor), http.StatusOK, ""},
		{"author-owner", matrixFake(1, "author", auth.RoleAuthor), http.StatusOK, ""},
		{"editor-owner", matrixFake(1, "editor", auth.RoleEditor), http.StatusOK, ""},
		{"administrator-owner", matrixFake(1, "admin", auth.RoleAdministrator), http.StatusOK, ""},
		// OTHER-author edit (caller is user 7, stored author is 1):
		// edit_others_posts required. author/contributor lack it → 403;
		// editor/administrator hold it → 200.
		{"contributor-other", matrixFake(7, "contrib7", auth.RoleContributor), http.StatusForbidden, "rest_cannot_edit"},
		{"author-other", matrixFake(7, "author7", auth.RoleAuthor), http.StatusForbidden, "rest_cannot_edit"},
		{"editor-other", matrixFake(7, "editor7", auth.RoleEditor), http.StatusOK, ""},
		{"administrator-other", matrixFake(7, "admin7", auth.RoleAdministrator), http.StatusOK, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := tc.fake
			if fake == nil {
				fake = &fakeSessions{}
			}
			h, repos, _ := newRESTMediaWriteRouter(t, fake)

			before, err := repos.Media.ByID(context.Background(), 201)
			if err != nil {
				t.Fatalf("repos.Media.ByID(201) before: %v", err)
			}

			var req *http.Request
			if tc.fake == nil {
				req = httptest.NewRequest(http.MethodPut, "/wp-json/wp/v2/media/201", strings.NewReader(`{"title":"Matrix"}`))
				req.Header.Set("Content-Type", "application/json")
			} else {
				req = mediaUpdateReq(http.MethodPut, 201, `{"title":"Matrix"}`)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d (body=%s)", rec.Code, tc.wantCode, rec.Body.String())
			}
			after, err := repos.Media.ByID(context.Background(), 201)
			if err != nil {
				t.Fatalf("repos.Media.ByID(201) after: %v", err)
			}
			if tc.wantErr != "" {
				if code := decodeRESTErrCode(t, rec); code != tc.wantErr {
					t.Errorf("error code = %q, want %q", code, tc.wantErr)
				}
				// Fail closed: a rejected edit must not have persisted a change.
				if after.Title != before.Title {
					t.Errorf("a 403 edit persisted a title change: before=%q after=%q", before.Title, after.Title)
				}
			} else if after.Title != "Matrix" {
				t.Errorf("a permitted edit did not persist; stored Title = %q, want %q", after.Title, "Matrix")
			}
		})
	}
}

// TestRESTMediaCreateMIMEBypassMatrix — Req 6.1, 6.2: the MIME allow-list is
// bypassed ONLY for the unfiltered_upload holder. The same text/plain body
// (sniffs outside the single-entry [image/png] allow-list) is rejected 415
// rest_upload_unsupported_media_type for an author (upload_files but NOT
// unfiltered_upload) and accepted 201 for an administrator (unfiltered_upload).
func TestRESTMediaCreateMIMEBypassMatrix(t *testing.T) {
	textBody := []byte("this is plainly not a png, it is ascii text that sniffs to text/plain")
	cases := []struct {
		name     string
		fake     *fakeSessions
		wantCode int
		wantErr  string
	}{
		{"author-no-bypass", matrixFake(1, "author", auth.RoleAuthor), http.StatusUnsupportedMediaType, "rest_upload_unsupported_media_type"},
		{"editor-no-bypass", matrixFake(4, "editor", auth.RoleEditor), http.StatusUnsupportedMediaType, "rest_upload_unsupported_media_type"},
		{"administrator-bypass", matrixFake(1, "admin", auth.RoleAdministrator), http.StatusCreated, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, _, _ := newRESTMediaWriteRouter(t, tc.fake)

			req := mediaUploadReq(pngAllowedMIME, `attachment; filename="note.png"`, textBody)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

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

// newRESTMediaWriteAppPasswordRouter builds the wp-json router with the REAL
// MediaWriteService wired AND Application Password auth wired
// (WithApplicationPasswords, requireTLS=false so the transport gate is
// disabled for the test), seeding user 1's {prefix}capabilities as
// administrator so a Verify of that user's Application Password resolves a
// principal holding upload_files (+ unfiltered_upload). It mirrors
// newRESTMediaWriteRouter and additionally returns the ApplicationPasswords
// manager so a test can mint a real secret. This is the harness the auth-matrix
// cells use: the AppPassword-vs-session CSRF distinction (Req 8.1, 8.2),
// following the pattern of newAppPasswordRESTRouter / mintAppPassword used by
// the comment and post write tests.
func newRESTMediaWriteAppPasswordRouter(t *testing.T, fake *fakeSessions) (http.Handler, *auth.ApplicationPasswords) {
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
	// upload_files (administrator → upload_files + unfiltered_upload). The base
	// fixtures create the "admin" user row but no capabilities meta, so without
	// this an AppPassword upload would correctly 403 for lack of upload_files —
	// which would not exercise the CSRF-skip cell the matrix is here to prove.
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

	uploadsDir := t.TempDir()
	media := content.NewMediaService(repos.Media, repos.MediaWriter, content.MediaConfig{
		UploadsDir:    uploadsDir,
		AllowedMIMEs:  []string{pngAllowedMIME},
		MaxUploadSize: smallMaxUpload,
	})
	mediaWrite := content.NewMediaWriteService(media, repos.Media, repos.MediaWriter, content.WithMediaContentPolicy(sanitize.New()))
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
		WithContentFeatures(comments, media, nil).
		WithMediaWrites(mediaWrite).
		WithREST(mapper, repos.AdminPosts, repos.PostWriter, repos.Posts, repos.Media, repos.Users, 0).
		WithApplicationPasswords(ap, false, "")
	return srv.Routes(), ap
}

// TestRESTMediaWriteAuthMatrix — Req 8.1, 8.2: the AppPassword-vs-session CSRF
// distinction on the media write surface, mirroring
// TestRESTCommentCreateSessionAuthRequiresCSRF /
// TestApplicationPasswordAuthValidBasicSkipsCSRF.
//
//   - Application Password auth skips CSRF: a POST /media authenticated with a
//     valid Application Password (user 1, an administrator) and NO X-CSRF-Token
//     succeeds 201. isAppPasswordAuth short-circuits the handler's CSRF branch.
//   - Session auth without a valid CSRF token → 403 rest_forbidden: the same
//     POST authenticated by a session cookie but with a missing (then
//     mismatched) X-CSRF-Token is rejected by requireSessionCSRFREST before any
//     capability or service work.
func TestRESTMediaWriteAuthMatrix(t *testing.T) {
	t.Run("AppPasswordSkipsCSRF", func(t *testing.T) {
		// No session principal: auth is resolved purely via the Application
		// Password, so the request is CSRF-exempt.
		h, ap := newRESTMediaWriteAppPasswordRouter(t, &fakeSessions{})
		secret := mintAppPassword(t, context.Background(), ap)

		req := httptest.NewRequest(http.MethodPost, "/wp-json/wp/v2/media", bytes.NewReader(pngBytes))
		req.Header.Set("Content-Type", pngAllowedMIME)
		req.Header.Set("Content-Disposition", `attachment; filename="viaapp.png"`)
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
		// A valid session principal (administrator, holds upload_files) but no
		// valid CSRF token: the request must be rejected at the CSRF gate, not
		// reach the service.
		fake := &fakeSessions{
			authPrincipal: auth.NewPrincipal(1, "admin", []string{auth.RoleAdministrator}),
			authSession:   domain.Session{ID: "s1", UserID: 1, CSRFToken: "correct-token"},
		}
		h, _ := newRESTMediaWriteAppPasswordRouter(t, fake)

		// Missing X-CSRF-Token → 403 rest_forbidden.
		missing := httptest.NewRequest(http.MethodPost, "/wp-json/wp/v2/media", bytes.NewReader(pngBytes))
		missing.Header.Set("Content-Type", pngAllowedMIME)
		missing.Header.Set("Content-Disposition", `attachment; filename="x.png"`)
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
		mismatch := httptest.NewRequest(http.MethodPost, "/wp-json/wp/v2/media", bytes.NewReader(pngBytes))
		mismatch.Header.Set("Content-Type", pngAllowedMIME)
		mismatch.Header.Set("Content-Disposition", `attachment; filename="x.png"`)
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

// Task 8.2 — content-safety and observability end-to-end tests for the REST
// media write surface, driven through the live router with the REAL
// MediaWriteService (whose policy is the shared sanitize.New()). These prove
// the four task-8.2 guarantees against the transport, not just the service:
//
//  1. A submitted <script> in a caption/description is sanitized BEFORE it
//     reaches the writer — asserted on the STORED value read back through
//     repos.Media (what the writer/DB saw), not merely on a response escape
//     (Req 11.1, 11.3).
//  2. create → update caption/alt_text/description → GET /media/{id} returns
//     the SANITIZED stored values across the write→read boundary (Req 10.3).
//  3. A GET of an attachment this milestone never writes to is stable
//     (byte-identical across repeated reads) and carries the additive keys in
//     their empty-but-present shape (Req 10.1).
//  4. A PATCH re-sending the stored caption verbatim does not alter it — the
//     byte-identity skip end-to-end through the transport (Req 2.4), mirroring
//     the content-package byte-identity test (task 4.2).

// mediaWriterTierForUploader is the sanitize.Writer the live service builds for
// uploaderFake (user 1, RoleAuthor — holds edit_posts but NOT unfiltered_html),
// so a per-field oracle computed with it matches exactly what the service
// persisted. Author selects tier B for the markup fields (caption/description)
// and the plain-text title path for alt_text (Req 4.1-4.6).
func mediaWriterTierForUploader() sanitize.Writer {
	return sanitize.For(auth.NewPrincipal(1, "author", []string{auth.RoleAuthor}))
}

// TestRESTMediaUpdateSanitizesScriptBeforeWriter — Req 11.1, 11.3: a <script>
// submitted in caption and description is stripped at the write boundary. The
// assertion is on the STORED value (repos.Media.ByID — what reached the
// writer/DB), proving the Policy ran BEFORE persistence rather than the
// response merely HTML-escaping on the way out. The surviving plain text proves
// the field was sanitized (not rejected/emptied), and the oracle pins the exact
// tier-B output.
func TestRESTMediaUpdateSanitizesScriptBeforeWriter(t *testing.T) {
	h, repos, _ := newRESTMediaWriteRouter(t, uploaderFake())

	const (
		capIn  = `hi <script>alert('xss')</script> there`
		descIn = `desc <script>alert(document.cookie)</script> end`
	)
	pol := sanitize.New()
	w := mediaWriterTierForUploader()
	wantCap, err := pol.Sanitize(sanitize.PostExcerpt, w, capIn)
	if err != nil {
		t.Fatalf("oracle caption: %v", err)
	}
	wantDesc, err := pol.Sanitize(sanitize.PostContent, w, descIn)
	if err != nil {
		t.Fatalf("oracle description: %v", err)
	}

	body, err := json.Marshal(map[string]any{"caption": capIn, "description": descIn})
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, mediaUpdateReq(http.MethodPut, 201, string(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}

	// The decisive assertion: read the STORED value (what the writer saw), not
	// the response envelope. The executable markup must be gone from the bytes
	// that reached persistence.
	stored, err := repos.Media.ByID(context.Background(), 201)
	if err != nil {
		t.Fatalf("repos.Media.ByID(201): %v", err)
	}
	if strings.Contains(stored.Caption, "<script") {
		t.Errorf("stored caption still carries a <script> element: %q", stored.Caption)
	}
	if strings.Contains(stored.Description, "<script") {
		t.Errorf("stored description still carries a <script> element: %q", stored.Description)
	}
	// The dangerous element was dropped at the write boundary, leaving the
	// inert surrounding text — exactly the real policy's tier-B output.
	if stored.Caption != wantCap {
		t.Errorf("stored caption = %q, want policy oracle %q (PostExcerpt, tier B)", stored.Caption, wantCap)
	}
	if stored.Description != wantDesc {
		t.Errorf("stored description = %q, want policy oracle %q (PostContent, tier B)", stored.Description, wantDesc)
	}
}

// TestRESTMediaCreateUpdateGetRoundTripReflectsSanitizedValues — Req 10.3: a
// full write→read round-trip through the SAME router. Create a fresh attachment
// (POST /media with a Content-Disposition filename and a real PNG body), take
// its id from the 201 envelope, PATCH caption+alt_text+description with values
// that include markup needing sanitization, then GET /media/{id} and assert the
// GET envelope's caption.rendered / alt_text / description.rendered reflect the
// SANITIZED stored values — consistency across the write→read boundary.
func TestRESTMediaCreateUpdateGetRoundTripReflectsSanitizedValues(t *testing.T) {
	h, _, _ := newRESTMediaWriteRouter(t, uploaderFake())

	// Create.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, mediaUploadReq(pngAllowedMIME, `attachment; filename="roundtrip.png"`, pngBytes))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201 (body=%s)", rec.Code, rec.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create envelope: %v", err)
	}
	idF, ok := created["id"].(float64)
	if !ok || idF <= 0 {
		t.Fatalf("create envelope id = %v, want a positive media ID", created["id"])
	}
	id := int64(idF)

	// Values carrying markup the tier-B / plain-text paths must sanitize.
	const (
		capIn  = `caption <script>steal()</script> body <em>kept</em>`
		altIn  = `<b>alt</b> text`
		descIn = `described <script>evil()</script> fully`
	)
	pol := sanitize.New()
	w := mediaWriterTierForUploader()
	wantCap, err := pol.Sanitize(sanitize.PostExcerpt, w, capIn)
	if err != nil {
		t.Fatalf("oracle caption: %v", err)
	}
	wantAlt, err := pol.Sanitize(sanitize.PostTitle, w, altIn)
	if err != nil {
		t.Fatalf("oracle alt: %v", err)
	}
	wantDesc, err := pol.Sanitize(sanitize.PostContent, w, descIn)
	if err != nil {
		t.Fatalf("oracle description: %v", err)
	}

	// Update (PATCH) the three metadata fields.
	upd, err := json.Marshal(map[string]any{"caption": capIn, "alt_text": altIn, "description": descIn})
	if err != nil {
		t.Fatalf("marshal update: %v", err)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, mediaUpdateReq(http.MethodPatch, id, string(upd)))
	if rec.Code != http.StatusOK {
		t.Fatalf("update status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}

	// GET /media/{id} through the SAME router and assert the read reflects the
	// sanitized stored values (Req 10.3).
	getReq := httptest.NewRequest(http.MethodGet, "/wp-json/wp/v2/media/"+strconv.FormatInt(id, 10), nil)
	getRec := httptest.NewRecorder()
	h.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200 (body=%s)", getRec.Code, getRec.Body.String())
	}
	var env map[string]any
	if err := json.Unmarshal(getRec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode GET envelope %q: %v", getRec.Body.String(), err)
	}

	caption, ok := env["caption"].(map[string]any)
	if !ok {
		t.Fatalf("GET envelope caption not an object: %v", env["caption"])
	}
	if caption["rendered"] != wantCap {
		t.Errorf("GET caption.rendered = %v, want sanitized %q", caption["rendered"], wantCap)
	}
	if env["alt_text"] != wantAlt {
		t.Errorf("GET alt_text = %v, want sanitized %q", env["alt_text"], wantAlt)
	}
	desc, ok := env["description"].(map[string]any)
	if !ok {
		t.Fatalf("GET envelope description not an object: %v", env["description"])
	}
	if desc["rendered"] != wantDesc {
		t.Errorf("GET description.rendered = %v, want sanitized %q", desc["rendered"], wantDesc)
	}
	// Belt-and-braces: no <script> survived into the read surface.
	if s, _ := caption["rendered"].(string); strings.Contains(s, "<script") {
		t.Errorf("GET caption.rendered still carries <script>: %q", s)
	}
	if s, _ := desc["rendered"].(string); strings.Contains(s, "<script") {
		t.Errorf("GET description.rendered still carries <script>: %q", s)
	}
}

// restMediaGetBody issues GET /media/{id} through the router and returns the
// raw response body bytes, failing the test on any non-200.
func restMediaGetBody(t *testing.T, h http.Handler, id int64) []byte {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/wp-json/wp/v2/media/"+strconv.FormatInt(id, 10), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /media/%d status = %d, want 200 (body=%s)", id, rec.Code, rec.Body.String())
	}
	return append([]byte(nil), rec.Body.Bytes()...)
}

// TestRESTMediaGetUnmodifiedAttachmentIsStable — Req 10.1: reading an
// attachment this milestone never writes to is byte-identical across repeated
// reads, and the additive caption/alt_text/description keys are present in the
// expected shape (caption/description as {"rendered": …} sub-objects, alt_text
// as a bare string). There is no literal "before this milestone" snapshot at
// test time, so the intent is satisfied by capturing the GET body, performing
// an unrelated write (an update to a DIFFERENT attachment, 201), and GETting
// 202 again asserting byte-equality — proving the untouched read is stable and
// the write surface did not perturb it. The asserted values are the seed's
// (post_excerpt "excerpt" → caption, post_content "<p>body</p>" → description,
// no _wp_attachment_image_alt → empty alt_text), so this pins the real
// read-path shape rather than an assumed-empty one.
func TestRESTMediaGetUnmodifiedAttachmentIsStable(t *testing.T) {
	h, repos, _ := newRESTMediaWriteRouter(t, uploaderFake())

	// Attachment 202 ("Asset", image/png) is never written to by this
	// milestone's tests. Read its stored values so the envelope assertions pin
	// the real read-path, not an assumption.
	stored, err := repos.Media.ByID(context.Background(), 202)
	if err != nil {
		t.Fatalf("repos.Media.ByID(202): %v", err)
	}

	before := restMediaGetBody(t, h, 202)

	// The additive keys must be present in the WordPress shape and reflect the
	// stored read-path values.
	var env map[string]any
	if err := json.Unmarshal(before, &env); err != nil {
		t.Fatalf("decode GET /media/202 envelope %q: %v", string(before), err)
	}
	caption, ok := env["caption"].(map[string]any)
	if !ok {
		t.Fatalf("caption key missing/!object (additive key must be present): %v", env["caption"])
	}
	if caption["rendered"] != stored.Caption {
		t.Errorf("unmodified caption.rendered = %v, want stored %q", caption["rendered"], stored.Caption)
	}
	altText, ok := env["alt_text"]
	if !ok {
		t.Fatalf("alt_text key missing (additive key must be present); envelope=%v", env)
	}
	if altText != stored.AltText {
		t.Errorf("unmodified alt_text = %v, want stored %q", altText, stored.AltText)
	}
	desc, ok := env["description"].(map[string]any)
	if !ok {
		t.Fatalf("description key missing/!object (additive key must be present): %v", env["description"])
	}
	if desc["rendered"] != stored.Description {
		t.Errorf("unmodified description.rendered = %v, want stored %q", desc["rendered"], stored.Description)
	}

	// An unrelated write (update attachment 201) must not perturb the read of
	// the untouched attachment 202.
	wrec := httptest.NewRecorder()
	h.ServeHTTP(wrec, mediaUpdateReq(http.MethodPut, 201, `{"title":"Unrelated Edit"}`))
	if wrec.Code != http.StatusOK {
		t.Fatalf("unrelated update status = %d, want 200 (body=%s)", wrec.Code, wrec.Body.String())
	}

	after := restMediaGetBody(t, h, 202)
	if !bytes.Equal(before, after) {
		t.Errorf("GET /media/202 changed after an unrelated write:\n before=%s\n after =%s", before, after)
	}
}

// TestRESTMediaUpdateByteIdentityCaptionVerbatimIsNoOp — Req 2.4: read
// attachment 201's stored caption, then PATCH re-sending that EXACT caption
// verbatim, and assert the stored caption is unchanged afterward. Re-sending
// the stored value verbatim is a no-op (the byte-identity skip), end-to-end
// through the transport. This mirrors the content-package byte-identity test
// from task 4.2 at the REST boundary.
func TestRESTMediaUpdateByteIdentityCaptionVerbatimIsNoOp(t *testing.T) {
	h, repos, _ := newRESTMediaWriteRouter(t, uploaderFake())

	// Seed a known caption on 201 through the live write path so there is a
	// definite stored value to re-send verbatim.
	seed, err := json.Marshal(map[string]any{"caption": "a plain stored caption"})
	if err != nil {
		t.Fatalf("marshal seed: %v", err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, mediaUpdateReq(http.MethodPut, 201, string(seed)))
	if rec.Code != http.StatusOK {
		t.Fatalf("seed update status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}

	stored, err := repos.Media.ByID(context.Background(), 201)
	if err != nil {
		t.Fatalf("repos.Media.ByID(201) after seed: %v", err)
	}
	storedCaption := stored.Caption

	// Re-send the stored caption VERBATIM.
	verbatim, err := json.Marshal(map[string]any{"caption": storedCaption})
	if err != nil {
		t.Fatalf("marshal verbatim: %v", err)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, mediaUpdateReq(http.MethodPatch, 201, string(verbatim)))
	if rec.Code != http.StatusOK {
		t.Fatalf("verbatim update status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}

	after, err := repos.Media.ByID(context.Background(), 201)
	if err != nil {
		t.Fatalf("repos.Media.ByID(201) after verbatim: %v", err)
	}
	if after.Caption != storedCaption {
		t.Errorf("re-sending the stored caption verbatim altered it: before=%q after=%q", storedCaption, after.Caption)
	}
}
