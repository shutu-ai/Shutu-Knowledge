# Shutu Knowledge v0.4.0 release report

Status: **PREPARING FINAL EVIDENCE** — the RC passed local validation, Windows
package smoke, and push CI. Final version packaging and tag CI are in progress.

## Research

Mechanism-only research covered WeKnora, RAGFlow, HippoRAG 2, LightRAG,
Microsoft GraphRAG, OpenSPG/KAG, Graphiti, RAPTOR, RAG-Anything, and
LlamaIndex PropertyGraph. Evidence and decisions are recorded in
`docs/0.4_reference_research.md` and `docs/0.4_gap_analysis.md`.

Adopted mechanisms were narrowed to provenance-safe compiled knowledge,
hierarchical orientation, temporal lifecycle, incremental projection maintenance,
regenerable wiki views, and benchmark-gated lightweight association. Copied
architectures, external graph infrastructure, broad extraction schemas, cloud
services, and mandatory LLM pipelines were rejected.

## Architecture

- **Knowledge Model:** versioned `KnowledgeUnit`, `Relation`, and immutable
  `Compilation`; facts, concepts, topics, summaries, pages, hierarchy, temporal
  status, and exact evidence.
- **Knowledge Compiler:** deterministic offline extract/normalize/merge/abstract
  pipeline; compiler/model/prompt identity is explicit.
- **Semantic Memory:** active-generation retrieval with concept/topic/summary
  activation and full derived-from evidence closure.
- **Context Compiler:** bounded ContextPackage with orientation, concepts,
  facts, relations, exact evidence, citations, token estimate, and diagnostics.
- **Query Routing:** deterministic `lexical-rules-v1` routes fact, local,
  global, cross-document, comparison, multi-hop, and temporal intents.

The 0.3 Evidence Layer remains authoritative. Every semantic unit either points
to exact IR/chunk evidence or resolves through a cycle-checked `derived_from`
closure.

## Benchmark

The immutable comparison baseline is `v0.2.1`
(`66d5a4fa9f05945b459235e171e7d43423eb302b`), persisted in
`docs/0.4_benchmark_0.2_baseline.json`. The 0.3 result equals that baseline.

Equal-budget aggregate results:

| Version | Support-quality proxy | Tokens |
|---|---:|---:|
| 0.2.1 | 4.500 | 454 |
| 0.3 | 4.500 | 454 |
| 0.4 | 5.667 | 436 |

Family results are non-regressing; global rises 1.000 to 1.667 and multi-hop
rises 1.000 to 1.500. Fact remains 1.000, cross-document is 0.500 under equal
stress (while improved at the default budget), and temporal-current remains
1.000. Expanded tests cover the document-intelligence corpus, SmartCare version
drift and causal chain, code-design causality, same-version contradiction,
historical recall, restart recovery, and explicit 0.3 migration.

## Context efficiency

At the equal per-query token cap, aggregate support quality improves 25.9%
while aggregate tokens fall from 454 to 436. The minimum useful budget is 64
tokens; exact evidence is reserved, packages are token-checked, and dropped or
duplicate evidence is reported in diagnostics.

## Migration

PASS. Explicit 0.3 database downgrade/upgrade tests reach additive schema 0020
without rebuilding chunks or Evidence Indexes and without changing chunk or
generation identity. Existing search remains operational with no semantic
compilation.

## Regression

PASS. Full Go, web, search, migration, semantic, race, and browser E2E suites
were exercised locally and/or in push CI. Fact, cross-document, and
temporal-current benchmark families do not regress; 0.3 API modes remain
unchanged.

## CI

- Push CI for RC candidate `46338297954eb16212a123b3109469304081b20e`:
  [`35379456813`](https://github.com/shutu-ai/Shutu-Knowledge/actions/runs/35379456813)
  — PASS.
- Final tag CI: PENDING.

## Artifact

Final artifact evidence: PENDING.

## Known limitations

- Semantic compilation is deterministic/lexical and does not use an LLM.
- Semantic unit search is lexical; embedding-backed unit retrieval is deferred.
- Relations are deliberately narrow and benchmark-gated.
- Wiki is a regenerated read-only view, not an editor or source of truth.
- Repeated-query reuse and human/LLM-judged answer scoring remain future work.

## Deferred

Advanced GraphRAG, domain ontology, logical reasoning, autonomous knowledge
maintenance, user feedback learning, memory consolidation, skill compilation,
and knowledge-to-agent planning are reserved for 0.5+.

## Release status

NOT READY — final exact-source package, package smoke, tag CI, and no-P0 audit
are not yet complete.

