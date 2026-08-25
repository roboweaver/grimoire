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
	"github.com/roboweaver/grimoire/internal/render"
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

	handler := web.NewServer(
		content.NewPostService(repos.Posts),
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
	)).WithREST(
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
