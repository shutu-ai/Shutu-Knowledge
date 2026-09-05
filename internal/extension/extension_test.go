package extension

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/shutu-ai/shutu-knowledge/internal/app"
)

func testApp(t *testing.T) *app.App {
	t.Helper()
	t.Setenv("SHUTU_KNOWLEDGE_HOME", t.TempDir())
	application, err := app.New(context.Background())
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	t.Cleanup(application.Close)
	return application
}

func TestManifestValidatesAgainstSDK(t *testing.T) {
	if err := Manifest().Validate(); err != nil {
		t.Fatalf("manifest invalid: %v", err)
	}
}

// TestStdioHandshakeAndHealth drives the real JSON-RPC loop end to end:
// initialize (version negotiation) then health (DB-backed readiness).
func TestStdioHandshakeAndHealth(t *testing.T) {
	application := testApp(t)

	requests := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"shutu-extension/1","agentApiVersion":"1.0","agentName":"shutu-agent","supportedCapabilities":{"health":true,"lifecycle":true}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"health"}`,
		`{"jsonrpc":"2.0","id":3,"method":"shutdown"}`,
	}, "\n") + "\n"

	var out bytes.Buffer
	if err := Run(context.Background(), application, strings.NewReader(requests), &out); err != nil {
		t.Fatalf("run: %v", err)
	}
	response := out.String()
	for _, want := range []string{`"protocolVersion":"shutu-extension/1"`, `"ready":true`, `"status":"ready"`} {
		if !strings.Contains(response, want) {
			t.Fatalf("response missing %s:\n%s", want, response)
		}
	}
}
