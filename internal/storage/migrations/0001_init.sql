-- Initial Knowledge schema: business state, chunk index, FTS, and jobs.
-- All long-term schema changes go through versioned migrations; business
-- code must never CREATE TABLE outside migrations.

CREATE TABLE kv (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

CREATE TABLE bases (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    grp         TEXT NOT NULL DEFAULT '',
    config      TEXT NOT NULL DEFAULT '{}',
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
);

CREATE TABLE documents (
    id                 TEXT PRIMARY KEY,
    base_id            TEXT NOT NULL,
    title              TEXT NOT NULL,
    source_type        TEXT NOT NULL CHECK (source_type IN ('text','file','url','directory')),
    file_name          TEXT,
    mime_type          TEXT,
    url                TEXT,
    parent_directory_id TEXT,
    source_path        TEXT,
    content_hash       TEXT,
    raw_file_path      TEXT,
    raw_text           TEXT,
    char_count         INTEGER NOT NULL DEFAULT 0,
    token_count        INTEGER,
    chunk_count        INTEGER NOT NULL DEFAULT 0,
    status             TEXT NOT NULL DEFAULT 'pending'
                       CHECK (status IN ('pending','processing','ready','failed','stale')),
    phase              TEXT CHECK (phase IN ('parsing','embedding')),
    progress           INTEGER NOT NULL DEFAULT 0,
    incomplete         INTEGER NOT NULL DEFAULT 0,
    error_code         TEXT,
    error_message      TEXT,
    created_at         INTEGER NOT NULL,
    updated_at         INTEGER
);

CREATE INDEX documents_base_idx ON documents(base_id);
CREATE INDEX documents_hash_idx ON documents(content_hash);

CREATE TABLE chunks (
    id                  TEXT PRIMARY KEY,
    doc_id              TEXT NOT NULL,
    base_id             TEXT NOT NULL,
    idx                 INTEGER NOT NULL,
    text                TEXT NOT NULL,
    heading             TEXT,
    context             TEXT NOT NULL DEFAULT '',
    embedding           BLOB,
    embedding_model     TEXT,
    embedding_text_hash TEXT,
    created_at          INTEGER NOT NULL
);

CREATE INDEX chunks_doc_idx ON chunks(doc_id);
CREATE INDEX chunks_base_idx ON chunks(base_id);
CREATE INDEX chunks_hash_idx ON chunks(embedding_text_hash, embedding_model);

CREATE VIRTUAL TABLE chunk_fts USING fts5(
    fts_rowid UNINDEXED,
    search_text,
    tokenize = 'trigram'
);

CREATE TRIGGER chunks_ai AFTER INSERT ON chunks BEGIN
    INSERT INTO chunk_fts(fts_rowid, search_text)
    VALUES (new.rowid, COALESCE(new.context, '') || ' ' || new.text);
END;

CREATE TRIGGER chunks_ad AFTER DELETE ON chunks BEGIN
    INSERT INTO chunk_fts(chunk_fts, fts_rowid, search_text)
    VALUES ('delete', old.rowid, COALESCE(old.context, '') || ' ' || old.text);
END;

CREATE TRIGGER chunks_au AFTER UPDATE ON chunks BEGIN
    INSERT INTO chunk_fts(chunk_fts, fts_rowid, search_text)
    VALUES ('delete', old.rowid, COALESCE(old.context, '') || ' ' || old.text);
    INSERT INTO chunk_fts(fts_rowid, search_text)
    VALUES (new.rowid, COALESCE(new.context, '') || ' ' || new.text);
END;

CREATE TABLE jobs (
    id         TEXT PRIMARY KEY,
    kind       TEXT NOT NULL,
    base_id    TEXT,
    payload    TEXT NOT NULL DEFAULT '{}',
    status     TEXT NOT NULL DEFAULT 'pending'
               CHECK (status IN ('pending','running','done','failed','cancelled')),
    progress   INTEGER NOT NULL DEFAULT 0,
    total      INTEGER NOT NULL DEFAULT 0,
    error      TEXT,
    created_at INTEGER NOT NULL,
    updated_at INTEGER
);

CREATE INDEX jobs_status_idx ON jobs(status);
