package web

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/render"
)

// Server wires content services and the render engine into HTTP handlers.
type Server struct {
	posts    *content.PostService
	terms    *content.TermService
	options  *content.OptionService
	comments *content.CommentService
	media    *content.MediaService
	menus    *content.NavMenuService
	render   *render.Engine
	log      *slog.Logger

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
}

// NewServer builds a Server. log may be nil, in which case slog.Default is used.
func NewServer(posts *content.PostService, terms *content.TermService, options *content.OptionService, eng *render.Engine, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	return &Server{posts: posts, terms: terms, options: options, render: eng, log: log}
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
	r.Method(http.MethodGet, "/{slug}", s.handler(s.single))
	return r
}
