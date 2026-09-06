package httpx

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewClientUsesEnvironmentProxyAndNoProxy(t *testing.T) {
	var proxyHits atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		proxyHits.Add(1)
		_, _ = io.WriteString(w, "through-proxy")
	}))
	defer proxy.Close()

	target := "http://proxy-target.invalid/item"
	t.Setenv("HTTP_PROXY", proxy.URL)
	t.Setenv("HTTPS_PROXY", proxy.URL)
	t.Setenv("NO_PROXY", "")
	client := NewClient(time.Second)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "through-proxy" || proxyHits.Load() != 1 {
		t.Fatalf("proxy request: status=%d body=%q hits=%d", resp.StatusCode, body, proxyHits.Load())
	}

	t.Setenv("NO_PROXY", "127.0.0.1")
	client = NewClient(time.Second)
	req, err = http.NewRequestWithContext(context.Background(), http.MethodGet, "http://127.0.0.1:9/item", nil)
	if err != nil {
		t.Fatal(err)
	}
	// Port 9 cannot be reached directly when NO_PROXY bypasses the proxy.
	if resp, err = client.Do(req); err == nil {
		_ = resp.Body.Close()
		t.Fatal("NO_PROXY target unexpectedly bypassed the fake-host failure path")
	}
	if proxyHits.Load() != 1 {
		t.Fatalf("NO_PROXY request hit proxy: %d", proxyHits.Load())
	}
}
