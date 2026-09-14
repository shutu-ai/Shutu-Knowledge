-- Compatibility baseline for generation-aware indexing and delete fences.
ALTER TABLE bases ADD COLUMN lifecycle_state TEXT NOT NULL DEFAULT 'active'
  CHECK (lifecycle_state IN ('active', 'deleting'));
ALTER TABLE bases ADD COLUMN mutation_epoch INTEGER NOT NULL DEFAULT 0;

ALTER TABLE documents ADD COLUMN lifecycle_state TEXT NOT NULL DEFAULT 'active'
  CHECK (lifecycle_state IN ('active', 'deleting'));
ALTER TABLE documents ADD COLUMN mutation_epoch INTEGER NOT NULL DEFAULT 0;
ALTER TABLE documents ADD COLUMN source_version INTEGER NOT NULL DEFAULT 0;
ALTER TABLE documents ADD COLUMN active_index_generation INTEGER NOT NULL DEFAULT 0;
ALTER TABLE documents ADD COLUMN desired_index_generation INTEGER;
ALTER TABLE documents ADD COLUMN index_state TEXT NOT NULL DEFAULT 'active'
  CHECK (index_state IN ('active', 'building', 'failed', 'retired'));

ALTER TABLE chunks ADD COLUMN index_generation INTEGER NOT NULL DEFAULT 0;

CREATE INDEX documents_generation_idx ON documents(base_id, lifecycle_state, active_index_generation);
CREATE INDEX chunks_generation_idx ON chunks(doc_id, index_generation);
CREATE INDEX chunks_active_generation_idx ON chunks(base_id, index_generation);

INSERT INTO kv (key, value) VALUES ('storage_format_version', '1')
  ON CONFLICT(key) DO UPDATE SET value = MAX(kv.value, excluded.value);
INSERT INTO kv (key, value) VALUES ('min_reader_version', '5')
  ON CONFLICT(key) DO UPDATE SET value = MAX(kv.value, excluded.value);
INSERT INTO kv (key, value) VALUES ('min_writer_version', '5')
  ON CONFLICT(key) DO UPDATE SET value = MAX(kv.value, excluded.value);
