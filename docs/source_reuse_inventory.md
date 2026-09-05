# Source Reuse Inventory

Policy: behavior-level reimplementation only. dsh-knowledge is AGPL-3.0; this project does not copy or translate its source. Reading, running, and describing behavior are permitted; copying requires the AGPL checklist (license propagation, attribution, corresponding source) and a license decision, neither of which has been made.

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
| Extension contract usage | shutu-agent `sdk/extension`, `examples/extension` | public API usage per `docs/agent_contract_mapping.md` |

## Reference baselines (audit evidence)

| Repository | Commit | Worktree state at audit |
|---|---|---|
| shutu-agent (`C:\dev-projects\Agent\shutu-agent`) | `8701c2adbc00b0af8c5fba5272daaf28a7327e92` | untracked files only: `.codegraph/`, `new-logo-b.png`, `new-logo-w.png`, `web/tmp-native-workspace.png` (pre-existing, user-owned; untouched by this project) |
| dsh-knowledge (`C:\dev-projects\dsh\dsh-knowledge`) | `95e4a135cca3282b345c6d12a8e09cc4a314402f` | clean |

Both baselines are re-checked at every phase boundary (see `Agent.md` §3).
