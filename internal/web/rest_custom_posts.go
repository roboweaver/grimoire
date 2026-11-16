package web

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// registerRESTCustomPosts registers the REST surface for every registered post
// type, public or not (M12 design 8c, M17 write). GET serves the shared posts
// handlers. When a post-write service is configured, POST/PUT/PATCH/DELETE serve
// the shared write handlers; otherwise, and for the combinations no write
// handler covers, the verb answers 501.
func (s *Server) registerRESTCustomPosts(r chi.Router) {
	if s.registry == nil {
		return
	}
	for _, pt := range s.registry.PostTypes() {
		path := "/" + pt.RESTBase
		single := path + "/{id}"
		resource := "Writing a " + pt.Name
		r.Method(http.MethodGet, path, s.handleRESTPostsCollection(pt.Name))
		r.Method(http.MethodGet, single, s.handleRESTPostSingle(pt.Name))
		if s.postWrite != nil {
			r.Method(http.MethodPost, path, s.handleRESTPostCreate(pt.Name))
			r.Method(http.MethodPut, single, s.handleRESTPostUpdate(pt.Name))
			r.Method(http.MethodPatch, single, s.handleRESTPostUpdate(pt.Name))
			r.Method(http.MethodDelete, single, s.handleRESTPostDelete(pt.Name))
			for _, m := range []string{http.MethodPut, http.MethodPatch, http.MethodDelete} {
				r.Method(m, path, restNotImplemented("rest_cannot_edit", resource))
			}
		} else {
			r.Method(http.MethodPost, path, restNotImplemented("rest_cannot_create", resource))
			for _, m := range []string{http.MethodPut, http.MethodPatch, http.MethodDelete} {
				r.Method(m, path, restNotImplemented("rest_cannot_edit", resource))
				r.Method(m, single, restNotImplemented("rest_cannot_edit", resource))
			}
		}
		r.Method(http.MethodPost, single, restNotImplemented("rest_cannot_edit", resource))
	}
}
