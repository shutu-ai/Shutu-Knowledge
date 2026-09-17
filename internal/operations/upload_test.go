package operations

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shutu-ai/shutu-knowledge/internal/storage"
)

func TestUploadForOperationHonorsCanceledContext(t *testing.T) {
	home := t.TempDir()
	db, err := storage.Open(filepath.Join(home, "operations.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	service, err := NewWithUploadRoot(db, 1, filepath.Join(home, "uploads"))
	if err != nil {
		t.Fatal(err)
	}
	session, err := service.CreateUpload(context.Background(), UploadCreate{BaseID: "base-a", FileName: "note.md"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := service.UploadForOperationContext(ctx, "operation-a", session.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled upload lookup error = %v, want context.Canceled", err)
	}
}

func TestUploadBindsAtomicallyAndReleasesOnSuccess(t *testing.T) {
	home := t.TempDir()
	db, err := storage.Open(filepath.Join(home, "operations.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var calls int
	var mu sync.Mutex
	service, err := NewWithUploadRoot(db, 1, filepath.Join(home, "uploads"))
	if err != nil {
		t.Fatal(err)
	}
	service.Register("import_file", func(_ context.Context, op Operation, payload json.RawMessage, _ func(Progress)) (any, error) {
		var command struct {
			UploadID string `json:"uploadId"`
		}
		if err := json.Unmarshal(payload, &command); err != nil {
			return nil, err
		}
		session, path, err := service.UploadForOperation(op.ID, command.UploadID)
		if err != nil {
			return nil, err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		if string(data) != "file body" || session.State != UploadStateBound {
			t.Fatalf("invalid staged input: %s %s", data, session.State)
		}
		mu.Lock()
		calls++
		mu.Unlock()
		return map[string]any{"bytes": len(data)}, nil
	})
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Stop)

	session, err := service.CreateUpload(context.Background(), UploadCreate{
		BaseID: "base-a", FileName: "note.md", ExpectedSize: int64Ptr(9),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.PutUploadContent(context.Background(), session.ID, strings.NewReader("file body")); err != nil {
		t.Fatal(err)
	}
	session, err = service.CompleteUpload(context.Background(), session.ID)
	if err != nil || session.State != UploadStateComplete {
		t.Fatalf("complete: %v %s", err, session.State)
	}
	op, err := service.Submit(context.Background(), Request{
		Type: "import_file", CommandSchemaVersion: CommandSchemaV1,
		BaseID: "base-a", DocumentID: "doc-a", Payload: json.RawMessage(`{"uploadId":"` + session.ID + `"}`),
		IdempotencyKey: "upload-key", UploadID: session.ID, TotalUnits: intPtr(1),
	})
	if err != nil {
		t.Fatal(err)
	}
	if op.TotalBytes == nil || *op.TotalBytes != int64(len("file body")) {
		t.Fatalf("operation total bytes = %v, want %d", op.TotalBytes, len("file body"))
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		current, err := service.Get(op.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.State == StateSucceeded {
			break
		}
		if current.State == StateFailed {
			t.Fatalf("operation failed: %s", current.ErrorMessage)
		}
		if time.Now().After(deadline) {
			t.Fatalf("operation state = %s", current.State)
		}
		time.Sleep(5 * time.Millisecond)
	}
	deadline = time.Now().Add(2 * time.Second)
	for {
		uploaded, err := service.GetUpload(session.ID)
		if err != nil {
			t.Fatal(err)
		}
		if uploaded.State == UploadStateReleased {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("upload state after success = %s", uploaded.State)
		}
		time.Sleep(5 * time.Millisecond)
	}
	second, err := service.Submit(context.Background(), Request{
		Type: "import_file", CommandSchemaVersion: CommandSchemaV1,
		BaseID: "base-a", DocumentID: "doc-a", Payload: json.RawMessage(`{"uploadId":"` + session.ID + `"}`),
		IdempotencyKey: "upload-key", UploadID: session.ID, TotalUnits: intPtr(1),
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != op.ID {
		t.Fatalf("idempotent retry created operation %s", second.ID)
	}
	_, err = service.Submit(context.Background(), Request{
		Type: "import_file", CommandSchemaVersion: CommandSchemaV1,
		BaseID: "base-a", DocumentID: "doc-b", Payload: json.RawMessage(`{"uploadId":"` + session.ID + `"}`),
		UploadID: session.ID,
	})
	if !errors.Is(err, ErrUploadNotComplete) {
		t.Fatalf("second binding error = %v", err)
	}
	released, err := service.GetUpload(session.ID)
	if err != nil || released.State != UploadStateReleased {
		t.Fatalf("released: %v %s", err, released.State)
	}
	stagingPath := filepath.Join(home, "uploads", session.ID+".staging")
	deadline = time.Now().Add(2 * time.Second)
	for {
		_, statErr := os.Stat(stagingPath)
		if os.IsNotExist(statErr) {
			break
		}
		if statErr != nil {
			t.Fatalf("inspect staging file: %v", statErr)
		}
		if time.Now().After(deadline) {
			t.Fatal("staging file remained")
		}
		time.Sleep(5 * time.Millisecond)
	}
	tempFiles, err := filepath.Glob(filepath.Join(home, "uploads", ".*.tmp-*"))
	if err != nil || len(tempFiles) != 0 {
		t.Fatalf("upload temp residue: %v %v", tempFiles, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("executor calls = %d, want 1", calls)
	}
}

func TestUploadReleasesAfterNonRetryableFailure(t *testing.T) {
	home := t.TempDir()
	db, err := storage.Open(filepath.Join(home, "operations.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	service, err := NewWithUploadRoot(db, 1, filepath.Join(home, "uploads"))
	if err != nil {
		t.Fatal(err)
	}
	service.maxPayload = 256
	service.Register("import_file", func(_ context.Context, _ Operation, _ json.RawMessage, _ func(Progress)) (any, error) {
		return strings.Repeat("x", service.maxPayload+1), nil
	})
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Stop)

	session, err := service.CreateUpload(context.Background(), UploadCreate{
		BaseID: "base-a", FileName: "too-large-result.md", ExpectedSize: int64Ptr(9),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.PutUploadContent(context.Background(), session.ID, strings.NewReader("file body")); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CompleteUpload(context.Background(), session.ID); err != nil {
		t.Fatal(err)
	}
	total := 1
	op, err := service.Submit(context.Background(), Request{
		Type: "import_file", CommandSchemaVersion: CommandSchemaV1,
		BaseID: "base-a", DocumentID: "doc-a", Payload: json.RawMessage(`{"uploadId":"` + session.ID + `"}`),
		UploadID: session.ID, TotalUnits: &total,
	})
	if err != nil {
		t.Fatal(err)
	}
	failed := waitForState(t, service, op.ID, StateFailed)
	if failed.Retryable {
		t.Fatal("oversized result should make the operation non-retryable")
	}
	deadline := time.Now().Add(2 * time.Second)
	var released Upload
	for {
		released, err = service.GetUpload(session.ID)
		if err != nil {
			t.Fatal(err)
		}
		if released.State == UploadStateReleased {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("upload state after non-retryable failure = %s, want released", released.State)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := os.Stat(filepath.Join(home, "uploads", session.ID+".staging")); !os.IsNotExist(err) {
		t.Fatalf("staging file still exists after release: %v", err)
	}
}

func TestStartupReleasesTerminalBoundUpload(t *testing.T) {
	home := t.TempDir()
	db, err := storage.Open(filepath.Join(home, "operations.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	first, err := NewWithUploadRoot(db, 1, filepath.Join(home, "uploads"))
	if err != nil {
		t.Fatal(err)
	}
	first.Register("import_file", func(_ context.Context, _ Operation, _ json.RawMessage, _ func(Progress)) (any, error) {
		return nil, nil
	})
	session, err := first.CreateUpload(context.Background(), UploadCreate{
		BaseID: "base-a", FileName: "orphaned.md", ExpectedSize: int64Ptr(9),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.PutUploadContent(context.Background(), session.ID, strings.NewReader("file body")); err != nil {
		t.Fatal(err)
	}
	if _, err := first.CompleteUpload(context.Background(), session.ID); err != nil {
		t.Fatal(err)
	}
	op, err := first.Submit(context.Background(), Request{
		Type: "import_file", CommandSchemaVersion: CommandSchemaV1,
		BaseID: "base-a", DocumentID: "doc-a", Payload: json.RawMessage(`{"uploadId":"` + session.ID + `"}`),
		UploadID: session.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecPriority(context.Background(), storage.ControlWrite, `UPDATE operations
		SET state = ?, retryable = 0, attempt = ?, finished_at = ?, updated_at = ? WHERE id = ?`,
		StateFailed, DefaultMaxAttempts, now(), now(), op.ID); err != nil {
		t.Fatal(err)
	}

	second, err := NewWithUploadRoot(db, 1, filepath.Join(home, "uploads"))
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(second.Stop)
	released, err := second.GetUpload(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if released.State != UploadStateReleased {
		t.Fatalf("startup terminal upload state = %s, want released", released.State)
	}
	if _, err := os.Stat(filepath.Join(home, "uploads", session.ID+".staging")); !os.IsNotExist(err) {
		t.Fatalf("startup recovery left staging file: %v", err)
	}
}

func int64Ptr(value int64) *int64 { return &value }

func TestUploadAggregateQuotaIsAtomic(t *testing.T) {
	home := t.TempDir()
	db, err := storage.Open(filepath.Join(home, "operations.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	service, err := NewWithUploadRoot(db, 1, filepath.Join(home, "uploads"))
	if err != nil {
		t.Fatal(err)
	}
	service.SetResourceLimits(ResourceLimits{IO: 1, DBWrite: 1, Disk: 1, Network: 1, Model: 1, Maintenance: 1, MaxPerBase: 1, UploadBytes: 10})
	first, err := service.CreateUpload(context.Background(), UploadCreate{BaseID: "base", FileName: "one.txt", ExpectedSize: int64Ptr(6)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.PutUploadContent(context.Background(), first.ID, strings.NewReader("123456")); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CompleteUpload(context.Background(), first.ID); err != nil {
		t.Fatal(err)
	}
	second, err := service.CreateUpload(context.Background(), UploadCreate{BaseID: "base", FileName: "two.txt", ExpectedSize: int64Ptr(5)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.PutUploadContent(context.Background(), second.ID, strings.NewReader("abcde")); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CompleteUpload(context.Background(), second.ID); !errors.Is(err, ErrUploadQuotaExceeded) {
		t.Fatalf("aggregate quota error = %v", err)
	}
	unchanged, err := service.GetUpload(second.ID)
	if err != nil || unchanged.State != UploadStateUploading {
		t.Fatalf("quota changed session: %+v %v", unchanged, err)
	}
}

func TestUploadPublishProcessKillConverges(t *testing.T) {
	uploadBody := "kill-safe upload body"
	testBinary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	runBreakpoint := func(breakpoint string) (string, string, string) {
		t.Helper()
		home := t.TempDir()
		dbPath := filepath.Join(home, "operations.db")
		db, err := storage.Open(dbPath)
		if err != nil {
			t.Fatal(err)
		}
		service, err := NewWithUploadRoot(db, 1, filepath.Join(home, "uploads"))
		if err != nil {
			t.Fatal(err)
		}
		session, err := service.CreateUpload(context.Background(), UploadCreate{
			BaseID: "base-kill", FileName: "kill.md",
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}

		workspace := filepath.Join(home, "child")
		if err := os.MkdirAll(workspace, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(workspace, "session-id"), []byte(session.ID), 0o600); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(testBinary)
		cmd.Env = append(os.Environ(),
			"OPERATIONS_UPLOAD_KILL_DB="+dbPath,
			"OPERATIONS_UPLOAD_KILL_WORK="+workspace,
			"OPERATIONS_UPLOAD_KILL_BREAKPOINT="+breakpoint,
		)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		marker := filepath.Join(workspace, "reached")
		deadline := time.Now().Add(10 * time.Second)
		for {
			if _, err := os.Stat(marker); err == nil {
				break
			} else if !os.IsNotExist(err) {
				t.Fatal(err)
			}
			if time.Now().After(deadline) {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
				t.Fatalf("upload child did not reach %s breakpoint", breakpoint)
			}
			time.Sleep(10 * time.Millisecond)
		}
		if err := cmd.Process.Kill(); err != nil {
			t.Fatal(err)
		}
		if err := cmd.Wait(); err == nil {
			t.Fatal("killed upload child exited successfully")
		}
		return home, dbPath, session.ID
	}

	for _, breakpoint := range []string{"before", "after"} {
		home, dbPath, sessionID := runBreakpoint(breakpoint)
		uploadRoot := filepath.Join(home, "uploads")
		stagingPath := filepath.Join(uploadRoot, sessionID+".staging")

		db, err := storage.Open(dbPath)
		if err != nil {
			t.Fatal(err)
		}
		service, err := NewWithUploadRoot(db, 1, uploadRoot)
		if err != nil {
			t.Fatal(err)
		}
		// Startup cleanup removes an orphan temp; the durable session remains
		// uploading and therefore safely accepts the same upload again.
		if err := service.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		session, err := service.PutUploadContent(
			context.Background(), sessionID, strings.NewReader(uploadBody),
		)
		if err != nil {
			t.Fatalf("retry after %s-rename kill: %v", breakpoint, err)
		}
		session, err = service.CompleteUpload(context.Background(), session.ID)
		if err != nil || session.State != UploadStateComplete {
			t.Fatalf("complete after %s-rename kill: %v %s", breakpoint, err, session.State)
		}
		if session.SizeBytes != int64(len(uploadBody)) || session.SHA256 == "" {
			t.Fatalf("recovered upload metadata: %+v", session)
		}
		data, err := os.ReadFile(stagingPath)
		if err != nil || string(data) != uploadBody {
			t.Fatalf("recovered staging bytes = %q %v", data, err)
		}
		temps, err := filepath.Glob(filepath.Join(uploadRoot, ".*.tmp-*"))
		if err != nil || len(temps) != 0 {
			t.Fatalf("temp residue after %s-rename recovery: %v %v", breakpoint, temps, err)
		}
		service.Stop()
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
