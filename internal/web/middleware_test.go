package web

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/roboweaver/grimoire/internal/domain"
)

func quietServer() *Server {
	return &Server{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func TestHandlerNotFoundMapsTo404(t *testing.T) {
	s := quietServer()
	h := s.handler(func(http.ResponseWriter, *http.Request) error {
		return fmt.Errorf("lookup: %w", domain.ErrNotFound)
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestHandlerGenericErrorMapsTo500NoLeak(t *testing.T) {
	s := quietServer()
	secret := "SELECT * FROM wp_posts secret detail"
	h := s.handler(func(http.ResponseWriter, *http.Request) error {
		return errors.New(secret)
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), secret) {
		t.Fatalf("response leaked internal error: %s", rec.Body.String())
	}
}

func TestRecovererCatchesPanic(t *testing.T) {
	mw := Recoverer(slog.New(slog.NewTextHandler(io.Discard, nil)))
	h := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "boom") {
		t.Fatalf("response leaked panic value: %s", rec.Body.String())
	}
}
