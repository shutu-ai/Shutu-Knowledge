# Shutu Knowledge 0.3.0 delivery report

## Version and candidate

- Version: `0.3.0`
- Validated implementation commit: `008125936fc63e68224a8093f6777ce1d9c0c7d2`
- Storage migration: `0018_document_intelligence.sql`

## Architecture

`document-ir/v1` is a deterministic parser-independent representation. It
contains ordered document/section/page/slide/sheet/block/paragraph/heading/
list/list-item/table/row/cell/figure/image/caption/footnote nodes, source
anchors, confidence, and relationships.
Node IDs are deterministic SHA-256 identifiers bound to the document ID.
Chunks retain node links and the best specific source anchor; retrieval emits
additive `CitationV2` data while preserving the 0.2 fields and ranking lanes.

## Migration and parser status

| Area | Result | Evidence |
| --- | --- | --- |
| 0.2 to 0.3 storage migration | PASS | Migration 0018, full normal and race suites |
| PDF | PASS | Page/block/bbox IR test plus deterministic table/figure/caption detection and real PDF corpus |
| DOCX | PASS | Heading/list/section/caption/table-cell IR test and real DOCX corpus |
| PPTX | PASS | Three-slide golden plus table/row/cell, figure/image, and speaker-notes coverage |
| XLSX | PASS | Named sheet, used range, table/row/cell, formula/header/merged metadata and real XLSX corpus |
| Legacy Office | OPTIONAL | Existing helper boundary remains explicit; no helper is bundled |
| Fallback paths | PASS | OCR/content fallback paths publish the same IR contract and increment local fallback telemetry |

## Golden and lifecycle tests

- Document IR golden: PASS (`TestGoldenCorpusManifestAndIR`), reading committed
  `simple.pdf`, `multicolumn.pdf`, `tables.pdf`, `figures.pdf`, `scanned.pdf`,
  `long.pdf` (105 pages), `structured.docx`, `tables.docx`,
  `presentation.pptx`, and `spreadsheet.xlsx` artifacts generated reproducibly
  by the corpus tool
- Retrieval golden: PASS (`TestGoldenRetrievalAndCitationAccuracy`)
- Citation accuracy: PASS, including section and XLSX `Revenue!A2` retrieval
  citations; PDF page/bbox anchors, structural table/header/row context, and
  generation-fenced provenance are covered by the parser/lifecycle tests
- Update/delete/reindex generation fencing: PASS in the existing Knowledge suite
- Delete cleanup: PASS, including nodes and chunk links
- Restart/recovery: PASS in the existing operations/runtime suite
- Agent extension adapter: PASS in the full suite; local native host acceptance
  passed healthz, duplicate-instance rejection, and crash-restart at
  `.tmp/release-acceptance/20260918-085701/result.json`; a real external Agent
  Host run was not performed
- Windows package smoke: PASS, including online import/OCR/vector retrieval and offline restart retrieval

## Regression and performance

- `go test ./... -count=1 -timeout=30m`: PASS
- `go test -race ./... -count=1 -timeout=30m`: PASS; no race reports
- `npm test`: PASS
- `npm run build`: PASS; embedded six-file Web bundle rebuilt
- One-shot local benchmark on Windows amd64, Intel Core Ultra 7 255H:
  - corpus ingestion: 22.761 ms/op, 93,344 B/op
  - lexical retrieval: 23.846 ms/op, 105,208 B/op
  - vector retrieval: 2.305 ms/op, 557,008 B/op
  - hybrid retrieval: 1.425 ms/op, 552,064 B/op
  - end-to-end RAG: 37.828 ms/op, 579,504 B/op

The benchmark uses the repository's deterministic benchmark provider and is a
local baseline, not a production capacity claim.

## Release artifact

- Filename: `shutu-knowledge-0.3.0-windows-amd64.zip`
- Size: `12,356,128` bytes
- SHA-256: `83cef3a03ba3bbd201004a22181447b4e2bf3510259314d80f3bb1e76bc29daa`
- Package source commit: `008125936fc63e68224a8093f6777ce1d9c0c7d2`
- Static package secret audit: PASS
- Package smoke: PASS, including text import, OCR import, online retrieval,
  runtime status, and offline restart retrieval

## Doctor and diagnostics

Doctor and `/api/status` expose critical `document-parser`, `document-ir`,
and `structured-index` checks. Optional LLM enrichment is reported as
`ENRICHMENT UNAVAILABLE` without failing core readiness. Local metrics include
parser selection, parse duration, node/table/figure/chunk counts, and fallback
count without document text or credentials.

## Known limitations and deferred work

- The committed corpus uses deterministic fixtures, including a 105-page PDF,
  rather than production-sized Office/PDF files; OCR/rendering remains an
  optional runtime.
- Visual figure understanding, entity/ontology extraction, GraphRAG, query
  routing v0, and a richer chunk-link inspector remain deferred to 0.4+.
- CI push/tag status was not queried because this workspace has no GitHub CLI
  authentication and no push/tag was performed.
- A real external Agent Host acceptance run remains a release-host gate.

## Release status

`NOT READY` until push/tag CI and external Agent Host acceptance are run and
recorded. The local implementation, tests, artifact build, static audit, and
final Windows package smoke are ready.
