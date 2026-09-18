# Document IR v1

## Contract

`document-ir/v1` is a deterministic, serializable intermediate
representation. It is generated without an LLM and is independent of the
parser backend. The existing `parser.Result.Text` remains the compatibility
text projection; `parser.Result.IR` is the structured projection.

An IR contains document metadata, ordered nodes, and explicit relationships.
Nodes use a small set of document-facing types: `document`, `section`,
`page`, `slide`, `sheet`, `block`, `paragraph`, `heading`, `table`,
`table_row`, `table_cell`, `figure`, `caption`, `list`, `list_item`,
`code_block`, `footnote`, `header`, and `footer`.

## Identity and provenance

Node IDs are SHA-256 based IDs over the bound document ID, node type, logical
position, and source anchor. Rebinding an IR to a document ID recomputes all
IDs and parent/child references. Re-parsing the same source with the same
parser/config therefore produces stable IDs; changing a source version still
uses the existing generation identity to control visibility.

Source anchors are typed and conservative. PDF anchors use page numbers and
optional bounding boxes; PowerPoint uses slide and shape/order information;
Excel uses sheet and cell/range information; text and fallback parsers use a
logical path. Missing precision is represented as an empty field, never a
fabricated location.

## Storage

Migration 0018 adds `document_nodes`, `document_relationships`,
`chunk_node_links`, and `derived_knowledge`. Nodes are JSON metadata rows so
new optional fields can be added without changing the document/chunk
contract. Rows are generation-scoped and are published with the same active
generation fence as chunks.

## Chunking boundary

The existing chunker remains the retrieval implementation. It receives an IR
projection and records the node IDs whose text contributes to each chunk.
This preserves heading-aware and token-limit behavior while making page,
slide, sheet, table, and section provenance available to later retrieval and
citation work.
