# Tool Contracts

All tools are read-only and return one envelope:
`{status, trace_id, data, context, warnings, degraded, error}`.
`status:"error"` carries `error.code`; `degraded:true` means the answer is partial.
Remember `trace_id` when debugging with `knowledge_diagnostics`.

## knowledge_list_bases

Input: `page_size`, `cursor`, `include_status`. Returns authorized bases with
`evidence_ready`, `semantic_status` (`ready`/`not_compiled`), `active_generation`,
`structured_model_count`. Never guess base IDs from memory — list first.

## knowledge_capabilities

Input: `base_ids?`. Returns modes, per-base readiness, generation ids, limits
(hops 4, nodes 256, fan-out 64, expand roots 8). Use it to decide whether semantic
navigation is available before promising concept maps.

## knowledge_search

Input: `query`, `base_ids?`, `mode` (`evidence`|`semantic`|`hybrid`|`auto`),
`top_k`, `version_policy {type: current|all|as_of|range, as_of, from_version,
to_version}`, `generation` (`active` or number), `evidence_mode`, `include_structured`.

Key fields: `effective_mode`, `evidence.hits[]` (id, score, document_id, chunk_id,
index_generation, source_version, citation), `semantic[].hits[]` (unit_id, kind,
score, evidence pointers), `snapshots`, `degraded`, `warnings`.

Pitfalls:
- `mode=semantic` with no compiled generation returns `SEMANTIC_NOT_READY` — fall
  back to evidence and say so; never pretend semantic results existed.
- Non-current version policies mark the evidence lane degraded (it reads active
  documents). Use `knowledge_evidence` for exact historical excerpts.
- `auto` may answer with evidence only; check `effective_mode` before describing
  what you searched.

## knowledge_explore

Input: `query`, `base_ids?`, `relation_types?`, `direction` (`out`|`in`|`both`),
`max_hops`, `max_nodes`, `fan_out`, `exclude_conflicts`, `version_policy`.

Returns per-base graphs: `seeds`, `nodes` (unit + hops + evidence status),
`edges` (relation + evidence + `conflict` flag), `paths`, `truncated`.
Nodes/edges carry `evidence` pointers or explicit `projection_only` status.

Pitfalls:
- `conflict:true` on an edge means a contradicts relation exists. It never tells
  you which side is true — resolve both sides with evidence.
- `truncated:true` means the graph was cut; widen limits only if needed and report
  the cut in the answer.

## knowledge_expand

Input: `root_unit_ids` (max 8), same traversal options, default `max_hops=2`.
Returns per-root graphs; a missing root reports `UNIT_NOT_FOUND` for that root
while others still expand. Use expand after explore when one unit is clearly the
hub; do not expand every seed.

## knowledge_evidence

Input: `unit_id` or `document_id` (+`chunk_id`), `base_ids?`, `generation?`,
`max_chars`. Returns `document_name`, `support_status:"resolved"`, `excerpt`,
`content_hash`, generation/version, and `supporting_pointers` for units.

Failure is informative: `EVIDENCE_UNRESOLVED` means the pointer does not resolve at
that generation — do not retry the same pointer into a different answer; find the
current pointer via search.

## knowledge_context

Input: `query`, `base_ids?`, `token_budget`, `version_policy`, `generation`.
Returns one bounded package per base: orientation (`concepts`, `facts`,
`relations`), `evidence` with citations, `rendered_context`, budget stats.
Use it to summarize a topic or prime a long research task. `rendered_context`
mixes evidence and semantic orientation — only the evidence entries are citations.

## knowledge_compare_versions

Input: `base_ids?`, `from`, `to`, `query?`, `include_evidence`,
`time_dimension` (only `source_version` today).
Returns `added`, `removed`, `changed` (before/after units), `unchanged_count`,
`conflicting`, `uncertain`.

Pitfalls:
- `uncertain:true` means version identities were missing or unordered — the diff
  is incomplete, say so.
- `removed` entries are proven by superseded history; absence alone is never
  reported as deletion.

## knowledge_diagnostics

Input: `trace_id?`, `limit?`, `include_details?`. Returns recent tool executions
(routing, elapsed, degradation, warning codes). Query text is never stored.
Use it to explain slow or degraded runs to the user.