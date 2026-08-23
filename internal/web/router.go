package web

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/render"
)

// Server wires content services and the render engine into HTTP handlers.
type Server struct {
	posts   *content.PostService
	terms   *content.TermService
	options *content.OptionService
	render  *render.Engine
	log     *slog.Logger

	auth    Sessions
	authCfg AuthConfig

	// admin backs the read-only /admin/api JSON endpoints; nil until WithAdmin.
	admin adminReader
	// spa serves the embedded React Spectrum admin under /admin; nil until WithAdmin.
	spa http.Handler
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
	r.Method(http.MethodGet, "/category/{slug}", s.handler(s.category))
	r.Method(http.MethodGet, "/", s.handler(s.home))
	r.Method(http.MethodGet, "/{slug}", s.handler(s.single))
	return r
}
