# Agent Integration Architecture

Status: **Phase 1 foundation implemented; MCP adapter not installed yet.**

## Layering

```text
HTTP REST (internal/web)
Agent Extension v1 (internal/extension)
future MCP stdio / HTTP adapter
        ↓
Shared Agent Query Application (internal/agentquery)
        ↓
Knowledge Core (internal/knowledge)
Semantic Memory / Structured Models / Evidence Index
```

Rules:

1. Transport adapters never implement separate retrieval semantics.
2. `internal/agentquery` depends only on Knowledge Core types.
3. Knowledge Core does not import MCP, Agent, REST, or Extension DTOs.
4. Existing Agent Extension v1 remains supported; MCP is additive.
5. Evidence is authoritative. Semantic and structured lanes are separately labeled.
6. Semantic failures do not silently become evidence results.
7. All cross-base requests pass through one scope resolution routine.

## Current Phase 1 wiring

At HEAD, these paths already route through `internal/agentquery`:

| Surface | Operation | Status |
|---|---|---|
| REST `POST /api/search` | evidence search | Wired to `AgentQuery.Search` while preserving `knowledge.SearchRequest` and `SearchResult` DTO/history behavior |
| REST `POST /api/bases/{id}/semantic/context` | bounded context | Wired to `AgentQuery.ContextForBase` |
| Extension `knowledge_search` | evidence tool | Wired to `AgentQuery.Search`; adapter restores the stable `searchToolResult` shape |
| Extension `knowledge_compile_context` | bounded context | Wired to `AgentQuery.ContextForBase`; preserves direct `semantic.ContextPackage` result |
| Automatic context provider | lexical Auto-RAG | Not migrated yet because it is a distinct bounded injection workflow |

The contract test compares shared application output with real Extension tool output for both search and context. Timing metadata is excluded because it is intentionally non-deterministic.

## Request semantics

The shared request starts with:

- `BaseIDs`: explicit authorized bases; omitted means the configured enabled scope.
- `Mode`: `evidence`, `semantic`, `hybrid`, or `auto`.
- `EvidenceMode`: existing retrieval lane (`auto`, `hybrid`, `vector`, `lexical`) used while the explicit evidence REST/tool DTO remains stable.
- `Generation`: semantic `active` or immutable numeric generation.
- `Filter`: existing evidence filter until the unified filter contract is expanded.
- `TopK`, `Debug`, and structured inclusion.

Current supported policy:

- `semantic` without a usable generation returns `SEMANTIC_NOT_READY`.
- `hybrid` still returns evidence and records semantic unavailability as a warning plus `degraded=true`.
- `auto` plans intent, includes evidence, and includes semantic only when available.
- A missing semantic compilation never silently rewrites a semantic request into evidence.

## Snapshot strategy

`Knowledge.GetSemanticCompilationByID` now exposes one immutable semantic generation. `AgentQuery.Search` reads the pinned compilation and returns a `Snapshot` with its base, generation, and `active` / `pinned` source.

Evidence snapshots are not yet exposed as a first-class shared envelope. The existing search result already carries per-hit `IndexGeneration` and `SourceVersion`; G1/G2 will add the snapshot envelope without inventing a global evidence generation that storage does not currently publish.

## MCP SDK decision

Use the official Go SDK:

```text
github.com/modelcontextprotocol/go-sdk
```

Selected evidence:

- It is maintained in the Model Context Protocol organization in collaboration with Google.
- The current stable package line inspected during this implementation is `v1.8.0`.
- Its `mcp` package provides server/client APIs, stdio transport, and streamable HTTP handler support.
- Its protocol documentation supports modern protocol eras including `2026-07-28` and `2025-11-25`.

The dependency has not been added yet. Phase 3 will add it in a dedicated commit with wire conformance tests; no custom JSON-RPC MCP implementation will be introduced.

## Migration strategy

1. Keep Extension v1 and REST behavior while moving execution below `agentquery`.
2. Add contract tests at every adapter boundary.
3. Add MCP adapter only after shared query behavior is covered.
4. Add read-only resources after tools and snapshots stabilize.
5. Add security defaults and limits before enabling streamable HTTP.
6. Preserve rollback by keeping the Extension v1 binary/mode fully functional.
