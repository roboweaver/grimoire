package web

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/roboweaver/grimoire/internal/domain"
)

// navMenuItemResponse is the camelCase, recursive wire shape
// web/admin/src/api/types.ts expects for a nav menu item (Req 3).
type navMenuItemResponse struct {
	ID       int64                 `json:"id"`
	Label    string                `json:"label"`
	URL      string                `json:"url"`
	Type     string                `json:"type"`
	Object   string                `json:"object"`
	ObjectID int64                 `json:"objectId"`
	ParentID int64                 `json:"parentId"`
	Order    int                   `json:"order"`
	Children []navMenuItemResponse `json:"children"`
}

type navMenuResponse struct {
	ID    int64                 `json:"id"`
	Name  string                `json:"name"`
	Slug  string                `json:"slug"`
	Items []navMenuItemResponse `json:"items"`
}

func navMenuItemDTO(item domain.NavMenuItem) navMenuItemResponse {
	children := make([]navMenuItemResponse, 0, len(item.Children))
	for _, c := range item.Children {
		children = append(children, navMenuItemDTO(c))
	}
	return navMenuItemResponse{
		ID:       item.ID,
		Label:    item.Label,
		URL:      item.URL,
		Type:     item.Type,
		Object:   item.Object,
		ObjectID: item.ObjectID,
		ParentID: item.ParentID,
		Order:    item.Order,
		Children: children,
	}
}

func navMenuDTO(menu domain.NavMenu) navMenuResponse {
	items := make([]navMenuItemResponse, 0, len(menu.Items))
	for _, item := range menu.Items {
		items = append(items, navMenuItemDTO(item))
	}
	return navMenuResponse{ID: menu.ID, Name: menu.Name, Slug: menu.Slug, Items: items}
}

// adminMenus returns the full menu list (unpaginated: design.md's route
// table and the SPA's client.ts both treat /menus as a flat {items:[...]}
// list, not a paginated envelope).
func (s *Server) adminMenus(w http.ResponseWriter, r *http.Request) error {
	menus, err := s.menus.List(r.Context())
	if err != nil {
		return err
	}
	out := make([]navMenuResponse, 0, len(menus))
	for _, m := range menus {
		out = append(out, navMenuDTO(m))
	}
	return writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

// adminMenu handles GET /admin/api/menus/{id}: a single menu with its item
// tree, 404 on a missing/unknown id (Req 7).
func (s *Server) adminMenu(w http.ResponseWriter, r *http.Request) error {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		return badRequestError{msg: "invalid menu id"}
	}
	menu, err := s.menus.Get(r.Context(), id)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, navMenuDTO(menu))
}
