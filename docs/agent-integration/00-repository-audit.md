# Repository Audit — Agent/MCP/Skills Full Implementation

- Audit date: 2026-10-09 (Africa/Cairo)
- Plan: `Shutu-Knowledge_Agent_MCP_Skills_Full_Implementation_Plan.md`
- Phase: **0 / G0**
- Rule followed: no production architecture change was made for this audit.

## 1. Repository identity

| Item | Evidence at audit time |
|---|---|
| Branch | `master`, even with `origin/master` |
| HEAD | `acd1d705347347201acb13e3814b978360343a34` |
| Subject | `fix(knowledge): sync directory status and improve semantic memory UX` |
| Dirty tracked files | none |
| Untracked local artifacts | `Shutu-Knowledge_Agent_MCP_Skills_Full_Implementation_Plan.md`, `p1.png`, two local Chinese report Markdown files |
| Runtime version | `0.7.0` |
| Storage contract | format `2`; reader/writer `9`; minimum reader/writer `9` |
| Latest tracked migration | `internal/storage/migrations/0022_schema_knowledge.sql` |
| Go module | `github.com/shutu-ai/shutu-knowledge`, Go `1.25.0` |
| Agent dependency | `github.com/shutu-ai/shutu-agent v0.2.1` |

The local runtime was built from this source but had empty `gitCommit` metadata; therefore source SHA, not runtime metadata, is authoritative.

## 2. Process entry points

| Mode | Function / path | Current behavior |
|---|---|---|
| HTTP server | `cmd/shutu-knowledge/main.go` → `cmdServe` → `app.NewWithOptions` → `web.New` | Standalone local HTTP + embedded web UI; background recovery/maintenance |
| Agent extension | `cmd/shutu-knowledge/main.go` → `cmdExtension` → `internal/extension.Run` | Shutu-Agent Extension Protocol v1 over stdio; starts ephemeral local web server |
| Diagnostics | `cmdDoctor` | Storage/migration/runtime diagnostics |
| Version | `cmdVersion` | Build/version JSON |

There is no MCP subcommand and no MCP SDK dependency in `go.mod` / `go.sum`.

## 3. Architecture and ownership

`internal/app.App` is the shared application assembly:

- `App.Knowledge` is `*knowledge.Service` (`internal/app/app.go:34-112`).
- `App.Operations` is the durable operation service.
- REST uses `internal/web` and calls Knowledge/Operations directly.
- Extension uses `internal/extension` and also calls Knowledge/Operations directly.
- The Agent public SDK import is confined to `internal/extension` by the CI architecture gate.

This matches the existing one-way boundary: Knowledge Core is Agent-agnostic; the adapter depends on `github.com/shutu-ai/shutu-agent/sdk/extension`.

## 4. Query entry-point call graph

### REST evidence search

```text
POST /api/search
  internal/web/api.go: Server.search
  → Knowledge.Search(ctx, knowledge.SearchRequest)
  → SaveSearchHistoryContext (Recall Test only)
  → SearchResult + SearchHit citations
```

`Search` is implemented in `internal/knowledge/search.go:321`. Supported request fields include `Query`, `Queries`, `BaseID`, `BaseIDs`, `TopK`, `Mode`, `Threshold`, `MMR`, `Debug`, and `Filter` (`internal/knowledge/types.go` and `search.go`).

Modes resolved by `Service.resolveMode` (`search.go:981`):

| Requested | Actual behavior |
|---|---|
| `lexical` | lexical/BM25 lane |
| `vector` | vector lane if embedder active; otherwise degrades to lexical |
| `hybrid` | lexical + vector fusion, optional rerank/MMR |
| empty / `auto` | becomes `hybrid` when vector available, otherwise `lexical` |

Important: current `auto` is evidence-mode auto-selection, not the plan's semantic/evidence/hybrid route planner.

### Semantic memory and context

Core functions in `internal/knowledge/semantic_memory.go`:

- `GetSemanticCompilation`
- `CompileSemanticMemory`
- `ResolveSemanticUnitEvidence`
- `SearchSemanticMemory`
- `CompileKnowledgeContext`
- `PlanKnowledgeQuery`
- `GetSemanticWiki`

REST routes are registered in `internal/web/api.go:102-106`:

- `POST /api/bases/{id}/semantic/compile`
- `GET  /api/bases/{id}/semantic`
- `POST /api/bases/{id}/semantic/search`
- `POST /api/bases/{id}/semantic/context`
- `GET  /api/bases/{id}/semantic/wiki`
- `GET  /api/semantic/units/{id}/evidence`

### Structured data

Implemented separately from semantic memory in:

- `internal/knowledge/schema.go`
- `internal/knowledge/schema_api.go`
- `internal/schemamodel/*`
- REST routes `internal/web/api.go:60,93-97`

Capabilities include immutable logical models, fields, enums, candidate keys, business concepts, deterministic schema search, and natural-language requirement resolution. They are not exposed through Extension tools nor merged into `Knowledge.Search` output.

### Agent automatic retrieval

`internal/extension/context.go` implements `ProvideContext`:

- Honors enabled scope and auto-retrieve configuration.
- Builds a bounded lexical evidence query from user input and short history.
- Applies score/relevance gates, per-base seat limits, deduplication, and token budget.
- Marks injected content as untrusted reference material.
- It does **not** use semantic compilation or context compilation.

## 5. Existing semantic model

Authoritative source: `internal/semantic/model.go`, `compiler.go`, `storage.go`, `search.go`, `context.go`, `temporal.go`.

Implemented unit types:

- `fact`
- `concept`
- `topic`
- `summary`
- `knowledge_page`

Implemented relation types include `derived_from`, `part_of`, `mentions`, `related_to`, `supports`, `contradicts`, `supersedes`, and `same_topic`.

Model invariants:

- Deterministic unit/relation IDs scoped by base + immutable generation.
- Lifecycle status: active / superseded / conflicted / deleted.
- Temporal validity fields and successor identity.
- Required exact evidence pointer or explicit `derived_from` closure.
- Cycles, duplicate canonical identities, invalid confidence, and invalid provenance are rejected.

Compiler identity in `internal/semantic/compiler.go`:

```text
shutu-builtin
0.6.0-range-history
model=deterministic
prompt=none
```

Compilation source selection (`CompileSemanticMemory`, `semantic_memory.go:44`):

- First compile: all ready documents with non-empty IR.
- Later compile: dirty/deleted documents from durable queue plus fingerprint comparison.
- No changes: return previous compilation.
- Evidence Index is not rebuilt.
- New compilation is validated, written, activated, then queue changes resolved.

## 6. Time/version and context behavior

`CompileKnowledgeContext` (`semantic_memory.go:280`) currently:

1. Requires a base and active semantic compilation.
2. Plans deterministic query intent via `semantic.RouteQuery`.
3. Activates semantic memory, concept/topic titles, and facts.
4. Expands bounded query variants.
5. Runs existing evidence search in `hybrid` mode.
6. Adds targeted evidence for versions or historical range buckets.
7. Assembles `semantic.CompileContext` under a token budget.

Implemented time/version features:

- Current-version intent and comparable release resolution.
- Explicit version evidence targeting.
- Evolution / compare-version intent.
- Historical range representatives from provenanced knowledge units.
- Supersession/conflict statuses in the semantic model.

Not yet implemented at plan level:

- Unified `document_version_policy` request field.
- Single-snapshot generation pinning across all query tools.
- General typed relation traversal with direction/depth/max-nodes options.
- Contradiction-aware ranked response type.
- Unified semantic/evidence/hybrid/auto application contract.

## 7. REST API inventory

Main current route families in `internal/web/api.go` and `internal/web/operations.go`:

| Area | Representative routes |
|---|---|
| Bases | list/create/read/update/delete/restore/stats/groups |
| Documents | list/tree/read/update/delete/reindex/context/raw/structure/understanding |
| Import | text, files, URL, backend directory, durable operations/uploads |
| Directory lifecycle | rescan, repoint, delete tree |
| Retrieval | `POST /api/search`, history, chunk context |
| Semantic memory | compile/get/search/context/wiki/unit evidence |
| Structured data | compile document/base, search, requirements, table/field lookup |
| Models/runtime | embedding/rerank/caption probes, OCR, local models, Ollama |
| Operations | submit/list/get/cancel/retry/events, uploads, operation keys |
| Ops | health, version, status, metrics, scope, config, maintenance |

The standalone HTTP server has no authentication layer. Default config binds localhost, but explicit public binding is a Phase 5 security concern, not a completed capability.

## 8. Existing Agent Extension inventory

Source: `extension.yaml`, `internal/extension/extension.go`, `manifest.go`, `tools.go`.

Extension Protocol v1 capabilities:

- stdio transport
- tools
- context provider
- web contribution
- lifecycle
- health
- permissions: session ID, session turn, user input

19 registered tools:

1. `knowledge_search`
2. `knowledge_compile_context`
3. `knowledge_list_bases`
4. `knowledge_create_base`
5. `knowledge_delete_base`
6. `knowledge_add_document`
7. `knowledge_list_documents`
8. `knowledge_delete_document`
9. `knowledge_import_url`
10. `knowledge_refresh_url`
11. `knowledge_stats`
12. `knowledge_get_document`
13. `knowledge_read_document`
14. `knowledge_reindex_document`
15. `knowledge_reindex_base`
16. `knowledge_maintenance_storage`
17. `knowledge_operation_status`
18. `knowledge_operation_cancel`
19. `knowledge_operation_retry`

These are Extension Protocol tools, not MCP tools. There is no evidence of real MCP client testing because no MCP server/SDK exists yet.

## 9. Required-by-plan MCP tool gap matrix

| Planned tool | Current equivalent | Status |
|---|---|---|
| `knowledge_list_bases` | Extension `knowledge_list_bases`; REST list bases | PARTIAL — no unified MCP envelope/capability view |
| `knowledge_capabilities` | none | MISSING |
| `knowledge_search` | `Knowledge.Search` + REST + Extension | PARTIAL — evidence only; no semantic/hybrid/auto application contract |
| `knowledge_explore` | separate semantic/schema APIs | MISSING as unified tool |
| `knowledge_expand` | semantic derived-from closure internally | MISSING as controlled traversal tool |
| `knowledge_evidence` | `ResolveSemanticUnitEvidence`, document context | PARTIAL — no unified scoped tool contract |
| `knowledge_context` | `CompileKnowledgeContext` + Extension tool | PARTIAL — no generation pinning or unified envelope |
| `knowledge_compare_versions` | historical/version features inside context compiler | MISSING as explicit tool |
| `knowledge_diagnostics` | search debug diagnostics + health/status | MISSING as unified tool |

MCP Resources/Prompts: MISSING.  
MCP stdio: MISSING.  
MCP streamable HTTP: MISSING.  
MCP SDK: not selected/installed.

## 10. Capability matrix

| Capability | Status | Key evidence |
|---|---|---|
| Base/document lifecycle | IMPLEMENTED | REST + Extension + durable operations |
| Parsing / OCR / captions | IMPLEMENTED | parser and knowledge service plus model/runtime routes |
| Chunking / Document IR | IMPLEMENTED | `internal/chunk`, `internal/documentir`, `service_doc.go` |
| Evidence retrieval | IMPLEMENTED | lexical/vector/hybrid/rerank/MMR in `search.go` |
| Evidence provenance | IMPLEMENTED | generation/source version/citation fields |
| Semantic compilation | IMPLEMENTED | `semantic_memory.go`, `internal/semantic` |
| Semantic retrieval | IMPLEMENTED | `SearchSemanticMemory`, REST |
| Bounded semantic context | IMPLEMENTED | `CompileKnowledgeContext`, REST/Extension |
| Time/version reasoning | PARTIAL | works inside semantic context, missing unified request contract |
| Structured data | PARTIAL | REST-only compile/search/resolve; not unified with query/tools |
| Typed relation traversal | PARTIAL/MISSING | internal closure exists, no bounded explore/expand API |
| Unified query application API | MISSING | REST and Extension duplicate DTO mapping |
| Four explicit query modes | PARTIAL/MISSING | evidence modes implemented; semantic/hybrid/auto plan contract missing |
| MCP server | MISSING | no SDK, transport, tools, resources, conformance tests |
| Portable Agent Skill | MISSING | no `skills/` package in repository |
| Evaluation harness / 200Q gold set | MISSING | no plan-specific harness/gold set in tracked source |
| Formal packaging | PARTIAL | Windows packaging/release acceptance exists for existing product; no MCP/Skill installation flow |
| Authentication for HTTP | MISSING | standalone API has no auth; localhost default only |
| Cross-base authorization | PARTIAL | enabled scope exists; unified base authorization not centralized |
| Windows packaging | PARTIAL | release packaging/native host jobs exist in CI |

## 11. Tests and current baseline

Existing test layers observed:

- Go unit/integration tests throughout `internal/*`, `cmd/*`.
- Extension adapter tests: `internal/extension/extension_test.go`, `gates_test.go`, `auto_policy_test.go`.
- Semantic/compiler tests: `internal/semantic/*_test.go`.
- Retrieval/golden tests: `internal/knowledge/*_test.go`.
- Browser E2E: `web/scripts/e2e.mjs`.
- CI: architecture gates, web build/typecheck/test, Go build/vet/test/race/benchmark smoke/browser E2E; manual/runtime packaging jobs.

Commands executed at audited HEAD:

```powershell
$env:GOCACHE='C:\dev-projects\shutu-knowledge\.gocache'
go test ./... -count=1
npm run test
```

Result recorded in `docs/agent-integration/baseline-2026-10-09.md`.

## 12. Historical documentation discrepancies

| Historical claim | Current source reality | Handling |
|---|---|---|
| README highlights stable `v0.3.1` and optional 0.4 | Runtime/source version is `0.7.0`; migrations through structured schema knowledge exist | Treat source/HEAD as truth; update README only in a later docs phase |
| Agent.md says Extension integration phases complete | Extension v1 tools/context are implemented, but no MCP | Preserve Extension; add MCP as an additional adapter, not replacement |
| Plan/task docs mention semantic compilation | Semantic compiler exists and is deterministic/offline | Reuse; do not create another knowledge domain |
| Structured model documents indicate 0.7 capability | REST model exists, but not exposed to Agent/MCP or unified query | Include in adapter/query design |

## 13. Constraints and non-negotiables for implementation

1. Keep Evidence Layer authoritative; semantic hits must resolve to exact evidence.
2. Keep Knowledge Core free of MCP/Agent DTO dependencies.
3. MCP and Extension adapters must be independent clients over shared application services.
4. Do not rewrite existing SQLite business data or evidence indexes.
5. Maintain existing REST and Extension behavior.
6. Introduce additive migrations only, starting at `0023` when needed.
7. Prefer a maintained Go MCP SDK; do not hand-roll an incompatible protocol.
8. Default deny/least privilege; do not implicitly scan all bases.
9. Preserve cancellation, timeout, model admission, and durable operation behavior.
10. Every completed MCP tool requires a real client/real transport test before claiming done.

## 14. G0 assessment

**Status: COMPLETE.**

The source audit and full current-source regression baseline are recorded in `baseline-2026-10-09.md`. The baseline includes normal Go tests, race tests, Go build/vet, web contract/API tests, typecheck, and real Chrome/CDP browser E2E at the frozen HEAD.
