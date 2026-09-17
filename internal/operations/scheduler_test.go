package operations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	sharedscheduler "github.com/shutu-ai/shutu-knowledge/internal/scheduler"
	"github.com/shutu-ai/shutu-knowledge/internal/storage"
)

func TestSchedulerReservesDeclaredByteBudgets(t *testing.T) {
	scheduler := newScheduler(ResourceLimits{
		IO: 1, DBWrite: 1, Disk: 1, Network: 1, Model: 1, Maintenance: 1,
		MaxPerBase: 2, MemoryBytes: 100, DiskBytes: 100, TempBytes: 100,
	})
	budget := ResourceBudget{MemoryBytes: 60, DiskBytes: 70, TempBytes: 80}
	if !scheduler.reserve("base-a", budget) {
		t.Fatal("first byte reservation was rejected")
	}
	if scheduler.reserve("base-b", ResourceBudget{MemoryBytes: 41, DiskBytes: 30, TempBytes: 20}) {
		t.Fatal("reservation exceeded aggregate byte budget")
	}
	snapshot := scheduler.snapshot(nil)
	if snapshot.ReservedMemoryBytes != 60 || snapshot.ReservedDiskBytes != 70 || snapshot.ReservedTempBytes != 80 {
		t.Fatalf("reserved bytes = %d/%d/%d, want 60/70/80", snapshot.ReservedMemoryBytes, snapshot.ReservedDiskBytes, snapshot.ReservedTempBytes)
	}
	scheduler.release("base-a", budget)
	if !scheduler.reserve("base-b", ResourceBudget{MemoryBytes: 41, DiskBytes: 30, TempBytes: 20}) {
		t.Fatal("reservation was not released after operation completion")
	}
}

func TestPriorityAgingPreventsStarvation(t *testing.T) {
	const at int64 = 20 * 1000
	oldLow := effectivePriority(0, 0, at)
	newHigh := effectivePriority(10, 19*1000, at)
	if oldLow < newHigh {
		t.Fatalf("aged low priority = %d, new high priority = %d", oldLow, newHigh)
	}
	if effectivePriority(0, at, at) != 0 {
		t.Fatal("future or current requests must not receive an aging bonus")
	}
}

func TestResourceLanesRunIndependently(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "scheduler.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	service, err := NewWithUploadRoot(db, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	service.SetResourceLimits(ResourceLimits{
		IO: 1, DBWrite: 1, Disk: 1, Network: 1,
		Model: 1, Maintenance: 1, MaxPerBase: 2,
	})

	started := make(chan struct{})
	blockCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		service.Stop()
	})
	service.Register("block_io", func(context.Context, Operation, json.RawMessage, func(Progress)) (any, error) {
		close(started)
		<-blockCtx.Done()
		return nil, blockCtx.Err()
	})
	service.Register("network_work", func(context.Context, Operation, json.RawMessage, func(Progress)) (any, error) {
		return map[string]any{"ok": true}, nil
	})
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	ioOperation, err := service.Submit(context.Background(), Request{
		Type: "block_io", CommandSchemaVersion: CommandSchemaV1,
		ResourceClass: ResourceIO, Payload: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("io lane did not start")
	}
	networkOperation, err := service.Submit(context.Background(), Request{
		Type: "network_work", CommandSchemaVersion: CommandSchemaV1,
		ResourceClass: ResourceNetwork, Payload: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		op, err := service.Get(networkOperation.ID)
		if err != nil {
			t.Fatal(err)
		}
		if op.State == StateSucceeded {
			break
		}
		if op.State == StateFailed {
			t.Fatalf("network operation failed: %s", op.ErrorMessage)
		}
		if time.Now().After(deadline) {
			io, _ := service.Get(ioOperation.ID)
			t.Fatalf("network operation state = %s while io = %s", op.State, io.State)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestSchedulerPrioritizesDeletes(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "priority.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	service, err := New(db, 1)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var order []string
	started := make(chan struct{})
	release := make(chan struct{})
	service.Register("block_io", func(context.Context, Operation, json.RawMessage, func(Progress)) (any, error) {
		mu.Lock()
		order = append(order, "block")
		mu.Unlock()
		close(started)
		<-release
		return nil, nil
	})
	service.Register("reindex_document", func(context.Context, Operation, json.RawMessage, func(Progress)) (any, error) {
		mu.Lock()
		order = append(order, "reindex")
		mu.Unlock()
		return nil, nil
	})
	service.Register("delete_document", func(context.Context, Operation, json.RawMessage, func(Progress)) (any, error) {
		mu.Lock()
		order = append(order, "delete")
		mu.Unlock()
		return nil, nil
	})
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Stop)

	blockOperation, err := service.Submit(context.Background(), Request{
		Type: "block_io", CommandSchemaVersion: CommandSchemaV1,
		BaseID: "base-snapshot", ResourceClass: ResourceIO, Payload: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("blocking operation did not start")
	}
	if _, err := service.Submit(context.Background(), Request{
		Type: "reindex_document", CommandSchemaVersion: CommandSchemaV1,
		Payload: json.RawMessage(`{}`),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Submit(context.Background(), Request{
		Type: "delete_document", CommandSchemaVersion: CommandSchemaV1,
		Payload: json.RawMessage(`{}`),
	}); err != nil {
		t.Fatal(err)
	}
	close(release)

	deadline := time.Now().Add(2 * time.Second)
	for {
		op, err := service.Get(blockOperation.ID)
		if err != nil {
			t.Fatal(err)
		}
		if op.State == StateSucceeded || op.State == StateFailed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("blocking operation state = %s", op.State)
		}
		time.Sleep(5 * time.Millisecond)
	}
	deadline = time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		done := len(order) == 3
		mu.Unlock()
		if done {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("operations did not finish: %v", order)
		}
		time.Sleep(5 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if order[1] != "delete" || order[2] != "reindex" {
		t.Fatalf("execution order = %v, want delete before reindex", order)
	}
}

func TestSchedulerSnapshotReportsCapacity(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "snapshot.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	service, err := New(db, 1)
	if err != nil {
		t.Fatal(err)
	}
	service.SetResourceLimits(ResourceLimits{IO: 2, Network: 3, MaxPerBase: 2})
	service.SetQueueLimit(77)

	started := make(chan struct{})
	release := make(chan struct{})
	service.Register("block_io", func(context.Context, Operation, json.RawMessage, func(Progress)) (any, error) {
		close(started)
		<-release
		return nil, nil
	})
	service.Register("reindex_document", func(context.Context, Operation, json.RawMessage, func(Progress)) (any, error) {
		return nil, nil
	})
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Stop)

	blockOperation, err := service.Submit(context.Background(), Request{
		Type: "block_io", CommandSchemaVersion: CommandSchemaV1,
		BaseID: "base-snapshot", ResourceClass: ResourceIO, Payload: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("blocking operation did not start")
	}
	if _, err := service.Submit(context.Background(), Request{
		Type: "reindex_document", CommandSchemaVersion: CommandSchemaV1,
		ResourceClass: ResourceIO, Payload: json.RawMessage(`{}`),
	}); err != nil {
		t.Fatal(err)
	}

	snapshot, err := service.Scheduler()
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.QueueLimit != 77 || snapshot.MaxPerBase != 2 || snapshot.ActiveTotal != 1 || snapshot.QueuedTotal != 1 {
		t.Fatalf("unexpected snapshot totals: %+v", snapshot)
	}
	if snapshot.Lanes[ResourceIO].Limit != 2 || snapshot.Lanes[ResourceIO].Active != 1 || snapshot.Lanes[ResourceIO].Queued != 1 {
		t.Fatalf("unexpected io lane: %+v", snapshot.Lanes[ResourceIO])
	}
	if snapshot.Lanes[ResourceNetwork].Limit != 3 || snapshot.Lanes[ResourceNetwork].Active != 0 {
		t.Fatalf("unexpected network lane: %+v", snapshot.Lanes[ResourceNetwork])
	}
	if snapshot.ActiveBases["base-snapshot"] != 1 {
		t.Fatalf("active bases: %+v", snapshot.ActiveBases)
	}
	close(release)
	_ = blockOperation
}

func percentileLatency(values []time.Duration, percentile float64) time.Duration {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]time.Duration(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	index := int(float64(len(sorted)-1) * percentile)
	return sorted[index]
}

func TestSustainedOverloadRejectsBoundedAndDrains(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "sustained-overload.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	service, err := New(db, 1)
	if err != nil {
		t.Fatal(err)
	}
	const queueLimit = 20
	service.SetQueueLimit(queueLimit)

	started := make(chan struct{})
	release := make(chan struct{})
	var mu sync.Mutex
	executions := 0
	service.Register("block_io", func(context.Context, Operation, json.RawMessage, func(Progress)) (any, error) {
		close(started)
		<-release
		return nil, nil
	})
	service.Register("queued_work", func(context.Context, Operation, json.RawMessage, func(Progress)) (any, error) {
		mu.Lock()
		executions++
		mu.Unlock()
		return map[string]any{"ok": true}, nil
	})
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Stop)

	block, err := service.Submit(context.Background(), Request{
		Type: "block_io", CommandSchemaVersion: CommandSchemaV1,
		BaseID: "overload-base", ResourceClass: ResourceIO,
		Payload: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("blocking operation did not start")
	}

	// Fill the finite durable queue under sustained distinct demand. Only
	// SQLite-inserted operations count as accepted; no rejection can allocate
	// a command or event.
	var accepted []Operation
	var submitLatencies, rejectionLatencies []time.Duration
	for index := range 64 {
		key := fmt.Sprintf("overload-%02d", index)
		startedAt := time.Now()
		op, err := service.Submit(context.Background(), Request{
			Type: "queued_work", CommandSchemaVersion: CommandSchemaV1,
			BaseID: "overload-base", ResourceClass: ResourceIO,
			Payload:        json.RawMessage(fmt.Sprintf(`{"index":%d}`, index)),
			IdempotencyKey: key,
		})
		elapsed := time.Since(startedAt)
		switch {
		case err == nil:
			accepted = append(accepted, op)
			submitLatencies = append(submitLatencies, elapsed)
		case errors.Is(err, ErrQueueFull):
			rejectionLatencies = append(rejectionLatencies, elapsed)
		default:
			t.Fatalf("unexpected submit error: %v", err)
		}
	}
	if got := len(accepted); got != queueLimit-1 {
		t.Fatalf("accepted queued operations = %d, want %d", got, queueLimit-1)
	}
	if len(rejectionLatencies) == 0 {
		t.Fatal("sustained demand produced no bounded rejections")
	}
	initialRejections := 64 - len(accepted)
	if snapshot, err := service.Scheduler(); err != nil {
		t.Fatal(err)
	} else if snapshot.QueuedTotal != queueLimit-1 || snapshot.ActiveTotal != 1 {
		t.Fatalf("saturated scheduler = %+v", snapshot)
	}

	// Keep pressure on while checking the two control paths: state visibility
	// must remain bounded, rejected demand must remain bounded, and a valid
	// idempotent retry must not require new admission capacity.
	var statusLatencies, retryLatencies []time.Duration
	for index := range 40 {
		startedAt := time.Now()
		if _, err := service.Scheduler(); err != nil {
			t.Fatal(err)
		}
		if _, err := service.Get(block.ID); err != nil {
			t.Fatal(err)
		}
		statusLatencies = append(statusLatencies, time.Since(startedAt))

		startedAt = time.Now()
		retried, err := service.Submit(context.Background(), Request{
			Type: "queued_work", CommandSchemaVersion: CommandSchemaV1,
			BaseID: "overload-base", ResourceClass: ResourceIO,
			Payload:        json.RawMessage(`{"index":0}`),
			IdempotencyKey: "overload-00",
		})
		if err != nil {
			t.Fatalf("idempotent retry during saturation: %v", err)
		}
		if retried.ID != accepted[0].ID {
			t.Fatalf("saturated retry created %s, wanted %s", retried.ID, accepted[0].ID)
		}
		retryLatencies = append(retryLatencies, time.Since(startedAt))

		startedAt = time.Now()
		if _, err := service.Submit(context.Background(), Request{
			Type: "queued_work", CommandSchemaVersion: CommandSchemaV1,
			BaseID: "overload-base", ResourceClass: ResourceIO,
			Payload:        json.RawMessage(fmt.Sprintf(`{"reject":%d}`, index)),
			IdempotencyKey: fmt.Sprintf("overload-reject-%02d", index),
		}); !errors.Is(err, ErrQueueFull) {
			t.Fatalf("post-saturation submit error = %v, want ErrQueueFull", err)
		}
		rejectionLatencies = append(rejectionLatencies, time.Since(startedAt))
	}

	// Cancellation frees durable capacity before the blocked lane finishes.
	cancelled := accepted[0]
	if _, err := service.Cancel(cancelled.ID); err != nil {
		t.Fatal(err)
	}
	waitForState(t, service, cancelled.ID, StateCancelled)
	freed, err := service.Submit(context.Background(), Request{
		Type: "queued_work", CommandSchemaVersion: CommandSchemaV1,
		BaseID: "overload-base", ResourceClass: ResourceIO,
		Payload:        json.RawMessage(`{"index":64}`),
		IdempotencyKey: "overload-after-cancel",
	})
	if err != nil {
		t.Fatalf("cancellation did not free queue capacity: %v", err)
	}
	accepted = append(accepted, freed)

	close(release)
	drainStarted := time.Now()
	deadline := time.Now().Add(5 * time.Second)
	for {
		rows, err := db.Query(`SELECT state, COUNT(*) FROM operations
			WHERE state IN ('queued','running','cancelling') GROUP BY state`)
		if err != nil {
			t.Fatal(err)
		}
		pending := 0
		for rows.Next() {
			var state string
			var count int
			if err := rows.Scan(&state, &count); err != nil {
				_ = rows.Close()
				t.Fatal(err)
			}
			pending += count
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if pending == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("overloaded queue did not drain: %d pending", pending)
		}
		time.Sleep(5 * time.Millisecond)
	}
	drain := time.Since(drainStarted)
	mu.Lock()
	completed := executions
	mu.Unlock()
	if completed != queueLimit-1 {
		t.Fatalf("effective completions = %d, want %d", completed, queueLimit-1)
	}

	submitP95 := percentileLatency(submitLatencies, .95)
	rejectP95 := percentileLatency(rejectionLatencies, .95)
	statusP95 := percentileLatency(statusLatencies, .95)
	retryP95 := percentileLatency(retryLatencies, .95)
	if submitP95 > time.Second || rejectP95 > time.Second ||
		statusP95 > time.Second || retryP95 > time.Second || drain > 3*time.Second {
		t.Fatalf("unbounded overload control path: submitP95=%s rejectP95=%s statusP95=%s retryP95=%s drain=%s",
			submitP95, rejectP95, statusP95, retryP95, drain)
	}
	t.Logf("overload accepted=%d rejected=%d submitP95=%s rejectP95=%s statusP95=%s retryP95=%s drain=%s throughput=%.1f/s",
		len(accepted)-1, initialRejections+40, submitP95, rejectP95, statusP95, retryP95,
		drain, float64(completed)/drain.Seconds())
}

func TestModelOperationsShareAdmission(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "shared-model.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	service, err := New(db, 1)
	if err != nil {
		t.Fatal(err)
	}
	service.SetResourceLimits(ResourceLimits{Model: 1, MaxPerBase: 2})
	admission := sharedscheduler.NewSemaphore(1)
	service.SetModelAdmission(admission)

	releaseInteractive, err := admission.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	service.Register("model_work", func(context.Context, Operation, json.RawMessage, func(Progress)) (any, error) {
		close(started)
		return map[string]any{"ok": true}, nil
	})
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Stop)

	operation, err := service.Submit(context.Background(), Request{
		Type: "model_work", CommandSchemaVersion: CommandSchemaV1,
		ResourceClass: ResourceModel, Payload: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		current, err := service.Get(operation.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.State == StateRunning {
			break
		}
		if current.State != StateQueued || time.Now().After(deadline) {
			t.Fatalf("shared model operation state = %s", current.State)
		}
		time.Sleep(5 * time.Millisecond)
	}
	select {
	case <-started:
		t.Fatal("model operation ignored the shared interactive admission")
	default:
	}
	releaseInteractive()

	deadline = time.Now().Add(2 * time.Second)
	for {
		current, err := service.Get(operation.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.State == StateSucceeded {
			break
		}
		if current.State != StateRunning || time.Now().After(deadline) {
			t.Fatalf("shared model operation state = %s", current.State)
		}
		time.Sleep(5 * time.Millisecond)
	}
	releaseAfterOperation, err := admission.Acquire(context.Background())
	if err != nil {
		t.Fatalf("operation did not release shared admission: %v", err)
	}
	releaseAfterOperation()
}

func TestPerBaseQuotaDoesNotStarveAnotherBase(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "per-base-fairness.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	service, err := New(db, 2)
	if err != nil {
		t.Fatal(err)
	}
	service.SetResourceLimits(ResourceLimits{IO: 2, DBWrite: 1, Disk: 1, Network: 1, Model: 1, Maintenance: 1, MaxPerBase: 1})
	startedA := make(chan struct{})
	startedB := make(chan struct{})
	releaseA := make(chan struct{})
	var startedAOnce sync.Once
	service.Register("blocked_base_work", func(context.Context, Operation, json.RawMessage, func(Progress)) (any, error) {
		startedAOnce.Do(func() { close(startedA) })
		<-releaseA
		return map[string]any{"ok": true}, nil
	})
	service.Register("quick_base_work", func(context.Context, Operation, json.RawMessage, func(Progress)) (any, error) {
		close(startedB)
		return map[string]any{"ok": true}, nil
	})
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Stop)

	first, err := service.Submit(context.Background(), Request{
		Type: "blocked_base_work", CommandSchemaVersion: CommandSchemaV1,
		BaseID: "base-a", ResourceClass: ResourceIO, Payload: json.RawMessage(`{"slot":1}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-startedA:
	case <-time.After(2 * time.Second):
		t.Fatal("base-a work did not start")
	}
	second, err := service.Submit(context.Background(), Request{
		Type: "blocked_base_work", CommandSchemaVersion: CommandSchemaV1,
		BaseID: "base-a", ResourceClass: ResourceIO, Payload: json.RawMessage(`{"slot":2}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.State != StateQueued {
		t.Fatalf("same-base work state = %s, want queued", second.State)
	}
	other, err := service.Submit(context.Background(), Request{
		Type: "quick_base_work", CommandSchemaVersion: CommandSchemaV1,
		BaseID: "base-b", ResourceClass: ResourceIO, Payload: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-startedB:
	case <-time.After(2 * time.Second):
		t.Fatal("base-b work was starved by base-a quota")
	}
	if current, err := service.Get(second.ID); err != nil {
		t.Fatal(err)
	} else if current.State != StateQueued {
		t.Fatalf("same-base queued work started early: %+v", current)
	}
	waitForState(t, service, other.ID, StateSucceeded)
	close(releaseA)
	waitForState(t, service, first.ID, StateSucceeded)
	waitForState(t, service, second.ID, StateSucceeded)
}
