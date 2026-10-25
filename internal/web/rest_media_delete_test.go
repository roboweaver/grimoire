package web_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/roboweaver/grimoire/internal/domain"
)

// seedDeletableAttachment inserts an attachment authored by author and returns
// its id, so the DELETE tests operate on a real stored row.
func seedDeletableAttachment(t *testing.T, create func(context.Context, domain.Media) (int64, error), author int64) int64 {
	t.Helper()
	id, err := create(context.Background(), domain.Media{
		AuthorID: author, Title: "To Delete", Filename: "2024/05/del.png", MimeType: "image/png",
	})
	if err != nil {
		t.Fatalf("seed attachment: %v", err)
	}
	return id
}

func doMediaDelete(t *testing.T, h http.Handler, fake *fakeSessions, id int64, query string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodDelete, "/wp-json/wp/v2/media/"+strconv.FormatInt(id, 10)+query, nil)
	req.AddCookie(&http.Cookie{Name: "grimoire_session", Value: "anything"})
	req.Header.Set("X-CSRF-Token", "uploader-token")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestRESTMediaDeleteSuccessEnvelope covers Req 1.1/1.2/4: an authorized
// own-attachment delete with force=true returns 200 with
// {"deleted":true,"previous":{...}} and removes the row.
func TestRESTMediaDeleteSuccessEnvelope(t *testing.T) {
	h, repos, _ := newRESTMediaWriteRouter(t, uploaderFake()) // author UserID 1
	id := seedDeletableAttachment(t, repos.MediaWriter.Create, 1)

	rec := doMediaDelete(t, h, uploaderFake(), id, "?force=true")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var env map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env["deleted"] != true {
		t.Errorf("deleted = %v, want true", env["deleted"])
	}
	prev, ok := env["previous"].(map[string]any)
	if !ok || prev["id"] == nil {
		t.Errorf("previous missing or not the media object: %v", env["previous"])
	}
	// Gone from storage.
	if _, err := repos.Media.ByID(context.Background(), id); err != domain.ErrNotFound {
		t.Errorf("attachment still present after delete: %v", err)
	}
}

// TestRESTMediaDeleteForceRequired covers Req 2.1: without force=true → 400
// rest_trash_not_supported, nothing deleted.
func TestRESTMediaDeleteForceRequired(t *testing.T) {
	h, repos, _ := newRESTMediaWriteRouter(t, uploaderFake())
	id := seedDeletableAttachment(t, repos.MediaWriter.Create, 1)

	rec := doMediaDelete(t, h, uploaderFake(), id, "") // no force
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	assertRESTErrCode(t, rec, "rest_trash_not_supported")
	if _, err := repos.Media.ByID(context.Background(), id); err != nil {
		t.Errorf("attachment must survive a force-less delete: %v", err)
	}
}

// TestRESTMediaDeleteForbidden covers Req 3: a caller who is not the author and
// lacks delete_others_posts → 403 rest_cannot_delete, nothing deleted.
func TestRESTMediaDeleteForbidden(t *testing.T) {
	// Attachment authored by user 1; caller is a subscriber (user 2, no delete caps).
	h, repos, _ := newRESTMediaWriteRouter(t, subscriberFake())
	id := seedDeletableAttachment(t, repos.MediaWriter.Create, 1)

	rec := doMediaDelete(t, h, subscriberFake(), id, "?force=true")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
	assertRESTErrCode(t, rec, "rest_cannot_delete")
	if _, err := repos.Media.ByID(context.Background(), id); err != nil {
		t.Errorf("attachment must survive a forbidden delete: %v", err)
	}
}

// TestRESTMediaDeleteUnknownID covers Req 1.4/11.3: unknown and non-numeric id
// → 404 rest_post_invalid_id.
func TestRESTMediaDeleteUnknownID(t *testing.T) {
	h, _, _ := newRESTMediaWriteRouter(t, uploaderFake())
	rec := doMediaDelete(t, h, uploaderFake(), 999999, "?force=true")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown id status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	assertRESTErrCode(t, rec, "rest_post_invalid_id")
}

// assertRESTErrCode decodes a wp-json error envelope and asserts its "code".
func assertRESTErrCode(t *testing.T, rec *httptest.ResponseRecorder, want string) {
	t.Helper()
	var e struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatalf("decode error envelope: %v (body=%s)", err, rec.Body.String())
	}
	if e.Code != want {
		t.Errorf("error code = %q, want %q (body=%s)", e.Code, want, rec.Body.String())
	}
}
