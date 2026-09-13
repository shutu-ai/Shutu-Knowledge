-- Persist the completed embedding model per document so document listings do
-- not need to aggregate the entire chunks table while an import is running.
ALTER TABLE documents ADD COLUMN embedding_model TEXT;
ALTER TABLE documents ADD COLUMN embedding_ready INTEGER NOT NULL DEFAULT 0;
