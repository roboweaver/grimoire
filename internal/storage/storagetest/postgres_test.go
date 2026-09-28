package storagetest

import (
	"os"
	"testing"

	"github.com/roboweaver/grimoire/internal/storage"
)

func TestPostgresContract(t *testing.T) {
	dsn := os.Getenv("GRIMOIRE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set GRIMOIRE_TEST_POSTGRES_DSN to run the PostgreSQL contract suite")
	}
	RunContract(t, func(t *testing.T) (*storage.Repositories, func()) {
		return newReposFromDSN(t, "postgres", dsn)
	})
}

func TestPostgresTermParentContract(t *testing.T) {
	dsn := os.Getenv("GRIMOIRE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set GRIMOIRE_TEST_POSTGRES_DSN to run the PostgreSQL contract suite")
	}
	RunTermParentContract(t, func(t *testing.T) (*storage.Repositories, func()) {
		return newReposFromDSN(t, "postgres", dsn, SeedNestedCategories)
	})
}

func TestPostgresArchiveContract(t *testing.T) {
	dsn := os.Getenv("GRIMOIRE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set GRIMOIRE_TEST_POSTGRES_DSN to run the PostgreSQL contract suite")
	}
	RunArchiveContract(t, func(t *testing.T) (*storage.Repositories, func()) {
		return newReposFromDSN(t, "postgres", dsn, SeedArchiveFixtures)
	})
}

func TestPostgresNicenameContract(t *testing.T) {
	dsn := os.Getenv("GRIMOIRE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set GRIMOIRE_TEST_POSTGRES_DSN to run the PostgreSQL contract suite")
	}
	RunNicenameContract(t, func(t *testing.T) (*storage.Repositories, func()) {
		return newReposFromDSN(t, "postgres", dsn, SeedDuplicateNicenames)
	})
}

func TestPostgresNicenameAuditorContract(t *testing.T) {
	dsn := os.Getenv("GRIMOIRE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set GRIMOIRE_TEST_POSTGRES_DSN to run the PostgreSQL contract suite")
	}
	RunNicenameAuditorContract(t,
		func(t *testing.T) (*storage.Repositories, func()) {
			return newReposFromDSN(t, "postgres", dsn, SeedDuplicateNicenames)
		},
		func(t *testing.T) (*storage.Repositories, func()) {
			return newReposFromDSN(t, "postgres", dsn)
		})
}
