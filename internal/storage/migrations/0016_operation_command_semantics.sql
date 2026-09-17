-- Persist the immutable command envelope needed to replay an operation after
-- restart. References and snapshots are opaque, bounded metadata; secrets and
-- process-local paths must never be copied into these columns.
ALTER TABLE operations ADD COLUMN principal_ref TEXT;
ALTER TABLE operations ADD COLUMN scope_ref TEXT;
ALTER TABLE operations ADD COLUMN input_ref TEXT;
ALTER TABLE operations ADD COLUMN input_sha256 TEXT;
ALTER TABLE operations ADD COLUMN source_version TEXT;
ALTER TABLE operations ADD COLUMN config_snapshot_ref TEXT;
ALTER TABLE operations ADD COLUMN model_snapshot_ref TEXT;
ALTER TABLE operations ADD COLUMN expected_target_epoch INTEGER;
ALTER TABLE operations ADD COLUMN expected_ancestor_epoch INTEGER;
ALTER TABLE operations ADD COLUMN allocated_document_id TEXT;
ALTER TABLE operations ADD COLUMN allocated_generation INTEGER;
ALTER TABLE operations ADD COLUMN next_attempt_at INTEGER;
ALTER TABLE operations ADD COLUMN recovery_basis TEXT;
ALTER TABLE operations ADD COLUMN result_ref TEXT;
ALTER TABLE operations ADD COLUMN result_expires_at INTEGER;
ALTER TABLE operations ADD COLUMN retained_until INTEGER;

CREATE INDEX operations_retry_schedule_idx
    ON operations(state, next_attempt_at, priority, requested_at, id);

CREATE TABLE operation_items (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    operation_id     TEXT NOT NULL REFERENCES operations(id) ON DELETE CASCADE,
    item_key         TEXT NOT NULL,
    attempt          INTEGER NOT NULL DEFAULT 0,
    state            TEXT NOT NULL CHECK (state IN ('pending','committed','skipped','failed','cancelled')),
    commit_marker    INTEGER NOT NULL DEFAULT 0,
    result           TEXT,
    error_code       TEXT,
    error_message    TEXT,
    committed_at     INTEGER,
    updated_at       INTEGER NOT NULL,
    UNIQUE(operation_id, item_key)
);

CREATE INDEX operation_items_operation_idx
    ON operation_items(operation_id, id);

UPDATE storage_format
   SET min_reader_version = 8,
       min_writer_version = 8
 WHERE id = 1;
