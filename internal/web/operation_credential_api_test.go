package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/shutu-ai/shutu-knowledge/internal/operations"
)

func callWithCredentialHeader(t *testing.T, s *Server, method, path, credential string, body any) (int, map[string]any) {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(data))
	request.Header.Set("Content-Type", "application/json")
	if credential != "" {
		request.Header.Set("Idempotency-Key", credential)
	}
	recorder := httptest.NewRecorder()
	s.srv.Handler.ServeHTTP(recorder, request)
	var payload map[string]any
	if recorder.Body.Len() > 0 {
		if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
	}
	return recorder.Code, payload
}

func TestOperationCredentialAPIAdmitsAndRevokesSignedScope(t *testing.T) {
	s := newTestServer(t)
	_, basePayload := call(t, s, "POST", "/api/bases", map[string]any{"name": "Credential API"})
	baseID := valueMap(t, basePayload)["id"].(string)

	code, payload := call(t, s, "POST", "/api/operation-keys", map[string]any{
		"type": "import_text", "baseId": baseID, "lifetimeSeconds": 3600,
	})
	if code != http.StatusCreated {
		t.Fatalf("issue credential status = %d, body=%v", code, payload)
	}
	issued := valueMap(t, payload)
	credential, _ := issued["credential"].(string)
	keyID, _ := issued["keyId"].(string)
	if credential == "" || keyID == "" || issued["operationKey"] == "" {
		t.Fatalf("incomplete credential response: %v", issued)
	}

	submission := map[string]any{
		"type": "import_text", "commandSchemaVersion": 1,
		"target": map[string]any{"baseId": baseID},
		"input":  map[string]any{"title": "Credential Import", "content": "server issued credential body"},
	}
	code, payload = callWithCredentialHeader(t, s, http.MethodPost, "/api/operations", credential, submission)
	if code != http.StatusAccepted {
		t.Fatalf("credential submit status = %d, body=%v", code, payload)
	}
	accepted := valueMap(t, payload)
	operationID := accepted["operationId"].(string)
	deadline := time.Now().Add(5 * time.Second)
	for {
		op, err := s.app.Operations.Get(operationID)
		if err != nil {
			t.Fatal(err)
		}
		if op.State == operations.StateSucceeded {
			break
		}
		if op.State == operations.StateFailed || op.State == operations.StateCancelled {
			t.Fatalf("credential operation ended %s", op.State)
		}
		if time.Now().After(deadline) {
			t.Fatalf("credential operation state = %s", op.State)
		}
		time.Sleep(10 * time.Millisecond)
	}

	code, payload = callWithCredentialHeader(t, s, http.MethodPost, "/api/operations", credential, submission)
	if code != http.StatusOK || valueMap(t, payload)["operationId"] != operationID {
		t.Fatalf("credential retry = (%d) %v", code, payload)
	}
	code, payload = callWithCredentialHeader(t, s, http.MethodPost, "/api/operations", credential+"x", submission)
	if code != http.StatusUnauthorized || payload["error"].(map[string]any)["code"] != "invalid_credential" {
		t.Fatalf("tampered credential = (%d) %v", code, payload)
	}

	code, payload = call(t, s, "POST", "/api/operation-keys/rotate", map[string]any{"retentionSeconds": 3600})
	if code != http.StatusOK {
		t.Fatalf("rotate credential key: %d %v", code, payload)
	}
	code, payload = callWithCredentialHeader(t, s, http.MethodPost, "/api/operations", credential, submission)
	if code != http.StatusOK || valueMap(t, payload)["operationId"] != operationID {
		t.Fatalf("old credential after rotation = (%d) %v", code, payload)
	}

	code, payload = call(t, s, "POST", "/api/operation-keys/revoke", map[string]any{"keyId": keyID})
	if code != http.StatusOK {
		t.Fatalf("revoke credential key: %d %v", code, payload)
	}
	code, payload = callWithCredentialHeader(t, s, http.MethodPost, "/api/operations", credential, submission)
	if code != http.StatusUnauthorized || payload["error"].(map[string]any)["code"] != "invalid_credential" {
		t.Fatalf("revoked credential retry = (%d) %v", code, payload)
	}

	limiter := newWindowRateLimiter(2, time.Minute)
	if !limiter.allow("peer") || !limiter.allow("peer") || limiter.allow("peer") {
		t.Fatal("operation key issuance limiter did not enforce its window")
	}
}
