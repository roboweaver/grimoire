// Package overlay embeds the per-vendor "overlay" migration set: the
// grimoire-owned schema objects that WordPress itself never creates.
//
// The greenfield sets in ../{mysql,postgres,sqlite} build grimoire's own
// minimal WordPress-compatible schema from nothing, and to do so they use plain
// ALTER TABLE ... ADD COLUMN (MySQL and SQLite have no portable ADD COLUMN IF
// NOT EXISTS). That makes them unusable against a real WordPress database,
// which already has every one of those columns: the ALTERs fail with a
// duplicate-column error.
//
// This set is the answer to that. It is the strict subset of DDL that a live
// WordPress database still needs in order to run grimoire, and it holds to
// three rules:
//
//  1. Grimoire-owned objects only. Nothing here shares a name with anything
//     WordPress creates, so it cannot collide with a stock WP schema.
//  2. Guarded and re-runnable. Every statement is IF NOT EXISTS, so applying
//     the set twice is a no-op.
//  3. Purely additive. No ALTER TABLE, no data manipulation. Tables WordPress
//     owns are left exactly as they are found.
//
// Apply it with `grimoire-cli migrate -overlay`, which records progress in
// {prefix}grimoire_migrations -- a separate version stream from the greenfield
// set's {prefix}schema_migrations, so the two never mistake each other's
// versions for their own.
package overlay

import (
	"embed"
	"fmt"
	"io/fs"
)

//go:embed mysql/*.up.sql postgres/*.up.sql sqlite/*.up.sql
var files embed.FS

// FS returns the embedded overlay migration file system for vendor, rooted so
// that migration files sit at the top level (matching the greenfield per-vendor
// packages' FS layout, which migrate.Apply globs as "*.up.sql").
func FS(vendor string) (fs.FS, error) {
	switch vendor {
	case "mysql", "postgres", "sqlite":
		sub, err := fs.Sub(files, vendor)
		if err != nil {
			return nil, fmt.Errorf("overlay: sub FS for %q: %w", vendor, err)
		}
		return sub, nil
	default:
		return nil, fmt.Errorf("overlay: unsupported vendor %q", vendor)
	}
}
