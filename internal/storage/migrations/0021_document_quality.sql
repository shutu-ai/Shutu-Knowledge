-- Document extraction quality visibility (v0.6.2). Extraction quality is
-- deliberately independent of operation completion: a completed import of an
-- under-extracted document must stay observable instead of silently ready.
-- Legacy rows keep the empty status, surfaced as UNKNOWN by the API.
ALTER TABLE documents ADD COLUMN quality_status TEXT NOT NULL DEFAULT '';
ALTER TABLE documents ADD COLUMN quality_score REAL NOT NULL DEFAULT 0;
ALTER TABLE documents ADD COLUMN quality_warnings TEXT NOT NULL DEFAULT '';
ALTER TABLE documents ADD COLUMN quality_partial INTEGER NOT NULL DEFAULT 0;
ALTER TABLE documents ADD COLUMN extraction_method TEXT NOT NULL DEFAULT '';
ALTER TABLE documents ADD COLUMN pages_total INTEGER NOT NULL DEFAULT 0;
ALTER TABLE documents ADD COLUMN pages_ocr INTEGER NOT NULL DEFAULT 0;
