// Package postgres embeds the WordPress-compatible PostgreSQL migrations.
package postgres

import (
	"embed"
	"io/fs"
)

//go:embed *.up.sql
var files embed.FS

// FS returns the embedded migration file system for PostgreSQL.
func FS() fs.FS { return files }
