# Deployment

## Build And Run

From the repository root:

```sh
go build -o shutu-knowledge ./cmd/shutu-knowledge
./shutu-knowledge doctor
./shutu-knowledge serve
```

`serve` is the standalone Web/API mode and defaults to loopback only.
`shutu-knowledge extension` is used automatically by the Agent through stdio.
`version` prints the binary version.

To build the independently owned Web assets before packaging:

```sh
cd web
npm install
npm run build
npm test
```

The Go binary embeds the generated `internal/web/dist` assets.

## Data Domain

Default data home:

```text
~/.shutu/knowledge/
```

Override it with `SHUTU_KNOWLEDGE_HOME`. Knowledge owns its SQLite database,
raw source store, model cache, jobs, configuration, and logs in this domain.
It does not write Agent sessions or Agent configuration. Run the service under
an account that can access this directory and no other service can read it.

## Agent Extension

The Agent discovers [extension.yaml](../extension.yaml) and runs:

```text
shutu-knowledge extension
```

The manifest declares Protocol v1 lifecycle, health, 14 tools, native context
provider, Web contribution, and minimal session/input permissions. The Agent
does not need Knowledge-specific code.

For a minimized child environment, provide a non-secret data path through the
manifest:

```yaml
transport:
  type: stdio
  command: /opt/shutu/bin/shutu-knowledge
  args: ["extension"]
  env:
    - SHUTU_KNOWLEDGE_HOME=/var/lib/shutu-knowledge
```

Knowledge starts an ephemeral loopback HTTP server and reports it privately to
the Agent as `webBaseUrl`. The Agent authenticates the user and reverse-proxies
the declared route to it.

## Optional Runtimes

Embedding, reranking, and OCR inference are currently manually configured
external helper processes described in [models.md](models.md); this is not an
out-of-box runtime installation. MinerU, legacy office conversion, and PDF
content-signature conversion are optional external document services. Configure commands through
[configuration.md](configuration.md), then verify readiness with:

```sh
shutu-knowledge doctor
```

The REST endpoint `GET /api/runtime-status` probes configured helper
capabilities. `doctor` also lists missing optional runtimes and model lifecycle
state. Missing artifact files alone never make a runtime ready; an installed
artifact set is only `INSTALLED` until load and inference smoke succeed.

## Operational Checks

- `go build ./...`, `go vet ./...`, `go test ./...`, and `go test -race ./...`
  for build/runtime assurance.
- `cd web && npm run build && npm test` for the SPA and API contract.
- `scripts/removal_gate.ps1` for a real Agent installed/removed comparison.

Removing the Knowledge directory or disabling its extension source leaves the
Agent with no Knowledge tools, navigation entry, or context contribution.
