package web

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/pkg/extensions"
)

// Hook names fired from the REST surface (Req 11.2). Declared here, not in
// pkg/extensions, which stays domain-agnostic and knows nothing about
// grimoire-specific hook names (Req 10.1).
const (
	hookRESTPreDispatch = "rest.pre_dispatch"
	hookRESTResponse    = "rest.response"
)

// RESTRequestContext is the payload fired on "rest.pre_dispatch", after
// authentication resolves but before the matched route handler runs
// (Req 11.2).
type RESTRequestContext struct {
	Method    string
	Path      string
	Principal *auth.Principal
}

// restPostByID reads a single post/page by primary key regardless of status
// or type. domain.PostWriter satisfies it (mirroring content.postByID); the
// REST single-item handlers reuse that reader so no new port is required.
type restPostByID interface {
	ByID(ctx context.Context, id int64) (domain.Post, error)
}

// WithREST enables the /wp-json/wp/v2/* REST API. mapper builds the WP-shaped
// view models (Req 2.2 etc.); posts/postByID back posts+pages reads; media
// backs media reads; users backs user reads. perPageMax caps the "per_page"
// query parameter (0 defaults to 100, matching WordPress's own ceiling,
// Req 2.3). Returns the same Server for chaining. When mapper is nil the
// REST routes are not registered.
func (s *Server) WithREST(mapper *content.RESTMapper, posts domain.AdminPostRepository, postByID restPostByID, bySlug domain.PostRepository, media domain.MediaRepository, users domain.UserRepository, perPageMax int) *Server {
	s.restMapper = mapper
	s.restPosts = posts
	s.restPostByID = postByID
	s.restBySlug = bySlug
	s.restMedia = media
	s.restUsers = users
	if perPageMax <= 0 {
		perPageMax = 100
	}
	s.restPerPageMax = perPageMax
	return s
}

// registerREST mounts /wp-json/* onto r. It MUST be called before the public
// catch-all /{slug} route so REST paths are never shadowed by content
// resolution, and must not collide with /admin, /admin/api, or
// /wp-content/uploads/ (Req 1.3).
func (s *Server) registerREST(r chi.Router) {
	if s.restMapper == nil {
		return
	}
	r.Route("/wp-json", func(wr chi.Router) {
		// Mount Application Password (and, when configured, session)
		// auth ahead of every /wp-json route, including the bare
		// namespace-less index below — an invalid Basic credential must
		// be rejected with 401 on every endpoint, public or not (Req 8.6),
		// not just inside /wp/v2.
		if s.appPasswords != nil {
			wr.Use(s.ApplicationPasswordAuth)
		}
		if s.auth != nil {
			wr.Use(s.SessionMiddleware)
		}
		wr.Get("/", s.handleRESTIndex)
		wr.Route("/wp/v2", func(nr chi.Router) {
			nr.NotFound(func(w http.ResponseWriter, _ *http.Request) {
				writeRESTError(w, http.StatusNotFound, "rest_no_route", "No route was found matching the URL and request method.")
			})
			nr.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
				writeRESTError(w, http.StatusMethodNotAllowed, "rest_no_route", "Method not allowed for this route.")
			})
			nr.Use(s.restPreDispatch)
			nr.Get("/", s.handleRESTNamespaceIndex)

			s.registerRESTPosts(nr)
			s.registerRESTMedia(nr)
			s.registerRESTUsers(nr)
			s.registerRESTAppPasswords(nr)
			s.registerRESTComments(nr)
		})
	})
}

// restPreDispatch fires the "rest.pre_dispatch" action after auth middleware
// has resolved the request's principal (session cookie for now; Application
// Password auth is layered in ahead of this in Phase 6) but before the
// matched route handler runs (Req 11.2).
func (s *Server) restPreDispatch(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p *auth.Principal
		if principal, ok := PrincipalFrom(r.Context()); ok {
			p = &principal
		}
		extensions.DoAction(r.Context(), hookRESTPreDispatch, &RESTRequestContext{
			Method:    r.Method,
			Path:      r.URL.Path,
			Principal: p,
		})
		next.ServeHTTP(w, r)
	})
}

// writeRESTResponse applies the "rest.response" filter to v (Req 11.2), then
// writes it as the wp-json response body with the given status.
func writeRESTResponse(w http.ResponseWriter, r *http.Request, status int, v any) error {
	filtered, err := extensions.ApplyFilters(r.Context(), hookRESTResponse, v)
	if err != nil {
		writeRESTError(w, http.StatusInternalServerError, "rest_response_filter_failed", "An extension failed while processing the response.")
		return nil
	}
	return writeRESTJSON(w, status, filtered)
}

// restNotImplemented returns a handler that always responds 501, for a wp/v2
// write method this milestone deliberately defers to M6 (Req 7.5). code
// identifies the deferred operation (e.g. "rest_cannot_create").
func restNotImplemented(code, resource string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeRESTError(w, http.StatusNotImplemented, code, resource+" is not yet implemented via the REST API; it is deferred to a later milestone.")
	}
}

// --- Discovery / namespace index (Req 1.1-1.2) ---

// restRouteEntry is one entry of the WordPress-shaped "routes" map: the
// supported methods for a route pattern.
type restRouteEntry struct {
	Methods []string `json:"methods"`
}

// restRoutes lists every wp/v2 route this milestone implements, matching
// WordPress's own PCRE-flavored route-pattern convention closely enough for
// discovery purposes (Req 1.1).
func restRoutes() map[string]restRouteEntry {
	get := []string{http.MethodGet}
	getPost := []string{http.MethodGet, http.MethodPost}
	return map[string]restRouteEntry{
		"/wp/v2/posts":                          {Methods: get},
		"/wp/v2/posts/(?P<id>[\\d]+)":           {Methods: get},
		"/wp/v2/pages":                          {Methods: get},
		"/wp/v2/pages/(?P<id>[\\d]+)":           {Methods: get},
		"/wp/v2/comments":                       {Methods: getPost},
		"/wp/v2/comments/(?P<id>[\\d]+)":        {Methods: get},
		"/wp/v2/media":                          {Methods: get},
		"/wp/v2/media/(?P<id>[\\d]+)":           {Methods: get},
		"/wp/v2/users":                          {Methods: get},
		"/wp/v2/users/(?P<id>[\\d]+)":           {Methods: get},
		"/wp/v2/users/me":                       {Methods: get},
		"/wp/v2/users/me/application-passwords": {Methods: getPost},
		"/wp/v2/users/me/application-passwords/(?P<uuid>[\\w-]+)": {Methods: []string{http.MethodDelete}},
	}
}

// handleRESTIndex serves GET /wp-json/: the top-level API index (Req 1.1).
func (s *Server) handleRESTIndex(w http.ResponseWriter, r *http.Request) {
	name, description := s.options.SiteInfo(r.Context())
	_ = writeRESTResponse(w, r, http.StatusOK, map[string]any{
		"name":        name,
		"description": description,
		"url":         requestBaseURL(r) + "/",
		"namespaces":  []string{"wp/v2"},
		"routes":      restRoutes(),
	})
}

// handleRESTNamespaceIndex serves GET /wp-json/wp/v2/: the namespace index
// (Req 1.2).
func (s *Server) handleRESTNamespaceIndex(w http.ResponseWriter, r *http.Request) {
	_ = writeRESTResponse(w, r, http.StatusOK, map[string]any{
		"namespace": "wp/v2",
		"routes":    restRoutes(),
	})
}
