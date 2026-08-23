// Package admin embeds the React Spectrum read-only admin SPA's production
// build and serves it under a URL prefix, mirroring the embedded-assets pattern
// in assets/assets.go. Node.js is a build-time-only dependency: the committed
// dist directory means the runtime binary stays pure Go.
package admin

import (
	"embed"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var files embed.FS

// distFS returns the embedded build rooted at dist so a request for
// "index.html" maps to dist/index.html.
func distFS() fs.FS {
	sub, err := fs.Sub(files, "dist")
	if err != nil {
		// dist is embedded above, so this cannot happen at runtime.
		panic(err)
	}
	return sub
}

// Handler returns an http.Handler that serves the embedded SPA under prefix,
// with SPA-fallback routing: existing files are served directly (hashed assets
// under assets/ get a long-lived immutable cache), a missing asset under
// assets/ is a 404, and any other path serves index.html so client-side routes
// resolve.
func Handler(prefix string) http.Handler {
	return handler(distFS(), prefix)
}

// handler is the prefix + fs.FS-parameterized core, exercised in tests over a
// fake filesystem so the serving rules are verified without the real build.
func handler(fsys fs.FS, prefix string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Reduce the request to a clean, slash-free FS path relative to prefix.
		rel := strings.TrimPrefix(r.URL.Path, prefix)
		rel = strings.TrimPrefix(rel, "/")
		rel = path.Clean(rel)
		if rel == "." || rel == "/" {
			rel = "index.html"
		}

		if f, ok := openFile(fsys, rel); ok {
			defer f.Close()
			serveFile(w, r, rel, f)
			return
		}

		// A missing hashed asset must 404 rather than mask a broken reference.
		if strings.HasPrefix(rel, "assets/") {
			http.NotFound(w, r)
			return
		}

		// Client-side route: fall back to the SPA entry document.
		serveIndex(w, r, fsys)
	})
}

// openFile opens name if it exists and is a regular file (not a directory).
func openFile(fsys fs.FS, name string) (fs.File, bool) {
	f, err := fsys.Open(name)
	if err != nil {
		return nil, false
	}
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		_ = f.Close()
		return nil, false
	}
	return f, true
}

// serveFile writes f with a Content-Type inferred from its extension and a
// cache policy: hashed assets are immutable, the entry document is never cached.
func serveFile(w http.ResponseWriter, r *http.Request, name string, f fs.File) {
	if strings.HasPrefix(name, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	if ct := contentType(name); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	_, _ = io.Copy(w, f)
}

// serveIndex writes dist/index.html as the SPA fallback with a no-cache policy.
func serveIndex(w http.ResponseWriter, r *http.Request, fsys fs.FS) {
	f, ok := openFile(fsys, "index.html")
	if !ok {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.Copy(w, f)
}

// contentType maps common SPA asset extensions to MIME types. Kept explicit so
// serving does not depend on the platform mime database.
func contentType(name string) string {
	switch {
	case strings.HasSuffix(name, ".html"):
		return "text/html; charset=utf-8"
	case strings.HasSuffix(name, ".js"), strings.HasSuffix(name, ".mjs"):
		return "text/javascript; charset=utf-8"
	case strings.HasSuffix(name, ".css"):
		return "text/css; charset=utf-8"
	case strings.HasSuffix(name, ".json"):
		return "application/json; charset=utf-8"
	case strings.HasSuffix(name, ".svg"):
		return "image/svg+xml"
	case strings.HasSuffix(name, ".png"):
		return "image/png"
	case strings.HasSuffix(name, ".woff2"):
		return "font/woff2"
	case strings.HasSuffix(name, ".woff"):
		return "font/woff"
	case strings.HasSuffix(name, ".ico"):
		return "image/x-icon"
	default:
		return ""
	}
}
