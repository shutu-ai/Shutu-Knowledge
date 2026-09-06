package extension

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/shutu-ai/shutu-agent/sdk/extension"
	"github.com/shutu-ai/shutu-knowledge/internal/config"
	"github.com/shutu-ai/shutu-knowledge/internal/knowledge"
)

type brokenEmbedder struct{}

func (brokenEmbedder) Embed(context.Context, []string) ([][]float64, error) {
	return nil, context.DeadlineExceeded
}
func (brokenEmbedder) ModelKey() string { return "broken:embedder" }

type upgradedEmbedder struct{}

func (upgradedEmbedder) Embed(_ context.Context, texts []string) ([][]float64, error) {
	vectors := make([][]float64, len(texts))
	for index := range texts {
		vectors[index] = []float64{1, 0}
	}
	return vectors, nil
}
func (upgradedEmbedder) ModelKey() string { return "upgraded:v2" }

type upgradedReranker struct{}

func (upgradedReranker) Rerank(_ context.Context, _ string, texts []string) ([]float64, error) {
	scores := make([]float64, len(texts))
	for index := range scores {
		scores[index] = 1
	}
	return scores, nil
}
func (upgradedReranker) ModelKey() string { return "upgraded:rerank-v2" }

// TestDisabledScopeIsolation covers the extension-owned part of removal: once
// Knowledge is disabled, neither native context nor tools may inject or expose
// data. The process itself stays healthy so Agent lifecycle is unaffected.
func TestDisabledScopeIsolation(t *testing.T) {
	application := testApp(t)
	ctx := context.Background()
	base, err := application.Knowledge.CreateBase("Private", "", "", knowledge.BaseConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.Knowledge.AddTextDocument(ctx, base.ID, "Operations",
		"The retry budget retry budget is 30 seconds and the release token is alpha-7788."); err != nil {
		t.Fatal(err)
	}
	enabledContext, err := ProvideContext(ctx, application, extension.ContextRequest{
		SessionID: "gate-session", UserInput: "What is the retry budget?",
	})
	if err != nil || len(enabledContext.Contributions) == 0 {
		t.Fatalf("enabled context: %#v %v", enabledContext, err)
	}

	if err := application.Knowledge.SetEnabledScope(boolPointer(false), nil); err != nil {
		t.Fatal(err)
	}
	disabledContext, err := ProvideContext(ctx, application, extension.ContextRequest{
		SessionID: "gate-session", UserInput: "What is the retry budget?",
	})
	if err != nil || len(disabledContext.Contributions) != 0 {
		t.Fatalf("disabled context must be empty: %#v %v", disabledContext, err)
	}
	tool, err := CallTool(ctx, application, extension.ToolCallRequest{
		Name: "knowledge_search", Arguments: map[string]any{"query": "retry budget"},
	})
	if err != nil || tool.Error == "" || tool.Value != nil {
		t.Fatalf("disabled tool must fail closed: %#v %v", tool, err)
	}

	report := application.Health.Snapshot(ctx)
	if !report.Ready {
		t.Fatalf("logical disable must not crash or fail the managed process: %#v", report)
	}
}

// TestProtocolUpgradeIndependence drives two complete v1 sessions on one app
// with different host-supplied versions/names. Knowledge code is unchanged;
// the same manifest and callbacks remain usable across compatible hosts.
func TestProtocolUpgradeIndependence(t *testing.T) {
	application := testApp(t)
	ctx := context.Background()
	base, err := application.Knowledge.CreateBase("Upgrade", "", "", knowledge.BaseConfig{})
	if err != nil {
		t.Fatal(err)
	}
	document, err := application.Knowledge.AddTextDocument(
		ctx, base.ID, "Guide", "The upgrade safety marker is ZQ-9001.",
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.Knowledge.AddTextDocument(
		ctx, base.ID, "Background", "Unrelated operational background remains available.",
	); err != nil {
		t.Fatal(err)
	}
	for _, hostVersion := range []string{"0.9.0", "1.1.0"} {
		requests := strings.Join([]string{
			`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"shutu-extension/1","agentApiVersion":"1.0","agentName":"shutu-agent","agentVersion":"` + hostVersion + `","supportedCapabilities":{"tools":true,"contextProvider":true,"health":true,"lifecycle":true}}}`,
			`{"jsonrpc":"2.0","id":2,"method":"health"}`,
		}, "\n") + "\n"
		var out bytes.Buffer
		if err := Run(context.Background(), application, strings.NewReader(requests), &out); err != nil {
			t.Fatalf("host %s: run: %v", hostVersion, err)
		}
		response := out.String()
		for _, want := range []string{`"ready":true`, `"tools":true`} {
			if !strings.Contains(response, want) {
				t.Fatalf("host %s response missing %s:\n%s", hostVersion, want, response)
			}
		}
	}
	// An internal provider upgrade changes ranking behavior without changing
	// the Agent-facing tool result contract.
	semantic := true
	if _, err := application.Knowledge.RenameBase(base.ID, nil, nil, nil, &knowledge.BaseConfig{
		SemanticChunk:  &semantic,
		ChunkSeparator: "\n",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := application.Knowledge.ReindexDocument(ctx, document.ID); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.Embedding.Provider = "openai"
	cfg.Rerank.Enabled = true
	cfg.Rerank.Model = "rerank-v2"
	cfg.Rerank.BaseURL = "http://127.0.0.1:1"
	application.Knowledge.SetGlobalConfig(cfg)
	application.Knowledge.SetProviders(upgradedEmbedder{}, upgradedReranker{})
	search, err := CallTool(ctx, application, extension.ToolCallRequest{
		Name: "knowledge_search", Arguments: map[string]any{
			"query": "upgrade safety marker", "extraQueries": []any{"unrelated background"},
		},
	})
	if err != nil || search.Error != "" {
		t.Fatalf("search after internal upgrade: %#v %v", search, err)
	}
	result, ok := search.Value.(searchToolResult)
	if !ok || result.Total != 2 || len(result.Citations) != 2 ||
		!strings.Contains(result.Citations[0], "Guide") &&
			!strings.Contains(result.Citations[1], "Guide") {
		t.Fatalf("stable tool contract: %#v", search.Value)
	}
	if !result.Reranked || result.Rerank == nil || !result.Rerank.Applied {
		t.Fatalf("replacement reranker was not used: %+v", result.Rerank)
	}
}

// TestFailureIsolationAdapters verifies that domain failures become tool
// errors rather than panics or RPC transport failures. Knowledge crash and
// process restart are additionally covered by docs/agent_integration.md.
func TestFailureIsolationAdapters(t *testing.T) {
	application := testApp(t)
	ctx := context.Background()
	base, err := application.Knowledge.CreateBase("Isolation", "", "", knowledge.BaseConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.Knowledge.AddTextDocument(ctx, base.ID, "Guide", "searchable isolation text"); err != nil {
		t.Fatal(err)
	}
	// Embedding provider is unavailable by default; lexical search remains a
	// successful tool result while explicit vector mode becomes a safe error.
	success, err := CallTool(ctx, application, extension.ToolCallRequest{
		Name: "knowledge_search", Arguments: map[string]any{
			"query": "anything", "mode": "lexical", "baseId": base.ID,
		},
	})
	if err != nil || success.Error != "" {
		t.Fatalf("lexical search with unavailable embedding: %#v %v", success, err)
	}
	cfg := config.Defaults()
	cfg.Embedding.Provider = "openai"
	cfg.Embedding.BaseURL = "http://127.0.0.1:9"
	cfg.Embedding.Model = "broken"
	application.Knowledge.SetGlobalConfig(cfg)
	application.Knowledge.SetProviders(brokenEmbedder{}, nil)
	vector, err := CallTool(ctx, application, extension.ToolCallRequest{
		Name: "knowledge_search", Arguments: map[string]any{
			"query": "anything", "mode": "vector", "baseId": base.ID,
		},
	})
	if err != nil || vector.Error != "" {
		t.Fatalf("vector provider failure must not become an RPC failure: %#v %v", vector, err)
	}
	if result, ok := vector.Value.(searchToolResult); !ok || result.Mode != "lexical" {
		t.Fatalf("vector provider failure must degrade safely: %#v", vector.Value)
	}
	badFile, err := CallTool(ctx, application, extension.ToolCallRequest{
		Name: "knowledge_add_document", Arguments: map[string]any{
			"baseId": base.ID, "title": "Bad", "content": "",
		},
	})
	if err != nil || badFile.Error == "" || badFile.Value != nil {
		t.Fatalf("bad input must be isolated: %#v %v", badFile, err)
	}
	report := application.Health.Snapshot(ctx)
	if !report.Ready {
		t.Fatalf("optional/model failures must not fail readiness: %#v", report)
	}
}

func intPointer(value int) *int { return &value }

func TestAutoRetrieveGlobalAndBaseLayering(t *testing.T) {
	application := testApp(t)
	ctx := context.Background()
	base, err := application.Knowledge.CreateBase("Layering", "", "", knowledge.BaseConfig{
		AutoRetrieve:    boolPointer(false),
		AutoRetrieveMax: intPointer(0),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.Knowledge.AddTextDocument(ctx, base.ID, "Operations",
		"The retry budget retry budget is 30 seconds and the release token is alpha-7788."); err != nil {
		t.Fatal(err)
	}
	request := func(session string) extension.ContextRequest {
		return extension.ContextRequest{SessionID: session, UserInput: "What is the retry budget?"}
	}

	result, err := ProvideContext(ctx, application, request("base-disabled"))
	if err != nil || len(result.Contributions) != 0 {
		t.Fatalf("explicit base disable: %#v %v", result, err)
	}

	application.Config.AutoRetrieve.Enabled = false
	if _, err := application.Knowledge.RenameBase(base.ID, nil, nil, nil, &knowledge.BaseConfig{
		AutoRetrieve:    boolPointer(true),
		AutoRetrieveMax: intPointer(3),
	}); err != nil {
		t.Fatal(err)
	}
	result, err = ProvideContext(ctx, application, request("global-disabled"))
	if err != nil || len(result.Contributions) != 0 {
		t.Fatalf("global disable must win: %#v %v", result, err)
	}

	application.Config.AutoRetrieve.Enabled = true
	if _, err := application.Knowledge.RenameBase(base.ID, nil, nil, nil, &knowledge.BaseConfig{
		AutoRetrieve:    boolPointer(true),
		AutoRetrieveMax: intPointer(0),
	}); err != nil {
		t.Fatal(err)
	}
	result, err = ProvideContext(ctx, application, request("weight-zero"))
	if err != nil || len(result.Contributions) != 0 {
		t.Fatalf("explicit zero weight must exclude base: %#v %v", result, err)
	}

	if _, err := application.Knowledge.RenameBase(base.ID, nil, nil, nil, &knowledge.BaseConfig{
		AutoRetrieve:    boolPointer(true),
		AutoRetrieveMax: intPointer(3),
	}); err != nil {
		t.Fatal(err)
	}
	result, err = ProvideContext(ctx, application, request("base-enabled"))
	if err != nil || len(result.Contributions) == 0 {
		t.Fatalf("enabled base should contribute: %#v %v", result, err)
	}
}
