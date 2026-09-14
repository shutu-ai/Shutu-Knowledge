-- A URL capture fixes the bytes fetched for one operation attempt chain.
-- Retries replay this source instead of fetching a possibly changed page.
CREATE TABLE url_captures (
    id            TEXT PRIMARY KEY,
    operation_id  TEXT NOT NULL UNIQUE REFERENCES operations(id) ON DELETE CASCADE,
    raw_url       TEXT NOT NULL,
    final_url     TEXT NOT NULL,
    content_type  TEXT NOT NULL DEFAULT '',
    sha256        TEXT NOT NULL,
    size_bytes    INTEGER NOT NULL,
    state         TEXT NOT NULL DEFAULT 'ready'
                  CHECK (state IN ('ready','consumed','failed')),
    body          BLOB,
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL,
    expires_at    INTEGER NOT NULL
);

CREATE INDEX url_captures_state_idx ON url_captures(state, expires_at);
