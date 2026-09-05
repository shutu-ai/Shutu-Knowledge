# Third-Party Notices

## Reference projects (read-only, not distributed)

| Project | License | Usage |
|---|---|---|
| [dsh-knowledge](https://github.com/Soren-ABT/dsh-knowledge) v0.3.9 (commit `95e4a135cca3282b345c6d12a8e09cc4a314402f`) | AGPL-3.0 | Functional/behavioral reference only. Audited for capability inventory. No source code copied or distributed by this project. |
| [shutu-agent](https://github.com/shutu-ai/shutu-agent) | per its repository | Agent base. This project links against the public `sdk/extension` package at runtime/build time; the Agent itself is not modified or redistributed here. |

Because dsh-knowledge is AGPL-3.0 and this project has not elected an AGPL-compatible license, the safe posture mandated by the project requirements is: **no direct copying of dsh-knowledge source**. All behavior is reimplemented from the capability inventory with original code. If that policy ever changes, AGPL-3.0 obligations (license propagation, corresponding source, attribution) must be satisfied before any distribution.

## Third-party dependencies

To be maintained automatically from `go.mod` / `web/package.json` as dependencies land (Go module notices and npm package licenses). None recorded yet at Phase 0.

Upstream dsh-knowledge dependencies relevant to behavior parity research (not dependencies of this project unless independently adopted): pdf-parse, pdfjs-dist, mupdf (AGPL-3.0), mammoth, word-extractor, @firecrawl/anydoc, jszip, turndown, tesseract.js, ppu-paddle-ocr, ppu-ocv, onnxruntime-node, @huggingface/transformers, undici.
