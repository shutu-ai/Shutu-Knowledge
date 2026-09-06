# Web Interface

## Architecture

The SPA is built independently under `web/` and embedded into the Go binary as
`internal/web/dist`. The same HTTP process serves:

- the Knowledge REST API under `/api/`,
- health and version endpoints,
- the bundled SPA, with unknown non-API paths falling back to `index.html`.

In extension mode the process starts on an ephemeral loopback port and reports
`webBaseUrl` through the Agent Extension handshake. Agent Web contributes the
extension navigation entry and reverse-proxies the business routes; Knowledge
owns only its own UI and API.

## Screens

| Route | Scope |
|---|---|
| Overview | corpus metrics and automatic-retrieval scope |
| Knowledge Bases | grouped bases, group create/rename/delete, base create/rename/delete |
| Documents | folder drilldown, status/progress/error details, text/chunk previews with chunk expand/collapse, rename/delete, single/batch reindex, URL refresh, directory rescan |
| Import | text, URL, supported-format multi-file upload with the 20-file preflight, and directory ingestion |
| Recall Test | retrieval execution, stage-level result inspection, citation copy, and replayable query history |
| Models | provider overrides, local HF artifact cache, dedicated PaddleOCR artifact lifecycle, runtime-helper state, and Ollama models |
| Settings | global runtime config (including processor/workflow/auto-retrieve, document helpers, image captioning, and secret state) and per-base overrides |

The sidebar exposes persistent English and Chinese UI selection. Route labels,
management forms, statuses, confirmation prompts, toasts, numeric and date
formats, and accessibility labels use the selected locale.

Recall supports a deep link in the form `#/recall?q=<query>`. A non-empty query
is placed in the form and executed automatically.

Each explicit Recall Test invocation is saved in Knowledge-owned SQLite history
with its base, query, mode, top-k, and MMR settings. The history panel replays
the full invocation and supports one-item deletion or clearing all entries.
Automatic RAG and Agent tool calls do not enter this Recall Test history. Each
hit has a citation-copy action, and the result header copies all citations in
result order.

## Retrieval Contract

`POST /api/search` returns a `SearchResult` with `hits` always represented as a
JSON array, including for empty/safe-zero results. Every hit carries:

- `score`, `lexicalScore`, `vectorScore`, `fusionScore`, and `rerankScore`
  when applicable,
- document/base/chunk identifiers and title,
- the composed `contextWindow`.

The result also reports search mode, elapsed time, and a structured rerank
status (`applied`, `not_needed`, or `degraded`) with candidate count and error
details.

## Model Management

The local artifact manager owns `~/.shutu/knowledge/models` by default and
never writes into Agent state. It supports:

- Hugging Face repository download with whole-percentage job progress,
- cancellation through the existing job manager,
- manifest-based readiness and artifact listing,
- safe removal with active-download protection,
- custom reranker registration and isolated helper self-test,
- dedicated PaddleOCR PP-OCRv5 mobile artifact download/validation/removal,
- SHA-256-verified cache migration with explicit source removal,
- configurable cache directory and HF endpoint.

Ollama lifecycle is exposed separately through `/api/ollama`: installed-model
listing, streamed pull progress and cancellation, and deletion. Browsing or
pulling an Ollama model does not change the configured embedding provider.

Global settings are persisted under the Knowledge data domain as
`config.yaml`. Configuration can be updated at runtime without a restart.
API keys are write-only through the web API: GET reports only whether a key is
set, while PUT preserves an existing key unless a new key is supplied or an
explicit clear flag is sent.

## Verification

Current evidence:

```text
web build:      npm run build
web contract:   npm test
web E2E:        npm run test:e2e
Go gates:       go build ./... && go vet ./... && go test ./...
```

Playwright checked Overview, Bases, Documents, Import, Recall, Models, and
Settings at 1440x900 and 390x844. All routes reported no horizontal overflow
and no console errors. `#/recall?q=retry%20budget` populated the query and
rendered `Final context - 1 hits` on both viewports.

`TestDocumentTreeAndGroupAPI` covers nested directory import, parent/child
summaries, chunk preview, group create/rename/delete, recursive tree deletion,
and exact deleted-row accounting. The Web build/contract suite verifies the
group, folder, preview, batch selection, supported-format filter, 20-file
preflight, and tree-deletion client paths. The Chrome/CDP E2E builds the real
backend and embedded SPA, creates a base, imports and indexes text, executes a
Recall Test, verifies replay history deletion, checks the dedicated OCR model
state, switches the complete shell to Chinese and back to English while checking
the Chinese viewport has no horizontal overflow, and deletes the base.
