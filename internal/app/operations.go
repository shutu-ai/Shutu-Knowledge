package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/shutu-ai/shutu-knowledge/internal/jobs"
	"github.com/shutu-ai/shutu-knowledge/internal/knowledge"
	"github.com/shutu-ai/shutu-knowledge/internal/models"
	"github.com/shutu-ai/shutu-knowledge/internal/operations"
	"github.com/shutu-ai/shutu-knowledge/internal/runtime"
)

type importTextCommand struct {
	Title   string `json:"title"`
	Content string `json:"content"`
}

type importFilesCommand struct {
	Files             []knowledge.AddFilesItem `json:"files"`
	Conflict          string                   `json:"conflict"`
	ParentDirectoryID string                   `json:"parentDirectoryId"`
}

type documentCommand struct {
	DocumentID string `json:"documentId"`
}

type deleteDocumentsCommand struct {
	DocumentIDs []string `json:"documentIds"`
}

type reindexDocumentsCommand struct {
	DocumentIDs []string `json:"documentIds"`
}

type modelDownloadCommand struct {
	ID        string   `json:"id"`
	Kind      string   `json:"kind"`
	Artifacts []string `json:"artifacts,omitempty"`
	Managed   bool     `json:"managed,omitempty"`
}

type modelIDCommand struct {
	ID string `json:"id"`
}

type modelRemoveCommand struct {
	ID             string `json:"id"`
	Kind           string `json:"kind,omitempty"`
	Managed        bool   `json:"managed,omitempty"`
	Capability     string `json:"capability,omitempty"`
	CustomReranker bool   `json:"customReranker,omitempty"`
}

type modelCacheMigrationCommand struct {
	TargetDir    string `json:"targetDir"`
	RemoveSource bool   `json:"removeSource"`
}

type ollamaPullCommand struct {
	Model string `json:"model"`
}

type ollamaDeleteCommand struct {
	Model string `json:"model"`
}

type storageMaintenanceCommand struct {
	DryRun          bool `json:"dryRun"`
	PurgeQuarantine bool `json:"purgeQuarantine"`
	// SQLiteOnly is used by deferred startup maintenance. Keeping the command
	// in the durable maintenance lane makes vacuum/FTS work observable and
	// replayable without turning startup into a full raw-file reconciliation.
	SQLiteOnly      bool  `json:"sqliteOnly"`
	OptimizeFTS     bool  `json:"optimizeFTS"`
	Vacuum          bool  `json:"vacuum"`
	VacuumThreshold int64 `json:"vacuumThresholdBytes"`
}
type importFileCommand struct {
	UploadID          string `json:"uploadId"`
	FileName          string `json:"fileName"`
	Conflict          string `json:"conflict"`
	ParentDirectoryID string `json:"parentDirectoryId"`
}

type importURLCommand struct {
	URL   string `json:"url"`
	Title string `json:"title"`
}

type importDirectoryCommand struct {
	Path string `json:"path"`
}

type restoreBaseCommand struct {
	Name         string                `json:"name"`
	Config       *knowledge.BaseConfig `json:"config"`
	TargetBaseID string                `json:"targetBaseId"`
}

// testImportPublished is installed only by a real process-kill child after
// the durable business publication and before its terminal operation write.
var testImportPublished func(op operations.Operation, document knowledge.Document)

type documentOperationResult struct {
	Documents []knowledge.Document `json:"documents,omitempty"`
	Succeeded int                  `json:"succeeded"`
	Failed    int                  `json:"failed"`
	Skipped   int                  `json:"skipped"`
	Partial   bool                 `json:"partial"`
}

func (a *App) withOperationItemCommit(ctx context.Context, operationID string, attempt int, itemKey string) context.Context {
	if operationID == "" || itemKey == "" {
		return ctx
	}
	return knowledge.WithCommitHook(ctx, func(tx *sql.Tx) error {
		return a.Operations.MarkItemTx(tx, operationID, attempt, itemKey, operations.ItemCommitted, nil, "", "")
	})
}

func (a *App) markOperationItem(operationID string, attempt int, itemKey, state string,
	errorCode, errorMessage string,
) error {
	return a.markOperationItemContext(context.Background(), operationID, attempt, itemKey, state, errorCode, errorMessage)
}

func (a *App) markOperationItemResult(operationID string, attempt int, itemKey, state string,
	result any, errorCode, errorMessage string,
) error {
	return a.markOperationItemResultContext(context.Background(), operationID, attempt, itemKey, state, result, errorCode, errorMessage)
}

func (a *App) markOperationItemContext(ctx context.Context, operationID string, attempt int, itemKey, state string,
	errorCode, errorMessage string,
) error {
	return a.markOperationItemResultContext(ctx, operationID, attempt, itemKey, state, nil, errorCode, errorMessage)
}

func (a *App) markOperationItemResultContext(ctx context.Context, operationID string, attempt int, itemKey, state string,
	result any, errorCode, errorMessage string,
) error {
	if operationID == "" {
		return nil
	}
	// A cancelled worker still has to durably record the final item boundary.
	// Detach only this small control write from cancellation and retain a hard
	// deadline; business work and ordinary marker writes remain on ctx.
	markerCtx := ctx
	cancel := func() {}
	if markerCtx == nil {
		markerCtx = context.Background()
	}
	if markerCtx.Err() != nil {
		markerCtx, cancel = context.WithTimeout(context.WithoutCancel(markerCtx), 5*time.Second)
	}
	defer cancel()
	err := a.Operations.MarkItem(markerCtx, operationID, attempt, itemKey, state, result, errorCode, errorMessage)
	if errors.Is(err, operations.ErrOperationItemCommitted) {
		// The business effect may have committed in the same transaction and a
		// later physical cleanup step may have failed. Never downgrade that
		// durable commit marker on retry.
		return nil
	}
	return err
}

// operationItemResolved is the retry boundary for aggregate and single-item
// executors. A committed or intentionally skipped item is already resolved;
// failed/cancelled/pending items remain eligible for the current replay.
func (a *App) operationItemResolved(ctx context.Context, operationID, itemKey string) (bool, bool, error) {
	_, resolved, skipped, err := a.operationItemResult(ctx, operationID, itemKey)
	return resolved, skipped, err
}

func (a *App) operationItemResult(ctx context.Context, operationID, itemKey string) (json.RawMessage, bool, bool, error) {
	if operationID == "" || itemKey == "" {
		return nil, false, false, nil
	}
	item, err := a.Operations.GetItem(ctx, operationID, itemKey)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, false, nil
	}
	if err != nil {
		return nil, false, false, err
	}
	skipped := item.State == operations.ItemSkipped
	return item.Result, item.Committed || item.State == operations.ItemCommitted || skipped, skipped, nil
}

type importFileAllocation struct {
	DocumentID string `json:"documentId"`
}

func importFilesItemKey(index int, fileName string) string {
	return fmt.Sprintf("%d:%s", index, strings.TrimSpace(fileName))
}

func (a *App) prepareImportFilesOperation(ctx context.Context, op operations.Operation,
	files []knowledge.AddFilesItem,
) error {
	items, err := a.Operations.ListItems(ctx, op.ID, 500)
	if err != nil {
		return err
	}
	byKey := make(map[string]operations.OperationItem, len(items))
	for _, item := range items {
		byKey[item.ItemKey] = item
	}
	for index := range files {
		key := importFilesItemKey(index, files[index].FileName)
		item, exists := byKey[key]
		if exists && len(item.Result) > 0 {
			var allocation importFileAllocation
			if err := json.Unmarshal(item.Result, &allocation); err == nil && allocation.DocumentID != "" {
				files[index].DocumentID = allocation.DocumentID
			}
		}
		files[index].Resolved = exists && (item.Committed ||
			item.State == operations.ItemCommitted || item.State == operations.ItemSkipped)
		files[index].ResolvedSkipped = files[index].Resolved && item.State == operations.ItemSkipped
		if files[index].Resolved && files[index].DocumentID != "" {
			if document, _, getErr := a.Knowledge.GetDocumentWithContext(ctx, files[index].DocumentID, false); getErr == nil {
				files[index].ResolvedTitle = document.Title
			}
		}
		if files[index].Resolved {
			continue
		}
		if files[index].DocumentID == "" {
			id, err := operations.NewID()
			if err != nil {
				return err
			}
			files[index].DocumentID = id
		}
		if !exists || item.State != operations.ItemCommitted {
			if err := a.Operations.MarkItem(ctx, op.ID, op.Attempt, key, operations.ItemPending,
				importFileAllocation{DocumentID: files[index].DocumentID}, "", ""); err != nil {
				return err
			}
		}
	}
	return nil
}

func (a *App) markImportFilesResult(op operations.Operation, files []knowledge.AddFilesItem, result knowledge.AddFilesResult) error {
	return a.markImportFilesResultContext(context.Background(), op, files, result)
}

func (a *App) markImportFilesResultContext(ctx context.Context, op operations.Operation, files []knowledge.AddFilesItem, result knowledge.AddFilesResult) error {
	for index, accepted := range result.Accepted {
		if index >= len(files) {
			break
		}
		key := importFilesItemKey(index, files[index].FileName)
		state := operations.ItemSkipped
		if accepted.ID != "" && !accepted.Skipped {
			state = operations.ItemCommitted
		}
		// Keep the durable allocation written before the worker starts. A
		// committed marker already rejects later downgrades, while a skipped
		// item still needs its pending allocation to remain available on replay.
		err := a.markOperationItemContext(ctx, op.ID, op.Attempt, key, state, "", "")
		if errors.Is(err, operations.ErrOperationItemCommitted) {
			continue
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func documentResult(document knowledge.Document) documentOperationResult {
	return documentOperationResult{Documents: []knowledge.Document{document}, Succeeded: 1}
}

func (a *App) newOperationService() (*operations.Service, error) {
	workers := a.Config.Scheduler.IO
	if workers < 1 {
		workers = 1
	}
	service, err := operations.NewWithUploadRoot(a.DB, workers, filepath.Join(a.Home, "uploads"))
	if err != nil {
		return nil, err
	}
	service.SetResourceLimits(operations.ResourceLimits{
		IO:                a.Config.Scheduler.IO,
		DBWrite:           a.Config.Scheduler.DBWrite,
		Disk:              a.Config.Scheduler.Disk,
		Network:           a.Config.Scheduler.Network,
		Model:             a.Config.Scheduler.Model,
		Maintenance:       a.Config.Scheduler.Maintenance,
		MaxPerBase:        a.Config.Scheduler.MaxPerBase,
		MemoryBytes:       a.Config.Scheduler.MemoryBytes,
		DiskBytes:         a.Config.Scheduler.DiskBytes,
		DiskLowWaterBytes: a.Config.Scheduler.DiskLowWaterBytes,
		UploadBytes:       a.Config.Scheduler.UploadBytes,
		TempBytes:         a.Config.Scheduler.TempBytes,
	})
	service.SetQueueLimit(a.Config.Scheduler.QueueLimit)
	service.SetIdempotencyRetentionHours(a.Config.Scheduler.IdempotencyRetentionHours)
	service.SetModelAdmission(a.modelAdmission)
	service.SetResourceSampler(operations.NewFilesystemResourceSampler(
		a.DB.Path(), a.RawStore.Root(), filepath.Join(a.Home, "uploads"),
	))
	service.SetDiskFreeSampler(operations.NewFilesystemDiskFreeSampler(a.DB.Path()))
	service.SetRequestEnricher(a.enrichOperationRequest)
	service.Register("import_text", func(ctx context.Context, op operations.Operation, payload json.RawMessage, report func(operations.Progress)) (any, error) {
		var command importTextCommand
		if err := json.Unmarshal(payload, &command); err != nil {
			return nil, fmt.Errorf("decode import_text: %w", err)
		}
		if op.DocumentID == "" {
			return nil, fmt.Errorf("import_text requires a preallocated documentId")
		}
		resolved, skipped, err := a.operationItemResolved(ctx, op.ID, op.DocumentID)
		if err != nil {
			return nil, err
		}
		if resolved {
			report(operations.Progress{Phase: "ready", Completed: 1, Total: 1})
			if skipped {
				return documentOperationResult{Skipped: 1}, nil
			}
			if existing, _, getErr := a.Knowledge.GetDocumentWithContext(ctx, op.DocumentID, false); getErr == nil {
				return documentResult(existing), nil
			}
			return documentOperationResult{Succeeded: 1}, nil
		}
		report(operations.Progress{Phase: "importing", Total: 1})
		itemCtx := a.withOperationItemCommit(ctx, op.ID, op.Attempt, op.DocumentID)
		document, err := a.Knowledge.AddTextDocumentWithID(itemCtx, op.BaseID, op.DocumentID, command.Title, command.Content)
		if err != nil {
			return nil, err
		}
		if testImportPublished != nil {
			testImportPublished(op, document)
		}
		report(operations.Progress{Phase: "ready", Completed: 1, Total: 1})
		return documentResult(document), nil
	})
	service.Register("delete_document", func(ctx context.Context, op operations.Operation, payload json.RawMessage, report func(operations.Progress)) (any, error) {
		var command documentCommand
		if op.DocumentID != "" {
			command.DocumentID = op.DocumentID
		} else if err := json.Unmarshal(payload, &command); err != nil {
			return nil, fmt.Errorf("decode delete_document: %w", err)
		}
		resolved, skipped, err := a.operationItemResolved(ctx, op.ID, command.DocumentID)
		if err != nil {
			return nil, err
		}
		if resolved {
			if !skipped {
				current, getErr := a.Knowledge.GetDocumentIncludingDeletingWithContext(ctx, command.DocumentID)
				if getErr != nil && !errors.Is(getErr, knowledge.ErrNotFound) {
					return nil, getErr
				}
				if getErr == nil && current.LifecycleState == knowledge.LifecycleDeleting {
					// The committed marker is written with the logical delete fence,
					// before physical raw/chunk cleanup. A replay must resume that
					// cleanup instead of treating the marker as proof that every
					// tombstoned generation has already been removed.
					report(operations.Progress{Phase: "deleting", Total: 1})
					if _, err := a.Knowledge.DeleteDocumentWithProgress(ctx, command.DocumentID, nil); err != nil {
						return nil, err
					}
				}
			}
			report(operations.Progress{Phase: "deleted", Completed: 1, Total: 1})
			if skipped {
				return documentOperationResult{Skipped: 1}, nil
			}
			return documentOperationResult{Succeeded: 1}, nil
		}
		report(operations.Progress{Phase: "deleting", Total: 1})
		var onDeleted func()
		if a.deleteDocumentBoundary != nil {
			onDeleted = a.deleteDocumentBoundary
		}
		itemCtx := a.withOperationItemCommit(ctx, op.ID, op.Attempt, command.DocumentID)
		removed, err := a.Knowledge.DeleteDocumentWithProgress(itemCtx, command.DocumentID, onDeleted)
		if err != nil {
			return nil, err
		}
		report(operations.Progress{Phase: "deleting", Completed: 1, Total: 1})
		return documentOperationResult{Succeeded: removed}, nil
	})
	service.Register("import_files", func(ctx context.Context, op operations.Operation, payload json.RawMessage, report func(operations.Progress)) (any, error) {
		var command importFilesCommand
		if err := json.Unmarshal(payload, &command); err != nil {
			return nil, fmt.Errorf("decode import_files: %w", err)
		}
		if err := a.prepareImportFilesOperation(ctx, op, command.Files); err != nil {
			return nil, err
		}
		report(operations.Progress{Phase: "importing", Completed: 0, Total: len(command.Files)})
		itemCtx := knowledge.WithItemCommitHook(ctx, func(itemKey string) knowledge.CommitHook {
			return func(tx *sql.Tx) error {
				return a.Operations.MarkItemTx(tx, op.ID, op.Attempt, itemKey, operations.ItemCommitted, nil, "", "")
			}
		})
		result, err := a.Knowledge.AddFiles(itemCtx, op.BaseID, command.Files, command.Conflict, command.ParentDirectoryID)
		if markerErr := a.markImportFilesResultContext(ctx, op, command.Files, result); markerErr != nil {
			return nil, markerErr
		}
		if err != nil {
			return nil, err
		}
		report(operations.Progress{Phase: "ready", Completed: len(command.Files), Total: len(command.Files)})
		return result, nil
	})
	service.Register("delete_documents", func(ctx context.Context, op operations.Operation, payload json.RawMessage, report func(operations.Progress)) (any, error) {
		var command deleteDocumentsCommand
		if err := json.Unmarshal(payload, &command); err != nil {
			return nil, fmt.Errorf("decode delete_documents: %w", err)
		}
		return a.runDeleteDocumentsForOperation(ctx, op.ID, op.Attempt, op.BaseID, command.DocumentIDs, report)
	})
	service.Register("delete_base", func(ctx context.Context, op operations.Operation, _ json.RawMessage, report func(operations.Progress)) (any, error) {
		if op.BaseID == "" {
			return nil, fmt.Errorf("delete_base requires baseId")
		}
		resolved, skipped, err := a.operationItemResolved(ctx, op.ID, op.BaseID)
		if err != nil {
			return nil, err
		}
		if resolved {
			if !skipped {
				current, getErr := a.Knowledge.GetBaseIncludingDeletingWithContext(ctx, op.BaseID)
				if getErr != nil && !errors.Is(getErr, knowledge.ErrNotFound) {
					return nil, getErr
				}
				if getErr == nil && current.LifecycleState == knowledge.LifecycleDeleting {
					// DeleteBaseWithContext is resumable after its lifecycle fence
					// commits. Re-run the physical cleanup on replay; the idempotent
					// raw/chunk cleanup is the recovery step, not an already-finished
					// operation implied by the item marker.
					report(operations.Progress{Phase: "deleting", Total: 1})
					if err := a.Knowledge.DeleteBaseWithContext(ctx, op.BaseID); err != nil {
						return nil, err
					}
				}
			}
			report(operations.Progress{Phase: "deleted", Completed: 1, Total: 1})
			if skipped {
				return documentOperationResult{Skipped: 1}, nil
			}
			return map[string]any{"deleted": true}, nil
		}
		report(operations.Progress{Phase: "deleting", Total: 1})
		itemCtx := a.withOperationItemCommit(ctx, op.ID, op.Attempt, op.BaseID)
		if err := a.Knowledge.DeleteBaseWithContext(itemCtx, op.BaseID); err != nil {
			return nil, err
		}
		if err := a.markOperationItemContext(ctx, op.ID, op.Attempt, op.BaseID, operations.ItemCommitted, "", ""); err != nil {
			return nil, err
		}
		report(operations.Progress{Phase: "deleted", Completed: 1, Total: 1})
		return map[string]any{"deleted": true}, nil
	})
	service.Register("import_file", func(ctx context.Context, op operations.Operation, payload json.RawMessage, report func(operations.Progress)) (any, error) {
		var command importFileCommand
		if err := json.Unmarshal(payload, &command); err != nil {
			return nil, fmt.Errorf("decode import_file: %w", err)
		}
		if op.DocumentID == "" || command.UploadID == "" {
			return nil, fmt.Errorf("import_file requires documentId and uploadId")
		}
		resolved, skipped, err := a.operationItemResolved(ctx, op.ID, op.DocumentID)
		if err != nil {
			return nil, err
		}
		if resolved {
			report(operations.Progress{Phase: "ready", Completed: 1, Total: 1})
			if skipped {
				return documentOperationResult{Skipped: 1}, nil
			}
			if existing, _, getErr := a.Knowledge.GetDocumentWithContext(ctx, op.DocumentID, false); getErr == nil {
				return documentResult(existing), nil
			}
			return documentOperationResult{Succeeded: 1}, nil
		}
		session, path, err := a.Operations.UploadForOperationContext(ctx, op.ID, command.UploadID)
		if err != nil {
			return nil, err
		}
		file, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("open staged upload: %w", err)
		}
		data, err := io.ReadAll(io.LimitReader(file, operations.MaxUploadBytes+1))
		closeErr := file.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			return nil, err
		}
		if int64(len(data)) != session.SizeBytes {
			return nil, operations.ErrUploadSizeMismatch
		}
		fileName := strings.TrimSpace(command.FileName)
		if fileName == "" {
			fileName = session.FileName
		}
		base, err := a.Knowledge.GetBaseWithContext(ctx, op.BaseID)
		if err != nil {
			return nil, err
		}
		conflict := command.Conflict
		if conflict == "" {
			conflict = knowledge.ResolveBaseConfig(a.Config, base.Config).ConflictStrategy
		}
		if conflict == "" {
			conflict = "rename"
		}
		switch conflict {
		case "detect", "rename", "replace", "keep":
		default:
			return nil, fmt.Errorf("invalid conflict strategy %q", conflict)
		}
		title := fileName
		if conflict == "detect" {
			if conflicts, conflictErr := a.Knowledge.DetectConflictsContext(ctx, op.BaseID, []string{title}); conflictErr != nil {
				return nil, conflictErr
			} else if len(conflicts) > 0 {
				return nil, &knowledge.ConflictError{Conflicts: conflicts}
			}
		} else if conflict == "replace" {
			existing, err := a.Knowledge.FindDocumentByTitleContext(ctx, op.BaseID, title)
			if err != nil && !errors.Is(err, knowledge.ErrNotFound) {
				return nil, err
			}
			if err == nil {
				replaceItemKey := "replace:" + existing.ID
				resolved, _, resolveErr := a.operationItemResolved(ctx, op.ID, replaceItemKey)
				if resolveErr != nil {
					return nil, resolveErr
				}
				if !resolved {
					replaceCtx := knowledge.WithCommitHook(ctx, func(tx *sql.Tx) error {
						return a.Operations.MarkItemTx(tx, op.ID, op.Attempt, replaceItemKey, operations.ItemCommitted, nil, "", "")
					})
					if _, err := a.Knowledge.DeleteDocumentWithProgress(replaceCtx, existing.ID, nil); err != nil && !errors.Is(err, knowledge.ErrNotFound) {
						return nil, err
					}
				}
			}
		} else if conflict == "rename" {
			_, err := a.Knowledge.FindDocumentByTitleContext(ctx, op.BaseID, title)
			if err != nil && !errors.Is(err, knowledge.ErrNotFound) {
				return nil, err
			}
			if err == nil {
				var renameErr error
				title, renameErr = a.Knowledge.RenameAvailableContext(ctx, op.BaseID, title)
				if renameErr != nil {
					return nil, renameErr
				}
			}
		}
		report(operations.Progress{Phase: "importing", Total: 1})
		itemCtx := a.withOperationItemCommit(ctx, op.ID, op.Attempt, op.DocumentID)
		document, err := a.Knowledge.AddFileDocumentWithID(itemCtx, op.BaseID, op.DocumentID, title, data, command.ParentDirectoryID)
		if err != nil {
			return nil, err
		}
		report(operations.Progress{Phase: "ready", Completed: 1, Total: 1})
		return documentResult(document), nil
	})
	service.Register("reindex_document", func(ctx context.Context, op operations.Operation, payload json.RawMessage, report func(operations.Progress)) (any, error) {
		var command documentCommand
		if op.DocumentID != "" {
			command.DocumentID = op.DocumentID
		} else if err := json.Unmarshal(payload, &command); err != nil {
			return nil, fmt.Errorf("decode reindex_document: %w", err)
		}
		resolved, skipped, err := a.operationItemResolved(ctx, op.ID, command.DocumentID)
		if err != nil {
			return nil, err
		}
		if resolved {
			report(operations.Progress{Phase: "ready", Completed: 1, Total: 1})
			if skipped {
				return documentOperationResult{Skipped: 1}, nil
			}
			if existing, _, getErr := a.Knowledge.GetDocumentWithContext(ctx, command.DocumentID, false); getErr == nil {
				return documentResult(existing), nil
			}
			return documentOperationResult{Succeeded: 1}, nil
		}
		report(operations.Progress{Phase: "reindexing", Total: 1})
		itemCtx := a.withOperationItemCommit(ctx, op.ID, op.Attempt, command.DocumentID)
		document, err := a.Knowledge.ReindexDocument(itemCtx, command.DocumentID)
		if err != nil {
			return nil, err
		}
		report(operations.Progress{Phase: "ready", Completed: 1, Total: 1})
		return documentResult(document), nil
	})
	service.Register("reindex_documents", func(ctx context.Context, op operations.Operation, payload json.RawMessage, report func(operations.Progress)) (any, error) {
		var command reindexDocumentsCommand
		if err := json.Unmarshal(payload, &command); err != nil {
			return nil, fmt.Errorf("decode reindex_documents: %w", err)
		}
		return a.runReindexDocumentsForOperation(ctx, op.ID, op.Attempt, op.BaseID, command.DocumentIDs, report)
	})
	service.Register("reindex_base", func(ctx context.Context, op operations.Operation, _ json.RawMessage, report func(operations.Progress)) (any, error) {
		if op.BaseID == "" {
			return nil, fmt.Errorf("reindex_base requires baseId")
		}
		return a.runReindexBaseForOperation(ctx, op.ID, op.Attempt, op.BaseID, report)
	})
	service.Register("restore_base", func(ctx context.Context, op operations.Operation, payload json.RawMessage, report func(operations.Progress)) (any, error) {
		var command restoreBaseCommand
		if err := json.Unmarshal(payload, &command); err != nil {
			return nil, fmt.Errorf("decode restore_base: %w", err)
		}
		if op.BaseID == "" || command.TargetBaseID == "" {
			if op.BaseID == "" {
				return nil, fmt.Errorf("restore_base requires source base id")
			}
			command.TargetBaseID = op.ID
		}
		itemCtx := knowledge.WithItemCommitHook(ctx, func(itemKey string) knowledge.CommitHook {
			return func(tx *sql.Tx) error {
				return a.Operations.MarkItemTx(tx, op.ID, op.Attempt, itemKey, operations.ItemCommitted, nil, "", "")
			}
		})
		base, err := a.Knowledge.RestoreBaseWithID(itemCtx, op.BaseID, command.TargetBaseID, command.Name, command.Config,
			func(update jobs.ProgressUpdate) {
				report(operations.Progress{Phase: update.Phase, Completed: update.Completed, Total: update.Total})
			})
		if err != nil {
			return nil, err
		}
		report(operations.Progress{Phase: "ready", Completed: 1, Total: 1})
		return map[string]any{"id": base.ID, "name": base.Name, "group": base.Group}, nil
	})
	service.Register("import_url", func(ctx context.Context, op operations.Operation, payload json.RawMessage, report func(operations.Progress)) (any, error) {
		var command importURLCommand
		if err := json.Unmarshal(payload, &command); err != nil {
			return nil, fmt.Errorf("decode import_url: %w", err)
		}
		if op.DocumentID == "" {
			return nil, fmt.Errorf("import_url requires a preallocated documentId")
		}
		resolved, skipped, err := a.operationItemResolved(ctx, op.ID, op.DocumentID)
		if err != nil {
			return nil, err
		}
		if resolved {
			// A prior attempt may have published the document but failed before
			// consuming its immutable URL capture. Reconcile that cleanup before
			// replaying the resolved business result.
			if err := a.Operations.ConsumeURLCapture(ctx, op.ID); err != nil {
				return nil, err
			}
			report(operations.Progress{Phase: "ready", Completed: 1, Total: 1})
			if skipped {
				return documentOperationResult{Skipped: 1}, nil
			}
			if existing, _, getErr := a.Knowledge.GetDocumentWithContext(ctx, op.DocumentID, false); getErr == nil {
				return documentResult(existing), nil
			}
			return documentOperationResult{Succeeded: 1}, nil
		}
		// A ready document means the business effect committed before the
		// previous worker wrote the terminal state.
		if existing, _, err := a.Knowledge.GetDocumentWithContext(ctx, op.DocumentID, false); err == nil && existing.Status == knowledge.StatusReady {
			return documentResult(existing), nil
		}
		report(operations.Progress{Phase: "fetching", Total: 1})
		finalURL, body, contentType, err := knowledge.FetchURL(ctx, command.URL)
		if err != nil {
			return nil, err
		}
		capture, err := a.Operations.EnsureURLCapture(ctx, op.ID, command.URL, finalURL, contentType, body)
		if err != nil {
			return nil, err
		}
		report(operations.Progress{Phase: "importing", CompletedBytes: capture.SizeBytes, TotalBytes: capture.SizeBytes})
		itemCtx := a.withOperationItemCommit(ctx, op.ID, op.Attempt, op.DocumentID)
		document, err := a.Knowledge.AddUrlDocumentWithID(itemCtx, op.BaseID, op.DocumentID, capture.FinalURL, command.Title, capture.Body)
		if err != nil {
			return nil, err
		}
		if err := a.Operations.ConsumeURLCapture(ctx, op.ID); err != nil {
			return nil, err
		}
		report(operations.Progress{Phase: "ready", Completed: 1, Total: 1})
		return documentResult(document), nil
	})
	service.Register("refresh_url", func(ctx context.Context, op operations.Operation, payload json.RawMessage, report func(operations.Progress)) (any, error) {
		var command documentCommand
		if op.DocumentID != "" {
			command.DocumentID = op.DocumentID
		} else if err := json.Unmarshal(payload, &command); err != nil {
			return nil, fmt.Errorf("decode refresh_url: %w", err)
		}
		resolved, skipped, err := a.operationItemResolved(ctx, op.ID, command.DocumentID)
		if err != nil {
			return nil, err
		}
		if resolved {
			// The marker proves the refresh publication, while capture cleanup
			// can still have been interrupted between those two commits.
			if err := a.Operations.ConsumeURLCapture(ctx, op.ID); err != nil {
				return nil, err
			}
			report(operations.Progress{Phase: "ready", Completed: 1, Total: 1})
			if skipped {
				return documentOperationResult{Skipped: 1}, nil
			}
			if existing, _, getErr := a.Knowledge.GetDocumentWithContext(ctx, command.DocumentID, false); getErr == nil {
				return documentResult(existing), nil
			}
			return documentOperationResult{Succeeded: 1}, nil
		}
		existing, _, err := a.Knowledge.GetDocumentWithContext(ctx, command.DocumentID, false)
		if err != nil {
			return nil, err
		}
		if existing.SourceType != "url" || existing.URL == "" {
			return nil, fmt.Errorf("document %s is not a url source", command.DocumentID)
		}
		report(operations.Progress{Phase: "fetching", Total: 1})
		finalURL, body, contentType, err := knowledge.FetchURL(ctx, existing.URL)
		if err != nil {
			return nil, err
		}
		capture, err := a.Operations.EnsureURLCapture(ctx, op.ID, existing.URL, finalURL, contentType, body)
		if err != nil {
			return nil, err
		}
		report(operations.Progress{Phase: "importing", CompletedBytes: capture.SizeBytes, TotalBytes: capture.SizeBytes})
		itemCtx := a.withOperationItemCommit(ctx, op.ID, op.Attempt, command.DocumentID)
		changed, document, err := a.Knowledge.RefreshUrlDocumentFromCapture(itemCtx, command.DocumentID, capture.FinalURL, capture.Body)
		if err != nil {
			return nil, err
		}
		if err := a.Operations.ConsumeURLCapture(ctx, op.ID); err != nil {
			return nil, err
		}
		report(operations.Progress{Phase: "ready", Completed: 1, Total: 1})
		result := documentResult(document)
		result.Skipped = boolToInt(!changed)
		result.Succeeded = boolToInt(changed)
		return result, nil
	})
	service.Register("import_directory", func(ctx context.Context, op operations.Operation, payload json.RawMessage, report func(operations.Progress)) (any, error) {
		var command importDirectoryCommand
		if err := json.Unmarshal(payload, &command); err != nil {
			return nil, fmt.Errorf("decode import_directory: %w", err)
		}
		existing, findErr := a.Knowledge.FindDirectoryByPathContext(ctx, op.BaseID, command.Path)
		if findErr != nil && !errors.Is(findErr, knowledge.ErrNotFound) {
			return nil, findErr
		}
		if findErr == nil {
			resolved, skipped, resolveErr := a.operationItemResolved(ctx, op.ID, existing.ID)
			if resolveErr != nil {
				return nil, resolveErr
			}
			if resolved {
				report(operations.Progress{Phase: "ready", Completed: 1, Total: 1})
				if skipped {
					return documentOperationResult{Skipped: 1}, nil
				}
				return documentResult(existing), nil
			}
		}
		document, err := a.Knowledge.RunDirectoryImport(ctx, op.BaseID, command.Path, directoryProgressReporter(report))
		if err != nil {
			partial := documentOperationResult{Failed: 1}
			if document.ID != "" {
				partial.Documents = []knowledge.Document{document}
				if markerErr := a.markOperationItemContext(ctx, op.ID, op.Attempt, document.ID, operations.ItemFailed, "directory_sync_failed", err.Error()); markerErr != nil {
					return partial, markerErr
				}
			}
			return partial, err
		}
		if document.ID != "" {
			if markerErr := a.markOperationItemContext(ctx, op.ID, op.Attempt, document.ID, operations.ItemCommitted, "", ""); markerErr != nil {
				return nil, markerErr
			}
		}
		return documentResult(document), nil
	})
	service.Register("rescan_directory", func(ctx context.Context, op operations.Operation, payload json.RawMessage, report func(operations.Progress)) (any, error) {
		var command documentCommand
		if op.DocumentID != "" {
			command.DocumentID = op.DocumentID
		} else if err := json.Unmarshal(payload, &command); err != nil {
			return nil, fmt.Errorf("decode rescan_directory: %w", err)
		}
		resolved, skipped, err := a.operationItemResolved(ctx, op.ID, command.DocumentID)
		if err != nil {
			return nil, err
		}
		if resolved {
			report(operations.Progress{Phase: "ready", Completed: 1, Total: 1})
			if skipped {
				return documentOperationResult{Skipped: 1}, nil
			}
			if existing, _, getErr := a.Knowledge.GetDocumentWithContext(ctx, command.DocumentID, false); getErr == nil {
				return documentResult(existing), nil
			}
			return documentOperationResult{Succeeded: 1}, nil
		}
		document, err := a.Knowledge.RunDirectoryRescan(ctx, command.DocumentID, directoryProgressReporter(report))
		if err != nil {
			partial := documentOperationResult{Failed: 1}
			if document.ID != "" {
				partial.Documents = []knowledge.Document{document}
				if markerErr := a.markOperationItemContext(ctx, op.ID, op.Attempt, document.ID, operations.ItemFailed, "directory_sync_failed", err.Error()); markerErr != nil {
					return partial, markerErr
				}
			}
			return partial, err
		}
		if markerErr := a.markOperationItemContext(ctx, op.ID, op.Attempt, command.DocumentID, operations.ItemCommitted, "", ""); markerErr != nil {
			return nil, markerErr
		}
		return documentResult(document), nil
	})
	service.Register("delete_directory", func(ctx context.Context, op operations.Operation, payload json.RawMessage, report func(operations.Progress)) (any, error) {
		var command documentCommand
		if op.DocumentID != "" {
			command.DocumentID = op.DocumentID
		} else if err := json.Unmarshal(payload, &command); err != nil {
			return nil, fmt.Errorf("decode delete_directory: %w", err)
		}
		completed := 0
		total := 0
		if op.TotalUnits != nil {
			total = *op.TotalUnits
		}
		resolved, skipped, err := a.operationItemResolved(ctx, op.ID, command.DocumentID)
		if err != nil {
			return nil, err
		}
		if resolved {
			if !skipped {
				current, getErr := a.Knowledge.GetDocumentIncludingDeletingWithContext(ctx, command.DocumentID)
				if getErr != nil && !errors.Is(getErr, knowledge.ErrNotFound) {
					return nil, getErr
				}
				if getErr == nil && current.LifecycleState == knowledge.LifecycleDeleting {
					// deleteDocumentTree commits the directory tombstone before
					// deleting every descendant's physical data. Keep the retry
					// boundary at the logical fence, but resume bounded cleanup.
					report(operations.Progress{Phase: "deleting", Completed: 0, Total: total})
					removed, err := a.Knowledge.DeleteDirectoryRecursiveWithProgress(ctx, command.DocumentID, nil)
					if err != nil {
						return nil, err
					}
					report(operations.Progress{Phase: "deleting", Completed: removed, Total: total})
				}
			}
			report(operations.Progress{Phase: "deleted", Completed: 1, Total: 1})
			if skipped {
				return documentOperationResult{Skipped: 1}, nil
			}
			return documentOperationResult{Succeeded: 1}, nil
		}
		report(operations.Progress{Phase: "deleting", Completed: 0, Total: total})
		itemCtx := a.withOperationItemCommit(ctx, op.ID, op.Attempt, command.DocumentID)
		removed, err := a.Knowledge.DeleteDirectoryRecursiveWithProgress(itemCtx, command.DocumentID, func() {
			completed++
			report(operations.Progress{Phase: "deleting", Completed: completed, Total: total})
		})
		if err != nil {
			if errors.Is(err, knowledge.ErrNotFound) {
				if markerErr := a.markOperationItemContext(ctx, op.ID, op.Attempt, command.DocumentID, operations.ItemSkipped, "", ""); markerErr != nil {
					return nil, markerErr
				}
				return documentOperationResult{Skipped: 1}, nil
			}
			if markerErr := a.markOperationItemContext(ctx, op.ID, op.Attempt, command.DocumentID, operations.ItemFailed, "delete_failed", err.Error()); markerErr != nil {
				return documentOperationResult{Failed: 1}, markerErr
			}
			return documentOperationResult{Failed: 1}, err
		}
		if markerErr := a.markOperationItemContext(ctx, op.ID, op.Attempt, command.DocumentID, operations.ItemCommitted, "", ""); markerErr != nil {
			return nil, markerErr
		}
		return documentOperationResult{Succeeded: removed}, nil
	})
	service.Register("download_ocr_model", func(ctx context.Context, op operations.Operation, _ json.RawMessage, report func(operations.Progress)) (any, error) {
		const itemKey = "effect:download_ocr_model"
		markerResult, resolved, skipped, err := a.operationItemResult(ctx, op.ID, itemKey)
		if err != nil {
			return nil, err
		}
		if resolved {
			report(operations.Progress{Phase: "ready", Completed: 100, Total: 100})
			if len(markerResult) > 0 {
				return json.RawMessage(markerResult), nil
			}
			if skipped {
				return map[string]any{"skipped": true, "modelId": models.OCRModelID, "kind": "ocr"}, nil
			}
			return map[string]any{"modelId": models.OCRModelID, "kind": "ocr"}, nil
		}
		if op.Attempt > 1 {
			status, statusErr := a.Models.OCRStatusContext(ctx)
			if statusErr == nil && status.Status == "installed" {
				result := map[string]any{"modelId": models.OCRModelID, "kind": "ocr", "reconciled": true}
				if markerErr := a.markOperationItemResultContext(ctx, op.ID, op.Attempt, itemKey, operations.ItemCommitted, result, "", ""); markerErr != nil {
					return nil, markerErr
				}
				report(operations.Progress{Phase: "ready", Completed: 100, Total: 100})
				return map[string]any{"modelId": models.OCRModelID, "kind": "ocr", "reconciled": true}, nil
			}
		}
		report(operations.Progress{Phase: "downloading", Completed: 0, Total: 100})
		if err := a.Models.DownloadOCR(ctx, percentProgressReporter(report)); err != nil {
			return nil, err
		}
		result := map[string]any{"modelId": models.OCRModelID, "kind": "ocr"}
		if err := a.markOperationItemResultContext(ctx, op.ID, op.Attempt, itemKey, operations.ItemCommitted, result, "", ""); err != nil {
			return nil, err
		}
		report(operations.Progress{Phase: "ready", Completed: 100, Total: 100})
		return result, nil
	})
	service.Register("remove_ocr_model", func(ctx context.Context, op operations.Operation, _ json.RawMessage, report func(operations.Progress)) (any, error) {
		const itemKey = "effect:remove_ocr_model"
		markerResult, resolved, skipped, err := a.operationItemResult(ctx, op.ID, itemKey)
		if err != nil {
			return nil, err
		}
		if resolved {
			report(operations.Progress{Phase: "removed", Completed: 1, Total: 1})
			if len(markerResult) > 0 {
				return json.RawMessage(markerResult), nil
			}
			return map[string]any{"removed": !skipped, "skipped": skipped, "modelId": models.OCRModelID, "kind": "ocr"}, nil
		}
		report(operations.Progress{Phase: "removing", Total: 1})
		if err := a.Models.RemoveContext(ctx, models.OCRModelID); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		result := map[string]any{"removed": true, "modelId": models.OCRModelID, "kind": "ocr"}
		if err := a.markOperationItemResultContext(ctx, op.ID, op.Attempt, itemKey, operations.ItemCommitted, result, "", ""); err != nil {
			return nil, err
		}
		report(operations.Progress{Phase: "removed", Completed: 1, Total: 1})
		return result, nil
	})
	service.Register("download_model", func(ctx context.Context, op operations.Operation, payload json.RawMessage, report func(operations.Progress)) (any, error) {
		var command modelDownloadCommand
		if err := json.Unmarshal(payload, &command); err != nil {
			return nil, fmt.Errorf("decode download_model: %w", err)
		}
		if command.ID == "" || (command.Kind != models.KindEmbedding && command.Kind != models.KindRerank) {
			return nil, fmt.Errorf("download_model requires a model id and embedding/rerank kind")
		}
		itemKey := "effect:download_model:" + command.Kind + ":" + command.ID
		markerResult, resolved, skipped, err := a.operationItemResult(ctx, op.ID, itemKey)
		if err != nil {
			return nil, err
		}
		if resolved {
			report(operations.Progress{Phase: "ready", Completed: 100, Total: 100})
			if len(markerResult) > 0 {
				return json.RawMessage(markerResult), nil
			}
			return map[string]any{"modelId": command.ID, "kind": command.Kind, "managed": command.Managed, "skipped": skipped}, nil
		}
		if op.Attempt > 1 && !command.Managed {
			installed, getErr := a.Models.GetContext(ctx, command.ID)
			if getErr == nil && installed.Kind == command.Kind && installed.Status == "installed" {
				result := map[string]any{"modelId": command.ID, "kind": command.Kind, "managed": command.Managed, "reconciled": true}
				if markerErr := a.markOperationItemResultContext(ctx, op.ID, op.Attempt, itemKey, operations.ItemCommitted, result, "", ""); markerErr != nil {
					return nil, markerErr
				}
				report(operations.Progress{Phase: "ready", Completed: 100, Total: 100})
				return map[string]any{"modelId": command.ID, "kind": command.Kind, "managed": command.Managed, "reconciled": true}, nil
			}
		}
		if command.Managed {
			managed, ok := a.Runtime.(runtime.ManagedModelController)
			if !ok {
				return nil, errors.New("managed runtime model control is unavailable")
			}
			if op.Attempt > 1 {
				capability := runtime.CapabilityEmbedding
				if command.Kind == models.KindRerank {
					capability = runtime.CapabilityRerank
				}
				health := a.Runtime.Status(ctx)[capability]
				healthModel := strings.TrimPrefix(strings.TrimSpace(health.Model), "local:")
				if healthModel == command.ID && health.Lifecycle != models.LifecycleNotInstalled && health.Lifecycle != models.LifecycleFailed {
					result := map[string]any{"modelId": command.ID, "kind": command.Kind, "managed": true, "reconciled": true}
					if markerErr := a.markOperationItemResultContext(ctx, op.ID, op.Attempt, itemKey, operations.ItemCommitted, result, "", ""); markerErr != nil {
						return nil, markerErr
					}
					report(operations.Progress{Phase: "ready", Completed: 100, Total: 100})
					return map[string]any{"modelId": command.ID, "kind": command.Kind, "managed": true, "reconciled": true}, nil
				}
			}
			if progressive, ok := managed.(runtime.ProgressiveManagedModelController); ok {
				_, err := progressive.LoadModelWithProgress(ctx, command.Kind, command.ID, runtimeProgressReporter(report))
				if err != nil {
					return nil, err
				}
			} else if _, err := managed.LoadModel(ctx, command.Kind, command.ID); err != nil {
				return nil, err
			}
		} else {
			if err := a.Models.Download(ctx, models.DownloadRequest{
				ID: command.ID, Kind: command.Kind, Artifacts: command.Artifacts,
			}, percentProgressReporter(report)); err != nil {
				return nil, err
			}
		}
		result := map[string]any{"modelId": command.ID, "kind": command.Kind, "managed": command.Managed}
		if err := a.markOperationItemResultContext(ctx, op.ID, op.Attempt, itemKey, operations.ItemCommitted, result, "", ""); err != nil {
			return nil, err
		}
		report(operations.Progress{Phase: "ready", Completed: 100, Total: 100})
		return result, nil
	})
	service.Register("remove_model", func(ctx context.Context, op operations.Operation, payload json.RawMessage, report func(operations.Progress)) (any, error) {
		var command modelRemoveCommand
		if err := json.Unmarshal(payload, &command); err != nil {
			return nil, fmt.Errorf("decode remove_model: %w", err)
		}
		command.ID = strings.TrimPrefix(strings.TrimSpace(command.ID), "local:")
		if command.ID == "" {
			return nil, errors.New("remove_model requires a model id")
		}
		itemKey := "effect:remove_model:" + command.ID
		markerResult, resolved, skipped, err := a.operationItemResult(ctx, op.ID, itemKey)
		if err != nil {
			return nil, err
		}
		if resolved {
			report(operations.Progress{Phase: "removed", Completed: 1, Total: 1})
			if len(markerResult) > 0 {
				return json.RawMessage(markerResult), nil
			}
			return map[string]any{"removed": !skipped, "skipped": skipped, "modelId": command.ID, "kind": command.Kind, "managed": command.Managed}, nil
		}
		report(operations.Progress{Phase: "removing", Total: 1})
		if command.Managed {
			managed, ok := a.Runtime.(runtime.ManagedModelController)
			if !ok || command.Capability == "" {
				return nil, errors.New("managed runtime model control is unavailable")
			}
			if op.Attempt > 1 {
				health := a.Runtime.Status(ctx)[command.Capability]
				healthModel := strings.TrimPrefix(strings.TrimSpace(health.Model), "local:")
				if healthModel != command.ID || health.Lifecycle == models.LifecycleNotInstalled || health.Lifecycle == models.LifecycleFailed {
					result := map[string]any{"removed": true, "modelId": command.ID, "kind": command.Kind, "managed": true, "reconciled": true}
					if markerErr := a.markOperationItemResultContext(ctx, op.ID, op.Attempt, itemKey, operations.ItemCommitted, result, "", ""); markerErr != nil {
						return nil, markerErr
					}
					report(operations.Progress{Phase: "removed", Completed: 1, Total: 1})
					return map[string]any{"removed": true, "modelId": command.ID, "kind": command.Kind, "managed": true, "reconciled": true}, nil
				}
			}
			if err := managed.RemoveModel(ctx, command.Capability, command.ID); err != nil {
				return nil, err
			}
		} else if command.CustomReranker {
			if err := a.Knowledge.DeleteCustomRerankerContext(ctx, command.ID); err != nil {
				return nil, err
			}
		} else {
			if err := a.Models.RemoveContext(ctx, command.ID); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return nil, err
			}
			if command.Kind == models.KindRerank {
				if err := a.Knowledge.DeleteCustomRerankerContext(ctx, command.ID); err != nil {
					return nil, err
				}
			}
		}
		result := map[string]any{"removed": true, "modelId": command.ID, "kind": command.Kind, "managed": command.Managed}
		if err := a.markOperationItemResultContext(ctx, op.ID, op.Attempt, itemKey, operations.ItemCommitted, result, "", ""); err != nil {
			return nil, err
		}
		report(operations.Progress{Phase: "removed", Completed: 1, Total: 1})
		return result, nil
	})
	service.Register("self_test_reranker", func(ctx context.Context, op operations.Operation, payload json.RawMessage, report func(operations.Progress)) (any, error) {
		var command modelIDCommand
		if err := json.Unmarshal(payload, &command); err != nil {
			return nil, fmt.Errorf("decode self_test_reranker: %w", err)
		}
		if command.ID == "" {
			return nil, fmt.Errorf("self_test_reranker requires a model id")
		}
		itemKey := "effect:self_test_reranker:" + command.ID
		markerResult, resolved, skipped, err := a.operationItemResult(ctx, op.ID, itemKey)
		if err != nil {
			return nil, err
		}
		if resolved {
			report(operations.Progress{Phase: "ready", Completed: 100, Total: 100})
			if len(markerResult) > 0 {
				return json.RawMessage(markerResult), nil
			}
			return map[string]any{"id": command.ID, "skipped": skipped}, nil
		}
		result, err := a.SelfTestRerankerWithProgress(ctx, command.ID, runtimeProgressReporter(report))
		if err != nil {
			return nil, err
		}
		if err := a.markOperationItemResultContext(ctx, op.ID, op.Attempt, itemKey, operations.ItemCommitted, result, "", ""); err != nil {
			return nil, err
		}
		return result, nil
	})
	service.Register("plan_model_cache_migration", func(ctx context.Context, op operations.Operation, payload json.RawMessage, report func(operations.Progress)) (any, error) {
		var command modelCacheMigrationCommand
		if err := json.Unmarshal(payload, &command); err != nil {
			return nil, fmt.Errorf("decode plan_model_cache_migration: %w", err)
		}
		if command.TargetDir == "" {
			return nil, fmt.Errorf("plan_model_cache_migration requires targetDir")
		}
		itemKey := "effect:plan_model_cache_migration:" + command.TargetDir
		markerResult, resolved, skipped, err := a.operationItemResult(ctx, op.ID, itemKey)
		if err != nil {
			return nil, err
		}
		if resolved {
			report(operations.Progress{Phase: "ready", Completed: 1, Total: 1})
			if len(markerResult) > 0 {
				return json.RawMessage(markerResult), nil
			}
			return map[string]any{"targetDir": command.TargetDir, "skipped": skipped}, nil
		}
		report(operations.Progress{Phase: "planning", Total: 1})
		plan, err := a.Models.PlanMigrationContext(ctx, command.TargetDir)
		if err != nil {
			return nil, err
		}
		if err := a.markOperationItemResultContext(ctx, op.ID, op.Attempt, itemKey, operations.ItemCommitted, plan, "", ""); err != nil {
			return nil, err
		}
		report(operations.Progress{Phase: "ready", Completed: 1, Total: 1})
		return plan, nil
	})
	service.Register("migrate_model_cache", func(ctx context.Context, op operations.Operation, payload json.RawMessage, report func(operations.Progress)) (any, error) {
		var command modelCacheMigrationCommand
		if err := json.Unmarshal(payload, &command); err != nil {
			return nil, fmt.Errorf("decode migrate_model_cache: %w", err)
		}
		if command.TargetDir == "" {
			return nil, fmt.Errorf("migrate_model_cache requires targetDir")
		}
		itemKey := fmt.Sprintf("effect:migrate_model_cache:%s:%t", command.TargetDir, command.RemoveSource)
		markerResult, resolved, skipped, err := a.operationItemResult(ctx, op.ID, itemKey)
		if err != nil {
			return nil, err
		}
		if resolved {
			report(operations.Progress{Phase: "ready", Completed: 1, Total: 1})
			if len(markerResult) > 0 {
				return json.RawMessage(markerResult), nil
			}
			return map[string]any{"targetDir": command.TargetDir, "skipped": skipped}, nil
		}
		report(operations.Progress{Phase: "migrating", Total: 1})
		result, err := a.MigrateModelCacheContext(ctx, command.TargetDir, command.RemoveSource)
		if err != nil {
			return nil, err
		}
		if err := a.markOperationItemResultContext(ctx, op.ID, op.Attempt, itemKey, operations.ItemCommitted, result, "", ""); err != nil {
			return nil, err
		}
		report(operations.Progress{Phase: "ready", Completed: 1, Total: 1})
		return result, nil
	})
	service.Register("ollama_pull", func(ctx context.Context, op operations.Operation, payload json.RawMessage, report func(operations.Progress)) (any, error) {
		var command ollamaPullCommand
		if err := json.Unmarshal(payload, &command); err != nil {
			return nil, fmt.Errorf("decode ollama_pull: %w", err)
		}
		if command.Model == "" {
			return nil, fmt.Errorf("ollama_pull requires model")
		}
		command.Model = strings.TrimSpace(command.Model)
		itemKey := "effect:ollama_pull:" + command.Model
		markerResult, resolved, skipped, err := a.operationItemResult(ctx, op.ID, itemKey)
		if err != nil {
			return nil, err
		}
		if resolved {
			report(operations.Progress{Phase: "ready", Completed: 100, Total: 100})
			if len(markerResult) > 0 {
				return json.RawMessage(markerResult), nil
			}
			return map[string]any{"model": command.Model, "skipped": skipped}, nil
		}
		if op.Attempt > 1 && a.Ollama != nil {
			if tags, tagsErr := a.Ollama.Tags(ctx); tagsErr == nil {
				for _, model := range tags {
					if strings.TrimSpace(model.Name) == command.Model {
						result := map[string]any{"model": command.Model, "reconciled": true}
						if markerErr := a.markOperationItemResultContext(ctx, op.ID, op.Attempt, itemKey, operations.ItemCommitted, result, "", ""); markerErr != nil {
							return nil, markerErr
						}
						report(operations.Progress{Phase: "ready", Completed: 100, Total: 100})
						return result, nil
					}
				}
			}
		}
		report(operations.Progress{Phase: "downloading", Completed: 0, Total: 100})
		if err := a.Ollama.Pull(ctx, command.Model, percentProgressReporter(report)); err != nil {
			return nil, err
		}
		result := map[string]any{"model": command.Model}
		if err := a.markOperationItemResultContext(ctx, op.ID, op.Attempt, itemKey, operations.ItemCommitted, result, "", ""); err != nil {
			return nil, err
		}
		report(operations.Progress{Phase: "ready", Completed: 100, Total: 100})
		return result, nil
	})
	service.Register("ollama_delete", func(ctx context.Context, op operations.Operation, payload json.RawMessage, report func(operations.Progress)) (any, error) {
		var command ollamaDeleteCommand
		if err := json.Unmarshal(payload, &command); err != nil {
			return nil, fmt.Errorf("decode ollama_delete: %w", err)
		}
		command.Model = strings.TrimSpace(command.Model)
		if command.Model == "" {
			return nil, errors.New("ollama_delete requires model")
		}
		itemKey := "effect:ollama_delete:" + command.Model
		markerResult, resolved, skipped, err := a.operationItemResult(ctx, op.ID, itemKey)
		if err != nil {
			return nil, err
		}
		if resolved {
			report(operations.Progress{Phase: "removed", Completed: 1, Total: 1})
			if len(markerResult) > 0 {
				return json.RawMessage(markerResult), nil
			}
			return map[string]any{"removed": !skipped, "skipped": skipped, "model": command.Model}, nil
		}
		report(operations.Progress{Phase: "removing", Total: 1})
		if a.Ollama == nil {
			return nil, errors.New("ollama is unavailable")
		}
		if err := a.Ollama.Delete(ctx, command.Model); err != nil {
			if op.Attempt > 1 {
				if tags, tagsErr := a.Ollama.Tags(ctx); tagsErr == nil {
					present := false
					for _, model := range tags {
						if strings.TrimSpace(model.Name) == command.Model {
							present = true
							break
						}
					}
					if !present {
						result := map[string]any{"removed": true, "model": command.Model, "reconciled": true}
						if markerErr := a.markOperationItemResultContext(ctx, op.ID, op.Attempt, itemKey, operations.ItemCommitted, result, "", ""); markerErr != nil {
							return nil, markerErr
						}
						report(operations.Progress{Phase: "removed", Completed: 1, Total: 1})
						return result, nil
					}
				}
			}
			return nil, err
		}
		result := map[string]any{"removed": true, "model": command.Model}
		if err := a.markOperationItemResultContext(ctx, op.ID, op.Attempt, itemKey, operations.ItemCommitted, result, "", ""); err != nil {
			return nil, err
		}
		report(operations.Progress{Phase: "removed", Completed: 1, Total: 1})
		return result, nil
	})
	service.Register("maintenance_storage", func(ctx context.Context, op operations.Operation, payload json.RawMessage, report func(operations.Progress)) (any, error) {
		var command storageMaintenanceCommand
		if err := json.Unmarshal(payload, &command); err != nil {
			return nil, fmt.Errorf("decode maintenance_storage: %w", err)
		}
		itemKey := fmt.Sprintf("effect:maintenance_storage:%t:%t:%t:%t:%t:%d", command.DryRun, command.PurgeQuarantine, command.SQLiteOnly, command.OptimizeFTS, command.Vacuum, command.VacuumThreshold)
		markerResult, resolved, skipped, err := a.operationItemResult(ctx, op.ID, itemKey)
		if err != nil {
			return nil, err
		}
		if resolved {
			report(operations.Progress{Phase: "ready", Completed: 1, Total: 1})
			if len(markerResult) > 0 {
				return json.RawMessage(markerResult), nil
			}
			return map[string]any{"skipped": skipped}, nil
		}
		if command.SQLiteOnly {
			report(operations.Progress{Phase: "sqlite-maintenance", Total: 1})
			result, err := a.DB.MaintainSQLiteContext(ctx, command.OptimizeFTS, command.Vacuum, command.VacuumThreshold)
			if err != nil {
				return nil, err
			}
			if err := a.markOperationItemResultContext(ctx, op.ID, op.Attempt, itemKey, operations.ItemCommitted, result, "", ""); err != nil {
				return nil, err
			}
			report(operations.Progress{Phase: "ready", Completed: 1, Total: 1})
			return result, nil
		}
		var retentionCutoff time.Time
		if retentionHours := a.Config.Maintenance.QuarantineRetentionHours; retentionHours > 0 {
			retentionCutoff = time.Now().Add(-time.Duration(retentionHours) * time.Hour)
		}
		result, err := a.Knowledge.ReconcileStorageSafe(ctx, knowledge.StorageReconcileOptions{
			DryRun:             command.DryRun,
			Quarantine:         !command.PurgeQuarantine,
			PurgeQuarantine:    command.PurgeQuarantine,
			PurgeExpiredBefore: retentionCutoff,
		}, func(phase string, completed, total int) {
			report(operations.Progress{Phase: phase, Completed: completed, Total: total})
		})
		if err != nil {
			return nil, err
		}
		if err := a.markOperationItemResultContext(ctx, op.ID, op.Attempt, itemKey, operations.ItemCommitted, result, "", ""); err != nil {
			return nil, err
		}
		return result, nil
	})
	return service, nil
}

type operationConfigSnapshot struct {
	BaseID    string                `json:"baseId,omitempty"`
	BaseEpoch int64                 `json:"baseEpoch,omitempty"`
	Global    operationGlobalConfig `json:"global"`
	Effective knowledge.BaseConfig  `json:"effective"`
}

type operationGlobalConfig struct {
	EmbeddingProvider  string `json:"embeddingProvider"`
	EmbeddingBaseURL   string `json:"embeddingBaseUrl"`
	EmbeddingModel     string `json:"embeddingModel"`
	RerankModel        string `json:"rerankModel"`
	RerankBaseURL      string `json:"rerankBaseUrl"`
	RerankEnabled      bool   `json:"rerankEnabled"`
	CaptionProvider    string `json:"captionProvider"`
	CaptionModel       string `json:"captionModel"`
	ProcessingProvider string `json:"processingProvider"`
	ProcessingHost     string `json:"processingHost"`
	ChunkSmart         bool   `json:"chunkSmart"`
	ChunkSeparator     string `json:"chunkSeparator"`
	ChunkSize          int    `json:"chunkSize"`
	ChunkOverlap       int    `json:"chunkOverlap"`
	ChunkSemantic      bool   `json:"chunkSemantic"`
	ChunkTokenLimit    int    `json:"chunkTokenLimit"`
	RetrievalTopK      int    `json:"retrievalTopK"`
	RetrievalMode      string `json:"retrievalMode"`
	RetrievalMMR       bool   `json:"retrievalMMR"`
}

type operationModelSnapshot struct {
	EmbeddingProvider  string `json:"embeddingProvider"`
	EmbeddingBaseURL   string `json:"embeddingBaseUrl"`
	EmbeddingModel     string `json:"embeddingModel"`
	RerankEnabled      bool   `json:"rerankEnabled"`
	RerankBaseURL      string `json:"rerankBaseUrl"`
	RerankModel        string `json:"rerankModel"`
	CaptionProvider    string `json:"captionProvider"`
	CaptionModel       string `json:"captionModel"`
	ProcessingProvider string `json:"processingProvider"`
	ProcessingHost     string `json:"processingHost"`
}

// enrichOperationRequest snapshots only replay metadata and non-secret
// semantic configuration. It is called for new commands after idempotency
// lookup; an existing key therefore remains recoverable after target deletion
// or later configuration changes.
func (a *App) enrichOperationRequest(ctx context.Context, req operations.Request) (operations.Request, error) {
	if req.PrincipalRef == "" {
		req.PrincipalRef = "local-process"
	}
	baseID := req.BaseID
	var base knowledge.Base
	if baseID != "" {
		var err error
		base, err = a.Knowledge.GetBaseWithContext(ctx, baseID)
		if err != nil {
			return operations.Request{}, err
		}
		if req.ScopeRef == "" {
			req.ScopeRef = "base:" + base.ID
		}
		if req.ExpectedAncestorEpoch == nil {
			epoch := base.MutationEpoch
			req.ExpectedAncestorEpoch = &epoch
		}
		if req.ExpectedTargetEpoch == nil {
			epoch := base.MutationEpoch
			req.ExpectedTargetEpoch = &epoch
		}
	} else if req.ScopeRef == "" {
		req.ScopeRef = "global"
	}

	effective := knowledge.BaseConfig{}
	if baseID != "" {
		effective = knowledge.ResolveBaseConfig(a.Config, base.Config)
	}
	if req.DocumentID != "" {
		document, _, err := a.Knowledge.GetDocumentWithContext(ctx, req.DocumentID, false)
		if err != nil {
			return operations.Request{}, err
		}
		if baseID != "" && document.BaseID != baseID {
			return operations.Request{}, fmt.Errorf("document %s is outside base %s", req.DocumentID, baseID)
		}
		if req.ExpectedTargetEpoch == nil {
			epoch := document.MutationEpoch
			req.ExpectedTargetEpoch = &epoch
		}
		if req.SourceVersion == "" {
			req.SourceVersion = fmt.Sprintf("source:%d", document.SourceVersion)
		}
	}
	if req.SourceVersion == "" && baseID != "" {
		req.SourceVersion = fmt.Sprintf("base:%d", base.MutationEpoch)
	}
	if req.ConfigSnapshotRef == "" {
		req.ConfigSnapshotRef = operationSnapshotRef("config", operationConfigSnapshot{
			BaseID: baseID, BaseEpoch: base.MutationEpoch,
			Global: operationGlobalConfig{
				EmbeddingProvider: a.Config.Embedding.Provider, EmbeddingBaseURL: a.Config.Embedding.BaseURL,
				EmbeddingModel: a.Config.Embedding.Model, RerankModel: a.Config.Rerank.Model,
				RerankBaseURL: a.Config.Rerank.BaseURL, RerankEnabled: a.Config.Rerank.Enabled,
				CaptionProvider: a.Config.Captioning.Provider, CaptionModel: a.Config.Captioning.Model,
				ProcessingProvider: a.Config.Processing.Provider, ProcessingHost: a.Config.Processing.APIHost,
				ChunkSmart: a.Config.Chunking.Smart, ChunkSeparator: a.Config.Chunking.Separator,
				ChunkSize: a.Config.Chunking.Size, ChunkOverlap: a.Config.Chunking.Overlap,
				ChunkSemantic: a.Config.Chunking.Semantic, ChunkTokenLimit: a.Config.Chunking.TokenLimit,
				RetrievalTopK: a.Config.Retrieval.TopK, RetrievalMode: a.Config.Retrieval.Mode,
				RetrievalMMR: a.Config.Retrieval.MMR,
			},
			Effective: effective.Redacted(),
		})
	}
	if req.ModelSnapshotRef == "" {
		req.ModelSnapshotRef = operationSnapshotRef("model", operationModelSnapshot{
			EmbeddingProvider: effective.EmbeddingProvider,
			EmbeddingBaseURL:  effective.EmbeddingBaseURL,
			EmbeddingModel:    effective.EmbeddingModel,
			RerankEnabled:     effective.RerankEnabled != nil && *effective.RerankEnabled,
			RerankBaseURL:     effective.RerankBaseURL, RerankModel: effective.RerankModel,
			CaptionProvider: a.Config.Captioning.Provider, CaptionModel: a.Config.Captioning.Model,
			ProcessingProvider: effective.Processor, ProcessingHost: effective.MineruAPIHost,
		})
	}
	return req, nil
}

func operationSnapshotRef(prefix string, value any) string {
	encoded, _ := json.Marshal(value)
	sum := sha256.Sum256(encoded)
	return prefix + ":" + hex.EncodeToString(sum[:])
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func directoryProgressReporter(report func(operations.Progress)) func(jobs.ProgressUpdate) {
	if report == nil {
		return func(jobs.ProgressUpdate) {}
	}
	return func(update jobs.ProgressUpdate) {
		report(operations.Progress{
			Phase: update.Phase, Completed: update.Completed, Total: update.Total,
			CompletedBytes: update.CompletedBytes, TotalBytes: update.TotalBytes,
		})
	}
}

func percentProgressReporter(report func(operations.Progress)) func(progress int) {
	return func(progress int) {
		if progress < 0 {
			progress = 0
		}
		if progress > 100 {
			progress = 100
		}
		report(operations.Progress{Phase: "downloading", Completed: progress, Total: 100})
	}
}

func runtimeProgressReporter(report func(operations.Progress)) func(runtime.ModelProgress) {
	return func(update runtime.ModelProgress) {
		progress := int(update.Percent)
		if progress < 0 {
			progress = 0
		}
		if progress > 100 {
			progress = 100
		}
		report(operations.Progress{
			Phase: update.Phase, Completed: progress, Total: 100,
			CompletedBytes: update.CompletedBytes, TotalBytes: update.TotalBytes,
		})
	}
}

func (a *App) runDeleteDocuments(ctx context.Context, baseID string, documentIDs []string, report func(operations.Progress)) (documentOperationResult, error) {
	return a.runDeleteDocumentsWithProbeAndOperation(ctx, "", 0, baseID, documentIDs, report, nil)
}

// batchUnitProbe lets tests synchronize cancellation to a committed batch
// unit; the production executor passes nil.
type batchUnitProbe func(index int, result documentOperationResult)

func (a *App) runDeleteDocumentsWithProbe(ctx context.Context, baseID string, documentIDs []string,
	report func(operations.Progress), probe batchUnitProbe,
) (documentOperationResult, error) {
	return a.runDeleteDocumentsWithProbeAndOperation(ctx, "", 0, baseID, documentIDs, report, probe)
}

func (a *App) runDeleteDocumentsForOperation(ctx context.Context, operationID string, attempt int,
	baseID string, documentIDs []string, report func(operations.Progress),
) (documentOperationResult, error) {
	return a.runDeleteDocumentsWithProbeAndOperation(ctx, operationID, attempt, baseID, documentIDs, report, nil)
}

func (a *App) runDeleteDocumentsWithProbeAndOperation(ctx context.Context, operationID string, attempt int,
	baseID string, documentIDs []string, report func(operations.Progress), probe batchUnitProbe,
) (documentOperationResult, error) {
	result := documentOperationResult{}
	failed := 0
	returnPartial := func(err error) (documentOperationResult, error) {
		result.Partial = result.Succeeded+result.Skipped > 0
		return result, err
	}
	recordItem := func(documentID, state, errorCode, errorMessage string) error {
		return a.markOperationItemContext(ctx, operationID, attempt, documentID, state, errorCode, errorMessage)
	}
	for index, documentID := range documentIDs {
		if err := ctx.Err(); err != nil {
			result.Partial = result.Succeeded+result.Skipped > 0
			return result, err
		}
		resolved, skipped, err := a.operationItemResolved(ctx, operationID, documentID)
		if err != nil {
			return returnPartial(err)
		}
		if resolved {
			if skipped {
				result.Skipped++
			} else {
				result.Succeeded++
			}
			report(operations.Progress{Phase: "deleting", Completed: index + 1, Total: len(documentIDs)})
			continue
		}
		report(operations.Progress{Phase: "deleting", Completed: index, Total: len(documentIDs)})
		document, _, err := a.Knowledge.GetDocumentWithContext(ctx, documentID, false)
		if errors.Is(err, knowledge.ErrNotFound) {
			// A committed delete fence hides the row before physical cleanup
			// finishes. Retry cleanup instead of treating that tombstone as a
			// missing item; this is the recovery path for a crash or raw-store
			// failure after the logical delete committed.
			tombstone, recoveryErr := a.Knowledge.GetDocumentIncludingDeletingWithContext(ctx, documentID)
			if recoveryErr == nil && tombstone.BaseID != baseID {
				failed++
				if markErr := recordItem(documentID, operations.ItemFailed, "base_scope_mismatch", "document is outside the operation base"); markErr != nil {
					return returnPartial(markErr)
				}
				continue
			}
			if recoveryErr != nil && !errors.Is(recoveryErr, knowledge.ErrNotFound) {
				failed++
				if markErr := recordItem(documentID, operations.ItemFailed, "document_read_failed", recoveryErr.Error()); markErr != nil {
					return returnPartial(markErr)
				}
				continue
			}
			itemCtx := a.withOperationItemCommit(ctx, operationID, attempt, documentID)
			if _, cleanupErr := a.Knowledge.DeleteDocumentWithProgress(itemCtx, documentID, nil); cleanupErr == nil {
				result.Succeeded++
				if markErr := recordItem(documentID, operations.ItemCommitted, "", ""); markErr != nil {
					return returnPartial(markErr)
				}
				if probe != nil {
					probe(index, result)
				}
				continue
			} else if errors.Is(cleanupErr, knowledge.ErrNotFound) {
				result.Skipped++
				if markErr := recordItem(documentID, operations.ItemSkipped, "", ""); markErr != nil {
					return returnPartial(markErr)
				}
				continue
			} else {
				failed++
				state, code, message := operations.ItemFailed, "delete_failed", cleanupErr.Error()
				if ctx.Err() != nil {
					state, code, message = operations.ItemCancelled, "cancelled", "operation was cancelled before cleanup completed"
				}
				if markErr := recordItem(documentID, state, code, message); markErr != nil {
					return returnPartial(markErr)
				}
				continue
			}
		}
		if err != nil {
			failed++
			state, code, message := operations.ItemFailed, "document_read_failed", err.Error()
			if ctx.Err() != nil {
				state, code, message = operations.ItemCancelled, "cancelled", "operation was cancelled before item commit"
			}
			if markErr := recordItem(documentID, state, code, message); markErr != nil {
				return returnPartial(markErr)
			}
			continue
		}
		if document.BaseID != baseID {
			failed++
			if markErr := recordItem(documentID, operations.ItemFailed, "base_scope_mismatch", "document is outside the operation base"); markErr != nil {
				return returnPartial(markErr)
			}
			continue
		}
		itemCtx := a.withOperationItemCommit(ctx, operationID, attempt, documentID)
		if _, err := a.Knowledge.DeleteDocumentWithProgress(itemCtx, documentID, nil); err != nil {
			if errors.Is(err, knowledge.ErrNotFound) {
				result.Skipped++
				if markErr := recordItem(documentID, operations.ItemSkipped, "", ""); markErr != nil {
					return returnPartial(markErr)
				}
				continue
			}
			failed++
			state, code, message := operations.ItemFailed, "delete_failed", err.Error()
			if ctx.Err() != nil {
				state, code, message = operations.ItemCancelled, "cancelled", "operation was cancelled before item commit"
			}
			if markErr := recordItem(documentID, state, code, message); markErr != nil {
				return returnPartial(markErr)
			}
			continue
		}
		result.Succeeded++
		if err := recordItem(documentID, operations.ItemCommitted, "", ""); err != nil {
			return returnPartial(err)
		}
		if probe != nil {
			probe(index, result)
		}
	}
	if err := ctx.Err(); err != nil {
		result.Partial = result.Succeeded+result.Skipped > 0
		return result, err
	}
	result.Failed = failed
	result.Partial = failed > 0 && result.Succeeded+result.Skipped > 0
	report(operations.Progress{Phase: "deleted", Completed: len(documentIDs), Total: len(documentIDs)})
	if failed > 0 {
		return result, fmt.Errorf("delete finished with %d failed document(s)", failed)
	}
	return result, nil
}

func (a *App) runReindexDocuments(ctx context.Context, baseID string, documentIDs []string, report func(operations.Progress)) (documentOperationResult, error) {
	return a.runReindexDocumentsForOperation(ctx, "", 0, baseID, documentIDs, report)
}

func (a *App) runReindexDocumentsForOperation(ctx context.Context, operationID string, attempt int,
	baseID string, documentIDs []string, report func(operations.Progress),
) (documentOperationResult, error) {
	result := documentOperationResult{}
	failed := 0
	returnPartial := func(err error) (documentOperationResult, error) {
		result.Partial = result.Succeeded+result.Skipped > 0
		return result, err
	}
	recordItem := func(documentID, state, errorCode, errorMessage string) error {
		return a.markOperationItemContext(ctx, operationID, attempt, documentID, state, errorCode, errorMessage)
	}
	for index, documentID := range documentIDs {
		if err := ctx.Err(); err != nil {
			result.Partial = result.Succeeded+result.Skipped > 0
			return result, err
		}
		resolved, skipped, err := a.operationItemResolved(ctx, operationID, documentID)
		if err != nil {
			return returnPartial(err)
		}
		if resolved {
			if skipped {
				result.Skipped++
			} else {
				result.Succeeded++
			}
			report(operations.Progress{Phase: "reindexing", Completed: index + 1, Total: len(documentIDs)})
			continue
		}
		report(operations.Progress{Phase: "reindexing", Completed: index, Total: len(documentIDs)})
		document, _, err := a.Knowledge.GetDocumentWithContext(ctx, documentID, false)
		if errors.Is(err, knowledge.ErrNotFound) {
			result.Skipped++
			if err := recordItem(documentID, operations.ItemSkipped, "", ""); err != nil {
				return returnPartial(err)
			}
			continue
		}
		if err != nil {
			failed++
			state, code, message := operations.ItemFailed, "document_read_failed", err.Error()
			if ctx.Err() != nil {
				state, code, message = operations.ItemCancelled, "cancelled", "operation was cancelled before item commit"
			}
			if markErr := recordItem(documentID, state, code, message); markErr != nil {
				return returnPartial(markErr)
			}
			continue
		}
		if document.BaseID != baseID {
			failed++
			if markErr := recordItem(documentID, operations.ItemFailed, "base_scope_mismatch", "document is outside the operation base"); markErr != nil {
				return returnPartial(markErr)
			}
			continue
		}
		itemCtx := a.withOperationItemCommit(ctx, operationID, attempt, documentID)
		if _, err := a.Knowledge.ReindexDocument(itemCtx, documentID); err != nil {
			failed++
			state, code, message := operations.ItemFailed, "reindex_failed", err.Error()
			if ctx.Err() != nil {
				state, code, message = operations.ItemCancelled, "cancelled", "operation was cancelled before item commit"
			}
			if markErr := recordItem(documentID, state, code, message); markErr != nil {
				return returnPartial(markErr)
			}
			continue
		}
		result.Succeeded++
		if err := recordItem(documentID, operations.ItemCommitted, "", ""); err != nil {
			return returnPartial(err)
		}
	}
	if err := ctx.Err(); err != nil {
		result.Partial = result.Succeeded+result.Skipped > 0
		return result, err
	}
	result.Failed = failed
	result.Partial = failed > 0 && result.Succeeded+result.Skipped > 0
	report(operations.Progress{Phase: "ready", Completed: len(documentIDs), Total: len(documentIDs)})
	if failed > 0 {
		return result, fmt.Errorf("re-index finished with %d failed document(s)", failed)
	}
	return result, nil
}

func (a *App) runReindexBase(ctx context.Context, baseID string, report func(operations.Progress)) (documentOperationResult, error) {
	return a.runReindexBaseForOperation(ctx, "", 0, baseID, report)
}

func (a *App) runReindexBaseForOperation(ctx context.Context, operationID string, attempt int,
	baseID string, report func(operations.Progress),
) (documentOperationResult, error) {
	result := documentOperationResult{}
	failed := 0
	plannedTotal := 0
	markPartial := func(err error) error {
		result.Partial = result.Succeeded+result.Skipped > 0
		return err
	}
	recordItem := func(documentID, state, errorCode, errorMessage string) error {
		return a.markOperationItemContext(ctx, operationID, attempt, documentID, state, errorCode, errorMessage)
	}
	err := a.Knowledge.ForEachReindexDocumentBatch(ctx, baseID, 50, func(start, total int, documents []knowledge.DocumentSummary) error {
		plannedTotal = total
		for offset, document := range documents {
			index := start + offset
			if err := ctx.Err(); err != nil {
				result.Partial = result.Succeeded+result.Skipped > 0
				return err
			}
			resolved, skipped, err := a.operationItemResolved(ctx, operationID, document.ID)
			if err != nil {
				return markPartial(err)
			}
			if resolved {
				if skipped {
					result.Skipped++
				} else {
					result.Succeeded++
				}
				report(operations.Progress{Phase: "reindexing", Completed: index + 1, Total: total})
				continue
			}
			report(operations.Progress{Phase: "reindexing", Completed: index, Total: total})
			if document.SourceType == "directory" {
				result.Skipped++
				if err := recordItem(document.ID, operations.ItemSkipped, "directory_container", "directory containers are not directly indexed"); err != nil {
					return markPartial(err)
				}
				continue
			}
			current, _, err := a.Knowledge.GetDocumentWithContext(ctx, document.ID, false)
			if errors.Is(err, knowledge.ErrNotFound) {
				result.Skipped++
				if markErr := recordItem(document.ID, operations.ItemSkipped, "", ""); markErr != nil {
					return markPartial(markErr)
				}
				continue
			}
			if err != nil || current.BaseID != baseID {
				failed++
				code, message := "document_read_failed", "unable to read document"
				state := operations.ItemFailed
				if err == nil {
					code, message = "base_scope_mismatch", "document is outside the operation base"
				} else {
					message = err.Error()
				}
				if ctx.Err() != nil {
					state, code, message = operations.ItemCancelled, "cancelled", "operation was cancelled before item commit"
				}
				if markErr := recordItem(document.ID, state, code, message); markErr != nil {
					return markPartial(markErr)
				}
				continue
			}
			itemCtx := a.withOperationItemCommit(ctx, operationID, attempt, document.ID)
			if _, err := a.Knowledge.ReindexDocument(itemCtx, document.ID); err != nil {
				failed++
				state, code, message := operations.ItemFailed, "reindex_failed", err.Error()
				if ctx.Err() != nil {
					state, code, message = operations.ItemCancelled, "cancelled", "operation was cancelled before item commit"
				}
				if markErr := recordItem(document.ID, state, code, message); markErr != nil {
					return markPartial(markErr)
				}
				continue
			}
			result.Succeeded++
			if err := recordItem(document.ID, operations.ItemCommitted, "", ""); err != nil {
				return markPartial(err)
			}
		}
		return nil
	})
	if err != nil {
		if ctx.Err() != nil {
			result.Partial = result.Succeeded+result.Skipped > 0
		}
		return result, err
	}
	if err := ctx.Err(); err != nil {
		result.Partial = result.Succeeded+result.Skipped > 0
		return result, err
	}
	result.Failed = failed
	result.Partial = failed > 0 && result.Succeeded+result.Skipped > 0
	report(operations.Progress{Phase: "ready", Completed: plannedTotal, Total: plannedTotal})
	if failed > 0 {
		return result, fmt.Errorf("re-index finished with %d failed document(s)", failed)
	}
	return result, nil
}
