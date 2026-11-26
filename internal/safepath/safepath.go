// Package safepath holds the symlink-aware containment check shared by the
// uploads file server (internal/web) and media file deletion
// (internal/content). It lives in its own package because internal/web already
// imports internal/content.
package safepath

import (
	"os"
	"path/filepath"
	"strings"
)

// ResolveWithinRoot resolves p through any symlinks (falling back to the
// nearest existing ancestor directory when p itself doesn't exist yet, then
// rejoining the not-yet-created suffix) and reports whether the fully
// resolved path is still contained within root (also symlink-resolved by the
// caller). A non-existent ancestor chain all the way up, or any stat error
// other than "not exist", is surfaced as an error.
func ResolveWithinRoot(p, root string) (resolved string, ok bool, err error) {
	resolved, err = EvalSymlinksLenient(p)
	if err != nil {
		return "", false, err
	}
	sep := string(os.PathSeparator)
	if resolved == root || strings.HasPrefix(resolved, root+sep) {
		return resolved, true, nil
	}
	return resolved, false, nil
}

// EvalSymlinksLenient resolves p through any symlinks. If p does not exist,
// it resolves the deepest existing ancestor directory instead and rejoins the
// remaining (not-yet-created) suffix, so a path that legitimately doesn't
// exist yet still gets an escape-safe answer instead of an error.
func EvalSymlinksLenient(p string) (string, error) {
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
	resolvedDir, derr := EvalSymlinksLenient(dir)
	if derr != nil {
		return "", derr
	}
	return filepath.Join(resolvedDir, filepath.Base(p)), nil
}
