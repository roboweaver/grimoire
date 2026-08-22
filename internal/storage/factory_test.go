package storage

import (
	"testing"

	"github.com/roboweaver/grimoire/internal/config"
)

func TestNewSQLiteReturnsReposAndCloses(t *testing.T) {
	cfg := config.DatabaseConfig{
		Vendor:      "sqlite",
		DSN:         "file::memory:?cache=shared",
		TablePrefix: "wp_",
	}
	repos, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if repos.Posts == nil || repos.Terms == nil || repos.Options == nil {
		t.Fatalf("expected non-nil repositories, got %+v", repos.Set)
	}
	if repos.DB() == nil {
		t.Fatal("expected non-nil *sql.DB")
	}
	if err := repos.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestNewUnsupportedVendor(t *testing.T) {
	_, err := New(config.DatabaseConfig{Vendor: "oracle", DSN: "x"})
	if err == nil {
		t.Fatal("expected error for unsupported vendor")
	}
}
