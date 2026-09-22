# Shutu Knowledge v0.6.4 Release Notes

v0.6.4 is a **maintenance release for generation GC and cross-process
historical-reader correctness/release validation**. It contains no new
semantic, data-model, retrieval, or UI capabilities.

## Generation GC correctness

- Retire a durable generation mapping before deleting its expired chunks.
- The mapping now acts as a fail-closed delete fence: readers that start after
  retirement receive `historical_evidence_expired`; readers whose SQLite WAL
  snapshot began earlier retain their complete pinned generation view.
- A deterministic regression rejects chunk-first deletion and verifies both
  the physical cleanup and fail-closed historical API.

## Release validation synchronization

- Publish the cross-process reader's test-only ready marker through a synced
  temporary file and atomic same-directory rename.
- The parent no longer mistakes marker-path creation for a completed marker
  write. The failed v0.6.3 Tag CI read a zero-byte marker before GC began.
- Failure diagnostics now expose observed marker bytes explicitly.

## Scope

This release preserves the v0.6.3 local embedding/reranker behavior and all
existing storage contracts. Excel semantic/data-model reasoning remains under
separate future validation and is not claimed here.
