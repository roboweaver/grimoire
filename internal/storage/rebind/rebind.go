// Package rebind translates neutral `?` SQL placeholders into a vendor's native
// placeholder syntax at exec time.
//
// The raw database/sql write paths (migrations, seeding, contract fixtures) are
// hand-written with `?` placeholders. MySQL and SQLite accept `?` natively, but
// the PostgreSQL driver (bun's pgdriver) only substitutes `$N`-style
// placeholders and passes a bare `?` through to the server verbatim, which
// errors. Rebind bridges that gap without pulling those queries through a query
// builder.
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
