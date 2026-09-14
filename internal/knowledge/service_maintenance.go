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
	docs, err := s.store.listStorageRefs()
	if err != nil {
		return StorageReconcileResult{}, err
	}
	referenced := make(map[string]bool, len(docs))
	for _, doc := range docs {
		if doc.RawFilePath != "" {
			referenced[doc.RawFilePath] = true
		}
	}
	generationPaths, err := s.store.listGenerationRawPaths()
	if err != nil {
		return StorageReconcileResult{}, err
	}
	for _, rel := range generationPaths {
		referenced[rel] = true
	}
	raws, err := s.raw.ListAll()
	if err != nil {
		return StorageReconcileResult{}, err
	}
	result := StorageReconcileResult{DryRun: options.DryRun}
	if report != nil {
		report("scanning", 0, len(raws)+len(docs))
	}
	for index, rel := range raws {
		if referenced[rel] {
			continue
		}
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if report != nil {
			report("scanning", index, len(raws)+len(docs))
		}
		size, err := s.raw.Size(rel)
		if err != nil {
			return result, err
		}
		result.Orphans++
		result.OrphanBytes += size
		switch {
		case options.DryRun:
		case options.Quarantine:
			quarantinePath, quarantinedBytes, err := s.raw.Quarantine(rel)
			if err != nil {
				return result, err
			}
			result.Quarantined++
			result.QuarantineBytes += quarantinedBytes
			_ = quarantinePath
		default:
			if err := s.raw.Delete(rel); err != nil {
				return result, err
			}
			result.DeletedRaw++
		}
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

	for index, doc := range docs {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if report != nil {
			report("counting", len(raws)+index, len(raws)+len(docs))
		}
		chunkCount, err := s.store.countChunksByDoc(doc.ID)
		if err != nil {
			return result, err
		}
		if doc.ChunkCount == chunkCount {
			continue
		}
		if err := s.store.updateChunkCount(doc.ID, chunkCount); err != nil {
			return result, err
		}
		result.FixedCounts++
	}
	if report != nil {
		report("ready", len(raws)+len(docs), len(raws)+len(docs))
	}
	return result, nil
}
