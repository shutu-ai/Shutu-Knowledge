# Agent Integration Architecture

Status: **Phase 2 complete; MCP adapter not installed yet.**

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
7. Capability discovery reports structured readiness only when active structured models exist.
8. All cross-base requests pass through one scope resolution routine.

## Current Phase 1 wiring

At HEAD, these paths already route through `internal/agentquery`:

| Surface | Operation | Status |
|---|---|---|
| REST `POST /api/search` | evidence search | Wired to `AgentQuery.Search` while preserving `knowledge.SearchRequest` and `SearchResult` DTO/history behavior |
| REST `POST /api/bases/{id}/semantic/context` | bounded context | Wired to `AgentQuery.ContextForBase` |
| Extension `knowledge_search` | evidence tool | Wired to `AgentQuery.Search`; adapter restores the stable `searchToolResult` shape |
| Extension `knowledge_compile_context` | bounded context | Wired to `AgentQuery.ContextForBase`; preserves direct `semantic.ContextPackage` result |
| Automatic context provider | lexical Auto-RAG | Wired to `AgentQuery.Search` with lexical evidence mode; preserves all injection, score, deduplication, and token-budget policy in the adapter |

The contract test compares shared application output with real Extension tool output for both search and context. Timing metadata is excluded because it is intentionally non-deterministic.

## Request semantics

The shared request starts with:

- `BaseIDs`: explicit authorized bases; omitted means the configured enabled scope.
- `Mode`: `evidence`, `semantic`, `hybrid`, or `auto`.
- `EvidenceMode`: existing retrieval lane (`auto`, `hybrid`, `vector`, `lexical`) used while the explicit evidence REST/tool DTO remains stable.
- `Generation`: semantic `active` or immutable numeric generation; context compilation supports both active and pinned generations.
- `VersionPolicy`: `current`, `all`, `as_of`, or an ordered semantic `range`.
  - Semantic search and context compilation enforce the policy against compiled source versions.
  - Current evidence retrieval always resolves active documents. If a non-current policy includes evidence, the shared result marks `VERSION_POLICY_DEGRADED` and `degraded=true`; it never claims that retired evidence was queried.
- `Filter`: existing evidence filter until the unified filter contract is expanded.
- `TopK`, `Debug`, and structured inclusion.

Current supported policy:

- `semantic` without a usable generation returns `SEMANTIC_NOT_READY`.
- `hybrid` still returns evidence and records semantic unavailability as a warning plus `degraded=true`.
- `auto` plans intent, includes evidence, and includes semantic only when available.
- A missing semantic compilation never silently rewrites a semantic request into evidence.
- Shared errors carry stable codes (`INVALID_ARGUMENT`, `PERMISSION_DENIED`, `BASE_NOT_FOUND`, `SEMANTIC_NOT_READY`, `GENERATION_NOT_FOUND`, `VERSION_UNSUPPORTED`) while adapters retain their existing transport shapes.

## Phase 2 semantic graph traversal

`internal/agentquery/explore.go` adds the shared, protocol-independent graph layer used by
G2. It introduces two application operations over one immutable semantic generation:

- `Explore`: lexical semantic activation produces seeds, then typed-relation BFS expands
  them per base.
- `Expand`: one exact unit ID seeds the walk; a unit hidden by the requested version
  policy reports `UNIT_NOT_FOUND` with a precise message instead of being silently
  dropped.

Contract rules enforced by the shared layer:

1. Per-base graphs are independent; results are never merged into a fake cross-base
   generation. Graphs carry the snapshot generation and the semantic routing plan.
2. Endpoint visibility reuses the exact semantic search rule (`TemporalVersionVisible`),
   so `current`, `as_of`, `range`, and `all` policies apply to seeds and expansion alike.
3. Relation traversal supports `in`/`out`/`both`, relation-type allowlists, per-node
   fan-out, hop and node ceilings with clamped defaults (1 hop / 64 nodes / 16 fan-out),
   cycle-safe BFS, and deterministic ordering (seeds keep activation order; adjacency,
   nodes, edges, and paths are sorted by stable IDs).
4. `contradicts` edges carry `conflict=true` — reporting that a conflict relation exists,
   never which side is true — and can be excluded with `excludeConflicts`.
5. Provenance closure: every node resolves through `semantic.ResolveEvidence` to exact
   IR/chunk pointers; every edge carries its relation sources or an explicit
   `projection_only` status (derived_from links only). Traversal fails loudly rather
   than emitting an unevidenced citation.
6. Stable errors: `SEMANTIC_NOT_READY` when no requested base has a usable generation,
   `GENERATION_NOT_FOUND` diagnostics for unusable pins, `UNIT_NOT_FOUND` for missing or
   version-hidden seeds, plus per-base `SEMANTIC_NOT_READY` diagnostics with
   `degraded=true` for partial scopes.

G2 isolated fixtures (`explore_test.go`) prove semantic activation over the real compile
pipeline, cross-document expansion with hop/path accounting, conflict filtering,
direction/relation/bounds behavior with cycle safety, version consistency for current /
as-of / evolution policies, provenance closure without ghost citations, and deterministic
repeats.

## Snapshot strategy

`Knowledge.GetSemanticCompilationByID` now exposes one immutable semantic generation. `AgentQuery.Search` reads the pinned compilation and returns a `Snapshot` with its base, generation, and `active` / `pinned` source.

Evidence snapshots are exposed as exact per-document identities in `Result.EvidenceSnapshots` (`baseId`, `documentId`, `indexGeneration`, and `sourceVersion`). SQLite currently has per-document active generations rather than one auditable base-wide index generation, so the shared layer does not invent an aggregate evidence generation.

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
