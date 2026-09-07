# Source Reuse Inventory

Policy: behavior-level reimplementation only. dsh-knowledge is AGPL-3.0; this project is Apache-2.0 and does not copy or translate its source. Reading, running, and describing behavior are permitted; copying requires the AGPL checklist (license propagation, attribution, corresponding source) and remains prohibited by the current reuse policy.

## Reused / translated / copied code

| Source repository | Source file/module | Reuse type | License | Local destination | Required attribution |
|---|---|---|---|---|---|
| (none) | | | | | |

## Behavioral references (design observation only, no code reuse)

| Behavior studied | dsh-knowledge evidence | Reimplementation note |
|---|---|---|
| Chunk scoring model (heading/code/paragraph/sentence breaks, decay) | `src/knowledge/chunk.ts` | original Go implementation driven by the documented scoring table and tests |
| RRF k=60 fusion, weight normalization | `src/knowledge/retrieval.ts`, `src/knowledge/index.ts` | formula from README/audit; original implementation + deterministic tests |
| Context window composition rules | `src/knowledge/context.ts` | rules captured in `architecture.md` §6; original implementation |
| Auto-RAG gates and budgets | `src/tool-knowledge/index.ts` | constants captured in inventory §15; original implementation |
| FTS query compilation (trigram terms, LIKE fallback) | `src/knowledge/chunkdb.ts` | behavior parity via golden tests |
| OCR full-page rendering, embedded-raster fallback, preprocessing, and raster ceilings | `src/knowledge/ocr.ts`, `src/knowledge/ocr-worker.ts` | independent Go PDF/parser implementation plus Knowledge-managed PDF.js/Tesseract runtime; no upstream source copied |
| Local embedding and reranking | `src/knowledge/embed.ts`, `src/knowledge/rerank-adapter.ts` | independent managed Transformers.js/ONNX runtime using pinned public model artifacts; no dsh runtime source copied |
| Legacy Office parsing | `src/knowledge/parse.ts` and dsh anydoc dependency behavior | independent integration with the MIT `@firecrawl/anydoc` package; no AGPL source copied |
| Extension contract usage | shutu-agent `sdk/extension`, `examples/extension` | public API usage per `docs/agent_contract_mapping.md` |

## Reference baselines (audit evidence)

| Repository | Commit | Worktree state at audit |
|---|---|---|
| shutu-agent (`C:\dev-projects\Agent\shutu-agent`) | `60730c671d30e30eb910b92a69c621ce9fecfdf0` (`v0.2.1`) | untracked files only: `.codegraph/`, `new-logo-b.png`, `new-logo-w.png`, `web/.npm-cache/`, `web/tmp-native-workspace.png` (pre-existing, user-owned; untouched by this project) |
| dsh-knowledge (`C:\dev-projects\dsh\dsh-knowledge`) | `95e4a135cca3282b345c6d12a8e09cc4a314402f` | clean |

Both baselines are re-checked at every phase boundary (see `Agent.md` §3).

The release dependency is the public module
`github.com/shutu-ai/shutu-agent v0.2.1`, whose module distribution includes
the upstream Apache-2.0 `LICENSE`. No local replacement, workspace injection,
symlink, or Agent source is used.
