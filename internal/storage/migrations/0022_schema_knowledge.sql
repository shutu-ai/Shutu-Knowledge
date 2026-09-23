-- Generic structured schema knowledge (v0.7). The layer is additive and each
-- document generation is replaced atomically; legacy documents remain valid
-- with zero schema entities.
CREATE TABLE schema_generations (
    base_id          TEXT NOT NULL,
    doc_id           TEXT NOT NULL,
    generation       INTEGER NOT NULL,
    state            TEXT NOT NULL DEFAULT 'active'
                     CHECK (state IN ('building','active','retired','failed','absent')),
    compiler         TEXT NOT NULL DEFAULT 'builtin',
    compiler_version TEXT NOT NULL DEFAULT 'schema-model-v1',
    detected         INTEGER NOT NULL DEFAULT 0,
    table_count      INTEGER NOT NULL DEFAULT 0,
    field_count      INTEGER NOT NULL DEFAULT 0,
    enum_count       INTEGER NOT NULL DEFAULT 0,
    concept_count    INTEGER NOT NULL DEFAULT 0,
    key_count        INTEGER NOT NULL DEFAULT 0,
    compile_ms       INTEGER NOT NULL DEFAULT 0,
    metadata         TEXT NOT NULL DEFAULT '{}',
    created_at       INTEGER NOT NULL,
    completed_at     INTEGER,
    PRIMARY KEY (base_id, doc_id, generation)
);

CREATE UNIQUE INDEX schema_generations_one_active
    ON schema_generations(base_id, doc_id) WHERE state = 'active';
CREATE INDEX schema_generations_state_idx
    ON schema_generations(base_id, state, generation DESC);

CREATE TABLE schema_tables (
    id                TEXT PRIMARY KEY,
    base_id           TEXT NOT NULL,
    doc_id            TEXT NOT NULL,
    generation        INTEGER NOT NULL,
    name              TEXT NOT NULL,
    display_name      TEXT NOT NULL DEFAULT '',
    aliases           TEXT NOT NULL DEFAULT '[]',
    description       TEXT NOT NULL DEFAULT '',
    business_purpose  TEXT NOT NULL DEFAULT '',
    purpose_evidence  TEXT NOT NULL DEFAULT '',
    purpose_inferred  INTEGER NOT NULL DEFAULT 0,
    scope             TEXT NOT NULL,
    region_anchor     TEXT NOT NULL DEFAULT '',
    field_ids         TEXT NOT NULL DEFAULT '[]',
    source            TEXT NOT NULL DEFAULT '{}',
    confidence        REAL NOT NULL DEFAULT 0,
    detection_signals TEXT NOT NULL DEFAULT '[]',
    UNIQUE (base_id, doc_id, generation, id)
);
CREATE INDEX schema_tables_scope_idx
    ON schema_tables(base_id, doc_id, generation, scope, name);

CREATE TABLE schema_fields (
    id           TEXT PRIMARY KEY,
    base_id      TEXT NOT NULL,
    doc_id       TEXT NOT NULL,
    generation   INTEGER NOT NULL,
    table_id     TEXT NOT NULL,
    table_name   TEXT NOT NULL,
    scope        TEXT NOT NULL,
    name         TEXT NOT NULL,
    display_name TEXT NOT NULL DEFAULT '',
    aliases      TEXT NOT NULL DEFAULT '[]',
    data_type    TEXT NOT NULL DEFAULT '',
    description  TEXT NOT NULL DEFAULT '',
    nullable     INTEGER NOT NULL DEFAULT 0,
    enum_id      TEXT,
    concepts     TEXT NOT NULL DEFAULT '[]',
    source       TEXT NOT NULL DEFAULT '{}',
    confidence   REAL NOT NULL DEFAULT 0,
    UNIQUE (base_id, doc_id, generation, id)
);
CREATE INDEX schema_fields_table_idx
    ON schema_fields(base_id, doc_id, generation, table_id, name);
CREATE INDEX schema_fields_name_idx
    ON schema_fields(base_id, generation, name);

CREATE TABLE schema_enums (
    id          TEXT PRIMARY KEY,
    base_id     TEXT NOT NULL,
    doc_id      TEXT NOT NULL,
    generation  INTEGER NOT NULL,
    field_id    TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    enum_values TEXT NOT NULL,
    source      TEXT NOT NULL DEFAULT '{}',
    confidence  REAL NOT NULL DEFAULT 0
);
CREATE INDEX schema_enums_field_idx
    ON schema_enums(base_id, doc_id, generation, field_id);

CREATE TABLE schema_key_candidates (
    id         TEXT PRIMARY KEY,
    base_id    TEXT NOT NULL,
    doc_id     TEXT NOT NULL,
    generation INTEGER NOT NULL,
    field_id   TEXT NOT NULL,
    key_type   TEXT NOT NULL,
    confidence REAL NOT NULL DEFAULT 0,
    reason     TEXT NOT NULL,
    evidence   TEXT NOT NULL DEFAULT '[]'
);
CREATE INDEX schema_key_candidates_field_idx
    ON schema_key_candidates(base_id, doc_id, generation, field_id, key_type);

CREATE TABLE schema_concepts (
    id         TEXT PRIMARY KEY,
    base_id    TEXT NOT NULL,
    doc_id     TEXT NOT NULL,
    generation INTEGER NOT NULL,
    name       TEXT NOT NULL,
    aliases    TEXT NOT NULL DEFAULT '[]',
    confidence REAL NOT NULL DEFAULT 0,
    UNIQUE (base_id, doc_id, generation, name)
);
CREATE INDEX schema_concepts_name_idx
    ON schema_concepts(base_id, generation, name);

CREATE TABLE schema_concept_fields (
    concept_id TEXT NOT NULL,
    field_id   TEXT NOT NULL,
    base_id    TEXT NOT NULL,
    doc_id     TEXT NOT NULL,
    generation INTEGER NOT NULL,
    PRIMARY KEY (concept_id, field_id)
);
CREATE INDEX schema_concept_fields_field_idx
    ON schema_concept_fields(base_id, generation, field_id);

-- Semantic entity projection is intentionally separate from evidence chunks.
-- Vectors are additive and may be absent for lexical-only deployments.
CREATE TABLE schema_entities (
    id          TEXT PRIMARY KEY,
    base_id     TEXT NOT NULL,
    doc_id      TEXT NOT NULL,
    generation  INTEGER NOT NULL,
    entity_type TEXT NOT NULL CHECK (entity_type IN ('TABLE','FIELD','BUSINESS_CONCEPT')),
    entity_id   TEXT NOT NULL,
    title       TEXT NOT NULL,
    content     TEXT NOT NULL,
    aliases     TEXT NOT NULL DEFAULT '[]',
    scope       TEXT NOT NULL,
    table_id    TEXT,
    concepts    TEXT NOT NULL DEFAULT '[]',
    source      TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX schema_entities_type_idx
    ON schema_entities(base_id, doc_id, generation, entity_type);
CREATE INDEX schema_entities_entity_idx
    ON schema_entities(base_id, doc_id, generation, entity_id);

CREATE TABLE schema_vectors (
    id           TEXT PRIMARY KEY,
    base_id      TEXT NOT NULL,
    doc_id       TEXT NOT NULL,
    generation   INTEGER NOT NULL,
    entity_type  TEXT NOT NULL,
    entity_id    TEXT NOT NULL,
    model        TEXT NOT NULL,
    dimension    INTEGER NOT NULL,
    content_hash TEXT NOT NULL,
    vector       BLOB NOT NULL,
    UNIQUE (base_id, doc_id, generation, entity_type, entity_id, model)
);
CREATE INDEX schema_vectors_model_idx
    ON schema_vectors(base_id, model, entity_type);


INSERT INTO kv (key, value) VALUES ('storage_format_version', '2')
  ON CONFLICT(key) DO UPDATE SET value = MAX(kv.value, excluded.value);
UPDATE storage_format
   SET min_reader_version = 9, min_writer_version = 9
 WHERE id = 1;


