package operations

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shutu-ai/shutu-knowledge/internal/storage"
)

func insertMaintenanceOperation(t *testing.T, service *Service, id, state string, retryable int) {
	t.Helper()
	current := now()
	_, err := service.db.Exec(`INSERT INTO operations
		(id, type, command_schema_version, command_payload, request_fingerprint,
		 state, retryable, result, requested_at, updated_at, idempotency_expires_at)
		VALUES (?, 'maintenance-test', 1, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, `{"payload":true}`, "fingerprint-"+id, state, retryable,
		`{"result":true}`, current, current, current-1)
	if err != nil {
		t.Fatal(err)
	}
}

func TestMaintenanceCleanupProcessesRowsInBatches(t *testing.T) {
	var calls int
	service := newTestService(t, &calls, nil)
	for i := 0; i < cleanupBatchSize+7; i++ {
		insertMaintenanceOperation(t, service, fmt.Sprintf("payload-cleanup-%03d", i), StateSucceeded, 1)
	}

	if err := service.expireTerminalOperationPayloads(context.Background()); err != nil {
		t.Fatal(err)
	}
	var remaining int
	if err := service.db.QueryRow(`SELECT COUNT(*) FROM operations
		WHERE id LIKE 'payload-cleanup-%' AND (length(command_payload) > 0 OR result IS NOT NULL)`).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("payload cleanup left %d rows", remaining)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := service.expireTerminalOperationPayloads(cancelled); err == nil || err != context.Canceled {
		t.Fatalf("cancelled payload cleanup error = %v", err)
	}
}

func TestURLCaptureCleanupProcessesRowsInBatches(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "operations.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	service, err := New(db, 1)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < cleanupBatchSize+7; i++ {
		opID := fmt.Sprintf("url-cleanup-op-%03d", i)
		insertMaintenanceOperation(t, service, opID, StateSucceeded, 1)
		captureState := URLStateReady
		if i%2 == 1 {
			captureState = URLStateFailed
		}
		_, err := db.Exec(`INSERT INTO url_captures
			(id, operation_id, raw_url, final_url, sha256, size_bytes, state, body, created_at, updated_at, expires_at)
			VALUES (?, ?, 'https://example.test/source', 'https://example.test/final', ?, 4, ?, ?, ?, ?, ?)`,
			fmt.Sprintf("url-capture-%03d", i), opID, strings.Repeat("a", 64), captureState,
			[]byte("body"), now(), now(), time.Now().UnixMilli()-1)
		if err != nil {
			t.Fatal(err)
		}
	}

	if err := service.ExpireURLCaptures(context.Background()); err != nil {
		t.Fatal(err)
	}
	var remaining int
	if err := db.QueryRow(`SELECT COUNT(*) FROM url_captures WHERE state IN ('ready','failed')`).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("URL capture cleanup left %d rows", remaining)
	}
	var body []byte
	if err := db.QueryRow(`SELECT body FROM url_captures LIMIT 1`).Scan(&body); err != nil {
		t.Fatal(err)
	}
	if body != nil {
		t.Fatalf("expired URL capture body retained: %q", body)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := service.ExpireURLCaptures(cancelled); err == nil || err != context.Canceled {
		t.Fatalf("cancelled URL capture cleanup error = %v", err)
	}
}

func TestUploadMaintenanceCleanupProcessesRowsInBatches(t *testing.T) {
	home := t.TempDir()
	db, err := storage.Open(filepath.Join(home, "operations.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	uploadRoot := filepath.Join(home, "uploads")
	service, err := NewWithUploadRoot(db, 1, uploadRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(uploadRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	current := now()
	for i := 0; i < cleanupBatchSize+7; i++ {
		id := fmt.Sprintf("%032x", i+1)
		if err := os.WriteFile(filepath.Join(uploadRoot, id+".staging"), []byte("stale"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := db.Exec(`INSERT INTO upload_sessions
			(id, base_id, file_name, state, staging_path, created_at, updated_at, expires_at)
			VALUES (?, 'maintenance', ?, 'uploading', ?, ?, ?, ?)`,
			id, id+".txt", filepath.Join(uploadRoot, id+".staging"), current, current, current-1)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := service.expireUploads(context.Background()); err != nil {
		t.Fatal(err)
	}
	var expired int
	if err := db.QueryRow(`SELECT COUNT(*) FROM upload_sessions WHERE state = 'expired'`).Scan(&expired); err != nil {
		t.Fatal(err)
	}
	if expired != cleanupBatchSize+7 {
		t.Fatalf("expired uploads = %d, want %d", expired, cleanupBatchSize+7)
	}
	var files int
	for i := 0; i < cleanupBatchSize+7; i++ {
		id := fmt.Sprintf("%032x", i+1)
		if _, err := os.Stat(filepath.Join(uploadRoot, id+".staging")); !os.IsNotExist(err) {
			files++
		}
	}
	if files != 0 {
		t.Fatalf("expired upload staging files retained = %d", files)
	}

	for i := 0; i < cleanupBatchSize+7; i++ {
		opID := fmt.Sprintf("bound-cleanup-op-%03d", i)
		insertMaintenanceOperation(t, service, opID, StateSucceeded, 1)
		id := fmt.Sprintf("%032x", cleanupBatchSize+1000+i)
		_, err := db.Exec(`INSERT INTO upload_sessions
			(id, base_id, file_name, state, staging_path, operation_id, created_at, updated_at, expires_at)
			VALUES (?, 'maintenance', ?, 'bound', ?, ?, ?, ?, ?)`,
			id, id+".txt", filepath.Join(uploadRoot, id+".staging"), opID, current, current, current+1)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := service.releaseTerminalUploads(context.Background()); err != nil {
		t.Fatal(err)
	}
	var released int
	if err := db.QueryRow(`SELECT COUNT(*) FROM upload_sessions WHERE state = 'released'`).Scan(&released); err != nil {
		t.Fatal(err)
	}
	if released != cleanupBatchSize+7 {
		t.Fatalf("released uploads = %d, want %d", released, cleanupBatchSize+7)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := service.expireUploads(cancelled); err == nil || err != context.Canceled {
		t.Fatalf("cancelled upload cleanup error = %v", err)
	}
	if err := service.releaseTerminalUploads(cancelled); err == nil || err != context.Canceled {
		t.Fatalf("cancelled upload lease cleanup error = %v", err)
	}
}

func TestUploadCleanupFailurePreservesRetryableLeaseState(t *testing.T) {
	home := t.TempDir()
	db, err := storage.Open(filepath.Join(home, "operations.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	root := filepath.Join(home, "uploads")
	service, err := NewWithUploadRoot(db, 1, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}

	expiredID := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	expiredPath := filepath.Join(root, expiredID+".staging")
	if err := os.Mkdir(expiredPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(expiredPath, "still-open"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	current := now()
	if _, err := db.Exec(`INSERT INTO upload_sessions
		(id, base_id, file_name, state, staging_path, created_at, updated_at, expires_at)
		VALUES (?, 'cleanup', 'expired.txt', 'uploading', ?, ?, ?, ?)`,
		expiredID, expiredPath, current, current, current-1); err != nil {
		t.Fatal(err)
	}
	if err := service.expireUploads(context.Background()); err == nil {
		t.Fatal("expired upload cleanup unexpectedly succeeded while staging path was not removable")
	}
	var state string
	if err := db.QueryRow(`SELECT state FROM upload_sessions WHERE id = ?`, expiredID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != UploadStateUploading {
		t.Fatalf("expired upload state = %q, want uploading for retry", state)
	}
	if err := os.Remove(filepath.Join(expiredPath, "still-open")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(expiredPath); err != nil {
		t.Fatal(err)
	}

	terminalID := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	terminalPath := filepath.Join(root, terminalID+".staging")
	if err := os.Mkdir(terminalPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(terminalPath, "still-open"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	opID := "terminal-cleanup-op"
	insertMaintenanceOperation(t, service, opID, StateSucceeded, 1)
	if _, err := db.Exec(`INSERT INTO upload_sessions
		(id, base_id, file_name, state, staging_path, operation_id, created_at, updated_at, expires_at)
		VALUES (?, 'cleanup', 'terminal.txt', 'bound', ?, ?, ?, ?, ?)`,
		terminalID, terminalPath, opID, current, current, current+1); err != nil {
		t.Fatal(err)
	}
	if err := service.releaseTerminalUploads(context.Background()); err == nil {
		t.Fatal("terminal upload cleanup unexpectedly succeeded while staging path was not removable")
	}
	if err := db.QueryRow(`SELECT state FROM upload_sessions WHERE id = ?`, terminalID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != UploadStateBound {
		t.Fatalf("terminal upload state = %q, want bound for retry", state)
	}
}
