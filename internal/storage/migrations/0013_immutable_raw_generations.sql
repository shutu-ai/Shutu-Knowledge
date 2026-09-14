-- Raw bytes are pinned by the same durable generation/source identity as
-- chunks. Legacy rows retain their shared path for reading, while newly
-- published generations must use immutable version paths.
ALTER TABLE document_generations ADD COLUMN raw_file_path TEXT NOT NULL DEFAULT '';
ALTER TABLE document_generations ADD COLUMN content_hash TEXT NOT NULL DEFAULT '';

UPDATE document_generations
   SET raw_file_path = (
       SELECT COALESCE(d.raw_file_path, '') FROM documents d
        WHERE d.id = document_generations.doc_id
          AND d.active_index_generation = document_generations.index_generation
   ),
       content_hash = (
       SELECT COALESCE(d.content_hash, '') FROM documents d
        WHERE d.id = document_generations.doc_id
          AND d.active_index_generation = document_generations.index_generation
   );

INSERT INTO kv (key, value) VALUES ('storage_format_version', '2')
  ON CONFLICT(key) DO UPDATE SET value = MAX(kv.value, excluded.value);
UPDATE storage_format
   SET format_version = 2, min_reader_version = 6, min_writer_version = 6
 WHERE id = 1;
