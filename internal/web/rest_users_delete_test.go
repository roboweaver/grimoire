package web_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/storage"
)

func doUserDelete(t *testing.T, h http.Handler, id int64, query string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodDelete, "/wp-json/wp/v2/users/"+strconv.FormatInt(id, 10)+query, nil)
	req.AddCookie(&http.Cookie{Name: "grimoire_session", Value: "anything"})
	req.Header.Set("X-CSRF-Token", "user-write-token")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func seedVictimUser(t *testing.T, repos *storage.Repositories, login string) int64 {
	t.Helper()
	id, err := repos.Users.Create(context.Background(), domain.User{
		Login: login, Nicename: login, DisplayName: login, Email: login + "@example.test",
	})
	if err != nil {
		t.Fatalf("seed victim %q: %v", login, err)
	}
	return id
}

// TestRESTUserDeleteSuccessEnvelope covers Req 5.1/5.2: an admin delete with
// force=true&reassign=1 returns 200 {"deleted":true,"previous":{...}} and
// removes the user.
func TestRESTUserDeleteSuccessEnvelope(t *testing.T) {
	h, repos := newRESTUserWriteRouter(t, adminUserFake())
	victim := seedVictimUser(t, repos, "victim")

	rec := doUserDelete(t, h, victim, "?force=true&reassign=1")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var env map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env["deleted"] != true {
		t.Errorf("deleted = %v, want true", env["deleted"])
	}
	if prev, ok := env["previous"].(map[string]any); !ok || prev["id"] == nil {
		t.Errorf("previous missing or not a user object: %v", env["previous"])
	}
	if _, err := repos.Users.ByID(context.Background(), victim); err != domain.ErrNotFound {
		t.Errorf("user still present after delete: %v", err)
	}
}

// TestRESTUserDeleteForceRequired covers Req 6.1.
func TestRESTUserDeleteForceRequired(t *testing.T) {
	h, repos := newRESTUserWriteRouter(t, adminUserFake())
	victim := seedVictimUser(t, repos, "victim")

	rec := doUserDelete(t, h, victim, "?reassign=1") // no force
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	assertRESTErrCode(t, rec, "rest_trash_not_supported")
	if _, err := repos.Users.ByID(context.Background(), victim); err != nil {
		t.Errorf("user must survive a force-less delete: %v", err)
	}
}

// TestRESTUserDeleteReassignContract covers Req 7: missing, unparseable, and
// non-existent-id reassign each → 400 rest_user_invalid_reassign; false and an
// existing id succeed.
func TestRESTUserDeleteReassignContract(t *testing.T) {
	cases := []struct {
		name  string
		query string
		want  int
		code  string
	}{
		{"missing reassign", "?force=true", http.StatusBadRequest, "rest_user_invalid_reassign"},
		{"unparseable reassign", "?force=true&reassign=yes", http.StatusBadRequest, "rest_user_invalid_reassign"},
		{"nonexistent reassign id", "?force=true&reassign=999999", http.StatusBadRequest, "rest_user_invalid_reassign"},
		{"reassign false", "?force=true&reassign=false", http.StatusOK, ""},
		{"reassign existing id", "?force=true&reassign=1", http.StatusOK, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, repos := newRESTUserWriteRouter(t, adminUserFake())
			victim := seedVictimUser(t, repos, "victim")
			rec := doUserDelete(t, h, victim, c.query)
			if rec.Code != c.want {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, c.want, rec.Body.String())
			}
			if c.code != "" {
				assertRESTErrCode(t, rec, c.code)
			}
		})
	}
}

// TestRESTUserDeleteCapabilityAndSelf covers Req 8: a non-admin → 403
// rest_user_cannot_delete; an admin deleting themselves → 403
// rest_user_cannot_delete.
func TestRESTUserDeleteCapabilityAndSelf(t *testing.T) {
	t.Run("non-admin forbidden", func(t *testing.T) {
		// author (user 2) lacks delete_users.
		h, repos := newRESTUserWriteRouter(t, authorUserFake())
		victim := seedVictimUser(t, repos, "victim")
		rec := doUserDelete(t, h, victim, "?force=true&reassign=1")
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
		}
		assertRESTErrCode(t, rec, "rest_user_cannot_delete")
		if _, err := repos.Users.ByID(context.Background(), victim); err != nil {
			t.Errorf("user must survive a forbidden delete: %v", err)
		}
	})

	t.Run("admin cannot delete self", func(t *testing.T) {
		h, _ := newRESTUserWriteRouter(t, adminUserFake()) // admin is user 1
		rec := doUserDelete(t, h, 1, "?force=true&reassign=2")
		if rec.Code != http.StatusForbidden {
			t.Fatalf("self-delete status = %d, want 403; body=%s", rec.Code, rec.Body.String())
		}
		assertRESTErrCode(t, rec, "rest_user_cannot_delete")
	})
}

// TestRESTUserDeleteUnknownID covers Req 11.4.
func TestRESTUserDeleteUnknownID(t *testing.T) {
	h, _ := newRESTUserWriteRouter(t, adminUserFake())
	rec := doUserDelete(t, h, 999999, "?force=true&reassign=1")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown id status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	assertRESTErrCode(t, rec, "rest_user_invalid_id")
}
