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
}

// NewServer builds a Server. log may be nil, in which case slog.Default is used.
func NewServer(posts *content.PostService, terms *content.TermService, options *content.OptionService, eng *render.Engine, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	return &Server{posts: posts, terms: terms, options: options, render: eng, log: log}
}

// Routes returns the chi mux with middleware and routes wired. Static routes
// are registered before the catch-all /{slug} so they win.
func (s *Server) Routes() http.Handler {
	r := chi.NewRouter()
	r.Use(RequestLogger(s.log))
	r.Use(Recoverer(s.log))

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok"))
	})
	registerStatic(r)
	r.Method(http.MethodGet, "/category/{slug}", s.handler(s.category))
	r.Method(http.MethodGet, "/", s.handler(s.home))
	r.Method(http.MethodGet, "/{slug}", s.handler(s.single))
	return r
}
