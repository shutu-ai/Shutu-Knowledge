# Release Source Provenance Audit

Audit date: 2026-09-06

## Scope And Method

Reference: `Soren-ABT/dsh-knowledge` v0.3.9, commit
`95e4a135cca3282b345c6d12a8e09cc4a314402f`, licensed AGPL-3.0. The reference
repository remained read-only.

This is a manual provenance review assisted by mechanical checks. Similarity
counts alone were not treated as proof. The review compared repository layout,
language/runtime boundaries, module ownership, algorithm organization, unique
comments and error text, public contract names, UI structure, tests, and the
recorded behavior references in `docs/source_reuse_inventory.md`.

Mechanical sampling results:

- Long exact string literals (at least 12 characters) in dsh TypeScript versus
  Knowledge Go production/tests: 0 overlap.
- Long exact string literals in dsh React/TSX and locale sources versus
  Knowledge independent browser JavaScript: 29 overlaps. They are common API
  field names, MIME types, configuration labels, and shared feature labels used
  for capability compatibility, not copied component code or copy expressions.
- 84 long identifiers overlap. They include public tool names, configuration
  fields, SQLite table concepts, and intentionally aligned domain terms. Manual
  review of complex context-fitting and chunk-boundary functions found different
  languages, data types, file/module organization, comments, and control-flow
  expression; no literal line translation was found.

The intentionally aligned names and behavior are recorded as clean-room
capability compatibility. Algorithms are not copied as protectable text, and
the AGPL source remains outside this repository's distribution.

## Provenance Matrix

| Area | Reference source | Implementation approach | Direct source copied? | License implication | Evidence | Result |
|---|---|---|---|---|---|---|
| Knowledge bases, documents, jobs | `src/knowledge/index.ts`, `domain.ts`, `store.ts` | Original Go service/store layer over Knowledge-owned SQLite and raw files | No | Clean behavioral reference only | Separate Go types/storage APIs; service and storage tests exercise required behavior without TS/Node architecture | PASS |
| Text/HTML parsers and ingestion | `src/knowledge/parse.ts` | Go parsers, encoding handling, URL/directory ingestion, and bounded job workflows | No | No AGPL text enters distribution | Parser tests, directory/URL lifecycle tests, and race suite pass | PASS |
| PDF parsing / caption rasters | `parse.ts`, `caption.ts` | Original Go PDF filter/color/tint/raster implementation; optional deployment decoder contract | No | No AGPL implementation or mupdf dependency bundled | Dedicated parser image/tint/soft-mask/optional-codec tests; optional external process tests | PASS |
| OCR and full-page rendering | `ocr.ts`, `ocr-worker.ts` | Original Go orchestration and bounded preprocessing; inference and rendering are external deployment processes | No | Inference, renderer binaries, and model weights remain external and separately licensed | Real child-process, fallback, timeout, health, and page-contract tests | PASS |
| Chunking and semantic merge | `chunk.ts` | Original Go scoring/refinement with typed pieces and separator decoding | No | Public scoring behavior may align; protectable expression does not | Go chunk/semantic tests and token-limit tests | PASS |
| Lexical/vector retrieval and context window | `chunkdb.ts`, `retrieval.ts`, `context.ts` | Original Go SQL retrieval, evidence window composer, and typed excerpt fitting | No | Domain/API-compatible names are not source copying; reviewed functions are independently expressed in Go | Exact long-literal overlap 0; focused review of anchor/budget/overlap code; retrieval/context tests | PASS |
| Embedding/rerank/local model runtime | embed/rerank/local model modules | Original Go provider adapters plus a generic isolated helper protocol | No | Inference engines, model weights, and third-party model terms are external | Runtime lifecycle, score/vector validation, timeout/restart, and self-test tests | PASS |
| Agent tools and auto RAG | `src/tool-knowledge/index.ts` | Original Go Extension adapter and policy implementation over public SDK types | No | Public `shutu-agent` SDK dependency is Apache-2.0 at v0.2.1; no Agent source is bundled | 14-tool catalog, scope/approval, auto-RAG, and real Agent process tests | PASS |
| Web management UI | `src/ui/client/*.tsx`, `locales.ts` | Independent dependency-free vanilla JavaScript/CSS UI | No | Shared functional labels/config names are compatibility contracts; component architecture differs | React/TSX versus vanilla JS; UI literal overlap limited to generic contract strings; contract test and Chrome CDP E2E | PASS |
| Tests and benchmarks | `tests/*.spec.ts`, benchmark data | Original Go unit/integration/benchmark/E2E tests using equivalent fixtures and assertions where needed | No | Test parity is behavioral evidence, not copied test code | Go/race suite, Web contract test, CDP E2E, benchmark smoke, and release gates | PASS |

## Result

No unrecorded direct or translated AGPL source copy was found. Source
provenance is **PASS**. This release only changes the public Agent module
version and release documentation; it introduces no new source reuse.

### Previous License Blocker

Previous blocker: `shutu-agent v0.2.0` had no explicit repository license.

Resolved: `shutu-agent v0.2.1` is publicly published under Apache-2.0 and its
module distribution includes `LICENSE`. The dependency is consumed through the
public `sdk/extension` API only.
