# Shutu-Knowledge 0.6.0 Release Notes

Shutu-Knowledge 0.6.0 adds evidence-grounded temporal range and historical
reasoning on top of the 0.5 version-aware knowledge model.

## New capabilities

- Version and time range models with open/closed boundaries.
- Ambiguous history intent that preserves uncertainty instead of selecting a
  single version.
- Relative `before`, `after`, `until`, and `since` boundaries resolved from
  evidence.
- Provenance-backed historical phases and transitions.
- A budgeted history context compiler with representative evidence per phase.

## Validation

The dedicated 84-query range/history benchmark passes 84/84 across range,
before, after, early, recent, full-history, phase, relative-event, and
ambiguous categories. The original 248-query temporal regression improves from
90.5% to 96.8% strict temporal accuracy, with 100% conflict and unknown-version
correctness, no unsupported claims, and no invalid audited provenance.

## Compatibility

There are no breaking API, storage, agent-contract, or configuration changes.
Existing 0.5 knowledge bases continue to open without a forced migration.
