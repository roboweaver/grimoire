package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/roboweaver/grimoire/internal/content"
)

// Admin-API nav-menu write handlers (M10.F). These six handlers back the
// edit_theme_options + CSRF route group registered in adminroutes.go. They
// decode the partial-update request DTOs, resolve the acting principal from the
// request context (for the write service's sanitize.For writer argument), call
// the NavMenuWriteService, and — for create/update — re-read the affected menu
// through the existing read service (s.menus.Get) and emit the shared
// navMenuDTO, so the read path reflects exactly what the write persisted
// (Req 1.5, 2.5). Error mapping is the existing jsonHandler switch
// (domain.ErrNotFound -> 404, content.ErrForbidden -> 403, badRequestError ->
// 400); the content validation sentinels (invalid custom URL, unknown item
// type, invalid parent) are mapped to badRequestError here so they surface as
// 400 rather than 500 (design Finding 5, Req 4.6, 5.4, 6.2).

// --- request DTOs (partial update via pointer fields) ---

// menuCreateRequest is the POST /menus body. Slug is optional (auto-derived from
// name when omitted, resolved open question 2).
type menuCreateRequest struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
}

// menuUpdateRequest is the PUT /menus/{id} body — partial. A nil field is left
// unchanged (Req 1.2).
type menuUpdateRequest struct {
	Name *string `json:"name"`
	Slug *string `json:"slug"`
}

// menuItemCreateRequest is the POST /menus/{id}/items body.
type menuItemCreateRequest struct {
	Label    string `json:"label"`
	URL      string `json:"url"`
	Type     string `json:"type"`
	Object   string `json:"object"`
	ObjectID int64  `json:"objectId"`
	ParentID int64  `json:"parentId"`
	Order    int    `json:"order"`
}

// menuItemUpdateRequest is the PUT /menus/{id}/items/{itemId} body — partial. A
// nil field is left unchanged (Req 2.2).
type menuItemUpdateRequest struct {
	Label    *string `json:"label"`
	URL      *string `json:"url"`
	Type     *string `json:"type"`
	Object   *string `json:"object"`
	ObjectID *int64  `json:"objectId"`
	ParentID *int64  `json:"parentId"`
	Order    *int    `json:"order"`
}

// menuWriteError maps the content validation sentinels to a 400 badRequestError
// (so jsonHandler emits "bad_request"), passing everything else through
// unchanged so domain.ErrNotFound -> 404 and content.ErrForbidden -> 403 still
// resolve in the jsonHandler switch (Req 4.6, 5.4, 6.2).
func menuWriteError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, content.ErrInvalidMenuURL):
		return badRequestError{msg: "invalid menu item URL"}
	case errors.Is(err, content.ErrUnknownMenuItemType):
		return badRequestError{msg: "unknown menu item type"}
	case errors.Is(err, content.ErrInvalidMenuParent):
		return badRequestError{msg: "parent item not in menu"}
	default:
		return err
	}
}

// adminMenuCreate handles POST /admin/api/menus (Req 1.1): 201 + navMenuDTO.
func (s *Server) adminMenuCreate(w http.ResponseWriter, r *http.Request) error {
	principal, _ := PrincipalFrom(r.Context())
	var body menuCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return badRequestError{msg: "invalid request body"}
	}
	menu, err := s.navMenuWrite.CreateMenu(r.Context(), principal, content.MenuInput{
		Name: body.Name,
		Slug: body.Slug,
	})
	if err != nil {
		return menuWriteError(err)
	}
	return writeJSON(w, http.StatusCreated, navMenuDTO(menu))
}

// adminMenuUpdate handles PUT /admin/api/menus/{id} (Req 1.2, 1.4): 200 +
// navMenuDTO; 404 on an unknown menu; 400 on a non-numeric id.
func (s *Server) adminMenuUpdate(w http.ResponseWriter, r *http.Request) error {
	principal, _ := PrincipalFrom(r.Context())
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		return badRequestError{msg: "invalid menu id"}
	}
	var body menuUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return badRequestError{msg: "invalid request body"}
	}
	menu, err := s.navMenuWrite.UpdateMenu(r.Context(), principal, id, content.MenuPatchInput{
		Name: body.Name,
		Slug: body.Slug,
	})
	if err != nil {
		return menuWriteError(err)
	}
	return writeJSON(w, http.StatusOK, navMenuDTO(menu))
}

// adminMenuDelete handles DELETE /admin/api/menus/{id} (Req 1.3): 204 no body;
// 404 on an unknown menu; 400 on a non-numeric id.
func (s *Server) adminMenuDelete(w http.ResponseWriter, r *http.Request) error {
	principal, _ := PrincipalFrom(r.Context())
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		return badRequestError{msg: "invalid menu id"}
	}
	if err := s.navMenuWrite.DeleteMenu(r.Context(), principal, id); err != nil {
		return menuWriteError(err)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// adminMenuItemCreate handles POST /admin/api/menus/{id}/items (Req 2.1): 201 +
// navMenuDTO of the owning menu; 404 on an unknown menu; 400 on a non-numeric
// id, a bad custom URL, an unknown type, or a bad parent.
func (s *Server) adminMenuItemCreate(w http.ResponseWriter, r *http.Request) error {
	principal, _ := PrincipalFrom(r.Context())
	menuID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		return badRequestError{msg: "invalid menu id"}
	}
	var body menuItemCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return badRequestError{msg: "invalid request body"}
	}
	menu, err := s.navMenuWrite.CreateItem(r.Context(), principal, menuID, content.MenuItemInput{
		Label:    body.Label,
		URL:      body.URL,
		Type:     body.Type,
		Object:   body.Object,
		ObjectID: body.ObjectID,
		ParentID: body.ParentID,
		Order:    body.Order,
	})
	if err != nil {
		return menuWriteError(err)
	}
	return writeJSON(w, http.StatusCreated, navMenuDTO(menu))
}

// adminMenuItemUpdate handles PUT /admin/api/menus/{id}/items/{itemId}
// (Req 2.2, 2.4): 200 + navMenuDTO of the owning menu; 404 on an unknown
// menu/item; 400 on a non-numeric id/itemId, a bad custom URL, an unknown type,
// or a bad parent.
func (s *Server) adminMenuItemUpdate(w http.ResponseWriter, r *http.Request) error {
	principal, _ := PrincipalFrom(r.Context())
	menuID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		return badRequestError{msg: "invalid menu id"}
	}
	itemID, err := strconv.ParseInt(chi.URLParam(r, "itemId"), 10, 64)
	if err != nil {
		return badRequestError{msg: "invalid menu item id"}
	}
	var body menuItemUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return badRequestError{msg: "invalid request body"}
	}
	menu, err := s.navMenuWrite.UpdateItem(r.Context(), principal, menuID, itemID, content.MenuItemPatchInput{
		Label:    body.Label,
		URL:      body.URL,
		Type:     body.Type,
		Object:   body.Object,
		ObjectID: body.ObjectID,
		ParentID: body.ParentID,
		Order:    body.Order,
	})
	if err != nil {
		return menuWriteError(err)
	}
	return writeJSON(w, http.StatusOK, navMenuDTO(menu))
}

// adminMenuItemDelete handles DELETE /admin/api/menus/{id}/items/{itemId}
// (Req 2.3, 2.4): 204 no body; 404 on an unknown menu/item; 400 on a
// non-numeric id/itemId.
func (s *Server) adminMenuItemDelete(w http.ResponseWriter, r *http.Request) error {
	principal, _ := PrincipalFrom(r.Context())
	menuID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		return badRequestError{msg: "invalid menu id"}
	}
	itemID, err := strconv.ParseInt(chi.URLParam(r, "itemId"), 10, 64)
	if err != nil {
		return badRequestError{msg: "invalid menu item id"}
	}
	if err := s.navMenuWrite.DeleteItem(r.Context(), principal, menuID, itemID); err != nil {
		return menuWriteError(err)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
