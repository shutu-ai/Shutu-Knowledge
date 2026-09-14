package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/shutu-ai/shutu-knowledge/internal/operations"
)

func percentileDuration(values []time.Duration, percentile float64) time.Duration {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]time.Duration(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return sorted[int(float64(len(sorted)-1)*percentile)]
}

type busyGateEmbedder struct {
	once    sync.Once
	started chan struct{}
	release chan struct{}
}

func (e *busyGateEmbedder) Embed(_ context.Context, texts []string) ([][]float64, error) {
	e.once.Do(func() { close(e.started) })
	<-e.release
	out := make([][]float64, 0, len(texts))
	for range texts {
		out = append(out, []float64{1, 0})
	}
	return out, nil
}

func (e *busyGateEmbedder) ModelKey() string { return "fake:busy-http-gate" }

type httpResponse struct {
	status int
	value  map[string]any
}

func callBusyHTTP(
	t *testing.T,
	client *http.Client,
	serverURL string,
	method string,
	path string,
	timeout time.Duration,
	body any,
) (httpResponse, error) {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return httpResponse{}, err
		}
		reader = bytes.NewReader(data)
	} else {
		reader = bytes.NewReader(nil)
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, serverURL+path, reader)
	if err != nil {
		return httpResponse{}, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(req)
	if err != nil {
		return httpResponse{}, err
	}
	defer response.Body.Close()
	var payload map[string]any
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return httpResponse{}, fmt.Errorf("decode %s: %w", path, err)
	}
	value, _ := payload["value"].(map[string]any)
	return httpResponse{status: response.StatusCode, value: value}, nil
}

func TestSustainedBusyWriterKeepsHTTPControlPathsBounded(t *testing.T) {
	s := newTestServer(t)
	server := httptest.NewServer(s.srv.Handler)
	t.Cleanup(server.Close)
	client := &http.Client{Timeout: time.Second}

	_, basePayload := call(t, s, "POST", "/api/bases", map[string]any{"name": "Busy HTTP"})
	baseID := valueMap(t, basePayload)["id"].(string)
	s.app.Config.Embedding.Provider = "openai"
	s.app.Knowledge.SetGlobalConfig(s.app.Config)
	blocked := &busyGateEmbedder{started: make(chan struct{}), release: make(chan struct{})}
	s.app.Knowledge.SetProviders(blocked, nil)

	code, payload := call(t, s, "POST", "/api/operations", map[string]any{
		"type": "import_text", "commandSchemaVersion": 1,
		"target": map[string]any{"baseId": baseID},
		"input": map[string]any{
			"title":   "Running Before Writer Lock",
			"content": "# Busy\n\nthe cancellation intent must remain bounded while SQLite is unavailable",
		},
		"idempotencyKey": "busy-http-cancellable",
	})
	if code != http.StatusAccepted {
		t.Fatalf("seed submit status = %d, body=%v", code, payload)
	}
	operationID := valueMap(t, payload)["operationId"].(string)
	select {
	case <-blocked.started:
	case <-time.After(2 * time.Second):
		t.Fatal("seed import did not reach cancellable embedding")
	}

	// A no-op write through the application pool takes SQLite's writer lock.
	// Reads remain available in WAL; this proves HTTP durability writes cannot
	// fake acceptance while the control path remains bounded.
	lockTx, err := s.app.DB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lockTx.Rollback() })
	if _, err := lockTx.ExecContext(context.Background(),
		`UPDATE storage_format SET migration_status = migration_status WHERE id = 1`); err != nil {
		t.Fatal(err)
	}

	const (
		submitRounds  = 30
		statusRounds  = 50
		cancelRounds  = 10
		requestBudget = 180 * time.Millisecond
	)
	var (
		mu              sync.Mutex
		submitLatencies []time.Duration
		statusLatencies []time.Duration
		cancelLatencies []time.Duration
		wg              sync.WaitGroup
	)
	record := func(destination *[]time.Duration, elapsed time.Duration) {
		mu.Lock()
		*destination = append(*destination, elapsed)
		mu.Unlock()
	}

	for index := 0; index < submitRounds; index++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			started := time.Now()
			_, err := callBusyHTTP(t, client, server.URL, http.MethodPost, "/api/operations",
				requestBudget, map[string]any{
					"type": "import_text", "commandSchemaVersion": 1,
					"target": map[string]any{"baseId": baseID},
					"input": map[string]any{
						"title":   fmt.Sprintf("Busy Request %02d", index),
						"content": fmt.Sprintf("request %02d must not survive an uncertain writer", index),
					},
					"idempotencyKey": fmt.Sprintf("busy-http-submit-%02d", index),
				})
			record(&submitLatencies, time.Since(started))
			if err == nil {
				t.Errorf("busy submit %02d succeeded from an unavailable writer", index)
			}
		}(index)
	}
	for index := 0; index < cancelRounds; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			started := time.Now()
			_, err := callBusyHTTP(t, client, server.URL, http.MethodPost,
				"/api/operations/"+operationID+"/cancel", requestBudget, map[string]any{})
			record(&cancelLatencies, time.Since(started))
			if err == nil {
				t.Error("cancel reported success while its durable intent could not commit")
			}
		}()
	}
	for index := 0; index < statusRounds; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			started := time.Now()
			response, err := callBusyHTTP(t, client, server.URL, http.MethodGet,
				"/api/operations/"+operationID, requestBudget, nil)
			record(&statusLatencies, time.Since(started))
			if err != nil {
				t.Errorf("status read failed while WAL reader was available: %v", err)
				return
			}
			if response.status != http.StatusOK {
				t.Errorf("status status = %d", response.status)
			}
		}()
	}
	wg.Wait()

	// No uncertain HTTP response may have leaked a committed command or a
	// partial cancellation mutation. The outer caller can safely retry the
	// same idempotency key and cancellation transition after recovery.
	time.Sleep(30 * time.Millisecond)
	var leaked, cancelRows int
	if err := s.app.DB.QueryRow(`SELECT COUNT(*) FROM operations
		WHERE idempotency_key LIKE 'busy-http-submit-%'`).Scan(&leaked); err != nil {
		t.Fatal(err)
	}
	if err := s.app.DB.QueryRow(`SELECT COUNT(*) FROM operations
		WHERE id = ? AND cancel_requested = 1`, operationID).Scan(&cancelRows); err != nil {
		t.Fatal(err)
	}
	if leaked != 0 || cancelRows != 0 {
		t.Fatalf("busy responses leaked durable writes: operations=%d cancellations=%d", leaked, cancelRows)
	}

	if err := lockTx.Rollback(); err != nil {
		t.Fatal(err)
	}

	submitP95 := percentileDuration(submitLatencies, .95)
	statusP95 := percentileDuration(statusLatencies, .95)
	cancelP95 := percentileDuration(cancelLatencies, .95)
	if submitP95 > 250*time.Millisecond || statusP95 > 100*time.Millisecond ||
		cancelP95 > 250*time.Millisecond {
		t.Fatalf("unbounded busy HTTP control path: submitP95=%s statusP95=%s cancelP95=%s",
			submitP95, statusP95, cancelP95)
	}

	// The response-loss retry reuses the original key; cancellation retries the
	// same operation transition. Both are idempotent after the writer recovers.
	retryStarted := time.Now()
	response, err := callBusyHTTP(t, client, server.URL, http.MethodPost, "/api/operations",
		2*time.Second, map[string]any{
			"type": "import_text", "commandSchemaVersion": 1,
			"target": map[string]any{"baseId": baseID},
			"input": map[string]any{
				"title":   "Recovered After Busy Writer",
				"content": "the original key survives an unavailable writer",
			},
			"idempotencyKey": "busy-http-submit-00",
		})
	if err != nil || response.status != http.StatusAccepted {
		t.Fatalf("post-recovery retry = (%d) %v %v", response.status, response.value, err)
	}
	retry := response.value
	if retry["operationId"] == "" {
		t.Fatalf("post-recovery response omitted operation ID: %v", retry)
	}
	_, err = callBusyHTTP(t, client, server.URL, http.MethodPost,
		"/api/operations/"+operationID+"/cancel", 2*time.Second, map[string]any{})
	if err != nil {
		t.Fatalf("post-recovery cancel: %v", err)
	}
	// The cancellation intent is durable before the executor observes it. Let
	// the deliberately slow embedding stage return so cleanup can converge.
	cancellingDeadline := time.Now().Add(2 * time.Second)
	for {
		op, err := s.app.Operations.Get(operationID)
		if err != nil {
			t.Fatal(err)
		}
		if op.State == operations.StateCancelling {
			break
		}
		if op.State == operations.StateCancelled {
			break
		}
		if op.State == operations.StateSucceeded || op.State == operations.StateFailed {
			t.Fatalf("cancellation moved to %s before worker release", op.State)
		}
		if time.Now().After(cancellingDeadline) {
			t.Fatalf("cancellation state = %s before worker release", op.State)
		}
		time.Sleep(10 * time.Millisecond)
	}
	close(blocked.release)
	deadline := time.Now().Add(5 * time.Second)
	for {
		op, err := s.app.Operations.Get(operationID)
		if err != nil {
			t.Fatal(err)
		}
		if op.State == operations.StateCancelled {
			break
		}
		if op.State == operations.StateSucceeded || op.State == operations.StateFailed {
			t.Fatalf("recovered cancellation converged to %s", op.State)
		}
		if time.Now().After(deadline) {
			t.Fatalf("recovered cancellation state = %s", op.State)
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancelDrain := time.Since(retryStarted)
	if cancelDrain > 5*time.Second {
		t.Fatalf("writer recovery drain = %s", cancelDrain)
	}

	var acceptedCommands, submittedEvents int
	if err := s.app.DB.QueryRow(`SELECT COUNT(*) FROM operations
		WHERE idempotency_key LIKE 'busy-http-submit-%'`).Scan(&acceptedCommands); err != nil {
		t.Fatal(err)
	}
	if err := s.app.DB.QueryRow(`SELECT COUNT(*) FROM operation_events
		WHERE operation_id = ? AND kind = 'submitted'`, retry["operationId"]).Scan(&submittedEvents); err != nil {
		t.Fatal(err)
	}
	if acceptedCommands != 1 || submittedEvents != 1 {
		t.Fatalf("writer recovery duplicated acceptance: operations=%d submitted=%d",
			acceptedCommands, submittedEvents)
	}
	t.Logf("busy HTTP rounds submit=%d status=%d cancel=%d submitP95=%s statusP95=%s cancelP95=%s drain=%s",
		submitRounds, statusRounds, cancelRounds, submitP95, statusP95, cancelP95, cancelDrain)
}
