# Out-of-Box Behavioral Parity Audit

Audit date: 2026-09-06

## Fixed reference

| Item | Value |
|---|---|
| Reference repository | `https://github.com/Soren-ABT/dsh-knowledge` |
| Reference version | `v0.3.9` |
| Reference commit | `95e4a135cca3282b345c6d12a8e09cc4a314402f` |
| Target repository | `shutu-ai/Shutu-Knowledge` |

The reference commit was verified with `git -C C:\dev-projects\dsh\dsh-knowledge
rev-parse HEAD` and remains the sole upstream basis for this audit. Existing
V1 capability and release reports are not replaced; this document adds the
stricter installation-and-runtime question.

## State definitions

- `BUILT_IN`: the Knowledge binary can perform the operation without another
  runtime.
- `BUNDLED_RUNTIME`: the release artifact contains the runtime and its required
  files.
- `AUTO_MANAGED_EXTERNAL_RUNTIME`: Knowledge discovers, installs, validates,
  starts, health-checks, and upgrades the runtime without a user-authored
  helper or command line.
- `MANUAL_EXTERNAL_RUNTIME`: the operator must install or configure a helper,
  executable, or service. This is not out-of-box complete.
- `BLOCKED_BY_AGENT`: the public Agent contract prevents exact behavior and
  the Knowledge project cannot safely bypass it.
- `NOT_APPLICABLE`: the behavior is outside the target capability scope.
- `FAIL`: the required behavior is not available in a fresh installation.

`READY` is reserved for a model whose artifact checksum/version is valid, whose
runtime is available, whose model has loaded successfully, and whose minimum
inference smoke test passes. Artifact presence alone is `INSTALLED`.

## Reference behavior versus current implementation

The pinned dsh release ships its local embedding and reranking through
transformers.js and `onnxruntime-node`, uses the Qwen3 embedding model and the
BGE reranker, renders PDF pages through its MuPDF integration, runs PaddleOCR
with Tesseract fallback, and parses legacy OLE Office formats through the
Anydoc dependency. These facts are evidenced by the pinned repository's
`package.json`, `src/knowledge/embed.ts`, `src/knowledge/ocr.ts`,
`src/knowledge/ocr-worker.ts`, and `src/knowledge/parse.ts`.

Shutu-Knowledge now ships an independently implemented managed runtime
bootstrap under `internal/runtime/assets`. It installs a pinned Node package
lock on first use, verifies fixed model revisions and file checksums, and
supervises real Transformers.js/ONNX, Tesseract.js, PDF.js/canvas, and Anydoc
operations. Explicit helper commands remain supported as deployment overrides;
they are not required by the default path. `internal/models` still reports a
downloaded artifact as `INSTALLED` until the managed runtime completes its
load and inference smoke test.

## Environment evidence

The audit machine has `python.exe`, `node.exe`, `npm`, and `ffmpeg` discoverable
on `PATH`. It has no discoverable `ollama`, `tesseract`, `soffice`/
`libreoffice`, `mutool`, or `pdftoppm`. The checkout contains no model weights;
the Go binary embeds the managed runtime bootstrap, lockfile, and manifest.
The audit run exercised the managed cache and real Go-to-Node model, OCR, and
PDF calls. Real legacy-format and JBIG2/JPX fixture certification is included
in the completed runtime smoke evidence.

## Findings

1. Local embedding and local reranking are `AUTO_MANAGED_EXTERNAL_RUNTIME`:
   the default path downloads fixed revisions, verifies files, loads the real
   models, and performs vector/score smoke tests through a supervised process.
2. OCR is `AUTO_MANAGED_EXTERNAL_RUNTIME`: Tesseract.js and its language data
   are loaded by the managed process, with per-document errors preserved.
3. Full-page PDF rendering and the real JBIG2/JPX fixtures pass through PDF.js
   and canvas as `AUTO_MANAGED_EXTERNAL_RUNTIME`.
4. `.doc`, `.ppt`, and `.xls` pass through the managed MIT Anydoc package, with
   LibreOffice discovery retained as an explicit fallback.
5. Model download → checksum verification → load → inference → cached restart
   is implemented and exercised for Qwen and BGE; corruption recovery passes.
6. Doctor exposes core health, runtime status, model lifecycle, versions, and
   errors. A first probe may install the embedded lockfile into the private
   runtime home; later probes reuse that cache.
7. GAP-001 remains `BLOCKED_BY_AGENT` and non-blocking. Extension Platform v1
   has no public system-prompt contribution channel; tool descriptions and
   context contributions remain the legal workaround. GAP-002 remains
   non-blocking for the same public-contract reason.

## Audit conclusion

The current product remains V1 Release Ready under its existing release
contract. Runtime delivery and management are implemented and the requested
parity certification passes. The remaining optional MinerU and Agent prompt
channel gaps are not runtime blockers.
