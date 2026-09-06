# Ingestion

## Sources

Knowledge supports text import, single and batch file import, URL import, and
directory import. Source bytes or recoverable text are owned by Knowledge's
raw store before indexing, so a document can be reindexed after configuration
changes or a restart.

| Source | Behavior |
|---|---|
| Text | Creates one document and immediately parses/chunks/indexes it. |
| Files | Batch requests are planned for title/content-hash conflicts and ingested by a bounded pool of at most five workers. |
| URL | Downloads current content, derives title when possible, and supports changed/unchanged refresh. |
| Directory | Tracks nested files as owned documents, supports rescan, repointing to a new source path, recursive deletion, and per-entry failures. |

## Formats

Built-in parsing covers `txt`, `md`, `mdx`, `csv`, `json`, and `log` with
UTF-8 and GB18030 fallback; HTML is reduced to structured text; DOCX, PPTX,
XLSX, and EPUB are read from their OOXML/EPUB containers. PDF text is extracted
in-process and checked with the upstream line-health heuristic. A fragmented
glyph stream is rebuilt from item coordinates when that produces healthy
lines; otherwise the native text is retained while OCR is attempted.

Two optional dependencies extend this path:

- **MinerU** can process PDFs when selected on a base and supplied an API key
  and host. A MinerU failure falls back to the local parsing/OCR chain.
- **OCR** runs in an optional helper process, with an optional
  deployment-supplied secondary command such as Tesseract. Modes are `auto`
  (healthy native text first, OCR fallback), `forced`, and `off`. When a
  deployment supplies `ocr.renderHelper`, Knowledge prefers bounded full-page
  PNG rendering before the OCR runtime. If that renderer or its response is
  unavailable, malformed, or produces no recognized text, Knowledge retries the
  PDF envelope and then bounded embedded page rasters as PNG inputs. Rasters
  are bounded to 100 pages, 200 images, and 512 MB of decoded pixels, then
  prepared with 2x low-resolution upscale, grayscale conversion, contrast
  stretch, and sharpening. Results are grouped by page and horizontal CJK
  spacing is folded without joining lines. The renderer is deployment-supplied;
  OCR failure never destroys text already recovered from the native parser,
  including a fragmented layer.

Legacy `.doc`, `.ppt`, and `.xls` require an explicitly configured external
converter; Knowledge does not bundle one. Without it they are reported as
unsupported rather than silently imported as empty content.

PDFs also accept an optional content-signature converter. For empty primary
text it runs before OCR; for fragmented native text it runs after OCR. This is
a deployment-provided helper, so the core remains independent and failures fall
back safely.

When configured, a vision model can describe embedded PDF figures and append
those descriptions to the indexed text. Knowledge decodes bounded JPEG and
sample-based PDF images, including common Flate/LZW/ASCII/RunLength filter
chains, predictors, Indexed palettes, CCITT Group4 and Group3 1D fax streams
with explicit EOL, 1/2/4/8-bit samples, Separation/DeviceN with sampled,
stitching, exponential, and calculator tint transforms, and matching soft-mask
alpha composition, before sending PNG data to the provider. Specialized JPEG
2000 and JBIG2 images are decoded only when the optional deployment-supplied
image decoder is configured; without it they are best-effort skips. PDF Pattern
is a painting color space, not an image XObject sample codec. Decoding and
captioning are
best-effort: unsupported rasters or provider failures never prevent import.

## Lifecycle

Documents move through `pending`, `processing` (with parsing/embedding
phases), and `ready` or `failed`. Model drift can mark chunks stale, and
interrupted work is marked incomplete. On startup Knowledge resumes recoverable
work, fails unrecoverable placeholders, reconciles chunk counts, and removes
orphan raw files.

Conflict handling supports `keep`, `replace`, and `rename`. Content hashes are
also used to avoid duplicate ingestion. Ingestion is performed by the job
manager, so Web requests and extension context calls are not blocked by large
imports.

## APIs

The REST surface is mounted under `/api`:

```text
POST /api/bases/{id}/documents           text import
POST /api/bases/{id}/files               one or more file items
POST /api/bases/{id}/url                 URL import
POST /api/bases/{id}/directories         directory import
POST /api/documents/{id}/refresh         URL refresh
POST /api/documents/{id}/rescan          directory rescan
POST /api/documents/{id}/reindex         document reindex
GET  /api/jobs/{id}                      job progress
POST /api/jobs/{id}/cancel               cancel a cancellable job
```

Responses contain stable lifecycle and error fields. Provider, parse, and
dimension failures appear on the document rather than causing the HTTP process
or Agent host to fail.
