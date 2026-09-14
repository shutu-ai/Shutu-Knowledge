package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shutu-ai/shutu-knowledge/internal/operations"
)

func waitForURLOperation(t *testing.T, s *Server, operationID string) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		op, err := s.app.Operations.Get(operationID)
		if err != nil {
			t.Fatal(err)
		}
		if op.State == operations.StateSucceeded {
			return
		}
		if op.State == operations.StateFailed {
			t.Fatalf("URL operation failed: %s", op.ErrorMessage)
		}
		time.Sleep(20 * time.Millisecond)
	}
	op, err := s.app.Operations.Get(operationID)
	if err != nil {
		t.Fatal(err)
	}
	t.Fatalf("URL operation state = %s", op.State)
}

func TestURLOperationUsesImmutableCapturedSource(t *testing.T) {
	content := "# Captured URL\n\nThis body is fixed across durable retries."
	requests := 0
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		_, _ = w.Write([]byte(content))
	}))
	defer source.Close()

	s := newTestServer(t)
	_, basePayload := call(t, s, "POST", "/api/bases", map[string]any{"name": "URL Operations"})
	baseID := valueMap(t, basePayload)["id"].(string)
	request := map[string]any{
		"type":                 "import_url",
		"commandSchemaVersion": 1,
		"target":               map[string]any{"baseId": baseID},
		"input":                map[string]any{"url": source.URL + "/page"},
		"idempotencyKey":       "url-operation-key",
	}
	code, payload := call(t, s, "POST", "/api/operations", request)
	if code != http.StatusAccepted {
		t.Fatalf("submit URL operation: %d %v", code, payload)
	}
	operationID := valueMap(t, payload)["operationId"].(string)
	waitForURLOperation(t, s, operationID)
	if requests != 1 {
		t.Fatalf("source requests = %d, want one capture", requests)
	}
	capture, err := s.app.Operations.GetURLCapture(context.Background(), operationID)
	if err != nil || capture.State != operations.URLStateConsumed || len(capture.Body) != 0 {
		t.Fatalf("capture after success: %+v %v", capture, err)
	}
	code, payload = call(t, s, "POST", "/api/operations", request)
	if code != http.StatusOK || valueMap(t, payload)["operationId"] != operationID {
		t.Fatalf("idempotent URL operation: %d %v", code, payload)
	}
	code, payload = call(t, s, "GET", "/api/bases/"+baseID+"/documents", nil)
	if code != http.StatusOK {
		t.Fatalf("list documents: %d %v", code, payload)
	}
	docs := payload["value"].([]any)
	if len(docs) != 1 {
		t.Fatalf("document count = %d, want 1", len(docs))
	}
	doc := docs[0].(map[string]any)
	documentID := doc["id"].(string)
	if !strings.Contains(doc["url"].(string), "/page") {
		t.Fatalf("document URL: %v", doc)
	}

	code, payload = call(t, s, "POST", "/api/operations", map[string]any{
		"type":                 "refresh_url",
		"commandSchemaVersion": 1,
		"target":               map[string]any{"baseId": baseID, "documentId": documentID},
		"input":                map[string]any{},
		"idempotencyKey":       "url-refresh-key",
	})
	if code != http.StatusAccepted {
		t.Fatalf("submit refresh: %d %v", code, payload)
	}
	refreshID := valueMap(t, payload)["operationId"].(string)
	waitForURLOperation(t, s, refreshID)
	refresh, err := s.app.Operations.Get(refreshID)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Skipped int `json:"skipped"`
	}
	if err := json.Unmarshal(refresh.Result, &result); err != nil {
		t.Fatalf("decode result %s: %v", refresh.Result, err)
	}
	if result.Skipped != 1 {
		t.Fatalf("unchanged refresh result: %s", refresh.Result)
	}
	if requests != 2 {
		t.Fatalf("source requests = %d, want capture and refresh", requests)
	}
}

func TestLegacyURLRoutesSubmitDurableOperations(t *testing.T) {
	content := "# Legacy URL route\n\nCompatibility traffic still captures once."
	requests := 0
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		_, _ = w.Write([]byte(content))
	}))
	defer source.Close()
	s := newTestServer(t)
	_, basePayload := call(t, s, "POST", "/api/bases", map[string]any{"name": "Legacy URL"})
	baseID := valueMap(t, basePayload)["id"].(string)

	code, payload := call(t, s, "POST", "/api/bases/"+baseID+"/url", map[string]any{
		"url": source.URL + "/compat", "title": "Legacy URL",
	})
	if code != http.StatusAccepted {
		t.Fatalf("legacy URL import status = %d, body=%v", code, payload)
	}
	imported := valueMap(t, payload)
	importOperationID := imported["jobId"].(string)
	if imported["operationId"] != importOperationID || imported["documentId"] == "" {
		t.Fatalf("legacy URL compatibility payload: %v", imported)
	}
	waitForURLOperation(t, s, importOperationID)

	code, payload = call(t, s, "GET", "/api/bases/"+baseID+"/documents", nil)
	if code != http.StatusOK || len(payload["value"].([]any)) != 1 {
		t.Fatalf("legacy URL documents: %d %v", code, payload)
	}
	documentID := payload["value"].([]any)[0].(map[string]any)["id"].(string)
	if documentID != imported["documentId"] {
		t.Fatalf("returned documentId %v, stored %s", imported["documentId"], documentID)
	}

	code, payload = call(t, s, "POST", "/api/documents/"+documentID+"/refresh", map[string]any{})
	if code != http.StatusAccepted {
		t.Fatalf("legacy URL refresh status = %d, body=%v", code, payload)
	}
	refreshed := valueMap(t, payload)
	refreshOperationID := refreshed["jobId"].(string)
	if refreshed["operationId"] != refreshOperationID || refreshed["documentId"] != documentID {
		t.Fatalf("legacy refresh compatibility payload: %v", refreshed)
	}
	waitForURLOperation(t, s, refreshOperationID)
	if requests != 2 {
		t.Fatalf("source requests = %d, want import and refresh", requests)
	}
}
