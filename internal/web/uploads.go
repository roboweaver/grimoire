package web

import (
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/roboweaver/grimoire/internal/safepath"
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
	cleanRoot, err = filepath.EvalSymlinks(cleanRoot)
	if err != nil {
		if os.IsNotExist(err) {
			http.NotFound(w, r)
			return nil
		}
		return err
	}
	cleanFull, err := filepath.Abs(full)
	if err != nil {
		http.NotFound(w, r)
		return nil
	}
	// Resolve symlinks on the requested path itself, not just the configured
	// root: a symlink planted inside the uploads directory (e.g. pointing at
	// /etc) must not let a request escape the root even though the naive
	// joined/cleaned path looks contained. safepath.ResolveWithinRoot degrades to
	// resolving the deepest existing ancestor when the leaf doesn't exist,
	// so a legitimate 404 for a missing file is preserved.
	resolved, ok, err := safepath.ResolveWithinRoot(cleanFull, cleanRoot)
	if err != nil {
		return err
	}
	if !ok {
		http.NotFound(w, r)
		return nil
	}
	w.Header().Set("Cache-Control", "public, max-age=31536000")
	http.ServeFile(w, r, resolved)
	return nil
}
