# Agent Integration Acceptance Report

Version: 0.7.0 · Close commit: see `git log` · Report date: 2026-10-10
Scope: `Shutu-Knowledge_Agent_MCP_Skills_Full_Implementation_Plan.md` Phases 0–9.

## Gate summary

| Gate | Claim | Primary evidence |
|---|---|---|
| G0 audit | repository audit + frozen baseline | `00-repository-audit.md`, `baseline-2026-10-09.md` |
| G1 shared query | REST/Extension route through `agentquery`; contract tests | `internal/agentquery/service_test.go` (`TestExtensionSearchAndContextUseSharedQueryApplication`, `TestRESTSearchAndContextMatchSharedQueryApplication`) |
| G2 semantic graph | explore/expand with typed relations, temporal visibility, provenance closure, no ghost citations | `internal/agentquery/explore_test.go` (7 fixtures) |
| G3 MCP wire | official Go SDK v1.8.0; 9 read-only tools + 4 resource templates + 2 prompts; real client over stdio subprocess and streamable HTTP; stdio/HTTP parity | `internal/mcp/server_test.go` (4 wire tests), `02-mcp-tools.md` |
| G4 skill | portable SKILL.md + references + trigger set; lint tests; **live Codex client discovered, invoked tools, produced chunk-level citations** | `internal/skill`, `03-agent-skill.md`, transcripts in Phase 4 run |
| G5 security | loopback-only HTTP with Host/Origin validation (AT-17), IDOR fail-closed, prompt-injection fixture, query/concurrency/deadline limits; Windows package built + binary verified standalone | `internal/mcp/security_test.go`, `internal/mcp/http.go`, `limits.go`, `04-deployment-security.md` (package SHA recorded) |
| G6 evaluation | deterministic harness + tiny public fixture + frozen report layout; found and fixed a real lexical zero-hit bug (reason-recorded rebaseline) | `internal/eval`, `evals/tiny-fixture`, `05-evaluation-protocol.md`, `docs/0.4_benchmark_0.2_baseline.json` (`rebaseline.reason`) |
| G7 A/B/C | frozen three-arm comparison: retrieval layer evidence=hybrid=0.833, semantic-only=0 (negative result recorded); agent layer 4/4 everywhere, zero fabricated citations; Skill changes verification behavior at no cost penalty | `reports/evals/phase7/` (`comparisons.json`, `agent-arms-report.md`) |
| G8 D-arm decision | precondition unmet — enrichment stays unimplemented/disabled with a frozen design and re-trigger criteria | `enrichment-d-arm-decision.md` |
| G9 release | clean-machine package verified (version/doctor/stdio); full regression green; operating guide | `04-deployment-security.md`, `../deployment.md`, this report |

## Release readiness matrix

| Area | Status | Notes |
|---|---|---|
| MCP tools (9, read-only) | ready | wire-verified on both transports |
| Resources / prompts | ready | templates list-scoped, fail closed |
| Shared query layer | ready | single source of retrieval semantics |
| Semantic graph (explore/expand) | ready | bounded, deterministic, provenance-closed |
| Agent Skill | ready | installed and verified on a real Codex client |
| Security baseline | ready | see G5 evidence; no accepted critical/high findings |
| Windows packaging | ready | formal zip + secret audit + standalone binary run |
| Linux packaging | ready | same script (`-Platform linux-amd64`), not exercised on a live host this cycle |
| Evaluation harness | ready | deterministic toy E2E; private benchmark pending operator data |
| Private 200Q benchmark | pending operator | harness + spec shipped; corpus is user-owned |
| Remote multi-user HTTP | not offered | loopback is the supported mode; `--allow-remote` requires operator auth stack |

## Demo case (reproduced twice this cycle)

1. Seed an isolated data home with the tiny fixture corpus and compile semantic
   memory (API or CLI import).
2. `shutu-knowledge mcp` + Codex with the Skill installed.
3. Prompt: 根据我们的知识库，KPI 是如何计算的？……给出文档出处。
4. Observed (transcripts preserved in Phase 4 and Phase 7 runs): tool calls
   follow the Skill core loop (`list_bases → capabilities → search →
   evidence`), answers carry document/chunk/version citations, no-answer
   questions abstain with an evidence trail, and prompt injection inside
   documents is treated as data.

## Failure ledger (top issues and resolutions)

1. Lexical zero-hit on queries with rare short words — **fixed** (`store_lane_lexical.go`), regression-protected by the eval harness and the golden PDF provenance test.
2. 0.2 token baseline distortion after the fix — **resolved by recorded rebaseline** (`rebaseline.reason`, enforced non-empty by the benchmark test).
3. No-answer queries receive non-zero vector scores — **documented**: abstention is an agent-layer decision (Skill stop rules); harness exposes the signal.
4. hybrid-vs-evidence gain not observable on toy corpus — **recorded as negative result**; private large-corpus benchmark required before claiming semantic value.
5. OCR runtime smoke requires external container image — release-host gate, run where artifacts exist.

## Known limitations

- `time_dimension` supports `source_version` only.
- Remote/multi-user HTTP is out of scope for this binary; loopback is the
  supported deployment.
- Abstention quality at the retrieval layer is an exposed signal, not an
  automated decision.

## Final package freeze

The closing commit `8846e8e` was packaged from the clean tree:
`shutu-knowledge-0.7.0-windows-amd64.zip`, SHA256
`6ee1201f5eb5a0e2b9119e68a7d0208f77907b8c301e4a209c00ff7a0d45d350` (binary
`f2b002de…e7a0459`), secret audit passed. The packaged binary reported
`version 0.7.0 / 8846e8e` and answered a real `initialize` over stdio with
clean JSON-RPC — the same evidence chain as the Phase 5 gate, re-run at the
final commit.

## Sign-off

All mandatory gates (G0–G7, G9) verified with the evidence linked above; G8
resolved as a documented decision. Remaining operator-owned items: private
benchmark corpus run and the OCR smoke artifacts.
