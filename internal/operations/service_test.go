package operations

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/shutu-ai/shutu-knowledge/internal/storage"
)

func newTestService(t *testing.T, calls *int, mu *sync.Mutex) *Service {
	t.Helper()
	db, err := storage.Open(filepath.Join(t.TempDir(), "operations.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	service, err := New(db, 1)
	if err != nil {
		t.Fatal(err)
	}
	service.Register("echo", func(_ context.Context, _ Operation, payload json.RawMessage, _ func(Progress)) (any, error) {
		mu.Lock()
		*calls++
		mu.Unlock()
		var value map[string]any
		if err := json.Unmarshal(payload, &value); err != nil {
			return nil, err
		}
		return value, nil
	})
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Stop)
	return service
}

func waitForState(t *testing.T, service *Service, id, state string) Operation {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		op, err := service.Get(id)
		if err == nil && op.State == state {
			return op
		}
		time.Sleep(5 * time.Millisecond)
	}
	op, err := service.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	t.Fatalf("operation state = %s, want %s: %s %s", op.State, state, op.ErrorCode, op.ErrorMessage)
	return op
}

func firstMapKey(values map[string]int) string {
	for key := range values {
		return key
	}
	return ""
}

func TestSubmitIsDurableAndIdempotent(t *testing.T) {
	var calls int
	var mu sync.Mutex
	service := newTestService(t, &calls, &mu)
	payload := json.RawMessage(`{"value":42}`)
	first, err := service.Submit(context.Background(), Request{
		Type: "echo", CommandSchemaVersion: CommandSchemaV1,
		Payload: payload, IdempotencyKey: "same-key", TotalUnits: intPtr(1),
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForState(t, service, first.ID, StateSucceeded)
	second, err := service.Submit(context.Background(), Request{
		Type: "echo", CommandSchemaVersion: CommandSchemaV1,
		Payload: payload, IdempotencyKey: "same-key", TotalUnits: intPtr(1),
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID {
		t.Fatalf("idempotent retry created %s, wanted %s", second.ID, first.ID)
	}
	_, err = service.Submit(context.Background(), Request{
		Type: "echo", CommandSchemaVersion: CommandSchemaV1,
		Payload: json.RawMessage(`{"value":43}`), IdempotencyKey: "same-key",
	})
	if !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("fingerprint conflict error = %v", err)
	}
	_, err = service.Submit(context.Background(), Request{
		Type: "echo", CommandSchemaVersion: CommandSchemaV1,
		BaseID: "other-base", Payload: payload, IdempotencyKey: "same-key",
	})
	if !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("target conflict error = %v", err)
	}
	mu.Lock()
	if calls != 1 {
		t.Fatalf("executor calls = %d, want 1", calls)
	}
	mu.Unlock()
}

func TestQueueFullStillReturnsBoundIdempotentOperation(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "queue-full.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	service, err := New(db, 1)
	if err != nil {
		t.Fatal(err)
	}
	var calls int
	var mu sync.Mutex
	service.Register("echo", func(context.Context, Operation, json.RawMessage, func(Progress)) (any, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		return nil, nil
	})
	service.SetQueueLimit(1)

	accepted, err := service.Submit(context.Background(), Request{
		Type: "echo", CommandSchemaVersion: CommandSchemaV1,
		Payload:        json.RawMessage(`{"value":"first"}`),
		IdempotencyKey: "capacity-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Submit(context.Background(), Request{
		Type: "echo", CommandSchemaVersion: CommandSchemaV1,
		Payload: json.RawMessage(`{"value":"second"}`),
	}); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("over-capacity submit error = %v, want ErrQueueFull", err)
	}
	retried, err := service.Submit(context.Background(), Request{
		Type: "echo", CommandSchemaVersion: CommandSchemaV1,
		Payload:        json.RawMessage(`{"value":"first"}`),
		IdempotencyKey: "capacity-key",
	})
	if err != nil {
		t.Fatalf("bound idempotent retry after queue filled: %v", err)
	}
	if retried.ID != accepted.ID {
		t.Fatalf("capacity retry returned %s, wanted original %s", retried.ID, accepted.ID)
	}

	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Stop)
	waitForState(t, service, accepted.ID, StateSucceeded)
	mu.Lock()
	if calls != 1 {
		mu.Unlock()
		t.Fatalf("executor calls = %d, want 1", calls)
	}
	mu.Unlock()
}

func TestBusyWriterBoundedFailureAndIdempotentRetry(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "busy-writer.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	var calls int
	var mu sync.Mutex
	service, err := New(db, 1)
	if err != nil {
		t.Fatal(err)
	}
	service.Register("echo", func(_ context.Context, _ Operation, _ json.RawMessage, _ func(Progress)) (any, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		return nil, nil
	})

	// Hold SQLite's writer lock outside the service. Reads of durable rows can
	// continue in WAL mode, but acceptance must wait on this lock or fail
	// bounded; it must never report success from memory.
	lockDB, err := sql.Open("sqlite", fmt.Sprintf(
		"file:%s?_pragma=busy_timeout(50)", filepath.ToSlash(dbPath)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lockDB.Close() })
	lockConn, err := lockDB.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lockConn.Close() })
	if _, err := lockConn.ExecContext(context.Background(), `PRAGMA busy_timeout = 50`); err != nil {
		t.Fatal(err)
	}
	if _, err := lockConn.ExecContext(context.Background(), `BEGIN IMMEDIATE`); err != nil {
		t.Fatal(err)
	}

	request := Request{
		Type: "echo", CommandSchemaVersion: CommandSchemaV1,
		Payload:        json.RawMessage(`{"value":"busy"}`),
		IdempotencyKey: "busy-key",
	}
	started := time.Now()
	submitCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	_, firstErr := service.Submit(submitCtx, request)
	cancel()
	if firstErr == nil {
		t.Fatal("submission succeeded despite an unavailable SQLite writer")
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("busy submission returned after %s, want a bounded control-path failure", elapsed)
	}

	// A failed call may not leak a committed command while the writer is still
	// unavailable. Waiting briefly also makes a late commit visible.
	time.Sleep(25 * time.Millisecond)
	var leaked int
	if err := db.QueryRow(`SELECT COUNT(*) FROM operations WHERE idempotency_key = ?`,
		request.IdempotencyKey).Scan(&leaked); err != nil {
		t.Fatal(err)
	}
	if leaked != 0 {
		t.Fatalf("failed submission leaked %d operation(s)", leaked)
	}
	if _, err := lockConn.ExecContext(context.Background(), `ROLLBACK`); err != nil {
		t.Fatal(err)
	}

	retried, err := service.Submit(context.Background(), request)
	if err != nil {
		t.Fatalf("idempotent retry after writer recovery: %v", err)
	}
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Stop)
	waitForState(t, service, retried.ID, StateSucceeded)
	events, err := service.Events(context.Background(), retried.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	submitted := 0
	for _, event := range events {
		if event.Kind == "submitted" {
			submitted++
		}
	}
	if submitted != 1 {
		t.Fatalf("submitted events = %d, want 1", submitted)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("executor calls = %d, want 1", calls)
	}
}

func TestExpiredTerminalIdempotencyBindingReturnsExplicitError(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "expired-idempotency.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	var calls int
	var mu sync.Mutex
	service, err := New(db, 1)
	if err != nil {
		t.Fatal(err)
	}
	service.SetIdempotencyRetentionHours(1)
	service.Register("echo", func(_ context.Context, _ Operation, _ json.RawMessage, _ func(Progress)) (any, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		return nil, nil
	})
	request := Request{
		Type: "echo", CommandSchemaVersion: CommandSchemaV1,
		Payload:        json.RawMessage(`{"value":"expired"}`),
		IdempotencyKey: "expired-key",
	}
	accepted, err := service.Submit(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	service.Start(context.Background())
	t.Cleanup(service.Stop)
	waitForState(t, service, accepted.ID, StateSucceeded)
	terminal, err := service.Get(accepted.ID)
	if err != nil {
		t.Fatal(err)
	}
	if terminal.IdempotencyExpiresAt == nil {
		t.Fatal("terminal operation did not receive an idempotency expiry")
	}
	if _, err := service.Submit(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE operations SET idempotency_expires_at = ? WHERE id = ?`,
		time.Now().UnixMilli()-1, accepted.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Submit(context.Background(), request); !errors.Is(err, ErrOperationExpired) {
		t.Fatalf("expired retry error = %v, want ErrOperationExpired", err)
	}
	found, foundAny, err := service.FindIdempotencyOperation(request)
	if !errors.Is(err, ErrOperationExpired) || foundAny || found.ID != "" {
		t.Fatalf("expired lookup = %+v, %v, %v", found, foundAny, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("expired retry executed executor %d additional time(s)", calls-1)
	}
}

func TestConcurrentSameKeySubmitsReturnOneOperation(t *testing.T) {
	var calls int
	var mu sync.Mutex
	service := newTestService(t, &calls, &mu)
	request := Request{
		Type: "echo", CommandSchemaVersion: CommandSchemaV1,
		Payload:        json.RawMessage(`{"value":"concurrent"}`),
		IdempotencyKey: "concurrent-key",
	}

	const callers = 16
	results := make(chan struct {
		op  Operation
		err error
	}, callers)
	for range callers {
		go func() {
			op, err := service.Submit(context.Background(), request)
			results <- struct {
				op  Operation
				err error
			}{op, err}
		}()
	}

	ids := make(map[string]int)
	for range callers {
		result := <-results
		if result.err != nil {
			t.Fatalf("concurrent idempotent submit: %v", result.err)
		}
		ids[result.op.ID]++
	}
	if len(ids) != 1 {
		t.Fatalf("concurrent retry produced %d operations: %v", len(ids), ids)
	}
	waitForState(t, service, firstMapKey(ids), StateSucceeded)
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("executor calls = %d, want 1", calls)
	}
}

func TestStartRejectsDuplicateServiceStart(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "duplicate-start.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	service, err := New(db, 1)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	service.Register("block", func(context.Context, Operation, json.RawMessage, func(Progress)) (any, error) {
		close(started)
		<-release
		return nil, nil
	})
	if _, err := service.Submit(context.Background(), Request{
		Type: "block", CommandSchemaVersion: CommandSchemaV1,
		Payload: json.RawMessage(`{}`),
	}); err != nil {
		t.Fatal(err)
	}

	startDone := make(chan error, 1)
	go func() { startDone <- service.Start(context.Background()) }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("operation service did not start")
	}
	if err := service.Start(context.Background()); err == nil {
		t.Fatal("duplicate Start was accepted")
	}
	close(release)
	if err := <-startDone; err != nil {
		t.Fatal(err)
	}
	service.Stop()
}

func TestStartRecoveryKeepsNewSubmissionQueued(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "recovery-race.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	var calls int
	var mu sync.Mutex
	service, err := New(db, 1)
	if err != nil {
		t.Fatal(err)
	}
	service.Register("echo", func(_ context.Context, _ Operation, _ json.RawMessage, _ func(Progress)) (any, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		return nil, nil
	})

	lost, err := service.Submit(context.Background(), Request{
		Type: "echo", CommandSchemaVersion: CommandSchemaV1,
		Payload: json.RawMessage(`{"value":"lost"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	// Simulate the durable pre-dispatch breakpoint: another process claimed
	// the command and died before running it.
	if _, err := db.Exec(`UPDATE operations SET state = ?, attempt = 1,
		state_revision = state_revision + 1 WHERE id = ?`, StateRunning, lost.ID); err != nil {
		t.Fatal(err)
	}
	fresh, err := service.Submit(context.Background(), Request{
		Type: "echo", CommandSchemaVersion: CommandSchemaV1,
		Payload: json.RawMessage(`{"value":"fresh"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Stop)
	recovered := waitForState(t, service, lost.ID, StateSucceeded)
	waitForState(t, service, fresh.ID, StateSucceeded)
	if recovered.Attempt != 2 {
		t.Fatalf("recovered attempt = %d, want 2", recovered.Attempt)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 2 {
		t.Fatalf("executor calls = %d, want one for recovery and one for the new command", calls)
	}
}

func TestRestartReplaysInterruptedCommand(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "operations.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var calls int
	var mu sync.Mutex
	newService := func() *Service {
		service, err := New(db, 1)
		if err != nil {
			t.Fatal(err)
		}
		service.Register("echo", func(_ context.Context, _ Operation, _ json.RawMessage, _ func(Progress)) (any, error) {
			mu.Lock()
			calls++
			mu.Unlock()
			return map[string]any{"replayed": true}, nil
		})
		return service
	}
	first := newService()
	if err := first.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	op, err := first.Submit(context.Background(), Request{
		Type: "echo", CommandSchemaVersion: CommandSchemaV1,
		Payload: json.RawMessage(`{"value":"restart"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForState(t, first, op.ID, StateSucceeded)
	first.Stop()
	// Simulate acceptance and claim followed by a process crash before the
	// terminal write. Only the durable command and attempt are available.
	if _, err := db.Exec(`UPDATE operations SET state = ?, attempt = 1 WHERE id = ?`,
		StateRunning, op.ID); err != nil {
		t.Fatal(err)
	}
	second := newService()
	if err := second.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer second.Stop()
	waitForState(t, second, op.ID, StateSucceeded)
	mu.Lock()
	replayed := calls
	mu.Unlock()
	if replayed < 2 {
		t.Fatalf("executor calls = %d, want restarted command to execute again", replayed)
	}
}

func TestRetryHonorsAttemptLimitAndRecordsEvents(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "operations.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	service, err := New(db, 1)
	if err != nil {
		t.Fatal(err)
	}
	service.Register("fail", func(_ context.Context, _ Operation, _ json.RawMessage, _ func(Progress)) (any, error) {
		return nil, errors.New("intentional failure")
	})
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Stop)
	op, err := service.Submit(context.Background(), Request{
		Type: "fail", CommandSchemaVersion: CommandSchemaV1,
		Payload: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForState(t, service, op.ID, StateFailed)
	for want := 2; want <= DefaultMaxAttempts; want++ {
		retried, err := service.Retry(op.ID)
		if err != nil {
			t.Fatal(err)
		}
		current := waitForState(t, service, retried.ID, StateFailed)
		if current.Attempt != want {
			t.Fatalf("attempt = %d, want %d", current.Attempt, want)
		}
	}
	if _, err := service.Retry(op.ID); err == nil {
		t.Fatal("retry exceeded maximum attempts")
	}
	events, err := service.Events(context.Background(), op.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	kinds := make([]string, 0, len(events))
	for _, event := range events {
		kinds = append(kinds, event.Kind)
	}
	want := []string{"submitted", "claimed", "finished"}
	for attempt := 2; attempt <= DefaultMaxAttempts; attempt++ {
		want = append(want, "retry_queued", "claimed", "finished")
	}
	if !slices.Equal(kinds, want) {
		t.Fatalf("events = %v, want %v", kinds, want)
	}
}

func TestStaleAttemptProgressAndFinishAreIgnored(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "stale-attempt.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	service, err := New(db, 1)
	if err != nil {
		t.Fatal(err)
	}
	service.Register("echo", func(context.Context, Operation, json.RawMessage, func(Progress)) (any, error) {
		return nil, nil
	})
	op, err := service.Submit(context.Background(), Request{
		Type: "echo", CommandSchemaVersion: CommandSchemaV1,
		Payload: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	// Simulate attempt one owned by a live worker and attempt two issued after
	// an explicit retry in another owner. The old callback arrives last.
	if _, err := db.Exec(`UPDATE operations SET state = ?, attempt = 1,
		state_revision = state_revision + 1, started_at = ? WHERE id = ?`,
		StateRunning, now(), op.ID); err != nil {
		t.Fatal(err)
	}

	if err := service.report(op.ID, 2, Progress{Phase: "stale", Completed: 9, Total: 9}); err != nil {
		t.Fatal(err)
	}
	staleProgress, err := service.Get(op.ID)
	if err != nil {
		t.Fatal(err)
	}
	if staleProgress.Attempt != 1 || staleProgress.CompletedUnits != 0 || staleProgress.Phase != "" {
		t.Fatalf("stale progress was applied: %+v", staleProgress)
	}
	if err := service.finish(op.ID, 2, map[string]any{"stale": true}, nil); err != nil {
		t.Fatal(err)
	}
	staleFinish, err := service.Get(op.ID)
	if err != nil {
		t.Fatal(err)
	}
	if staleFinish.State != StateRunning || staleFinish.StateRevision != op.StateRevision+1 {
		t.Fatalf("stale finish was applied: %+v", staleFinish)
	}

	if err := service.report(op.ID, 1, Progress{Phase: "working", Completed: 3, Total: 5}); err != nil {
		t.Fatal(err)
	}
	progress, err := service.Get(op.ID)
	if err != nil {
		t.Fatal(err)
	}
	if progress.Phase != "working" || progress.CompletedUnits != 3 || progress.TotalUnits == nil || *progress.TotalUnits != 5 {
		t.Fatalf("current attempt progress was not applied: %+v", progress)
	}
	if err := service.finish(op.ID, 1, map[string]any{"current": true}, nil); err != nil {
		t.Fatal(err)
	}
	final, err := service.Get(op.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.State != StateSucceeded || final.Attempt != 1 {
		t.Fatalf("current attempt did not finish: %+v", final)
	}
}

func TestCancelQueuedOperationWithoutRunningIt(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "cancel-queued.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	service, err := New(db, 1)
	if err != nil {
		t.Fatal(err)
	}
	var calls int
	var mu sync.Mutex
	service.Register("echo", func(context.Context, Operation, json.RawMessage, func(Progress)) (any, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		return nil, nil
	})

	op, err := service.Submit(context.Background(), Request{
		Type: "echo", CommandSchemaVersion: CommandSchemaV1,
		Payload: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	cancelled, err := service.Cancel(op.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.State != StateCancelled || cancelled.Cancellable || cancelled.Attempt != 0 {
		t.Fatalf("queued cancel state: %+v", cancelled)
	}
	if _, err := service.Retry(op.ID); err == nil {
		t.Fatal("cancelled operation accepted retry")
	}

	// The durable row is terminal before any dispatcher can claim it. A short
	// wait makes an accidental claim visible without depending on exact timing.
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Stop)
	time.Sleep(30 * time.Millisecond)
	mu.Lock()
	if calls != 0 {
		mu.Unlock()
		t.Fatalf("cancelled queued operation ran %d time(s)", calls)
	}
	mu.Unlock()

	events, err := service.Events(context.Background(), op.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	kinds := operationEventKinds(events)
	if want := []string{"submitted", "cancel_requested", "finished"}; !slices.Equal(kinds, want) {
		t.Fatalf("cancel events = %v, want %v", kinds, want)
	}
}

func TestCancelRunningOperationAndRejectRepeatRetry(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "cancel-running.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	service, err := New(db, 1)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	var calls int
	var mu sync.Mutex
	service.Register("block", func(ctx context.Context, _ Operation, _ json.RawMessage, _ func(Progress)) (any, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		close(started)
		<-release
		return nil, ctx.Err()
	})
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Stop)

	op, err := service.Submit(context.Background(), Request{
		Type: "block", CommandSchemaVersion: CommandSchemaV1,
		Payload: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("running operation did not start")
	}
	cancelling, err := service.Cancel(op.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cancelling.State != StateCancelling || !cancelling.CancelRequested {
		t.Fatalf("running cancel state: %+v", cancelling)
	}
	close(release)
	cancelled := waitForState(t, service, op.ID, StateCancelled)
	if cancelled.Attempt != 1 || cancelled.Cancellable {
		t.Fatalf("cancelled running state: %+v", cancelled)
	}
	repeated, err := service.Cancel(op.ID)
	if err != nil {
		t.Fatal(err)
	}
	if repeated.State != StateCancelled || repeated.StateRevision != cancelled.StateRevision {
		t.Fatalf("repeat cancel mutated terminal state: %+v", repeated)
	}
	if _, err := service.Retry(op.ID); err == nil {
		t.Fatal("cancelled operation accepted retry")
	}
	mu.Lock()
	if calls != 1 {
		mu.Unlock()
		t.Fatalf("executor calls = %d, want 1", calls)
	}
	mu.Unlock()

	events, err := service.Events(context.Background(), op.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	kinds := operationEventKinds(events)
	if want := []string{"submitted", "claimed", "cancel_requested", "finished"}; !slices.Equal(kinds, want) {
		t.Fatalf("cancel events = %v, want %v", kinds, want)
	}
}

func TestPublishedResultWinsCommittedCancelIntent(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "cancel-publish-race.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	service, err := New(db, 1)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	service.Register("publish", func(context.Context, Operation, json.RawMessage, func(Progress)) (any, error) {
		close(started)
		<-release
		// The intent context has fired, but publication and its result are
		// already authoritative. Cancellation must not roll this back.
		return map[string]any{"published": true}, nil
	})
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Stop)

	op, err := service.Submit(context.Background(), Request{
		Type: "publish", CommandSchemaVersion: CommandSchemaV1,
		Payload: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("publishing operation did not start")
	}
	cancelling, err := service.Cancel(op.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cancelling.State != StateCancelling || !cancelling.CancelRequested {
		t.Fatalf("cancel intent state: %+v", cancelling)
	}
	cancelRevision := cancelling.StateRevision
	close(release)

	published := waitForState(t, service, op.ID, StateSucceeded)
	if published.StateRevision <= cancelRevision {
		t.Fatalf("published terminal revision = %d, want > %d", published.StateRevision, cancelRevision)
	}
	if !published.CancelRequested || published.Cancellable {
		t.Fatalf("published operation cancel flags: %+v", published)
	}
	var result map[string]any
	if err := json.Unmarshal(published.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result["published"] != true {
		t.Fatalf("published result was lost: %+v", result)
	}
	repeated, err := service.Cancel(op.ID)
	if err != nil {
		t.Fatal(err)
	}
	if repeated.State != StateSucceeded || repeated.StateRevision != published.StateRevision {
		t.Fatalf("repeat cancel mutated published result: %+v", repeated)
	}
	if _, err := service.Retry(op.ID); err == nil {
		t.Fatal("published terminal operation accepted retry")
	}

	events, err := service.Events(context.Background(), op.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	kinds := operationEventKinds(events)
	if want := []string{"submitted", "claimed", "cancel_requested", "finished"}; !slices.Equal(kinds, want) {
		t.Fatalf("events = %v, want %v", kinds, want)
	}
}

func TestRestartHonorsPersistedCancelIntentWithoutReplay(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "cancel-recovery.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	var calls int
	var mu sync.Mutex
	newService := func() *Service {
		service, err := New(db, 1)
		if err != nil {
			t.Fatal(err)
		}
		service.Register("echo", func(context.Context, Operation, json.RawMessage, func(Progress)) (any, error) {
			mu.Lock()
			calls++
			mu.Unlock()
			return nil, nil
		})
		return service
	}
	first := newService()
	if err := first.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	op, err := first.Submit(context.Background(), Request{
		Type: "echo", CommandSchemaVersion: CommandSchemaV1,
		Payload: json.RawMessage(`{"value":"cancel-recovery"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForState(t, first, op.ID, StateSucceeded)
	first.Stop()

	// Recreate the durable breakpoint: a worker claimed the command, cancel
	// was committed, but the process died before the terminal state was written.
	simulatedRevision := op.StateRevision + 2
	if _, err := db.Exec(`UPDATE operations SET state = ?, cancel_requested = 1,
		attempt = 1, state_revision = ?, finished_at = NULL WHERE id = ?`,
		StateRunning, simulatedRevision, op.ID); err != nil {
		t.Fatal(err)
	}

	second := newService()
	if err := second.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(second.Stop)
	recovered := waitForState(t, second, op.ID, StateCancelled)
	if recovered.Attempt != 1 || recovered.StateRevision <= simulatedRevision {
		t.Fatalf("recovered cancellation = %+v, simulated revision %d", recovered, simulatedRevision)
	}
	events, err := second.Events(context.Background(), op.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 || events[len(events)-1].Kind != "finished" {
		t.Fatalf("recovery did not persist a terminal event: %+v", events)
	}
	mu.Lock()
	if calls != 1 {
		mu.Unlock()
		t.Fatalf("recovery replayed cancelled operation %d time(s)", calls-1)
	}
	mu.Unlock()
}

func operationEventKinds(events []operationEvent) []string {
	kinds := make([]string, 0, len(events))
	for _, event := range events {
		kinds = append(kinds, event.Kind)
	}
	return kinds
}

func intPtr(value int) *int { return &value }
