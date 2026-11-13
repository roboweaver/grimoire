// Command grimoire is the public web server. It loads configuration, opens the
// configured database, wires content services and the theme render engine into
// the HTTP router, serves the site, and shuts down gracefully.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/render"
	"github.com/roboweaver/grimoire/internal/sanitize"
	"github.com/roboweaver/grimoire/internal/scheduler"
	"github.com/roboweaver/grimoire/internal/storage"
	// Extension packages register grimoire hooks (see pkg/extensions:
	// RegisterAction/RegisterFilter) from their init() function. To compile
	// a real extension into this binary, blank-import it here, e.g.:
	//
	//	_ "your/extension/package"
	//
	// No extensions are registered by default: grimoire boots and serves
	// wp-json with zero hooks firing until one is linked in.
)

func main() {
	cfgPath := flag.String("config", "configs/grimoire.sqlite.yaml", "path to grimoire config YAML")
	themesDir := flag.String("themes", "themes", "path to the themes directory")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stdout, nil))

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Error("load config", "err", err)
		os.Exit(1)
	}

	log.Info("starting grimoire",
		"addr", cfg.Server.Addr,
		"theme", cfg.Theme,
		"vendor", cfg.Database.Vendor,
		"dsn", config.RedactDSN(cfg.Database.Vendor, cfg.Database.DSN),
	)

	// Non-fatal: a fresh install may not have any uploads yet, but this
	// gives operators pointing grimoire at an existing external WordPress
	// database a clear diagnostic instead of silent 404s on every image.
	if err := config.CheckUploadsDir(cfg.Media.UploadsDir); err != nil {
		log.Warn("media uploads dir may be misconfigured", "err", err)
	}

	repos, err := storage.New(cfg.Database)
	if err != nil {
		log.Error("open storage", "err", err)
		os.Exit(1)
	}
	defer repos.Close()

	eng, err := render.Load(*themesDir, cfg.Theme)
	if err != nil {
		log.Error("load theme", "theme", cfg.Theme, "err", err)
		os.Exit(1)
	}

	sm := &auth.SessionManager{
		Users:    repos.Users,
		Meta:     repos.UserMeta,
		Sessions: repos.Sessions,
		TTL:      cfg.Session.TTL(),
		Prefix:   cfg.Database.TablePrefix,
	}

	// One Policy for the process: allow-listing HTML is not vendor-, transport-
	// or field-specific, so there is exactly one of these (Req 1.1). Shared by
	// both write services and the Server (pendingEcho + commentView backstop) so
	// a single instance governs every path.
	contentPolicy := sanitize.New()

	// The permalink structure is resolved once here, then handed to both the
	// server and the REST mapper, so the path the site serves at 200 and the
	// "link" a REST response advertises come from one parse rather than two
	// (M9a Req 1.6, 6.1).
	options := content.NewOptionService(repos.Options)
	permalinks := resolvePermalinks(context.Background(), options, log)

	handler, postWrite, err := buildHandler(serverDeps{
		Repos:         repos,
		Config:        cfg,
		Log:           log,
		Permalinks:    permalinks,
		Sessions:      sm,
		ContentPolicy: contentPolicy,
		Engine:        eng,
		ThemesDir:     *themesDir,
	})
	if err != nil {
		log.Error("assemble handler", "err", err)
		os.Exit(1)
	}

	srv := &http.Server{Addr: cfg.Server.Addr, Handler: handler}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server error", "err", err)
			stop()
		}
	}()

	// sched polls for "future" posts whose post_date has passed and
	// publishes them (Requirement 4). It shares ctx with the HTTP server
	// goroutine above, so cancelling ctx (via signal or the server error
	// path) stops both the same way -- no separate scheduler lifecycle to
	// reason about (Req 4.3).
	sched := scheduler.New(repos.Scheduled, postWrite, cfg.Scheduler.Interval(), log)
	go sched.Run(ctx)

	<-ctx.Done()
	log.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("graceful shutdown failed", "err", err)
		os.Exit(1)
	}
	log.Info("stopped")
}
