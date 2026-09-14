package operations

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shutu-ai/shutu-knowledge/internal/storage"
)

const (
	childDBEnv        = "OPERATIONS_CROSS_PROCESS_DB"
	childWorkspaceEnv = "OPERATIONS_CROSS_PROCESS_WORK"
	cancelKillDBEnv   = "OPERATIONS_CANCEL_KILL_DB"
	cancelKillWorkEnv = "OPERATIONS_CANCEL_KILL_WORK"
	uploadKillDBEnv   = "OPERATIONS_UPLOAD_KILL_DB"
	uploadKillWorkEnv = "OPERATIONS_UPLOAD_KILL_WORK"
	uploadKillBPEnv   = "OPERATIONS_UPLOAD_KILL_BREAKPOINT"
)

func TestMain(m *testing.M) {
	if dbPath := os.Getenv(cancelKillDBEnv); dbPath != "" {
		os.Exit(runCancelKillChild(dbPath, os.Getenv(cancelKillWorkEnv)))
	}
	if dbPath := os.Getenv(childDBEnv); dbPath != "" {
		os.Exit(runCrossProcessChild(dbPath, os.Getenv(childWorkspaceEnv)))
	}
	if dbPath := os.Getenv(uploadKillDBEnv); dbPath != "" {
		os.Exit(runUploadPublishKillChild(
			dbPath,
			os.Getenv(uploadKillWorkEnv),
			os.Getenv(uploadKillBPEnv),
		))
	}
	os.Exit(m.Run())
}

func runCancelKillChild(dbPath, workspace string) int {
	db, err := storage.Open(dbPath)
	if err != nil {
		return 2
	}
	defer func() { _ = db.Close() }()
	service, err := NewWithUploadRoot(db, 1, "")
	if err != nil {
		return 2
	}
	service.Register("cross_cancel", func(_ context.Context, op Operation, _ json.RawMessage, _ func(Progress)) (any, error) {
		if err := os.WriteFile(filepath.Join(workspace, "started"), []byte(op.ID), 0o600); err != nil {
			return nil, err
		}
		// A cancel transaction in the owner process cannot cancel this
		// process-local context directly. The executor observes the durable
		// intent, which is the ownership boundary C11 must exercise.
		for {
			var cancelRequested int
			err := db.QueryRow(`SELECT cancel_requested FROM operations WHERE id = ?`, op.ID).Scan(&cancelRequested)
			if err == nil && cancelRequested == 1 {
				break
			}
			if err != nil && err != sql.ErrNoRows {
				return nil, err
			}
			time.Sleep(5 * time.Millisecond)
		}
		if err := os.WriteFile(filepath.Join(workspace, "cancel-seen"), []byte(op.ID), 0o600); err != nil {
			return nil, err
		}
		// The parent kills the sole owner after this durable intent is visible
		// and before the child can write the terminal result.
		for {
			time.Sleep(time.Hour)
		}
	})
	if err := service.Start(context.Background()); err != nil {
		return 2
	}
	for {
		time.Sleep(time.Hour)
	}
}

func runUploadPublishKillChild(dbPath, workspace, breakpoint string) int {
	db, err := storage.Open(dbPath)
	if err != nil {
		return 2
	}
	defer func() { _ = db.Close() }()
	service, err := NewWithUploadRoot(db, 1, filepath.Join(filepath.Dir(dbPath), "uploads"))
	if err != nil {
		return 2
	}
	sessionIDBytes, err := os.ReadFile(filepath.Join(workspace, "session-id"))
	if err != nil {
		return 2
	}
	marker := filepath.Join(workspace, "reached")
	hook := func() {
		if err := os.WriteFile(marker, []byte(breakpoint), 0o600); err != nil {
			return
		}
		select {}
	}
	switch breakpoint {
	case "before":
		testUploadBeforePublish = hook
	case "after":
		testUploadAfterPublish = hook
	default:
		return 2
	}
	if _, err := service.PutUploadContent(
		context.Background(), string(sessionIDBytes), strings.NewReader("kill-safe upload body"),
	); err != nil {
		return 2
	}
	return 0
}

// childGate waits for the parent's post-kill recovery phase. The normal child
// exit is a forced process kill, so cleanup is intentionally not exercised.
func childGate(ctx context.Context, markerDir, operationID string, result any) (any, error) {
	marker := filepath.Join(markerDir, "started-"+operationID)
	if err := os.WriteFile(marker, []byte(operationID), 0o600); err != nil {
		return nil, err
	}
	release := filepath.Join(markerDir, "release")
	for {
		if _, err := os.Stat(release); err == nil {
			completed := filepath.Join(markerDir, "effect-"+operationID)
			if err := os.WriteFile(completed, []byte(operationID), 0o600); err != nil {
				return nil, err
			}
			return result, nil
		} else if !os.IsNotExist(err) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func runCrossProcessChild(dbPath, workspace string) int {
	db, err := storage.Open(dbPath)
	if err != nil {
		return 2
	}
	defer func() { _ = db.Close() }()
	service, err := NewWithUploadRoot(db, 2, "")
	if err != nil {
		return 2
	}
	service.SetResourceLimits(ResourceLimits{
		IO: 2, DBWrite: 1, Disk: 1, Network: 1,
		Model: 1, Maintenance: 1, MaxPerBase: 2,
	})
	service.Register("cross_gate", func(ctx context.Context, op Operation, _ json.RawMessage, _ func(Progress)) (any, error) {
		return childGate(ctx, workspace, op.ID, map[string]any{"operation": op.ID})
	})
	if err := service.Start(context.Background()); err != nil {
		return 2
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 2
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /submit", func(w http.ResponseWriter, r *http.Request) {
		var req Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		op, err := service.Submit(r.Context(), req)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"operation": op, "error": errorText(err)})
	})
	server := &http.Server{Handler: mux}
	serverError := make(chan error, 1)
	go func() { serverError <- server.Serve(listener) }()
	ready := filepath.Join(workspace, "child-ready")
	if err := os.WriteFile(ready, []byte(listener.Addr().String()), 0o600); err != nil {
		_ = server.Close()
		return 2
	}

	// The parent forcibly terminates this process after it has recovered one
	// operation and accepted another across the process boundary.
	select {}
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func TestCrossProcessKillRecoversRecoveredAndNewCommands(t *testing.T) {
	home := t.TempDir()
	dbPath := filepath.Join(home, "cross-process.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// Prepare the stale command at the exact running breakpoint without
	// starting a local competitor for child ownership.
	preparer, err := New(db, 1)
	if err != nil {
		t.Fatal(err)
	}
	preparer.Register("cross_gate", func(context.Context, Operation, json.RawMessage, func(Progress)) (any, error) {
		return nil, errors.New("preparer never executes")
	})
	stale, err := preparer.Submit(context.Background(), Request{
		Type: "cross_gate", CommandSchemaVersion: CommandSchemaV1,
		BaseID: "cross-base", ResourceClass: ResourceIO,
		Payload:        json.RawMessage(`{"kind":"stale"}`),
		IdempotencyKey: "cross-process-stale",
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UnixMilli()
	if _, err := db.Exec(`UPDATE operations
		SET state = ?, attempt = 1, instance_id = ?, started_at = ?,
		    state_revision = state_revision + 1, updated_at = ?
		WHERE id = ? AND state = ?`,
		StateRunning, "preparer", now, now, stale.ID, StateQueued); err != nil {
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
	cmd := exec.Command(testBinary)
	var childLog bytes.Buffer
	cmd.Stdout = &childLog
	cmd.Stderr = &childLog
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("%s=%s", childDBEnv, dbPath),
		fmt.Sprintf("%s=%s", childWorkspaceEnv, workspace),
	)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	cleanupChild := func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}
	t.Cleanup(cleanupChild)

	readyPath := filepath.Join(workspace, "child-ready")
	var childAddr string
	deadline := time.Now().Add(10 * time.Second)
	for {
		data, err := os.ReadFile(readyPath)
		if err == nil {
			childAddr = string(data)
			break
		}
		if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if cmd.ProcessState != nil || time.Now().After(deadline) {
			t.Fatalf("child process failed before readiness: %s", childLog.String())
		}
		time.Sleep(10 * time.Millisecond)
	}

	waitForMarker := func(name string) {
		t.Helper()
		path := filepath.Join(workspace, name)
		for {
			if _, err := os.Stat(path); err == nil {
				return
			} else if !os.IsNotExist(err) {
				t.Fatal(err)
			}
			if time.Now().After(deadline) {
				t.Fatalf("child marker %s was not created", name)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	waitForMarker("started-" + stale.ID)

	// A separate transport process submits new work while the child owns the
	// stale command. The child's second IO worker claims this new command.
	crossPayload, err := json.Marshal(map[string]string{"kind": "new"})
	if err != nil {
		t.Fatal(err)
	}
	submitBody, err := json.Marshal(Request{
		Type: "cross_gate", CommandSchemaVersion: CommandSchemaV1,
		BaseID: "new-base", ResourceClass: ResourceIO,
		Payload:        crossPayload,
		IdempotencyKey: "cross-process-new",
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.Post("http://"+childAddr+"/submit", "application/json", bytes.NewReader(submitBody))
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Operation *Operation `json:"operation"`
		Error     string     `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || envelope.Error != "" || envelope.Operation == nil {
		t.Fatalf("cross-process submission failed: code=%d envelope=%+v log=%s",
			response.StatusCode, envelope, childLog.String())
	}
	fresh := envelope.Operation
	if fresh.State != StateQueued && fresh.State != StateRunning {
		t.Fatalf("new cross-process command state = %s", fresh.State)
	}
	waitForMarker("started-" + fresh.ID)

	// This is the C16 breakpoint: the sole owner dies after it has recovered
	// the old command and accepted/claimed the new command.
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatal(err)
		}
	}
	for _, operationID := range []string{stale.ID, fresh.ID} {
		var state string
		if err := db.QueryRow(`SELECT state FROM operations WHERE id = ?`, operationID).Scan(&state); err != nil {
			t.Fatal(err)
		}
		if state != StateRunning {
			t.Fatalf("operation %s state after kill = %s, want running", operationID, state)
		}
	}

	// A new sole owner must repair both rows, replay both commands, and produce
	// exactly one business effect per durable operation.
	recovery, err := NewWithUploadRoot(db, 2, "")
	if err != nil {
		t.Fatal(err)
	}
	recovery.SetResourceLimits(ResourceLimits{
		IO: 2, DBWrite: 1, Disk: 1, Network: 1,
		Model: 1, Maintenance: 1, MaxPerBase: 2,
	})
	recovery.Register("cross_gate", func(ctx context.Context, op Operation, _ json.RawMessage, _ func(Progress)) (any, error) {
		return childGate(ctx, workspace, op.ID, map[string]any{"recovered": op.ID})
	})
	if err := recovery.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(recovery.Stop)

	waitForOperationState := func(id, want string, attempt int) Operation {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for {
			op, err := recovery.Get(id)
			if err == nil {
				if op.State == want && op.Attempt >= attempt {
					return op
				}
				if terminal(op.State) && op.State != want {
					t.Fatalf("operation %s ended %s, want %s: %+v", id, op.State, want, op)
				}
			} else if !errors.Is(err, ErrNotFound) && !errors.Is(err, sql.ErrNoRows) {
				t.Fatal(err)
			}
			if time.Now().After(deadline) {
				if op, getErr := recovery.Get(id); getErr == nil {
					t.Fatalf("operation %s state = %s attempt = %d, want %s/%d",
						id, op.State, op.Attempt, want, attempt)
				}
				t.Fatalf("operation %s did not reach %s/%d", id, want, attempt)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	waitForRunning := func(id string, attempt int) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for {
			op, err := recovery.Get(id)
			if err == nil {
				if op.State == StateRunning && op.Attempt == attempt {
					return
				}
				if terminal(op.State) {
					t.Fatalf("operation %s ended %s before release: %+v", id, op.State, op)
				}
			} else if !errors.Is(err, ErrNotFound) {
				t.Fatal(err)
			}
			if time.Now().After(deadline) {
				var state string
				var currentAttempt int
				var instance string
				_ = recovery.db.QueryRow(`SELECT state, attempt, COALESCE(instance_id, '')
					FROM operations WHERE id = ?`, id).Scan(&state, &currentAttempt, &instance)
				t.Fatalf("operation %s did not resume as running/%d: state=%s attempt=%d instance=%s",
					id, attempt, state, currentAttempt, instance)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	waitForRunning(stale.ID, 3)
	waitForRunning(fresh.ID, 2)
	if err := os.WriteFile(filepath.Join(workspace, "release"), []byte("release"), 0o600); err != nil {
		t.Fatal(err)
	}
	recoveredStale := waitForOperationState(stale.ID, StateSucceeded, 3)
	recoveredFresh := waitForOperationState(fresh.ID, StateSucceeded, 2)

	var operationCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM operations`).Scan(&operationCount); err != nil {
		t.Fatal(err)
	}
	if operationCount != 2 {
		t.Fatalf("operation count = %d, want 2", operationCount)
	}
	if recoveredStale.Attempt != 3 || recoveredFresh.Attempt != 2 {
		t.Fatalf("attempt history stale=%d fresh=%d, want 3/2",
			recoveredStale.Attempt, recoveredFresh.Attempt)
	}
	for _, operationID := range []string{stale.ID, fresh.ID} {
		var effectCount int
		if err := db.QueryRow(`SELECT COUNT(*) FROM operation_events
			WHERE operation_id = ? AND kind = ?`, operationID, "finished").Scan(&effectCount); err != nil {
			t.Fatal(err)
		}
		if effectCount != 1 {
			t.Fatalf("operation %s has %d terminal events, want 1", operationID, effectCount)
		}
		effect := filepath.Join(workspace, "effect-"+operationID)
		if _, err := os.Stat(effect); err != nil {
			t.Fatalf("operation %s business effect missing: %v", operationID, err)
		}
	}
}
