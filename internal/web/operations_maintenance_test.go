package web

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/shutu-ai/shutu-knowledge/internal/operations"
	"github.com/shutu-ai/shutu-knowledge/internal/storage"
)

func TestStorageMaintenanceOperationQuarantinesOrphans(t *testing.T) {
	s := newTestServer(t)
	_, basePayload := call(t, s, "POST", "/api/bases", map[string]any{"name": "Maintenance"})
	baseID := valueMap(t, basePayload)["id"].(string)
	orphan := filepath.Join(s.app.RawStore.Root(), baseID, "orphan.bin")
	if err := os.MkdirAll(filepath.Dir(orphan), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(orphan, []byte("bytes"), 0o600); err != nil {
		t.Fatal(err)
	}

	submit := func(input map[string]any, key string) operations.Operation {
		t.Helper()
		code, payload := call(t, s, "POST", "/api/operations", map[string]any{
			"type":                 "maintenance_storage",
			"commandSchemaVersion": operations.CommandSchemaV1,
			"target":               map[string]any{},
			"input":                input,
			"idempotencyKey":       key,
		})
		if code != http.StatusAccepted {
			t.Fatalf("submit maintenance: %d %v", code, payload)
		}
		operationID := valueMap(t, payload)["operationId"].(string)
		deadline := time.Now().Add(5 * time.Second)
		for {
			op, err := s.app.Operations.Get(operationID)
			if err != nil {
				t.Fatal(err)
			}
			if op.State == operations.StateSucceeded {
				return op
			}
			if op.State == operations.StateFailed {
				t.Fatalf("maintenance failed: %s", op.ErrorMessage)
			}
			if op.ResourceClass != operations.ResourceMaintenance {
				t.Fatalf("maintenance resource class: %s", op.ResourceClass)
			}
			if time.Now().After(deadline) {
				t.Fatalf("maintenance state = %s", op.State)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}

	dryRunOperation := submit(map[string]any{"dryRun": true}, "maintenance-dry-run")
	var dryRun struct {
		Orphans     int   `json:"orphans"`
		OrphanBytes int64 `json:"orphanBytes"`
		Quarantined int   `json:"quarantined"`
	}
	if err := json.Unmarshal(dryRunOperation.Result, &dryRun); err != nil {
		t.Fatal(err)
	}
	if dryRun.Orphans != 1 || dryRun.OrphanBytes != 5 || dryRun.Quarantined != 0 {
		t.Fatalf("dry-run result: %+v", dryRun)
	}

	quarantineOperation := submit(map[string]any{}, "maintenance-quarantine")
	var quarantined struct {
		Quarantined     int   `json:"quarantined"`
		QuarantineBytes int64 `json:"quarantineBytes"`
	}
	if err := json.Unmarshal(quarantineOperation.Result, &quarantined); err != nil {
		t.Fatal(err)
	}
	if quarantined.Quarantined != 1 || quarantined.QuarantineBytes != 5 {
		t.Fatalf("quarantine result: %+v", quarantined)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatalf("orphan remained active: %v", err)
	}
	retained := filepath.Join(s.app.RawStore.Root(), storage.QuarantineDir, baseID, "orphan.bin")
	if _, err := os.Stat(retained); err != nil {
		t.Fatalf("quarantine file missing: %v", err)
	}

	purgeOperation := submit(map[string]any{"purgeQuarantine": true}, "maintenance-purge")
	var purged struct {
		QuarantinePurged bool `json:"quarantinePurged"`
	}
	if err := json.Unmarshal(purgeOperation.Result, &purged); err != nil {
		t.Fatal(err)
	}
	if !purged.QuarantinePurged {
		t.Fatalf("purge result: %+v", purged)
	}
	if _, err := os.Stat(retained); !os.IsNotExist(err) {
		t.Fatalf("quarantine file remained: %v", err)
	}
}
