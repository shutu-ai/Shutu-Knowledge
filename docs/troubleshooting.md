# Troubleshooting

## First Checks

```sh
shutu-knowledge version
shutu-knowledge doctor
```

Confirm the printed data home is the intended private directory. Doctor
reports schema version, raw-store accessibility, process/database/index, model
lifecycle, configured runtime, and missing optional-runtime component state,
plus non-secret environment overrides.

## Database Or Startup Failures

| Symptom | Action |
|---|---|
| `unknown field` while loading config | Remove or correct the unknown YAML key; unknown fields fail closed. |
| Database is not ready | Check disk space, file permissions, and that no incompatible SQLite file occupies `knowledge.db`. |
| Startup recovery reports failures | Inspect document `errorCode`/`errorMessage`; recoverable source stays in the raw store for reindex. |
| Wrong data directory | Set `SHUTU_KNOWLEDGE_HOME` consistently for every start and Agent extension environment. |

After changing schema-related code, do not modify database tables by hand; use
the versioned migrations and report the schema version from doctor.

## Ingestion Problems

| Symptom | Action |
|---|---|
| A file is unsupported | Legacy office conversion and OCR are optional. Configure a helper or convert to a supported modern format. |
| A PDF has no text | Try OCR when an OCR runtime is configured, or MinerU for advanced PDF processing. |
| OCR fails | Restore/reindex the document; native text is preserved when available. A failed optional helper should not mark the whole process unhealthy. |
| URL refresh is unchanged | The source content hash did not change, so embeddings and chunks are intentionally reused. |
| Batch import is slow | Check job progress and ensure the configured worker count fits disk and embedding capacity. |

Documents remain independent failure rows where possible. One malformed file
does not abort the rest of a bounded batch.

## Retrieval Problems

| Symptom | Action |
|---|---|
| Only lexical results | No embedding provider is configured, vectors are stale/dimension-mismatched, or vector search degraded. |
| Rerank status is degraded | Check the rerank endpoint/model, deadline, and structured error code. Search remains available in original order. |
| No results for an allowed-looking query | Inspect enabled scope and filters. Explicitly empty scopes/filters intentionally return zero. |
| Context is too short | Increase top-k/context tokens within bounds or inspect heading and overlap boundaries. |

`POST /api/search` exposes mode, elapsed time, per-hit scores, and rerank
status; use these fields before changing model configuration.

## Model Runtime Problems

1. Run `shutu-knowledge doctor`; missing runtimes are reported as degraded with
   a remediation, while `GET /api/runtime-status` shows configured helper probes.
2. Verify the configured helper starts, completes the line-delimited JSON
   handshake, answers health, and reports the requested capability.
3. Confirm dimensions match existing vectors and model keys before searching.
4. Remember that a downloaded artifact is not readiness; the runtime must
   answer successfully.

Helper timeouts, crashes, invalid scores, and dimension mismatches are isolated
and do not require restarting the Agent.

## Extension/Web Problems

| Symptom | Action |
|---|---|
| Extension is not discovered | Confirm Agent extension sources include `extension.yaml` and the transport command resolves. |
| Navigation is missing | Check Agent `/api/extensions`; disabled/removed extensions have no route. |
| Tools are missing | Check the Agent tool catalog and the Knowledge enabled scope. |
| Web proxy returns unavailable | The Knowledge process may have exited; Agent lifecycle should restart it, then inspect Knowledge logs/doctor. |

Knowledge never modifies Agent session data. Restarting Knowledge alone is safe
and does not reset Agent history.
