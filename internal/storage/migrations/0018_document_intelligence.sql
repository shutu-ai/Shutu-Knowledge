-- Document Intelligence foundation. These tables are additive: legacy
-- documents and chunks remain readable when the IR is absent.
CREATE TABLE document_nodes (
    doc_id          TEXT NOT NULL,
    index_generation INTEGER NOT NULL,
    node_id         TEXT NOT NULL,
    parent_node_id  TEXT,
    node_type       TEXT NOT NULL,
    node_order      INTEGER NOT NULL,
    text            TEXT NOT NULL DEFAULT '',
    heading_path    TEXT NOT NULL DEFAULT '[]',
    source_anchor   TEXT NOT NULL DEFAULT '{}',
    page_number     INTEGER,
    slide_number    INTEGER,
    sheet_name      TEXT,
    bbox            TEXT,
    metadata        TEXT NOT NULL DEFAULT '{}',
    parser          TEXT NOT NULL DEFAULT '',
    parser_version  TEXT NOT NULL DEFAULT '',
    confidence      REAL NOT NULL DEFAULT 0,
    PRIMARY KEY (doc_id, index_generation, node_id)
);

CREATE INDEX document_nodes_parent_idx
    ON document_nodes(doc_id, index_generation, parent_node_id, node_order);
CREATE INDEX document_nodes_anchor_idx
    ON document_nodes(doc_id, index_generation, page_number, slide_number, sheet_name);

CREATE TABLE document_parse_metadata (
    doc_id          TEXT NOT NULL,
    index_generation INTEGER NOT NULL,
    ir_version      TEXT NOT NULL,
    parser          TEXT NOT NULL DEFAULT '',
    parser_version  TEXT NOT NULL DEFAULT '',
    parse_config    TEXT NOT NULL DEFAULT '{}',
    PRIMARY KEY (doc_id, index_generation)
);

CREATE TABLE document_relationships (
    doc_id            TEXT NOT NULL,
    index_generation  INTEGER NOT NULL,
    relationship_id   TEXT NOT NULL,
    from_node_id      TEXT NOT NULL,
    to_node_id        TEXT NOT NULL,
    relationship_type TEXT NOT NULL,
    relationship_order INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (doc_id, index_generation, relationship_id)
);

CREATE INDEX document_relationships_from_idx
    ON document_relationships(doc_id, index_generation, from_node_id);

CREATE TABLE chunk_node_links (
    doc_id           TEXT NOT NULL,
    index_generation INTEGER NOT NULL,
    chunk_id         TEXT NOT NULL,
    node_id          TEXT NOT NULL,
    link_order       INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (doc_id, index_generation, chunk_id, node_id)
);

CREATE INDEX chunk_node_links_node_idx
    ON chunk_node_links(doc_id, index_generation, node_id);

CREATE TABLE derived_knowledge (
    id               TEXT PRIMARY KEY,
    doc_id           TEXT NOT NULL,
    index_generation INTEGER NOT NULL,
    kind             TEXT NOT NULL,
    content          TEXT NOT NULL,
    derived_from     TEXT NOT NULL DEFAULT '[]',
    model            TEXT NOT NULL DEFAULT 'builtin',
    model_version    TEXT NOT NULL DEFAULT 'builtin-v1',
    provenance       TEXT NOT NULL DEFAULT '{}',
    created_at       INTEGER NOT NULL,
    invalidated_at   INTEGER
);

CREATE INDEX derived_knowledge_doc_idx
    ON derived_knowledge(doc_id, index_generation, invalidated_at);
