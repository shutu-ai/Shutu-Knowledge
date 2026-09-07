# Shutu-Knowledge Final Runtime Parity Release Gate

Status:

```text
SHUTU-KNOWLEDGE OUT-OF-BOX PARITY READY
SHUTU-KNOWLEDGE OFFICIAL RELEASE COMPLETE
```

## Final candidate and remote gates

| Gate | Result |
|---|---|
| Runtime implementation candidate | `369c3436045165198e8360edb36693f01c0201da` |
| Final release commit | `e63b1f4825de81d52fc9c0a280cf24c9931a79b0` |
| Earlier candidate CI | run `34093542980`, success, implementation candidate |
| Final release ordinary CI | run `34114559387`, success, final release commit |
| Final release runtime-release CI | run `34115126042`, success; `runtime-release` and `build` jobs both passed |
| GitHub source | `https://github.com/shutu-ai/Shutu-Knowledge/tree/v0.1.1` |

The earlier runtime-release workflows for the implementation candidate and the
docs-only gate were runs `34097221413` and `34097927780`. The final release
runtime-release workflow was triggered by the immutable pushed tag `v0.1.1`; it
ran the real managed runtime smoke and the offline managed-runtime restart
smoke on a GitHub Linux runner.

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

## Formal package and official Release verification

The original `0.1.0` package matched its recorded archive SHA-256, but its
package-internal `checksums.sha256` entries referenced a truncated temporary-
clone path rather than package-root relative paths. It was rejected and was
not released. The packaging metadata bug was fixed before the official release.

```text
Release version: 0.1.1
Release tag: v0.1.1 (annotated, immutable)
Release commit: e63b1f4825de81d52fc9c0a280cf24c9931a79b0
GitHub Release: https://github.com/shutu-ai/Shutu-Knowledge/releases/tag/v0.1.1
Package: shutu-knowledge-0.1.1-windows-amd64.zip
Size: 11,707,791 bytes
SHA256: 4d0ea1abd7556d3ec9ef2403416679150abc220d7e36a7b1320f47dd314d7fc6
Binary SHA256: 21aa31f0e1409df91ffb593736fea83039dbe829d7324ac216b940a46518548f
Metadata GitSHA: e63b1f4825de81d52fc9c0a280cf24c9931a79b0
```

The corrected package metadata implementation resolves the repository and
output roots before generating checksums. The final package's 12 checksum
entries were verified after local build and again after downloading the ZIP
from GitHub Release. The remote asset size and SHA-256 match exactly.

Package inspection passed: required binary, license, notices, manifest,
checksums, deployment/runtime documentation, and extension metadata were
present; source checkout files, `.git`, `node_modules`, model caches, runtime
caches, npm caches, temporary homes, absolute development paths, and secrets
were not present.

Release assets uploaded and verified:

- `shutu-knowledge-0.1.1-windows-amd64.zip`
- `BUILD-METADATA.json`
- `checksums.sha256`
- `runtime-manifest.json`

Remote package smoke passed from the downloaded GitHub Release ZIP after
extraction to a new directory and data home: version `0.1.1`, doctor/init,
startup, runtime discovery, and `/api/status` all passed.

The previously validated package-only Runtime E2E passed from a new
package/data home. It verified:

- Qwen embedding vectors and 1024-dimensional vector stats;
- semantic retrieval and real BGE reranker loading/application;
- real OCR image import and vector retrieval;
- real `.doc`, `.ppt`, and `.xls` import with non-empty extracted content;
- runtime status, including managed embedding/OCR/PDF/Office capabilities;
- offline process restart and vector retrieval;
- JPX codec fixture presence, with direct PDF.js codec rendering covered by the
  direct runtime gate.

Final status:

```text
SHUTU-KNOWLEDGE OFFICIAL RELEASE COMPLETE
```

The immutable release tag and its package point to `e63b1f4`. This report is a
post-release documentation follow-up on `master`; it does not move the tag or
change any release asset.

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

The validated runtime implementation candidate contains only runtime-gate fixes
within Knowledge:

- first embedding/rerank inference and model health probes receive an explicit
  cancellable model-load budget instead of the ordinary request budget;
- reusable embedding vectors are materialized on the destination document,
  preserving cross-document hash reuse without creating vectorless ready rows;
- the package-only smoke asserts vector materialization rather than accepting a
  lexical-only false positive.

No retrieval redesign, new business capability, parser redesign, or Agent
change was introduced after the validated runtime candidate. The final release
commit additionally contains only version synchronization, packaging metadata
path correction, and release evidence updates.

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
