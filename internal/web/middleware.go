package web

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/roboweaver/grimoire/internal/domain"
)

// handlerFunc is a handler that may return an error, mapped to a status by the
// server's error handling.
type handlerFunc func(http.ResponseWriter, *http.Request) error

// handler adapts a handlerFunc into an http.Handler, mapping errors to status
// codes without leaking internal detail to the client.
func (s *Server) handler(h handlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := h(w, r); err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				http.Error(w, "Not Found", http.StatusNotFound)
				return
			}
			s.log.Error("handler error", "method", r.Method, "path", r.URL.Path, "err", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
	})
}

// statusRecorder captures the response status for logging.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// RequestLogger logs each request's method, path, status, and duration.
func RequestLogger(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)
			log.Info("request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.status,
				"duration", time.Since(start),
			)
		})
	}
}

// Recoverer converts panics into a 500 response and logs them.
func Recoverer(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					log.Error("panic recovered", "method", r.Method, "path", r.URL.Path, "panic", rec)
					http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
