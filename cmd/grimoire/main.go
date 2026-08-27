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

	"github.com/roboweaver/grimoire/internal/admin"
	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/render"
	"github.com/roboweaver/grimoire/internal/scheduler"
	"github.com/roboweaver/grimoire/internal/storage"
	"github.com/roboweaver/grimoire/internal/web"
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

	comments := content.NewCommentService(repos.Comments, repos.CommentWriter, repos.CommentMeta, repos.PostWriter, content.NewBasicCommentSpamFilter(content.BasicCommentSpamFilterConfig{}))
	menus := content.NewNavMenuService(repos.NavMenus, cfg.Theme)
	media := content.NewMediaService(repos.Media, repos.MediaWriter, content.MediaConfig{UploadsDir: cfg.Media.UploadsDir, BaseURL: "/wp-content/uploads", AllowedMIMEs: cfg.Media.AllowedMIMEs, MaxUploadSize: cfg.Media.MaxUploadSize})

	restMapper := content.NewRESTMapper(repos.PostTerms, repos.PostMeta, repos.UserMeta, cfg.Database.TablePrefix)
	featured := content.NewFeaturedImageService(repos.PostMeta, repos.Media)
	appPasswords := &auth.ApplicationPasswords{
		Users:  repos.Users,
		Meta:   repos.UserMeta,
		Prefix: cfg.Database.TablePrefix,
	}

	// termReadWriter combines the storage factory's separate TermWriter and
	// TermReader ports (both backed by the same concrete wprepo.TermRepo,
	// per factory.go's comment) into the single value
	// content.NewTermWriteService requires, without touching Phase 1's
	// already-committed factory.go Set shape.
	termRW := termReadWriter{TermWriter: repos.TermWriter, TermReader: repos.TermReader}
	// revisionWrite snapshots a post's pre-edit state on every update/delete
	// (Req 1, 5) and backs the admin revision-history routes (Req 2).
	// maxPerPost is -1 (unlimited retention) since M7 adds no config knob
	// for Requirement 5.1's "configurable maximum" -- operators wanting a
	// cap can be served by a future config field without changing this
	// wiring shape.
	revisionWrite := content.NewRevisionWriteService(repos.Revisions, repos.PostWriter, -1)
	autosave := content.NewAutosaveService(repos.Revisions, repos.PostWriter)
	postWrite := content.NewPostWriteService(repos.PostWriter, content.WithRevisionSnapshotter(revisionWrite))
	termWrite := content.NewTermWriteService(termRW)
	postTermsWrite := content.NewPostTermsWriteService(repos.PostWriter, repos.PostTermsWriter)

	posts := content.NewPostService(repos.Posts).WithCounter(repos.PostCounter)
	handler := web.NewServer(
		posts,
		content.NewTermService(repos.Terms, repos.Posts),
		content.NewOptionService(repos.Options),
		eng,
		log,
	).WithThemeStatic(*themesDir, cfg.Theme).WithContentFeatures(comments, media, menus).WithFeaturedImages(featured).WithAuth(sm, web.AuthConfig{
		CookieName: cfg.Session.CookieName,
		Secure:     cfg.Session.CookieSecure,
		MaxAge:     cfg.Session.TTLHours * 3600,
	}).WithAdmin(admin.Handler("/admin"), content.NewAdminService(
		repos.AdminPosts, repos.PostWriter, repos.PostCounter,
		repos.UserCounter, repos.TermCounter, repos.Users,
	)).WithAdminWrites(
		postWrite, termWrite, postTermsWrite, repos.PostTerms,
	).WithAdminRevisions(
		revisionWrite, autosave,
	).WithREST(
		restMapper, repos.AdminPosts, repos.PostWriter, repos.Posts, repos.Media, repos.Users,
		cfg.REST.PerPageMax,
	).WithApplicationPasswords(
		appPasswords, cfg.REST.RequireTLSForApplicationPasswords, cfg.REST.TrustedProxyHeader,
	).Routes()

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

// termReadWriter combines domain.TermWriter and domain.TermReader into the
// single value content.NewTermWriteService requires. Both storage.Set fields
// are backed by the same concrete wprepo.TermRepo, but Set exposes them as
// separate interface-typed fields, so main wires them together here rather
// than widening Set's shape.
type termReadWriter struct {
	domain.TermWriter
	domain.TermReader
}
