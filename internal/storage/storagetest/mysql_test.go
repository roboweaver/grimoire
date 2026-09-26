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

func TestMySQLTermParentContract(t *testing.T) {
	dsn := os.Getenv("GRIMOIRE_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("set GRIMOIRE_TEST_MYSQL_DSN to run the MySQL contract suite")
	}
	RunTermParentContract(t, func(t *testing.T) (*storage.Repositories, func()) {
		return newReposFromDSN(t, "mysql", dsn, SeedNestedCategories)
	})
}

func TestMySQLArchiveContract(t *testing.T) {
	dsn := os.Getenv("GRIMOIRE_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("set GRIMOIRE_TEST_MYSQL_DSN to run the MySQL contract suite")
	}
	RunArchiveContract(t, func(t *testing.T) (*storage.Repositories, func()) {
		return newReposFromDSN(t, "mysql", dsn, SeedArchiveFixtures)
	})
}

func TestMySQLNicenameContract(t *testing.T) {
	dsn := os.Getenv("GRIMOIRE_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("set GRIMOIRE_TEST_MYSQL_DSN to run the MySQL contract suite")
	}
	RunNicenameContract(t, func(t *testing.T) (*storage.Repositories, func()) {
		return newReposFromDSN(t, "mysql", dsn, SeedDuplicateNicenames)
	})
}

func TestMySQLNicenameAuditorContract(t *testing.T) {
	dsn := os.Getenv("GRIMOIRE_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("set GRIMOIRE_TEST_MYSQL_DSN to run the MySQL contract suite")
	}
	RunNicenameAuditorContract(t,
		func(t *testing.T) (*storage.Repositories, func()) {
			return newReposFromDSN(t, "mysql", dsn, SeedDuplicateNicenames)
		},
		func(t *testing.T) (*storage.Repositories, func()) {
			return newReposFromDSN(t, "mysql", dsn)
		})
}
