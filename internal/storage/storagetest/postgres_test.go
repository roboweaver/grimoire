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

func TestPostgresMediaWriteMetaContract(t *testing.T) {
	dsn := os.Getenv("GRIMOIRE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set GRIMOIRE_TEST_POSTGRES_DSN to run the PostgreSQL contract suite")
	}
	RunMediaWriteMetaContract(t, func(t *testing.T) (*storage.Repositories, string, string, func()) {
		repos, prefix, cleanup := newReposFromDSNWithPrefix(t, "postgres", dsn)
		return repos, "postgres", prefix, cleanup
	})
}

func TestPostgresPostDependentsPurgerContract(t *testing.T) {
	dsn := os.Getenv("GRIMOIRE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set GRIMOIRE_TEST_POSTGRES_DSN to run the PostgreSQL contract suite")
	}
	RunPostDependentsPurgerContract(t, func(t *testing.T) (*storage.Repositories, string, string, func()) {
		repos, prefix, cleanup := newReposFromDSNWithPrefix(t, "postgres", dsn)
		return repos, "postgres", prefix, cleanup
	})
}

func TestPostgresPostMenuItemPurgerContract(t *testing.T) {
	dsn := os.Getenv("GRIMOIRE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set GRIMOIRE_TEST_POSTGRES_DSN to run the PostgreSQL contract suite")
	}
	RunPostMenuItemPurgerContract(t, func(t *testing.T) (*storage.Repositories, string, string, func()) {
		repos, prefix, cleanup := newReposFromDSNWithPrefix(t, "postgres", dsn)
		return repos, "postgres", prefix, cleanup
	})
}

func TestPostgresNavMenuDeleteItemContract(t *testing.T) {
	dsn := os.Getenv("GRIMOIRE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set GRIMOIRE_TEST_POSTGRES_DSN to run the PostgreSQL contract suite")
	}
	RunNavMenuDeleteItemContract(t, func(t *testing.T) (*storage.Repositories, string, string, func()) {
		repos, prefix, cleanup := newReposFromDSNWithPrefix(t, "postgres", dsn)
		return repos, "postgres", prefix, cleanup
	})
}

func TestPostgresPostTermRelationshipPurgerContract(t *testing.T) {
	dsn := os.Getenv("GRIMOIRE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set GRIMOIRE_TEST_POSTGRES_DSN to run the PostgreSQL contract suite")
	}
	RunPostTermRelationshipPurgerContract(t, func(t *testing.T) (*storage.Repositories, string, string, func()) {
		repos, prefix, cleanup := newReposFromDSNWithPrefix(t, "postgres", dsn)
		return repos, "postgres", prefix, cleanup
	})
}

func TestPostgresMediaDeleteContract(t *testing.T) {
	dsn := os.Getenv("GRIMOIRE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set GRIMOIRE_TEST_POSTGRES_DSN to run the PostgreSQL contract suite")
	}
	RunMediaDeleteContract(t, func(t *testing.T) (*storage.Repositories, string, string, func()) {
		repos, prefix, cleanup := newReposFromDSNWithPrefix(t, "postgres", dsn)
		return repos, "postgres", prefix, cleanup
	})
}

func TestPostgresTermCountContract(t *testing.T) {
	dsn := os.Getenv("GRIMOIRE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set GRIMOIRE_TEST_POSTGRES_DSN to run the Postgres contract suite")
	}
	RunTermCountContract(t, func(t *testing.T) (*storage.Repositories, string, string, func()) {
		repos, prefix, cleanup := newReposFromDSNWithPrefix(t, "postgres", dsn)
		return repos, "postgres", prefix, cleanup
	})
}

func TestPostgresPostTrasherContract(t *testing.T) {
	dsn := os.Getenv("GRIMOIRE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set GRIMOIRE_TEST_POSTGRES_DSN to run the PostgreSQL contract suite")
	}
	RunPostTrasherContract(t, func(t *testing.T) (*storage.Repositories, string, string, func()) {
		repos, prefix, cleanup := newReposFromDSNWithPrefix(t, "postgres", dsn)
		return repos, "postgres", prefix, cleanup
	})
}

func TestPostgresNavMenuTrashedTargetContract(t *testing.T) {
	dsn := os.Getenv("GRIMOIRE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set GRIMOIRE_TEST_POSTGRES_DSN to run the PostgreSQL contract suite")
	}
	RunNavMenuTrashedTargetContract(t, func(t *testing.T) (*storage.Repositories, string, string, func()) {
		repos, prefix, cleanup := newReposFromDSNWithPrefix(t, "postgres", dsn)
		return repos, "postgres", prefix, cleanup
	})
}

func TestPostgresTrashedPostListerContract(t *testing.T) {
	dsn := os.Getenv("GRIMOIRE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set GRIMOIRE_TEST_POSTGRES_DSN to run the PostgreSQL contract suite")
	}
	RunTrashedPostListerContract(t, func(t *testing.T) (*storage.Repositories, string, string, func()) {
		repos, prefix, cleanup := newReposFromDSNWithPrefix(t, "postgres", dsn)
		return repos, "postgres", prefix, cleanup
	})
}

func TestPostgresPostMetaBackupSizesContract(t *testing.T) {
	dsn := os.Getenv("GRIMOIRE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set GRIMOIRE_TEST_POSTGRES_DSN to run the PostgreSQL contract suite")
	}
	RunPostMetaBackupSizesContract(t, func(t *testing.T) (*storage.Repositories, string, string, func()) {
		repos, prefix, cleanup := newReposFromDSNWithPrefix(t, "postgres", dsn)
		return repos, "postgres", prefix, cleanup
	})
}

func TestPostgresPostMetaThumbSharedContract(t *testing.T) {
	dsn := os.Getenv("GRIMOIRE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set GRIMOIRE_TEST_POSTGRES_DSN to run the PostgreSQL contract suite")
	}
	RunPostMetaThumbSharedContract(t, func(t *testing.T) (*storage.Repositories, string, string, func()) {
		repos, prefix, cleanup := newReposFromDSNWithPrefix(t, "postgres", dsn)
		return repos, "postgres", prefix, cleanup
	})
}

func TestPostgresThemeModCleanerContract(t *testing.T) {
	dsn := os.Getenv("GRIMOIRE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set GRIMOIRE_TEST_POSTGRES_DSN to run the PostgreSQL contract suite")
	}
	RunThemeModCleanerContract(t, func(t *testing.T) (*storage.Repositories, string, string, func()) {
		repos, prefix, cleanup := newReposFromDSNWithPrefix(t, "postgres", dsn)
		return repos, "postgres", prefix, cleanup
	})
}
