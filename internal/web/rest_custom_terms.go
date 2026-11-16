package web

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// registerRESTCustomTerms registers the REST surface for every
// registered taxonomy (M12 design 8c, M17 D1). Like registerRESTTerms it needs
// s.termWrite. GET and the create/update/delete verbs use the shared term
// handlers; POST /{base}/{id} and collection-level PUT/PATCH/DELETE answer 501. restTermBase is deliberately not used: its default arm is /tags.
func (s *Server) registerRESTCustomTerms(r chi.Router) {
	if s.registry == nil || s.termWrite == nil {
		return
	}
	for _, tax := range s.registry.Taxonomies() {
		path := "/" + tax.RESTBase
		single := path + "/{id}"
		r.Method(http.MethodGet, path, s.handleRESTTermsCollection(tax.Name))
		r.Method(http.MethodGet, single, s.handleRESTTermSingle(tax.Name))
		r.Method(http.MethodPost, path, s.handleRESTTermCreate(tax.Name))
		r.Method(http.MethodPut, single, s.handleRESTTermUpdate(tax.Name))
		r.Method(http.MethodPatch, single, s.handleRESTTermUpdate(tax.Name))
		r.Method(http.MethodDelete, single, s.handleRESTTermDelete(tax.Name))
		// POST /{base}/{id} and collection-level PUT/PATCH/DELETE stay 501.
		resource := "Writing a " + tax.Name + " term"
		r.Method(http.MethodPost, single, restNotImplemented("rest_cannot_create", resource))
		for _, m := range []string{http.MethodPut, http.MethodPatch, http.MethodDelete} {
			r.Method(m, path, restNotImplemented("rest_cannot_edit", resource))
		}
	}
}
