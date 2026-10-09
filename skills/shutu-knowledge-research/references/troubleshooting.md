# Troubleshooting

Operational errors must produce actionable guidance, never a silent fallback to
unverified answers.

| Symptom | Meaning | What to do |
|---|---|---|
| Tool call fails / transport error | MCP server unreachable | check the client config (see INSTALL.md), restart `shutu-knowledge mcp`; tell the user the knowledge tools are offline instead of answering from memory |
| `BASE_NOT_FOUND` | base id typo or base removed | re-run `knowledge_list_bases`; ask the user which base if still ambiguous |
| `PERMISSION_DENIED` | base outside enabled scope | tell the user to enable the base or name an accessible one |
| `SEMANTIC_NOT_READY` | semantic memory never compiled or pinned generation missing | use evidence mode now; suggest running semantic compilation from the management CLI for concept navigation |
| `GENERATION_NOT_FOUND` | pinned generation unavailable | re-run with `generation: "active"` and mention the pin failed |
| `VERSION_UNSUPPORTED` | malformed version identity or unsupported dimension | re-read versions from search/context output; only `source_version` dimension exists |
| `UNIT_NOT_FOUND` | unit id from another base/generation, or hidden by version policy | re-find the unit with `knowledge_search` in the same scope |
| `EVIDENCE_UNRESOLVED` | document/chunk/generation mismatch | re-search for the current pointer; never cite the failed pointer |
| `degraded:true` | partial scope or semantic lane failed | disclose in the answer which lane was degraded |
| empty evidence hits | phrasing too specific | retry with broader phrasing, then explore; after two failures declare the gap |