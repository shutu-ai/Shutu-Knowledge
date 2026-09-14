-- Durable command log for asynchronous operations. Payloads are replayable
-- inputs, never process-local closures.
CREATE TABLE operations (
    id                      TEXT PRIMARY KEY,
    type                    TEXT NOT NULL,
    command_schema_version  INTEGER NOT NULL,
    command_payload         TEXT NOT NULL,
    request_fingerprint     TEXT NOT NULL,
    idempotency_key         TEXT,
    base_id                 TEXT,
    document_id             TEXT,
    parent_operation_id     TEXT,
    state                   TEXT NOT NULL DEFAULT 'queued'
                            CHECK (state IN ('queued','running','succeeded','failed','cancelled','cancelling','interrupted')),
    state_revision          INTEGER NOT NULL DEFAULT 1,
    phase                   TEXT,
    resource_class          TEXT NOT NULL DEFAULT 'io',
    priority                INTEGER NOT NULL DEFAULT 0,
    attempt                 INTEGER NOT NULL DEFAULT 0,
    instance_id             TEXT,
    cancel_requested        INTEGER NOT NULL DEFAULT 0,
    retryable               INTEGER NOT NULL DEFAULT 1,
    completed_units         INTEGER NOT NULL DEFAULT 0,
    total_units             INTEGER,
    completed_bytes         INTEGER NOT NULL DEFAULT 0,
    total_bytes             INTEGER,
    error_code              TEXT,
    error_message           TEXT,
    result                  TEXT,
    requested_at            INTEGER NOT NULL,
    started_at              INTEGER,
    finished_at             INTEGER,
    updated_at              INTEGER NOT NULL
);

CREATE UNIQUE INDEX operations_idempotency_idx
    ON operations(idempotency_key)
    WHERE idempotency_key IS NOT NULL;
CREATE INDEX operations_dispatch_idx ON operations(state, priority, requested_at, id);
CREATE INDEX operations_scope_idx ON operations(base_id, document_id, requested_at);

CREATE TABLE operation_events (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    operation_id  TEXT NOT NULL REFERENCES operations(id) ON DELETE CASCADE,
    revision      INTEGER NOT NULL,
    kind          TEXT NOT NULL,
    payload       TEXT NOT NULL DEFAULT '{}',
    created_at    INTEGER NOT NULL
);

CREATE INDEX operation_events_operation_idx ON operation_events(operation_id, id);
