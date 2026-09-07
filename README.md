# shutu-knowledge

An independent knowledge-base system for [shutu-agent](https://github.com/shutu-ai/shutu-agent), implementing capability equivalence with [dsh-knowledge](https://github.com/Soren-ABT/dsh-knowledge) through the frozen shutu-agent Extension Platform v1.

Status: **V1 Release Ready.** Target capability coverage is complete except
documented non-blocking upstream Extension Contract limitations. The Agent
dependency is the explicitly Apache-2.0 licensed public module
`github.com/shutu-ai/shutu-agent v0.2.1`. [Agent.md](Agent.md) tracks the plan,
[docs/gates.md](docs/gates.md) tracks Gate A-J evidence,
[docs/release_readiness_report.md](docs/release_readiness_report.md) tracks
release gates, and
[shutu_knowledge_implementation_report.md](shutu_knowledge_implementation_report.md)
records the current evidence-backed conclusion.

Out-of-box runtime status: **ready**. Knowledge automatically manages local ML,
OCR, PDF rendering, and Office conversion without user-authored helpers. See
[out_of_box_parity_report.md](out_of_box_parity_report.md) and
[docs/runtime_dependencies.md](docs/runtime_dependencies.md), the
[runtime_implementation_report.md](runtime_implementation_report.md), and the
[runtime license inventory](docs/runtime_license_inventory.md). V1 release
readiness and out-of-box runtime parity are intentionally separate statuses.

## Architecture at a glance

- **One-way dependency**: `shutu-knowledge → shutu-agent public Extension API`. The Agent is a read-only dependency.
- **Knowledge Core** is agent-agnostic; only `internal/extension` touches Agent DTOs.
- **Own data domain** at `~/.shutu/knowledge/` (SQLite + raw store + models + jobs).
- **Native integration**: context provider (auto RAG), 14 model tools with risk metadata and approval, web contribution with its own HTTP UI, lifecycle and health.

Key documents:

- [docs/architecture.md](docs/architecture.md) — technical design
- [docs/agent_contract_mapping.md](docs/agent_contract_mapping.md) — capability-to-contract mapping
- [docs/dsh_knowledge_capability_inventory.md](docs/dsh_knowledge_capability_inventory.md) — audited reference behavior
- [docs/dsh_knowledge_equivalence_matrix.md](docs/dsh_knowledge_equivalence_matrix.md) — parity tracking
- [docs/agent_extension_gap_report.md](docs/agent_extension_gap_report.md) — Agent contract gaps and workarounds
- [docs/security_review.md](docs/security_review.md) — hardening review and residual deployment risks
- [docs/deployment.md](docs/deployment.md) — standalone and Agent-managed deployment
- [docs/out_of_box_parity_matrix.md](docs/out_of_box_parity_matrix.md) — strict runtime parity matrix
- [docs/runtime_dependencies.md](docs/runtime_dependencies.md) — packaged and external runtime boundaries

## License

Released under [Apache-2.0](LICENSE). Behavior-only reference use from
AGPL-3.0 dsh-knowledge and adopted third-party notices are documented in
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
