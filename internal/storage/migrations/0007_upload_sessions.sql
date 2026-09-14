-- Upload sessions own immutable staging bytes between transport and operation
-- binding. operation_id is the input-ownership reference.
CREATE TABLE upload_sessions (
    id                  TEXT PRIMARY KEY,
    base_id             TEXT NOT NULL,
    parent_directory_id TEXT,
    file_name           TEXT NOT NULL,
    state               TEXT NOT NULL DEFAULT 'uploading'
                        CHECK (state IN ('uploading','complete','bound','released','expired')),
    expected_size       INTEGER,
    expected_sha256     TEXT,
    size_bytes          INTEGER NOT NULL DEFAULT 0,
    sha256              TEXT,
    staging_path        TEXT NOT NULL,
    operation_id        TEXT UNIQUE REFERENCES operations(id) ON DELETE SET NULL,
    created_at          INTEGER NOT NULL,
    updated_at          INTEGER NOT NULL,
    expires_at          INTEGER NOT NULL
);

CREATE INDEX upload_sessions_state_idx ON upload_sessions(state, expires_at);
CREATE INDEX upload_sessions_base_idx ON upload_sessions(base_id, created_at);
