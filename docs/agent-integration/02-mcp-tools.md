# MCP Tools, Resources, and Prompts

Status: **Phase 3 complete — real-client wire verified on stdio and streamable HTTP.**

## Transport

- Official SDK: `github.com/modelcontextprotocol/go-sdk` **v1.8.0**.
- `shutu-knowledge mcp` runs stdio (default); `shutu-knowledge mcp --http <addr>` runs streamable HTTP.
- The server is read-only. Compilation, import, and deletion stay in the existing
  management API/CLI and are not exposed as MCP tools.
- One binary, one storage instance; concurrent `serve`/`extension`/`mcp` processes on the
  same data home fail on the instance lock by design.

## Response envelope

Every tool returns the plan §6.10 envelope as both structured content and JSON text:

```json
{
  "status": "ok",
  "trace_id": "trace-…",
  "data": {},
  "context": {"base_ids": [], "semantic_generation": 0, "effective_mode": "hybrid"},
  "warnings": [],
  "degraded": false,
  "error": null
}
```

Application errors return `status:"error"` with `IsError=true` and a stable
`error.code`: `INVALID_ARGUMENT`, `PERMISSION_DENIED`, `BASE_NOT_FOUND`,
`SEMANTIC_NOT_READY`, `GENERATION_NOT_FOUND`, `VERSION_UNSUPPORTED`,
`UNIT_NOT_FOUND`, `EVIDENCE_UNRESOLVED`, `INDEX_NOT_READY`, `INTERNAL_ERROR`.
Codes map 1:1 from the shared agentquery taxonomy; adapters never invent
semantics.

## Tools (9, all read-only)

| Tool | Backing application call | Notes |
|---|---|---|
| `knowledge_list_bases` | `agentquery.Capabilities` | authorized scope only; `page_size`/`cursor` pagination |
| `knowledge_capabilities` | `agentquery.Capabilities` | modes, per-base readiness, generation ids, protocol info, limits |
| `knowledge_search` | `agentquery.Search` | evidence/semantic/hybrid/auto; version policy; structured lane; snapshots |
| `knowledge_explore` | `agentquery.Explore` | semantic activation + typed relation BFS with provenance closure |
| `knowledge_expand` | `agentquery.Expand` | up to 8 root units; direction/relation filters; per-root error reporting |
| `knowledge_evidence` | `ResolveSemanticUnitEvidence` + document/chunk read | version-verified excerpts; missing/chunk orphan fails closed (`EVIDENCE_UNRESOLVED`) |
| `knowledge_context` | `agentquery.Context` | bounded, citation-bearing package per base; renders through the core compiler |
| `knowledge_compare_versions` | semantic compilation diff | `added/removed/changed/unchanged_count/conflicting/uncertain`; absence without supersession evidence stays `uncertain`, never deleted |
| `knowledge_diagnostics` | in-process trace ring | bounded 256-entry registry; query text and document bodies are never stored or returned |

`knowledge_expand` defaults `max_hops=2` per plan; hard ceilings (hops 4, nodes 256,
fan-out 64) match the shared application layer, which clamps all requests.

## Resources (read-only)

| URI template | Content |
|---|---|
| `shutu-knowledge://base/{base_id}/manifest` | base manifest with counts, semantic/structured readiness, modes |
| `shutu-knowledge://base/{base_id}/generation/{generation}/unit/{unit_id}` | one unit plus its resolved evidence closure |
| `shutu-knowledge://base/{base_id}/document/{doc_id}/evidence/{locator}` | version-verified excerpt for a chunk locator |
| `shutu-knowledge://base/{base_id}/generation/{generation}/topic/{topic_id}` | topic unit plus related units |

URIs are logical identifiers only and are never interpreted as filesystem paths.
Unknown identities return `ResourceNotFoundError`.

## Prompts

- `cited-research(question)` — evidence-first workflow instructions.
- `temporal-compare(topic, from_version, to_version)` — version comparison workflow.

Prompts intentionally stay minimal; the portable Agent Skill (Phase 4) owns the
full reasoning playbook so there are no conflicting instruction layers.

## Wire verification evidence

`internal/mcp/server_test.go` drives a real MCP client over real transports:

- `TestMCPToolsOverHTTP` — all 9 tools called through `StreamableClientTransport`
  against `StreamableHTTPHandler` on a seeded storage, asserting envelope
  contract, lane data, unit/evidence resolution, and that trace records never
  contain query text.
- `TestMCPResourcesAndPrompts` — `resources/read` for manifest, unit, and a
  not-found case; prompts list/get through the real client.
- `TestMCPStdioRealProcess` — compiles the shipped binary, runs
  `shutu-knowledge mcp` as a subprocess over `mcp.CommandTransport` (real
  stdin/stdout pipes), lists all 9 tools, and calls search.
- `TestMCPStdioHTTPSemanticParity` — same requests over both transports return
  identical stable fields (only volatile timing/trace fields may differ).