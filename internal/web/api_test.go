package web

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shutu-ai/shutu-knowledge/internal/app"
	"github.com/shutu-ai/shutu-knowledge/internal/knowledge"
	"github.com/shutu-ai/shutu-knowledge/internal/models"
	"github.com/shutu-ai/shutu-knowledge/internal/operations"
	"github.com/shutu-ai/shutu-knowledge/internal/runtime"
	"os"
	"path/filepath"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	t.Setenv("SHUTU_KNOWLEDGE_HOME", t.TempDir())
	t.Setenv("SHUTU_KNOWLEDGE_DISABLE_MANAGED_RUNTIME", "1")
	application, err := app.New(context.Background())
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	t.Cleanup(application.Close)
	return New(application)
}

func callRaw(t *testing.T, s *Server, method, path string, body any) (int, map[string]any, []byte) {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(data)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	rec := httptest.NewRecorder()
	s.srv.Handler.ServeHTTP(rec, req)
	var payload map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
	}
	return rec.Code, payload, rec.Body.Bytes()
}

func call(t *testing.T, s *Server, method, path string, body any) (int, map[string]any) {
	t.Helper()
	code, payload, _ := callRaw(t, s, method, path, body)
	return code, payload
}

func valueMap(t *testing.T, payload map[string]any) map[string]any {
	t.Helper()
	value, ok := payload["value"].(map[string]any)
	if !ok {
		t.Fatalf("expected object value: %v", payload)
	}
	return value
}

func waitOperation(t *testing.T, s *Server, operationID string) operations.Operation {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for {
		op, err := s.app.Operations.Get(operationID)
		if err != nil {
			t.Fatal(err)
		}
		switch op.State {
		case operations.StateSucceeded, operations.StateFailed, operations.StateCancelled, operations.StateInterrupted:
			return op
		}
		if time.Now().After(deadline) {
			t.Fatalf("operation %s timeout: %+v", operationID, op)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func createTextDocument(t *testing.T, s *Server, baseID, title, content string) string {
	t.Helper()
	code, payload := call(t, s, "POST", "/api/bases/"+baseID+"/documents", map[string]any{
		"title": title, "content": content,
	})
	if code != http.StatusAccepted {
		t.Fatalf("add document status = %d, body=%v", code, payload)
	}
	accepted := valueMap(t, payload)
	documentID := accepted["documentId"].(string)
	operationID := accepted["operationId"].(string)
	deadline := time.Now().Add(5 * time.Second)
	for {
		op, err := s.app.Operations.Get(operationID)
		if err != nil {
			t.Fatal(err)
		}
		if op.State == "succeeded" {
			return documentID
		}
		if op.State == "failed" {
			t.Fatalf("add document failed: %s", op.ErrorMessage)
		}
		if time.Now().After(deadline) {
			t.Fatalf("add document state = %s", op.State)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func addLegacyFiles(t *testing.T, s *Server, baseID string, files []map[string]any, conflict string) knowledge.AddFilesResult {
	t.Helper()
	code, payload := call(t, s, "POST", "/api/bases/"+baseID+"/files", map[string]any{
		"files": files, "conflict": conflict,
	})
	if code != http.StatusAccepted {
		t.Fatalf("add files status = %d, body=%v", code, payload)
	}
	operationID := valueMap(t, payload)["operationId"].(string)
	deadline := time.Now().Add(8 * time.Second)
	for {
		op, err := s.app.Operations.Get(operationID)
		if err != nil {
			t.Fatal(err)
		}
		if op.State == "succeeded" {
			var result knowledge.AddFilesResult
			if err := json.Unmarshal(op.Result, &result); err != nil {
				t.Fatalf("decode add files result: %v", err)
			}
			return result
		}
		if op.State == "failed" {
			t.Fatalf("add files failed: %s", op.ErrorMessage)
		}
		if time.Now().After(deadline) {
			t.Fatalf("add files state = %s", op.State)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestKnowledgeAPIRoundTrip(t *testing.T) {
	s := newTestServer(t)

	// Create a base.
	code, payload := call(t, s, "POST", "/api/bases", map[string]any{"name": "Docs", "group": "G1"})
	if code != http.StatusOK {
		t.Fatalf("create base: %d %v", code, payload)
	}
	base := valueMap(t, payload)
	baseID := base["id"].(string)

	// Import a text document and read chunks.
	docID := createTextDocument(t, s, baseID, "Note", "# Head\n\nsome body text")

	code, payload = call(t, s, "GET", "/api/documents/"+docID+"/chunks?limit=10&offset=0", nil)
	if code != http.StatusOK {
		t.Fatalf("chunks: %d %v", code, payload)
	}
	chunks := payload["value"].([]any)
	if len(chunks) == 0 {
		t.Fatal("no chunks returned")
	}

	// Rename, then delete.
	code, _ = call(t, s, "PATCH", "/api/bases/"+baseID, map[string]any{"name": "Papers"})
	if code != http.StatusOK {
		t.Fatalf("rename base: %d", code)
	}
	code, payload = call(t, s, "GET", "/api/bases/"+baseID+"/name", nil)
	if code != http.StatusOK || valueMap(t, payload)["name"] != "Papers" {
		t.Fatalf("base name endpoint: %d %v", code, payload)
	}
	code, _ = call(t, s, "PATCH", "/api/documents/"+docID, map[string]any{"title": "Renamed"})
	if code != http.StatusOK {
		t.Fatalf("rename: %d", code)
	}
	code, _ = call(t, s, "DELETE", "/api/documents/"+docID, nil)
	if code != http.StatusAccepted {
		t.Fatalf("delete: %d", code)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		code, _ = call(t, s, "GET", "/api/documents/"+docID+"?includeChunks=false", nil)
		if code == http.StatusNotFound {
			break
		}
		if code != http.StatusOK {
			t.Fatalf("deleted document lookup: %d", code)
		}
		if time.Now().After(deadline) {
			t.Fatal("deleted document remained readable")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Scope toggle round-trip.
	code, _ = call(t, s, "PUT", "/api/scope", map[string]any{"enabled": false})
	if code != http.StatusOK {
		t.Fatalf("scope put: %d", code)
	}
	code, payload = call(t, s, "GET", "/api/scope", nil)
	scope := valueMap(t, payload)
	if scope["enabled"] != false {
		t.Fatalf("scope state: %v", scope)
	}

	// Missing base yields 404 envelope.
	code, payload = call(t, s, "GET", "/api/bases/does-not-exist", nil)
	if code != http.StatusNotFound {
		t.Fatalf("missing base: %d %v", code, payload)
	}
}

func TestLegacyDocumentListProvidesBoundedCompatibilityMode(t *testing.T) {
	s := newTestServer(t)
	base, err := s.app.Knowledge.CreateBase("Paged documents", "", "", knowledge.BaseConfig{})
	if err != nil {
		t.Fatal(err)
	}
	titles := []string{"one", "two", "three"}
	for i := 0; i < 51; i++ {
		titles = append(titles, fmt.Sprintf("bulk-%02d", i))
	}
	for _, title := range titles {
		if _, err := s.app.Knowledge.AddTextDocument(context.Background(), base.ID, title, title+" body"); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/api/bases/"+base.ID+"/documents?limit=2&offset=0", nil)
	rec := httptest.NewRecorder()
	s.srv.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("bounded list status: %d %s", rec.Code, rec.Body.String())
	}
	var envelope struct {
		Value knowledge.DocumentListPage `json:"value"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Value.Total != len(titles) || len(envelope.Value.Documents) != 2 || !envelope.Value.HasMore {
		t.Fatalf("bounded list = %+v", envelope.Value)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/bases/"+base.ID+"/documents", nil)
	rec = httptest.NewRecorder()
	s.srv.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Header().Get("Deprecation") != "true" {
		t.Fatalf("legacy list status/header: %d %q", rec.Code, rec.Header().Get("Deprecation"))
	}
	var legacy struct {
		Value []knowledge.DocumentSummary `json:"value"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &legacy); err != nil {
		t.Fatal(err)
	}
	if len(legacy.Value) != 50 {
		t.Fatalf("legacy compatibility page size = %d, want 50", len(legacy.Value))
	}
}

func TestDocumentDeleteJobRemovesDocumentWithoutBlockingRequest(t *testing.T) {
	s := newTestServer(t)
	code, payload := call(t, s, "POST", "/api/bases", map[string]any{"name": "Delete job"})
	if code != http.StatusOK {
		t.Fatalf("create base: %d %v", code, payload)
	}
	baseID := valueMap(t, payload)["id"].(string)
	docID := createTextDocument(t, s, baseID, "SmartCare product dictionary.xlsx", "delete job marker")

	code, payload = call(t, s, "POST", "/api/documents/"+docID+"/delete", nil)
	if code != http.StatusAccepted {
		t.Fatalf("submit delete job: %d %v", code, payload)
	}
	jobID := valueMap(t, payload)["jobId"].(string)
	deadline := time.Now().Add(5 * time.Second)
	for {
		code, payload = call(t, s, "GET", "/api/jobs/"+jobID, nil)
		if code != http.StatusOK {
			t.Fatalf("delete job status: %d %v", code, payload)
		}
		job := valueMap(t, payload)
		if job["status"] != "done" {
			if job["status"] == "failed" {
				t.Fatalf("delete job failed: %v", job)
			}
			if time.Now().After(deadline) {
				t.Fatalf("delete job timeout: %v", job)
			}
			time.Sleep(10 * time.Millisecond)
			continue
		}
		if int(job["progress"].(float64)) != 1 || int(job["total"].(float64)) != 1 || job["phase"] != "deleting" {
			t.Fatalf("delete job result: %v", job)
		}
		break
	}
	code, payload = call(t, s, "GET", "/api/documents/"+docID+"?includeChunks=false", nil)
	if code != http.StatusNotFound {
		t.Fatalf("deleted document still available: %d %v", code, payload)
	}
}

// Per-base credentials may be written by an administrator but must never be
// readable back through the API. The flags preserve UI state without a
// round-tripped secret.
func TestBaseAPIKeysAreWriteOnly(t *testing.T) {
	s := newTestServer(t)
	request := map[string]any{
		"name": "Secret Base",
		"config": map[string]any{
			"embeddingApiKey": "embedding-secret",
			"rerankApiKey":    "rerank-secret",
			"mineruApiKey":    "mineru-secret",
		},
	}
	code, payload, raw := callRaw(t, s, "POST", "/api/bases", request)
	if code != http.StatusOK || strings.Contains(string(raw), "embedding-secret") ||
		strings.Contains(string(raw), "rerank-secret") || strings.Contains(string(raw), "mineru-secret") {
		t.Fatalf("create response leaked credentials: %d %s", code, raw)
	}
	base := valueMap(t, payload)
	baseID := base["id"].(string)
	config := base["config"].(map[string]any)
	if config["embeddingApiKeySet"] != true || config["rerankApiKeySet"] != true ||
		config["mineruApiKeySet"] != true {
		t.Fatalf("configured flags missing: %v", config)
	}

	code, payload, raw = callRaw(t, s, "GET", "/api/bases/"+baseID, nil)
	if code != http.StatusOK || strings.Contains(string(raw), "-secret") {
		t.Fatalf("base detail leaked credentials: %d %s", code, raw)
	}
	config = valueMap(t, payload)["config"].(map[string]any)
	if config["embeddingApiKeySet"] != true || config["rerankApiKeySet"] != true ||
		config["mineruApiKeySet"] != true {
		t.Fatalf("detail configured flags missing: %v", config)
	}

	code, _, raw = callRaw(t, s, "GET", "/api/bases", nil)
	if code != http.StatusOK || strings.Contains(string(raw), "-secret") {
		t.Fatalf("base list leaked credentials: %d %s", code, raw)
	}

	// A config patch that omits credentials preserves them.
	code, _ = call(t, s, "PATCH", "/api/bases/"+baseID, map[string]any{
		"config": map[string]any{"chunkSize": 123},
	})
	if code != http.StatusOK {
		t.Fatalf("preserve patch: %d", code)
	}
	_, payload = call(t, s, "GET", "/api/bases/"+baseID, nil)
	config = valueMap(t, payload)["config"].(map[string]any)
	if config["embeddingApiKeySet"] != true || int(config["chunkSize"].(float64)) != 123 {
		t.Fatalf("credential/config patch state: %v", config)
	}

	// Explicit clear requests remove credentials and are not echoed.
	code, _ = call(t, s, "PATCH", "/api/bases/"+baseID, map[string]any{
		"config": map[string]any{
			"clearEmbeddingApiKey": true,
			"clearRerankApiKey":    true,
			"clearMineruApiKey":    true,
		},
	})
	if code != http.StatusOK {
		t.Fatalf("clear patch: %d", code)
	}
	_, payload = call(t, s, "GET", "/api/bases/"+baseID, nil)
	config = valueMap(t, payload)["config"].(map[string]any)
	if config["embeddingApiKeySet"] == true || config["rerankApiKeySet"] == true ||
		config["mineruApiKeySet"] == true || config["clearEmbeddingApiKey"] == true ||
		config["clearRerankApiKey"] == true || config["clearMineruApiKey"] == true {
		t.Fatalf("credentials were not cleared safely: %v", config)
	}
}

func TestRecallSearchHistoryAPI(t *testing.T) {
	s := newTestServer(t)
	code, payload := call(t, s, "POST", "/api/bases", map[string]any{"name": "History"})
	if code != http.StatusOK {
		t.Fatalf("create base: %d %v", code, payload)
	}
	baseID := valueMap(t, payload)["id"].(string)
	createTextDocument(t, s, baseID, "Guide", "The recall history marker is QH-4171.")

	request := map[string]any{
		"query": "recall history marker", "baseId": baseID,
		"mode": "lexical", "topK": 3, "mmr": true,
	}
	code, payload = call(t, s, "POST", "/api/search", request)
	if code != http.StatusOK || valueMap(t, payload)["total"].(float64) != 1 {
		t.Fatalf("recall search: %d %v", code, payload)
	}
	code, payload = call(t, s, "GET", "/api/search-history", nil)
	if code != http.StatusOK {
		t.Fatalf("history list: %d %v", code, payload)
	}
	items, ok := payload["value"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("history items: %v", payload)
	}
	item := items[0].(map[string]any)
	if item["query"] != "recall history marker" || item["baseId"] != baseID ||
		item["mode"] != "lexical" || item["topK"].(float64) != 3 || item["mmr"] != true ||
		item["totalHits"].(float64) != 1 {
		t.Fatalf("replay fields: %v", item)
	}

	code, _ = call(t, s, "DELETE", "/api/search-history/"+item["id"].(string), nil)
	if code != http.StatusOK {
		t.Fatalf("delete history item: %d", code)
	}
	_, payload = call(t, s, "GET", "/api/search-history", nil)
	if items = payload["value"].([]any); len(items) != 0 {
		t.Fatalf("history item was not deleted: %v", items)
	}

	code, _ = call(t, s, "POST", "/api/search", request)
	if code != http.StatusOK {
		t.Fatalf("second recall search: %d", code)
	}
	code, _ = call(t, s, "DELETE", "/api/search-history", nil)
	if code != http.StatusOK {
		t.Fatalf("clear history: %d", code)
	}
	_, payload = call(t, s, "GET", "/api/search-history", nil)
	if items = payload["value"].([]any); len(items) != 0 {
		t.Fatalf("history was not cleared: %v", items)
	}
}

func TestDocumentTreeAndGroupAPI(t *testing.T) {
	s := newTestServer(t)
	source := t.TempDir()
	nested := filepath.Join(source, "nested")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "guide.md"), []byte("folder preview marker FT-7391"), 0o600); err != nil {
		t.Fatal(err)
	}

	code, payload := call(t, s, "POST", "/api/bases", map[string]any{"name": "Folders", "group": "Work"})
	if code != http.StatusOK {
		t.Fatalf("create base: %d %v", code, payload)
	}
	baseID := valueMap(t, payload)["id"].(string)
	code, payload = call(t, s, "POST", "/api/bases/"+baseID+"/import-directory", map[string]any{"path": source})
	if code != http.StatusAccepted {
		t.Fatalf("import directory: %d %v", code, payload)
	}
	jobID := valueMap(t, payload)["jobId"].(string)
	for {
		_, payload = call(t, s, "GET", "/api/jobs/"+jobID, nil)
		job := valueMap(t, payload)
		if job["status"] == "done" || job["status"] == "failed" {
			if job["status"] != "done" {
				t.Fatalf("directory import failed: %v", job)
			}
			break
		}
	}

	_, payload = call(t, s, "GET", "/api/bases/"+baseID+"/documents", nil)
	docs := payload["value"].([]any)
	if len(docs) != 3 {
		t.Fatalf("directory tree documents: %v", docs)
	}
	var rootID, fileID string
	for _, raw := range docs {
		doc := raw.(map[string]any)
		if doc["sourceType"] == "directory" && doc["parentDirectoryId"] == nil {
			rootID = doc["id"].(string)
		} else if doc["sourceType"] == "file" {
			fileID = doc["id"].(string)
			if doc["parentDirectoryId"] == rootID || doc["parentDirectoryId"] == "" {
				t.Fatalf("file parent: %v", doc)
			}
		}
	}
	code, payload = call(t, s, "GET", "/api/documents/"+fileID, nil)
	if code != http.StatusOK || !strings.Contains(payload["value"].(map[string]any)["chunks"].([]any)[0].(map[string]any)["text"].(string), "FT-7391") {
		t.Fatalf("chunk preview API: %d %v", code, payload)
	}

	code, _ = call(t, s, "POST", "/api/groups", map[string]any{"name": "Personal"})
	if code != http.StatusOK {
		t.Fatalf("create group: %d", code)
	}
	code, payload = call(t, s, "PATCH", "/api/groups", map[string]any{"from": "Work", "to": "Job"})
	if code != http.StatusOK || !strings.Contains(fmt.Sprint(payload["value"]), "Job") {
		t.Fatalf("rename group: %d %v", code, payload)
	}
	code, _ = call(t, s, "DELETE", "/api/groups", map[string]any{"name": "Job"})
	if code != http.StatusOK {
		t.Fatalf("delete group: %d", code)
	}

	code, payload = call(t, s, "POST", "/api/documents/"+rootID+"/delete-tree-job", nil)
	if code != http.StatusAccepted {
		t.Fatalf("submit delete document tree job: %d %v", code, payload)
	}
	deleteJobID := valueMap(t, payload)["jobId"].(string)
	deadline := time.Now().Add(5 * time.Second)
	for {
		code, payload = call(t, s, "GET", "/api/jobs/"+deleteJobID, nil)
		if code != http.StatusOK {
			t.Fatalf("delete tree job status: %d %v", code, payload)
		}
		job := valueMap(t, payload)
		if job["status"] != "done" {
			if job["status"] == "failed" {
				t.Fatalf("delete tree job failed: %v", job)
			}
			if time.Now().After(deadline) {
				t.Fatalf("delete tree job timeout: %v", job)
			}
			time.Sleep(10 * time.Millisecond)
			continue
		}
		if int(job["progress"].(float64)) != 3 || int(job["total"].(float64)) != 3 || job["phase"] != "deleting" {
			t.Fatalf("delete tree job result: %v", job)
		}
		break
	}
	_, payload = call(t, s, "GET", "/api/bases/"+baseID+"/documents", nil)
	if len(payload["value"].([]any)) != 0 {
		t.Fatalf("tree was not deleted: %v", payload)
	}
}

type fakeRerankRuntime struct {
	request map[string]any
	scores  []float64
	health  runtime.Health
}

func (f *fakeRerankRuntime) Configured(capability string) bool {
	return capability == runtime.CapabilityRerank
}

func (f *fakeRerankRuntime) Call(_ context.Context, capability string, params, out any) error {
	if capability != runtime.CapabilityRerank {
		return &runtime.Error{Code: "unsupported", Message: capability}
	}
	f.request = params.(map[string]any)
	target := out.(*struct {
		Scores []float64 `json:"scores"`
	})
	target.Scores = f.scores
	return nil
}

func (f *fakeRerankRuntime) Probe(_ context.Context, capability string) (runtime.Health, error) {
	return runtime.Health{Capability: capability, Ready: f.Configured(capability)}, nil
}

func (f *fakeRerankRuntime) Status(_ context.Context) map[string]runtime.Health {
	if f.health.Model != "" {
		return map[string]runtime.Health{runtime.CapabilityRerank: f.health}
	}
	return map[string]runtime.Health{runtime.CapabilityRerank: {Capability: runtime.CapabilityRerank, Ready: true}}
}

func (f *fakeRerankRuntime) Close() {}

type fakeManagedModelRuntime struct {
	health  runtime.Health
	removed bool
}

func (f *fakeManagedModelRuntime) Configured(capability string) bool {
	return capability == runtime.CapabilityEmbedding
}

func (f *fakeManagedModelRuntime) Call(_ context.Context, capability string, _ any, _ any) error {
	return &runtime.Error{Code: "unsupported", Message: capability}
}

func (f *fakeManagedModelRuntime) Probe(_ context.Context, capability string) (runtime.Health, error) {
	if capability != runtime.CapabilityEmbedding {
		return runtime.Health{Capability: capability}, nil
	}
	return f.health, nil
}

func (f *fakeManagedModelRuntime) Status(_ context.Context) map[string]runtime.Health {
	return map[string]runtime.Health{runtime.CapabilityEmbedding: f.health}
}

func (f *fakeManagedModelRuntime) Close() {}

func (f *fakeManagedModelRuntime) LoadModel(_ context.Context, _ string, _ string) (runtime.Health, error) {
	return f.health, nil
}

func (f *fakeManagedModelRuntime) RemoveModel(_ context.Context, _ string, _ string) error {
	f.removed = true
	return nil
}

func TestManagedRuntimeModelAppearsInLocalModelList(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SHUTU_KNOWLEDGE_HOME", home)
	application, err := app.New(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(application.Close)
	modelID := "onnx-community/Qwen3-Embedding-0.6B-ONNX"
	application.Config.Embedding.Model = modelID
	application.Runtime = &fakeManagedModelRuntime{health: runtime.Health{
		Capability: runtime.CapabilityEmbedding, Model: modelID,
		Status: "installed", Lifecycle: "INSTALLED", Version: "transformers.js/onnxruntime-node",
	}}
	application.ManagedRuntime = true
	s := New(application)

	_, payload := call(t, s, "GET", "/api/local-models", nil)
	items := valueMap(t, payload)["models"].([]any)
	if len(items) != 1 {
		t.Fatalf("managed model list: %v", items)
	}
	item := items[0].(map[string]any)
	if item["id"] != modelID || item["status"] != "installed" || item["lifecycle"] != "INSTALLED" {
		t.Fatalf("managed model state: %v", item)
	}
	code, payload := call(t, s, "GET", "/api/local-models?limit=1&offset=0", nil)
	if code != http.StatusOK {
		t.Fatalf("paged managed model list: %d %v", code, payload)
	}
	paged := valueMap(t, payload)
	if paged["hasMore"] != false || paged["nextOffset"] != float64(0) {
		t.Fatalf("paged managed model metadata: %v", paged)
	}

	code, payload = call(t, s, "POST", "/api/local-models/remove", map[string]any{"id": modelID})
	if code != http.StatusAccepted {
		t.Fatalf("remove managed model: %d %v", code, payload)
	}
	op := waitOperation(t, s, valueMap(t, payload)["jobId"].(string))
	if op.State != operations.StateSucceeded {
		t.Fatalf("remove managed model operation: %+v", op)
	}
	if !application.Runtime.(*fakeManagedModelRuntime).removed {
		t.Fatal("managed runtime remove was not called")
	}
}

func TestBaseConfiguredManagedModelAppearsAsInstalledNotLoaded(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SHUTU_KNOWLEDGE_HOME", home)
	application, err := app.New(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(application.Close)
	modelID := "onnx-community/base-embedding"
	base, err := application.Knowledge.CreateBase("Base override", "", "", knowledge.BaseConfig{
		EmbeddingProvider: "local",
		EmbeddingModel:    modelID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(application.Models.Root(), filepath.FromSlash(modelID)), 0o700); err != nil {
		t.Fatal(err)
	}
	application.Runtime = &fakeManagedModelRuntime{}
	application.ManagedRuntime = true

	s := New(application)
	_, payload := call(t, s, "GET", "/api/local-models?baseId="+base.ID, nil)
	items := valueMap(t, payload)["models"].([]any)
	if len(items) != 1 {
		t.Fatalf("base model list: %v", items)
	}
	item := items[0].(map[string]any)
	if item["id"] != modelID || item["kind"] != models.KindEmbedding || item["status"] != "installed" || item["lifecycle"] != models.LifecycleInstalled || item["ready"] != false {
		t.Fatalf("base model state: %v", item)
	}
}

func TestCustomRerankerRegistrationAndSelfTest(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SHUTU_KNOWLEDGE_HOME", home)
	application, err := app.New(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(application.Close)
	runtimeStub := &fakeRerankRuntime{scores: []float64{0.91, 0.08}}
	application.Runtime = runtimeStub
	s := New(application)

	code, payload := call(t, s, "POST", "/api/local-rerankers", map[string]any{"id": "owner/custom-reranker"})
	if code != http.StatusOK {
		t.Fatalf("register reranker: %d %v", code, payload)
	}
	if valueMap(t, payload)["id"] != "owner/custom-reranker" {
		t.Fatalf("registration response: %v", payload)
	}
	code, _ = call(t, s, "POST", "/api/local-rerankers", map[string]any{"id": "../escape"})
	if code != http.StatusBadRequest {
		t.Fatalf("invalid reranker id should fail: %d", code)
	}

	modelDir := filepath.Join(application.Models.Root(), "owner", "custom-reranker")
	if err := os.MkdirAll(modelDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(modelDir, "model.onnx"), []byte("weights"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := `{"id":"owner/custom-reranker","kind":"rerank","artifacts":["model.onnx"],"downloaded":123}`
	if err := os.WriteFile(filepath.Join(modelDir, "manifest.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}

	code, payload = call(t, s, "POST", "/api/local-models/self-test", map[string]any{"id": "local:owner/custom-reranker"})
	if code != http.StatusAccepted {
		t.Fatalf("self-test: %d %v", code, payload)
	}
	jobID := valueMap(t, payload)["jobId"].(string)
	deadline := time.Now().Add(5 * time.Second)
	for {
		op, err := application.Operations.Get(jobID)
		if err != nil {
			t.Fatal(err)
		}
		if op.State == operations.StateSucceeded {
			if op.CompletedUnits != 100 || op.Phase != "ready" {
				t.Fatalf("self-test operation result: %+v", op)
			}
			break
		}
		if op.State == operations.StateFailed {
			t.Fatalf("self-test operation failed: %+v", op)
		}
		if time.Now().After(deadline) {
			t.Fatalf("self-test operation timeout: %+v", op)
		}
		time.Sleep(10 * time.Millisecond)
	}
	documents, ok := runtimeStub.request["documents"].([]string)
	if runtimeStub.request["model"] != "owner/custom-reranker" || !ok || len(documents) != 2 {
		t.Fatalf("helper request: %v", runtimeStub.request)
	}

	_, payload = call(t, s, "GET", "/api/local-models", nil)
	models := valueMap(t, payload)["models"].([]any)
	if len(models) != 1 {
		t.Fatalf("model list: %v", models)
	}
	model := models[0].(map[string]any)
	selfTest := model["selfTest"].(map[string]any)
	if model["status"] != "installed" || model["lifecycle"] != "INSTALLED" || model["ready"] != false || model["runtimeStatus"] != "RUNTIME_MISSING" || selfTest["current"] != true || selfTest["healthy"] != true {
		t.Fatalf("model/self-test state: %v", model)
	}

	code, payload = call(t, s, "POST", "/api/local-models/remove", map[string]any{"id": "owner/custom-reranker"})
	if code != http.StatusAccepted {
		t.Fatalf("remove downloaded custom reranker: %d %v", code, payload)
	}
	if op := waitOperation(t, s, valueMap(t, payload)["jobId"].(string)); op.State != operations.StateSucceeded {
		t.Fatalf("remove downloaded custom reranker operation: %+v", op)
	}
	_, payload = call(t, s, "GET", "/api/local-models", nil)
	if models := valueMap(t, payload)["models"].([]any); len(models) != 0 {
		t.Fatalf("custom reranker artifacts remain: %v", models)
	}

	code, _ = call(t, s, "POST", "/api/local-rerankers", map[string]any{"id": "owner/missing-reranker"})
	if code != http.StatusOK {
		t.Fatalf("register missing reranker: %d", code)
	}
	code, payload = call(t, s, "POST", "/api/local-models/remove", map[string]any{"id": "owner/missing-reranker"})
	if code != http.StatusAccepted {
		t.Fatalf("unregister missing reranker: %d %v", code, payload)
	}
	if op := waitOperation(t, s, valueMap(t, payload)["jobId"].(string)); op.State != operations.StateSucceeded {
		t.Fatalf("unregister missing reranker operation: %+v", op)
	}
	_, payload = call(t, s, "GET", "/api/local-models", nil)
	if models := valueMap(t, payload)["models"].([]any); len(models) != 0 {
		t.Fatalf("registered reranker was not unregistered: %v", models)
	}
}

func TestManagedRerankerSelfTestAcceptsRuntimeCache(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SHUTU_KNOWLEDGE_HOME", home)
	application, err := app.New(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(application.Close)
	modelID := "Xenova/bge-reranker-base"
	runtimeStub := &fakeRerankRuntime{
		scores: []float64{0.91, 0.08},
		health: runtime.Health{
			Capability: runtime.CapabilityRerank, Model: modelID,
			Status: "installed", Lifecycle: "INSTALLED",
			Version: "transformers.js/onnxruntime-node",
		},
	}
	application.Runtime = runtimeStub
	application.ManagedRuntime = true

	result, err := application.SelfTestReranker(context.Background(), "local:"+modelID)
	if err != nil {
		t.Fatalf("managed self-test: %v", err)
	}
	if !result.Healthy || !result.Current || runtimeStub.request["model"] != modelID {
		t.Fatalf("managed self-test result: %v request=%v", result, runtimeStub.request)
	}
	views, err := application.ListLocalModels()
	if err != nil || len(views) != 1 || views[0].SelfTest == nil || !views[0].SelfTest.Current {
		t.Fatalf("managed self-test state: %+v err=%v", views, err)
	}
}

func TestModelCacheMigrationAPI(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SHUTU_KNOWLEDGE_HOME", home)
	application, err := app.New(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(application.Close)
	s := New(application)

	modelDir := filepath.Join(application.Models.Root(), "example", "model")
	if err := os.MkdirAll(modelDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(modelDir, "model.onnx"), []byte("weights"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := `{"id":"example/model","kind":"embedding","artifacts":["model.onnx"],"status":"ready"}`
	if err := os.WriteFile(filepath.Join(modelDir, "manifest.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "migrated-models")

	code, payload, raw := callRaw(t, s, "GET",
		"/api/local-models/cache-migration?targetDir="+filepath.ToSlash(target), nil)
	if code != http.StatusAccepted || strings.Contains(string(raw), "weights") {
		t.Fatalf("migration plan: %d %s", code, raw)
	}
	planOperationID := valueMap(t, payload)["operationId"].(string)
	planOperation := waitOperation(t, s, planOperationID)
	if planOperation.State != operations.StateSucceeded || strings.Contains(string(planOperation.Result), "weights") {
		t.Fatalf("migration plan operation: %+v", planOperation)
	}
	var plan models.MigrationPlan
	if err := json.Unmarshal(planOperation.Result, &plan); err != nil {
		t.Fatalf("decode migration plan: %v", err)
	}
	if len(plan.Models) != 1 || plan.TargetDir != filepath.Clean(target) {
		t.Fatalf("migration plan value: %+v", plan)
	}

	code, payload = call(t, s, "POST", "/api/local-models/cache-migration", map[string]any{
		"targetDir": target, "removeSource": true,
	})
	if code != http.StatusAccepted {
		t.Fatalf("migrate cache: %d %v", code, payload)
	}
	migrationID := valueMap(t, payload)["jobId"].(string)
	deadline := time.Now().Add(5 * time.Second)
	var migrationResult struct {
		ModelCount   int  `json:"modelCount"`
		SourceRemove bool `json:"sourceRemoved"`
	}
	for {
		op, err := application.Operations.Get(migrationID)
		if err != nil {
			t.Fatal(err)
		}
		if op.State == operations.StateSucceeded {
			if err := json.Unmarshal(op.Result, &migrationResult); err != nil {
				t.Fatalf("decode migration result %s: %v", op.Result, err)
			}
			break
		}
		if op.State == operations.StateFailed {
			t.Fatalf("migration operation failed: %+v", op)
		}
		if time.Now().After(deadline) {
			t.Fatalf("migration operation timeout: %+v", op)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if migrationResult.ModelCount != 1 || migrationResult.SourceRemove != true {
		t.Fatalf("migration result: %+v", migrationResult)
	}
	if application.Config.Models.CacheDir != filepath.Clean(target) || application.Models.Root() != filepath.Clean(target) {
		t.Fatalf("migration was not activated: config=%q root=%q",
			application.Config.Models.CacheDir, application.Models.Root())
	}

	_, payload = call(t, s, "GET", "/api/local-models", nil)
	cache := valueMap(t, payload)
	if cache["cacheDir"] != filepath.Clean(target) || len(cache["models"].([]any)) != 1 {
		t.Fatalf("migrated model list: %v", cache)
	}

	code, payload = call(t, s, "POST", "/api/local-models/cache-migration", map[string]any{
		"targetDir": filepath.Join(target, "nested"), "removeSource": false,
	})
	if code != http.StatusAccepted {
		t.Fatalf("nested target should be deferred to operation: %d %v", code, payload)
	}
	if op := waitOperation(t, s, valueMap(t, payload)["jobId"].(string)); op.State != operations.StateFailed {
		t.Fatalf("nested target operation should fail: %+v", op)
	}
}

func TestOCRModelAPIStatusAndRemove(t *testing.T) {
	s := newTestServer(t)

	_, payload := call(t, s, "GET", "/api/ocr/model", nil)
	status := valueMap(t, payload)
	if status["status"] != "not-downloaded" || status["id"] != models.OCRModelID {
		t.Fatalf("initial OCR model: %v", status)
	}

	modelDir := filepath.Join(s.app.Models.Root(), "PaddlePaddle", "PP-OCRv5-mobile")
	if err := os.MkdirAll(modelDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, artifact := range []string{"ppocrv5_det.onnx", "ppocrv5_rec.onnx", "ppocrv5_dict.txt"} {
		if err := os.WriteFile(filepath.Join(modelDir, artifact), []byte("artifact"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	manifest := `{"id":"PaddlePaddle/PP-OCRv5-mobile","kind":"ocr","artifacts":["ppocrv5_det.onnx","ppocrv5_rec.onnx","ppocrv5_dict.txt"],"status":"ready"}`
	if err := os.WriteFile(filepath.Join(modelDir, "manifest.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}

	_, payload = call(t, s, "GET", "/api/ocr/model", nil)
	status = valueMap(t, payload)
	if status["status"] != "installed" || status["lifecycle"] != "INSTALLED" || status["ready"] != false || status["runtimeStatus"] != "RUNTIME_MISSING" || status["missing"] != nil {
		t.Fatalf("downloaded OCR model: %v", status)
	}

	code, payload := call(t, s, "POST", "/api/ocr/model/remove", nil)
	if code != http.StatusAccepted {
		t.Fatalf("remove OCR model: %d %v", code, payload)
	}
	if op := waitOperation(t, s, valueMap(t, payload)["jobId"].(string)); op.State != operations.StateSucceeded {
		t.Fatalf("remove OCR model operation: %+v", op)
	}
	_, payload = call(t, s, "GET", "/api/ocr/model", nil)
	if valueMap(t, payload)["status"] != "not-downloaded" {
		t.Fatalf("OCR model was not removed: %v", payload)
	}
}

func TestOCRModelDownloadHonorsRequestCancellation(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/ocr/model/download", nil)
	ctx, cancel := context.WithCancel(req.Context())
	cancel()
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	s.srv.Handler.ServeHTTP(rec, req)
	if rec.Code == http.StatusAccepted {
		t.Fatalf("cancelled OCR download request was accepted: %s", rec.Body.String())
	}
	var count int
	if err := s.app.DB.QueryRow(`SELECT COUNT(*) FROM operations WHERE type = 'download_ocr_model'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("cancelled OCR download created %d durable operations", count)
	}
}

func TestOllamaModelRemoveUsesDurableOperation(t *testing.T) {
	s := newTestServer(t)
	requested := make(chan string, 1)
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/api/delete" {
			http.Error(w, "unexpected Ollama request", http.StatusNotFound)
			return
		}
		var body struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		requested <- body.Model
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(ollama.Close)
	s.app.Ollama = models.NewOllama(ollama.URL, ollama.Client())

	code, payload := call(t, s, "POST", "/api/ollama/delete", map[string]any{"model": "nomic-embed-text"})
	if code != http.StatusAccepted {
		t.Fatalf("delete Ollama model: %d %v", code, payload)
	}
	op := waitOperation(t, s, valueMap(t, payload)["jobId"].(string))
	if op.State != operations.StateSucceeded {
		t.Fatalf("delete Ollama operation: %+v", op)
	}
	select {
	case model := <-requested:
		if model != "nomic-embed-text" {
			t.Fatalf("deleted Ollama model: %q", model)
		}
	case <-time.After(time.Second):
		t.Fatal("Ollama delete request was not sent")
	}
}

func TestAddFilesConflictRoundTrip(t *testing.T) {
	s := newTestServer(t)
	_, payload := call(t, s, "POST", "/api/bases", map[string]any{"name": "Files"})
	baseID := valueMap(t, payload)["id"].(string)

	content := base64Of("# Doc")
	files := []map[string]any{{"fileName": "a.md", "contentBase64": content}}
	result := addLegacyFiles(t, s, baseID, files, "rename")
	if result.Accepted[0].Skipped {
		t.Fatalf("first import skipped: %+v", result)
	}
	// Same content again is skipped (dedup).
	result = addLegacyFiles(t, s, baseID, files, "rename")
	if !result.Accepted[0].Skipped {
		t.Fatalf("dedup round did not skip: %+v", result)
	}
	// detect now collides on the title imported in round one.
	_, _ = call(t, s, "POST", "/api/bases", map[string]any{"name": "Files2"})
}

func TestGlobalConfigRoundTripProtectsSecrets(t *testing.T) {
	s := newTestServer(t)
	code, payload := call(t, s, "GET", "/api/config", nil)
	if code != http.StatusOK {
		t.Fatalf("get config: %d %v", code, payload)
	}
	view := valueMap(t, payload)
	configMap := view["config"].(map[string]any)
	if _, exists := configMap["embedding"].(map[string]any)["apiKey"]; exists {
		t.Fatal("embedding API key must not be returned")
	}
	if _, exists := configMap["processing"].(map[string]any)["apiKey"]; exists {
		t.Fatal("MinerU API key must not be returned")
	}
	if _, exists := configMap["captioning"].(map[string]any)["apiKey"]; exists {
		t.Fatal("caption API key must not be returned")
	}

	configMap["embedding"].(map[string]any)["provider"] = "openai"
	configMap["embedding"].(map[string]any)["model"] = "text-embedding-3-small"
	configMap["chunking"].(map[string]any)["size"] = 921
	configMap["retrieval"].(map[string]any)["topK"] = 7
	configMap["processing"].(map[string]any)["provider"] = "mineru"
	configMap["processing"].(map[string]any)["apiHost"] = "https://mineru.example"
	configMap["workflow"].(map[string]any)["conflictStrategy"] = "replace"
	configMap["workflow"].(map[string]any)["urlRefreshHours"] = 48
	configMap["captioning"].(map[string]any)["provider"] = "openai"
	configMap["captioning"].(map[string]any)["model"] = "vision-model"
	configMap["captioning"].(map[string]any)["baseUrl"] = "https://vision.example"
	code, payload = call(t, s, "PUT", "/api/config", map[string]any{
		"config": configMap, "embeddingApiKey": "secret-value", "mineruApiKey": "mineru-secret",
		"captionApiKey": "caption-secret",
	})
	if code != http.StatusOK {
		t.Fatalf("put config: %d %v", code, payload)
	}

	code, payload = call(t, s, "GET", "/api/config", nil)
	view = valueMap(t, payload)
	configMap = view["config"].(map[string]any)
	if _, exists := configMap["embedding"].(map[string]any)["apiKey"]; exists {
		t.Fatal("saved embedding API key must not be returned")
	}
	if _, exists := configMap["processing"].(map[string]any)["apiKey"]; exists {
		t.Fatal("saved MinerU API key must not be returned")
	}
	if _, exists := configMap["captioning"].(map[string]any)["apiKey"]; exists {
		t.Fatal("saved caption API key must not be returned")
	}
	if view["embeddingApiKeySet"] != true || configMap["embedding"].(map[string]any)["model"] != "text-embedding-3-small" {
		t.Fatalf("config state: %v", view)
	}
	if view["mineruApiKeySet"] != true || configMap["processing"].(map[string]any)["provider"] != "mineru" ||
		configMap["processing"].(map[string]any)["apiHost"] != "https://mineru.example" {
		t.Fatalf("processing config state: %v", view)
	}
	if configMap["workflow"].(map[string]any)["conflictStrategy"] != "replace" ||
		int(configMap["workflow"].(map[string]any)["urlRefreshHours"].(float64)) != 48 {
		t.Fatalf("workflow config was not persisted: %v", configMap["workflow"])
	}
	if view["captionApiKeySet"] != true || configMap["captioning"].(map[string]any)["model"] != "vision-model" {
		t.Fatalf("caption config state: %v", view)
	}
	if int(configMap["chunking"].(map[string]any)["size"].(float64)) != 921 {
		t.Fatalf("chunk size was not persisted: %v", configMap["chunking"])
	}
	// The service applies the config immediately; providers use the new key
	// without exposing it back through the API.
	svc := s.Service()
	if got := svc.GlobalConfig().Embedding.APIKey; got != "secret-value" {
		t.Fatal("runtime API key was not applied")
	}
	if got := svc.GlobalConfig().Processing.APIKey; got != "mineru-secret" {
		t.Fatal("MinerU API key was not applied")
	}
	if got := svc.GlobalConfig().Captioning.APIKey; got != "caption-secret" {
		t.Fatal("caption API key was not applied")
	}
}

func TestRawRestoreProbeAndIndexingAPI(t *testing.T) {
	s := newTestServer(t)
	code, payload := call(t, s, "POST", "/api/bases", map[string]any{"name": "Source"})
	if code != http.StatusOK {
		t.Fatalf("create source: %d %v", code, payload)
	}
	source := valueMap(t, payload)
	sourceID := source["id"].(string)
	files := []map[string]any{{"fileName": "source.txt", "contentBase64": base64Of("retry budget is thirty seconds")}}
	result := addLegacyFiles(t, s, sourceID, files, "rename")
	docID := result.Accepted[0].ID
	published, _, err := s.app.Knowledge.GetDocument(docID, false)
	if err != nil {
		t.Fatal(err)
	}
	citation := fmt.Sprintf("indexGeneration=%d&sourceVersion=%d",
		published.ActiveIndexGen, published.SourceVersion)

	// Raw source is a binary response, not the normal JSON envelope.
	req := httptest.NewRequest("GET", "/api/documents/"+docID+"/raw", nil)
	rec := httptest.NewRecorder()
	s.srv.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "retry budget is thirty seconds" ||
		rec.Header().Get("Content-Disposition") == "" {
		t.Fatalf("raw download: code=%d headers=%v body=%q", rec.Code, rec.Header(), rec.Body.String())
	}
	if rec.Header().Get("X-Index-Generation") != strconv.Itoa(int(published.ActiveIndexGen)) ||
		rec.Header().Get("X-Source-Version") != strconv.Itoa(int(published.SourceVersion)) {
		t.Fatalf("raw citation headers: %v", rec.Header())
	}
	req = httptest.NewRequest("GET", "/api/documents/"+docID+"/raw?"+citation, nil)
	rec = httptest.NewRecorder()
	s.srv.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "retry budget is thirty seconds" {
		t.Fatalf("pinned raw download: code=%d body=%q", rec.Code, rec.Body.String())
	}
	if _, err := s.app.Knowledge.ReindexDocument(context.Background(), docID); err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest("GET", "/api/documents/"+docID+"/raw?"+citation, nil)
	rec = httptest.NewRecorder()
	s.srv.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "retry budget is thirty seconds" ||
		rec.Header().Get("X-Index-Generation") != strconv.Itoa(int(published.ActiveIndexGen)) ||
		rec.Header().Get("X-Source-Version") != strconv.Itoa(int(published.SourceVersion)) {
		t.Fatalf("retained historical raw citation = %d %q headers=%v", rec.Code, rec.Body.String(), rec.Header())
	}
	current, _, err := s.app.Knowledge.GetDocument(docID, false)
	if err != nil {
		t.Fatal(err)
	}
	if current.ActiveIndexGen != published.ActiveIndexGen+1 || current.SourceVersion != published.SourceVersion+1 {
		t.Fatalf("reindex did not advance citation identity: before=%+v after=%+v", published, current)
	}
	req = httptest.NewRequest("GET", "/api/documents/"+docID+"/raw?inline=1", nil)
	rec = httptest.NewRecorder()
	s.srv.Handler.ServeHTTP(rec, req)
	if disposition := rec.Header().Get("Content-Disposition"); rec.Code != http.StatusOK || !strings.HasPrefix(disposition, "inline;") {
		t.Fatalf("raw inline: code=%d disposition=%q", rec.Code, disposition)
	}

	code, payload = call(t, s, "POST", "/api/bases/"+sourceID+"/restore", map[string]any{"name": "Restored"})
	if code != http.StatusAccepted {
		t.Fatalf("restore accepted: %d %v", code, payload)
	}
	restoreOperationID := valueMap(t, payload)["operationId"].(string)
	deadline := time.Now().Add(8 * time.Second)
	var restoreOperation operations.Operation
	for {
		restoreOperation, err = s.app.Operations.Get(restoreOperationID)
		if err != nil {
			t.Fatal(err)
		}
		if restoreOperation.State == operations.StateSucceeded {
			break
		}
		if restoreOperation.State == operations.StateFailed || restoreOperation.State == operations.StateCancelled {
			t.Fatalf("restore operation ended %s: %s", restoreOperation.State, restoreOperation.ErrorMessage)
		}
		if time.Now().After(deadline) {
			t.Fatalf("restore operation state = %s", restoreOperation.State)
		}
		time.Sleep(10 * time.Millisecond)
	}
	var restored map[string]any
	if err := json.Unmarshal(restoreOperation.Result, &restored); err != nil {
		t.Fatalf("decode restore result: %v", err)
	}
	restoredID := restored["id"].(string)
	if restoredID == sourceID || restored["name"] != "Restored" {
		t.Fatalf("restore base: %v", restored)
	}
	_, payload = call(t, s, "GET", "/api/bases/"+restoredID+"/documents", nil)
	docs := payload["value"].([]any)
	if len(docs) != 1 || docs[0].(map[string]any)["status"] != "ready" {
		t.Fatalf("restored documents: %v", docs)
	}
	items, err := s.app.Operations.ListItems(context.Background(), restoreOperationID, 10)
	if err != nil || len(items) != 1 || items[0].ItemKey != docID || !items[0].Committed {
		t.Fatalf("restore item markers: %+v, err=%v", items, err)
	}
	code, payload = call(t, s, "POST", "/api/search", map[string]any{"query": "retry budget", "baseId": restoredID})
	if code != http.StatusOK || valueMap(t, payload)["total"].(float64) != 1 {
		t.Fatalf("restored search: %d %v", code, payload)
	}

	code, payload = call(t, s, "GET", "/api/metrics", nil)
	if code != http.StatusOK {
		t.Fatalf("metrics: %d %v", code, payload)
	}
	metrics := valueMap(t, payload)
	if int(metrics["imports"].(float64)) != 3 || int(metrics["searches"].(float64)) != 1 ||
		int(metrics["contextCount"].(float64)) != 1 || int(metrics["candidateCount"].(float64)) < 1 {
		t.Fatalf("metrics: %v", metrics)
	}

	// The default test service has no embedding provider; this route must
	// fail explicitly rather than silently reporting zero dimensions.
	code, payload = call(t, s, "POST", "/api/probe-embedding-dimensions", map[string]any{})
	if code != http.StatusBadRequest {
		t.Fatalf("probe absent provider: %d %v", code, payload)
	}
	code, payload = call(t, s, "GET", "/api/indexing-status", nil)
	if code != http.StatusOK {
		t.Fatalf("indexing status: %d %v", code, payload)
	}
	if values, ok := payload["value"].([]any); !ok || len(values) != 0 {
		t.Fatalf("indexing status must be an array: %v", payload["value"])
	}
}

func TestRuntimeStatusExposesOnlyConfiguredHelpers(t *testing.T) {
	// Keep this API contract test hermetic. Managed runtime behavior is covered
	// by internal/runtime tests and the explicit real-runtime smoke suite.
	t.Setenv("SHUTU_KNOWLEDGE_DISABLE_MANAGED_RUNTIME", "1")
	s := newTestServer(t)
	code, payload := call(t, s, "GET", "/api/runtime-status", nil)
	if code != http.StatusOK {
		t.Fatalf("runtime status: %d %v", code, payload)
	}
	view := valueMap(t, payload)
	status := view["status"].(map[string]any)
	if len(status) != 0 {
		t.Fatalf("default deployment must not report phantom runtimes: %v", status)
	}
}

func base64Of(text string) string {
	return base64.StdEncoding.EncodeToString([]byte(text))
}
