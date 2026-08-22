// Package domain defines grimoire's core entities (Post, Term, Taxonomy, Option,
// User) and the repository interfaces (ports) used to read and write them.
//
// Nothing in this package imports a database driver: storage adapters under
// internal/storage implement these interfaces, keeping vendor-specific SQL out
// of the business logic.
package domain
