package operations

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/shutu-ai/shutu-knowledge/internal/storage"
)

func TestCrossProcessCancelIntentSurvivesOwnerKill(t *testing.T) {
	home := t.TempDir()
	dbPath := filepath.Join(home, "cancel-kill.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// Submit without starting a local owner, so the child becomes the first
	// and sole executor of this durable command.
	preparer, err := New(db, 1)
	if err != nil {
		t.Fatal(err)
	}
	preparer.Register("cross_cancel", func(context.Context, Operation, json.RawMessage, func(Progress)) (any, error) {
		return nil, errors.New("preparer must not execute")
	})
	operation, err := preparer.Submit(context.Background(), Request{
		Type: "cross_cancel", CommandSchemaVersion: CommandSchemaV1,
		BaseID: "cancel-base", ResourceClass: ResourceIO,
		Payload:        json.RawMessage(`{"kind":"cancellable"}`),
		IdempotencyKey: "cross-cancel-owner",
	})
	if err != nil {
		t.Fatal(err)
	}

	workspace := filepath.Join(home, "child")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	testBinary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.Command(testBinary)
	var childLog bytes.Buffer
	child.Stdout = &childLog
	child.Stderr = &childLog
	child.Env = append(os.Environ(),
		fmt.Sprintf("%s=%s", cancelKillDBEnv, dbPath),
		fmt.Sprintf("%s=%s", cancelKillWorkEnv, workspace),
	)
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if child.Process != nil {
			_ = child.Process.Kill()
			_ = child.Wait()
		}
	})

	waitForPath := func(name string) {
		t.Helper()
		path := filepath.Join(workspace, name)
		deadline := time.Now().Add(10 * time.Second)
		for {
			if _, err := os.Stat(path); err == nil {
				return
			} else if !os.IsNotExist(err) {
				t.Fatal(err)
			}
			if child.ProcessState != nil || time.Now().After(deadline) {
				t.Fatalf("child did not reach %s: %s", name, childLog.String())
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	waitForPath("started")

	// This service never starts a worker: it is a cross-process control-plane
	// owner whose Cancel transaction is visible to the running child.
	controller, err := New(db, 1)
	if err != nil {
		t.Fatal(err)
	}
	cancelling, err := controller.Cancel(operation.ID)
	if err != nil || cancelling.State != StateCancelling || !cancelling.CancelRequested {
		t.Fatalf("cross-process cancel = %+v %v", cancelling, err)
	}
	waitForPath("cancel-seen")
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := child.Wait(); err == nil {
		t.Fatal("forced cancel owner exited successfully")
	}

	var state string
	var cancelRequested int
	if err := db.QueryRow(`SELECT state, cancel_requested FROM operations WHERE id = ?`,
		operation.ID).Scan(&state, &cancelRequested); err != nil {
		t.Fatal(err)
	}
	if state != StateCancelling || cancelRequested != 1 {
		t.Fatalf("after owner kill state=%s cancelRequested=%d", state, cancelRequested)
	}
	var cancelEvents, finishEvents int
	if err := db.QueryRow(`SELECT COUNT(*) FROM operation_events
		WHERE operation_id = ? AND kind = ?`, operation.ID, "cancel_requested").Scan(&cancelEvents); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM operation_events
		WHERE operation_id = ? AND kind = ?`, operation.ID, "finished").Scan(&finishEvents); err != nil {
		t.Fatal(err)
	}
	if cancelEvents != 1 || finishEvents != 0 {
		t.Fatalf("post-kill events cancel=%d finish=%d", cancelEvents, finishEvents)
	}
	effect := filepath.Join(workspace, "replayed")
	if _, err := os.Stat(effect); !os.IsNotExist(err) {
		t.Fatalf("cancelled effect existed after owner kill: %v", err)
	}

	// The next sole owner resolves the interrupted/cancelling row to a
	// terminal cancelled result without claiming or replaying the executor.
	recovery, err := New(db, 1)
	if err != nil {
		t.Fatal(err)
	}
	var replays int
	recovery.Register("cross_cancel", func(context.Context, Operation, json.RawMessage, func(Progress)) (any, error) {
		replays++
		return nil, nil
	})
	if err := recovery.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(recovery.Stop)

	deadline := time.Now().Add(5 * time.Second)
	var recovered Operation
	for {
		recovered, err = recovery.Get(operation.ID)
		if err != nil {
			t.Fatal(err)
		}
		if recovered.State == StateCancelled {
			break
		}
		if terminal(recovered.State) && recovered.State != StateCancelled {
			t.Fatalf("recovery ended cancelled command as %s", recovered.State)
		}
		if time.Now().After(deadline) {
			t.Fatalf("recovery state = %s", recovered.State)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if recovered.Attempt != 1 {
		t.Fatalf("recovery attempt = %d, want 1", recovered.Attempt)
	}
	if replays != 0 {
		t.Fatalf("recovery replayed cancelled command %d time(s)", replays)
	}
	if _, err := recovery.Retry(operation.ID); err == nil {
		t.Fatal("cancelled operation accepted retry")
	}
	events, err := recovery.Events(context.Background(), operation.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	kinds := operationEventKinds(events)
	if want := []string{"submitted", "claimed", "cancel_requested", "finished"}; !slices.Equal(kinds, want) {
		t.Fatalf("events = %v, want %v", kinds, want)
	}
	var operationCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM operations`).Scan(&operationCount); err != nil {
		t.Fatal(err)
	}
	if operationCount != 1 {
		t.Fatalf("operation count = %d, want 1", operationCount)
	}
}
