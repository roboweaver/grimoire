package storagetest

import (
	"os"
	"testing"

	"github.com/roboweaver/grimoire/internal/storage"
)

func TestMySQLContract(t *testing.T) {
	dsn := os.Getenv("GRIMOIRE_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("set GRIMOIRE_TEST_MYSQL_DSN to run the MySQL contract suite")
	}
	RunContract(t, func(t *testing.T) (*storage.Repositories, func()) {
		return newReposFromDSN(t, "mysql", dsn)
	})
}
