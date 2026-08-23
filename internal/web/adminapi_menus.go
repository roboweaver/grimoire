package web

import "net/http"

func (s *Server) adminMenus(w http.ResponseWriter, r *http.Request) error {
	menus, err := s.menus.List(r.Context())
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, map[string]any{"items": menus})
}
