package web_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/roboweaver/grimoire/pkg/extensions"
)

// phase8CtxKey scopes the test filter registered on "render.post_html"
// below to only the specific requests this test issues, via a context value
// set on each request. This matters because pkg/extensions's registry is
// process-global and append-only (no unregister): without scoping, a filter
// registered here would keep firing for every other test in this package
// that happens to render a single/page view afterwards, for the remainder
// of the test binary's run. Checking an unexported, test-local context key
// means every *other* request (untouched by this test) still observes the
// hook as if nothing were registered, which is exactly the "no filter
// registered" behavior Phase 8.1 requires as its regression guard.
type phase8CtxKey struct{}

type phase8Action string

const (
	phase8ActionNone   phase8Action = ""
	phase8ActionMark   phase8Action = "mark"
	phase8ActionPanic  phase8Action = "panic"
	phase8MarkerString              = "<!--phase8-marker-->"
)

func phase8Request(method, path string, action phase8Action) *http.Request {
	req := httptest.NewRequest(method, path, nil)
	if action != phase8ActionNone {
		req = req.WithContext(context.WithValue(req.Context(), phase8CtxKey{}, action))
	}
	return req
}

// registerPhase8Filter registers, exactly once for the whole test binary
// run (sync.Once via a package-level guard would be another option, but a
// bare var+init suffices here since this file's TestMain-less package has
// no other registrant for this hook), the single filter every subtest in
// TestRenderPostHTMLExtensionPoint relies on.
var phase8FilterRegistered = false

func registerPhase8Filter() {
	if phase8FilterRegistered {
		return
	}
	phase8FilterRegistered = true
	extensions.RegisterFilter("render.post_html", func(ctx context.Context, html []byte) ([]byte, error) {
		switch action, _ := ctx.Value(phase8CtxKey{}).(phase8Action); action {
		case phase8ActionMark:
			return append(html, []byte(phase8MarkerString)...), nil
		case phase8ActionPanic:
			panic("phase8 boom")
		default:
			return html, nil
		}
	})
}

// TestRenderPostHTMLExtensionPoint covers Phase 8.1's acceptance list for
// the "render.post_html" filter (Req 11.1): a registered filter observably
// transforms the rendered HTML of a public single/page request; with no
// filter registered (here: registered but not triggered by context, which
// is behaviorally identical from the handler's perspective) the response is
// unaffected; the admin SPA and REST JSON responses are never passed
// through it; a panicking filter is recovered and surfaces as a 500, not a
// crashed request.
func TestRenderPostHTMLExtensionPoint(t *testing.T) {
	registerPhase8Filter()

	t.Run("NoFilterRegistered_Unaffected", func(t *testing.T) {
		h := newRESTRouter(t, &fakeSessions{})

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, phase8Request(http.MethodGet, "/hello-1", phase8ActionNone))
		if rec.Code != http.StatusOK {
			t.Fatalf("single status = %d, body: %s", rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), phase8MarkerString) {
			t.Fatalf("marker present with no filter action requested: %s", rec.Body.String())
		}

		pageRec := httptest.NewRecorder()
		h.ServeHTTP(pageRec, phase8Request(http.MethodGet, "/about", phase8ActionNone))
		if pageRec.Code != http.StatusOK {
			t.Fatalf("page status = %d, body: %s", pageRec.Code, pageRec.Body.String())
		}
		if strings.Contains(pageRec.Body.String(), phase8MarkerString) {
			t.Fatalf("marker present with no filter action requested: %s", pageRec.Body.String())
		}
	})

	t.Run("FilterAppliesToSingleAndPageOnly", func(t *testing.T) {
		h := newRESTRouter(t, &fakeSessions{})

		single := httptest.NewRecorder()
		h.ServeHTTP(single, phase8Request(http.MethodGet, "/hello-1", phase8ActionMark))
		if single.Code != http.StatusOK {
			t.Fatalf("single status = %d, body: %s", single.Code, single.Body.String())
		}
		if !strings.Contains(single.Body.String(), phase8MarkerString) {
			t.Fatalf("single response missing filter marker: %s", single.Body.String())
		}

		page := httptest.NewRecorder()
		h.ServeHTTP(page, phase8Request(http.MethodGet, "/about", phase8ActionMark))
		if page.Code != http.StatusOK {
			t.Fatalf("page status = %d, body: %s", page.Code, page.Body.String())
		}
		if !strings.Contains(page.Body.String(), phase8MarkerString) {
			t.Fatalf("page response missing filter marker: %s", page.Body.String())
		}

		home := httptest.NewRecorder()
		h.ServeHTTP(home, phase8Request(http.MethodGet, "/", phase8ActionMark))
		if home.Code != http.StatusOK {
			t.Fatalf("home status = %d, body: %s", home.Code, home.Body.String())
		}
		if strings.Contains(home.Body.String(), phase8MarkerString) {
			t.Fatalf("home (kind=index, not single/page) was filtered: %s", home.Body.String())
		}
	})

	t.Run("NotAppliedToRESTJSON", func(t *testing.T) {
		h := newRESTRouter(t, &fakeSessions{})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, phase8Request(http.MethodGet, "/wp-json/wp/v2/pages?slug=about", phase8ActionMark))
		if rec.Code != http.StatusOK {
			t.Fatalf("REST status = %d, body: %s", rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), phase8MarkerString) {
			t.Fatalf("REST JSON response was filtered by render.post_html: %s", rec.Body.String())
		}
	})

	t.Run("NotAppliedToAdminSPA", func(t *testing.T) {
		// The admin SPA is served directly via the raw http.Handler passed
		// to WithAdmin (never through renderHTML), so it structurally
		// cannot be touched by this filter either; exercised here as a
		// regression guard.
		h := newRESTRouter(t, &fakeSessions{})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, phase8Request(http.MethodGet, "/admin/", phase8ActionMark))
		if strings.Contains(rec.Body.String(), phase8MarkerString) {
			t.Fatalf("admin SPA response was filtered by render.post_html: %s", rec.Body.String())
		}
	})

	t.Run("PanickingFilterRecoveredAs500", func(t *testing.T) {
		h := newRESTRouter(t, &fakeSessions{})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, phase8Request(http.MethodGet, "/hello-1", phase8ActionPanic))
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500; body: %s", rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), phase8MarkerString) || strings.Contains(rec.Body.String(), "<html") {
			t.Fatalf("panic response leaked partial/pre-filter HTML instead of a clean 500: %s", rec.Body.String())
		}
	})
}
