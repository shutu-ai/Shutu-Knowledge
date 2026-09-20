# Shutu Knowledge v0.6.2 Release Report

Type: MAINTENANCE RELEASE (PDF ingestion correctness & quality visibility).
Baseline: v0.6.1 (`77b1db92a0dc0ac5e26ce6f6701c2b0d4527d46c`).

## Gate evidence

| Gate | Result |
| --- | --- |
| Build (`go build ./...`) | PASS |
| `go vet ./...` | PASS |
| `go test ./...` (full suite, incl. durable-operation, IR, retrieval, compiler, semantic, temporal, citation regressions) | PASS |
| Race (`go test -race ./internal/parser/...`) | PASS |
| Web typecheck / contract / api tests | PASS |
| Web build | PASS |
| Browser E2E (Chrome/CDP lifecycle) | PASS |
| Parser regression + PDF quality evaluator + candidate selection | PASS |
| U+FFFD tolerance | PASS (unit + real corpus) |
| OCR replacement safety | PASS (`TestWorseOCRDoesNotReplaceFragmentedNative`, `TestOCRReplacementSafetyRule`) |
| OCR partial coverage detection | PASS (`TestPartialOCRSurfacesWarningNotGood`) |
| Chunk hygiene | PASS (`TestChunkHygieneMergesFragments`) |
| Migration 0.6.1 -> 0.6.2 (schema 20 -> 21) | PASS (legacy rows surface UNKNOWN) |
| Restart/recovery + quality persistence | PASS (metadata survives server restart) |
| Real-corpus re-import | PASS (below) |
| No P0 | PASS |

## Real-corpus acceptance (aggregated; documents anonymized Doc A..I)

Corpus shape: 9 PDFs, ~11,859 pages, ~9.75M reference-extracted characters.
Clean isolated data home, runtime/model cache reused, no prior import state.

| Metric | v0.6.1 | v0.6.2 |
| --- | ---: | ---: |
| Documents | 9 | 9 |
| Pages | 11,859 | 11,859 |
| Approx text coverage | ~34% | **96.6%** |
| Low-quality docs | 7 | **0** |
| Silent incomplete docs | 7 | **0** |
| Fragment noise (per broken doc) | 20-91% | **0-1.4%** |
| Total import time | ~37 min | **~16 min** |
| OCR pages | up to 100/doc rendered (mostly lossy) | **0** |
| Retrieval probes passed | 0/4 | **5/5** |

### Per-document comparison

Reference extraction is an independent reader used as a scale reference, not
absolute ground truth (reading order and whitespace differ).

```text
Doc A ( 18 pages, text-layer)  before ~98%   after 96.0%  GOOD native
Doc B (3645 pages, text-layer) before 96.7%  after 96.7%  GOOD native
Doc C ( 792 pages, per-glyph)  before  0.04% after 95.0%  GOOD native
Doc D (1614 pages, per-glyph)  before  0.8%  after 96.8%  GOOD native
Doc E ( 306 pages, per-glyph)  before  8%    after 96.7%  GOOD native
Doc F (3285 pages, per-glyph)  before  0.1%  after 97.2%  GOOD native
Doc G (1412 pages, per-glyph)  before  2.3%  after 96.2%  GOOD native
Doc H ( 414 pages, per-glyph)  before  4.5%  after 95.8%  GOOD native
Doc I ( 373 pages, per-glyph)  before  3.9%  after 97.4%  GOOD native
```

Every document: `quality_status = GOOD`, `extraction_method = native`,
`pages_ocr = 0`, `quality_partial = false`. Doc B and Doc A (already healthy
in v0.6.1) did not regress.

### Retrieval probes (previously failing)

- Certificate-management query against Doc C: target document retrieved,
  readable table content (previously 0 hits from that document).
- Threshold-configuration query against Doc G: operational steps retrieved.
- Backup bucket query against Doc I: previously **zero results**, now the
  target section is the top hit.
- Broker-service-status query against Doc F: previously 0 hits from that
  document, now top hits with readable troubleshooting steps.
- Service-certificate query: previously 0 hits from Doc C, now readable
  certificate-management tables retrieved.

No garbage chunks dominate Top-K; OCR does not participate for this corpus.

## Release provenance

- Release source SHA: `1ae78de38593a0d7db58a7c2b6846fdd2a607544`
- Tag: `v0.6.2` (annotated), tag object `24ab3c9e2efd4710b9547be57024384cb95f496e`,
  tag target `1ae78de38593a0d7db58a7c2b6846fdd2a607544`
- Push/tag CI (run `35522967668`, refs/tags/v0.6.2): **success** - build,
  release-package, runtime-release, release-host windows/macos/ubuntu
- Master CI on the release commit and on the closure commit: success

## Artifact

Published on the GitHub Release `Shutu Knowledge v0.6.2`:

- Package: `shutu-knowledge-0.6.2-windows-amd64.zip` (built by the release CI
  from the tagged source; `BUILD-METADATA.json` records `git_sha =
  1ae78de38593a0d7db58a7c2b6846fdd2a607544`, version 0.6.2, storage
  format 2 / reader 8 / writer 8)
- Size: 12,626,237 bytes
- SHA-256: `8a8eedb66535ff4eab146d88f46555184b32a30dd2c85639b9f50c73e9193875`
- Post-download verification: re-downloaded the published asset and
  recomputed SHA-256 - exact match
- Internal `checksums.sha256`: all entries verified OK

A pre-CI local package from the same source and packaging script
(12,682,414 bytes, SHA-256 `58f391805c75c0708c8dc104ab0ac059f10a693a23233f10dc54aabd493d339c`)
was used for the formal package smoke below before publication.

## Formal package smoke (unzipped package, isolated home)

- Startup + runtime status: PASS (`doctor` ready, schema 21)
- Fragmented per-glyph text fixture: ready / WARNING (reassembled, tiny
  fixture flagged LOW_TEXT_DENSITY) / retrievable - the reassembled text is
  searchable and the per-glyph fragmentation is resolved
- True scanned fixture without OCR runtime installed: explicitly
  `parse_failed` (visible failure, never silent ready)
- Import quality metadata visible via API: qualityStatus / extractionMethod
- Restart: quality metadata persists
- Offline restart (`offline=1`): PASS

## Remaining limitations

- OCR batching across renderer calls (100 pages per call) is future work;
  partial coverage is now explicit via `OCR_PAGE_LIMIT_REACHED`.
- Legacy (pre-0.6.2) documents report quality UNKNOWN until re-imported.
