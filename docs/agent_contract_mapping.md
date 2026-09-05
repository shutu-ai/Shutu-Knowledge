# Agent Contract Mapping

Maps every Knowledge integration need onto `shutu-agent` Extension Platform v1 (protocol `shutu-extension/1`, SDK `github.com/shutu-ai/shutu-agent/sdk/extension`, audited at shutu-agent commit `8701c2adbc00b0af8c5fba5272daaf28a7327e92`). Only public contract surfaces are used: `sdk/extension` DTOs, `docs/extension_protocol_v1.md`, `docs/extension_development_guide.md`, and the `examples/extension` reference.

## 1. Contract surfaces available (verified in source)

| Surface | v1 contract | Notes |
|---|---|---|
| Manifest | `extension.Manifest` (YAML/JSON, unknown fields rejected) | identity, capabilities, transport, tools, contextProvider, web, events, health, lifecycle, permissions, configurationSchema |
| Transports | `stdio` (Agent-managed child, newline-delimited JSON-RPC) or `http` (one request per POST) | credential-shaped env vars rejected |
| Methods | `initialize`, `health`, `context/provide`, `tool/call`, `event`, `shutdown` | JSON-RPC 2.0 |
| Context strategies | `once_per_turn`, `before_every_model_call`, `on_user_input_change`, `after_tool_result`, `manual` | `after_tool_result`: at-most-once per durable tool-result batch; consumed on empty/timeout/cancel/failure |
| Tools | `ToolDefinition{name, description, inputSchema, outputSchema, risk, requiresApproval}` | risks: read/write/destructive/external_side_effect/privileged; Agent approval policy remains authoritative |
| Context DTO | `ContextRequest{sessionId, turnId, stepId, step, workspace, userInput, metadata}` -> `ContextContribution{source, content, priority, estimatedTokens, truncatable, metadata}` | minimal permitted session context |
| Web | `WebContribution{route, title, icon, navigation*, order, serviceUrl}`; `initialize` may return ephemeral `webBaseUrl` | stdio extensions may start a local listener and report the URL |
| Events | allow-list subscription only; observational, best-effort, at-most-once | 13 event types; payloads contain identifiers/counts only |
| Permissions | named grants (`session.id`, `session.turn`, `session.step`, `workspace.path`, `user.input`, ...) | granted at discovery config; `required` marks startup-critical |
| Lifecycle/health | `health` -> `{ready, status, detail}`; lifecycle with startup/shutdown timeouts and restart policy | single health signal, free-form detail |

## 2. Knowledge capability -> contract mapping

| Knowledge need | v1 mechanism | Design decision |
|---|---|---|
| Auto RAG injection before answering | Native Context Provider, strategy `on_user_input_change` (default), optional `after_tool_result` for follow-up enrichment, `once_per_turn` fallback | Provider receives `userInput`, runs the current-turn-first retrieval, returns ContextContribution(s) with source citations and estimated tokens. Agent owns injection, ordering, budget and dedup surfaces. dsh's "fold user-role message" behavior is intentionally NOT replicated: context ownership stays with the Agent. |
| Evidence content | `content` carries serialized context window (before/anchor/after, `>>>` anchor marker); `metadata` carries baseId/docId/chunkId/scores; `truncatable=true`; `priority` by relevance | Mirrors dsh `SearchHit.contextWindow` semantics without Agent message mutation |
| Token budgets | Provider-internal budgets (768/hit explicit, 180/640 auto) + `estimatedTokens`; Agent enforces final budget (`global_context_tokens`, `max_contribution_tokens`) | Knowledge never decides the Agent context window |
| 14 model tools | tools capability + 14 ToolDefinitions returned at initialize (manifest keeps identity/risk record) | Tool names unchanged; host prefixes `ext__shutu-knowledge__` |
| Destructive approval | `risk: destructive` + `requiresApproval: true` on delete_base/delete_document | read/write declared accordingly; Agent whitelist governs non-read enablement |
| Enabled-scope guard | extension-side check inside `tool/call` using owned config (enabledBaseIds) | Replicates dsh tools guard without a second registry |
| Proactive-use guidance | encoded in tool descriptions (model-visible) | GAP-001 workaround; see agent_extension_gap_report.md |
| Web management panel | web capability + route (e.g. `/extensions/shutu-knowledge/`); extension binary runs its own HTTP server (API + SPA) and reports `webBaseUrl` at initialize | Agent owns navigation/proxy/auth shell; Knowledge owns business UI/API/state |
| Lifecycle | lifecycle capability; `restart_policy: on-failure`, bounded timeouts | idempotent start/stop; startup resume of interrupted imports |
| Health | health capability; `ready` requires DB open + migrations current; `detail` lists index/model/parser subsystem states | optional components (reranker, OCR, local models) degrade instead of failing ready unless configured required |
| Events | minimal subscription; none required for v1 core behavior | dsh relies on host-loop hooks, not events; we subscribe to nothing initially, revisit only with a proven need |
| Session context access | permissions: `session.id`, `user.input`, `session.turn`, `session.step`, `workspace.path` (only what retrieval uses) | least privilege; no additional grants requested |
| Configuration UI | manifest `configurationSchema` for host-generic presentation + Knowledge-owned settings storage/API | business config stays in Knowledge data domain |
| Background ingestion | entirely extension-owned (job manager + worker pool); not a contract surface | Agent is never blocked by imports |

## 3. Deliberate behavior differences from dsh-knowledge

1. **Context ownership**: dsh folds a user-role background message into the pre-step batch; shutu-agent has no extension message mutation. We return contributions and let the Agent compose context. Equivalent user outcome (model sees evidence before answering), different ownership.
2. **Same-origin panel**: dsh serves `/knowledge/*` on the host web server; v1 gives the extension its own `webBaseUrl` behind the Agent's navigation. Upload/management API calls target the extension server through the Agent's shell.
3. **System prompt guidance**: see GAP-001.
4. **Event-driven features**: dsh uses `tools/pre-execute` and `agent/pre-step` host hooks; v1 extensions get no mutating hooks. Approval is declared via tool risk metadata; retrieval is scheduled by the provider strategy.

## 4. Architecture invariants enforced by the adapter

- `internal/extension` is the only package importing `github.com/shutu-ai/shutu-agent/sdk/extension`; Knowledge Core never sees Agent DTOs (`ContextRequest` is mapped to a core `KnowledgeQuery` at the adapter boundary).
- Production imports of `shutu-agent/internal/...` are forbidden (Gate B).
- Knowledge state (SQLite DB, raw files, models, jobs) lives under the Knowledge data domain; nothing is written to Agent storage.
- Removing the extension directory leaves the Agent untouched (Gate I is validated by test).
