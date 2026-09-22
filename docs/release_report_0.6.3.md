# Shutu Knowledge v0.6.3 Release Report

Type: MAINTENANCE RELEASE (local embedding / reranker runtime stability).
Baseline: v0.6.2 (`1ae78de38593a0d7db58a7c2b6846fdd2a607544`).
Candidate source: `1636e5bd45eda6e647bbc7b741576d8489eb88a3`.

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
| Build (`go build ./...`) | pending |
| `go vet ./...` | pending |
| `go test ./...` | pending |
| Race (`go test -race ./...`) | pending |
| Web typecheck/tests/build | pending |
| Browser E2E | pending |
| Windows package smoke | pending |
| Push CI | pending |

## Release provenance

To be completed after tag CI, artifact publication, and post-download hash
verification.
