package scheduler

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/registry"
	"github.com/roboweaver/grimoire/internal/routing"
	"github.com/roboweaver/grimoire/internal/storage"
	"github.com/roboweaver/grimoire/internal/storage/migrate"
)

// Tests for the capability-suffix scheduler option (task 11.1; Req 7.6, D14).
// They fail to compile until WithCapabilitySuffixes exists, and content's
// WithPostRegistry is the registry hook of task 8.

// suffixFixture opens a migrated SQLite database, builds a registry with one
// product type (capability_type product) and a real PostWriteService over it,
// and stores one due future product row owned by another author.
func suffixFixture(t *testing.T) (repos *storage.Repositories, posts *content.PostWriteService, id int64) {
	t.Helper()
	ctx := context.Background()
	dbCfg := config.DatabaseConfig{Vendor: "sqlite", DSN: t.TempDir() + "/grimoire.db", TablePrefix: "wp_"}
	repos, err := storage.New(dbCfg)
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	t.Cleanup(func() { repos.Close() })
	migFS, err := storage.MigrationsFS(dbCfg.Vendor)
	if err != nil {
		t.Fatalf("MigrationsFS: %v", err)
	}
	if _, err := migrate.Apply(ctx, repos.DB(), migFS, dbCfg.Vendor, dbCfg.TablePrefix); err != nil {
		t.Fatalf("migrate.Apply: %v", err)
	}
	st, err := routing.Parse("/%postname%/", "", "")
	if err != nil {
		t.Fatalf("routing.Parse: %v", err)
	}
	yes := true
	reg, err := registry.Build(config.ContentConfig{
		PostTypes: []config.PostTypeConfig{
			{Name: "product", Public: &yes, Slug: "product", WithFront: &yes, CapabilityType: "product"},
		},
	}, st)
	if err != nil {
		t.Fatalf("registry.Build: %v", err)
	}
	posts = content.NewPostWriteService(repos.PostWriter,
		content.WithPostRegistry(reg), content.WithPostNodeReader(repos.PostNodeReader))
	past := time.Now().Add(-time.Hour)
	id, err = repos.PostWriter.Create(ctx, domain.Post{
		Author: 7, Title: "Due widget", Status: "future", Type: "product",
		Slug: "due-widget", Date: past, DateGMT: past,
	})
	if err != nil {
		t.Fatalf("create future product: %v", err)
	}
	return repos, posts, id
}

func TestSchedulerWithCapabilitySuffixesPublishesCustomType(t *testing.T) {
	repos, posts, id := suffixFixture(t)
	s := New(repos.Scheduled, posts, time.Minute, testLogger(), WithCapabilitySuffixes("products"))
	s.tick(context.Background())
	got, err := repos.PostWriter.ByID(context.Background(), id)
	if err != nil {
		t.Fatalf("ByID: %v", err)
	}
	if got.Status != "publish" {
		t.Fatalf("status = %q, want publish", got.Status)
	}
}

func TestSchedulerWithoutSuffixesLogsAndLeavesCustomTypeFuture(t *testing.T) {
	repos, posts, id := suffixFixture(t)
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	s := New(repos.Scheduled, posts, time.Minute, log)
	s.tick(context.Background())
	got, err := repos.PostWriter.ByID(context.Background(), id)
	if err != nil {
		t.Fatalf("ByID: %v", err)
	}
	if got.Status != "future" {
		t.Fatalf("status = %q, want future", got.Status)
	}
	if !strings.Contains(buf.String(), "failed to publish scheduled post") {
		t.Fatalf("expected a failure log line, got %q", buf.String())
	}
}
