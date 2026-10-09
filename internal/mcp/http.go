package mcp

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// HTTPOptions controls the streamable HTTP exposure. The defaults implement
// plan §10.2.3: loopback-only binding and strict Host/Origin validation so a
// web page cannot drive a local agent (DNS rebinding / CSRF). AllowRemote
// explicitly accepts the deployment responsibility for auth, TLS, and reverse
// proxying.
type HTTPOptions struct {
	Addr        string
	AllowRemote bool
}

// Validate rejects non-loopback bindings unless the operator opted in. This is
// the AT-17 gate: a misconfigured public listener refuses to start instead of
// serving an unauthenticated knowledge service.
func (o HTTPOptions) Validate() error {
	if o.AllowRemote || strings.TrimSpace(o.Addr) == "" {
		return nil
	}
	host, _, err := net.SplitHostPort(o.Addr)
	if err != nil {
		return fmt.Errorf("invalid http address %q: %w", o.Addr, err)
	}
	host = strings.TrimSpace(host)
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("http address %q is not loopback; pass --allow-remote after configuring authentication and TLS", o.Addr)
	}
	return nil
}

// isLoopbackHost reports whether a host or host:port refers to the local
// machine without trusting a rebinding DNS name.
func isLoopbackHost(hostPort string) bool {
	host := hostPort
	if hostname, _, err := net.SplitHostPort(hostPort); err == nil {
		host = hostname
	}
	host = strings.Trim(strings.TrimSpace(host), "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// SecurityMiddleware wraps the streamable handler with Host/Origin checks.
// Non-browser clients send no Origin and pass; the Host header is still
// validated so a rebound DNS name cannot reach a loopback server. AllowRemote
// deployments own origin policy at their proxy.
func SecurityMiddleware(handler http.Handler, allowRemote bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if allowRemote {
			handler.ServeHTTP(w, req)
			return
		}
		if !isLoopbackHost(req.Host) {
			http.Error(w, "host header is not loopback", http.StatusForbidden)
			return
		}
		if origin := req.Header.Get("Origin"); origin != "" {
			parsed, err := url.Parse(origin)
			if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || !isLoopbackHost(parsed.Host) {
				http.Error(w, "cross-origin requests are rejected on loopback servers", http.StatusForbidden)
				return
			}
		}
		handler.ServeHTTP(w, req)
	})
}
