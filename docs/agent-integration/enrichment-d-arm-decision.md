# Selective LLM Enrichment (D-arm) — Design and Phase 8 Decision

Status: **Design complete; implementation NOT triggered — G7 precondition unmet
(documented decision, per plan §13/Phase 8).**

## G7 precondition check

Phase 8 may implement enrichment only when G7 proves a hard-question failure
class is caused by insufficient Fact/Relation semantic extraction. The Phase 7
arms showed:

- multi-hop and temporal questions fully covered at both the retrieval and the
  agent layer (4/4 correct with citations, including `compare_versions`
  verification),
- the negative result was that the deterministic semantic lane adds no gain over
  evidence on the toy corpus — an extraction-coverage issue, but one with no
  observed downstream failure to fix,
- no failure class was attributable to missing facts/relations.

**Decision: the D-arm precondition is unmet. Enrichment stays unimplemented and
disabled. Re-evaluate after a private large-corpus G7 rerun produces a
documented failure class with that signature.**

## Design (frozen for future implementation)

Pipeline (plan §9):

```text
Ready Document IR / Source → Candidate Units
  → selective LLM enrichment (opt-in provider, budget-capped)
  → schema validation (negation, units, conditions, temporal scopes)
  → evidence alignment to exact IR/chunk spans
  → duplicate/contradiction detection
  → confidence + source type → candidate generation
  → quality gates → atomic activation as a new immutable generation
```

Hard constraints carried from plan §9.1:

1. Facts support subject/predicate/object, conditions, modality, negation,
   unit/value, scope, exceptions, temporal applicability, provenance — but every
   field must be extractable from the source span; nothing is inferred into
   asserted fields.
2. Relations keep `structural/extracted/inferred` provenance; inferred is never
   upgraded to asserted by enrichment.
3. The LLM must return evidence spans or IR/chunk ids; a deterministic validator
   re-derives each claim from those spans before acceptance. Unparseable output
   is quarantined as candidates, never activated.
4. Full generation fingerprints (compiler/model/prompt/version, source hashes)
   are persisted; incremental recompiles, rollback, and idempotent reruns are
   required properties.
5. Offline mode is the default; any external provider requires explicit opt-in,
   a sensitive-data warning, a redaction policy, and a spend cap.
6. Effectiveness must be proven on the private benchmark (net benefit, false
   relation rate, tokens, latency) before the flag ships enabled; the default
   remains off.

## Implementation sketch (when triggered)

- New package `internal/enrichment` behind the `knowledge` compile step; the MCP
  surface never calls it at query time.
- Provider interface mirroring `internal/embedding.Provider`: local-first,
  pluggable, cancellable, budget-aware.
- Candidates land in the existing `knowledge_compilations` machinery as a new
  building generation; activation reuses `ActivateCompilation` semantics, so
  rollback is the existing generation swap.
- Gate: rerun A/B/C/D with the D arm on the same frozen gold set; D must show a
  pre-registered net benefit with zero false citations, or the module stays off
  and the negative result is recorded here.