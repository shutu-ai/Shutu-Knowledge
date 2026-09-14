package operations

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/shutu-ai/shutu-knowledge/internal/storage"
)

func TestIdempotencyCredentialSurvivesRestartAndRotation(t *testing.T) {
	db, err := storage.Open(t.TempDir() + "/credential-rotation.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	service, err := New(db, 1)
	if err != nil {
		t.Fatal(err)
	}
	service.Register("echo", func(_ context.Context, _ Operation, _ json.RawMessage, _ func(Progress)) (any, error) {
		return map[string]any{"ok": true}, nil
	})
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Stop)

	scope := IdempotencyCredentialRequest{OperationType: "echo", BaseID: "credential-base"}
	issued, err := service.IssueIdempotencyCredential(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	if issued.OperationKey == "" || issued.KeyID == "" || issued.Credential == "" {
		t.Fatalf("incomplete issued credential: %+v", issued)
	}
	request := Request{
		Type: "echo", CommandSchemaVersion: CommandSchemaV1,
		BaseID: "credential-base", Payload: json.RawMessage(`{"value":"credential"}`),
		IdempotencyKey: issued.OperationKey,
	}
	accepted, err := service.Submit(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	waitForState(t, service, accepted.ID, StateSucceeded)
	retried, err := service.Submit(context.Background(), request)
	if err != nil || retried.ID != accepted.ID {
		t.Fatalf("credential retry = (%s) %v, wanted %s", retried.ID, err, accepted.ID)
	}

	if _, err := service.ValidateIdempotencyCredential(context.Background(),
		issued.Credential+"tampered", request); !errors.Is(err, ErrIdempotencyCredentialInvalid) {
		t.Fatalf("tampered credential error = %v", err)
	}
	wrongScope := request
	wrongScope.BaseID = "other-base"
	if _, err := service.ValidateIdempotencyCredential(context.Background(),
		issued.Credential, wrongScope); !errors.Is(err, ErrIdempotencyCredentialInvalid) {
		t.Fatalf("wrong credential scope error = %v", err)
	}
	expired := expiredCredentialForTest(t, service, scope)
	if _, err := service.ValidateIdempotencyCredential(context.Background(),
		expired, request); !errors.Is(err, ErrIdempotencyCredentialExpired) {
		t.Fatalf("expired credential error = %v", err)
	}

	// A newly constructed owner over the same persisted state proves the
	// validation secret is durable, not process-local.
	restarted, err := New(db, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.ValidateIdempotencyCredential(context.Background(),
		issued.Credential, request); err != nil {
		t.Fatalf("credential did not survive restart: %v", err)
	}
	newKeyID, err := restarted.RotateIdempotencySigningKey(context.Background(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if newKeyID == issued.KeyID {
		t.Fatal("rotation reused the active signing key ID")
	}
	if _, err := restarted.ValidateIdempotencyCredential(context.Background(),
		issued.Credential, request); err != nil {
		t.Fatalf("rotated old credential was not retained: %v", err)
	}
	replacement, err := restarted.IssueIdempotencyCredential(context.Background(), scope)
	if err != nil || replacement.KeyID != newKeyID {
		t.Fatalf("post-rotation issue = %+v %v", replacement, err)
	}
	if err := restarted.RevokeIdempotencySigningKey(context.Background(), issued.KeyID); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.ValidateIdempotencyCredential(context.Background(),
		issued.Credential, request); !errors.Is(err, ErrIdempotencyCredentialInvalid) {
		t.Fatalf("revoked credential error = %v", err)
	}
	nextRequest := request
	nextRequest.Payload = json.RawMessage(`{"value":"replacement"}`)
	nextRequest.IdempotencyKey = replacement.OperationKey
	if _, err := service.Submit(context.Background(), nextRequest); err != nil {
		t.Fatalf("replacement credential submit: %v", err)
	}
}

func TestConcurrentCredentialClientsKeepDistinctBindingsUntilExpiry(t *testing.T) {
	db, err := storage.Open(t.TempDir() + "/credential-clients.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	service, err := New(db, 3)
	if err != nil {
		t.Fatal(err)
	}
	var calls int
	var mu sync.Mutex
	service.Register("echo", func(_ context.Context, _ Operation, _ json.RawMessage, _ func(Progress)) (any, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		return nil, nil
	})
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Stop)

	const clients = 12
	type clientState struct {
		credential IdempotencyCredential
		operation  Operation
	}
	states := make([]clientState, clients)
	var wg sync.WaitGroup
	for index := range states {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			credential, err := service.IssueIdempotencyCredential(context.Background(), IdempotencyCredentialRequest{
				OperationType: "echo", BaseID: fmt.Sprintf("client-%02d", index),
			})
			if err != nil {
				t.Errorf("issue client %02d: %v", index, err)
				return
			}
			operation, err := service.Submit(context.Background(), Request{
				Type: "echo", CommandSchemaVersion: CommandSchemaV1,
				BaseID:         fmt.Sprintf("client-%02d", index),
				Payload:        json.RawMessage(fmt.Sprintf(`{"client":%d}`, index)),
				IdempotencyKey: credential.OperationKey,
			})
			if err != nil {
				t.Errorf("submit client %02d: %v", index, err)
				return
			}
			states[index] = clientState{credential: credential, operation: operation}
		}(index)
	}
	wg.Wait()

	for index, state := range states {
		if state.operation.ID == "" {
			t.Fatalf("client %02d had no accepted operation", index)
		}
		waitForState(t, service, state.operation.ID, StateSucceeded)
	}
	for index, state := range states {
		request := Request{
			Type: "echo", CommandSchemaVersion: CommandSchemaV1,
			BaseID: state.operation.BaseID, Payload: json.RawMessage(fmt.Sprintf(`{"client":%d}`, index)),
			IdempotencyKey: state.credential.OperationKey,
		}
		retried, err := service.Submit(context.Background(), request)
		if err != nil || retried.ID != state.operation.ID {
			t.Fatalf("client %02d retry = (%s) %v, wanted %s",
				index, retried.ID, err, state.operation.ID)
		}
	}
	mu.Lock()
	if calls != clients {
		mu.Unlock()
		t.Fatalf("executor calls = %d, want %d", calls, clients)
	}
	mu.Unlock()

	if _, err := db.Exec(`UPDATE operations SET idempotency_expires_at = ?
		WHERE idempotency_key LIKE 'opcred:%'`, time.Now().UnixMilli()-1); err != nil {
		t.Fatal(err)
	}
	for index, state := range states {
		request := Request{
			Type: "echo", CommandSchemaVersion: CommandSchemaV1,
			BaseID: state.operation.BaseID, Payload: json.RawMessage(fmt.Sprintf(`{"client":%d}`, index)),
			IdempotencyKey: state.credential.OperationKey,
		}
		if _, err := service.Submit(context.Background(), request); !errors.Is(err, ErrOperationExpired) {
			t.Fatalf("client %02d expired retry error = %v", index, err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != clients {
		t.Fatalf("expired retries executed %d additional commands", calls-clients)
	}
}

func expiredCredentialForTest(
	t *testing.T, service *Service, scope IdempotencyCredentialRequest,
) string {
	t.Helper()
	var keyID, secretText string
	if err := service.db.QueryRow(`SELECT id, secret FROM operation_signing_keys
		WHERE state = 'active' LIMIT 1`).Scan(&keyID, &secretText); err != nil {
		t.Fatal(err)
	}
	secret, err := hex.DecodeString(secretText)
	if err != nil {
		t.Fatal(err)
	}
	credentialID, err := newID()
	if err != nil {
		t.Fatal(err)
	}
	credential, err := signCredential(credentialClaims{
		KeyID: keyID, CredentialID: credentialID,
		OperationType: scope.OperationType, BaseID: scope.BaseID,
		DocumentID: scope.DocumentID, ExpiresAt: now() - 1,
	}, secret)
	if err != nil {
		t.Fatal(err)
	}
	return credential
}
