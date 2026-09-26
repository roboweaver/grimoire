package web_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roboweaver/grimoire/internal/admin"
	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/render"
	"github.com/roboweaver/grimoire/internal/routing"
	"github.com/roboweaver/grimoire/internal/storage"
	"github.com/roboweaver/grimoire/internal/storage/migrate"
	"github.com/roboweaver/grimoire/internal/storage/storagetest"
	"github.com/roboweaver/grimoire/internal/web"
)

// This file is Req 9.6's assertion: registering the M9b archive routes must not
// change the behavior of any route registered before the dispatcher. It is a
// regression net rather than a specification — every expectation below is the
// response the route produces *today*, captured before tasks 7.5-7.7 replace the
// route registration wholesale. It therefore passes now; its value is that it
// fails if that replacement moves any of these.
//
// The design's claim is that these routes are safe because they all have static
// first segments and chi prefers a static node over a parameter node
// (design.md, "The structural idea: one handler, one classifier"). That is a
// claim about chi's internals, which is exactly the kind of claim Req 9.6 says
// must be asserted rather than concluded by inspection.
//
// Every case runs against all five permalink structures, because the route
// registration under test is derived from the structure: a regression that only
// appears under, say, a single-segment structure (where the permalink pattern
// collides with /{slug} at chi's parameter node) would otherwise go unseen.

// regressionStructures is every permalink structure the M9a presets cover, plus
// the flat/plain setting. The flat entry is the pre-M9a baseline and the others
// are the shapes whose chi patterns can collide with the routes below.
var regressionStructures = []string{
	"", // WordPress's "plain" setting
	structPostName,
	structDayAndName,
	structMonthAndName,
	structNumeric,
}

// canonicalForStructure maps a structure to hello-1's canonical permalink under
// it, mirroring TestCanonicalPathDoesNotRedirect. The empty structure has no
// permalink pattern at all, so the flat path is canonical.
var canonicalForStructure = map[string]string{
	"":                 "/hello-1",
	structPostName:     "/hello-1/",
	structDayAndName:   helloCanonical,
	structMonthAndName: "/2024/01/hello-1/",
	structNumeric:      "/archives/1/",
}

// newFullRouter builds the router with *every* optional dependency wired: auth,
// comments, media, menus, theme static, the admin SPA + JSON API, and REST. No
// existing helper does — newTestServerSeeded wires none of them, newCommentServer
// omits auth/admin/REST, newRESTRouterWithPermalinks omits media — and a
// regression net that cannot reach a route cannot notice it breaking.
//
// The returned uploads directory is the MediaService root, so a test can plant a
// real file and assert the uploads route serves it rather than asserting a 404
// that a missing route would produce just as well.
//
// fake is configured with an administrator principal: a request carrying the
// session cookie (see authed) is that administrator, and a request without one is
// anonymous, so both sides of every gated route are reachable from one server.
func newFullRouter(t *testing.T, structure string) (http.Handler, string) {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	uploads := filepath.Join(root, "uploads")
	cfg := config.DatabaseConfig{
		Vendor:      "sqlite",
		DSN:         filepath.Join(root, "grimoire.db"),
		TablePrefix: "wp_",
	}
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

	themesDir := filepath.Join("..", "..", "themes")
	eng, err := render.Load(themesDir, "default")
	if err != nil {
		t.Fatalf("render.Load: %v", err)
	}
	st, err := routing.Parse(structure, "", "")
	if err != nil {
		t.Fatalf("routing.Parse(%q): %v", structure, err)
	}

	comments := content.NewCommentService(
		repos.Comments, repos.CommentWriter, repos.CommentMeta, repos.PostWriter,
		content.NewBasicCommentSpamFilter(content.BasicCommentSpamFilterConfig{}),
	)
	media := content.NewMediaService(repos.Media, repos.MediaWriter, content.MediaConfig{
		UploadsDir: uploads,
		BaseURL:    "/wp-content/uploads",
	})
	menus := content.NewNavMenuService(repos.NavMenus, "default")
	adminSvc := content.NewAdminService(
		repos.AdminPosts, repos.PostWriter, repos.PostCounter,
		repos.UserCounter, repos.TermCounter, repos.Users,
	)
	mapper := content.NewRESTMapper(repos.PostTerms, repos.PostMeta, repos.UserMeta, "wp_").
		WithPermalinks(st)
	fake := &fakeSessions{
		authPrincipal: auth.NewPrincipal(1, "admin", []string{"administrator"}),
	}

	srv := web.NewServer(
		content.NewPostService(repos.Posts).WithCounter(repos.PostCounter).WithAuthors(repos.Users),
		content.NewTermService(repos.Terms, repos.Posts).WithHierarchy(repos.TermReader),
		content.NewOptionService(repos.Options),
		eng,
		nil,
	).WithPermalinks(st).
		WithThemeStatic(themesDir, "default").
		WithAuth(fake, web.AuthConfig{}).
		WithContentFeatures(comments, media, menus).
		WithAdmin(admin.Handler("/admin"), adminSvc).
		WithREST(mapper, repos.AdminPosts, repos.PostWriter, repos.Posts, repos.Media, repos.Users, 0)
	return srv.Routes(), uploads
}

// routeCase is one request and the response it produces today. wantLocation is
// asserted even when empty, so a 200 that also emits a Location cannot pass; the
// same goes for wantBody and wantContentType, which are skipped only when empty.
type routeCase struct {
	name            string
	method          string
	path            string
	session         bool // send the session cookie the fake authenticates
	form            url.Values
	wantStatus      int
	wantLocation    string
	wantBodyHas     string
	wantContentType string
}

func runRouteCases(t *testing.T, h http.Handler, cases []routeCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var req *http.Request
			if tc.form != nil {
				req = httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.form.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			} else {
				req = httptest.NewRequest(tc.method, tc.path, nil)
			}
			if tc.session {
				req = authed(req)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("%s %s = %d, want %d (body: %.200s)",
					tc.method, tc.path, rec.Code, tc.wantStatus, rec.Body.String())
			}
			if got := rec.Header().Get("Location"); got != tc.wantLocation {
				t.Errorf("%s %s Location = %q, want %q", tc.method, tc.path, got, tc.wantLocation)
			}
			if tc.wantContentType != "" {
				if got := rec.Header().Get("Content-Type"); got != tc.wantContentType {
					t.Errorf("%s %s Content-Type = %q, want %q",
						tc.method, tc.path, got, tc.wantContentType)
				}
			}
			if tc.wantBodyHas != "" && !strings.Contains(rec.Body.String(), tc.wantBodyHas) {
				t.Errorf("%s %s body does not contain %q; body: %.400s",
					tc.method, tc.path, tc.wantBodyHas, rec.Body.String())
			}
		})
	}
}

// structureIndependentCases are the routes registered before the dispatcher
// whose behavior does not depend on the permalink structure. They are asserted
// under every structure, which is the actual content of Req 9.6: the structure
// decides what the dispatcher registers, so "unaffected" means "the same under
// all of them".
//
// The trailing-slash form is included wherever chi treats it as a second route,
// because that second form is the one that can fall through to a permalink
// pattern or to /{slug} and so is where a shadowing regression would surface
// first.
func structureIndependentCases() []routeCase {
	const html = "text/html; charset=utf-8"
	const jsonCT = "application/json; charset=utf-8"
	return []routeCase{
		// --- /healthz ---
		{
			name:            "healthz",
			method:          http.MethodGet,
			path:            "/healthz",
			wantStatus:      http.StatusOK,
			wantBodyHas:     "ok",
			wantContentType: "text/plain; charset=utf-8",
		},
		{
			// Not a registered route in either slash form, so it falls through
			// to content resolution, which finds no post named "healthz".
			name:       "healthz with a trailing slash 404s",
			method:     http.MethodGet,
			path:       "/healthz/",
			wantStatus: http.StatusNotFound,
		},

		// --- / ---
		{
			name:            "home",
			method:          http.MethodGet,
			path:            "/",
			wantStatus:      http.StatusOK,
			wantBodyHas:     "grimoire test",
			wantContentType: html,
		},
		{
			// The out-of-range pagination guard, which shares its shape with the
			// archive guard tasks 7.7 adds.
			name:       "home page past the last page 404s",
			method:     http.MethodGet,
			path:       "/?page=999",
			wantStatus: http.StatusNotFound,
		},

		// --- /login, /logout ---
		{
			name:            "login form",
			method:          http.MethodGet,
			path:            "/login",
			wantStatus:      http.StatusOK,
			wantBodyHas:     `name="csrf_token"`,
			wantContentType: html,
		},
		{
			name:       "login form with a trailing slash 404s",
			method:     http.MethodGet,
			path:       "/login/",
			wantStatus: http.StatusNotFound,
		},
		{
			// Unauthenticated POST with no double-submit token: 403, not the 404
			// or 405 an unregistered route would produce.
			name:       "login submit without a CSRF token is forbidden",
			method:     http.MethodPost,
			path:       "/login",
			form:       url.Values{"log": {"admin"}, "pwd": {"pw"}},
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "logout without a CSRF token is forbidden",
			method:     http.MethodPost,
			path:       "/logout",
			form:       url.Values{},
			wantStatus: http.StatusForbidden,
		},

		// --- /comment ---
		{
			// The route is reachable and its handler ran: a missing route would
			// 404/405 here rather than reaching the CSRF check.
			name:       "comment submit without a CSRF token is forbidden",
			method:     http.MethodPost,
			path:       "/comment",
			form:       url.Values{"post_id": {"1"}, "author": {"A"}, "email": {"a@example.com"}, "content": {"Hi"}},
			wantStatus: http.StatusForbidden,
		},

		// --- /wp-content/uploads/* ---
		{
			name:        "uploads serves a planted file",
			method:      http.MethodGet,
			path:        "/wp-content/uploads/2026/08/demo.txt",
			wantStatus:  http.StatusOK,
			wantBodyHas: "hello",
		},
		{
			name:       "uploads 404s a missing file",
			method:     http.MethodGet,
			path:       "/wp-content/uploads/2026/08/nope.txt",
			wantStatus: http.StatusNotFound,
		},
		{
			// The wildcard matches with an empty remainder, which names no file.
			name:       "uploads root 404s",
			method:     http.MethodGet,
			path:       "/wp-content/uploads/",
			wantStatus: http.StatusNotFound,
		},

		// --- /admin and /admin/api/... ---
		{
			name:         "admin page unauthenticated redirects to login",
			method:       http.MethodGet,
			path:         "/admin",
			wantStatus:   http.StatusSeeOther,
			wantLocation: "/login?redirect=%2Fadmin",
		},
		{
			name:            "admin page authenticated serves the SPA",
			method:          http.MethodGet,
			path:            "/admin",
			session:         true,
			wantStatus:      http.StatusOK,
			wantContentType: html,
		},
		{
			// The /admin group registers both "/" and "/*", so the slash form is
			// the SPA shell too rather than a 404.
			name:            "admin page with a trailing slash serves the SPA",
			method:          http.MethodGet,
			path:            "/admin/",
			session:         true,
			wantStatus:      http.StatusOK,
			wantContentType: html,
		},
		{
			name:            "admin api session unauthenticated is JSON 401",
			method:          http.MethodGet,
			path:            "/admin/api/session",
			wantStatus:      http.StatusUnauthorized,
			wantBodyHas:     `"unauthorized"`,
			wantContentType: jsonCT,
		},
		{
			name:            "admin api session authenticated",
			method:          http.MethodGet,
			path:            "/admin/api/session",
			session:         true,
			wantStatus:      http.StatusOK,
			wantContentType: jsonCT,
		},
		{
			name:            "admin api stats authenticated",
			method:          http.MethodGet,
			path:            "/admin/api/stats",
			session:         true,
			wantStatus:      http.StatusOK,
			wantContentType: jsonCT,
		},
		{
			name:       "admin api posts authenticated",
			method:     http.MethodGet,
			path:       "/admin/api/posts",
			session:    true,
			wantStatus: http.StatusOK,
		},
		{
			name:            "admin api unknown path is JSON 404",
			method:          http.MethodGet,
			path:            "/admin/api/nope",
			session:         true,
			wantStatus:      http.StatusNotFound,
			wantBodyHas:     `"not_found"`,
			wantContentType: jsonCT,
		},
		{
			name:        "admin api wrong method is JSON 405",
			method:      http.MethodPost,
			path:        "/admin/api/stats",
			session:     true,
			wantStatus:  http.StatusMethodNotAllowed,
			wantBodyHas: `"method_not_allowed"`,
		},
		{
			// The slash form of an /admin/api endpoint is not a registered route,
			// and the subrouter's JSON NotFound answers it rather than the SPA
			// wildcard or content resolution.
			name:            "admin api endpoint with a trailing slash is JSON 404",
			method:          http.MethodGet,
			path:            "/admin/api/session/",
			session:         true,
			wantStatus:      http.StatusNotFound,
			wantBodyHas:     `"not_found"`,
			wantContentType: jsonCT,
		},

		// --- /wp-json/... ---
		{
			name:        "rest index",
			method:      http.MethodGet,
			path:        "/wp-json/",
			wantStatus:  http.StatusOK,
			wantBodyHas: `"grimoire test"`,
		},
		{
			// chi resolves the mount point's bare form to the subrouter's "/"
			// route, so both forms serve the index rather than one 404ing.
			name:        "rest index without a trailing slash",
			method:      http.MethodGet,
			path:        "/wp-json",
			wantStatus:  http.StatusOK,
			wantBodyHas: `"grimoire test"`,
		},
		{
			name:        "rest posts collection",
			method:      http.MethodGet,
			path:        "/wp-json/wp/v2/posts",
			wantStatus:  http.StatusOK,
			wantBodyHas: `"hello-3"`,
		},
		{
			name:       "rest single post",
			method:     http.MethodGet,
			path:       "/wp-json/wp/v2/posts/1",
			wantStatus: http.StatusOK,
		},
		{
			name:        "rest unknown route",
			method:      http.MethodGet,
			path:        "/wp-json/wp/v2/nope",
			wantStatus:  http.StatusNotFound,
			wantBodyHas: `"rest_no_route"`,
		},
		{
			name:        "rest users/me unauthenticated is 401",
			method:      http.MethodGet,
			path:        "/wp-json/wp/v2/users/me",
			wantStatus:  http.StatusUnauthorized,
			wantBodyHas: `"rest_not_logged_in"`,
		},
		{
			name:       "rest users/me authenticated",
			method:     http.MethodGet,
			path:       "/wp-json/wp/v2/users/me",
			session:    true,
			wantStatus: http.StatusOK,
		},

		// --- embedded and theme static assets ---
		{
			name:            "favicon",
			method:          http.MethodGet,
			path:            "/favicon.ico",
			wantStatus:      http.StatusOK,
			wantContentType: "image/x-icon",
		},
		{
			name:       "favicon with a trailing slash 404s",
			method:     http.MethodGet,
			path:       "/favicon.ico/",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "embedded icon asset",
			method:     http.MethodGet,
			path:       "/assets/icons/favicon-32x32.png",
			wantStatus: http.StatusOK,
		},
		{
			name:       "theme static asset",
			method:     http.MethodGet,
			path:       "/theme/static/css/theme.css",
			wantStatus: http.StatusOK,
		},
		{
			// http.FileServer's directory listing. Recorded because it is what
			// these routes return today, not because it is endorsed: changing it
			// should be a deliberate decision, not a side effect of task 7.6.
			name:       "embedded icon root lists the directory",
			method:     http.MethodGet,
			path:       "/assets/icons/",
			wantStatus: http.StatusOK,
		},
		{
			name:       "theme static root lists the directory",
			method:     http.MethodGet,
			path:       "/theme/static/",
			wantStatus: http.StatusOK,
		},
	}
}

// slashFormCases are the trailing-slash forms whose current response depends on
// the permalink structure, so they cannot join the invariant table. There is one:
// POST /comment/ is not a registered route under any structure, but a
// single-segment GET route whose pattern matches "/comment/" makes chi answer 405
// rather than 404, because the path matches and only the method does not.
//
// Three of the five structures register such a route:
//
//   - /%postname%/ registers the permalink pattern /{postname}/ (M9a);
//   - /%year%/%monthnum%/%day%/%postname%/ and /%year%/%monthnum%/%postname%/
//     register the bare year archive /{year}/ (task 7.6): neither carries a front,
//     so their date archives are rooted at /, and neither leads with %post_id%, so
//     no "date/" disambiguation segment applies (Req 6.7).
//
// The other two answer 404: the flat structure registers no permalink pattern and
// no date routes at all (Req 6.8), and /archives/%post_id% roots its permalink
// pattern under /archives and its date archives under /archives/date.
//
// The 405 under the two date-carrying structures is the one response in this file
// that task 7.6 moved, and it is recorded rather than suppressed. /comment itself
// is unchanged in both of its methods (see the cases above and
// TestCommentSubmissionStillSucceeds); what moved is the status a POST to a path
// that was never the comment route receives, and it moved to the status a POST to
// that same path already received under /%postname%/. It stays asserted because
// Req 9.6 is about this surface not moving without anybody noticing.
func slashFormCases(structure string) []routeCase {
	want := http.StatusNotFound
	switch structure {
	case structPostName, structDayAndName, structMonthAndName:
		want = http.StatusMethodNotAllowed
	}
	return []routeCase{{
		name:       "comment submit with a trailing slash is not the comment route",
		method:     http.MethodPost,
		path:       "/comment/",
		form:       url.Values{"post_id": {"1"}},
		wantStatus: want,
	}}
}

// TestPreDispatcherRoutesUnaffected is the regression net itself: every route
// registered before the dispatcher, under every permalink structure, with the
// exact status, Location, Content-Type and body marker it produces today
// (Req 9.6).
func TestPreDispatcherRoutesUnaffected(t *testing.T) {
	for _, structure := range regressionStructures {
		name := structure
		if name == "" {
			name = "flat"
		}
		t.Run(name, func(t *testing.T) {
			h, uploads := newFullRouter(t, structure)
			plantUpload(t, uploads)
			runRouteCases(t, h, append(structureIndependentCases(), slashFormCases(structure)...))
		})
	}
}

// plantUpload writes the file the uploads-route case reads. A real file is what
// makes that case meaningful: an unregistered /wp-content/uploads/* route would
// 404 exactly as a missing file does.
func plantUpload(t *testing.T, uploads string) {
	t.Helper()
	dir := filepath.Join(uploads, "2026", "08")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "demo.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

// TestPermalinkRoutesUnaffected covers the M9a permalink patterns and the flat
// /{slug} fallback, which are structure-dependent and so cannot live in the
// table above. Both slash forms of the canonical path are asserted, because
// M9a registers both and relies on the handler to redirect the non-canonical one
// (Req 9.6).
func TestPermalinkRoutesUnaffected(t *testing.T) {
	for _, structure := range regressionStructures {
		name := structure
		if name == "" {
			name = "flat"
		}
		canonical := canonicalForStructure[structure]
		t.Run(name, func(t *testing.T) {
			h, _ := newFullRouter(t, structure)
			cases := []routeCase{
				{
					name:            "canonical permalink renders and does not redirect",
					method:          http.MethodGet,
					path:            canonical,
					wantStatus:      http.StatusOK,
					wantContentType: "text/html; charset=utf-8",
					wantBodyHas:     "Hello One",
				},
				{
					name:       "unknown slug 404s",
					method:     http.MethodGet,
					path:       "/does-not-exist",
					wantStatus: http.StatusNotFound,
				},
				{
					// The fixture's draft: resolution is published-only, and an
					// id-shaped structure must not turn that into a disclosure.
					name:       "unpublished slug 404s",
					method:     http.MethodGet,
					path:       "/secret",
					wantStatus: http.StatusNotFound,
				},
			}
			cases = append(cases, otherSlashFormCase(structure, canonical)...)
			cases = append(cases, flatFallbackCases(structure, canonical)...)
			runRouteCases(t, h, cases)
		})
	}
}

// otherSlashFormCase asserts the non-canonical slash form of the permalink
// pattern still 301s to the canonical one rather than 404ing inside the router,
// which is the whole reason M9a registers both forms. A flat structure registers
// no permalink pattern, so it has no second form to assert.
func otherSlashFormCase(structure, canonical string) []routeCase {
	if structure == "" {
		return nil
	}
	other := strings.TrimSuffix(canonical, "/")
	if !strings.HasSuffix(canonical, "/") {
		other = canonical + "/"
	}
	return []routeCase{{
		name:         "other slash form redirects to the canonical permalink",
		method:       http.MethodGet,
		path:         other,
		wantStatus:   http.StatusMovedPermanently,
		wantLocation: canonical,
	}}
}

// flatFallbackCases cover the flat /{slug} route in both of its roles: the
// renderer, when no structure is configured, and the canonical-redirect path
// when one is. Under structPostName the flat form *is* the other slash form of
// the pattern, so it is already asserted by otherSlashFormCase.
func flatFallbackCases(structure, canonical string) []routeCase {
	if structure == "" {
		return []routeCase{{
			name:        "flat slug renders when no structure is configured",
			method:      http.MethodGet,
			path:        "/hello-1",
			wantStatus:  http.StatusOK,
			wantBodyHas: "Hello One",
		}}
	}
	if structure == structPostName {
		return nil
	}
	return []routeCase{{
		name:         "flat slug redirects to the canonical permalink",
		method:       http.MethodGet,
		path:         "/hello-1",
		wantStatus:   http.StatusMovedPermanently,
		wantLocation: canonical,
	}}
}

// TestCommentSubmissionStillSucceeds pins the /comment happy path, which the
// forbidden-without-CSRF case above cannot: a 403 proves the route is reachable,
// but only a 303 proves the handler still completes. Asserted under the flat
// structure, where the post page the token comes from renders at /hello-1.
func TestCommentSubmissionStillSucceeds(t *testing.T) {
	h, _ := newFullRouter(t, "")
	token, cookie := commentCSRFToken(t, h)
	form := url.Values{
		"post_id":            {"1"},
		"author":             {"A"},
		"email":              {"a@example.com"},
		"content":            {"Hello"},
		"comment_csrf_token": {token},
	}
	req := httptest.NewRequest(http.MethodPost, "/comment", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /comment with a valid token = %d, want 303 (body: %.200s)",
			rec.Code, rec.Body.String())
	}
}

// TestRESTPaginationHeadersUnaffected asserts the REST collection's pagination
// headers, not only its status: they are the part of the /wp-json surface that a
// change to the shared PostService counter would move, and task 7.8 rewires that
// service.
func TestRESTPaginationHeadersUnaffected(t *testing.T) {
	for _, structure := range regressionStructures {
		name := structure
		if name == "" {
			name = "flat"
		}
		t.Run(name, func(t *testing.T) {
			h, _ := newFullRouter(t, structure)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/wp-json/wp/v2/posts", nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("GET /wp-json/wp/v2/posts = %d, want 200", rec.Code)
			}
			if got := rec.Header().Get("X-WP-Total"); got != "3" {
				t.Errorf("X-WP-Total = %q, want 3", got)
			}
			if got := rec.Header().Get("X-WP-TotalPages"); got != "1" {
				t.Errorf("X-WP-TotalPages = %q, want 1", got)
			}
			var items []map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if len(items) != 3 {
				t.Fatalf("len(items) = %d, want 3 (the draft stays excluded)", len(items))
			}
		})
	}
}
