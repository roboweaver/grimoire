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
