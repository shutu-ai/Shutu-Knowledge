package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shutu-ai/shutu-knowledge/internal/runtime"
)

type blockingLegacyRuntime struct {
	release chan struct{}
}

func (r *blockingLegacyRuntime) Call(context.Context, string, any, any) error { return nil }

func (r *blockingLegacyRuntime) Configured(string) bool { return true }

func (r *blockingLegacyRuntime) Probe(context.Context, string) (runtime.Health, error) {
	return runtime.Health{}, nil
}

func (r *blockingLegacyRuntime) Status(context.Context) map[string]runtime.Health {
	return map[string]runtime.Health{}
}

func (r *blockingLegacyRuntime) Close() { <-r.release }

func TestCloseRuntimeWithContextBoundsLegacyController(t *testing.T) {
	controller := &blockingLegacyRuntime{release: make(chan struct{})}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	started := time.Now()
	err := closeRuntimeWithContext(ctx, controller)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("legacy close error = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("legacy close was not bounded: %s", elapsed)
	}
	close(controller.release)
}

func TestHealthSnapshotDoesNotReportReadyDuringDeferredRecovery(t *testing.T) {
	application := &App{startupDone: make(chan struct{})}

	report := application.HealthSnapshot(context.Background())
	if report.Ready {
		t.Fatalf("deferred recovery health reported ready: %+v", report)
	}
	if report.Status != "starting" {
		t.Fatalf("deferred recovery status = %q, want starting", report.Status)
	}
	if len(report.Components) != 1 || report.Components[0].Name != "startup-recovery" {
		t.Fatalf("deferred recovery components = %+v", report.Components)
	}
}

func TestHealthSnapshotDoesNotReportReadyAfterDeferredRecoveryFailure(t *testing.T) {
	application := &App{startupErr: errors.New("writer unavailable")}

	report := application.HealthSnapshot(context.Background())
	if report.Ready {
		t.Fatalf("failed deferred recovery reported ready: %+v", report)
	}
	if report.Status != "unhealthy: startup-recovery" {
		t.Fatalf("failed deferred recovery status = %q", report.Status)
	}
	if len(report.Components) != 1 || report.Components[0].Status != "failed" {
		t.Fatalf("failed deferred recovery components = %+v", report.Components)
	}
}
