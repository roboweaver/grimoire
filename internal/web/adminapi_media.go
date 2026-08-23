package web

import (
	"bytes"
	"io"
	"net/http"

	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/domain"
)

const adminMaxUploadBytes = 512

func (s *Server) adminMedia(w http.ResponseWriter, r *http.Request) error {
	switch r.Method {
	case http.MethodGet:
		items, _, err := s.media.List(r.Context(), domain.MediaFilter{})
		if err != nil {
			return err
		}
		return writeJSON(w, http.StatusOK, map[string]any{"items": items})
	case http.MethodPost:
		if !s.requireSessionCSRF(w, r) {
			return nil
		}
		r.Body = http.MaxBytesReader(w, r.Body, adminMaxUploadBytes)
		if err := r.ParseMultipartForm(adminMaxUploadBytes); err != nil {
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
		m, err := s.media.Store(r.Context(), bytes.NewReader(data), content.MediaUpload{Filename: h.Filename, MimeType: h.Header.Get("Content-Type")})
		if err != nil {
			return err
		}
		return writeJSON(w, http.StatusCreated, m)
	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return nil
	}
}
