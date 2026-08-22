package rebind

import "testing"

func TestRebindPostgresNumbersPlaceholders(t *testing.T) {
	got := Rebind("postgres", "INSERT INTO t (a, b, c) VALUES (?, ?, ?)")
	want := "INSERT INTO t (a, b, c) VALUES ($1, $2, $3)"
	if got != want {
		t.Fatalf("Rebind postgres = %q, want %q", got, want)
	}
}

func TestRebindPostgresSinglePlaceholder(t *testing.T) {
	got := Rebind("postgres", "SELECT 1 FROM t WHERE name = ? LIMIT 1")
	want := "SELECT 1 FROM t WHERE name = $1 LIMIT 1"
	if got != want {
		t.Fatalf("Rebind postgres = %q, want %q", got, want)
	}
}

func TestRebindPostgresSkipsStringLiterals(t *testing.T) {
	got := Rebind("postgres", "INSERT INTO t (a, b) VALUES ('who?', ?)")
	want := "INSERT INTO t (a, b) VALUES ('who?', $1)"
	if got != want {
		t.Fatalf("Rebind postgres = %q, want %q", got, want)
	}
}

func TestRebindMySQLPassthrough(t *testing.T) {
	q := "INSERT INTO t (a, b, c) VALUES (?, ?, ?)"
	if got := Rebind("mysql", q); got != q {
		t.Fatalf("Rebind mysql = %q, want unchanged %q", got, q)
	}
}

func TestRebindSQLitePassthrough(t *testing.T) {
	q := "SELECT 1 FROM t WHERE name = ? LIMIT 1"
	if got := Rebind("sqlite", q); got != q {
		t.Fatalf("Rebind sqlite = %q, want unchanged %q", got, q)
	}
}

func TestRebindNoPlaceholders(t *testing.T) {
	q := "CREATE TABLE t (id INTEGER PRIMARY KEY)"
	if got := Rebind("postgres", q); got != q {
		t.Fatalf("Rebind postgres = %q, want unchanged %q", got, q)
	}
}
