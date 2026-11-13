package main

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/roboweaver/grimoire/internal/admin"
	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/render"
	"github.com/roboweaver/grimoire/internal/routing"
	"github.com/roboweaver/grimoire/internal/sanitize"
	"github.com/roboweaver/grimoire/internal/storage"
	"github.com/roboweaver/grimoire/internal/web"
)

// serverDeps is the process-level state main() resolves before the HTTP server
// is assembled. buildHandler turns it into the routed handler so a test can
// drive the exact wiring the binary serves (the M15 PR #74 lesson).
type serverDeps struct {
	Repos         *storage.Repositories
	Config        config.Config
	Log           *slog.Logger
	Permalinks    routing.Structure
	Sessions      *auth.SessionManager
	ContentPolicy *sanitize.Policy
	Engine        *render.Engine
	ThemesDir     string
}

// buildHandler constructs every content service, the REST mapper and the web
// Server from deps, and returns the routed handler plus the PostWriteService
// the Server's admin writes use (main hands the same instance to the scheduler).
func buildHandler(deps serverDeps) (http.Handler, *content.PostWriteService, error) {
	repos := deps.Repos
	cfg := deps.Config
	log := deps.Log
	permalinks := deps.Permalinks
	sm := deps.Sessions
	contentPolicy := deps.ContentPolicy
	eng := deps.Engine

	navMenuWriter, ok := repos.NavMenus.(domain.NavMenuWriter)
	if !ok {
		return nil, nil, fmt.Errorf("nav menu repository %T does not implement domain.NavMenuWriter", repos.NavMenus)
	}

	options := content.NewOptionService(repos.Options)
	comments := content.NewCommentService(repos.Comments, repos.CommentWriter, repos.CommentMeta, repos.PostWriter, content.NewBasicCommentSpamFilter(content.BasicCommentSpamFilterConfig{}), contentPolicy)
	menus := content.NewNavMenuService(repos.NavMenus, cfg.Theme)
	// One source of truth for the uploads base URL (Finding 3): both the media
	// endpoint's BaseURL and the REST mapper's srcset fallback anchor read this
	// same value, so they can never drift apart.
	const uploadsBaseURL = "/wp-content/uploads"
	media := content.NewMediaService(repos.Media, repos.MediaWriter, content.MediaConfig{UploadsDir: cfg.Media.UploadsDir, BaseURL: uploadsBaseURL, AllowedMIMEs: cfg.Media.AllowedMIMEs, MaxUploadSize: cfg.Media.MaxUploadSize})

	// One PageService serves both the web handlers (the nested /{slug}/* route and
	// the flat-leaf 301) and the REST mapper (a page's nested "link"), so the
	// served URL and the advertised link come from one ancestry computation
	// (M15 Req 3.5, 4.4). Without these WithPages calls the nested route is not
	// registered at all and a nested page 404s in the real binary, however well
	// the handler tests pass: they build their own Server.
	pageSvc := content.NewPageService(repos.PageReader, repos.Posts)
	restMapper := content.NewRESTMapper(repos.PostTerms, repos.PostMeta, repos.UserMeta, cfg.Database.TablePrefix).WithPermalinks(permalinks).WithUploadsBaseURL(uploadsBaseURL).WithPages(pageSvc)
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
	postWrite := content.NewPostWriteService(repos.PostWriter, content.WithRevisionSnapshotter(revisionWrite), content.WithContentPolicy(contentPolicy))
	// mediaWrite backs the REST media write surface (POST/PATCH
	// /wp-json/wp/v2/media). It layers capability-checked create/update over the
	// existing media read/write service and the Media reader / MediaWriter ports,
	// sharing the process-wide contentPolicy so metadata fields sanitize at the
	// write boundary like every other write path (Req 8.3, 11.1).
	mediaWrite := content.NewMediaWriteService(media, repos.Media, repos.MediaWriter, content.WithMediaContentPolicy(contentPolicy))
	// userWrite backs the REST user write surface (POST/PATCH/DELETE
	// /wp-json/wp/v2/users and .../users/me). It layers capability-checked
	// profile writes over the Users / UserMeta ports and the table prefix,
	// sharing the process-wide contentPolicy so the user description field
	// sanitizes at the write boundary like every other write path -- one
	// *sanitize.Policy now also governs the user description surface
	// (Req 9.3, 11.1).
	userWrite := content.NewUserService(repos.Users, repos.UserMeta, cfg.Database.TablePrefix, content.WithUserContentPolicy(contentPolicy))
	termWrite := content.NewTermWriteService(termRW)
	postTermsWrite := content.NewPostTermsWriteService(repos.PostWriter, repos.PostTermsWriter)
	// navMenuWrite backs the admin-API menu write surface (POST/PUT/DELETE on
	// /admin/api/menus and /menus/{id}/items). The factory's repos.NavMenus is a
	// concrete *wprepo.NavMenuRepo, which implements BOTH domain.NavMenuRepository
	// (read) and domain.NavMenuWriter (write); the type assertion recovers the
	// write view of that same instance so the writer (arg 1) and the read-back
	// reader (arg 2) are one object -- a write round-trips through the existing
	// read path -- without widening the factory Set shape, exactly as the
	// termReadWriter adapter does for terms. It shares the process-wide
	// contentPolicy so menu name/label fields sanitize at the write boundary like
	// every other write path (Req 3.1, 4, 9.1).
	navMenuWrite := content.NewNavMenuWriteService(navMenuWriter, repos.NavMenus, content.WithNavMenuContentPolicy(contentPolicy))

	// The archive routes M9b registers need two opt-in dependencies beyond the
	// M8 counter: WithHierarchy supplies the taxonomy read CategoryArchive's
	// segment walk, ancestry and descendant set all derive from (Req 1.6), and
	// WithAuthors supplies the user_nicename resolution AuthorArchive needs
	// (Req 7.2). Both panic when unwired -- matching RecentPage's posture for a
	// missing PostCounter -- and both are now reachable from a request, so this
	// is where they get supplied.
	posts := content.NewPostService(repos.Posts).WithCounter(repos.PostCounter).WithAuthors(repos.Users)
	terms := content.NewTermService(repos.Terms, repos.Posts).WithHierarchy(repos.TermReader)
	handler := web.NewServer(
		posts,
		terms,
		options,
		eng,
		log,
	).WithPermalinks(permalinks).WithPages(pageSvc).WithThemeStatic(deps.ThemesDir, cfg.Theme).WithContentFeatures(comments, media, menus).WithMediaWrites(mediaWrite).WithUserWrites(userWrite).WithNavMenuWrites(navMenuWrite).WithContentPolicy(contentPolicy).WithFeaturedImages(featured).WithAuth(sm, web.AuthConfig{
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

	return handler, postWrite, nil
}

// termReadWriter combines domain.TermWriter and domain.TermReader into the
// single value content.NewTermWriteService requires. Both storage.Set fields
// are backed by the same concrete wprepo.TermRepo, but Set exposes them as
// separate interface-typed fields, so buildHandler wires them together here
// rather than widening Set's shape.
type termReadWriter struct {
	domain.TermWriter
	domain.TermReader
}
