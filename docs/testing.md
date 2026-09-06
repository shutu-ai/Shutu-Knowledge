# Testing

## Test Layers

| Layer | Location / command | Scope |
|---|---|---|
| Unit / regression | `go test ./...` | parsers, chunking, stores, retrieval, model providers, service lifecycle |
| API integration | `go test ./internal/web` | HTTP contracts, conflict handling, metrics, raw restore, write-only per-base credentials |
| Extension integration | `go test ./internal/extension` | manifest, stdio lifecycle, tools, automatic context policy |
| Runtime integration | `go test ./internal/runtime` | real helper process handshake, calls, restart, idle lifecycle |
| Storage | `go test ./internal/storage` | migrations, FTS, raw-store guards/private modes, maintenance |
| Web | `cd web && npm test` | API route contracts and build integrity |
| Browser E2E | `cd web && npm run test:e2e` | real Chrome/CDP lifecycle: base create, import, ready state, chunk expand/collapse, Recall Test, history replay/delete, OCR model workflow, dismissable toast and Ollama isolation policy, zh/en shell localization, and base delete |
| Race / concurrency | `go test -race ./...` | jobs, batch ingestion, runtime processes, service state |
| Benchmark | `go test ./internal/knowledge -run '^$' -bench Benchmark -benchmem` | repeatable corpus timing and retrieval quality |

Gate evidence is consolidated in `docs/gates.md`; the Phase 8 security review
and residual deployment risks are recorded in `docs/security_review.md`. The
one-for-one map from 25 upstream spec areas to current test layers is in
`docs/test_parity.md`.

CI runs web build/test, the Agent-internal import gate, build, vet, unit/race
tests, a one-iteration benchmark smoke run, and the browser E2E lifecycle.
Browser E2E builds the Web assets and Go binary, starts a temporary Knowledge
data domain on loopback, drives real Chrome through CDP, checks the dedicated
PaddleOCR model/runtime state, chunk preview expansion, upstream UI-policy
behavior (dismissable toasts and Ollama browsing/pulling isolated from provider
persistence), persistent zh/en localization, and cleans successful
temporary state automatically. It requires Chrome, Chromium, or Edge; CI
selects `google-chrome`.

## Retrieval Benchmark Dataset

The Go benchmark fixture creates the same 119-document corpus on every run. It
covers:

- English and Chinese lexical queries,
- semantic/vector concepts,
- multi-document relevance,
- exact duplicate copies with one distinguishing identifier,
- a long document,
- mixed-language finance vocabulary,
- and deterministic filler documents so ranking is not trivial.

The correctness gate checks Hit@3, MRR, and context recall across six labeled
queries. Deterministic vectors make this gate reproducible in CI; it evaluates
retrieval composition, not neural-model quality.

## Benchmark Commands

Fast CI smoke:

```sh
go test ./internal/knowledge -run '^$' -bench Benchmark -benchtime=1x
```

Longer local run:

```sh
go test ./internal/knowledge -run '^$' -bench Benchmark -benchmem -benchtime=3s
```

The suite reports ingestion, chunking, embedding hash reuse, lexical, vector,
hybrid, rerank, and end-to-end ingest/search timings plus allocations. Numbers
are machine-dependent and are useful for regression comparison, not absolute
product-performance claims.
