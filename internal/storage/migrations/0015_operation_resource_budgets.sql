-- Persist the declared peak resource footprint of each durable operation.
-- Older binaries must not reopen this database because they cannot honor the
-- admission contract when claiming a queued operation.
ALTER TABLE operations ADD COLUMN memory_bytes INTEGER NOT NULL DEFAULT 134217728;
ALTER TABLE operations ADD COLUMN disk_bytes INTEGER NOT NULL DEFAULT 268435456;
ALTER TABLE operations ADD COLUMN temp_bytes INTEGER NOT NULL DEFAULT 67108864;

UPDATE storage_format
   SET min_reader_version = 7,
       min_writer_version = 7
 WHERE id = 1;
