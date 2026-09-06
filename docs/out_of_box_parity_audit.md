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

Shutu-Knowledge currently has original adapters and supervision contracts for
these areas, but no shipped inference engine, PDF page renderer, legacy Office
converter, or automatic runtime installer. `internal/runtime` starts only a
command explicitly supplied in Knowledge configuration. `internal/models`
downloads Hugging Face artifacts but previously treated a complete artifact
set as `ready`; this audit changes that representation to `INSTALLED` with
`ready: false` until a runtime-backed smoke test exists.

## Environment evidence

The audit machine has `python.exe`, `node.exe`, `npm`, and `ffmpeg` discoverable
on `PATH`. It has no discoverable `ollama`, `tesseract`, `soffice`/
`libreoffice`, `mutool`, or `pdftoppm`. No Knowledge runtime helper commands or
model cache are part of the repository checkout. Python and Node are not
counted as managed Knowledge runtimes because the product does not install or
configure the required model/OCR stack through them.

## Findings

1. Local embedding and local reranking are `MANUAL_EXTERNAL_RUNTIME`: the
   public path is a line-delimited JSON helper contract; no supported helper or
   bundled inference engine is shipped.
2. OCR is `MANUAL_EXTERNAL_RUNTIME`: the PaddleOCR artifact bundle can be
   downloaded, but recognition still requires a separately configured helper.
3. Full-page PDF rendering and heavyweight JBIG2/JPX decoding are
   `MANUAL_EXTERNAL_RUNTIME`: the built-in parser handles bounded supported
   image paths, while the full-page/optional-codec paths require configured
   helpers.
4. `.doc`, `.ppt`, and `.xls` are `MANUAL_EXTERNAL_RUNTIME`: the registered
   parser delegates to a configured converter and does not discover or install
   LibreOffice/Anydoc.
5. Model download is built into the product, but download → load → inference is
   not a complete lifecycle. It is therefore `FAIL` for out-of-box parity.
6. Doctor is built in and now exposes the artifact lifecycle and absent helper
   diagnostics, but it cannot make those runtimes available. The Doctor gate is
   `FAIL` for the required one-stop ready state.
7. GAP-001 remains `BLOCKED_BY_AGENT` and non-blocking. Extension Platform v1
   has no public system-prompt contribution channel; tool descriptions and
   context contributions remain the legal workaround. GAP-002 remains
   non-blocking for the same public-contract reason.

## Audit conclusion

The current product remains V1 Release Ready under its existing release
contract, but it is not Out-of-Box Behavioral Parity Ready. The missing pieces
are runtime delivery/management, not a Knowledge-to-Agent architecture gap.
