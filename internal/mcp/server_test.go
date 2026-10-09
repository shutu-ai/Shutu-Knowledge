package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/shutu-ai/shutu-knowledge/internal/agentquery"
	"github.com/shutu-ai/shutu-knowledge/internal/config"
	"github.com/shutu-ai/shutu-knowledge/internal/knowledge"
	"github.com/shutu-ai/shutu-knowledge/internal/storage"
)

// seedKnowledgeHome creates one isolated data home with one base, one
// document, and an active semantic compilation, then closes its connections
// so a subprocess can open the same storage.
func seedKnowledgeHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	db, err := storage.Open(filepath.Join(home, "knowledge.db"))
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	raw, err := storage.NewRawFileStore(filepath.Join(home, "raw"))
	if err != nil {
		t.Fatalf("open raw store: %v", err)
	}
	service := knowledge.NewService(db, raw, config.Defaults())
	base, err := service.CreateBase("MCP Fixture", "wire test base", "", knowledge.BaseConfig{})
	if err != nil {
		t.Fatalf("create base: %v", err)
	}
	if _, err := service.AddTextDocument(context.Background(), base.ID, "KPI overview",
		"# KPI overview\n\nThe KPI is calculated from the SDR counter and refreshed every fifteen minutes."); err != nil {
		t.Fatalf("add text: %v", err)
	}
	if _, err := service.CompileSemanticMemory(context.Background(), base.ID); err != nil {
		t.Fatalf("compile semantic memory: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}
	return home
}

func newTestServer(t *testing.T, home string) *mcp.Server {
	t.Helper()
	db, err := storage.Open(filepath.Join(home, "knowledge.db"))
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	raw, err := storage.NewRawFileStore(filepath.Join(home, "raw"))
	if err != nil {
		t.Fatalf("open raw store: %v", err)
	}
	service := knowledge.NewService(db, raw, config.Defaults())
	server, err := New(Options{Knowledge: service, AgentQuery: agentquery.New(service)})
	if err != nil {
		t.Fatalf("create mcp server: %v", err)
	}
	return server
}

const expectedToolCount = 9

func toolNames(tools []*mcp.Tool) map[string]bool {
	out := map[string]bool{}
	for _, tool := range tools {
		out[tool.Name] = true
	}
	return out
}

func decodeEnvelope(t *testing.T, result *mcp.CallToolResult) *Envelope {
	t.Helper()
	if result == nil {
		t.Fatal("nil tool result")
	}
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	var envelope Envelope
	if err := json.Unmarshal(encoded, &envelope); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	return &envelope
}

func callTool(t *testing.T, session *mcp.ClientSession, name string, arguments map[string]any) *Envelope {
	t.Helper()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: name, Arguments: arguments,
	})
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	return decodeEnvelope(t, result)
}

// TestMCPToolContractOverHTTP drives every read-only tool through a real MCP
// client over the real streamable HTTP transport against one seeded storage.
func TestMCPToolsOverHTTP(t *testing.T) {
	home := seedKnowledgeHome(t)
	server := newTestServer(t, home)
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "wire-test"}, nil)
	transport := &mcp.StreamableClientTransport{Endpoint: httpServer.URL}
	session, err := client.Connect(context.Background(), transport, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer session.Close()

	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(tools.Tools) != expectedToolCount {
		t.Fatalf("tool count = %d, want %d: %v", len(tools.Tools), expectedToolCount, toolNames(tools.Tools))
	}
	want := []string{
		"knowledge_list_bases", "knowledge_capabilities", "knowledge_search",
		"knowledge_explore", "knowledge_expand", "knowledge_evidence",
		"knowledge_context", "knowledge_compare_versions", "knowledge_diagnostics",
	}
	for _, name := range want {
		if !toolNames(tools.Tools)[name] {
			t.Fatalf("missing tool %q", name)
		}
	}

	bases := callTool(t, session, "knowledge_list_bases", map[string]any{})
	if bases.Status != "ok" || len(bases.Data.(map[string]any)["bases"].([]any)) == 0 {
		t.Fatalf("list bases envelope: %+v", bases)
	}
	baseID := bases.Data.(map[string]any)["bases"].([]any)[0].(map[string]any)["id"].(string)

	capabilities := callTool(t, session, "knowledge_capabilities", map[string]any{"base_ids": []string{baseID}})
	if capabilities.Status != "ok" {
		t.Fatalf("capabilities envelope: %+v", capabilities)
	}

	search := callTool(t, session, "knowledge_search", map[string]any{
		"query": "KPI", "base_ids": []string{baseID}, "mode": "hybrid", "top_k": 5,
	})
	if search.Status != "ok" || search.Context == nil || search.Context.EffectiveMode == "" {
		t.Fatalf("search envelope: %+v", search)
	}
	evidence := search.Data.(map[string]any)["evidence"].(map[string]any)
	if len(evidence["hits"].([]any)) == 0 {
		t.Fatal("hybrid search returned no evidence hits")
	}

	semanticHits := search.Data.(map[string]any)["semantic"].([]any)
	if len(semanticHits) == 0 {
		t.Fatal("hybrid search returned no semantic lane")
	}
	unitID := semanticHits[0].(map[string]any)["hits"].([]any)[0].(map[string]any)["unit_id"].(string)

	explore := callTool(t, session, "knowledge_explore", map[string]any{
		"query": "KPI", "base_ids": []string{baseID},
	})
	if explore.Status != "ok" {
		t.Fatalf("explore envelope: %+v", explore)
	}

	expand := callTool(t, session, "knowledge_expand", map[string]any{
		"root_unit_ids": []string{unitID}, "base_ids": []string{baseID},
	})
	if expand.Status != "ok" {
		t.Fatalf("expand envelope: %+v", expand)
	}

	ev := callTool(t, session, "knowledge_evidence", map[string]any{
		"unit_id": unitID, "base_ids": []string{baseID},
	})
	if ev.Status != "ok" || ev.Data.(map[string]any)["support_status"] != "resolved" {
		t.Fatalf("evidence envelope: %+v err=%s", ev, ev.Error.Message)
	}

	context := callTool(t, session, "knowledge_context", map[string]any{
		"query": "KPI", "base_ids": []string{baseID}, "token_budget": 800,
	})
	if context.Status != "ok" {
		t.Fatalf("context envelope: %+v", context)
	}

	compare := callTool(t, session, "knowledge_compare_versions", map[string]any{
		"base_ids": []string{baseID}, "from": "0.2", "to": "0.3",
	})
	if compare.Status != "ok" {
		t.Fatalf("compare envelope: %+v", compare)
	}

	diagnostics := callTool(t, session, "knowledge_diagnostics", map[string]any{})
	if diagnostics.Status != "ok" {
		t.Fatalf("diagnostics envelope: %+v", diagnostics)
	}
	traces := diagnostics.Data.(map[string]any)["traces"].([]any)
	if len(traces) < 5 {
		t.Fatalf("expected recorded traces, got %d", len(traces))
	}
	for _, trace := range traces {
		encoded, _ := json.Marshal(trace)
		if strings.Contains(string(encoded), "KPI") {
			t.Fatal("trace records must not contain query text")
		}
	}
}

// TestMCPResourcesAndPrompts verifies read-only resources and prompts through
// the real client.
func TestMCPResourcesAndPrompts(t *testing.T) {
	home := seedKnowledgeHome(t)
	server := newTestServer(t, home)
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "wire-test"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: httpServer.URL}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer session.Close()

	bases := callTool(t, session, "knowledge_list_bases", map[string]any{})
	baseID := bases.Data.(map[string]any)["bases"].([]any)[0].(map[string]any)["id"].(string)
	search := callTool(t, session, "knowledge_search", map[string]any{
		"query": "KPI", "base_ids": []string{baseID}, "mode": "semantic",
	})
	semanticHits := search.Data.(map[string]any)["semantic"].([]any)
	unitID := semanticHits[0].(map[string]any)["hits"].([]any)[0].(map[string]any)["unit_id"].(string)
	snapshot := search.Data.(map[string]any)["snapshots"].([]any)[0].(map[string]any)["semanticGeneration"].(float64)

	manifestURI := "shutu-knowledge://base/" + baseID + "/manifest"
	read, err := session.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: manifestURI})
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if !strings.Contains(read.Contents[0].Text, baseID) {
		t.Fatalf("manifest content: %s", read.Contents[0].Text)
	}

	unitURI := fmt.Sprintf("shutu-knowledge://base/%s/generation/%d/unit/%s", baseID, int64(snapshot), unitID)
	unitRead, err := session.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: unitURI})
	if err != nil {
		t.Fatalf("read unit resource: %v", err)
	}
	if !strings.Contains(unitRead.Contents[0].Text, unitID) {
		t.Fatalf("unit resource content: %s", unitRead.Contents[0].Text)
	}

	_, err = session.ReadResource(context.Background(), &mcp.ReadResourceParams{
		URI: "shutu-knowledge://base/missing/manifest",
	})
	if err == nil {
		t.Fatal("missing base resource must fail")
	}

	prompts, err := session.ListPrompts(context.Background(), nil)
	if err != nil {
		t.Fatalf("list prompts: %v", err)
	}
	if len(prompts.Prompts) != 2 {
		t.Fatalf("prompt count = %d, want 2", len(prompts.Prompts))
	}
	prompt, err := session.GetPrompt(context.Background(), &mcp.GetPromptParams{
		Name:      "cited-research",
		Arguments: map[string]string{"question": "How is KPI computed?"},
	})
	if err != nil {
		t.Fatalf("get prompt: %v", err)
	}
	if len(prompt.Messages) != 1 || prompt.Messages[0].Role != "user" {
		t.Fatalf("prompt messages: %+v", prompt.Messages)
	}
}

// TestMCPStdioRealProcess proves the shipped binary serves the same contract
// over a real stdio subprocess with a real MCP client.
func TestMCPStdioRealProcess(t *testing.T) {
	home := seedKnowledgeHome(t)
	binary := buildTestBinary(t)

	cmd := exec.Command(binary, "mcp")
	cmd.Env = append(os.Environ(), "SHUTU_KNOWLEDGE_HOME="+home)

	client := mcp.NewClient(&mcp.Implementation{Name: "stdio-wire-test"}, nil)
	session, err := client.Connect(context.Background(), &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatalf("connect stdio: %v", err)
	}
	defer session.Close()

	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools over stdio: %v", err)
	}
	if len(tools.Tools) != expectedToolCount {
		t.Fatalf("stdio tool count = %d", len(tools.Tools))
	}

	bases := callTool(t, session, "knowledge_list_bases", map[string]any{})
	if bases.Status != "ok" {
		t.Fatalf("stdio list bases envelope: %+v", bases)
	}
	baseID := bases.Data.(map[string]any)["bases"].([]any)[0].(map[string]any)["id"].(string)
	search := callTool(t, session, "knowledge_search", map[string]any{
		"query": "KPI", "base_ids": []string{baseID}, "mode": "hybrid", "top_k": 5,
	})
	if search.Status != "ok" {
		t.Fatalf("stdio search envelope: %+v", search)
	}
	evidence := search.Data.(map[string]any)["evidence"].(map[string]any)
	if len(evidence["hits"].([]any)) == 0 {
		t.Fatal("stdio hybrid search returned no evidence hits")
	}
}

// TestMCPStdioHTTPSemanticParity proves the same tool request produces the
// same envelope data on both transports (trace ids intentionally differ).
func TestMCPStdioHTTPSemanticParity(t *testing.T) {
	home := seedKnowledgeHome(t)
	server := newTestServer(t, home)
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()

	httpClient := mcp.NewClient(&mcp.Implementation{Name: "parity-http"}, nil)
	httpSession, err := httpClient.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: httpServer.URL}, nil)
	if err != nil {
		t.Fatalf("connect http: %v", err)
	}
	defer httpSession.Close()

	binary := buildTestBinary(t)
	cmd := exec.Command(binary, "mcp")
	cmd.Env = append(os.Environ(), "SHUTU_KNOWLEDGE_HOME="+home)
	stdioClient := mcp.NewClient(&mcp.Implementation{Name: "parity-stdio"}, nil)
	stdioSession, err := stdioClient.Connect(context.Background(), &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatalf("connect stdio: %v", err)
	}
	defer stdioSession.Close()

	baseFingerprint := func(envelope *Envelope) string {
		bases := envelope.Data.(map[string]any)["bases"].([]any)
		if len(bases) == 0 {
			return "no-bases"
		}
		base := bases[0].(map[string]any)
		return fmt.Sprintf("%v|%v|%v|%v", base["id"], base["display_name"], base["document_count"], base["semantic_status"])
	}
	httpBases := callTool(t, httpSession, "knowledge_list_bases", map[string]any{})
	stdioBases := callTool(t, stdioSession, "knowledge_list_bases", map[string]any{})
	if baseFingerprint(httpBases) != baseFingerprint(stdioBases) {
		t.Fatal("list_bases envelopes differ between transports")
	}

	request := map[string]any{"query": "KPI", "mode": "hybrid", "top_k": 5}
	httpSearch := callTool(t, httpSession, "knowledge_search", request)
	stdioSearch := callTool(t, stdioSession, "knowledge_search", request)
	hitFingerprint := func(envelope *Envelope) string {
		hits := envelope.Data.(map[string]any)["evidence"].(map[string]any)["hits"].([]any)
		out := make([]string, 0, len(hits))
		for _, hit := range hits {
			item := hit.(map[string]any)
			out = append(out, fmt.Sprintf("%v|%v|%v", item["id"], item["score"], item["rank"]))
		}
		return strings.Join(out, ";")
	}
	if hitFingerprint(httpSearch) != hitFingerprint(stdioSearch) {
		t.Fatal("search hits differ between transports")
	}
	if httpSearch.Context.EffectiveMode != stdioSearch.Context.EffectiveMode ||
		!reflect.DeepEqual(httpSearch.Context.BaseIDs, stdioSearch.Context.BaseIDs) {
		t.Fatal("search contexts differ between transports")
	}
}

// buildTestBinary compiles the shipped CLI once for stdio subprocess tests.
func buildTestBinary(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	name := "shutu-knowledge-mcp-test"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	binary := filepath.Join(dir, name)
	cmd := exec.Command("go", "build", "-o", binary, "github.com/shutu-ai/shutu-knowledge/cmd/shutu-knowledge")
	cmd.Dir = repoRoot(t)
	cmd.Env = append(os.Environ(), "GOCACHE="+filepath.Join(repoRoot(t), ".gocache"), "GOFLAGS=-mod=mod")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build test binary: %v\n%s", err, output)
	}
	return binary
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve repo root")
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}
