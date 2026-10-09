# Installing the Skill and MCP Server

## 1. Run the MCP server

```bash
# stdio (what Codex launches) — isolated data home for testing:
SHUTU_KNOWLEDGE_HOME=/path/to/data shutu-knowledge mcp

# streamable HTTP:
shutu-knowledge mcp --http 127.0.0.1:7801
```

The server is read-only. Import/compile/manage data with the existing CLI
(`shutu-knowledge serve`, document import APIs) before or alongside it.

## 2. Register the server with Codex (project level)

Copy `examples/mcp/codex-config.example.toml` content into the project
`.codex/config.toml` (or your Codex client's equivalent MCP config) and adjust the
binary path and data home. Client examples: `examples/mcp/client-stdio.example.md`
and `examples/mcp/client-http.example.md`.

Verify before relying on it: start a session and call `knowledge_list_bases`.

## 3. Install the Skill

Codex loads skills from its skills directory; copy or symlink this folder:

```bash
# user-level (all projects)
cp -r skills/shutu-knowledge-research ~/.codex/skills/
# or project-level
cp -r skills/shutu-knowledge-research .codex/skills/
```

On clients without native skill support, paste `SKILL.md` into the system/project
prompt and keep `references/` reachable as files. The Skill targets the
agentskills.io frontmatter convention; clients that do not parse it still work via
prompt installation.

## 4. Smoke check

1. Ask: "List my knowledge bases" → expect `knowledge_list_bases` output.
2. Ask: "What do the docs say about <a real indexed topic>?" → expect citations
   with document/chunk/version, produced through tools (visible in the trace).
3. Stop the server and ask again → the agent should report the tools are offline,
   not invent answers.

## 5. Trigger tests

`tests/trigger-cases.md` lists prompts that should and should not activate the
Skill. Run them manually after client upgrades; automation lives in
`internal/skill` (`go test ./internal/skill/`).