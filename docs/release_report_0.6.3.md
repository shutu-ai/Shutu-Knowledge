# Shutu Knowledge v0.6.3 Release Report

Type: MAINTENANCE RELEASE (local embedding / reranker runtime stability).
Baseline: v0.6.2 (`1ae78de38593a0d7db58a7c2b6846fdd2a607544`).
Candidate source: `4699a697674020ce5e90b69523a764917cb8bf55`. The candidate adds only packaged runtime-model metadata/package-smoke alignment after the runtime audit.

## Scope audit

`git diff v0.6.2..1636e5bd45eda6e647bbc7b741576d8489eb88a3` contains only the
four already-pushed runtime candidate commits:

- `38efdee` - stabilize local embeddings and reranker at real scale
- `240ff4d` - cover synthetic load cancellation and record soak evidence
- `cc2c50b` - record synthetic scale matrix evidence
- `1636e5b` - tolerate unknown write outcome during synthetic cancel

Production changes are limited to embedding/reranker execution, bounded top-K
vector reads/writes, search deadline and candidate-window behavior, managed
worker process retirement, managed web build robustness, and their tests/docs.
The Excel QA documents in the same diff are evidence, not a semantic capability
change. No 0.7 schema/data-model implementation is included.

## Runtime release evidence

Aggregated evidence is in
`docs/local_model_runtime_real_scale_validation.md`.

| Gate | Result |
| --- | --- |
| Real corpus scale | PASS - 154,475 / 154,475 active chunks embedded |
| Largest workbook | PASS - 64,268 / 64,268 chunks embedded |
| BM25 | PASS - 100/100, 0 timeout/error |
| Vector | PASS - 100/100, 0 timeout/error |
| Hybrid | PASS - 100/100, 0 timeout/error |
| Hybrid + reranker | PASS - 100/100, 0 timeout/error |
| Rerank applied | PASS - 100/100 eligible searches |
| Repeated runs | PASS - 3/3 representative 20-query runs |
| Monotonic memory growth | NO - steady-state run 2 to 3 delta 4.0 MiB |
| Bounded CPU | PASS - steady state below about 3 of 4 logical threads |
| Background embedding + search | PASS - 20/20 foreground searches |
| Cancellation | PASS - old active generation remained authoritative |
| Restart | PASS |
| Recovery | PASS - interrupted generation transition and retry completed |
| Offline restart | PASS |
| Synthetic runtime cancellation/retry | PASS - committed mixed-token test |
| No P0 | PASS |

Excel semantic QA remains WEAK (semantic reverse 7/30, cross-workbook 0/30,
analysis 2-3/20, join evidence 3/20). This is explicitly outside the v0.6.3
runtime release claim and does not justify 0.7 promotion.

## Software gates

| Gate | Result |
| --- | --- |
| Build (`go build ./...`) | PASS |
| `go vet ./...` | PASS |
| `go test ./...` | PASS |
| Race (`go test -race ./...`) | PASS |
| Web typecheck/tests/build | pending |
| Browser E2E | pending |
| Windows package smoke | PASS |
| Push CI | pending (run started after this candidate commit) |

## Release provenance

To be completed after tag CI, artifact publication, and post-download hash
verification.

## Pre-tag local candidate evidence

- Final candidate: `4699a697674020ce5e90b69523a764917cb8bf55`
- Formal package smoke: PASS (text + OCR fixtures, semantic retrieval,
  runtime status)
- Package offline restart: PASS
- Pre-CI package size: 12,712,015 bytes
- Pre-CI package SHA-256: `e2ca5a63d38fc75d9470a7eeaa52f8cad09904e4d9f62530d14795340f7aeb6f`
- Managed runtime direct smoke: PASS (embedding + reranker + PDF/OCR)
- Managed runtime offline restart: PASS
- Native host lifecycle acceptance: PASS