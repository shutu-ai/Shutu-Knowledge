# Shutu Knowledge v0.7.0 Release Notes

v0.7.0 is **Structured Data Model Intelligence**. It upgrades spreadsheet data
dictionaries from isolated searchable chunks to a generic, evidence-grounded
schema-knowledge layer while preserving Document IR, original evidence, and
generation provenance.

## Structured schema knowledge

- Generic **LogicalTable**, **Field**, **Enum**, **KeyCandidate**, and
  **BusinessConcept** entities are stored independently from evidence chunks.
- Field identity includes scope, logical table, and field; same names in
  different tables are never merged by name.
- Conservative schema detection maps field-name, type, meaning, enum, and
  primary-key signals. Non-dictionary Excel remains on the ordinary path.
- Multi-region sheets, propagated table context, and bounded multi-row headers
  are represented with source ranges.
- Enums preserve ordered `value -> meaning` mappings.

## Schema-aware retrieval and reasoning

- Field/table entity retrieval is separate from ordinary evidence-chunk search.
- Cross-workbook set reasoning resolves concepts to field sets, maps fields to
  tables, intersects qualifying tables, and groups by workbook.
- Candidate joins expose two fields, confidence, reason, and evidence. They are
  probabilistic candidates and are never asserted as required foreign keys.
- Data-requirement resolution produces concepts, candidate tables and fields,
  possible join candidates, evidence, and explicit unknowns.
- Structured context is bounded and traceable. SQL generation and execution are
  explicitly out of scope.

## Real-world validation

Using the same fixed 50-workbook corpus and 150-query benchmark:

| Category | v0.6.4 | v0.7.0 |
|---|---:|---:|
| Exact | 50/50 | 50/50 |
| Semantic reverse | 7/30 | 23/30 |
| Cross-workbook | 0/30 | 18/30 |
| Analysis | 2–3/20 | 12/20 |
| Join/data-model | 3/20 | 18/20 |
| Unsupported join claims | — | 0 |
| Same-name wrong merges | — | 0 |

Compilation produced 4,194 logical tables, 79,685 fields, 2,574 enums, 323
business concepts, and 21,295 key candidates.

## Remaining limitations

These measured gaps remain and are intentionally not hidden:

- Semantic reverse lookup: **23/30**.
- Cross-workbook resolution: **18/30**.
- Analysis requirement resolution: **12/20**.
- Join/data-model resolution: **18/20**.

v0.7.0 does **not** claim perfect semantic reasoning, SQL generation/execution,
GraphRAG, ontology support, or autonomous recursive research.
