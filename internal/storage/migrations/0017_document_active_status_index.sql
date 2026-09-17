-- Keep the bounded indexing-status control path indexable on large stores.
CREATE INDEX documents_active_status_idx
    ON documents(lifecycle_state, status, updated_at, id);
