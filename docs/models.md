# Models

Knowledge owns its model cache and model runtime configuration inside the
Knowledge data domain. The Agent never stores model state or launches model
processes.

## Local ML runtime

By default, Knowledge prepares a private managed Node.js runtime with
Transformers.js and ONNX Runtime. It supervises lifecycle, readiness, requests,
timeouts, and restarts; model weights are downloaded into the Knowledge data
domain and validated before real inference. A deployment can still override
the managed process with explicit capability helper commands.

Configuration is under `runtime`:

```yaml
runtime:
  helperCommand: "shutu-ml-helper --models ~/.shutu/knowledge/models"
  embeddingHelper: ""
  rerankHelper: ""
  ocrHelper: ""
  startupTimeoutMs: 10000
  requestTimeoutMs: 60000
  idleTimeoutMs: 300000
```

The capability-specific fields override `helperCommand`; when all are empty,
Knowledge uses the embedded managed runtime automatically. The environment
variables `SHUTU_KNOWLEDGE_EMBEDDING_HELPER`,
`SHUTU_KNOWLEDGE_RERANK_HELPER`, and `SHUTU_KNOWLEDGE_OCR_HELPER` can override
the typed config for container deployments.

A local reranker model may be configured with the `local:owner/model` form.
Knowledge strips that prefix before calling the isolated helper; a remote
endpoint is ignored for this form. Custom Hugging Face IDs are validated as an
`owner/model` pair and registered in Knowledge-owned state.

OCR can have a second, independent command through `ocr.fallbackHelper` (or
`SHUTU_KNOWLEDGE_OCR_FALLBACK_HELPER`). The primary path is the isolated OCR
runtime/helper above; the secondary command runs only after that primary path
fails. The managed primary uses Tesseract.js and `eng+chi_sim` language data.

OCR can also prefer a separate `ocr.renderHelper` (or
`SHUTU_KNOWLEDGE_OCR_RENDER_HELPER`) full-page PDF renderer before calling the
inference runtime. When it is empty, the managed PDF.js renderer is used. Both
paths are bounded, health-checked, and fail-closed.

## Wire contract

The helper uses line-delimited JSON over stdin and stdout and speaks protocol
version `1`. The first exchange is:

```json
{"id":1,"method":"initialize","params":{"protocol":1}}
{"id":1,"result":{"protocol":1,"version":"1.0.0","capabilities":["embedding","rerank","ocr"]}}
```

Each request has the capability name as its method and a JSON object as its
parameters. Responses use `result` or an `error` object with `code` and
`message`.

Embedding:

```json
{"id":2,"method":"embedding","params":{"model":"qwen3","texts":["hello"]}}
{"id":2,"result":{"vectors":[[0.1,0.2]]}}
```

Knowledge normalizes vectors and rejects mismatched dimensions or non-finite
values.

Reranking:

```json
{"id":3,"method":"rerank","params":{"model":"bge","query":"q","documents":["a","b"]}}
{"id":3,"result":{"scores":[0.8,0.2]}}
```

Scores must be finite and in the inclusive range `[0,1]`. Local rerank failures
use the same circuit breaker and lexical/original-order degradation as remote
rerank.

OCR:

```json
{"id":4,"method":"ocr","params":{"format":"pdf","data":"<base64>","modelPath":"<absolute-model-dir>"}}
{"id":4,"result":{"text":"recognized text"}}
```

`format` is normally `pdf`. When PDF-envelope OCR fails, Knowledge can retry
the bounded embedded page rasters individually with `format:"png"`; helpers
must honor the format hint and reject inputs they cannot read.
When `ocr.renderHelper` is configured, Knowledge first renders complete pages
and sends each bounded PNG with `format:"png"` to the same OCR runtime. Renderer
or validation failures fall back to PDF-envelope OCR and then embedded rasters.
Knowledge prepares these fallback rasters with low-resolution upscale,
grayscale conversion, contrast stretch, and sharpening; recognizer output is
grouped by page and CJK horizontal-space artifacts are folded.

Health uses method `health` with `{"capability":"embedding"}` (or the other
capability names) and must return `{"ready":true}` plus the loaded model when
ready. `GET /api/runtime-status` probes every configured helper and reports
failures; unconfigured helpers are omitted from that low-level runtime map,
while `shutu-knowledge doctor` exposes them as actionable degraded checks.

Knowledge never treats an installed model artifact as ready by itself. The
model API reports a complete artifact set as `INSTALLED` with `ready: false`.
`READY` additionally requires a successful managed-runtime load and inference
smoke test. Explicit helper commands remain supported as deployment overrides,
but are not required by the default local model path.

## OCR Artifact Lifecycle

The Models screen manages the PaddleOCR PP-OCRv5 mobile bundle separately from
the generic embedding/rerank download form. It downloads the detector ONNX,
recognizer ONNX, and recognizer `inference.yml`, validates the latter as a
CJK character dictionary with more than 1,000 entries, writes the dictionary in
PaddleOCR format, and publishes all three files as one manifest under
`PaddlePaddle/PP-OCRv5-mobile`.

The dedicated API is `GET /api/ocr/model`,
`POST /api/ocr/model/download`, and `POST /api/ocr/model/remove`. Download and
removal are serialized with the shared model-cache manager. Artifact presence
does not mark OCR usable: the screen also reports whether an OCR helper is
configured and healthy, and ingestion selects the runtime helper only when both
the bundle and helper are available. A deployment-provided `ocr.helper` command
can remain self-contained and is not required to use the shared bundle.
An additional `ocr.fallbackHelper` command can cover helper-runtime failures,
but it is likewise supplied by the deployment rather than bundled with
Knowledge.
The managed PDF.js renderer rasterizes vector-only PDFs before inference by
default. An `ocr.renderHelper` command remains an optional override and never
changes artifact presence into OCR readiness.

## Custom Reranker Registration And Self-Test

`POST /api/local-rerankers` records an experimental `owner/model` reranker in
Knowledge-owned state. Registration is intentionally separate from download,
artifact validation, and readiness. After its manifest and artifacts are
downloaded, the Models screen offers `Self-test`; the API is:

```text
POST /api/local-models/self-test {"id":"local:owner/model"}
```

The isolated helper scores one relevant and one irrelevant passage against a
query. Knowledge rejects malformed, out-of-range, non-finite, or
non-discriminating scores, then persists the latest result together with the
artifact size, artifact count, and download timestamp. If the artifact set
changes, the prior self-test becomes stale and must be rerun. A successful
self-test is a runtime health result, not proof of retrieval quality.

## Cache Migration

`GET /api/local-models/cache-migration?targetDir=<absolute-path>` returns a
plan without changing files. `POST /api/local-models/cache-migration` accepts
`targetDir` and `removeSource`. A migration requires no active downloads and
an empty target. It copies manifest-owned model trees, verifies every copied
file with SHA-256, and only then activates the target. Source removal is
opt-in.

The Models screen exposes this operation and shows the active cache directory.
Relative cache settings remain resolved under the Knowledge data home; a
migration target is resolved and persisted as an absolute path.
