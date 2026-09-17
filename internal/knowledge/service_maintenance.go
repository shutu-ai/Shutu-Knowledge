package knowledge

import (
	"context"
	"fmt"
	"time"
)

// StorageReconcileOptions controls how unreferenced source bytes are handled.
// Explicit maintenance defaults to quarantine; direct startup reconciliation
// retains its historical immediate-delete behavior.
type StorageReconcileOptions struct {
	DryRun             bool      `json:"dryRun"`
	Quarantine         bool      `json:"quarantine"`
	PurgeQuarantine    bool      `json:"purgeQuarantine"`
	PurgeExpiredBefore time.Time `json:"-"`
}

// StorageReconcileResult is a bounded, text-free maintenance contract.
type StorageReconcileResult struct {
	DryRun           bool  `json:"dryRun"`
	Orphans          int   `json:"orphans"`
	OrphanBytes      int64 `json:"orphanBytes"`
	DeletedRaw       int   `json:"deletedRaw"`
	Quarantined      int   `json:"quarantined"`
	QuarantineBytes  int64 `json:"quarantineBytes"`
	ExpiredPurged    int   `json:"expiredPurged"`
	ExpiredBytes     int64 `json:"expiredBytes"`
	FixedCounts      int   `json:"fixedCounts"`
	QuarantinePurged bool  `json:"quarantinePurged"`
}

type reconcileProgress func(phase string, completed, total int)

// ReconcileStorage repairs two startup-time drift cases: raw files no longer
// referenced by a document, and document chunk-count metadata that diverged
// from the actual chunk table.
func (s *Service) ReconcileStorage() (removedRaw int, fixedCounts int, err error) {
	result, err := s.reconcileStorage(context.Background(), StorageReconcileOptions{}, nil)
	return result.DeletedRaw, result.FixedCounts, err
}

// ReconcileStorageSafe performs an explicit maintenance pass. Unreferenced
// raw files are moved to the reserved quarantine area, and removal of that
// area is an explicit second decision. No source text enters the result.
func (s *Service) ReconcileStorageSafe(ctx context.Context, options StorageReconcileOptions, report reconcileProgress) (StorageReconcileResult, error) {
	if options.Quarantine && options.PurgeQuarantine {
		return StorageReconcileResult{}, fmt.Errorf("quarantine and purgeQuarantine cannot run in the same pass")
	}
	if !options.DryRun && !options.Quarantine && !options.PurgeQuarantine {
		options.Quarantine = true
	}
	return s.reconcileStorage(ctx, options, report)
}

func (s *Service) reconcileStorage(ctx context.Context, options StorageReconcileOptions, report reconcileProgress) (StorageReconcileResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	docCount, err := s.store.countStorageRefs(ctx)
	if err != nil {
		return StorageReconcileResult{}, err
	}
	referenced := make(map[string]struct{}, docCount)
	if err := s.store.visitStorageRefs(ctx, func(doc storageDocumentRef) error {
		if doc.RawFilePath != "" {
			referenced[doc.RawFilePath] = struct{}{}
		}
		return nil
	}); err != nil {
		return StorageReconcileResult{}, err
	}
	if err := s.store.visitGenerationRawPaths(ctx, func(rel string) error {
		referenced[rel] = struct{}{}
		return nil
	}); err != nil {
		return StorageReconcileResult{}, err
	}
	rawCount, err := s.raw.CountAll(ctx)
	if err != nil {
		return StorageReconcileResult{}, err
	}
	total := rawCount + docCount
	result := StorageReconcileResult{DryRun: options.DryRun}
	if report != nil {
		report("scanning", 0, total)
	}
	rawIndex := 0
	if err := s.raw.WalkAll(ctx, func(rel string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if report != nil {
			report("scanning", rawIndex, total)
		}
		rawIndex++
		if _, ok := referenced[rel]; ok {
			return nil
		}
		size, err := s.raw.Size(rel)
		if err != nil {
			return err
		}
		result.Orphans++
		result.OrphanBytes += size
		switch {
		case options.DryRun:
		case options.Quarantine:
			quarantinePath, quarantinedBytes, err := s.raw.Quarantine(rel)
			if err != nil {
				return err
			}
			result.Quarantined++
			result.QuarantineBytes += quarantinedBytes
			_ = quarantinePath
		default:
			if err := s.raw.Delete(rel); err != nil {
				return err
			}
			result.DeletedRaw++
		}
		return nil
	}); err != nil {
		return result, err
	}

	if options.PurgeQuarantine && !options.DryRun {
		retainedCount, retainedBytes, err := s.raw.QuarantineStats()
		if err != nil {
			return result, err
		}
		result.Quarantined += retainedCount
		result.QuarantineBytes += retainedBytes
		if err := s.raw.PurgeQuarantine(); err != nil {
			return result, err
		}
		result.QuarantinePurged = true
	}
	if !options.DryRun && !options.PurgeQuarantine && !options.PurgeExpiredBefore.IsZero() {
		expiredCount, expiredBytes, err := s.raw.PurgeExpiredQuarantine(options.PurgeExpiredBefore)
		if err != nil {
			return result, err
		}
		result.ExpiredPurged = expiredCount
		result.ExpiredBytes = expiredBytes
	}

	docIndex := 0
	if err := s.store.visitStorageRefs(ctx, func(doc storageDocumentRef) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if report != nil {
			report("counting", rawCount+docIndex, total)
		}
		docIndex++
		chunkCount, err := s.store.countChunksByDocContext(ctx, doc.ID)
		if err != nil {
			return err
		}
		if doc.ChunkCount == chunkCount {
			return nil
		}
		if err := s.store.updateChunkCountWithContext(ctx, doc.ID, chunkCount); err != nil {
			return err
		}
		result.FixedCounts++
		return nil
	}); err != nil {
		return result, err
	}
	if report != nil {
		report("ready", total, total)
	}
	return result, nil
}
