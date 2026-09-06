# Third-Party Notices

This project is released under Apache-2.0; see [LICENSE](LICENSE).

## Reference projects (read-only, not distributed)

| Project | License | Usage |
|---|---|---|
| [dsh-knowledge](https://github.com/Soren-ABT/dsh-knowledge) v0.3.9 (commit `95e4a135cca3282b345c6d12a8e09cc4a314402f`) | AGPL-3.0 | Functional/behavioral reference only. Audited for capability inventory. No source code copied or distributed by this project. |
| [shutu-agent](https://github.com/shutu-ai/shutu-agent/releases/tag/v0.2.1) | Apache-2.0 | v0.2.1 is the required public Go dependency for Extension Protocol v1 and `sdk/extension`. Knowledge links to the published module and does not modify or redistribute the Agent application. |

Because dsh-knowledge is AGPL-3.0 and this project is Apache-2.0, the safe posture mandated by the project requirements remains: **no direct copying of dsh-knowledge source**. All behavior is reimplemented from the capability inventory with original code. If that policy ever changes, AGPL-3.0 obligations (license propagation, corresponding source, attribution) must be satisfied before any distribution.

## Third-party dependencies

Runtime dependencies are recorded below. The vendored/pinned reference
repositories are read-only inputs and are not distributed by this project.

| Module | Version / source | License | Used for |
|---|---|---|---|
| `github.com/ledongthuc/pdf` | `v0.0.0-20260903153007-b3c860c23753` | BSD 3-Clause (“Go Authors”) | PDF text-layer extraction |
| `golang.org/x/image` | `v0.45.0` | BSD 3-Clause | Pure-Go CCITT fax raster decoding |
| `golang.org/x/net`, `golang.org/x/text` | Go.org x repositories | BSD 3-Clause | HTTP/HTML parsing and character encoding |
| `gopkg.in/yaml.v3` | `v3.0.1` | MIT / Apache-2.0 | Knowledge-owned configuration and manifest serialization |
| `modernc.org/sqlite` and transitive `modernc.org/*` runtime | `v1.50.0` | BSD 3-Clause | Pure-Go SQLite storage |
| Web frontend | zero runtime npm dependencies; Node.js build/test scripts only | MIT for Node.js when redistributed with a binary | Independent web build and contract test |

The modernc.org/libc distribution also carries its own
`LICENSE-3RD-PARTY.md`; those terms apply to the files it identifies. The Go
module distribution of shutu-agent `v0.2.1` includes its Apache-2.0 `LICENSE`;
the earlier v0.2.0 licensing gap is retained in the release audit as history,
but does not apply to the dependency used by this release.

## Optional models and external components

| Component | Distribution boundary | License / terms |
|---|---|---|
| PaddleOCR PP-OCRv5 mobile artifacts | Optional model download managed by the deployment; not bundled in this repository | Source and model/weight terms apply separately; verify the exact artifact and version before redistribution. |
| Embedding and reranker models | Optional operator-selected downloads or remote services | Model repository/provider terms apply separately; not covered by this project's Apache-2.0 file. |
| Ollama | Optional external runtime/service | Operator-selected version and service terms; not bundled. |
| MinerU | Optional remote processing service | Service/upstream terms apply; the AGPL-capable mupdf path observed in the reference is not a Knowledge dependency. |
| Tesseract or another OCR command | Optional deployment-provided executable | The operator-selected binary and version govern its terms; not bundled. |

This is maintained from `go.mod` and `web/package.json`. Additions must add a
row before release; transitive license changes must be re-checked.

Upstream dsh-knowledge dependencies relevant to behavior parity research (not dependencies of this project unless independently adopted): pdf-parse, pdfjs-dist, mupdf (AGPL-3.0), mammoth, word-extractor, @firecrawl/anydoc, jszip, turndown, tesseract.js, ppu-paddle-ocr, ppu-ocv, onnxruntime-node, @huggingface/transformers, undici.
