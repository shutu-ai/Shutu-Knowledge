# Sourced Answer Template

Use this shape for research answers. Keep sections that apply; drop the rest
explicitly rather than leaving them empty.

```markdown
## Answer

<one or two sentences: the direct conclusion>

## Evidence

- <claim> — [C1] <document title>, <chunk/locator>, <version/generation>
- <claim> — [C2] ...

## Reasoning chain (multi-hop only)

1. <sub-claim> — document_asserted — [C1]
2. <sub-claim> — inferred_for_navigation → resolved by [C3]
...

## Version applicability

- Claims C1, C2 hold as of <version>; changed in <version> (see compare output).

## Conflicts / caveats

- <conflicting statement A> [C4] vs <statement B> [C5]; <why both exist /
  conditions>.

## Not verified

- <point> — searched <phrasings/modes>, no direct source found (unresolved).

Tools used: knowledge_search, knowledge_explore, knowledge_evidence
(traces: <trace_ids>).
```

Rules: every [Cn] must map to a pointer that appeared in tool output; if a section
has no content, write "none found" with the search paths tried instead of
deleting the section silently.