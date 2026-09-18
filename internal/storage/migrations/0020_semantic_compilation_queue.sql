-- Durable dirty set for incremental semantic compilation. A row is retained
-- after resolution so restarts can audit the last generation that consumed the
-- change; unresolved rows are the pending work.
CREATE TABLE knowledge_compilation_queue (
    base_id             TEXT NOT NULL,
    doc_id              TEXT NOT NULL,
    change_type         TEXT NOT NULL
                        CHECK (change_type IN ('updated','deleted')),
    index_generation    INTEGER NOT NULL DEFAULT 0,
    source_version      INTEGER NOT NULL DEFAULT 0,
    content_hash        TEXT NOT NULL DEFAULT '',
    created_at          INTEGER NOT NULL,
    updated_at          INTEGER NOT NULL,
    resolved_generation INTEGER,
    PRIMARY KEY (base_id, doc_id)
);

CREATE INDEX knowledge_compilation_queue_pending_idx
    ON knowledge_compilation_queue(base_id, resolved_generation, updated_at, doc_id);
CREATE INDEX knowledge_compilation_queue_resolution_idx
    ON knowledge_compilation_queue(base_id, resolved_generation, doc_id);
