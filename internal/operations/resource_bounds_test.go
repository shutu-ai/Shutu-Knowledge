package operations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shutu-ai/shutu-knowledge/internal/storage"
)

func databaseFootprint(paths ...string) uint64 {
	var total uint64
	for _, path := range paths {
		if info, err := os.Stat(path); err == nil {
			total += uint64(info.Size())
		}
	}
	return total
}

func sampleProcessResourcePeak(observedRSS, observedHeap *uint64) error {
	rss, _, err := currentProcessRSS()
	if err != nil {
		return err
	}
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	if rss > *observedRSS {
		*observedRSS = rss
	}
	if memory.HeapAlloc > *observedHeap {
		*observedHeap = memory.HeapAlloc
	}
	return nil
}

func TestSlowModelAndSustainedQueueDemandStayResourceBounded(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "resource-bounds.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	service, err := New(db, 1)
	if err != nil {
		t.Fatal(err)
	}
	const queueLimit = 64
	const payloadBytes = 64 << 10
	service.SetQueueLimit(queueLimit)
	service.SetResourceLimits(ResourceLimits{
		IO: 1, DBWrite: 1, Disk: 1, Network: 1,
		Model: 1, Maintenance: 1, MaxPerBase: 2,
	})

	modelStarted := make(chan struct{})
	modelRelease := make(chan struct{})
	ioStarted := make(chan struct{})
	ioRelease := make(chan struct{})
	var slowExecutions int
	var queuedExecutions int
	var countsMu sync.Mutex
	service.Register("slow_model", func(context.Context, Operation, json.RawMessage, func(Progress)) (any, error) {
		countsMu.Lock()
		slowExecutions++
		countsMu.Unlock()
		close(modelStarted)
		<-modelRelease
		return map[string]any{"ok": true}, nil
	})
	service.Register("slow_io", func(context.Context, Operation, json.RawMessage, func(Progress)) (any, error) {
		close(ioStarted)
		<-ioRelease
		return map[string]any{"ok": true}, nil
	})
	service.Register("bounded_io", func(_ context.Context, _ Operation, payload json.RawMessage, _ func(Progress)) (any, error) {
		var decoded struct {
			Pad string `json:"pad"`
		}
		if err := json.Unmarshal(payload, &decoded); err != nil || len(decoded.Pad) != payloadBytes {
			return nil, errors.New("invalid bounded payload")
		}
		countsMu.Lock()
		queuedExecutions++
		countsMu.Unlock()
		return map[string]any{"ok": true}, nil
	})
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Stop)

	pad := strings.Repeat("r", payloadBytes)
	payloadText, err := json.Marshal(map[string]string{"pad": pad})
	if err != nil {
		t.Fatal(err)
	}
	if len(payloadText) < payloadBytes {
		t.Fatalf("encoded payload = %d bytes, want at least %d", len(payloadText), payloadBytes)
	}
	slowModel, err := service.Submit(context.Background(), Request{
		Type: "slow_model", CommandSchemaVersion: CommandSchemaV1,
		BaseID: "slow-model-base", ResourceClass: ResourceModel,
		Payload: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-modelStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("slow model did not start")
	}
	ioActive, err := service.Submit(context.Background(), Request{
		Type: "slow_io", CommandSchemaVersion: CommandSchemaV1,
		BaseID: "bounded-io-base", ResourceClass: ResourceIO,
		Payload: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-ioStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("slow io did not start")
	}
	runtime.GC()
	if err := sampleProcessResourcePeak(new(uint64), new(uint64)); err != nil {
		t.Fatalf("sample baseline RSS: %v", err)
	}
	baselineRSS, peakRSS, err := currentProcessRSS()
	if err != nil {
		t.Fatalf("read baseline RSS: %v", err)
	}
	var baselineHeap uint64
	if err := sampleProcessResourcePeak(&peakRSS, &baselineHeap); err != nil {
		t.Fatalf("sample baseline heap: %v", err)
	}
	baselineDisk := databaseFootprint(dbPath, dbPath+"-wal", dbPath+"-shm")

	// Distinct 64KiB commands create measurable durable disk demand without
	// unbounded allocation. The model lane remains blocked the whole time.
	var accepted []Operation
	var submitLatencies, rejectionLatencies []time.Duration
	for index := 0; index < 128; index++ {
		startedAt := time.Now()
		operation, submitErr := service.Submit(context.Background(), Request{
			Type: "bounded_io", CommandSchemaVersion: CommandSchemaV1,
			BaseID: "bounded-io-base", ResourceClass: ResourceIO,
			Payload:        json.RawMessage(payloadText),
			IdempotencyKey: fmt.Sprintf("resource-io-%03d", index),
		})
		elapsed := time.Since(startedAt)
		switch {
		case submitErr == nil:
			accepted = append(accepted, operation)
			submitLatencies = append(submitLatencies, elapsed)
		case errors.Is(submitErr, ErrQueueFull):
			rejectionLatencies = append(rejectionLatencies, elapsed)
		default:
			t.Fatalf("unexpected submit error: %v", submitErr)
		}
	}
	if got := len(accepted); got != queueLimit-2 {
		t.Fatalf("accepted queued commands = %d, want %d", got, queueLimit-2)
	}
	if len(rejectionLatencies) == 0 {
		t.Fatal("sustained demand produced no bounded rejections")
	}
	if snapshot, err := service.Scheduler(); err != nil {
		t.Fatal(err)
	} else if snapshot.QueuedTotal != queueLimit-2 || snapshot.ActiveTotal != 2 {
		t.Fatalf("saturated scheduler = %+v", snapshot)
	}
	peakDisk := databaseFootprint(dbPath, dbPath+"-wal", dbPath+"-shm")
	diskDelta := peakDisk - baselineDisk

	// Concurrent status readers model slow readers against saturated durable
	// state while the model lane remains blocked.
	var readerMu sync.Mutex
	var readerLatencies []time.Duration
	var readerWG sync.WaitGroup
	for reader := 0; reader < 4; reader++ {
		readerWG.Add(1)
		go func() {
			defer readerWG.Done()
			for round := 0; round < 20; round++ {
				startedAt := time.Now()
				if _, err := service.Scheduler(); err != nil {
					t.Errorf("reader scheduler: %v", err)
					return
				}
				if _, err := service.Get(slowModel.ID); err != nil {
					t.Errorf("reader model operation: %v", err)
					return
				}
				if _, err := service.Get(ioActive.ID); err != nil {
					t.Errorf("reader io operation: %v", err)
					return
				}
				elapsed := time.Since(startedAt)
				readerMu.Lock()
				readerLatencies = append(readerLatencies, elapsed)
				readerMu.Unlock()
			}
		}()
	}
	readerWG.Wait()
	if err := sampleProcessResourcePeak(&peakRSS, &baselineHeap); err != nil {
		t.Fatalf("sample peak resource: %v", err)
	}

	const maxDiskDelta = 8 << 20
	const maxRSS = 256 << 20
	if diskDelta > maxDiskDelta {
		t.Fatalf("durable queue footprint grew %d bytes, limit %d", diskDelta, maxDiskDelta)
	}
	if peakRSS > maxRSS {
		t.Fatalf("peak RSS = %d bytes, limit %d", peakRSS, maxRSS)
	}

	// Cancelling one queued command releases durable capacity without waiting
	// for the slow model lane.
	cancelled := accepted[0]
	if _, err := service.Cancel(cancelled.ID); err != nil {
		t.Fatal(err)
	}
	waitForState(t, service, cancelled.ID, StateCancelled)
	freed, err := service.Submit(context.Background(), Request{
		Type: "bounded_io", CommandSchemaVersion: CommandSchemaV1,
		BaseID: "bounded-io-base", ResourceClass: ResourceIO,
		Payload:        json.RawMessage(payloadText),
		IdempotencyKey: "resource-io-after-cancel",
	})
	if err != nil {
		t.Fatalf("cancellation did not release queue capacity: %v", err)
	}
	accepted = append(accepted, freed)

	close(modelRelease)
	close(ioRelease)
	drainStarted := time.Now()
	deadline := time.Now().Add(10 * time.Second)
	for {
		rows, err := db.Query(`SELECT COUNT(*) FROM operations
			WHERE state IN ('queued','running','cancelling')`)
		if err != nil {
			t.Fatal(err)
		}
		var pending int
		if !rows.Next() {
			if err := rows.Err(); err != nil {
				_ = rows.Close()
				t.Fatal(err)
			}
			pending = -1
			_ = rows.Close()
			t.Fatalf("pending operations query returned no row")
		}
		if err := rows.Scan(&pending); err != nil {
			_ = rows.Close()
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		if pending == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("resource-bounded queue did not drain: %d pending", pending)
		}
		time.Sleep(5 * time.Millisecond)
	}
	drain := time.Since(drainStarted)

	countsMu.Lock()
	slowCount, queuedCount := slowExecutions, queuedExecutions
	countsMu.Unlock()
	if slowCount != 1 || queuedCount != queueLimit-2 {
		t.Fatalf("business effects = slow=%d queued=%d, want 1/%d",
			slowCount, queuedCount, queueLimit-2)
	}
	submitP95 := percentileLatency(submitLatencies, .95)
	rejectP95 := percentileLatency(rejectionLatencies, .95)
	readerP95 := percentileLatency(readerLatencies, .95)
	if submitP95 > time.Second || rejectP95 > time.Second || readerP95 > time.Second || drain > 3*time.Second {
		t.Fatalf("unbounded slow-model control path: submitP95=%s rejectP95=%s readerP95=%s drain=%s",
			submitP95, rejectP95, readerP95, drain)
	}
	rejectionRate := float64(len(rejectionLatencies)) /
		float64(len(submitLatencies)+len(rejectionLatencies))
	throughput := float64(slowCount+queuedCount) / drain.Seconds()
	t.Logf("resource bounds baselineDisk=%d peakDisk=%d delta=%d baselineRSS=%d peakRSS=%d heap=%d submitP95=%s rejectP95=%s readerP95=%s drain=%s",
		baselineDisk, peakDisk, diskDelta, baselineRSS, peakRSS, baselineHeap,
		submitP95, rejectP95, readerP95, drain)
	t.Logf("resource bounds initialRejectionRate=%.2f effectiveDrainThroughput=%.1f/s",
		rejectionRate, throughput)
}
