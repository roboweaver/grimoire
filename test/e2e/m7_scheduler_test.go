package e2e_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/scheduler"
	"github.com/roboweaver/grimoire/internal/storage"
	"github.com/roboweaver/grimoire/internal/storage/migrate"
)

// TestM7SchedulerWiring exercises the exact wiring cmd/grimoire/main.go uses
// to run the publish scheduler (Phase 5, Req 4.1-4.3, 4.6, 4.8): a
// scheduler.Scheduler built from cfg.Scheduler.Interval() and the real
// storage.Set.Scheduled/PostWriter ports (the same ports and config field
// main.go wires), started in its own goroutine sharing a cancellable
// context with the simulated HTTP server's shutdown context -- exactly the
// arrangement design.md's `cmd/grimoire/main.go` section describes. It
// asserts a "future" post whose post_date is already due becomes "publish"
// within one interval with zero HTTP requests made against it, and that
// cancelling the shared context stops the scheduler goroutine promptly (no
// goroutine leak).
func TestM7SchedulerWiring(t *testing.T) {
	ctx := context.Background()
	dsn := filepath.Join(t.TempDir(), "grimoire.db")
	dbcfg := config.DatabaseConfig{Vendor: "sqlite", DSN: dsn, TablePrefix: "wp_"}

	repos, err := storage.New(dbcfg)
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	t.Cleanup(func() { repos.Close() })

	migFS, err := storage.MigrationsFS(dbcfg.Vendor)
	if err != nil {
		t.Fatalf("MigrationsFS: %v", err)
	}
	if _, err := migrate.Apply(ctx, repos.DB(), migFS, dbcfg.Vendor, dbcfg.TablePrefix); err != nil {
		t.Fatalf("migrate.Apply: %v", err)
	}

	// A "future" post whose post_date is already in the past: due to
	// publish on the very first tick.
	past := time.Now().Add(-time.Hour)
	postID, err := repos.PostWriter.Create(ctx, domain.Post{
		Author: 1, Title: "Scheduled post", Content: "content",
		Status: "future", Type: "post", Slug: "scheduled-post",
		Date: past, DateGMT: past,
	})
	if err != nil {
		t.Fatalf("create scheduled post: %v", err)
	}

	// cfg.Scheduler.Interval() -- the new M7 config field/method (task
	// 5.2) -- is the exact value main.go passes to scheduler.New (Req
	// 4.2). Loading it through config.Load (rather than constructing
	// SchedulerConfig by hand) exercises the real YAML/default wiring
	// main.go relies on.
	cfgPath := filepath.Join(t.TempDir(), "grimoire.yaml")
	cfgYAML := "database:\n  vendor: sqlite\n  dsn: " + dsn + "\nscheduler:\n  interval_seconds: 1\n"
	if err := os.WriteFile(cfgPath, []byte(cfgYAML), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if got, want := cfg.Scheduler.Interval(), time.Second; got != want {
		t.Fatalf("cfg.Scheduler.Interval() = %v, want %v", got, want)
	}

	postWrite := content.NewPostWriteService(repos.PostWriter)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	sched := scheduler.New(repos.Scheduled, postWrite, cfg.Scheduler.Interval(), log)

	// schedCtx mirrors main.go's signal.NotifyContext-derived ctx: the
	// scheduler goroutine and the HTTP server goroutine share exactly this
	// context, so cancelling it is the one shutdown signal both observe
	// (Req 4.3). No HTTP server is started here at all -- the point of
	// this test is that the scheduled publish happens without any HTTP
	// request being made.
	schedCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		sched.Run(schedCtx)
		close(done)
	}()

	// Wait for slightly more than one interval for the first tick to fire
	// and publish the post.
	deadline := time.Now().Add(3 * cfg.Scheduler.Interval())
	var got domain.Post
	for time.Now().Before(deadline) {
		got, err = repos.PostWriter.ByID(ctx, postID)
		if err != nil {
			t.Fatalf("ByID: %v", err)
		}
		if got.Status == "publish" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if got.Status != "publish" {
		t.Fatalf("post %d status = %q after waiting for scheduler, want %q", postID, got.Status, "publish")
	}

	// Cancelling the shared context must stop the scheduler goroutine
	// promptly -- no goroutine leak past shutdown.
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("scheduler goroutine did not stop after context cancellation (leak)")
	}
}
