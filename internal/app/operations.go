package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

type modelCacheMigrationCommand struct {
	TargetDir    string `json:"targetDir"`
	RemoveSource bool   `json:"removeSource"`
}

type ollamaPullCommand struct {
	Model string `json:"model"`
}

type storageMaintenanceCommand struct {
	DryRun          bool `json:"dryRun"`
	PurgeQuarantine bool `json:"purgeQuarantine"`
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
		IO:          a.Config.Scheduler.IO,
		DBWrite:     a.Config.Scheduler.DBWrite,
		Disk:        a.Config.Scheduler.Disk,
		Network:     a.Config.Scheduler.Network,
		Model:       a.Config.Scheduler.Model,
		Maintenance: a.Config.Scheduler.Maintenance,
		MaxPerBase:  a.Config.Scheduler.MaxPerBase,
	})
	service.SetQueueLimit(a.Config.Scheduler.QueueLimit)
	service.SetIdempotencyRetentionHours(a.Config.Scheduler.IdempotencyRetentionHours)
	service.SetModelAdmission(a.modelAdmission)
	service.Register("import_text", func(ctx context.Context, op operations.Operation, payload json.RawMessage, report func(operations.Progress)) (any, error) {
		var command importTextCommand
		if err := json.Unmarshal(payload, &command); err != nil {
			return nil, fmt.Errorf("decode import_text: %w", err)
		}
		if op.DocumentID == "" {
			return nil, fmt.Errorf("import_text requires a preallocated documentId")
		}
		report(operations.Progress{Phase: "importing", Total: 1})
		document, err := a.Knowledge.AddTextDocumentWithID(ctx, op.BaseID, op.DocumentID, command.Title, command.Content)
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
		report(operations.Progress{Phase: "deleting", Total: 1})
		var onDeleted func()
		if a.deleteDocumentBoundary != nil {
			onDeleted = a.deleteDocumentBoundary
		}
		removed, err := a.Knowledge.DeleteDocumentWithProgress(ctx, command.DocumentID, onDeleted)
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
		report(operations.Progress{Phase: "importing", Completed: 0, Total: len(command.Files)})
		result, err := a.Knowledge.AddFiles(ctx, op.BaseID, command.Files, command.Conflict, command.ParentDirectoryID)
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
		return a.runDeleteDocuments(ctx, op.BaseID, command.DocumentIDs, report)
	})
	service.Register("delete_base", func(ctx context.Context, op operations.Operation, _ json.RawMessage, report func(operations.Progress)) (any, error) {
		if op.BaseID == "" {
			return nil, fmt.Errorf("delete_base requires baseId")
		}
		report(operations.Progress{Phase: "deleting", Total: 1})
		if err := a.Knowledge.DeleteBase(op.BaseID); err != nil {
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
		session, path, err := a.Operations.UploadForOperation(op.ID, command.UploadID)
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
		base, err := a.Knowledge.GetBase(op.BaseID)
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
			if conflicts := a.Knowledge.DetectConflicts(op.BaseID, []string{title}); len(conflicts) > 0 {
				return nil, &knowledge.ConflictError{Conflicts: conflicts}
			}
		} else if conflict == "replace" {
			if existing, err := a.Knowledge.FindDocumentByTitle(op.BaseID, title); err == nil {
				if err := a.Knowledge.DeleteDocument(existing.ID); err != nil {
					return nil, err
				}
			}
		} else if conflict == "rename" {
			if _, err := a.Knowledge.FindDocumentByTitle(op.BaseID, title); err == nil {
				title = a.Knowledge.RenameAvailable(op.BaseID, title)
			}
		}
		report(operations.Progress{Phase: "importing", Total: 1})
		document, err := a.Knowledge.AddFileDocumentWithID(ctx, op.BaseID, op.DocumentID, title, data, command.ParentDirectoryID)
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
		report(operations.Progress{Phase: "reindexing", Total: 1})
		document, err := a.Knowledge.ReindexDocument(ctx, command.DocumentID)
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
		return a.runReindexDocuments(ctx, op.BaseID, command.DocumentIDs, report)
	})
	service.Register("reindex_base", func(ctx context.Context, op operations.Operation, _ json.RawMessage, report func(operations.Progress)) (any, error) {
		if op.BaseID == "" {
			return nil, fmt.Errorf("reindex_base requires baseId")
		}
		documents, err := a.Knowledge.ListDocuments(op.BaseID)
		if err != nil {
			return nil, err
		}
		ids := make([]string, 0, len(documents))
		for _, document := range documents {
			ids = append(ids, document.ID)
		}
		return a.runReindexDocuments(ctx, op.BaseID, ids, report)
	})
	service.Register("import_url", func(ctx context.Context, op operations.Operation, payload json.RawMessage, report func(operations.Progress)) (any, error) {
		var command importURLCommand
		if err := json.Unmarshal(payload, &command); err != nil {
			return nil, fmt.Errorf("decode import_url: %w", err)
		}
		if op.DocumentID == "" {
			return nil, fmt.Errorf("import_url requires a preallocated documentId")
		}
		// A ready document means the business effect committed before the
		// previous worker wrote the terminal state.
		if existing, _, err := a.Knowledge.GetDocument(op.DocumentID, false); err == nil && existing.Status == knowledge.StatusReady {
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
		document, err := a.Knowledge.AddUrlDocumentWithID(ctx, op.BaseID, op.DocumentID, capture.FinalURL, command.Title, capture.Body)
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
		existing, _, err := a.Knowledge.GetDocument(command.DocumentID, false)
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
		changed, document, err := a.Knowledge.RefreshUrlDocumentFromCapture(ctx, command.DocumentID, capture.FinalURL, capture.Body)
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
		document, err := a.Knowledge.RunDirectoryImport(ctx, op.BaseID, command.Path, directoryProgressReporter(report))
		if err != nil {
			return nil, err
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
		document, err := a.Knowledge.RunDirectoryRescan(ctx, command.DocumentID, directoryProgressReporter(report))
		if err != nil {
			return nil, err
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
		report(operations.Progress{Phase: "deleting", Completed: 0, Total: total})
		removed, err := a.Knowledge.DeleteDirectoryRecursiveWithProgress(ctx, command.DocumentID, func() {
			completed++
			report(operations.Progress{Phase: "deleting", Completed: completed, Total: total})
		})
		if err != nil {
			return nil, err
		}
		return documentOperationResult{Succeeded: removed}, nil
	})
	service.Register("download_ocr_model", func(ctx context.Context, _ operations.Operation, _ json.RawMessage, report func(operations.Progress)) (any, error) {
		report(operations.Progress{Phase: "downloading", Completed: 0, Total: 100})
		if err := a.Models.DownloadOCR(ctx, percentProgressReporter(report)); err != nil {
			return nil, err
		}
		report(operations.Progress{Phase: "ready", Completed: 100, Total: 100})
		return map[string]any{"modelId": models.OCRModelID, "kind": "ocr"}, nil
	})
	service.Register("download_model", func(ctx context.Context, _ operations.Operation, payload json.RawMessage, report func(operations.Progress)) (any, error) {
		var command modelDownloadCommand
		if err := json.Unmarshal(payload, &command); err != nil {
			return nil, fmt.Errorf("decode download_model: %w", err)
		}
		if command.ID == "" || (command.Kind != models.KindEmbedding && command.Kind != models.KindRerank) {
			return nil, fmt.Errorf("download_model requires a model id and embedding/rerank kind")
		}
		if command.Managed {
			managed, ok := a.Runtime.(runtime.ManagedModelController)
			if !ok {
				return nil, errors.New("managed runtime model control is unavailable")
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
		report(operations.Progress{Phase: "ready", Completed: 100, Total: 100})
		return map[string]any{"modelId": command.ID, "kind": command.Kind, "managed": command.Managed}, nil
	})
	service.Register("self_test_reranker", func(ctx context.Context, _ operations.Operation, payload json.RawMessage, report func(operations.Progress)) (any, error) {
		var command modelIDCommand
		if err := json.Unmarshal(payload, &command); err != nil {
			return nil, fmt.Errorf("decode self_test_reranker: %w", err)
		}
		if command.ID == "" {
			return nil, fmt.Errorf("self_test_reranker requires a model id")
		}
		return a.SelfTestRerankerWithProgress(ctx, command.ID, runtimeProgressReporter(report))
	})
	service.Register("migrate_model_cache", func(ctx context.Context, _ operations.Operation, payload json.RawMessage, report func(operations.Progress)) (any, error) {
		var command modelCacheMigrationCommand
		if err := json.Unmarshal(payload, &command); err != nil {
			return nil, fmt.Errorf("decode migrate_model_cache: %w", err)
		}
		if command.TargetDir == "" {
			return nil, fmt.Errorf("migrate_model_cache requires targetDir")
		}
		report(operations.Progress{Phase: "migrating", Total: 1})
		result, err := a.MigrateModelCache(command.TargetDir, command.RemoveSource)
		if err != nil {
			return nil, err
		}
		report(operations.Progress{Phase: "ready", Completed: 1, Total: 1})
		return result, nil
	})
	service.Register("ollama_pull", func(ctx context.Context, _ operations.Operation, payload json.RawMessage, report func(operations.Progress)) (any, error) {
		var command ollamaPullCommand
		if err := json.Unmarshal(payload, &command); err != nil {
			return nil, fmt.Errorf("decode ollama_pull: %w", err)
		}
		if command.Model == "" {
			return nil, fmt.Errorf("ollama_pull requires model")
		}
		report(operations.Progress{Phase: "downloading", Completed: 0, Total: 100})
		if err := a.Ollama.Pull(ctx, command.Model, percentProgressReporter(report)); err != nil {
			return nil, err
		}
		report(operations.Progress{Phase: "ready", Completed: 100, Total: 100})
		return map[string]any{"model": command.Model}, nil
	})
	service.Register("maintenance_storage", func(ctx context.Context, _ operations.Operation, payload json.RawMessage, report func(operations.Progress)) (any, error) {
		var command storageMaintenanceCommand
		if err := json.Unmarshal(payload, &command); err != nil {
			return nil, fmt.Errorf("decode maintenance_storage: %w", err)
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
		return result, nil
	})
	return service, nil
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
	return a.runDeleteDocumentsWithProbe(ctx, baseID, documentIDs, report, nil)
}

// batchUnitProbe lets tests synchronize cancellation to a committed batch
// unit; the production executor passes nil.
type batchUnitProbe func(index int, result documentOperationResult)

func (a *App) runDeleteDocumentsWithProbe(ctx context.Context, baseID string, documentIDs []string,
	report func(operations.Progress), probe batchUnitProbe,
) (documentOperationResult, error) {
	result := documentOperationResult{}
	failed := 0
	for index, documentID := range documentIDs {
		if err := ctx.Err(); err != nil {
			result.Partial = result.Succeeded+result.Skipped > 0
			return result, err
		}
		report(operations.Progress{Phase: "deleting", Completed: index, Total: len(documentIDs)})
		document, _, err := a.Knowledge.GetDocument(documentID, false)
		if errors.Is(err, knowledge.ErrNotFound) {
			result.Skipped++
			continue
		}
		if err != nil {
			failed++
			continue
		}
		if document.BaseID != baseID {
			failed++
			continue
		}
		if err := a.Knowledge.DeleteDocument(documentID); err != nil {
			if errors.Is(err, knowledge.ErrNotFound) {
				result.Skipped++
				continue
			}
			failed++
			continue
		}
		result.Succeeded++
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
	result := documentOperationResult{}
	failed := 0
	for index, documentID := range documentIDs {
		if err := ctx.Err(); err != nil {
			result.Partial = result.Succeeded+result.Skipped > 0
			return result, err
		}
		report(operations.Progress{Phase: "reindexing", Completed: index, Total: len(documentIDs)})
		document, _, err := a.Knowledge.GetDocument(documentID, false)
		if errors.Is(err, knowledge.ErrNotFound) {
			result.Skipped++
			continue
		}
		if err != nil {
			failed++
			continue
		}
		if document.BaseID != baseID {
			failed++
			continue
		}
		if _, err := a.Knowledge.ReindexDocument(ctx, documentID); err != nil {
			failed++
			continue
		}
		result.Succeeded++
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
