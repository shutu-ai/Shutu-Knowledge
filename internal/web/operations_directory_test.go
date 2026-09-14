package web

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/shutu-ai/shutu-knowledge/internal/operations"
)

func waitForDirectoryOperation(t *testing.T, s *Server, operationID string) {
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
			t.Fatalf("directory operation failed: %s", op.ErrorMessage)
		}
		time.Sleep(20 * time.Millisecond)
	}
	op, err := s.app.Operations.Get(operationID)
	if err != nil {
		t.Fatal(err)
	}
	t.Fatalf("directory operation state = %s: %s", op.State, op.ErrorMessage)
}

func TestLegacyDirectoryRoutesSubmitDurableOperations(t *testing.T) {
	s := newTestServer(t)
	_, basePayload := call(t, s, "POST", "/api/bases", map[string]any{"name": "Legacy Directories"})
	baseID := valueMap(t, basePayload)["id"].(string)
	root := filepath.Join(t.TempDir(), "legacy-tree")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "legacy.md"), []byte("# legacy directory route"), 0o600); err != nil {
		t.Fatal(err)
	}

	code, payload := call(t, s, "POST", "/api/bases/"+baseID+"/import-directory", map[string]any{"path": root})
	if code != http.StatusAccepted {
		t.Fatalf("legacy import status = %d, body=%v", code, payload)
	}
	legacyImport := valueMap(t, payload)
	importID := legacyImport["jobId"].(string)
	if legacyImport["operationId"] != importID {
		t.Fatalf("legacy import compatibility payload: %v", legacyImport)
	}
	waitForDirectoryOperation(t, s, importID)

	code, payload = call(t, s, "GET", "/api/bases/"+baseID+"/documents", nil)
	if code != http.StatusOK || len(payload["value"].([]any)) != 2 {
		t.Fatalf("legacy import documents: %d %v", code, payload)
	}
	directoryID := payload["value"].([]any)[0].(map[string]any)["id"].(string)

	code, payload = call(t, s, "POST", "/api/documents/"+directoryID+"/rescan", map[string]any{})
	if code != http.StatusAccepted {
		t.Fatalf("legacy rescan status = %d, body=%v", code, payload)
	}
	legacyRescan := valueMap(t, payload)
	rescanID := legacyRescan["jobId"].(string)
	if legacyRescan["operationId"] != rescanID {
		t.Fatalf("legacy rescan compatibility payload: %v", legacyRescan)
	}
	waitForDirectoryOperation(t, s, rescanID)
}

func TestDirectoryOperationsAreDurable(t *testing.T) {
	s := newTestServer(t)
	_, basePayload := call(t, s, "POST", "/api/bases", map[string]any{"name": "Directory Operations"})
	baseID := valueMap(t, basePayload)["id"].(string)
	root := filepath.Join(t.TempDir(), "tree")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "note.md"), []byte("# durable directory entry"), 0o600); err != nil {
		t.Fatal(err)
	}
	importRequest := map[string]any{
		"type":                 "import_directory",
		"commandSchemaVersion": 1,
		"target":               map[string]any{"baseId": baseID},
		"input":                map[string]any{"path": root},
		"idempotencyKey":       "directory-import-key",
	}
	code, payload := call(t, s, "POST", "/api/operations", importRequest)
	if code != http.StatusAccepted {
		t.Fatalf("submit import: %d %v", code, payload)
	}
	importID := valueMap(t, payload)["operationId"].(string)
	waitForDirectoryOperation(t, s, importID)
	code, payload = call(t, s, "GET", "/api/bases/"+baseID+"/documents", nil)
	if code != http.StatusOK {
		t.Fatalf("list after import: %d %v", code, payload)
	}
	if documents := payload["value"].([]any); len(documents) != 2 {
		t.Fatalf("document count = %d, want directory and file", len(documents))
	}
	var directoryID string
	if documents := payload["value"].([]any); len(documents) == 2 {
		directory := documents[0].(map[string]any)
		file := documents[1].(map[string]any)
		if directory["sourceType"] != "directory" || file["sourceType"] != "file" {
			t.Fatalf("unexpected documents: %v %v", directory, file)
		}
		directoryID = directory["id"].(string)
	}
	code, payload = call(t, s, "POST", "/api/operations", importRequest)
	if code != http.StatusOK || valueMap(t, payload)["operationId"] != importID {
		t.Fatalf("idempotent directory import: %d %v", code, payload)
	}
	code, payload = call(t, s, "POST", "/api/operations", map[string]any{
		"type":                 "rescan_directory",
		"commandSchemaVersion": 1,
		"target":               map[string]any{"baseId": baseID, "documentId": directoryID},
		"input":                map[string]any{},
		"idempotencyKey":       "directory-rescan-key",
	})
	if code != http.StatusAccepted {
		t.Fatalf("submit rescan: %d %v", code, payload)
	}
	waitForDirectoryOperation(t, s, valueMap(t, payload)["operationId"].(string))
	code, payload = call(t, s, "POST", "/api/operations", map[string]any{
		"type":                 "delete_directory",
		"commandSchemaVersion": 1,
		"target":               map[string]any{"baseId": baseID, "documentId": directoryID},
		"input":                map[string]any{},
		"idempotencyKey":       "directory-delete-key",
	})
	if code != http.StatusAccepted {
		t.Fatalf("submit delete: %d %v", code, payload)
	}
	deleteID := valueMap(t, payload)["operationId"].(string)
	waitForDirectoryOperation(t, s, deleteID)
	code, payload = call(t, s, "GET", "/api/bases/"+baseID+"/documents", nil)
	if code != http.StatusOK {
		t.Fatalf("list after delete: %d %v", code, payload)
	}
	if documents := payload["value"].([]any); len(documents) != 0 {
		t.Fatalf("document count after delete = %d, want 0", len(documents))
	}
	deleted, err := s.app.Operations.Get(deleteID)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Succeeded int `json:"succeeded"`
	}
	if err := json.Unmarshal(deleted.Result, &result); err != nil {
		t.Fatalf("decode delete result %s: %v", deleted.Result, err)
	}
	if result.Succeeded != 2 {
		t.Fatalf("delete result: %+v", result)
	}
}

func TestBatchDocumentDeleteOperationReportsSkipped(t *testing.T) {
	s := newTestServer(t)
	_, basePayload := call(t, s, "POST", "/api/bases", map[string]any{"name": "Batch Delete"})
	baseID := valueMap(t, basePayload)["id"].(string)
	var firstID, secondID string
	for index, title := range []string{"first", "second"} {
		if index == 0 {
			firstID = createTextDocument(t, s, baseID, title, "batch delete body")
		} else {
			secondID = createTextDocument(t, s, baseID, title, "batch delete body")
		}
	}
	code, payload := call(t, s, "POST", "/api/operations", map[string]any{
		"type":                 "delete_documents",
		"commandSchemaVersion": 1,
		"target":               map[string]any{"baseId": baseID},
		"input":                map[string]any{"documentIds": []string{firstID, secondID, "missing"}},
		"idempotencyKey":       "batch-delete-key",
	})
	if code != http.StatusAccepted {
		t.Fatalf("submit batch delete: %d %v", code, payload)
	}
	operationID := valueMap(t, payload)["operationId"].(string)
	waitForDirectoryOperation(t, s, operationID)
	op, err := s.app.Operations.Get(operationID)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Succeeded int `json:"succeeded"`
		Skipped   int `json:"skipped"`
	}
	if err := json.Unmarshal(op.Result, &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.Succeeded != 2 || result.Skipped != 1 {
		t.Fatalf("batch delete result: %+v", result)
	}
	code, payload = call(t, s, "GET", "/api/bases/"+baseID+"/documents", nil)
	if code != http.StatusOK {
		t.Fatalf("list after batch delete: %d %v", code, payload)
	}
	if documents := payload["value"].([]any); len(documents) != 0 {
		t.Fatalf("documents remained: %v", documents)
	}
}

func TestBatchDocumentReindexOperationReportsSkipped(t *testing.T) {
	s := newTestServer(t)
	_, basePayload := call(t, s, "POST", "/api/bases", map[string]any{"name": "Batch Reindex"})
	baseID := valueMap(t, basePayload)["id"].(string)
	var firstID string
	var secondID string
	for index, title := range []string{"first", "second"} {
		if index == 0 {
			firstID = createTextDocument(t, s, baseID, title, "batch re-index body")
		} else {
			secondID = createTextDocument(t, s, baseID, title, "batch re-index body")
		}
	}
	code, payload := call(t, s, "POST", "/api/operations", map[string]any{
		"type":                 "reindex_documents",
		"commandSchemaVersion": 1,
		"target":               map[string]any{"baseId": baseID},
		"input":                map[string]any{"documentIds": []string{firstID, secondID, "missing"}},
		"idempotencyKey":       "batch-reindex-key",
	})
	if code != http.StatusAccepted {
		t.Fatalf("submit batch re-index: %d %v", code, payload)
	}
	operationID := valueMap(t, payload)["operationId"].(string)
	waitForDirectoryOperation(t, s, operationID)
	op, err := s.app.Operations.Get(operationID)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Succeeded int `json:"succeeded"`
		Skipped   int `json:"skipped"`
	}
	if err := json.Unmarshal(op.Result, &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.Succeeded != 2 || result.Skipped != 1 {
		t.Fatalf("batch re-index result: %+v", result)
	}
}

func TestBaseDeleteSubmitsDurableOperation(t *testing.T) {
	s := newTestServer(t)
	_, basePayload := call(t, s, "POST", "/api/bases", map[string]any{"name": "Durable Base Delete"})
	baseID := valueMap(t, basePayload)["id"].(string)
	createTextDocument(t, s, baseID, "base delete", "durable base delete body")
	code, payload := call(t, s, "DELETE", "/api/bases/"+baseID, nil)
	if code != http.StatusAccepted {
		t.Fatalf("submit base delete: %d %v", code, payload)
	}
	operationID := valueMap(t, payload)["operationId"].(string)
	waitForDirectoryOperation(t, s, operationID)
	code, payload = call(t, s, "GET", "/api/bases", nil)
	if code != http.StatusOK {
		t.Fatalf("list bases after delete: %d %v", code, payload)
	}
	for _, item := range payload["value"].([]any) {
		if item.(map[string]any)["id"] == baseID {
			t.Fatalf("deleted base remains visible: %v", item)
		}
	}
}

func TestBaseReindexSubmitsDurableOperation(t *testing.T) {
	s := newTestServer(t)
	_, basePayload := call(t, s, "POST", "/api/bases", map[string]any{"name": "Durable Base Reindex"})
	baseID := valueMap(t, basePayload)["id"].(string)
	createTextDocument(t, s, baseID, "base reindex", "durable base reindex body")
	code, payload := call(t, s, "POST", "/api/bases/"+baseID+"/reindex", nil)
	if code != http.StatusAccepted {
		t.Fatalf("submit base re-index: %d %v", code, payload)
	}
	operationID := valueMap(t, payload)["operationId"].(string)
	waitForDirectoryOperation(t, s, operationID)
	op, err := s.app.Operations.Get(operationID)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Succeeded int `json:"succeeded"`
	}
	if err := json.Unmarshal(op.Result, &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.Succeeded != 1 {
		t.Fatalf("base re-index result: %+v", result)
	}
}
