# Shutu Knowledge v0.6.2 Release Notes

v0.6.2 is a maintenance release focused on **PDF ingestion correctness and
quality visibility**. It contains no new knowledge capabilities.

## PDF ingestion correctness

- **Unified PDF text quality evaluation.** Every extraction candidate
  (native plain, coordinate reassembly, OCR) is scored by the same quality
  evaluator: replacement-glyph ratio, line structure, fragment ratio, and
  per-page density.
- **A single damaged glyph no longer destroys a document.** Isolated U+FFFD
  characters (typically ~1% of glyphs in real-world exports) are stripped and
  recorded as a warning instead of rejecting ~99% healthy text.
- **OCR is a fallback, not a replacement.** OCR output may only replace a
  native text layer when it is itself readable and measurably improves
  coverage. A smaller or more fragmented OCR result can never overwrite
  better native evidence.
- **Fragmented-but-recoverable PDFs are rebuilt.** Per-glyph PDF text
  objects (common in generated documentation) are reassembled into lines by
  coordinate clustering before any OCR escalation.

## Quality visibility

- Documents persist extraction quality metadata: `quality_status`
  (GOOD / WARNING / LOW_QUALITY / INCOMPLETE / UNKNOWN), `quality_score`,
  `quality_warnings`, `extraction_method`, `pages_total`, `pages_ocr`, and a
  `quality_partial` flag.
- Partial OCR (renderer page budget reached) raises
  `OCR_PAGE_LIMIT_REACHED` and caps quality at WARNING — partial extraction
  is never reported as complete success.
- A catastrophic-loss detector marks documents with many pages but almost no
  extracted text as INCOMPLETE instead of GOOD.
- The document list UI shows the quality status; directory-import operation
  results include a per-import quality summary.
- Retrieval demotes chunks from LOW_QUALITY documents and excludes INCOMPLETE
  ones: bad evidence is often worse than missing evidence.

## Chunk hygiene

Short fragment chunks (layout debris such as stray page furniture) merge into
neighboring chunks instead of standing alone in the index. Short but
meaningful content (commands, error codes, parameter values, table cells) is
preserved.

## Migration

Existing 0.6.1 data homes upgrade in place (schema v21). Documents imported
before the upgrade report quality `UNKNOWN`; re-import to compute quality.

## Notes

- `embedding.provider: none` (the default) means lexical/BM25 retrieval
  only; semantic retrieval requires configuring an embedding model. This is
  unchanged behavior, now documented explicitly.
