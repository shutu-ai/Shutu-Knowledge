# Shutu-Knowledge Final Runtime Parity Release Gate

Status:

```text
SHUTU-KNOWLEDGE OUT-OF-BOX PARITY READY
```

## Final candidate and remote gates

| Gate | Result |
|---|---|
| Final runtime candidate commit | `369c3436045165198e8360edb36693f01c0201da` |
| Earlier candidate CI | run `34093542980`, success, candidate SHA above |
| Final release ordinary CI | run `34097716164`, success, final docs commit |
| Final release runtime-release CI | run `34097927780`, success; `runtime-release` and `build` jobs both passed |
| GitHub source | `https://github.com/shutu-ai/Shutu-Knowledge/tree/369c3436045165198e8360edb36693f01c0201da` |

The earlier runtime-release workflow for the implementation candidate was run
`34097221413`. The final release runtime-release workflow was triggered by the
pushed tag `runtime-gate-a871af6`; it ran the real managed runtime smoke and
the offline managed-runtime restart smoke on a GitHub Linux runner.

## Fresh-clone gate

Fresh clone:

```text
C:\Users\jabin\AppData\Local\Temp\shutu-knowledge-runtime-final-clone-217c02f8309041f383bd53a0c1e59158
```

The clone was obtained from GitHub `master` after the final candidate push and
resolved to `369c3436045165198e8360edb36693f01c0201da`.

- Windows fresh-clone `go test ./...`: PASS.
- Windows fresh-clone `go vet ./...` and `go build ./...`: PASS.
- Windows fresh-clone `npm ci`: PASS, 0 vulnerabilities.
- Windows fresh-clone Web contract, typecheck, and build: PASS.
- Linux fresh-clone `go test ./...`: PASS, including extension, Knowledge,
  runtime, and web packages.

## Formal package and package-only Runtime E2E

The formal package was built by `scripts/package_release.ps1` inside the fresh
GitHub clone from the final runtime candidate:

```text
Package: shutu-knowledge-0.1.0-windows-amd64.zip
Size: 11,571,335 bytes
SHA256: e7d4ee862acc213c2641cfbae018a819da2da4a13c9cc2b53f14b803051dfdae
Binary SHA256: ae55db20a2815d5fecf5cd26a3e646dff95bb24103537773410306c4ce3897ca
Metadata GitSHA: 369c3436045165198e8360edb36693f01c0201da
```

Package inspection passed: required binary, license, notices, manifest,
checksums, deployment/runtime documentation, and extension metadata were
present; source checkout files, `.git`, `node_modules`, model caches, runtime
caches, npm caches, temporary homes, and absolute development paths were not
present.

Package-only smoke passed from a new package/data home. It verified:

- Qwen embedding vectors and 1024-dimensional vector stats;
- semantic retrieval and real BGE reranker loading/application;
- real OCR image import and vector retrieval;
- real `.doc`, `.ppt`, and `.xls` import with non-empty extracted content;
- runtime status, including managed embedding/OCR/PDF/Office capabilities;
- offline process restart and vector retrieval;
- JPX codec fixture presence, with direct PDF.js codec rendering covered by the
  direct runtime gate.

## Windows direct Runtime evidence

The direct fresh-clone managed-runtime gate passed with the real Qwen and BGE
models, using the pinned revisions and checksum-verified cache:

- embedding: ready, model `onnx-community/Qwen3-Embedding-0.6B-ONNX`,
  dimension `1024`;
- embedding behavior: Chinese, English, mixed, long, empty, and whitespace
  cases passed;
- rerank: relevant score `0.999962...` ranked above unrelated scores;
- model lifecycle: removed, cache-invalidated, reinstalled, and re-smoked;
- corruption recovery: corrupt model became `FAILED` with Doctor-health
  evidence, then restored to ready;
- PDF: full-page render and malformed-PDF failure isolation passed;
- JPX PDF: one page rendered and decoded through PDF.js;
- OCR: normal, rotated, low-quality, and two-page scanned-PDF cases passed;
- legacy Office: DOC, PPT, and XLS each produced non-empty Markdown;
- Knowledge E2E: `documents=12`, `vector-dimension=1024`, semantic top
  `expense-reimbursement.md`, `rerank=applied`, `office=3`,
  `failure-isolated=true`;
- offline restart: embedding, reranker, and vector retrieval passed.

## Linux direct Runtime evidence

On Ubuntu 22.04 from the same fresh clone, the managed Linux runtime gate
passed with a fresh Linux runtime home and the real model cache:

- embedding and behavior batch: PASS, dimension `1024`;
- BGE rerank ordering: PASS;
- full-page PDF, malformed PDF, and JPX: PASS;
- normal/rotated/low-quality/multi-page OCR: PASS;
- DOC/PPT/XLS conversion: PASS;
- Knowledge E2E: `documents=12`, `vector-dimension=1024`,
  `semantic-top=expense-reimbursement.md`, `rerank=applied`,
  `office=3`, `failure-isolated=true`;
- same Linux runtime home offline restart: embedding, reranker, PDF, OCR,
  Office, and codec smoke passed.

## Implementation and failure-boundary fixes

The final candidate contains only runtime-gate fixes within Knowledge:

- first embedding/rerank inference and model health probes receive an explicit
  cancellable model-load budget instead of the ordinary request budget;
- reusable embedding vectors are materialized on the destination document,
  preserving cross-document hash reuse without creating vectorless ready rows;
- the package-only smoke asserts vector materialization rather than accepting a
  lexical-only false positive.

No retrieval redesign, new business capability, parser redesign, or Agent
change was introduced.

## Dependencies, provenance, and Agent boundary

- Managed Node is pinned to `22.14.0`; the embedded npm lock pins Transformers,
  ONNX Runtime, PDF.js, Tesseract.js, canvas, and Anydoc versions.
- Qwen and BGE model revisions and artifact SHA-256 values are recorded in
  `runtime-manifest.json`; weights are downloaded into the private Knowledge
  cache and are not redistributed by the package.
- OCR language data is checksum-verified and kept in the private runtime cache.
- `THIRD_PARTY_NOTICES.md` and `docs/runtime_license_inventory.md` record the
  Apache/MIT/BSD/Node/model/reference provenance boundaries. The AGPL-3.0
  dsh-knowledge repository remains a read-only behavioral reference; no source
  is copied or distributed.
- `C:\dev-projects\Agent\shutu-agent` remained at
  `60730c671d30e30eb910b92a69c621ce9fecfdf0` with no tracked changes. Its
  pre-existing untracked `.codegraph`, logo, npm-cache, and temporary image
  files were preserved.

## Remaining declared gaps

- Gap-001: Agent Extension v1 has no public `systemPrompt.section` prompt-
  guidance channel. This remains `BLOCKED_BY_AGENT / NON-BLOCKING`.
- Gap-002: Agent-side historical lifecycle/diagnostic parity remains a real
  `PARTIAL` boundary and was not weakened or hidden by the Knowledge runtime.

The final runtime parity gate is therefore ready for the stated out-of-box
Knowledge scope, with the two declared Agent-owned limitations preserved.
