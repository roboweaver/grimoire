// Package rebind translates neutral `?` SQL placeholders into a vendor's native
// placeholder syntax at exec time.
//
// The raw database/sql write paths (migrations, seeding, contract fixtures) are
// hand-written with `?` placeholders. MySQL and SQLite accept `?` natively, but
// the PostgreSQL driver (bun's pgdriver) only substitutes `$N`-style
// placeholders and passes a bare `?` through to the server verbatim, which
// errors. Rebind bridges that gap without pulling those queries through a query
// builder.
//
// # Rebind is only for database/sql
//
// Apply Rebind if and only if the query is handed to a *sql.DB or *sql.Tx.
// Queries executed through Bun — including Bun's own ExecContext, QueryContext
// and QueryRowContext, and therefore anything in internal/storage/wprepo — must
// keep their `?` placeholders, because Bun substitutes the arguments itself and
// then calls the driver with the finished SQL and no arguments at all.
//
// Rebinding first breaks those paths on PostgreSQL in a way that is easy to
// misread: Bun's query formatter short-circuits on
// `strings.IndexByte(query, '?') == -1` and returns the text unchanged, so every
// argument is silently discarded and the server answers
// `there is no parameter $1` (SQLSTATE 42P02). Nothing goes wrong on MySQL or
// SQLite, where Rebind is a no-op, so the mistake is invisible until Postgres
// runs.
package rebind

import (
	"strconv"
	"strings"
)

// Rebind rewrites sequential `?` placeholders for the given vendor.
//
//   - postgres: `?` becomes `$1`, `$2`, ... in order of appearance.
//   - mysql, sqlite (and any other vendor): the query is returned unchanged.
//
// Placeholders inside single-quoted string literals are left untouched, so a
// literal such as 'who?' is not mistaken for a bind parameter. Standard SQL
// escaped quotes (”) toggle literal state twice and are handled correctly.
func Rebind(vendor, query string) string {
	if vendor != "postgres" {
		return query
	}

	var b strings.Builder
	b.Grow(len(query) + 8)
	inLiteral := false
	n := 0
	for i := 0; i < len(query); i++ {
		c := query[i]
		switch {
		case c == '\'':
			inLiteral = !inLiteral
			b.WriteByte(c)
		case c == '?' && !inLiteral:
			n++
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(n))
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}
