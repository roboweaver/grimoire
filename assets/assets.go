// Package assets embeds static binary assets (application icons) so the
// grimoire server ships them inside the single binary, mirroring how the
// storage migrations and their per-vendor packages embed their SQL.
package assets

import (
	"embed"
	"io/fs"
)

//go:embed icons
var files embed.FS

// IconsFS returns the embedded icon files, rooted at the icons directory so
// that a request for "favicon-32x32.png" maps to icons/favicon-32x32.png.
func IconsFS() fs.FS {
	sub, err := fs.Sub(files, "icons")
	if err != nil {
		// icons is embedded above, so this cannot happen at runtime.
		panic(err)
	}
	return sub
}
