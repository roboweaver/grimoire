package web

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/domain"
)

// defaultAdminMaxUploadBytes is a defensive fallback only: NewMediaService
// always sets a positive MaxUploadSize (10 MiB by default), so this is only
// reached if a caller constructs *content.MediaService some other way.
const defaultAdminMaxUploadBytes = 10 << 20

// mediaItemResponse is the camelCase wire shape web/admin/src/api/types.ts
// expects for a single media item (Req 3).
type mediaItemResponse struct {
	ID       int64  `json:"id"`
	Title    string `json:"title"`
	Filename string `json:"filename"`
	URL      string `json:"url"`
	MimeType string `json:"mimeType"`
	Date     string `json:"date"`
	ParentID int64  `json:"parentId"`
}

type mediaListResponse struct {
	Items      []mediaItemResponse `json:"items"`
	Page       int                 `json:"page"`
	PerPage    int                 `json:"perPage"`
	Total      int                 `json:"total"`
	TotalPages int                 `json:"totalPages"`
}

func mediaItemDTO(m domain.Media) mediaItemResponse {
	return mediaItemResponse{
		ID:       m.ID,
		Title:    m.Title,
		Filename: m.Filename,
		URL:      m.URL,
		MimeType: m.MimeType,
		Date:     m.Date.UTC().Format("2006-01-02T15:04:05Z07:00"),
		ParentID: m.ParentID,
	}
}

func (s *Server) adminMedia(w http.ResponseWriter, r *http.Request) error {
	switch r.Method {
	case http.MethodGet:
		return s.adminMediaList(w, r)
	case http.MethodPost:
		return s.adminMediaUpload(w, r)
	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return nil
	}
}

// adminMediaList returns a paginated, capability-gated media list (Req 3,
// Req 11). Query params: page, perPage, parentId — mirroring adminPosts.
func (s *Server) adminMediaList(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	page, perPage, offset := clampPage(atoiDefault(q.Get("page"), 1), atoiDefault(q.Get("perPage"), 0))
	filter := domain.MediaFilter{Limit: perPage, Offset: offset}
	if parentID := atoiDefault(q.Get("parentId"), 0); parentID > 0 {
		filter.ParentID = int64(parentID)
	}
	items, total, err := s.media.List(r.Context(), filter)
	if err != nil {
		return err
	}
	out := make([]mediaItemResponse, 0, len(items))
	for _, m := range items {
		out = append(out, mediaItemDTO(m))
	}
	totalPages := (total + perPage - 1) / perPage
	if totalPages < 1 {
		totalPages = 1
	}
	return writeJSON(w, http.StatusOK, mediaListResponse{
		Items:      out,
		Page:       page,
		PerPage:    perPage,
		Total:      total,
		TotalPages: totalPages,
	})
}

// adminMediaUpload handles POST /admin/api/media: a capped, content-sniffed,
// allowlist-enforced file upload (Req 5). The client-supplied Content-Type is
// never trusted for the allowlist decision — only http.DetectContentType's
// sniff of the actual bytes is.
func (s *Server) adminMediaUpload(w http.ResponseWriter, r *http.Request) error {
	if !s.requireSessionCSRFJSON(w, r) {
		return nil
	}
	maxBytes := s.media.Config().MaxUploadSize
	if maxBytes <= 0 {
		maxBytes = defaultAdminMaxUploadBytes
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	if err := r.ParseMultipartForm(maxBytes); err != nil {
		writeJSONError(w, http.StatusRequestEntityTooLarge, "too_large", "upload too large")
		return nil
	}
	f, h, err := r.FormFile("file")
	if err != nil {
		return badRequestError{msg: "file is required"}
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return err
	}
	sniffLen := len(data)
	if sniffLen > 512 {
		sniffLen = 512
	}
	sniffed := http.DetectContentType(data[:sniffLen])
	if !mimeAllowed(sniffed, s.media.Config().AllowedMIMEs) {
		writeJSONError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "file type not allowed")
		return nil
	}
	m, err := s.media.Store(r.Context(), bytes.NewReader(data), content.MediaUpload{Filename: h.Filename, MimeType: sniffed})
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated, mediaItemDTO(m))
}

// mimeAllowed reports whether mimeType (already sniffed, may carry a
// "; charset=..." parameter) is permitted by allowed. An empty allowlist
// means "no restriction configured" — always allow (matches deployments,
// including the e2e test harness, that never set AllowedMIMEs).
func mimeAllowed(mimeType string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	base := baseMime(mimeType)
	for _, a := range allowed {
		if strings.EqualFold(base, baseMime(a)) {
			return true
		}
	}
	return false
}

// baseMime strips any "; charset=..." (or other) parameter suffix from a
// MIME type string.
func baseMime(s string) string {
	if i := strings.IndexByte(s, ';'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// adminMediaAttach handles POST /admin/api/media/{id}/attach: JSON body
// {"parentId": <int>} sets the attachment's post_parent (Req 7).
func (s *Server) adminMediaAttach(w http.ResponseWriter, r *http.Request) error {
	if !s.requireSessionCSRFJSON(w, r) {
		return nil
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		return badRequestError{msg: "invalid media id"}
	}
	var body struct {
		ParentID int64 `json:"parentId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return badRequestError{msg: "invalid request body"}
	}
	if err := s.media.Attach(r.Context(), id, body.ParentID); err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, map[string]any{"id": id, "parentId": body.ParentID})
}
