package operations

import (
	"strings"
	"testing"
)

func TestSafeErrorMessageRedactsSecretsPathsAndBoundsOutput(t *testing.T) {
	message := `request failed: apiKey="secret-value" token=token-value Bearer bearer-value ` +
		`url=https://example.test/search?access_token=url-secret&query=hello ` +
		`open C:\Users\alice\private\input.pdf path=/var/lib/shutu/private.txt ` +
		strings.Repeat("x", 2000)
	got := SafeErrorMessage(message)
	for _, secret := range []string{"secret-value", "token-value", "bearer-value", "url-secret", "C:\\Users\\alice", "/var/lib/shutu/private.txt"} {
		if strings.Contains(got, secret) {
			t.Fatalf("sanitized error contains %q: %s", secret, got)
		}
	}
	if !strings.Contains(got, "[REDACTED]") || !strings.Contains(got, "[PATH_REDACTED]") {
		t.Fatalf("sanitized markers missing: %s", got)
	}
	if len([]rune(got)) > 1024 {
		t.Fatalf("sanitized error is unbounded: %d runes", len([]rune(got)))
	}
}

func TestSafeErrorMessagePreservesUsefulCause(t *testing.T) {
	got := SafeErrorMessage("document not found")
	if got != "document not found" {
		t.Fatalf("useful error changed: %q", got)
	}
}
