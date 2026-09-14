-- Persist the compatibility envelope used by runtime negotiation. The values
-- are bumped explicitly by format-changing migrations, not derived from the
-- migration counter (adding a table does not necessarily break readers).
CREATE TABLE storage_format (
    id                 INTEGER PRIMARY KEY CHECK (id = 1),
    format_version     INTEGER NOT NULL,
    min_reader_version INTEGER NOT NULL,
    min_writer_version INTEGER NOT NULL,
    migration_status   TEXT NOT NULL DEFAULT 'ready'
                       CHECK (migration_status IN ('ready','migrating','failed'))
);

INSERT INTO storage_format (id, format_version, min_reader_version, min_writer_version)
VALUES (1, 1, 5, 5);
