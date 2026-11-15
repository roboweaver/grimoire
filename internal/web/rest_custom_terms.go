package web

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// registerRESTCustomTerms registers the read-only REST surface for every
// registered taxonomy (M12 design 8c). Like registerRESTTerms it needs
// s.termWrite. GET serves the shared term handlers; every write verb answers
// 501. restTermBase is deliberately not used: its default arm is /tags.
func (s *Server) registerRESTCustomTerms(r chi.Router) {
	if s.registry == nil || s.termWrite == nil {
		return
	}
	for _, tax := range s.registry.Taxonomies() {
		path := "/" + tax.RESTBase
		single := path + "/{id}"
		resource := "Writing a " + tax.Name + " term"
		r.Method(http.MethodGet, path, s.handleRESTTermsCollection(tax.Name))
		r.Method(http.MethodGet, single, s.handleRESTTermSingle(tax.Name))
		r.Method(http.MethodPost, path, restNotImplemented("rest_cannot_create", resource))
		for _, m := range []string{http.MethodPut, http.MethodPatch, http.MethodDelete} {
			r.Method(m, single, restNotImplemented("rest_cannot_edit", resource))
		}
	}
}
