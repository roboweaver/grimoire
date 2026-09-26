package web

import (
	"log/slog"
	"net/http"
	"path/filepath"

	"github.com/go-chi/chi/v5"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/render"
	"github.com/roboweaver/grimoire/internal/routing"
)

// Server wires content services and the render engine into HTTP handlers.
type Server struct {
	posts    *content.PostService
	terms    *content.TermService
	options  *content.OptionService
	comments *content.CommentService
	media    *content.MediaService
	menus    *content.NavMenuService
	featured *content.FeaturedImageService
	render   *render.Engine
	log      *slog.Logger

	themeStaticDir string

	auth    Sessions
	authCfg AuthConfig

	// admin backs the read-only /admin/api JSON endpoints; nil until WithAdmin.
	admin adminReader
	// spa serves the embedded React Spectrum admin under /admin; nil until WithAdmin.
	spa http.Handler

	// Admin write dependencies (M6); all nil until WithAdminWrites. The
	// /admin/api write routes are only registered when their corresponding
	// dependency is non-nil (see adminAPIRouter) — WithAdminWrites is
	// expected to be called whenever WithAdmin is, so these are only ever
	// nil in tests/embedders that intentionally omit the write routes.
	postWrite      postAdminWriter
	termWrite      termAdminService
	postTermsWrite postTermsAdminWriter
	postTermsRead  postTermsAdminReader

	// Revision/autosave dependencies (M7); both nil until WithAdminRevisions,
	// in which case the corresponding /admin/api routes are not registered
	// (see adminAPIRouter).
	revisions revisionAdminService
	autosave  autosaveAdminService

	// REST API dependencies; restMapper nil until WithREST, in which case the
	// /wp-json/* routes are not registered at all.
	restMapper     *content.RESTMapper
	restPosts      domain.AdminPostRepository
	restPostByID   restPostByID
	restBySlug     domain.PostRepository
	restMedia      domain.MediaRepository
	restUsers      domain.UserRepository
	restPerPageMax int

	// Application Password auth (Req 8); appPasswords nil until
	// WithApplicationPasswords, in which case ApplicationPasswordAuth is
	// not mounted and Basic-auth credentials on /wp-json are ignored.
	appPasswords           *auth.ApplicationPasswords
	restRequireTLS         bool
	restTrustedProxyHeader string

	// permalinks is the resolved permalink_structure. It is read once at
	// startup rather than per request, because OptionService performs no
	// caching and the chi patterns are derived from it at registration time
	// (see the M9a design). NewServer defaults it to a flat structure, so a
	// Server that never saw WithPermalinks behaves exactly as it did before
	// M9a rather than carrying an ambiguous zero value.
	permalinks routing.Structure
}

// NewServer builds a Server. log may be nil, in which case slog.Default is used.
func NewServer(posts *content.PostService, terms *content.TermService, options *content.OptionService, eng *render.Engine, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	// An empty structure is WordPress's "plain" setting and never fails to
	// parse, so the error is not reachable here.
	flat, _ := routing.Parse("", "", "")
	return &Server{posts: posts, terms: terms, options: options, render: eng, log: log, permalinks: flat}
}

// WithPermalinks configures the permalink structure used to resolve single-post
// requests and to build canonical paths. It returns the same Server for
// chaining. When not called, or called with a flat Structure, the flat /{slug}
// route is canonical and no canonical redirects are issued.
//
// The Structure is supplied already parsed so the caller owns the fallback
// decision: routing.Parse returns a usable flat Structure alongside its error,
// and the startup path logs that error rather than refusing to boot.
func (s *Server) WithPermalinks(st routing.Structure) *Server {
	s.permalinks = st
	return s
}

// WithAuth enables the authentication routes and session middleware, wiring the
// session manager and cookie configuration into the server. It returns the same
// Server for chaining. When auth is nil the login/logout routes and session
// middleware are not registered.
func (s *Server) WithAuth(sessions Sessions, cfg AuthConfig) *Server {
	s.auth = sessions
	s.authCfg = cfg
	return s
}

// WithThemeStatic configures the server to serve the given theme's static/
// directory (e.g. its vendored Spectrum CSS) at /theme/static/*. themesDir is
// the root themes directory (as passed to render.Load), theme is the active
// theme name.
func (s *Server) WithThemeStatic(themesDir, theme string) *Server {
	s.themeStaticDir = filepath.Join(themesDir, theme, "static")
	return s
}

// WithApplicationPasswords enables HTTP Basic Application Password auth on
// the /wp-json REST surface (Req 8), wiring the shared
// *auth.ApplicationPasswords verifier into the server. requireTLS gates the
// transport-security check (Req 8.9; matches real WordPress's default
// refusal to accept Application Passwords over a plain, non-local
// connection); trustedProxyHeader, if non-empty, is honored as an
// additional TLS signal for deployments behind a TLS-terminating reverse
// proxy (same operator-declared-trust posture as AuthConfig.Secure). It
// returns the same Server for chaining. When ap is nil,
// ApplicationPasswordAuth is not mounted and Basic-auth credentials on
// /wp-json are ignored (session-cookie/anonymous evaluation proceeds as
// before).
func (s *Server) WithApplicationPasswords(ap *auth.ApplicationPasswords, requireTLS bool, trustedProxyHeader string) *Server {
	s.appPasswords = ap
	s.restRequireTLS = requireTLS
	s.restTrustedProxyHeader = trustedProxyHeader
	return s
}

// Routes returns the chi mux with middleware and routes wired. Static routes
// are registered before the catch-all /{slug} so they win.
func (s *Server) Routes() http.Handler {
	r := chi.NewRouter()
	r.Use(RequestLogger(s.log))
	r.Use(Recoverer(s.log))
	if s.auth != nil {
		r.Use(s.SessionMiddleware)
	}

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok"))
	})
	registerStatic(r)
	if s.themeStaticDir != "" {
		registerThemeStatic(r, s.themeStaticDir)
	}
	if s.auth != nil {
		r.Method(http.MethodGet, "/login", s.handler(s.loginForm))
		r.Method(http.MethodPost, "/login", s.handler(s.loginSubmit))
		r.Method(http.MethodPost, "/logout", s.handler(s.logoutSubmit))
	}
	if s.comments != nil {
		r.Method(http.MethodPost, "/comment", s.handler(s.commentSubmit))
	}
	if s.media != nil {
		r.Method(http.MethodGet, "/wp-content/uploads/*", s.handler(s.uploads))
	}
	r.Method(http.MethodGet, "/", s.handler(s.home))
	// Admin group must be registered before the public catch-all so /admin is
	// never shadowed by content resolution (Req 1.2).
	s.registerAdmin(r)
	// REST group likewise, so /wp-json/* is never shadowed (Req 1.3).
	s.registerREST(r)
	// Content routes for the configured structure: the permalink patterns, the
	// archive patterns derived from the resolved bases and the structure's front,
	// and the flat single-segment fallback. Every one of them is registered to
	// the single dispatcher s.resolve (Req 9.1).
	//
	// Both slash forms of each pattern are registered because chi matches them as
	// distinct routes: registering only the canonical one would make chi 404 the
	// other before the dispatcher could redirect it, which is exactly the
	// duplicate-URL case Requirement 3.3 exists to close. The category archive is
	// the one exception -- ArchivePatterns emits it as a single wildcard rooted at
	// its base, because a nested category path has a segment count no chi pattern
	// can express (Req 9.5), and a wildcard's remainder spans both slash forms on
	// its own. The handler derives the segments from r.URL.Path, the way
	// resolveSingle already does.
	//
	// These patterns collide with each other in ways chi does not report. It
	// resolves a collision at a parameter node silently in favour of whichever
	// pattern was registered last rather than panicking (M9a probed this; M9b
	// probed the wildcard-versus-parameter pairs and found the same), so a path
	// can arrive under a parameter name belonging to a different pattern. The
	// cases this tree can register today:
	//
	//   - a single-segment structure such as /%postname%/ against the flat
	//     /{slug}, so the two forms of one post arrive as {postname} on one and
	//     {slug} on the other (M9a's case);
	//   - the bare year archive /{year} against both of those, since a front-less
	//     structure roots its date archives at /;
	//   - a date archive against a permalink pattern of the same segment count,
	//     e.g. /{year}/{monthnum}/{day} against /%year%/%monthnum%/%postname%/;
	//   - a base that equals a leading literal of the structure, which registers
	//     /archives/* alongside /archives/{post_id} for category_base=archives on
	//     WordPress's Numeric preset (Req 4.4b, 9.7);
	//   - category_base equal to tag_base, or either equal to "author", which
	//     roots two archive patterns at one segment (Req 4.4a).
	//
	// Pointing all of them at s.resolve is what makes those silent choices
	// unobservable rather than something this code has to win: the dispatcher
	// classifies r.URL.Path exactly once and dispatches on the answer, so
	// precedence is a property of routing.Structure.Classify -- a pure function
	// with a documented table and a test per row -- and not of the order of these
	// calls (Req 9.1, 9.2, 9.7).
	//
	// The routes registered above keep their behavior because chi prefers a static
	// node over a parameter node, which is asserted rather than concluded by
	// inspection (routes_regression_test.go, Req 9.6). The one place that net
	// records a move is POST /comment/ -- never a registered route -- which
	// answers 405 instead of 404 under a structure whose date archives root a
	// GET-only /{year}/ at the same node; slashFormCases spells that out.
	s.requireArchiveDeps(s.permalinks)
	for _, pattern := range contentPatterns(s.permalinks) {
		r.Method(http.MethodGet, pattern, s.handler(s.resolve))
	}
	return r
}

// requireArchiveDeps panics when this Server is about to register archive
// patterns it cannot serve.
//
// The archive patterns are not opt-in. contentPatterns registers them from the
// permalink structure alone, and ArchivePatterns emits the category, tag and
// author patterns for every structure including the flat one -- /category/*
// has shipped since M1 -- so an embedder who builds a TermService without
// WithHierarchy, or a PostService without WithAuthors, gets a route whose
// handler dereferences a nil dependency on its first request. Recoverer turns
// that into a 500, which means the wiring gap is discoverable only by asking
// for a category or author URL and is reported as a server fault rather than as
// the startup mistake it is. Failing here moves it to the one moment the
// operator is watching, and names the call to add.
//
// Panicking is the point, in both senses. The nil dereference inside
// CategoryArchive and AuthorArchive is deliberate -- it matches RecentPage's
// posture for an unwired PostCounter -- and this only moves it earlier; it does
// not soften it into a log line an embedder would never read. And the
// alternative of quietly not registering the patterns was rejected: two
// embedders would then serve different routing tables for the same path, so
// /category/foo would 404 on one and render on the other, which is exactly the
// per-deployment routing divergence routing.Structure.Classify exists to
// prevent. A route either exists for every embedder of a given structure or the
// server does not start.
//
// NewServer takes posts and terms positionally, so neither is nil in any
// supported construction; the nil-service checks cost one comparison and keep
// the failure a named message rather than a second nil panic from the predicate
// call, for a caller who built a Server literal inside the package.
func (s *Server) requireArchiveDeps(st routing.Structure) {
	if len(st.ArchivePatterns()) == 0 {
		return
	}
	if s.terms == nil || !s.terms.HasHierarchy() {
		panic("web: Routes registers the category archive but the TermService has no taxonomy reader: " +
			"call content.NewTermService(...).WithHierarchy(repos.TermReader), " +
			"or every /category/* request will 500")
	}
	if s.posts == nil || !s.posts.HasAuthors() {
		panic("web: Routes registers the author archive but the PostService has no user reader: " +
			"call content.NewPostService(...).WithAuthors(repos.Users), " +
			"or every /author/* request will 500")
	}
}

// contentPatterns is every chi pattern the dispatcher owns, in registration
// order: the structure's permalink patterns, its archive patterns, and the flat
// /{slug} fallback.
//
// The flat pattern is registered whether or not a structure is configured; what
// changes is its role. With no structure it is the renderer, exactly as it was
// before M9a; with one it becomes the canonical-redirect path (Req 3.1).
//
// No pattern can appear twice in the result, so nothing here registers a
// duplicate: ArchivePatterns is internally duplicate-free (asserted in
// internal/routing), and its three parameter names -- {slug}, {nicename} and the
// date components -- cannot collide with a permalink pattern, because
// ChiPatterns always carries {postname} or {post_id} (Parse rejects a structure
// with no identifying token) and a base never normalizes to nothing, so every
// entity-archive pattern has at least two segments where the flat fallback has
// one. That is a claim worth stating rather than relying on: chi overwrites a
// duplicate pattern's endpoint silently, so a duplicate would be one more thing
// the router cannot tell anyone about.
func contentPatterns(st routing.Structure) []string {
	pats := st.ChiPatterns()
	pats = append(pats, st.ArchivePatterns()...)
	return append(pats, "/{slug}")
}
