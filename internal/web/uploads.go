package web

import (
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

func (s *Server) uploads(w http.ResponseWriter, r *http.Request) error {
	rel := strings.TrimPrefix(r.URL.Path, "/wp-content/uploads/")
	if rel == r.URL.Path || rel == "" || strings.Contains(rel, "..") || path.IsAbs(rel) {
		http.Error(w, "Not Found", http.StatusNotFound)
		return nil
	}
	full := filepath.Join(s.media.Config().UploadsDir, filepath.FromSlash(rel))
	cleanRoot, err := filepath.Abs(s.media.Config().UploadsDir)
	if err != nil {
		return err
	}
	cleanFull, err := filepath.Abs(full)
	if err != nil {
		http.NotFound(w, r)
		return nil
	}
	if !strings.HasPrefix(cleanFull, cleanRoot+string(os.PathSeparator)) && cleanFull != cleanRoot {
		http.NotFound(w, r)
		return nil
	}
	w.Header().Set("Cache-Control", "public, max-age=31536000")
	http.ServeFile(w, r, cleanFull)
	return nil
}
