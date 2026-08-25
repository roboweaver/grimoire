package web

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/roboweaver/grimoire/internal/content"
)

// WithAdmin enables the read-only admin: the embedded React Spectrum SPA (spa)
// served under /admin and the JSON API backed by svc under /admin/api. It
// returns the same Server for chaining. When either argument is nil the admin
// routes are not registered.
func (s *Server) WithAdmin(spa http.Handler, svc *content.AdminService) *Server {
	s.spa = spa
	s.admin = svc
	return s
}

// WithAdminWrites enables the M6 admin write routes: post create/update/delete
// and term create/update/delete under /admin/api, each gated by the same
// capability the read routes require plus a CSRF check. It returns the same
// Server for chaining. When any argument is nil the write routes are not
// registered (WithAdmin's read-only routes are unaffected either way).
func (s *Server) WithAdminWrites(postWrite postAdminWriter, termWrite termAdminService, postTermsWrite postTermsAdminWriter, postTermsRead postTermsAdminReader) *Server {
	s.postWrite = postWrite
	s.termWrite = termWrite
	s.postTermsWrite = postTermsWrite
	s.postTermsRead = postTermsRead
	return s
}

// registerAdmin mounts the /admin group onto r. It MUST be called before the
// public catch-all /{slug} route so admin paths are never shadowed by content
// resolution (Req 1.2). The JSON API is a nested subrouter registered ahead of
// the SPA fallback so /admin/api/* never falls through to index.html.
func (s *Server) registerAdmin(r chi.Router) {
	if s.spa == nil || s.admin == nil {
		return
	}
	r.Route("/admin", func(ar chi.Router) {
		// JSON API first: a static /api segment out-prioritizes the SPA wildcard.
		ar.Mount("/api", s.adminAPIRouter())

		// Page shell: capability-gated, SPA fallback for every other /admin path.
		ar.Group(func(pr chi.Router) {
			pr.Use(s.RequireLogin)
			pr.Use(s.RequireCapability("edit_posts"))
			pr.Handle("/", s.spa)
			pr.Handle("/*", s.spa)
		})
	})
}

// adminAPIRouter builds the /admin/api subrouter: JSON-only errors for unknown
// paths (404) and unsupported methods (405), a login gate on everything, and a
// capability gate on the content endpoints (session needs only a valid session,
// per Req 2.6).
func (s *Server) adminAPIRouter() http.Handler {
	r := chi.NewRouter()
	r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		writeJSONError(w, http.StatusNotFound, "not_found", "resource not found")
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
		writeJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	})
	r.Use(s.requireLoginJSON)

	r.Method(http.MethodGet, "/session", s.jsonHandler(s.adminSession))

	r.Group(func(gr chi.Router) {
		gr.Use(s.requireCapabilityJSON("edit_posts"))
		gr.Method(http.MethodGet, "/stats", s.jsonHandler(s.adminStats))
		gr.Method(http.MethodGet, "/posts", s.jsonHandler(s.adminPosts))
		gr.Method(http.MethodGet, "/posts/{id}", s.jsonHandler(s.adminPost))
		gr.Method(http.MethodGet, "/menus", s.jsonHandler(s.adminMenus))
		gr.Method(http.MethodGet, "/menus/{id}", s.jsonHandler(s.adminMenu))
		gr.Method(http.MethodGet, "/terms", s.jsonHandler(s.adminTerms))
		if s.postWrite != nil {
			gr.Group(func(wgr chi.Router) {
				wgr.Use(s.csrfJSONMiddleware) // adapter around the existing M4 helper
				wgr.Method(http.MethodPost, "/posts", s.jsonHandler(s.adminPostCreate))
				wgr.Method(http.MethodPut, "/posts/{id}", s.jsonHandler(s.adminPostUpdate))
				wgr.Method(http.MethodDelete, "/posts/{id}", s.jsonHandler(s.adminPostDelete))
			})
		}
	})
	if s.termWrite != nil {
		r.Group(func(gr chi.Router) {
			gr.Use(s.requireCapabilityJSON("manage_categories"))
			gr.Use(s.csrfJSONMiddleware)
			gr.Method(http.MethodPost, "/terms", s.jsonHandler(s.adminTermCreate))
			gr.Method(http.MethodPut, "/terms/{id}", s.jsonHandler(s.adminTermUpdate))
			gr.Method(http.MethodDelete, "/terms/{id}", s.jsonHandler(s.adminTermDelete))
		})
	}
	r.Group(func(gr chi.Router) {
		gr.Use(s.requireCapabilityJSON("moderate_comments"))
		gr.Method(http.MethodGet, "/comments", s.jsonHandler(s.adminComments))
		gr.Method(http.MethodPost, "/comments/{id}/{action}", s.jsonHandler(s.adminCommentAction))
		gr.Method(http.MethodPost, "/comments/{id}/status", s.jsonHandler(s.adminCommentStatus))
	})
	r.Group(func(gr chi.Router) {
		gr.Use(s.requireCapabilityJSON("upload_files"))
		gr.Method(http.MethodGet, "/media", s.jsonHandler(s.adminMedia))
		gr.Method(http.MethodPost, "/media", s.jsonHandler(s.adminMedia))
		gr.Method(http.MethodPost, "/media/{id}/attach", s.jsonHandler(s.adminMediaAttach))
	})
	return r
}
