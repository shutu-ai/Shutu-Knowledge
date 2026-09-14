-- Durable identity for each published index generation. Historical evidence
-- requests must pair generation and source version; rows are retained for a
-- bounded grace period before retired chunks and mappings are reclaimed.
CREATE TABLE document_generations (
    doc_id           TEXT NOT NULL,
    index_generation INTEGER NOT NULL,
    source_version   INTEGER NOT NULL,
    chunk_count      INTEGER NOT NULL,
    created_at       INTEGER NOT NULL,
    PRIMARY KEY (doc_id, index_generation)
);

CREATE INDEX document_generations_retention_idx
    ON document_generations(created_at);

INSERT INTO document_generations
    (doc_id, index_generation, source_version, chunk_count, created_at)
SELECT id, active_index_generation, source_version, chunk_count,
       COALESCE(updated_at, created_at)
  FROM documents;
