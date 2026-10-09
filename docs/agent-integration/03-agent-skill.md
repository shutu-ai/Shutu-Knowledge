# Production-grade Agent Skill

Status: **Phase 4 complete — real Codex client discovered, invoked, and cited.**

## Deliverables

- `skills/shutu-knowledge-research/SKILL.md` — lean main skill (agentskills.io
  frontmatter, core loop, tool selection table, stop rules, answer contract,
  safety rule). Body is intentionally far below the lint ceiling; detail lives in
  references.
- `references/tool-contracts.md` — per-tool inputs, key fields, pitfalls.
- `references/reasoning-playbook.md` — cross-document research, multi-hop chains,
  fault diagnosis, structured data, no-answer handling.
- `references/citation-policy.md` — resolved-pointer citations, version
  consistency, `document_asserted` vs `inferred_for_navigation`, prohibitions.
- `references/temporal-queries.md` — version policy cheat sheet and rules.
- `references/troubleshooting.md` — operational error table; tool failures produce
  actionable guidance, never silent fallback.
- `assets/sourced-answer-template.md` — the answer shape with citations.
- `tests/trigger-cases.md` — 12 should-trigger + 12 should-not-trigger prompts.
- `INSTALL.md` + `examples/mcp/` — Codex and generic client setup.

## Automated verification

`internal/skill` lints the shipped package (`go test ./internal/skill/`):

- frontmatter parses; name/description present; description carries the trigger
  vocabulary and stays within the size budget; main body stays lean;
- every `references/`+`assets/` path mentioned in SKILL.md exists;
- every `knowledge_*` name across the package belongs to the read-only MCP tool
  surface (unknown tools fail the lint);
- trigger test set holds at least 10 cases in each direction.

Negative tests prove the lint rejects unknown tool names, missing references, and
short trigger sets.

## Live client verification (G4)

Executed against a real Codex client with the skill installed in
`~/.codex/skills/shutu-knowledge-research` and the MCP server registered via
`codex mcp add`, answering a seeded two-document knowledge base:

Prompt: 根据我们的知识库，KPI 是如何计算的？刷新频率是多少？给出文档出处。

Observed transcript:

1. Codex loaded the skill (its body, including the safety rule, is echoed at the
   top of the session).
2. Tool calls, exactly the prescribed core loop: `knowledge_list_bases` →
   `knowledge_capabilities` → `knowledge_search` → `knowledge_evidence` ×2.
3. The answer carries real citations with document ids, chunk locators, and
   source versions matching the seeded documents verbatim; the two refresh-rate
   figures (5-minute SDR sampling vs 15-minute KPI refresh) are reconciled as
   `document_asserted` relations; the missing mathematical formula is reported as
   an explicit unverified point instead of being invented.

After the run the temporary global MCP registration was removed; the skill itself
remains installed for future use.