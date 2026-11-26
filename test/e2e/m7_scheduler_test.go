package e2e_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/scheduler"
	"github.com/roboweaver/grimoire/internal/storage"
	"github.com/roboweaver/grimoire/internal/storage/migrate"
	"github.com/roboweaver/grimoire/internal/storage/seed"
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
	dsn := testDSN(t)
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

// TestM22BinaryTrashPurge builds the real ./cmd/grimoire binary with a short
// content.trash_days and asserts that a trashed row whose _wp_trash_meta_time is
// older than the window disappears after the first scheduler tick, while a
// freshly trashed row stays (M22 Req 23.3). It catches a main.go or
// buildScheduler omission that unit-level assembly tests cannot see.
func TestM22BinaryTrashPurge(t *testing.T) {
	if testing.Short() {
		t.Skip("built-binary test skipped under -short")
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go toolchain unavailable: ", err)
	}
	ctx := context.Background()
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	bin := filepath.Join(tmp, "grimoire")
	build := exec.Command(goBin, "build", "-o", bin, "./cmd/grimoire")
	build.Dir = repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build ./cmd/grimoire: %v\n%s", err, out)
	}

	dsn := "file:" + filepath.Join(tmp, "grimoire.db") + "?_pragma=busy_timeout(5000)"
	dbCfg := config.DatabaseConfig{Vendor: "sqlite", DSN: dsn, TablePrefix: "wp_"}
	repos, err := storage.New(dbCfg)
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	migFS, err := storage.MigrationsFS(dbCfg.Vendor)
	if err != nil {
		t.Fatalf("MigrationsFS: %v", err)
	}
	if _, err := migrate.Apply(ctx, repos.DB(), migFS, dbCfg.Vendor, dbCfg.TablePrefix); err != nil {
		t.Fatalf("migrate.Apply: %v", err)
	}
	if err := seed.Run(ctx, repos.DB(), dbCfg.Vendor, dbCfg.TablePrefix); err != nil {
		t.Fatalf("seed.Run: %v", err)
	}
	trashed := func(title, slug string, age time.Duration) int64 {
		t.Helper()
		id, err := repos.PostWriter.Create(ctx, domain.Post{
			Author: 1, Title: title, Status: "trash", Type: "post", Slug: slug,
			Date: time.Now(), DateGMT: time.Now(),
		})
		if err != nil {
			t.Fatalf("create trashed %s: %v", slug, err)
		}
		at := time.Now().Add(-age).Unix()
		if _, err := repos.DB().ExecContext(ctx,
			"INSERT INTO wp_postmeta (post_id, meta_key, meta_value) VALUES (?, '_wp_trash_meta_time', ?)", id, fmt.Sprint(at)); err != nil {
			t.Fatalf("insert _wp_trash_meta_time for %s: %v", slug, err)
		}
		return id
	}
	expired := trashed("Expired", "expired", 3*24*time.Hour)
	fresh := trashed("Fresh", "fresh", time.Hour)
	exists := func(id int64) bool {
		t.Helper()
		var n int
		if err := repos.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM wp_posts WHERE ID = ?", id).Scan(&n); err != nil {
			t.Fatalf("count post %d: %v", id, err)
		}
		return n > 0
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()
	cfgPath := filepath.Join(tmp, "grimoire.yaml")
	cfgYAML := fmt.Sprintf(`server:
  addr: %q
theme: default
database:
  vendor: sqlite
  dsn: %q
  table_prefix: wp_
session:
  cookie_name: grimoire_session
media:
  uploads_dir: %q
scheduler:
  interval_seconds: 1
content:
  trash_days: 1
`, addr, dsn, filepath.Join(tmp, "uploads"))
	if err := os.WriteFile(cfgPath, []byte(cfgYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	logs := &syncBuffer{}
	cmd := exec.Command(bin, "-config", cfgPath, "-themes", filepath.Join(repoRoot, "themes"))
	cmd.Dir = tmp
	cmd.Stdout, cmd.Stderr = logs, logs
	if err := cmd.Start(); err != nil {
		t.Fatalf("start binary: %v", err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Signal(os.Interrupt)
		select {
		case <-exited:
		case <-time.After(15 * time.Second):
			_ = cmd.Process.Kill()
			<-exited
		}
		repos.Close()
		if t.Failed() {
			t.Logf("binary output:\n%s", logs.String())
		}
	})

	gone := false
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		select {
		case err := <-exited:
			t.Fatalf("binary exited early: %v\n%s", err, logs.String())
		default:
		}
		if !exists(expired) {
			gone = true
			break
		}
	}
	if !gone {
		t.Fatalf("expired trashed row %d was not purged by the binary within 20s", expired)
	}
	if !exists(fresh) {
		t.Errorf("fresh trashed row %d was purged", fresh)
	}
}
