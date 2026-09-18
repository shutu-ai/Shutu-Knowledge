# Shutu Knowledge v0.3.1 release report

## Release identity

- Implementation commit: `a4aa1267f43c33fd197ced9f5fa37dad81d78375`
- Validated candidate: `v0.3.0` / `f1afd9a3b4b49c83a6581d3964b68ce5bd78c799`
- Documentation and release closure commit: `2f82808b1cac3dae4dc24f4e9be9954ed1e464ef`
- Formal tag: `v0.3.1`, annotated tag object `5b36accbac8b87783d14e3883b0e38095696b777`,
  pointing to closure commit `2f82808b1cac3dae4dc24f4e9be9954ed1e464ef`
- `v0.3.0` unchanged: YES; its annotated tag object remains
  `8d397f43805a6d3240155dee57da11c17721752b`

## Remote validation

- Source Push CI `35337424796`: PASS, head `2f82808b1cac3dae4dc24f4e9be9954ed1e464ef`
- `v0.3.1` Tag CI `35339515710`: PASS, including Build, Race, Benchmark,
  Browser E2E, managed runtime, Windows/Ubuntu/macOS release-host acceptance,
  formal package verification, and static audit
- No P0 found: PASS

## Release artifact

- Filename: `shutu-knowledge-0.3.1-windows-amd64.zip`
- Source: `v0.3.1` / `2f82808b1cac3dae4dc24f4e9be9954ed1e464ef`
- Size: `12,307,456` bytes
- SHA-256: `f140441583f4f1851c5923e30760a9ae096bd78e3668d72ec6de0b924d954b80`
- Package smoke: PASS — extract, startup, text/OCR/DOCX/PPTX/XLSX import,
  embedding, online retrieval, runtime status, and offline restart retrieval

## GitHub Release

- Status: CREATED
- Name: `Shutu Knowledge v0.3.1`
- URL: https://github.com/shutu-ai/Shutu-Knowledge/releases/tag/v0.3.1
- Asset uploaded: YES

## External Agent Host

- Acceptance status: NOT CLAIMED
- Release blocking: NO
- Follow-up integration validation: YES

Cross-repository external Agent Host acceptance was not independently run. It
is a non-blocking post-release integration validation item, not a Shutu-
Knowledge 0.3 release gate.

## Scope and maintenance

- BUSINESS CODE CHANGED: NO
- Release Notes: `docs/release_notes_0.3.1.md`
- The 0.3.x line is in maintenance mode. New capabilities and architecture
  work belong to 0.4+.
- `fix04.md` and all user task documents were preserved and not committed.

## Final status

`RELEASED`
