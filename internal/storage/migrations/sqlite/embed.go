// Package sqlite embeds the WordPress-compatible SQLite migrations.
package sqlite

import (
	"embed"
	"io/fs"
)

//go:embed *.up.sql
var files embed.FS

// FS returns the embedded migration file system for SQLite.
func FS() fs.FS { return files }
