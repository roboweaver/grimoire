package rebind

import "strings"

// Ident quotes a SQL identifier for the given vendor.
//
// This exists for the same reason Rebind does: the raw database/sql paths
// (seeding, preflight probes, contract fixtures) are hand-written SQL text, so
// they get no help from a query builder's dialect handling. The repository layer
// does not need this — it goes through bun, whose bun.Ident already quotes
// per-dialect.
//
// The WordPress schema has mixed-case column names (ID, comment_ID,
// comment_post_ID, comment_author_IP), and that is what makes quoting
// load-bearing rather than cosmetic:
//
//   - Postgres folds an UNQUOTED identifier to lower case and preserves a
//     QUOTED one. grimoire's Postgres migrations declare every one of these
//     columns quoted, so the stored name keeps WordPress's case; referencing
//     them unquoted resolves to a lower-case name that does not exist. That
//     mismatch is what broke seeding and preflight on Postgres entirely.
//
//     "Every one" is worth stating because it was not always true.
//     comment_post_ID and comment_author_IP were originally declared bare in
//     the 0003 migration while "ID" and "comment_ID" beside them were quoted,
//     which meant a caller had to know, per column, which style had been used —
//     and callers duly got it wrong in both directions. The migration was
//     normalised so quoting can be applied unconditionally to any WordPress
//     column name.
//
//   - MySQL quotes with BACKTICKS. A double-quoted identifier is a string
//     literal unless ANSI_QUOTES is enabled, so quoting the Postgres way on
//     MySQL silently turns a column reference into a constant — a wrong answer
//     rather than an error.
//
//   - SQLite accepts double quotes and is case-insensitive for column names.
//
// Only Postgres is quoted. MySQL and SQLite match column names
// case-insensitively, so a bare reference already resolves correctly there, and
// quoting them is not merely unnecessary but actively harmful:
//
//   - SQLite has a legacy misfeature where a DOUBLE-QUOTED string that matches
//     no identifier is silently accepted as a STRING LITERAL instead of
//     erroring. That defeats any probe which infers "column missing" from a
//     failed statement: SELECT "post_password" FROM t succeeds on a table with
//     no such column, returning the constant 'post_password'. migrate.Preflight
//     works exactly that way, so quoting for SQLite makes it report a broken
//     schema as healthy. This was caught by the contract suite rather than
//     reasoned about, which is the only reason it is documented here.
//   - MySQL would need backticks, since a double-quoted identifier there is a
//     string literal unless ANSI_QUOTES is enabled — the same class of silent
//     wrong answer.
//
// So the rule is: quote where case is load-bearing (Postgres), leave bare where
// it is not. Any unrecognised vendor is left bare for the same reason.
func Ident(vendor, name string) string {
	if vendor == "postgres" {
		return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
	}
	return name
}

// IdentList quotes each identifier for the vendor and joins them with ", ",
// ready to drop into a SELECT list. Returns "" for an empty list.
func IdentList(vendor string, names []string) string {
	if len(names) == 0 {
		return ""
	}
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = Ident(vendor, n)
	}
	return strings.Join(quoted, ", ")
}
