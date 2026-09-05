package web

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shutu-ai/shutu-knowledge/internal/app"
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

func call(t *testing.T, s *Server, method, path string, body any) (int, map[string]any) {
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
	return rec.Code, payload
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

func base64Of(text string) string {
	return base64.StdEncoding.EncodeToString([]byte(text))
}
