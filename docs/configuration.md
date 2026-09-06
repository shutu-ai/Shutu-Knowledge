# Configuration

## Layers

Effective configuration is resolved in this order:

1. built-in defaults,
2. `<data home>/config.yaml`,
3. environment variables,
4. runtime global and per-base settings saved through the API,
5. per-base overrides used by the relevant request.

The data home defaults to `~/.shutu/knowledge`; set `SHUTU_KNOWLEDGE_HOME` to
override it. Unknown YAML fields fail startup instead of being ignored.
Numeric values and enums are normalized to safe ranges.

## File And Environment

Generate a commented baseline with:

```sh
shutu-knowledge doctor --init
```

Recognized environment variables are:

| Variable | Purpose |
|---|---|
| `SHUTU_KNOWLEDGE_HOME` | Knowledge data root |
| `SHUTU_KNOWLEDGE_LOG_LEVEL` | `debug`, `info`, `warn`, or `error` |
| `SHUTU_KNOWLEDGE_ADDR` | standalone HTTP address |
| `KNOWLEDGE_API_KEY` | global embedding API key |
| `KNOWLEDGE_RERANK_API_KEY` | global rerank API key |
| `HF_ENDPOINT` | Hugging Face-compatible endpoint |
| `SHUTU_KNOWLEDGE_RUNTIME_HELPER` | default optional ML helper command |
| `SHUTU_KNOWLEDGE_EMBEDDING_HELPER` | embedding-specific helper command |
| `SHUTU_KNOWLEDGE_RERANK_HELPER` | rerank-specific helper command |
| `SHUTU_KNOWLEDGE_OCR_HELPER` | OCR-specific helper command |
| `SHUTU_KNOWLEDGE_OCR_FALLBACK_HELPER` | secondary OCR command used after the primary OCR runtime/helper fails |
| `SHUTU_KNOWLEDGE_IMAGE_DECODER` | optional JBIG2/JPEG 2000 image decoder command |
| `SHUTU_KNOWLEDGE_CAPTION_API_KEY` | vision captioning API key for `openai` provider |

Standard `HTTP_PROXY`, `HTTPS_PROXY`, and `NO_PROXY` variables are honored by
the shared outbound HTTP client.

## Main Groups

| Group | Scope |
|---|---|
| `database` | optional database path relative to the data home |
| `logging` | structured log level |
| `server` | standalone loopback HTTP address |
| `embedding` | provider (`openai`, `ollama`, `local`, `none`), endpoint, model, batch size, secret |
| `rerank` | remote endpoint/model, timeout, enabled/required, queue limit, secret |
| `captioning` | PDF image caption provider (`off`, `openai`, or `ollama`), model, endpoint, secret |
| `chunking` | smart/delimiter/semantic strategies, size, overlap, threshold, token refinement |
| `retrieval` | mode, top-k, threshold, MMR, RRF vector weight, sibling windows, context deadline |
| `processing` | global document processor (`builtin` or `mineru`), MinerU host, MinerU secret |
| `workflow` | global same-name conflict strategy and URL refresh interval |
| `autoRetrieve` | global automatic-retrieval switch and default per-base weight |
| `jobs` | bounded import worker count and startup resume behavior |
| `models` | local artifact cache and Hugging Face endpoint |
| `runtime` | optional helper processes and their lifecycle deadlines |
| `ocr` | auto/forced/off mode, primary helper, secondary fallback helper, timeout |
| `helpers` | optional legacy office, PDF content-signature, and image decoder commands |
| `maintenance` | startup FTS optimize and threshold-based VACUUM |

## API Updates

`GET /api/config` and `PUT /api/config` manage the global runtime view without
a restart. Per-base settings are part of base create/patch requests. API keys
are write-only: responses report `*ApiKeySet`, an omitted key preserves the
stored value, and explicit clear flags remove it. Files containing secrets are
created with private permissions and doctor output reports secret variables
only as `<set>`.

Base settings resolve at use time. Empty processor, MinerU, conflict, refresh,
and auto-retrieve fields inherit the global values; explicit base values,
including automatic-retrieval off and weight `0`, override them. Processor and
MinerU settings apply to PDF imports; conflict strategy applies when a file
upload omits its request-level strategy; URL refresh applies to the background
refresh pass; automatic-retrieval settings apply to Agent context injection.

`captioning` is a global best-effort PDF enrichment setting. Supported OpenAI
compatible and Ollama vision responses are appended as searchable figure
descriptions; provider failures leave the parsed document unchanged. The API
key is write-only through the runtime API.

`helpers.contentConverter` is an optional PDF fallback command such as
`anydoc-helper {input} {format}`. For PDFs with no primary text it runs before
OCR; for fragmented native text it runs after OCR. Its nonempty stdout is
treated as Markdown; a missing or failing converter leaves the native
text/error behavior unchanged.

`helpers.legacyOffice` uses the same command convention for `.doc`, `.ppt`,
and `.xls`. It accepts quoted executables, is hot-reloaded by `/api/config`,
and fails closed while unavailable. Configure a deployment converter such as
`anydoc`, LibreOffice in headless mode, or another Markdown-capable service.

`ocr.renderHelper` is an optional deployment-provided full-page PDF renderer.
Knowledge writes the PDF to the `{input}` path, passes `pdf` through
`{format}`, and expects a bounded JSON/PNG response on stdout:

```json
{"pages":[{"page":1,"png":"<base64 PNG>"}]}
```

Pages must be ordered and uniquely numbered, each PNG is bounded to 32
megapixels, the response is bounded to 100 pages and 512 MB, and malformed
output falls back to PDF-envelope OCR followed by embedded-raster OCR. The
default `ocr.renderTimeoutMs` is 120000 ms and is normalized to the range
1000-600000 ms. `SHUTU_KNOWLEDGE_OCR_RENDER_HELPER` overrides the command for
container deployments, and a configured renderer is reported by the optional
`helper-ocr-render` health check.

`ocr.fallbackHelper` is an optional deployment-provided secondary OCR command,
typically a Tesseract wrapper. It uses the same `{input}` and `{format}`
command convention and is selected only when the primary isolated OCR runtime
or `ocr.helper` command fails or is unavailable. A configured fallback is also
reported by the optional `helper-ocr-fallback` health check. Knowledge does
not bundle OCR inference, a PDF renderer, or a Tesseract binary.

`helpers.imageDecoder` is an optional command for PDF image codecs that the
pure-Go baseline does not decode: JBIG2 and JPEG 2000. Knowledge writes a JSON
envelope to `{input}` containing base64 `format`, `data`, and optional JBIG2
`globals`, passes the format through `{format}`, and expects a bounded PNG on
stdout. For example: `image-decode-helper {input} {format}`. Missing helpers
fail closed and unsupported rasters remain best-effort skips; Knowledge does
not bundle a decoder.
