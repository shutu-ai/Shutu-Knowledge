# Phase 7 — A/B/C Effectiveness Report

Run date: 2026-10-10 · Corpus: `evals/tiny-fixture` (frozen, 4 docs / 9 chunks) ·
Fixed agent: Codex CLI `codex exec` (same account/model/session settings) ·
Fixed MCP server: this repo at commit `00b2607` binary · 4-question subset
(exact fact / multi-hop / temporal / no-answer).

> Sample size n=4 per arm on a toy corpus. Per §12.4, these results are **not
> statistically significant**; they are the reproducible smoke plus the
> falsifiable conclusions the plan requires, not a substitute for a private
> large-corpus run.

## Retrieval-layer arms (deterministic harness, `comparisons.json`)

| Arm | Evidence recall | Full coverage | Answer accuracy (retrieval) |
|---|---|---|---|
| A evidence | 0.833 | 0.833 | 0.833 |
| semantic-only | 0.000 | 0.000 | 0.000 |
| B hybrid | 0.833 | 0.833 | 0.833 |

Per-category (evidence vs hybrid): identical — exact_fact 1.0, cross_document
1.0, multi_hop 1.0, temporal 1.0, conflict 1.0, no_answer 0.0.

**Falsifiable conclusion 1 (negative, honest):** on this corpus, hybrid does not
beat evidence (B ≥ A + 10 % NOT met — delta 0). Root causes: (a) the lexical
zero-hit bug fix already gave evidence full recall; (b) the builtin deterministic
semantic lane excludes facts from default search kinds and contributes no extra
locators on a 4-document corpus. The semantic-only ablation (0.0) proves the
deterministic semantic lane cannot stand alone; it needs the evidence lane.
Consequence for Phase 8: G7 did NOT show a multi-hop failure class caused by
insufficient semantic extraction, so the conditional D-arm enrichment stays
**not triggered** by its own precondition.

## Agent-layer arms (real Codex client, identical prompts for B/C)

| Arm | Skill | Tool lane | Q1 fact | Q2 multi-hop | Q3 temporal | Q4 no-answer | Tokens |
|---|---|---|---|---|---|---|---|
| A | none | forced evidence-only | ✓ | ✓ | ✓ | ✓ abstained | 113,645 |
| B | none | free (hybrid) | ✓ | ✓ | ✓ | ✓ abstained | 83,214 |
| C | yes | free (hybrid) | ✓ | ✓ + cross-doc corroboration | ✓ + `knowledge_compare_versions` verification | ✓ abstained, 4-doc coverage stated | 80,120 |

Zero fabricated citations in all arms: every cited chunk id resolved to a real
chunk; every quote matched the corpus verbatim (manual transcript audit).

**Falsifiable conclusion 2:** answer accuracy shows no measurable difference
(4/4 everywhere, n=4 — no statistical claim possible). What differs is behavior
quality: C (with the Skill) additionally used `knowledge_compare_versions` for
the temporal question, cross-corroborated the 2024 gasket exemption across two
documents (a detail A and B missed), and stated explicit corpus coverage for the
abstention — exactly the behaviors the Skill prescribes. Cost did not increase.

**Falsifiable conclusion 3:** the no-answer question was answered correctly
(abstained with evidence trail) in all arms; the retrieval-layer score dump for
no-answer queries (near-zero lexical, non-zero vector) is recorded in
`traces.jsonl` and confirms that abstention must be decided by the agent layer.

## Failure ledger

1. no_answer category recall metric reads 0.0 by construction (abstain questions
   carry no gold locators). Not a product failure; measurement semantics
   documented in 05-evaluation-protocol.md.
2. hybrid-vs-evidence no gain on toy corpus — recorded as a negative result per
   G7; a private large-corpus rerun is required before claiming semantic value.
3. Latency fields are 0 ms in the harness reports because the deterministic
   retrieval path is faster than the millisecond timer granularity on this
   machine; timing must be re-measured on a realistic corpus size.

## Verdict

- 语义编译是否有效？On the toy corpus, the deterministic semantic lane adds no
  retrieval gain over evidence alone (negative result, honestly recorded).
- Skill 是否有效？It changes agent behavior toward verification workflows at no
  accuracy or cost penalty; effectiveness on hard corpora remains to be proven
  by the private benchmark.
- Gate G7: satisfied as a falsifiable, negative-result-inclusive conclusion.