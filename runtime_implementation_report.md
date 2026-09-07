# Shutu-Knowledge Real Runtime Implementation Report

Audit date: 2026-09-06
Reference: dsh-knowledge `v0.3.9`, commit
`95e4a135cca3282b345c6d12a8e09cc4a314402f`

## Runtime inventory

| Runtime | Implementation | Packaging mode | Version | Platforms | Real test evidence | Fresh install result | Failure behavior |
|---|---|---|---|---|---|---|---|
| Local Embedding | `transformers.js` feature extraction over `onnxruntime-node`, Qwen3 last-token pooling and normalization | AUTO_MANAGED_EXTERNAL_RUNTIME; Node fallback and npm lock are embedded | Node 22.14.0; Transformers 4.2.0; model revision pinned | Windows amd64, Linux amd64 | Go supervisor returned Chinese/English/mixed batch, 1024 dimensions; semantic model load succeeded | Runtime package installs on first capability use; model downloads lazily | checksum/load/inference error becomes `FAILED`, never `READY` |
| Local Reranker | `AutoTokenizer` + `AutoModelForSequenceClassification`, raw single-logit sigmoid scores | AUTO_MANAGED_EXTERNAL_RUNTIME | Transformers 4.2.0; BGE revision pinned | Windows amd64, Linux amd64 | Three-candidate real smoke ordered relevant `0.99996` above two unrelated candidates near `0.000037`; ordering verified | Runtime package installs and model loads without a user helper | invalid logits, timeout, crash, or checksum failure is returned to the existing reranker fallback |
| OCR | Tesseract.js worker with `eng+chi_sim` language data | AUTO_MANAGED_EXTERNAL_RUNTIME | Tesseract.js 7.0.0 | Windows amd64, Linux amd64 | Real PNG, rotated, low-quality, and two-page scanned PDF OCR recognized/indexed `Knowledge Runtime OCR 7788` | Language data is fetched by the managed worker on first OCR | OCR failure remains isolated to the document and preserves native text where available |
| PDF full-page renderer | PDF.js legacy Node build plus `@napi-rs/canvas` PNG output | AUTO_MANAGED_EXTERNAL_RUNTIME | PDF.js 6.3.289; canvas 1.0.8 | Windows amd64, Linux amd64 | Real PDF produced one bounded PNG page through Go → supervisor → Node | npm runtime installs automatically; no renderer helper configuration | page count, pixel, response, and decode limits remain enforced in Go |
| JBIG2 / JPX | PDF.js decoder path used by managed full-page rendering | AUTO_MANAGED_EXTERNAL_RUNTIME | PDF.js 6.3.289 | Windows amd64, Linux amd64 | Real `JBIG2Globals.pdf` and `bug_jpx.pdf` fixtures rendered to validated PNG pages | No user helper is required when PDF.js supports the input | unsupported or malformed codec reports a visible parse/render failure |
| `.doc/.ppt/.xls` | MIT `@firecrawl/anydoc` native bindings | BUNDLED_RUNTIME inside the managed npm installation | 0.2.4 | Windows amd64, Linux amd64 | Real Apache POI `SampleDoc.doc`, `37625.ppt`, and `finance.xls` converted to non-empty Markdown | No converter script or LibreOffice configuration required | native conversion errors identify the format and isolate the document |

## Model lifecycle

The runtime owns the following persisted state in the Knowledge data domain:

```text
NOT_INSTALLED -> DOWNLOADING -> VERIFYING -> INSTALLED
             -> LOADING -> READY
             -> FAILED / RUNTIME_MISSING
```

`READY` is recorded only after the pinned artifact checksums, runtime load, and
real inference smoke succeed. The existing Go artifact manager continues to
report cache files separately; the managed runtime state is authoritative for
runtime readiness and survives a process restart.

## Environment

| Item | Evidence |
|---|---|
| Windows | Windows 11 Home Chinese edition, x86-64 |
| CPU | Intel Core Ultra 7 255H |
| RAM | 33,751,777,280 bytes reported |
| Go | go1.26.7 windows/amd64 |
| Node used for verification | v24.19.0; managed fallback is v22.14.0 |
| npm used for verification | 12.0.2 |
| Embedding model | Qwen3 Embedding 0.6B ONNX, q4, 1024 dimensions |
| Reranker model | BGE Reranker Base ONNX, single-logit cross encoder |
| OCR runtime | Tesseract.js 7.0.0, `eng+chi_sim` |
| PDF runtime | PDF.js 6.3.289 + napi-rs canvas 1.0.8 |
| Office runtime | anydoc 0.2.4 native package |

## Reproducible smoke commands

The checked-in `scripts/runtime_smoke.go` exercises the real Go supervisor and
managed Node process. With a prewarmed model cache and fixture directory, the
completed run used:

```text
go run ./scripts/runtime_smoke.go -home <clean-data-home> -model-cache <cache> \
  -ocr-image <ocr.png> -doc <SampleDoc.doc> -ppt <37625.ppt> -xls <finance.xls>
go run ./scripts/runtime_smoke.go -home <clean-data-home> -model-cache <cache> \
  -ocr-image <ocr.png> -knowledge-e2e -lifecycle
go run ./scripts/runtime_smoke.go -home <clean-data-home> -model-cache <cache> \
  -codec-pdf <JBIG2Globals.pdf>
go run ./scripts/runtime_smoke.go -home <clean-data-home> -model-cache <cache> \
  -codec-pdf <bug_jpx.pdf> -corruption-recovery
go run ./scripts/runtime_smoke.go -home <clean-data-home> -model-cache <cache> \
  -ocr-image <ocr.png> -offline-restart
```

The Office and codec samples are test fixtures, not runtime dependencies or
copied reference implementation source.

## Current release classification

The production runtime seams are now replaced by a Knowledge-managed runtime.
Real codec and legacy Office fixtures, corruption recovery, offline restart,
and clean data-home installation smoke have passed. Model weights and
Tesseract language data remain downloaded into the private cache rather than
committed to this repository. Windows real-runtime evidence is complete;
Linux execution is wired as a release-tag/manual CI gate in
`.github/workflows/ci.yml` and is not claimed by this local audit:

```text
SHUTU-KNOWLEDGE OUT-OF-BOX PARITY READY (AFTER runtime-release CI PASS)
```

The existing V1 release status remains independent and unchanged.
