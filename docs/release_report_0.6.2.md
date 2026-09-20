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

## Artifact

- `shutu-knowledge-0.6.2-windows-amd64.zip` - size and SHA-256 recorded at
  publication; source SHA documented in the closure commit.

## Remaining limitations

- OCR batching across renderer calls (100 pages per call) is future work;
  partial coverage is now explicit via `OCR_PAGE_LIMIT_REACHED`.
- Legacy (pre-0.6.2) documents report quality UNKNOWN until re-imported.
