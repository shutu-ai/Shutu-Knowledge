-- Directory listings read one parent at a time. This index keeps bounded
-- child pages from scanning a large base.
CREATE INDEX IF NOT EXISTS documents_children_idx
    ON documents(base_id, parent_directory_id, lifecycle_state, created_at, id);
