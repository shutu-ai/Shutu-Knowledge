# Shutu-Knowledge 0.5.1 Release Notes

Shutu-Knowledge 0.5.1 is a maintenance release. It improves temporal evidence
selection after real-world validation and keeps the 0.5 architecture unchanged.

## Improvements

- Preserve explicit and historical version scope instead of silently falling
  back to another version.
- Exclude versions with unknown ordering from current-version resolution.
- Handle combined release-filename identities more conservatively.
- Project exact provenance from version-matched Knowledge Units when retrieval
  splits version tokens.
- Normalize compact 3GPP filenames such as `v181200p` to `18.12.0`.

## Validation

The release includes a 248-query real-world temporal/version validation suite,
20 conflict fixtures, authority/date controls, and regression coverage for the
targeted fixes. Validation measured strict temporal accuracy at 90.5%, conflict
accuracy at 100%, unknown-version correctness at 100%, and valid provenance on
all 170 audited temporal units.

## Compatibility

There are no breaking API, storage, agent-contract, or configuration changes.
No new temporal architecture, parser, knowledge model, or UI workflow is
included.
