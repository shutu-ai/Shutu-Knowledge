package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shutu-ai/shutu-knowledge/internal/operations"
)

func TestOperationTextImportAcceptedAndIdempotent(t *testing.T) {
	s := newTestServer(t)
	_, basePayload := call(t, s, "POST", "/api/bases", map[string]any{"name": "Operations"})
	baseID := valueMap(t, basePayload)["id"].(string)
	request := map[string]any{
		"type":                 "import_text",
		"commandSchemaVersion": 1,
		"target":               map[string]any{"baseId": baseID},
		"input":                map[string]any{"title": "Operation Note", "content": "durable operation body"},
		"idempotencyKey":       "web-import-key",
	}
	code, payload, raw := callRaw(t, s, "POST", "/api/operations", request)
	if code != http.StatusAccepted {
		t.Fatalf("submit status = %d, body=%s", code, raw)
	}
	first := valueMap(t, payload)
	if first["state"] != operations.StateQueued && first["state"] != operations.StateRunning && first["state"] != operations.StateSucceeded {
		t.Fatalf("unexpected accepted state: %v", first)
	}
	operationID := first["operationId"].(string)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		op, err := s.app.Operations.Get(operationID)
		if err == nil && op.State == operations.StateSucceeded {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	op, err := s.app.Operations.Get(operationID)
	if err != nil || op.State != operations.StateSucceeded {
		t.Fatalf("operation did not succeed: %v %v", op, err)
	}
	code, payload = call(t, s, "POST", "/api/operations", request)
	if code != http.StatusOK {
		t.Fatalf("idempotent terminal status = %d %v", code, payload)
	}
	second := valueMap(t, payload)
	if second["operationId"] != operationID {
		t.Fatalf("idempotency returned %v, wanted %s", second["operationId"], operationID)
	}
	code, payload = call(t, s, "GET", "/api/bases/"+baseID+"/documents", nil)
	if code != http.StatusOK {
		t.Fatalf("list documents: %d %v", code, payload)
	}
	docs := payload["value"].([]any)
	if len(docs) != 1 {
		t.Fatalf("document count = %d, want 1", len(docs))
	}
}

func TestOperationTextImportFailureIsReceivableRetryableAndVisible(t *testing.T) {
	s := newTestServer(t)
	_, basePayload := call(t, s, "POST", "/api/bases", map[string]any{"name": "Failing Imports"})
	baseID := valueMap(t, basePayload)["id"].(string)
	s.app.Operations.SetResourceLimits(operations.ResourceLimits{
		IO:          2,
		DBWrite:     1,
		Disk:        1,
		Network:     1,
		Model:       1,
		Maintenance: 1,
		MaxPerBase:  1,
	})
	s.app.Config.Embedding.Provider = "openai"
	s.app.Knowledge.SetGlobalConfig(s.app.Config)
	blocked := &gatedImportEmbedder{started: make(chan struct{}), release: make(chan struct{})}
	s.app.Knowledge.SetProviders(blocked, nil)
	blockRequest := map[string]any{
		"type":                 "import_text",
		"commandSchemaVersion": 1,
		"target":               map[string]any{"baseId": baseID},
		"input":                map[string]any{"title": "Blocked First", "content": "# Storage\n\nthe first import waits in embedding"},
		"idempotencyKey":       "web-import-block-key",
	}
	code, payload := call(t, s, "POST", "/api/operations", blockRequest)
	if code != http.StatusAccepted {
		t.Fatalf("block submit status = %d, body=%v", code, payload)
	}
	select {
	case <-blocked.started:
	case <-time.After(2 * time.Second):
		t.Fatal("first import did not reach embedding")
	}
	request := map[string]any{
		"type":                 "import_text",
		"commandSchemaVersion": 1,
		"target":               map[string]any{"baseId": baseID},
		"input":                map[string]any{"title": "Lost Base", "content": "worker fails after acceptance"},
		"idempotencyKey":       "web-import-failure-key",
	}

	code, payload, raw := callRaw(t, s, "POST", "/api/operations", request)
	if code != http.StatusAccepted {
		t.Fatalf("submit status = %d, body=%s", code, raw)
	}
	accepted := valueMap(t, payload)
	switch accepted["state"] {
	case operations.StateQueued, operations.StateRunning:
	default:
		t.Fatalf("accepted state = %v, want queued/running before executor", accepted["state"])
	}
	if accepted["result"] != nil {
		t.Fatalf("accepted operation fabricated a result: %v", accepted["result"])
	}
	documentID := accepted["documentId"].(string)
	if documentID == "" {
		t.Fatalf("accepted response omitted stable document ID: %v", accepted)
	}

	// The worker's replayable command now points at a genuinely missing
	// target. This is the real post-acceptance conflict C01 must expose.
	if err := s.app.Knowledge.DeleteBase(baseID); err != nil {
		t.Fatal(err)
	}
	close(blocked.release)
	operationID := accepted["operationId"].(string)
	deadline := time.Now().Add(5 * time.Second)
	for {
		op, err := s.app.Operations.Get(operationID)
		if err != nil {
			t.Fatal(err)
		}
		if op.State == operations.StateFailed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("operation state = %s", op.State)
		}
		time.Sleep(10 * time.Millisecond)
	}

	code, payload, raw = callRaw(t, s, "GET", "/api/operations/"+operationID, nil)
	if code != http.StatusOK {
		t.Fatalf("terminal status = %d, body=%s", code, raw)
	}
	failed := valueMap(t, payload)
	if failed["state"] != operations.StateFailed || failed["errorCode"] != "operation_failed" {
		t.Fatalf("terminal operation = %v", failed)
	}
	if !strings.Contains(failed["errorMessage"].(string), "not found") {
		t.Fatalf("terminal error lost business cause: %v", failed["errorMessage"])
	}
	if failed["result"] != nil || failed["documentId"] != documentID {
		t.Fatalf("terminal operation changed receiver identity/result: %v", failed)
	}
	failedRevision := failed["stateRevision"].(float64)

	code, payload = call(t, s, "GET", "/api/jobs/"+operationID, nil)
	if code != http.StatusOK {
		t.Fatalf("legacy job status: %d %v", code, payload)
	}
	job := valueMap(t, payload)
	if job["status"] != "failed" || job["error"] != failed["errorMessage"] {
		t.Fatalf("legacy UI did not expose terminal failure: %v", job)
	}

	code, payload, raw = callRaw(t, s, "POST", "/api/operations/"+operationID+"/retry", nil)
	if code != http.StatusAccepted {
		t.Fatalf("retry status = %d, body=%s", code, raw)
	}
	retried := valueMap(t, payload)
	if retried["state"] != operations.StateQueued || retried["stateRevision"].(float64) <= failedRevision {
		t.Fatalf("retry operation = %v", retried)
	}
	deadline = time.Now().Add(5 * time.Second)
	for {
		op, err := s.app.Operations.Get(operationID)
		if err != nil {
			t.Fatal(err)
		}
		if op.State == operations.StateFailed {
			if op.Attempt != 2 {
				t.Fatalf("retried attempt = %d, want 2", op.Attempt)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("retry state = %s", op.State)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

type gatedImportEmbedder struct {
	started chan struct{}
	release chan struct{}
}

func (e *gatedImportEmbedder) Embed(_ context.Context, texts []string) ([][]float64, error) {
	close(e.started)
	<-e.release
	out := make([][]float64, 0, len(texts))
	for range texts {
		out = append(out, []float64{1, 0})
	}
	return out, nil
}

func (e *gatedImportEmbedder) ModelKey() string { return "fake:gated-import" }

func TestOperationCancelTransportConvergesToCancelled(t *testing.T) {
	s := newTestServer(t)
	_, basePayload := call(t, s, "POST", "/api/bases", map[string]any{"name": "Cancellable"})
	baseID := valueMap(t, basePayload)["id"].(string)
	s.app.Config.Embedding.Provider = "openai"
	s.app.Knowledge.SetGlobalConfig(s.app.Config)
	blocked := &gatedImportEmbedder{started: make(chan struct{}), release: make(chan struct{})}
	s.app.Knowledge.SetProviders(blocked, nil)

	code, payload := call(t, s, "POST", "/api/operations", map[string]any{
		"type":                 "import_text",
		"commandSchemaVersion": 1,
		"target":               map[string]any{"baseId": baseID},
		"input":                map[string]any{"title": "Cancellable Import", "content": "# Storage\n\nwait for cancel transport"},
		"idempotencyKey":       "web-cancel-transport-key",
	})
	if code != http.StatusAccepted {
		t.Fatalf("submit status = %d, body=%v", code, payload)
	}
	operationID := valueMap(t, payload)["operationId"].(string)
	select {
	case <-blocked.started:
	case <-time.After(2 * time.Second):
		t.Fatal("import did not reach cancellable embedding phase")
	}

	code, payload = call(t, s, "POST", "/api/operations/"+operationID+"/cancel", map[string]any{})
	if code != http.StatusOK {
		t.Fatalf("cancel status = %d, body=%v", code, payload)
	}
	cancelling := valueMap(t, payload)
	if cancelling["state"] != operations.StateCancelling || cancelling["cancelRequested"] != true {
		t.Fatalf("cancel response = %v", cancelling)
	}
	close(blocked.release)

	deadline := time.Now().Add(5 * time.Second)
	for {
		code, payload = call(t, s, "GET", "/api/operations/"+operationID, nil)
		if code != http.StatusOK {
			t.Fatalf("operation status = %d, body=%v", code, payload)
		}
		op := valueMap(t, payload)
		if op["state"] == operations.StateCancelled {
			break
		}
		if op["state"] == operations.StateSucceeded || op["state"] == operations.StateFailed {
			t.Fatalf("cancel converged to %v", op)
		}
		if time.Now().After(deadline) {
			t.Fatalf("operation state = %v", op["state"])
		}
		time.Sleep(10 * time.Millisecond)
	}
	code, payload = call(t, s, "GET", "/api/jobs/"+operationID, nil)
	if code != http.StatusOK {
		t.Fatalf("legacy status: %d %v", code, payload)
	}
	job := valueMap(t, payload)
	if job["status"] != "cancelled" {
		t.Fatalf("legacy UI status = %v", job)
	}
}

func TestStatusExposesSchedulerCapacity(t *testing.T) {
	s := newTestServer(t)
	code, payload := call(t, s, "GET", "/api/status", nil)
	if code != http.StatusOK {
		t.Fatalf("status: %d %v", code, payload)
	}
	scheduler := payload["scheduler"].(map[string]any)
	limits := scheduler["limits"].(map[string]any)
	if limits["io"].(float64) < 1 || limits["network"].(float64) < 1 {
		t.Fatalf("scheduler limits: %+v", limits)
	}
	if scheduler["queueLimit"].(float64) != 1000 || scheduler["maxPerBase"].(float64) != 2 {
		t.Fatalf("scheduler capacity: %+v", scheduler)
	}
	if _, ok := scheduler["lanes"].(map[string]any); !ok {
		t.Fatalf("scheduler lanes missing: %+v", scheduler)
	}
	format := payload["storageFormat"].(map[string]any)
	if format["formatVersion"].(float64) != 2 || format["migrationStatus"] != "ready" {
		t.Fatalf("storage format: %+v", format)
	}
}

func TestExpiredIdempotentRetryReturnsGone(t *testing.T) {
	s := newTestServer(t)
	_, basePayload := call(t, s, "POST", "/api/bases", map[string]any{"name": "Expired"})
	baseID := valueMap(t, basePayload)["id"].(string)
	request := map[string]any{
		"type":                 "import_text",
		"commandSchemaVersion": 1,
		"target":               map[string]any{"baseId": baseID},
		"input":                map[string]any{"title": "Expired Key", "content": "expired operation body"},
		"idempotencyKey":       "expired-web-key",
	}
	code, payload := call(t, s, "POST", "/api/operations", request)
	if code != http.StatusAccepted {
		t.Fatalf("submit status = %d, body=%v", code, payload)
	}
	operationID := valueMap(t, payload)["operationId"].(string)
	deadline := time.Now().Add(5 * time.Second)
	for {
		op, err := s.app.Operations.Get(operationID)
		if err != nil {
			t.Fatal(err)
		}
		if op.State == operations.StateSucceeded {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("operation state = %s", op.State)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := s.app.DB.Exec(`UPDATE operations SET idempotency_expires_at = ? WHERE id = ?`,
		time.Now().UnixMilli()-1, operationID); err != nil {
		t.Fatal(err)
	}
	code, payload = call(t, s, "POST", "/api/operations", request)
	if code != http.StatusGone {
		t.Fatalf("expired retry status = %d, body=%v", code, payload)
	}
	errMap := payload["error"].(map[string]any)
	if errMap["code"] != "operation_expired" {
		t.Fatalf("expired error code = %v", errMap["code"])
	}
}

func TestModelCacheMigrationOperationIsDurable(t *testing.T) {
	s := newTestServer(t)
	modelDir := filepath.Join(s.app.Models.Root(), "example", "model")
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
	request := map[string]any{
		"type":                 "migrate_model_cache",
		"commandSchemaVersion": 1,
		"target":               map[string]any{},
		"input":                map[string]any{"targetDir": target, "removeSource": true},
		"idempotencyKey":       "model-cache-migration-key",
	}
	code, payload := call(t, s, "POST", "/api/operations", request)
	if code != http.StatusAccepted {
		t.Fatalf("submit migration: %d %v", code, payload)
	}
	operationID := valueMap(t, payload)["operationId"].(string)
	deadline := time.Now().Add(5 * time.Second)
	var result struct {
		ModelCount   int  `json:"modelCount"`
		SourceRemove bool `json:"sourceRemoved"`
	}
	for {
		op, err := s.app.Operations.Get(operationID)
		if err != nil {
			t.Fatal(err)
		}
		if op.State == operations.StateSucceeded {
			if err := json.Unmarshal(op.Result, &result); err != nil {
				t.Fatalf("decode result: %v", err)
			}
			break
		}
		if op.State == operations.StateFailed {
			t.Fatalf("migration failed: %s", op.ErrorMessage)
		}
		if time.Now().After(deadline) {
			t.Fatalf("migration state = %s", op.State)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if result.ModelCount != 1 || result.SourceRemove != true {
		t.Fatalf("migration result: %+v", result)
	}
	if s.app.Config.Models.CacheDir != filepath.Clean(target) || s.app.Models.Root() != filepath.Clean(target) {
		t.Fatalf("migration was not activated: config=%q root=%q",
			s.app.Config.Models.CacheDir, s.app.Models.Root())
	}
	code, payload = call(t, s, "POST", "/api/operations", request)
	if code != http.StatusOK || valueMap(t, payload)["operationId"] != operationID {
		t.Fatalf("idempotent migration: %d %v", code, payload)
	}
}

func TestOperationFileUploadImportIsDurable(t *testing.T) {
	s := newTestServer(t)
	_, basePayload := call(t, s, "POST", "/api/bases", map[string]any{"name": "Uploads"})
	baseID := valueMap(t, basePayload)["id"].(string)
	content := "# Staged file\n\nupload operation body"
	sum := sha256.Sum256([]byte(content))
	create := map[string]any{
		"baseId":         baseID,
		"fileName":       "staged.md",
		"expectedSize":   len(content),
		"expectedSha256": hex.EncodeToString(sum[:]),
	}
	code, payload := call(t, s, "POST", "/api/uploads", create)
	if code != http.StatusCreated {
		t.Fatalf("create upload: %d %v", code, payload)
	}
	upload := valueMap(t, payload)["upload"].(map[string]any)
	uploadID := upload["uploadId"].(string)
	request := httptest.NewRequest(http.MethodPut, "/api/uploads/"+uploadID+"/content", strings.NewReader(content))
	recorder := httptest.NewRecorder()
	s.srv.Handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("put upload: %d %s", recorder.Code, recorder.Body.String())
	}
	code, payload = call(t, s, "POST", "/api/uploads/"+uploadID+"/complete", map[string]any{})
	if code != http.StatusOK {
		t.Fatalf("complete upload: %d %v", code, payload)
	}
	code, payload = call(t, s, "POST", "/api/operations", map[string]any{
		"type":                 "import_file",
		"commandSchemaVersion": 1,
		"target":               map[string]any{"baseId": baseID},
		"input": map[string]any{
			"uploadId": uploadID,
			"fileName": "staged.md",
			"conflict": "rename",
		},
		"idempotencyKey": "upload-operation-key",
	})
	if code != http.StatusAccepted {
		t.Fatalf("submit import: %d %v", code, payload)
	}
	operationID := valueMap(t, payload)["operationId"].(string)
	deadline := time.Now().Add(5 * time.Second)
	for {
		op, err := s.app.Operations.Get(operationID)
		if err != nil {
			t.Fatal(err)
		}
		if op.State == operations.StateSucceeded {
			break
		}
		if op.State == operations.StateFailed {
			t.Fatalf("import failed: %s", op.ErrorMessage)
		}
		if time.Now().After(deadline) {
			t.Fatalf("import state = %s", op.State)
		}
		time.Sleep(10 * time.Millisecond)
	}
	code, payload = call(t, s, "GET", "/api/bases/"+baseID+"/documents", nil)
	if code != http.StatusOK {
		t.Fatalf("list documents: %d %v", code, payload)
	}
	docs := payload["value"].([]any)
	if len(docs) != 1 {
		t.Fatalf("document count = %d, want 1", len(docs))
	}
	code, payload = call(t, s, "POST", "/api/operations", map[string]any{
		"type":                 "import_file",
		"commandSchemaVersion": 1,
		"target":               map[string]any{"baseId": baseID},
		"input": map[string]any{
			"uploadId": uploadID,
			"fileName": "staged.md",
			"conflict": "rename",
		},
		"idempotencyKey": "upload-operation-key",
	})
	if code != http.StatusOK || valueMap(t, payload)["operationId"] != operationID {
		t.Fatalf("idempotent upload operation: %d %v", code, payload)
	}
}
