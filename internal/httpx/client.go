// Package httpx centralizes outbound HTTP policy for Knowledge. Remote
// integrations share proxy handling and bounded timeouts; tests may inject
// their own clients.
package httpx

import (
	"net/http"
	"time"
)

// NewClient builds an outbound client with the standard environment proxy
// contract: HTTP_PROXY/HTTPS_PROXY select a proxy and NO_PROXY creates
// exceptions. The transport is explicit rather than relying on whether a
// caller happened to leave http.Client.Transport zero.
func NewClient(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyFromEnvironment
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
	}
}
