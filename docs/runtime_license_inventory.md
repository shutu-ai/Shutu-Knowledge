# Runtime and Model License Inventory

The Knowledge source remains Apache-2.0. Runtime code is independently written
in `internal/runtime/assets/managed-runtime.mjs`; no dsh-knowledge runtime
source is copied.

| Component | Version / revision | License | Redistributed? | Downloaded? | Attribution | Status |
|---|---|---|---:|---:|---|---|
| Node.js | 22.14.0 | Node.js license (MIT plus bundled notices) | No; official archive is downloaded and checksum-verified | Yes, only when Node is absent | Node.js Foundation notices | Documented managed dependency |
| `@huggingface/transformers` | 4.2.0 | Apache-2.0 | No; npm package is installed into Knowledge data | Yes | npm package license and notices | Pinned package lock |
| `onnxruntime-node` | 1.24.3 transitive | MIT | No; installed transitively | Yes | Microsoft ONNX Runtime notices | Pinned package lock |
| `pdfjs-dist` | 6.3.289 | Apache-2.0 | No; installed into Knowledge data | Yes | Mozilla PDF.js notices | Pinned package lock |
| `@napi-rs/canvas` | 1.0.8 | MIT | No; installed into Knowledge data | Yes | npm package license and notices | Pinned package lock |
| `tesseract.js` | 7.0.0 | Apache-2.0 | No; installed into Knowledge data | Yes | npm package license and notices | Pinned package lock |
| `@firecrawl/anydoc` | 0.2.4 | MIT | No; installed into Knowledge data | Yes | npm package license and notices | Pinned native package |
| Qwen3 Embedding 0.6B ONNX | revision `c25a394dd583836952667c12f008335071b3f43d` | Upstream model terms; review before redistribution | No | Yes, first local embedding load | Upstream model card | SHA-256 verified |
| BGE Reranker Base ONNX | revision `280bcc27a84e0b898c251e06fddb25171bd9b101` | Upstream model terms; review before redistribution | No | Yes, first local reranker load | Upstream model card | SHA-256 verified |
| Tesseract `eng+chi_sim` traineddata | Tesseract language-data terms | No | Yes, first OCR load | Tesseract project | Managed private cache |
| LibreOffice | System-selected version | MPL-2.0 and bundled component notices | No | No | System installation owns notices | Auto-discovered system dependency |

Model weights are not committed to this repository or included in the Go
binary. The runtime pins revisions and validates the two production ONNX files
and tokenizers before recording `READY`; model-specific terms remain visible as
an installation-time responsibility rather than being silently relicensed by
Shutu-Knowledge.
