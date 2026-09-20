# PDF Ingestion Real-World Failure Analysis (fixed in v0.6.2)

Corpus details are aggregated on purpose; no proprietary content, excerpts,
screenshots, or file contents from the private corpus are reproduced here.

## Symptom

A real-world 9-PDF / ~11,859-page / ~330 MB product-documentation corpus was
imported with v0.6.1. All nine import operations completed, none reported an
error, and every document was marked `ready` with `incomplete = 0`.

Measured against an independent reference extraction (~9.75M characters of
text-layer content), only ~34% of the corpus text survived. Seven of nine
documents retained between ~0.04% and ~8% of their reference-scale text, and
the retained chunks were largely OCR debris (20–91% of chunks under 10
meaningful characters). Retrieval against those documents returned garbage
chunks or no hits at all.

## Root causes

1. **Weak primary extraction shape.** The primary pure-Go PDF reader emits
   one text item per glyph for this corpus, so the "plain" extraction had an
   average line length near 1 and was classified as fragmented.
2. **Single U+FFFD veto.** About 1% of glyphs lacked usable ToUnicode
   mappings and decoded to U+FFFD. The v0.6.1 health gate rejected any
   candidate containing a single replacement rune — including the coordinate
   reassembly that had in fact recovered ~99% usable text. One damaged glyph
   destroyed an otherwise healthy document.
3. **OCR replaced native text unconditionally.** When the parser flagged the
   layer as needing OCR, any non-empty OCR result replaced the native text.
   There was no quality or coverage comparison.
4. **Silent 100-page OCR ceiling.** Rendered-page OCR stops at 100 pages per
   document. For 306–3,645 page documents this was at most ~3–33% page
   coverage, reported as success with no warning.
5. **No quality visibility.** Nothing in the data model, API, or UI could
   express "completed but under-extracted", so catastrophic loss looked
   identical to a healthy import.

## Fix (v0.6.2)

- Unified `PDFTextQuality` evaluator used by every extraction candidate;
  replacement glyphs are a ratio-bounded quality signal (isolated U+FFFD is
  stripped), never a binary veto.
- Candidate selection: healthy native plain → coordinate reassembly →
  (only then) OCR escalation; the reassembly path is labelled in parser
  metadata.
- OCR replacement safety rule: OCR wins only when it is itself readable and
  measurably improves coverage; a smaller OCR result can never overwrite a
  larger native layer.
- OCR page coverage is tracked (`pages_total`, `pages_ocr`,
  `quality_partial`); partial OCR raises `OCR_PAGE_LIMIT_REACHED` and caps
  the quality status at WARNING.
- Persisted quality model per document (`quality_status`,
  `quality_score`, `quality_warnings`, `extraction_method`, page counters)
  with statuses GOOD / WARNING / LOW_QUALITY / INCOMPLETE / UNKNOWN, plus a
  catastrophic-loss detector (many pages + almost no text can never be GOOD).
- Chunk hygiene: fragments merge into neighbors instead of standing alone in
  the index; retrieval demotes LOW_QUALITY chunks and drops INCOMPLETE ones.

## Before / after evidence (aggregated, private corpus)

- Native/reassembled coverage: 2/9 documents healthy → 9/9 documents healthy.
- Seven failed documents moved from ~0.04–8% to ~90–94% reference-scale
  coverage each (per-document table in the release report).
- OCR pages: up to ~11,859 rendered+OCR attempts in v0.6.1 → 0 in v0.6.2 for
  this corpus (text-layer PDFs never enter OCR).
- Import time: ~37 minutes → minutes-scale.
- Fragment chunks (<10 meaningful chars): 20–91% per broken document →
  merged/absent.

## Remaining limitations

- Scanned PDFs still depend on OCR and are bounded by the renderer page
  budget per call; partial coverage is now visible but OCR batching across
  calls is future work.
- Quality classification is heuristic; image-heavy legitimate documents can
  surface WARNING (LOW_TEXT_DENSITY) rather than GOOD.
- Legacy documents imported before v0.6.2 report UNKNOWN until re-imported.
