package web

import (
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/roboweaver/grimoire/assets"
)

// registerStatic wires the icon asset routes onto r. Two things browsers ask
// for: the bare /favicon.ico, and the individual icon files referenced from
// the page <head>. Both are served from the embedded assets FS so the
// single-binary story holds.
func registerStatic(r interface {
	Get(pattern string, h http.HandlerFunc)
	Handle(pattern string, h http.Handler)
}) {
	iconsFS := assets.IconsFS()

	r.Get("/favicon.ico", func(w http.ResponseWriter, req *http.Request) {
		f, err := iconsFS.Open("favicon.ico")
		if err != nil {
			http.NotFound(w, req)
			return
		}
		defer f.Close()
		w.Header().Set("Content-Type", "image/x-icon")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		_, _ = io.Copy(w, f)
	})

	fileServer := http.StripPrefix("/assets/icons/", http.FileServer(http.FS(iconsFS)))
	r.Handle("/assets/icons/*", fileServer)
}

// registerThemeStatic mounts the active theme's static/ directory (e.g. its
// vendored Spectrum CSS) at /theme/static/*. Unlike registerStatic (which
// serves go:embed'd assets), themes are already loaded from disk at runtime,
// so this serves directly from themeDir with no embedding.
func registerThemeStatic(r chi.Router, themeStaticDir string) {
	fileServer := http.StripPrefix("/theme/static/", http.FileServer(http.Dir(themeStaticDir)))
	r.Get("/theme/static/*", func(w http.ResponseWriter, req *http.Request) {
		fileServer.ServeHTTP(w, req)
	})
}
