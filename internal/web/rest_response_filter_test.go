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
	"github.com/roboweaver/grimoire/pkg/extensions"
)

// restResponseFilterCtxKey scopes the "rest.response" test filter below to
// only the specific requests a subtest issues, the same way
// rest_render_post_html_test.go's phase8CtxKey scopes its filter: pkg/
// extensions's registry is process-global and append-only, so an unscoped
// filter registered here would keep firing for every other test in this
// package for the remainder of the test binary's run.
type restResponseFilterCtxKey struct{}

const restResponseFilterMarkerField = "__rest_response_filter_marker__"

var restResponseFilterRegistered = false

// registerRESTResponseMarkerFilter registers, exactly once for the whole
// test binary run, a "rest.response" filter that stamps a marker field onto
// any map[string]any response body when the request context carries the
// restResponseFilterCtxKey marker. Since ApplyFilters is called with a
// static `any`-typed value from writeRESTResponse, this filter is
// registered against T=any and type-switches internally to handle every
// response shape (map, slice, struct) actually produced by wp/v2 handlers.
func registerRESTResponseMarkerFilter() {
	if restResponseFilterRegistered {
		return
	}
	restResponseFilterRegistered = true
	extensions.RegisterFilter("rest.response", func(ctx context.Context, v any) (any, error) {
		if marked, _ := ctx.Value(restResponseFilterCtxKey{}).(bool); !marked {
			return v, nil
		}
		switch t := v.(type) {
		case map[string]any:
			out := make(map[string]any, len(t)+1)
			for k, val := range t {
				out[k] = val
			}
			out[restResponseFilterMarkerField] = true
			return out, nil
		default:
			// Non-map response bodies (e.g. the app-password list's
			// []restAppPassword slice, or the create endpoint's struct)
			// are wrapped so the marker is still observable without
			// needing per-shape filter logic.
			return map[string]any{"data": t, restResponseFilterMarkerField: true}, nil
		}
	})
}

func restResponseFilterRequest(method, path string) *http.Request {
	req := httptest.NewRequest(method, path, nil)
	return req.WithContext(context.WithValue(req.Context(), restResponseFilterCtxKey{}, true))
}

// TestRESTResponseFilterFiresOnIndexAndAppPasswordRoutes is a regression
// test for review finding 4: the "rest.response" filter (Req 11.2, "every
// wp/v2 route") previously did not fire for the bare /wp-json/ index, the
// /wp-json/wp/v2/ namespace index, or any of the three app-password
// self-service endpoints, because those handlers wrote via writeRESTJSON
// directly instead of writeRESTResponse. All five must now be observably
// filtered.
func TestRESTResponseFilterFiresOnIndexAndAppPasswordRoutes(t *testing.T) {
	registerRESTResponseMarkerFilter()

	fake := &fakeSessions{
		authPrincipal: auth.Principal{UserID: 1, Login: "admin"},
		authSession:   domain.Session{ID: "s1", CSRFToken: "tok"},
	}
	h, _, ap := newAppPasswordRESTRouter(t, fake, false, "")
	created, _, err := ap.Create(context.Background(), 1, "seed")
	if err != nil {
		t.Fatalf("seed Create: %v", err)
	}

	assertMarked := func(t *testing.T, rec *httptest.ResponseRecorder) {
		t.Helper()
		if !strings.Contains(rec.Body.String(), restResponseFilterMarkerField) {
			t.Fatalf("response missing rest.response filter marker: status=%d body=%s", rec.Code, rec.Body.String())
		}
		var out map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v; body=%s", err, rec.Body.String())
		}
		if out[restResponseFilterMarkerField] != true {
			t.Fatalf("marker field not true in decoded body: %#v", out)
		}
	}

	t.Run("BareIndex", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, restResponseFilterRequest(http.MethodGet, "/wp-json/"))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
		}
		assertMarked(t, rec)
	})

	t.Run("NamespaceIndex", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, restResponseFilterRequest(http.MethodGet, "/wp-json/wp/v2/"))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
		}
		assertMarked(t, rec)
	})

	t.Run("AppPasswordsList", func(t *testing.T) {
		req := restResponseFilterRequest(http.MethodGet, "/wp-json/wp/v2/users/me/application-passwords")
		req.AddCookie(&http.Cookie{Name: "grimoire_session", Value: "anything"})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
		}
		assertMarked(t, rec)
	})

	t.Run("AppPasswordsCreate", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/wp-json/wp/v2/users/me/application-passwords", strings.NewReader(`{"name":"filter test"}`))
		req = req.WithContext(context.WithValue(req.Context(), restResponseFilterCtxKey{}, true))
		req.AddCookie(&http.Cookie{Name: "grimoire_session", Value: "anything"})
		req.Header.Set("X-CSRF-Token", "tok")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
		}
		assertMarked(t, rec)
	})

	t.Run("AppPasswordsRevoke", func(t *testing.T) {
		req := restResponseFilterRequest(http.MethodDelete, "/wp-json/wp/v2/users/me/application-passwords/"+created.UUID)
		req.AddCookie(&http.Cookie{Name: "grimoire_session", Value: "anything"})
		req.Header.Set("X-CSRF-Token", "tok")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
		}
		assertMarked(t, rec)
	})
}
