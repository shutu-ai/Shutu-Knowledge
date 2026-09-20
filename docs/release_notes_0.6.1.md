# Shutu Knowledge 0.6.1 Release Notes

Shutu-Knowledge 0.6.1 is a maintenance release for durable operation recovery and
task-status reporting.

## Improvements

- Automatically requeue retryable durable operations that are interrupted
  without a durable cancel request.
- Preserve the bounded three-attempt recovery limit and existing cancellation
  semantics.
- Treat `interrupted` as a terminal task state in the web task list so it no
  longer remains counted as an active document task.
- Add a Windows package trial and verification guide.

## Compatibility

There are no breaking API, storage, agent-contract, or configuration changes.
The storage contract remains format `2`, reader `8`, writer `8`; existing
knowledge bases do not require a migration.
