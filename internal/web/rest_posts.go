package web

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/domain"
)

// registerRESTPosts registers the GET .../posts, .../pages endpoints (Req
// 2.1, 2.4) and 501 stubs for every write method (Req 2.5).
func (s *Server) registerRESTPosts(r chi.Router) {
	for _, typ := range []string{"post", "page"} {
		path, single := restPostBase(typ)
		r.Method(http.MethodGet, path, s.handleRESTPostsCollection(typ))
		r.Method(http.MethodGet, single, s.handleRESTPostSingle(typ))
		r.Method(http.MethodPost, path, restNotImplemented("rest_cannot_create", "Creating a "+typ))
		for _, m := range []string{http.MethodPut, http.MethodPatch, http.MethodDelete} {
			r.Method(m, single, restNotImplemented("rest_cannot_edit", "Updating or deleting a "+typ))
		}
	}
}

// restPostBase returns the collection and single-item route paths for a post
// type ("post" -> "/posts", "page" -> "/pages").
func restPostBase(typ string) (collection, single string) {
	base := "/" + typ + "s"
	return base, base + "/{id}"
}

// handleRESTPostsCollection serves GET .../posts and GET .../pages: a
// paginated, newest-first collection of published items of the given type
// (Req 2.1, 2.3). Draft/pending/private items are never listed here
// regardless of caller capability; that widening only applies to the
// single-item endpoint (Req 2.4).
func (s *Server) handleRESTPostsCollection(typ string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if slug := q.Get("slug"); slug != "" {
			s.writeRESTPostsBySlug(w, r, typ, slug)
			return
		}

		paging := parseRESTPaging(r, s.restPerPageMax)
		orderBy, order := restOrder(r)
		f := domain.AdminPostFilter{
			Types:    []string{typ},
			Statuses: []string{"publish"},
			Limit:    paging.PerPage,
			Offset:   paging.Offset,
			Search:   q.Get("search"),
			OrderBy:  orderBy,
			Order:    order,
		}
		posts, err := s.restPosts.ListForAdmin(r.Context(), f)
		if err != nil {
			writeRESTError(w, http.StatusInternalServerError, "rest_list_failed", "Could not list "+typ+"s.")
			return
		}
		total, err := s.restPosts.CountForAdmin(r.Context(), domain.AdminPostFilter{Types: f.Types, Statuses: f.Statuses, Search: f.Search})
		if err != nil {
			writeRESTError(w, http.StatusInternalServerError, "rest_list_failed", "Could not count "+typ+"s.")
			return
		}

		items := make([]any, 0, len(posts))
		for _, p := range posts {
			item, links, embedded, err := s.mapRESTPost(r, typ, p)
			if err != nil {
				writeRESTError(w, http.StatusInternalServerError, "rest_list_failed", "Could not map "+typ+" #"+strconv.FormatInt(p.ID, 10)+".")
				return
			}
			enveloped, err := withEnvelope(item, links, embedded)
			if err != nil {
				writeRESTError(w, http.StatusInternalServerError, "rest_list_failed", "Could not encode "+typ+" #"+strconv.FormatInt(p.ID, 10)+".")
				return
			}
			items = append(items, enveloped)
		}
		setRESTPaginationHeaders(w, total, paging.PerPage)
		_ = writeRESTResponse(w, r, http.StatusOK, items)
	}
}

// writeRESTPostsBySlug implements the "slug" collection query parameter
// (Req 2.3). AdminPostFilter has no slug field (only Search/OrderBy/Order
// were added for M5, Req 12 storage-ports), so this resolves the request
// through the existing published-only, type-scoped domain.PostRepository.BySlug
// port instead, returning a 0-or-1-item collection exactly like WordPress's
// own slug-filtered collection response.
func (s *Server) writeRESTPostsBySlug(w http.ResponseWriter, r *http.Request, typ, slug string) {
	p, err := s.restBySlug.BySlug(r.Context(), slug, typ)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			setRESTPaginationHeaders(w, 0, s.restPerPageMax)
			_ = writeRESTResponse(w, r, http.StatusOK, []any{})
			return
		}
		writeRESTError(w, http.StatusInternalServerError, "rest_list_failed", "Could not look up "+typ+" by slug.")
		return
	}
	item, links, embedded, err := s.mapRESTPost(r, typ, p)
	if err != nil {
		writeRESTError(w, http.StatusInternalServerError, "rest_list_failed", "Could not map "+typ+".")
		return
	}
	enveloped, err := withEnvelope(item, links, embedded)
	if err != nil {
		writeRESTError(w, http.StatusInternalServerError, "rest_list_failed", "Could not encode "+typ+".")
		return
	}
	setRESTPaginationHeaders(w, 1, s.restPerPageMax)
	_ = writeRESTResponse(w, r, http.StatusOK, []any{enveloped})
}

// handleRESTPostSingle serves GET .../posts/{id} and GET .../pages/{id}
// (Req 2.4). Published items are visible to everyone; draft/pending/private
// items (and a type mismatch, e.g. requesting a page's ID from /posts) are
// visible only to a caller with "edit_posts", returning
// 404 rest_post_invalid_id otherwise (matching real WordPress's own
// information-hiding behavior of never distinguishing "wrong type" from
// "does not exist" for an unprivileged caller).
func (s *Server) handleRESTPostSingle(typ string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil || id <= 0 {
			writeRESTError(w, http.StatusNotFound, "rest_post_invalid_id", "Invalid "+typ+" ID.")
			return
		}
		p, err := s.restPostByID.ByID(r.Context(), id)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				writeRESTError(w, http.StatusNotFound, "rest_post_invalid_id", "Invalid "+typ+" ID.")
				return
			}
			writeRESTError(w, http.StatusInternalServerError, "rest_post_invalid_id", "Could not look up "+typ+".")
			return
		}
		if p.Type != typ {
			writeRESTError(w, http.StatusNotFound, "rest_post_invalid_id", "Invalid "+typ+" ID.")
			return
		}
		if p.Status != "publish" && !s.canEditPosts(r) {
			writeRESTError(w, http.StatusNotFound, "rest_post_invalid_id", "Invalid "+typ+" ID.")
			return
		}
		item, links, embedded, err := s.mapRESTPost(r, typ, p)
		if err != nil {
			writeRESTError(w, http.StatusInternalServerError, "rest_list_failed", "Could not map "+typ+".")
			return
		}
		enveloped, err := withEnvelope(item, links, embedded)
		if err != nil {
			writeRESTError(w, http.StatusInternalServerError, "rest_list_failed", "Could not encode "+typ+".")
			return
		}
		_ = writeRESTResponse(w, r, http.StatusOK, enveloped)
	}
}

// canEditPosts reports whether the request's resolved principal (if any) has
// the "edit_posts" capability.
func (s *Server) canEditPosts(r *http.Request) bool {
	p, ok := PrincipalFrom(r.Context())
	return ok && p.Can("edit_posts")
}

// mapRESTPost maps a domain.Post through content.RESTMapper.Post/Page and
// builds its _links (+ _embedded, on ?_embed), applying restAbs to every
// relative link/URL the mapper produced (Req 6.6).
func (s *Server) mapRESTPost(r *http.Request, typ string, p domain.Post) (any, restLinks, map[string]any, error) {
	base := "/wp-json/wp/v2/" + typ + "s"
	b := newRESTLinks()
	b.add("self", restAbs(r, base+"/"+strconv.FormatInt(p.ID, 10)))
	b.add("collection", restAbs(r, base))

	var item any
	switch typ {
	case "page":
		mapped, err := s.restMapper.Page(r.Context(), p)
		if err != nil {
			return nil, nil, nil, err
		}
		mapped.Link = restAbs(r, mapped.Link)
		mapped.GUID.Rendered = restAbs(r, mapped.GUID.Rendered)
		item = mapped
	default:
		mapped, err := s.restMapper.Post(r.Context(), p)
		if err != nil {
			return nil, nil, nil, err
		}
		mapped.Link = restAbs(r, mapped.Link)
		mapped.GUID.Rendered = restAbs(r, mapped.GUID.Rendered)
		item = mapped
	}

	embed := embedRequested(r)
	author, embeddedAuthor, err := s.authorEmbed(r, p.Author, embed)
	if err != nil {
		return nil, nil, nil, err
	}
	b.addEmbeddable("author", restAbs(r, "/wp-json/wp/v2/users/"+strconv.FormatInt(p.Author, 10)))
	embedded := map[string]any{}
	if embed && author != nil {
		embedded["author"] = []any{embeddedAuthor}
	}

	b.add("replies", restAbs(r, "/wp-json/wp/v2/comments?post="+strconv.FormatInt(p.ID, 10)))
	b.add("wp:attachment", restAbs(r, "/wp-json/wp/v2/media?parent="+strconv.FormatInt(p.ID, 10)))

	if featured := restCommonFeaturedMedia(item); featured != 0 {
		mediaHref := restAbs(r, "/wp-json/wp/v2/media/"+strconv.FormatInt(featured, 10))
		b.addEmbeddable("wp:featuredmedia", mediaHref)
		if embed {
			media, err := s.restMedia.ByID(r.Context(), featured)
			if err == nil {
				mediaItem, err := s.restMapper.Media(r.Context(), media)
				if err == nil {
					mediaItem.Link = restAbs(r, mediaItem.Link)
					mediaItem.SourceURL = restAbs(r, mediaItem.SourceURL)
					embedded["wp:featuredmedia"] = []any{mediaItem}
				}
			}
		}
	}

	if len(embedded) == 0 {
		return item, b.links, nil, nil
	}
	return item, b.links, embedded, nil
}

// restCommonFeaturedMedia extracts the featured_media field from whichever
// concrete REST view-model type item holds (RESTPost or RESTPage), so
// mapRESTPost can stay type-generic.
func restCommonFeaturedMedia(item any) int64 {
	switch v := item.(type) {
	case content.RESTPost:
		return v.FeaturedMedia
	case content.RESTPage:
		return v.FeaturedMedia
	}
	return 0
}

// authorEmbed resolves a post/media author for the _links.author entry and,
// when embed is true, its embeddable view-context representation for
// _embedded.author.
func (s *Server) authorEmbed(r *http.Request, authorID int64, embed bool) (*domain.User, any, error) {
	if !embed || authorID == 0 {
		return nil, nil, nil
	}
	u, err := s.restUsers.ByID(r.Context(), authorID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	view, err := s.restMapper.User(r.Context(), u, content.RESTContextView)
	if err != nil {
		return nil, nil, err
	}
	return &u, view, nil
}
