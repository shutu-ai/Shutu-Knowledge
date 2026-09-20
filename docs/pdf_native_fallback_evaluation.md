# PDF Native Fallback Evaluation (v0.6.2)

Task requirement: before reaching for OCR, PDF ingestion must try a stronger
native text path. This note records the candidate audit and the final
decision, per the release task ("Strong Native Fallback", section 16).

## Problem shape (real corpus)

The SmartCare Suite 7.1.0 corpus (9 PDFs, ~11,859 pages) uses PDF text
objects where most glyphs are emitted as individual text items and roughly
1% of glyphs lack usable ToUnicode mappings. The primary pure-Go reader
(`github.com/ledongthuc/pdf`) extracts the characters but fragments them, and
v0.6.1 rejected whole documents for the damaged-glyph share, falling back to
rendered-page OCR that covered at most 100 pages per document.

## Candidates considered

| Candidate | License | Distribution / packaging | CGO / runtime deps | Compatibility notes |
| --- | --- | --- | --- | --- |
| MuPDF via CGO | AGPL / commercial dual | Requires cross-compiled C toolchain per platform | Yes, heavy binary-size and licensing impact | Excellent extraction, unacceptable license/packaging cost for an Apache-2.0 product |
| PDFium via shared library | BSD-3 | Requires shipping a per-platform DLL/shared object | Runtime dependency, loader failure modes on Windows | Strong, but adds an out-of-box runtime surface for a case the existing reader can already cover |
| External `pdftotext` (poppler) | GPL-2.0 helper | External process, not redistributable in the Windows package | Process boundary | GPL helper conflicts with the out-of-box packaging posture |
| Improve in-house candidate selection over the existing pure-Go reader | Apache-2.0 (already a dependency) | No new artifacts | None | Adds per-page coordinate reassembly + quality scoring; validated against the real corpus |

## Decision

No new dependency. v0.6.2 formalizes the in-house fallback chain:

1. Candidate A: native plain text walk (`GetPlainText`).
2. Candidate B: per-page coordinate reassembly (`reassemblePDFPages`) —
   y-band clustering by median glyph height, x-ordered joining; this
   reconstruction is what makes per-glyph PDFs readable.
3. Both candidates are scored by the shared `PDFTextQuality` evaluator
   (replacement-ratio tolerance, line structure, fragment ratio, density).
4. OCR remains the last resort and may only replace a native layer when it
   measurably improves coverage and quality (`ocrCandidateWins`).

Measured against the SmartCare corpus (reference extraction via an
independent reader, ~9.75M chars): every document now ingests natively at
~90–94% reference-character-scale, OCR page count drops to 0, and import time
falls from ~37 minutes to minutes-scale. The remaining ~6–10% delta against
the reference extractor is reading-order/whitespace/glyph-normalization
noise, not content loss.

## When to revisit

If a future corpus shows genuine per-page extraction failures that neither
candidate can cover (damaged content streams, CID-keyed fonts with no
ToUnicode at all), the PDFium shared-library route is the preferred next
step, gated behind the existing optional runtime machinery so it never
becomes a startup dependency.
