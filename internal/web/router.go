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
	r.Method(http.MethodGet, "/category/{slug}", s.handler(s.category))
	r.Method(http.MethodGet, "/", s.handler(s.home))
	// Admin group must be registered before the public catch-all so /admin is
	// never shadowed by content resolution (Req 1.2).
	s.registerAdmin(r)
	// REST group likewise, so /wp-json/* is never shadowed (Req 1.3).
	s.registerREST(r)
	// Permalink routes for the configured structure, registered before the flat
	// catch-all. Both slash forms are registered because chi matches them as
	// distinct routes: registering only the canonical one would make chi 404 the
	// other before `single` could redirect it, which is exactly the duplicate-URL
	// case Requirement 3.3 exists to close.
	//
	// These patterns have a fixed segment count and so cannot shadow
	// /category/{slug}, /, /login or /wp-content/uploads/*; the relative order of
	// those is unchanged. A single-segment structure such as /%postname%/ does
	// collide with /{slug} at chi's parameter node, which chi resolves silently in
	// favor of whichever was registered last rather than panicking -- so the two
	// forms of one post can arrive under different parameter names. That is why
	// `single` derives its components from the request path rather than from
	// chi's parameter names.
	for _, pattern := range s.permalinks.ChiPatterns() {
		r.Method(http.MethodGet, pattern, s.handler(s.single))
	}
	// Still registered when a structure is configured, but its role changes: it
	// becomes the canonical-redirect path (Req 3.1) rather than a renderer.
	r.Method(http.MethodGet, "/{slug}", s.handler(s.single))
	return r
}
