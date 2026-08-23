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
	cleanRoot, err = filepath.EvalSymlinks(cleanRoot)
	if err != nil {
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
	// joined/cleaned path looks contained. resolveWithinRoot degrades to
	// resolving the deepest existing ancestor when the leaf doesn't exist,
	// so a legitimate 404 for a missing file is preserved.
	resolved, ok, err := resolveWithinRoot(cleanFull, cleanRoot)
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

// resolveWithinRoot resolves p through any symlinks (falling back to the
// nearest existing ancestor directory when p itself doesn't exist yet, then
// rejoining the not-yet-created suffix) and reports whether the fully
// resolved path is still contained within root (also symlink-resolved by the
// caller). A non-existent ancestor chain all the way up, or any stat error
// other than "not exist", is surfaced as an error.
func resolveWithinRoot(p, root string) (resolved string, ok bool, err error) {
	resolved, err = evalSymlinksLenient(p)
	if err != nil {
		return "", false, err
	}
	sep := string(os.PathSeparator)
	if resolved == root || strings.HasPrefix(resolved, root+sep) {
		return resolved, true, nil
	}
	return resolved, false, nil
}

// evalSymlinksLenient resolves p through any symlinks. If p does not exist,
// it resolves the deepest existing ancestor directory instead and rejoins the
// remaining (not-yet-created) suffix, so a path that legitimately doesn't
// exist yet (e.g. a new upload about to be written) still gets an
// escape-safe answer instead of an error.
func evalSymlinksLenient(p string) (string, error) {
	resolved, err := filepath.EvalSymlinks(p)
	if err == nil {
		return resolved, nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	dir := filepath.Dir(p)
	if dir == p {
		return "", err
	}
	resolvedDir, derr := evalSymlinksLenient(dir)
	if derr != nil {
		return "", derr
	}
	return filepath.Join(resolvedDir, filepath.Base(p)), nil
}
