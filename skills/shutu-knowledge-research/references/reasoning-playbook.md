# Reasoning Playbooks

## Cross-document concept research

1. `knowledge_search` (hybrid, top_k 8-12) to find where the concept lives.
2. `knowledge_explore` from the query to see the concept graph across documents.
3. Pick the 2-4 highest-value nodes; `knowledge_evidence` each source document.
4. Synthesize per document; if accounts differ, present both with citations and
   applicability conditions instead of averaging them.

## Multi-hop questions (why / cause / impact)

1. Split the question into verifiable sub-claims before searching.
2. For each sub-claim: search → resolve evidence. Each hop needs a direct source.
3. Use `knowledge_expand` from the hub unit with `relation_types` narrowed to the
   relations the hypothesis needs (for example `supersedes`, `related_to`).
4. In the answer, print the chain as: claim → source(s) → next claim. Mark each
   link `document_asserted` when a document states it, `inferred_for_navigation`
   when you chose the path. Both links and navigation are visible; only asserted
   links are facts.
5. A hop without a direct source stays `unresolved` — never bridge it with world
   knowledge presented as if the knowledge base said it.

## Fault diagnosis

1. Restate the symptom precisely (component, version, error codes).
2. Search the symptom text and each candidate error code separately (evidence
   mode); codes retrieve better as exact strings.
3. For each candidate cause found: record its supporting evidence and look
   actively for counter-evidence (search the negation or adjacent sections).
4. Output: probable cause (with evidence), alternative causes considered (with
   evidence or why rejected), and verification steps the user can run. Never claim
   you executed anything; you have no execution tools.

## Structured data questions

1. `knowledge_search` with `include_structured:true` to find tables/fields.
2. Verify real field names in the structured response before drafting SQL.
3. Output proposed SQL / data models clearly labeled as proposals; state that they
   were not executed. Executing queries requires a separate authorized tool.

## No answer / low confidence

After at least two distinct search phrasings plus one explore attempt with no
usable evidence:
- Say the knowledge base does not contain an answer to this question.
- List what you searched (phrasings, bases, modes) and what came closest.
- Offer what would be needed (document types, versions) rather than guessing.