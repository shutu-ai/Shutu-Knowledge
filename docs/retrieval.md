# Retrieval

## Pipeline

`POST /api/search` executes:

1. query construction and optional extra variants,
2. lexical SQLite FTS5 trigram/BM25 retrieval,
3. vector cosine retrieval when a usable embedding provider is configured,
4. weighted Reciprocal Rank Fusion,
5. optional MMR diversification,
6. optional reranking,
7. context-window composition and serialization under a token budget.

Modes are `auto`, `hybrid`, `vector`, and `lexical`. A missing embedding
provider or vector-query failure degrades to lexical rather than making the
whole service unavailable. Explicitly empty enabled-base or document filters
return no results; they are never reinterpreted as unrestricted.

## Scoring

RRF uses `k=60`. The configured `rrfVectorWeight` controls the vector lane
weight relative to lexical. MMR trades relevance against near-duplicate
suppression. Search hits expose lexical, vector, fusion, rerank, and final
scores where applicable, plus elapsed time and selected mode.

The optional reranker may be remote OpenAI-compatible/Jina-style or an isolated
local helper. Scores are validated for count, index alignment, and the
inclusive `[0,1]` range. A timeout, invalid response, or provider error is
reported as `degraded`; the pre-rerank order remains usable. Three consecutive
failures open the circuit breaker for five minutes, followed by a bounded
half-open probe.

## Context

Each hit includes a composed context window around the anchor chunk. The
composer adds preceding and following chunks, prefers heading boundaries,
removes at least 24-character overlap, crops to a focus where supplied, and
enforces the serialized token budget. `GET /api/documents/{id}/context` supports
anchor continuation by chunk ID or index with explicit before/after/max-token
controls.

Every serialized context entry is source-attributed, and anchor text carries
the `>>>` marker. Knowledge estimates the token cost, while the Agent retains
final authority over its own model context budget.

## Automatic Retrieval

In extension mode an `on_user_input_change` provider plans retrieval for the
current turn. Policy includes a bounded history-enhanced variant, language and
strict-identifier gates, comparable lane relevance gates, same-topic
throttling, and deduplication against chunks already injected in the window.
Failures result in no injection; they never mutate session history or block the
Agent.

The Knowledge tools include `knowledge_search` and `knowledge_read_document`,
so an Agent can perform explicit retrieval through the normal approved tool
path when automatic retrieval is not sufficient.
