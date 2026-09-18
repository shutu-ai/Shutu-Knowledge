# Document intelligence golden corpus

This corpus is intentionally small and reviewable. The checked-in PDF and
Office containers are deterministic, generated from the adjacent XML sources
by `scripts/generate_document_intelligence_corpus.go`, and are consumed
directly by the parser golden test. Regenerate them with:

```powershell
go run -tags corpusgen ./scripts/generate_document_intelligence_corpus.go
```

The manifest names the release-gate cases and their semantic assertions.

The source set covers English, Chinese, and mixed text, page/heading anchors,
DOCX headings and table cells, eight PPTX slides, and an XLSX named sheet with
cell and row anchors. `scanned.pdf` remains a deterministic text-layer
stand-in for the OCR fallback path; OCR/rendering is an optional local runtime
and is tested separately.
