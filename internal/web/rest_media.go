package web

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/sanitize"
)

// registerRESTMedia registers GET .../media[/{id}] (Req 4.1, 4.4) and 501
// stubs for every write method (Req 4.5), on both the collection and
// single-item route so no plausible write verb/route combination falls
// through to chi's MethodNotAllowed 405 (Req 7.5).
func (s *Server) registerRESTMedia(r chi.Router) {
	r.Method(http.MethodGet, "/media", s.handleRESTMediaCollection())
	r.Method(http.MethodGet, "/media/{id}", s.handleRESTMediaSingle())
	// POST /media is the live create handler once a MediaWriteService is wired
	// (task 7.1); harnesses that omit it fall back to the 501 stub (Req 1.1,
	// 4.5). All other write verbs/routes keep their stubs — update/delete land
	// in later tasks.
	if s.mediaWrite != nil {
		r.Method(http.MethodPost, "/media", s.handleRESTMediaCreate())
	} else {
		r.Method(http.MethodPost, "/media", restNotImplemented("rest_cannot_create", "Uploading media"))
	}
	for _, m := range []string{http.MethodPut, http.MethodPatch, http.MethodDelete} {
		r.Method(m, "/media", restNotImplemented("rest_cannot_edit", "Updating or deleting media"))
	}
	// PUT/PATCH/POST /media/{id} are the live update handler once a
	// MediaWriteService is wired (task 7.1); POST on the item route is the
	// WordPress update verb (POST /media creates, POST /media/{id} updates).
	// DELETE /media/{id} ALWAYS stays a 501 stub (delete lands in a later
	// task, Req 9.2); when mediaWrite is nil, all four write verbs stub.
	if s.mediaWrite != nil {
		for _, m := range []string{http.MethodPut, http.MethodPatch, http.MethodPost} {
			r.Method(m, "/media/{id}", s.handleRESTMediaUpdate())
		}
	} else {
		for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodPatch} {
			r.Method(m, "/media/{id}", restNotImplemented("rest_cannot_edit", "Updating or deleting media"))
		}
	}
	r.Method(http.MethodDelete, "/media/{id}", restNotImplemented("rest_cannot_edit", "Updating or deleting media"))
}

// handleRESTMediaCollection serves GET .../media: a paginated collection of
// attachments, optionally filtered to a single post's attachments via
// ?parent= (Req 4.1, 4.4).
func (s *Server) handleRESTMediaCollection() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		var parent int64
		if raw := q.Get("parent"); raw != "" {
			parsed, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || parsed < 0 {
				writeRESTError(w, http.StatusBadRequest, "rest_invalid_param", "Invalid parent parameter.")
				return
			}
			parent = parsed
		}
		paging := parseRESTPaging(r, s.restPerPageMax)
		f := domain.MediaFilter{ParentID: parent, Limit: paging.PerPage, Offset: paging.Offset}
		items, err := s.restMedia.List(r.Context(), f)
		if err != nil {
			writeRESTError(w, http.StatusInternalServerError, "rest_list_failed", "Could not list media.")
			return
		}
		total, err := s.restMedia.Count(r.Context(), domain.MediaFilter{ParentID: parent})
		if err != nil {
			writeRESTError(w, http.StatusInternalServerError, "rest_list_failed", "Could not count media.")
			return
		}
		out := make([]any, 0, len(items))
		for _, m := range items {
			enveloped, err := s.mapRESTMedia(r, m)
			if err != nil {
				writeRESTError(w, http.StatusInternalServerError, "rest_list_failed", "Could not map media #"+strconv.FormatInt(m.ID, 10)+".")
				return
			}
			out = append(out, enveloped)
		}
		setRESTPaginationHeaders(w, total, paging.PerPage)
		_ = writeRESTResponse(w, r, http.StatusOK, out)
	}
}

// handleRESTMediaSingle serves GET .../media/{id} (Req 4.1).
func (s *Server) handleRESTMediaSingle() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil || id <= 0 {
			writeRESTError(w, http.StatusNotFound, "rest_post_invalid_id", "Invalid media ID.")
			return
		}
		m, err := s.restMedia.ByID(r.Context(), id)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				writeRESTError(w, http.StatusNotFound, "rest_post_invalid_id", "Invalid media ID.")
				return
			}
			writeRESTError(w, http.StatusInternalServerError, "rest_list_failed", "Could not look up media.")
			return
		}
		enveloped, err := s.mapRESTMedia(r, m)
		if err != nil {
			writeRESTError(w, http.StatusInternalServerError, "rest_list_failed", "Could not map media.")
			return
		}
		_ = writeRESTResponse(w, r, http.StatusOK, enveloped)
	}
}

// handleRESTMediaCreate handles POST .../media (Req 1.1-1.6): a raw-binary
// attachment upload. Auth follows the same pattern as post/comment writes (Req
// 7.1/7.2): an Application Password skips the CSRF check; a session-cookie
// request enforces the M4 X-CSRF-Token contract. The filename comes from the
// Content-Disposition header (Req 1.5), the body is capped at the configured
// MaxUploadSize (Req 7.1/7.2), and the MIME decision uses the SERVER-sniffed
// type via http.DetectContentType rather than the client Content-Type (Req
// 6.4). The capability/MIME/unfiltered_upload decisions live in
// MediaWriteService.Create; this handler only shapes the transport.
func (s *Server) handleRESTMediaCreate() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isAppPasswordAuth(r.Context()) {
			if _, hasSession := sessionFrom(r.Context()); hasSession {
				if !s.requireSessionCSRFREST(w, r) {
					return
				}
			}
		}
		principal, _ := PrincipalFrom(r.Context())

		filename, ok := parseContentDispositionFilename(r.Header.Get("Content-Disposition"))
		if !ok {
			writeRESTError(w, http.StatusBadRequest, "rest_upload_no_content_disposition", "No Content-Disposition supplied with the request.")
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, s.media.Config().MaxUploadSize)
		data, err := io.ReadAll(r.Body)
		if err != nil {
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				writeRESTError(w, http.StatusRequestEntityTooLarge, "rest_upload_file_too_big", "The uploaded file exceeds the maximum allowed size.")
				return
			}
			writeRESTError(w, http.StatusBadRequest, "rest_invalid_param", "Could not read the request body.")
			return
		}
		if len(data) == 0 {
			writeRESTError(w, http.StatusBadRequest, "rest_upload_no_data", "No data supplied.")
			return
		}

		// The MIME allow-list decision uses the server-sniffed type, never the
		// client-supplied Content-Type (Req 6.4).
		sniffed := http.DetectContentType(data[:min(len(data), 512)])

		created, err := s.mediaWrite.Create(r.Context(), principal, content.MediaCreateInput{
			Data:     data,
			Filename: filename,
			MimeType: sniffed,
		})
		if err != nil {
			switch {
			case errors.Is(err, content.ErrForbidden):
				writeRESTError(w, http.StatusForbidden, "rest_cannot_create", "Sorry, you are not allowed to create media.")
			case errors.Is(err, content.ErrUnsupportedMediaType):
				writeRESTError(w, http.StatusUnsupportedMediaType, "rest_upload_unsupported_media_type", "Sorry, you are not allowed to upload this file type.")
			case errors.Is(err, content.ErrUploadTooLarge):
				writeRESTError(w, http.StatusRequestEntityTooLarge, "rest_upload_file_too_big", "The uploaded file exceeds the maximum allowed size.")
			default:
				writeRESTError(w, http.StatusInternalServerError, "rest_upload_failed", "Could not upload the file.")
			}
			return
		}

		enveloped, err := s.mapRESTMedia(r, created)
		if err != nil {
			writeRESTError(w, http.StatusInternalServerError, "rest_upload_failed", "Could not upload the file.")
			return
		}
		_ = writeRESTResponse(w, r, http.StatusCreated, enveloped)
	}
}

// handleRESTMediaUpdate serves PUT/PATCH/POST .../media/{id}: a partial update
// of the editable metadata set. Auth matches handleRESTMediaCreate (an
// Application Password skips CSRF; a session enforces the M4 X-CSRF-Token
// contract). {id} must resolve to a positive integer attachment id (Req 2.5);
// a bad "post" parent is 400 rest_invalid_param (Req 2.6). The service loads
// the stored row, authorizes against its post_author, sanitizes each supplied
// field, and persists; the 200 response is the same envelope GET returns,
// re-read from storage so it reflects the sanitized stored values (Req 2.1,
// 10.3).
func (s *Server) handleRESTMediaUpdate() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isAppPasswordAuth(r.Context()) {
			if _, hasSession := sessionFrom(r.Context()); hasSession {
				if !s.requireSessionCSRFREST(w, r) {
					return
				}
			}
		}
		principal, _ := PrincipalFrom(r.Context())

		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil || id <= 0 {
			writeRESTError(w, http.StatusNotFound, "rest_post_invalid_id", "Invalid media ID.")
			return
		}

		var body restMediaUpdateBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeRESTError(w, http.StatusBadRequest, "rest_invalid_param", "Invalid request body.")
			return
		}
		upd, code, err := parseRESTMediaUpdate(body) // validates "post" (Req 2.6)
		if err != nil {
			writeRESTError(w, http.StatusBadRequest, code, err.Error())
			return
		}

		updated, err := s.mediaWrite.Update(r.Context(), principal, id, upd)
		if err != nil {
			switch {
			case errors.Is(err, domain.ErrNotFound):
				writeRESTError(w, http.StatusNotFound, "rest_post_invalid_id", "Invalid media ID.") // Req 2.5
			case errors.Is(err, content.ErrForbidden):
				writeRESTError(w, http.StatusForbidden, "rest_cannot_edit", "Sorry, you are not allowed to edit this media.") // Req 5.3-5.6
			case errors.Is(err, sanitize.ErrTitleNotConverging):
				// The only policy error the title/alt path can surface (Req
				// 4.7). The service returns it unchanged (the PostWriteService
				// posture); map it to the same 400 a bad post title gets,
				// never a 500. caption/description (tier B/C) cannot error, so
				// there is no generic policy-error branch.
				writeRESTError(w, http.StatusBadRequest, "rest_invalid_param", "The supplied title or alt text could not be accepted.")
			default:
				writeRESTError(w, http.StatusInternalServerError, "rest_update_failed", "Could not update media.")
			}
			return
		}

		enveloped, err := s.mapRESTMedia(r, updated)
		if err != nil {
			writeRESTError(w, http.StatusInternalServerError, "rest_update_failed", "Could not update media.")
			return
		}
		_ = writeRESTResponse(w, r, http.StatusOK, enveloped) // 200 (Req 2.1, 10.3)
	}
}

// restMediaUpdateBody is the WordPress-vocabulary request body (Req 2.1).
// Pointers distinguish an omitted field from an explicitly-empty one, giving
// partial-update semantics (Req 2.3): a nil field leaves the stored value
// unchanged.
type restMediaUpdateBody struct {
	Title       *string `json:"title"`
	Caption     *string `json:"caption"`
	AltText     *string `json:"alt_text"`
	Description *string `json:"description"`
	Post        *int64  `json:"post"` // parent (Req 2.2)
}

// parseRESTMediaUpdate validates the decoded body and maps it to the service
// input. A negative "post" is rejected with rest_invalid_param (Req 2.6); a
// non-numeric "post" is caught earlier by JSON decoding into *int64, which the
// handler also maps to rest_invalid_param. "post" maps to Parent.
func parseRESTMediaUpdate(body restMediaUpdateBody) (content.MediaMetaUpdate, string, error) {
	if body.Post != nil && *body.Post < 0 {
		return content.MediaMetaUpdate{}, "rest_invalid_param", errors.New("post must be a non-negative integer")
	}
	return content.MediaMetaUpdate{
		Title:       body.Title,
		Caption:     body.Caption,
		AltText:     body.AltText,
		Description: body.Description,
		Parent:      body.Post,
	}, "", nil
}

// parseContentDispositionFilename extracts the filename parameter from a
// Content-Disposition header (Req 1.5). It returns ("", false) when the header
// is absent, unparseable, or carries no non-empty filename parameter — every
// one of which the caller maps to the same 400 rest_upload_no_content_disposition.
func parseContentDispositionFilename(header string) (string, bool) {
	if header == "" {
		return "", false
	}
	_, params, err := mime.ParseMediaType(header)
	if err != nil {
		return "", false
	}
	filename := params["filename"]
	if filename == "" {
		return "", false
	}
	return filename, true
}

// mapRESTMedia maps a domain.Media through content.RESTMapper.Media, applies
// restAbs to its link/source_url (both relative content-layer paths, Req
// 6.6), and builds its _links (+ _embedded, on ?_embed): self, collection,
// author, and "up" (parent post) when the attachment has one (Req 6.2).
func (s *Server) mapRESTMedia(r *http.Request, m domain.Media) (map[string]any, error) {
	mapped, err := s.restMapper.Media(r.Context(), m)
	if err != nil {
		return nil, err
	}
	mapped.Link = restAbs(r, mapped.Link)
	mapped.SourceURL = restAbs(r, mapped.SourceURL)

	b := newRESTLinks()
	base := "/wp-json/wp/v2/media"
	b.add("self", restAbs(r, base+"/"+strconv.FormatInt(m.ID, 10)))
	b.add("collection", restAbs(r, base))

	embed := embedRequested(r)
	embedded := map[string]any{}
	if m.AuthorID != 0 {
		b.addEmbeddable("author", restAbs(r, "/wp-json/wp/v2/users/"+strconv.FormatInt(m.AuthorID, 10)))
		if embed {
			if u, err := s.restUsers.ByID(r.Context(), m.AuthorID); err == nil {
				if view, err := s.restMapper.User(r.Context(), u, content.RESTContextView); err == nil {
					embedded["author"] = []any{view}
				}
			}
		}
	}
	if m.ParentID != 0 {
		b.addEmbeddable("up", restAbs(r, "/wp-json/wp/v2/posts/"+strconv.FormatInt(m.ParentID, 10)))
		if embed {
			if p, err := s.restPostByID.ByID(r.Context(), m.ParentID); err == nil {
				var mappedParent any
				var perr error
				if p.Type == "page" {
					mappedParent, perr = s.restMapper.Page(r.Context(), p)
				} else {
					mappedParent, perr = s.restMapper.Post(r.Context(), p)
				}
				if perr == nil {
					embedded["up"] = []any{mappedParent}
				}
			}
		}
	}

	if len(embedded) == 0 {
		return withEnvelope(mapped, b.links, nil)
	}
	return withEnvelope(mapped, b.links, embedded)
}
