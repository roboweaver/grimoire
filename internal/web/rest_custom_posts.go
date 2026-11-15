package web

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// registerRESTCustomPosts registers the read-only REST surface for every
// registered post type, public or not (M12 design 8c). GET serves the shared
// posts handlers; every write verb answers 501.
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
		r.Method(http.MethodPost, path, restNotImplemented("rest_cannot_create", resource))
		for _, m := range []string{http.MethodPut, http.MethodPatch, http.MethodDelete} {
			r.Method(m, path, restNotImplemented("rest_cannot_edit", resource))
			r.Method(m, single, restNotImplemented("rest_cannot_edit", resource))
		}
		r.Method(http.MethodPost, single, restNotImplemented("rest_cannot_edit", resource))
	}
}
