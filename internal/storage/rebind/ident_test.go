package rebind_test

import (
	"testing"

	"github.com/roboweaver/grimoire/internal/storage/rebind"
)

func TestIdent(t *testing.T) {
	cases := []struct {
		name   string
		vendor string
		ident  string
		want   string
	}{
		// Postgres preserves the case of a quoted identifier and folds an
		// unquoted one to lower case. WordPress's mixed-case columns are created
		// quoted by grimoire's Postgres migrations, so a reference to them must
		// be quoted too or it resolves to a lower-case name that does not exist.
		{"postgres mixed case", "postgres", "ID", `"ID"`},
		{"postgres comment_post_ID", "postgres", "comment_post_ID", `"comment_post_ID"`},
		{"postgres lower case", "postgres", "post_author", `"post_author"`},

		// MySQL and SQLite match column names case-insensitively, so bare
		// already resolves. They are left bare deliberately, not by omission:
		// on SQLite a double-quoted string matching no identifier is accepted
		// as a STRING LITERAL rather than erroring, which would make
		// migrate.Preflight report a schema missing a column as healthy. On
		// MySQL a double-quoted identifier is likewise a string literal unless
		// ANSI_QUOTES is set.
		{"mysql mixed case stays bare", "mysql", "ID", "ID"},
		{"mysql lower case stays bare", "mysql", "post_author", "post_author"},
		{"sqlite mixed case stays bare", "sqlite", "ID", "ID"},

		// An unknown vendor is left bare for the same reason.
		{"unknown vendor", "cockroach", "ID", "ID"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := rebind.Ident(tc.vendor, tc.ident); got != tc.want {
				t.Errorf("Ident(%q, %q) = %s, want %s", tc.vendor, tc.ident, got, tc.want)
			}
		})
	}
}

// An identifier containing the vendor's own quote character could otherwise
// terminate the quoting early and inject SQL. Every caller passes a compile-time
// constant today, but the prefix is operator-supplied, so the escaping is
// asserted rather than assumed.
func TestIdentEscapesEmbeddedQuotes(t *testing.T) {
	if got, want := rebind.Ident("postgres", `we"ird`), `"we""ird"`; got != want {
		t.Errorf(`Ident(postgres, we"ird) = %s, want %s`, got, want)
	}
	// MySQL is not quoted, so nothing is escaped there -- asserted so the
	// asymmetry is deliberate and visible rather than looking like an oversight.
	if got, want := rebind.Ident("mysql", "we`ird"), "we`ird"; got != want {
		t.Errorf("Ident(mysql, we`ird) = %s, want %s", got, want)
	}
}

// Table identifiers carry an operator-supplied prefix, so the same quoting has
// to work for them.
func TestIdentWithPrefixedTableName(t *testing.T) {
	if got, want := rebind.Ident("postgres", "wp_posts"), `"wp_posts"`; got != want {
		t.Errorf("Ident = %s, want %s", got, want)
	}
	if got, want := rebind.Ident("mysql", "accuweaverposts"), "accuweaverposts"; got != want {
		t.Errorf("Ident = %s, want %s", got, want)
	}
}

// IdentList is what the preflight probe needs: a comma-separated, individually
// quoted column list.
func TestIdentList(t *testing.T) {
	got := rebind.IdentList("postgres", []string{"ID", "post_author", "comment_post_ID"})
	want := `"ID", "post_author", "comment_post_ID"`
	if got != want {
		t.Errorf("IdentList(postgres) = %s, want %s", got, want)
	}
	if got, want := rebind.IdentList("mysql", []string{"ID", "post_author"}), "ID, post_author"; got != want {
		t.Errorf("IdentList(mysql) = %s, want %s (bare)", got, want)
	}
	if got := rebind.IdentList("postgres", nil); got != "" {
		t.Errorf("IdentList(nil) = %q, want empty", got)
	}
}
