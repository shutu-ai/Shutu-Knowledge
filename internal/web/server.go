// Package web serves the Knowledge HTTP surface: health/status endpoints in
// Phase 1, the REST API in later phases, and the embedded SPA in the Web
// phase. In extension mode the Agent reverse-proxies this server; in serve
// mode it runs standalone.
package web

import (
	"context"
	"encoding/json"
	"net"
	"net/http"

	"github.com/shutu-ai/shutu-knowledge/internal/app"
	"github.com/shutu-ai/shutu-knowledge/internal/version"
)

// Server is the HTTP surface owner.
type Server struct {
	app *app.App
	srv *http.Server
}

// New builds the HTTP server with the Phase 1 routes.
func New(a *app.App) *Server {
	mux := http.NewServeMux()
	s := &Server{app: a, srv: &http.Server{Handler: mux}}
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /api/status", s.handleStatus)
	mux.HandleFunc("GET /api/version", s.handleVersion)
	return s
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
