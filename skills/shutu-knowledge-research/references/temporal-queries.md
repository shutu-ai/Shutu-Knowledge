# Temporal and Version Queries

## Decide the dimension first

- Product/source version ("in 0.2 vs 0.3") → version policies.
- Point in time ("as of March") → `as_of` with the version that was current then;
  if unknown, ask or search release notes before guessing.
- What changed between versions → `knowledge_compare_versions`.

`time_dimension` currently supports `source_version` only. Ingestion-time queries
are unsupported — say so rather than approximating.

## Policy cheat sheet

| Intent | version_policy | Notes |
|---|---|---|
| Current behavior | `{type: current}` (default) | evidence lane reads active documents |
| Any point in history | `{type: as_of, as_of: "0.2"}` | semantic lane filters; evidence lane stays current and degraded |
| Evolution / all history | `{type: all}` | keeps superseded units visible |
| Explicit range | `{type: range, from_version, to_version}` | inclusive; identities must be ordered |

## Rules

- Version identities are comparable strings like `0.2`, `v0.3`, `7.1`. If the
  knowledge base uses a different scheme, check `resolved_version` in search or
  context output before assuming ordering.
- `SEMANTIC_NOT_READY` or missing generations: report, fall back to evidence mode
  for current-only questions, and do not fabricate historical answers.
- Mixed-version answers must annotate every claim with its version; if the user
  asked for one version and evidence returned another, either re-search with an
  `as_of` policy or disclose the mismatch.
- Superseded facts are history, not errors: cite them as "superseded by X in Y"
  when they matter to the timeline, using `knowledge_compare_versions` output.