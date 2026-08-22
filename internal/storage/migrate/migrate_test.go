package migrate

import (
	"context"
	"database/sql"
	"testing"
	"testing/fstest"

	_ "modernc.org/sqlite"
)

func openSQLite(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func tableExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var got string
	err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&got)
	if err == sql.ErrNoRows {
		return false
	}
	if err != nil {
		t.Fatalf("tableExists(%s): %v", name, err)
	}
	return got == name
}

func TestApplyCreatesTablesAndIsIdempotent(t *testing.T) {
	db := openSQLite(t)
	migFS := fstest.MapFS{
		"0001_init.up.sql": {Data: []byte("CREATE TABLE IF NOT EXISTS {{prefix}}t (id INTEGER PRIMARY KEY);")},
	}
	ctx := context.Background()

	v, err := Apply(ctx, db, migFS, "sqlite", "wp_")
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if v != 1 {
		t.Fatalf("version = %d, want 1", v)
	}
	if !tableExists(t, db, "wp_t") {
		t.Fatal("wp_t not created")
	}
	if !tableExists(t, db, "wp_schema_migrations") {
		t.Fatal("wp_schema_migrations not created")
	}

	// Second run applies nothing new.
	v2, err := Apply(ctx, db, migFS, "sqlite", "wp_")
	if err != nil {
		t.Fatalf("Apply (2nd): %v", err)
	}
	if v2 != 1 {
		t.Fatalf("version (2nd) = %d, want 1", v2)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM wp_schema_migrations`).Scan(&count); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if count != 1 {
		t.Fatalf("migration rows = %d, want 1 (idempotent)", count)
	}
}

func TestApplyMultipleVersionsAndPrefix(t *testing.T) {
	db := openSQLite(t)
	migFS := fstest.MapFS{
		"0001_a.up.sql": {Data: []byte("CREATE TABLE IF NOT EXISTS {{prefix}}a (id INTEGER PRIMARY KEY);")},
		"0002_b.up.sql": {Data: []byte("CREATE TABLE IF NOT EXISTS {{prefix}}b (id INTEGER PRIMARY KEY);\nCREATE TABLE IF NOT EXISTS {{prefix}}c (id INTEGER PRIMARY KEY);")},
	}
	v, err := Apply(context.Background(), db, migFS, "sqlite", "gr_")
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if v != 2 {
		t.Fatalf("version = %d, want 2", v)
	}
	for _, name := range []string{"gr_a", "gr_b", "gr_c"} {
		if !tableExists(t, db, name) {
			t.Errorf("%s not created", name)
		}
	}
}
