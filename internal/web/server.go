// Package web serves the Knowledge HTTP surface: health/status endpoints in
// Phase 1, the REST API in later phases, and the embedded SPA in the Web
// phase. In extension mode the Agent reverse-proxies this server; in serve
// mode it runs standalone.
package web

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/shutu-ai/shutu-knowledge/internal/app"
	"github.com/shutu-ai/shutu-knowledge/internal/operations"
	"github.com/shutu-ai/shutu-knowledge/internal/storage"
	"github.com/shutu-ai/shutu-knowledge/internal/version"
)

// Server is the HTTP surface owner.
type Server struct {
	app               *app.App
	srv               *http.Server
	operationKeyLimit *windowRateLimiter
}

// MaxBodyBytes allows a 100 MiB raw file plus Base64 and JSON envelope
// overhead for the file-upload API.
const MaxBodyBytes = 140 << 20

//go:embed all:dist
var distFS embed.FS

type webBuildInfo struct {
	BuiltAt string `json:"builtAt"`
	BuildID string `json:"buildId"`
	Files   int    `json:"files"`
}

var webBuild = mustWebBuild()

func mustWebBuild() webBuildInfo {
	data, err := fs.ReadFile(distFS, "dist/build-info.json")
	if err != nil {
		panic(fmt.Sprintf("read web build info: %v", err))
	}
	var info webBuildInfo
	if err := json.Unmarshal(data, &info); err != nil {
		panic(fmt.Sprintf("decode web build info: %v", err))
	}
	// Fallback for an out-of-tree build script that predates build identity.
	// Cache revalidation remains correct because the ETag is derived from the
	// embedded manifest bytes rather than trusting an empty field.
	if info.BuildID == "" {
		info.BuildID = fmt.Sprintf("%x", sha256.Sum256(data))
	}
	return info
}

// New builds the HTTP server with the Phase 1 routes.
func New(a *app.App) *Server {
	mux := http.NewServeMux()
	handler := maxBodyHandler(mux)
	s := &Server{
		app:               a,
		srv:               &http.Server{Handler: handler},
		operationKeyLimit: newWindowRateLimiter(60, time.Minute),
	}
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

// windowRateLimiter bounds unauthenticated credential issuance per peer. It is
// process-local fail-closed admission; a restart starts a fresh bounded window.
type windowRateLimiter struct {
	mu      sync.Mutex
	window  time.Duration
	limit   int
	entries map[string]*rateWindow
}

type rateWindow struct {
	start time.Time
	count int
}

func newWindowRateLimiter(limit int, window time.Duration) *windowRateLimiter {
	return &windowRateLimiter{window: window, limit: limit, entries: make(map[string]*rateWindow)}
}

func (l *windowRateLimiter) allow(key string) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	entry := l.entries[key]
	if entry == nil || now.Sub(entry.start) >= l.window {
		if len(l.entries) > 10000 {
			l.entries = make(map[string]*rateWindow)
		}
		entry = &rateWindow{start: now}
		l.entries[key] = entry
	}
	entry.count++
	return entry.count <= l.limit
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
				setWebCacheHeaders(w)
				if webCacheNotModified(w, r) {
					return
				}
				fileServer.ServeHTTP(w, r)
				return
			}
		}
		setWebCacheHeaders(w)
		if webCacheNotModified(w, r) {
			return
		}
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})
}

func webETag() string {
	return `"` + webBuild.BuildID + `"`
}

func setWebCacheHeaders(w http.ResponseWriter) {
	// Filenames are not content-addressed, so no-cache forces one lightweight
	// revalidation. The build-wide ETag makes that a 304 while the deployed
	// binary is unchanged and reliably changes after a new web build.
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("ETag", webETag())
}

func webCacheNotModified(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("If-None-Match") != webETag() {
		return false
	}
	w.WriteHeader(http.StatusNotModified)
	return true
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	report := s.app.HealthSnapshot(r.Context())
	status := http.StatusOK
	if !report.Ready {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, report)
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	report := s.app.HealthSnapshot(r.Context())
	var scheduler any
	if s.app.Operations != nil {
		snapshot, err := s.app.Operations.SchedulerContext(r.Context())
		if err != nil {
			scheduler = map[string]string{"error": err.Error()}
		} else {
			scheduler = snapshot
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"version": version.Version,
		"ready":   report.Ready,
		"status":  report.Status,
		"health":  report.Components,
		"capabilities": map[string]any{
			"operationsV1":         s.app.Operations != nil,
			"operationKeysV1":      s.app.Operations != nil,
			"uploadsV1":            s.app.Operations != nil,
			"commandSchemaVersion": 1,
			"maxUploadBytes":       operations.MaxUploadBytes,
		},
		"storageFormat": storageFormatReport(s.app, r.Context()),
		"scheduler":     scheduler,
	})
}

type versionPayload struct {
	version.Build
	WebBuild string `json:"webBuild"`
}

func storageFormatReport(a *app.App, ctx context.Context) any {
	format, err := storage.StorageFormatContext(ctx, a.DB.ReadDB())
	if err != nil {
		return map[string]string{"error": err.Error()}
	}
	return format
}

func (s *Server) handleVersion(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, versionPayload{
		Build:    version.Current(),
		WebBuild: webBuild.BuildID,
	})
}
