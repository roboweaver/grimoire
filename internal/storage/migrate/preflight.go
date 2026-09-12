package migrate

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// RequiredTable names a WordPress table grimoire reads, together with the
// columns it selects from it.
type RequiredTable struct {
	// Name is the table name without the configured prefix.
	Name string
	// Columns are the unprefixed column names grimoire's queries reference.
	Columns []string
}

// RequiredSchema returns the WordPress tables and columns grimoire needs in
// order to serve a database it did not create.
//
// This mirrors exactly what the greenfield migration set (0001-0004) builds,
// minus the grimoire-owned objects the overlay set adds. It is written out
// explicitly rather than parsed back out of the migration SQL, so the migration
// contract suite pins the two together: it applies the greenfield set to a fresh
// database and then requires Preflight to pass against it. A new column added to
// a greenfield migration without a matching entry here fails that test.
//
// A stock WordPress installation satisfies all of this, because every name below
// is core WordPress schema. The list is a compatibility floor, not a request for
// WordPress to change anything.
func RequiredSchema() []RequiredTable {
	return []RequiredTable{
		{Name: "posts", Columns: []string{
			// 0001 core content columns.
			"ID", "post_author", "post_date", "post_content", "post_title",
			"post_excerpt", "post_status", "post_name", "post_type",
			// 0003 comment/media/menu columns.
			"comment_status", "post_parent", "post_mime_type", "menu_order",
			// 0004 REST-parity columns.
			"post_date_gmt", "post_modified", "post_modified_gmt",
			"ping_status", "post_password", "guid",
		}},
		{Name: "postmeta", Columns: []string{"meta_id", "post_id", "meta_key", "meta_value"}},
		{Name: "options", Columns: []string{"option_id", "option_name", "option_value", "autoload"}},
		{Name: "terms", Columns: []string{"term_id", "name", "slug"}},
		{Name: "term_taxonomy", Columns: []string{
			"term_taxonomy_id", "term_id", "taxonomy", "description", "parent", "count",
		}},
		{Name: "term_relationships", Columns: []string{"object_id", "term_taxonomy_id", "term_order"}},
		{Name: "users", Columns: []string{
			// 0001 identity columns.
			"ID", "user_login", "user_nicename", "display_name",
			// 0002 auth columns.
			"user_pass", "user_email", "user_url", "user_registered",
			"user_activation_key", "user_status",
		}},
		{Name: "usermeta", Columns: []string{"umeta_id", "user_id", "meta_key", "meta_value"}},
		{Name: "comments", Columns: []string{
			"comment_ID", "comment_post_ID", "comment_author", "comment_author_email",
			"comment_author_url", "comment_author_IP", "comment_date", "comment_date_gmt",
			"comment_content", "comment_approved", "comment_agent", "comment_parent", "user_id",
		}},
		{Name: "commentmeta", Columns: []string{"meta_id", "comment_id", "meta_key", "meta_value"}},
	}
}

// PreflightReport describes how a database measured up against
// RequiredSchema.
type PreflightReport struct {
	// Prefix is the table prefix that was probed.
	Prefix string
	// MissingTables lists prefixed table names that could not be read at all.
	MissingTables []string
	// MissingColumns maps a prefixed table name to the columns it lacks. Only
	// tables that do exist appear here.
	MissingColumns map[string][]string
}

// OK reports whether the database satisfies RequiredSchema.
func (r *PreflightReport) OK() bool {
	return len(r.MissingTables) == 0 && len(r.MissingColumns) == 0
}

// Err returns a single error summarizing everything missing, or nil when the
// report is clean.
func (r *PreflightReport) Err() error {
	if r.OK() {
		return nil
	}
	var b strings.Builder
	b.WriteString("migrate: database is not WordPress-schema compatible with table prefix ")
	b.WriteString(fmt.Sprintf("%q", r.Prefix))
	if len(r.MissingTables) > 0 {
		b.WriteString("\n  missing tables: ")
		b.WriteString(strings.Join(r.MissingTables, ", "))
	}
	// Report tables in RequiredSchema order so the message is stable.
	for _, rt := range RequiredSchema() {
		name := r.Prefix + rt.Name
		if cols, ok := r.MissingColumns[name]; ok {
			b.WriteString(fmt.Sprintf("\n  %s is missing columns: %s", name, strings.Join(cols, ", ")))
		}
	}
	b.WriteString("\n\nIf the prefix is wrong, set database.table_prefix (or " +
		"GRIMOIRE_DATABASE_TABLE_PREFIX) to match the target site. " +
		"If this is an empty database, provision it with `grimoire-cli migrate` " +
		"instead of `migrate -overlay`.")
	return fmt.Errorf("%s", b.String())
}

// Preflight checks, without writing anything, that db provides every table and
// column in RequiredSchema under the given prefix. It returns a report even when
// the database is incomplete; callers decide whether to treat that as fatal.
//
// Detection is deliberately vendor-agnostic: rather than querying
// information_schema (absent on SQLite) or pragma (absent elsewhere), it asks
// the server to plan a zero-row SELECT of the columns in question. A missing
// table or column makes the statement fail to prepare, which is exactly the
// signal we want, and "WHERE 1=0" guarantees no rows are read even on a table
// with a hundred thousand of them.
//
// Identifiers are interpolated bare rather than quoted, which is correct on all
// three vendors despite the mixed-case WordPress column names (ID,
// comment_post_ID): MySQL and SQLite match column names case-insensitively, and
// Postgres folds the reference and the stored name alike to lower case because
// grimoire's Postgres migrations declare those columns unquoted too. Quoting
// would actively break MySQL, where double quotes denote a string literal unless
// ANSI_QUOTES is enabled. Every table and column name here is a constant from
// RequiredSchema; prefix is operator-supplied deployment configuration,
// interpolated the same way the migration files substitute {{prefix}}.
func Preflight(ctx context.Context, db *sql.DB, prefix string) (*PreflightReport, error) {
	report := &PreflightReport{Prefix: prefix, MissingColumns: map[string][]string{}}
	for _, rt := range RequiredSchema() {
		table := prefix + rt.Name
		if !probe(ctx, db, "SELECT 1 FROM "+table+" WHERE 1=0") {
			report.MissingTables = append(report.MissingTables, table)
			continue
		}
		// The table is readable, so narrow down which columns are absent. Probe
		// them all at once first, since that is one round trip and the common
		// case is that nothing is missing.
		if probe(ctx, db, "SELECT "+strings.Join(rt.Columns, ", ")+" FROM "+table+" WHERE 1=0") {
			continue
		}
		var missing []string
		for _, col := range rt.Columns {
			if !probe(ctx, db, "SELECT "+col+" FROM "+table+" WHERE 1=0") {
				missing = append(missing, col)
			}
		}
		if len(missing) == 0 {
			// The combined probe failed but no single column did. That is not a
			// schema gap we can attribute, so surface it rather than passing
			// silently.
			return nil, fmt.Errorf("migrate: preflight could not verify %s: "+
				"selecting all required columns together failed but each column "+
				"succeeded individually", table)
		}
		report.MissingColumns[table] = missing
	}
	if report.MissingColumns != nil && len(report.MissingColumns) == 0 {
		report.MissingColumns = nil
	}
	return report, nil
}

// probe reports whether query executes without error. Any error is read as
// "this table or column is not available", which is the only way the
// zero-row SELECTs above can fail short of the connection dropping; a genuine
// connectivity failure surfaces on the next statement regardless.
func probe(ctx context.Context, db *sql.DB, query string) bool {
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return false
	}
	defer rows.Close()
	// WHERE 1=0 returns no rows; Err surfaces a deferred plan/prepare failure
	// that QueryContext itself did not report.
	return rows.Err() == nil
}
