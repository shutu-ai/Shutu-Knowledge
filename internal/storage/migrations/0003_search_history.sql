-- Recall-test history is Knowledge-owned replay metadata, not Agent session
-- state. Web recall saves it explicitly; automatic retrieval does not.
CREATE TABLE search_history (
    id         TEXT PRIMARY KEY,
    base_id    TEXT,
    query      TEXT NOT NULL,
    mode       TEXT NOT NULL,
    top_k      INTEGER NOT NULL,
    mmr        INTEGER NOT NULL CHECK (mmr IN (0, 1)),
    total_hits INTEGER NOT NULL DEFAULT 0,
    elapsed_ms INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL
);

CREATE INDEX search_history_created_idx ON search_history(created_at DESC, id DESC);
