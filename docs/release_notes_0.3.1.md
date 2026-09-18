# Shutu Knowledge v0.3.1

Shutu Knowledge 0.3.1 is the formal Windows x64 release of the Document
Intelligence Foundation.

## Major

- Document IR v1 with deterministic source anchors and structure metadata
- Structured PDF, DOCX, PPTX, and XLSX parsing
- Structure-aware chunking and structured citation
- Existing hybrid retrieval integration
- Windows x64 release artifact with managed runtime support

## Compatibility

- Builds on the 0.2.x retrieval, storage, and Agent Extension Platform
  foundation
- The immutable `v0.3.0` tag remains the validated candidate; `v0.3.1` is the
  formal release metadata closure

## Known limitations

- Cross-repository external Agent Host acceptance was not independently
  completed. It is a non-blocking post-release integration validation item and
  is not claimed as PASS.
- Advanced LLM-Wiki, GraphRAG, and Knowledge Graph capabilities are not part
  of 0.3 and belong to 0.4+.

The 0.3.x line is now in maintenance mode. Bugfixes, security fixes, data
correctness fixes, parser/citation correctness fixes, compatibility fixes, and
packaging fixes are accepted; new capabilities and architecture work belong
to 0.4+.
