// Package mysql embeds the WordPress-compatible MySQL/MariaDB migrations.
package mysql

import (
	"embed"
	"io/fs"
)

//go:embed *.up.sql
var files embed.FS

// FS returns the embedded migration file system for MySQL/MariaDB.
func FS() fs.FS { return files }
