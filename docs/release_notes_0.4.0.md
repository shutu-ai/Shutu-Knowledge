# Shutu Knowledge v0.4.0

Shutu Knowledge 0.4.0 is the formal Knowledge Compiler and Semantic Memory
release. It adds optional, local-first knowledge compilation on top of the
authoritative 0.3 Evidence Layer without changing the existing search contract.

## Major

- Knowledge model with Facts, Concepts, Topics, Summaries, generated pages,
  temporal lifecycle, provenance, and immutable compilation generations
- Deterministic offline knowledge compiler requiring no LLM or weight training
- Incremental compilation with durable update/delete queues and no evidence-index
  rebuild
- Semantic retrieval, deterministic query routing, bounded relation traversal,
  and compiled evidence-backed ContextPackages
- Read-only Living Wiki projection and Web Knowledge Explorer
- New semantic REST APIs and read-only Agent tool
  `knowledge_compile_context`
- Real 0.2/0.3 comparison showing aggregate support quality 4.500 to 5.667
  (+25.9%) while aggregate context tokens fall from 454 to 436

## Compatibility

- 0.3 databases migrate additively through schemas 0019/0020.
- Existing BM25/vector/hybrid search, citations, and parsing continue to work.
- Compilation is explicit and optional; a migrated KB with zero semantic rows
  remains fully usable for 0.3 retrieval.
- Document IR, chunks, and Evidence Indexes are not rebuilt.
- SQLite/local-first architecture is preserved; no graph database, external
  vector service, queue service, or cloud dependency is introduced.

## Known limitations

- Compilation is deterministic and lexical rather than LLM-enriched.
- Relations are intentionally limited to benchmark-justified adjacent sequence
  facts within one IR node, with at most two-hop traversal.
- Wiki is read-only and regenerated; manual Wiki editing and revision history
  are deferred.
- The benchmark uses reproducible support-quality and token proxies, not
  human/LLM answer judging.

## Deferred to 0.5+

Advanced GraphRAG, rich ontologies, logical theorem proving, autonomous
knowledge maintenance, feedback learning, memory consolidation, and
knowledge-to-agent planning.

