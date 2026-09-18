-- 0.4 Knowledge Model foundation. The tables are additive: an existing 0.3
-- database remains searchable with zero compiled semantic units.
CREATE TABLE knowledge_compilations (
    base_id             TEXT NOT NULL,
    generation          INTEGER NOT NULL,
    state               TEXT NOT NULL DEFAULT 'building'
                        CHECK (state IN ('building','active','retired','failed')),
    compiler            TEXT NOT NULL,
    compiler_version    TEXT NOT NULL,
    model               TEXT NOT NULL DEFAULT 'builtin',
    model_version       TEXT NOT NULL DEFAULT 'builtin-v1',
    prompt_version      TEXT NOT NULL DEFAULT 'builtin-v1',
    source_document_ids TEXT NOT NULL DEFAULT '[]',
    metadata            TEXT NOT NULL DEFAULT '{}',
    created_at          INTEGER NOT NULL,
    completed_at        INTEGER,
    PRIMARY KEY (base_id, generation)
);

-- A base has at most one active immutable compilation generation.
CREATE UNIQUE INDEX knowledge_compilations_one_active
    ON knowledge_compilations(base_id) WHERE state = 'active';
CREATE INDEX knowledge_compilations_state_idx
    ON knowledge_compilations(base_id, state, generation DESC);

CREATE TABLE knowledge_units (
    id                  TEXT PRIMARY KEY,
    base_id             TEXT NOT NULL,
    generation          INTEGER NOT NULL,
    unit_type           TEXT NOT NULL
                        CHECK (unit_type IN ('fact','concept','topic','summary','knowledge_page')),
    title               TEXT NOT NULL,
    canonical_key       TEXT NOT NULL,
    content             TEXT NOT NULL,
    aliases             TEXT NOT NULL DEFAULT '[]',
    parent_unit_id      TEXT,
    confidence          REAL NOT NULL DEFAULT 0.5
                        CHECK (confidence >= 0 AND confidence <= 1),
    status              TEXT NOT NULL DEFAULT 'active'
                        CHECK (status IN ('active','superseded','conflicted','deleted')),
    valid_from          INTEGER,
    valid_to            INTEGER,
    superseded_by       TEXT,
    metadata            TEXT NOT NULL DEFAULT '{}',
    created_at          INTEGER NOT NULL,
    updated_at          INTEGER NOT NULL,
    UNIQUE (base_id, generation, unit_type, canonical_key),
    CHECK (valid_from IS NULL OR valid_to IS NULL OR valid_to >= valid_from)
);

CREATE INDEX knowledge_units_active_idx
    ON knowledge_units(base_id, generation, status, unit_type);
CREATE INDEX knowledge_units_parent_idx
    ON knowledge_units(base_id, generation, parent_unit_id);
CREATE INDEX knowledge_units_canonical_idx
    ON knowledge_units(base_id, generation, canonical_key);
CREATE INDEX knowledge_units_superseded_idx
    ON knowledge_units(superseded_by);

-- Exact Document IR / chunk provenance. At least node_id or chunk_id must be
-- supplied by the repository validation path; SQL keeps them nullable because
-- one source can be IR-only or chunk-only.
CREATE TABLE knowledge_unit_sources (
    provenance_id      INTEGER PRIMARY KEY,
    unit_id            TEXT NOT NULL,
    doc_id             TEXT NOT NULL,
    index_generation   INTEGER NOT NULL,
    source_version     INTEGER NOT NULL,
    node_id            TEXT,
    chunk_id           TEXT,
    source_anchor      TEXT NOT NULL DEFAULT '{}',
    source_order       INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX knowledge_unit_sources_unit_idx
    ON knowledge_unit_sources(unit_id, source_order, provenance_id);
CREATE INDEX knowledge_unit_sources_evidence_idx
    ON knowledge_unit_sources(doc_id, index_generation, node_id, chunk_id);

-- Explicit derived_from edges preserve auditable abstraction chains such as
-- KnowledgePage -> Concept -> Fact before reaching the source rows above.
CREATE TABLE knowledge_unit_derived_from (
    unit_id             TEXT NOT NULL,
    derived_unit_id     TEXT NOT NULL,
    link_order          INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (unit_id, derived_unit_id)
);

CREATE INDEX knowledge_unit_derived_from_target_idx
    ON knowledge_unit_derived_from(derived_unit_id);

CREATE TABLE knowledge_relations (
    id                  TEXT PRIMARY KEY,
    base_id             TEXT NOT NULL,
    generation          INTEGER NOT NULL,
    relation_type       TEXT NOT NULL
                        CHECK (relation_type IN ('derived_from','part_of','mentions','related_to','supports','contradicts','supersedes','same_topic')),
    subject_unit_id     TEXT NOT NULL,
    object_unit_id      TEXT NOT NULL,
    predicate           TEXT NOT NULL DEFAULT '',
    statement           TEXT NOT NULL DEFAULT '',
    canonical_key       TEXT NOT NULL,
    confidence          REAL NOT NULL DEFAULT 0.5
                        CHECK (confidence >= 0 AND confidence <= 1),
    status              TEXT NOT NULL DEFAULT 'active'
                        CHECK (status IN ('active','superseded','conflicted','deleted')),
    valid_from          INTEGER,
    valid_to            INTEGER,
    superseded_by       TEXT,
    metadata            TEXT NOT NULL DEFAULT '{}',
    created_at          INTEGER NOT NULL,
    updated_at          INTEGER NOT NULL,
    UNIQUE (base_id, generation, relation_type, canonical_key),
    CHECK (valid_from IS NULL OR valid_to IS NULL OR valid_to >= valid_from)
);

CREATE INDEX knowledge_relations_subject_idx
    ON knowledge_relations(base_id, generation, subject_unit_id, status, relation_type);
CREATE INDEX knowledge_relations_object_idx
    ON knowledge_relations(base_id, generation, object_unit_id, status, relation_type);
CREATE INDEX knowledge_relations_superseded_idx
    ON knowledge_relations(superseded_by);

CREATE TABLE knowledge_relation_sources (
    provenance_id      INTEGER PRIMARY KEY,
    relation_id        TEXT NOT NULL,
    doc_id             TEXT NOT NULL,
    index_generation   INTEGER NOT NULL,
    source_version     INTEGER NOT NULL,
    node_id            TEXT,
    chunk_id           TEXT,
    source_anchor      TEXT NOT NULL DEFAULT '{}',
    source_order       INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX knowledge_relation_sources_relation_idx
    ON knowledge_relation_sources(relation_id, source_order, provenance_id);
CREATE INDEX knowledge_relation_sources_evidence_idx
    ON knowledge_relation_sources(doc_id, index_generation, node_id, chunk_id);
