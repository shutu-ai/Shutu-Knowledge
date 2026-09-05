# Agent Extension Gap Report

Gaps discovered while mapping dsh-knowledge capabilities onto Extension Platform v1. Rule: a gap never justifies modifying shutu-agent or bypassing the contract. Improvement requests must be generic Agent capabilities.

## GAP-001: No model-visible extension guidance channel beyond tool descriptions

- **Capability**: proactive-use guidance ("search the knowledge base before answering when the user asks about imported facts").
- **Business scenario**: dsh-knowledge injects a system-prompt section (`ctx.systemPrompt.section`) while knowledge is enabled, so the model proactively calls `knowledge_search` even when the user never mentions the knowledge base. Without it, tool adoption depends on the deployment's agent instructions.
- **Why Extension v1 cannot express it today**: v1 contributions are scheduled retrieval content (ContextContribution) and tool metadata. There is no system-prompt/prompt-guidance contribution channel, and injecting guidance via context retrieval would couple a static instruction to retrieval scheduling semantics.
- **Current Agent capability**: tool `description` strings are model-visible and already carry behavioral guidance (the demo/tool layer renders them into the catalog). This is a legitimate v1 surface.
- **Missing minimal generic capability**: none strictly required for v1. If the Agent later wants richer extension steering, the generic shape would be "extension-contributed prompt guidance section with host-controlled priority and budget" (not a Knowledge-specific API).
- **Recommended Agent Improvement Requirement**: optional `promptGuidance` declaration on ToolDefinition/manifest (generic, budgeted, host-ordered), only if tool-description guidance proves insufficient in practice.
- **Blocking**: NO.
- **Non-breaking workaround**: encode the proactive-use instruction in every `knowledge_search` tool description (dsh's own tool description already does this) and rely on auto-RAG context contributions for the passive path.

## GAP-002: Health result is a single scalar with free-form detail

- **Capability**: distinguish process / database / index / model subsystem health inside the Agent's health surface.
- **Business scenario**: `doctor` and the Knowledge panel need per-subsystem status; the Agent only observes `{ready, status, detail}`.
- **Why Extension v1 cannot express it today**: `HealthResult` has no structured subsystem field.
- **Current Agent capability**: `detail` is free-form; extension-owned `/health` HTTP endpoint can expose structured data to the panel.
- **Missing minimal generic capability**: a generic structured health payload (e.g. `components: map[name]{status, detail}`) would benefit any extension with subsystems.
- **Recommended Agent Improvement Requirement**: extend `HealthResult` with an optional structured `components` map in a v1 minor revision (backward compatible).
- **Blocking**: NO.
- **Non-breaking workaround**: keep Agent-facing `detail` as a compact string; expose full structured health via the extension's own HTTP API; `doctor` CLI prints subsystem detail.

## Watch items (not gaps)

| Item | Assessment |
|---|---|
| dsh folds background as a user-role message adjacent to the trigger | v1 `on_user_input_change` provider scheduling achieves the same user outcome; context ownership stays with the Agent. No gap. |
| dsh `tools/pre-execute` approval interception | replaced by declared `risk`/`requiresApproval` metadata + Agent approval policy. No gap. |
| dsh `tools.guard` enable/disable switch | implemented inside extension `tool/call` using owned config. No gap. |
| Directory/file ingestion permissions | the extension process owns its own OS-level file access; v1 permissions govern granted session context only. No gap. |
| Long-running imports during context requests | provider must honor `context_timeout_ms`; ingestion runs on separate workers. No gap. |
