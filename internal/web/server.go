// Package web serves the Knowledge HTTP surface: health/status endpoints in
// Phase 1, the REST API in later phases, and the embedded SPA in the Web
// phase. In extension mode the Agent reverse-proxies this server; in serve
// mode it runs standalone.
package web

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"strings"

	"github.com/shutu-ai/shutu-knowledge/internal/app"
	"github.com/shutu-ai/shutu-knowledge/internal/version"
)

// Server is the HTTP surface owner.
type Server struct {
	app *app.App
	srv *http.Server
}

// MaxBodyBytes matches the upstream Knowledge API upload envelope limit.
const MaxBodyBytes = 32 << 20

//go:embed all:dist
var distFS embed.FS

// New builds the HTTP server with the Phase 1 routes.
func New(a *app.App) *Server {
	mux := http.NewServeMux()
	handler := maxBodyHandler(mux)
	s := &Server{app: a, srv: &http.Server{Handler: handler}}
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /api/status", s.handleStatus)
	mux.HandleFunc("GET /api/version", s.handleVersion)
	registerKnowledgeAPI(mux, s)
	mux.Handle("/", spaHandler())
	return s
}

func maxBodyHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)
		next.ServeHTTP(w, r)
	})
}

// Listen binds the configured address (port 0 = ephemeral).
func (s *Server) Listen(addr string) (net.Addr, error) {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	s.srv.Addr = listener.Addr().String()
	go func() {
		_ = s.srv.Serve(listener)
	}()
	return listener.Addr(), nil
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.srv.Shutdown(ctx)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

// spaHandler serves the built single-page app. Unknown non-API paths return
// index.html so the extension web contribution can own client-side routes.
func spaHandler() http.Handler {
	root, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic(fmt.Sprintf("web dist: %v", err))
	}
	fileServer := http.FileServer(http.FS(root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path != "/" {
			if _, err := fs.Stat(root, strings.TrimPrefix(r.URL.Path, "/")); err == nil {
				fileServer.ServeHTTP(w, r)
				return
			}
		}
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	report := s.app.Health.Snapshot(r.Context())
	status := http.StatusOK
	if !report.Ready {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, report)
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	report := s.app.Health.Snapshot(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"version": version.Version,
		"ready":   report.Ready,
		"status":  report.Status,
		"health":  report.Components,
	})
}

func (s *Server) handleVersion(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"version": version.Version})
}
