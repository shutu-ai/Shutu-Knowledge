package web

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shutu-ai/shutu-knowledge/internal/app"
	"github.com/shutu-ai/shutu-knowledge/internal/models"
	"github.com/shutu-ai/shutu-knowledge/internal/runtime"
	"os"
	"path/filepath"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	t.Setenv("SHUTU_KNOWLEDGE_HOME", t.TempDir())
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
	code, payload = call(t, s, "POST", "/api/bases/"+baseID+"/documents", map[string]any{
		"title":   "Note",
		"content": "# Head\n\nsome body text",
	})
	if code != http.StatusOK {
		t.Fatalf("add doc: %d %v", code, payload)
	}
	doc := valueMap(t, payload)
	docID := doc["id"].(string)
	if doc["status"] != "ready" {
		t.Fatalf("doc not ready: %v", doc)
	}

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
	if code != http.StatusOK {
		t.Fatalf("delete: %d", code)
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
	code, _ = call(t, s, "POST", "/api/bases/"+baseID+"/documents", map[string]any{
		"title": "Guide", "content": "The recall history marker is QH-4171.",
	})
	if code != http.StatusOK {
		t.Fatalf("add document: %d", code)
	}

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
	if code != http.StatusOK {
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

	code, payload = call(t, s, "POST", "/api/documents/"+rootID+"/delete-tree", nil)
	if code != http.StatusOK || valueMap(t, payload)["deleted"].(float64) != 3 {
		t.Fatalf("delete document tree: %d %v", code, payload)
	}
	_, payload = call(t, s, "GET", "/api/bases/"+baseID+"/documents", nil)
	if len(payload["value"].([]any)) != 0 {
		t.Fatalf("tree was not deleted: %v", payload)
	}
}

type fakeRerankRuntime struct {
	request map[string]any
	scores  []float64
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
	return map[string]runtime.Health{runtime.CapabilityRerank: {Capability: runtime.CapabilityRerank, Ready: true}}
}

func (f *fakeRerankRuntime) Close() {}

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
	if code != http.StatusOK {
		t.Fatalf("self-test: %d %v", code, payload)
	}
	result := valueMap(t, payload)
	if result["healthy"] != true || result["current"] != true || result["artifactCount"].(float64) != 1 {
		t.Fatalf("self-test result: %v", result)
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
	if model["status"] != "ready" || selfTest["current"] != true || selfTest["healthy"] != true {
		t.Fatalf("model/self-test state: %v", model)
	}

	code, _ = call(t, s, "POST", "/api/local-models/remove", map[string]any{"id": "owner/custom-reranker"})
	if code != http.StatusOK {
		t.Fatalf("remove downloaded custom reranker: %d", code)
	}
	_, payload = call(t, s, "GET", "/api/local-models", nil)
	if models := valueMap(t, payload)["models"].([]any); len(models) != 0 {
		t.Fatalf("custom reranker artifacts remain: %v", models)
	}

	code, _ = call(t, s, "POST", "/api/local-rerankers", map[string]any{"id": "owner/missing-reranker"})
	if code != http.StatusOK {
		t.Fatalf("register missing reranker: %d", code)
	}
	code, _ = call(t, s, "POST", "/api/local-models/remove", map[string]any{"id": "owner/missing-reranker"})
	if code != http.StatusOK {
		t.Fatalf("unregister missing reranker: %d", code)
	}
	_, payload = call(t, s, "GET", "/api/local-models", nil)
	if models := valueMap(t, payload)["models"].([]any); len(models) != 0 {
		t.Fatalf("registered reranker was not unregistered: %v", models)
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
	if code != http.StatusOK || strings.Contains(string(raw), "weights") {
		t.Fatalf("migration plan: %d %s", code, raw)
	}
	plan := valueMap(t, payload)
	if len(plan["models"].([]any)) != 1 || plan["targetDir"] != filepath.Clean(target) {
		t.Fatalf("migration plan value: %v", plan)
	}

	code, payload = call(t, s, "POST", "/api/local-models/cache-migration", map[string]any{
		"targetDir": target, "removeSource": true,
	})
	if code != http.StatusOK {
		t.Fatalf("migrate cache: %d %v", code, payload)
	}
	result := valueMap(t, payload)
	if result["modelCount"].(float64) != 1 || result["sourceRemoved"] != true {
		t.Fatalf("migration result: %v", result)
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

	code, _ = call(t, s, "POST", "/api/local-models/cache-migration", map[string]any{
		"targetDir": home, "removeSource": false,
	})
	if code != http.StatusBadRequest {
		t.Fatalf("nested target should fail: %d", code)
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
	if status["status"] != "ready" || status["missing"] != nil {
		t.Fatalf("downloaded OCR model: %v", status)
	}

	code, payload := call(t, s, "POST", "/api/ocr/model/remove", nil)
	if code != http.StatusOK || valueMap(t, payload)["removed"] != true {
		t.Fatalf("remove OCR model: %d %v", code, payload)
	}
	_, payload = call(t, s, "GET", "/api/ocr/model", nil)
	if valueMap(t, payload)["status"] != "not-downloaded" {
		t.Fatalf("OCR model was not removed: %v", payload)
	}
}

func TestAddFilesConflictRoundTrip(t *testing.T) {
	s := newTestServer(t)
	_, payload := call(t, s, "POST", "/api/bases", map[string]any{"name": "Files"})
	baseID := valueMap(t, payload)["id"].(string)

	content := base64Of("# Doc")
	files := []map[string]any{{"fileName": "a.md", "contentBase64": content}}
	code, payload := call(t, s, "POST", "/api/bases/"+baseID+"/files", map[string]any{"files": files, "conflict": "rename"})
	if code != http.StatusOK {
		t.Fatalf("add files: %d %v", code, payload)
	}
	// Same content again is skipped (dedup).
	code, payload = call(t, s, "POST", "/api/bases/"+baseID+"/files", map[string]any{"files": files, "conflict": "rename"})
	accepted := payload["value"].(map[string]any)["accepted"].([]any)
	if code != http.StatusOK || !accepted[0].(map[string]any)["skipped"].(bool) {
		t.Fatalf("dedup round: %d %v", code, payload)
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
	_, payload = call(t, s, "POST", "/api/bases/"+sourceID+"/files", map[string]any{"files": files, "conflict": "rename"})
	accepted := valueMap(t, payload)["accepted"].([]any)
	docID := accepted[0].(map[string]any)["id"].(string)

	// Raw source is a binary response, not the normal JSON envelope.
	req := httptest.NewRequest("GET", "/api/documents/"+docID+"/raw", nil)
	rec := httptest.NewRecorder()
	s.srv.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "retry budget is thirty seconds" ||
		rec.Header().Get("Content-Disposition") == "" {
		t.Fatalf("raw download: code=%d headers=%v body=%q", rec.Code, rec.Header(), rec.Body.String())
	}
	req = httptest.NewRequest("GET", "/api/documents/"+docID+"/raw?inline=1", nil)
	rec = httptest.NewRecorder()
	s.srv.Handler.ServeHTTP(rec, req)
	if disposition := rec.Header().Get("Content-Disposition"); rec.Code != http.StatusOK || !strings.HasPrefix(disposition, "inline;") {
		t.Fatalf("raw inline: code=%d disposition=%q", rec.Code, disposition)
	}

	_, payload = call(t, s, "POST", "/api/bases/"+sourceID+"/restore", map[string]any{"name": "Restored"})
	restored := valueMap(t, payload)
	restoredID := restored["id"].(string)
	if restoredID == sourceID || restored["name"] != "Restored" {
		t.Fatalf("restore base: %v", restored)
	}
	_, payload = call(t, s, "GET", "/api/bases/"+restoredID+"/documents", nil)
	docs := payload["value"].([]any)
	if len(docs) != 1 || docs[0].(map[string]any)["status"] != "ready" {
		t.Fatalf("restored documents: %v", docs)
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
	if int(metrics["imports"].(float64)) != 2 || int(metrics["searches"].(float64)) != 1 ||
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
