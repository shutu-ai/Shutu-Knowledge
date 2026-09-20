# Shutu Knowledge 0.6.1 Release Report

This is the formal maintenance closure for durable interrupted-operation recovery
and web task-status reporting.

## Release source and scope

* Development branch: `fix/interrupted-operation-auto-retry`
* Release PR: [#3](https://github.com/shutu-ai/Shutu-Knowledge/pull/3)
* Merge / release source: `7026da6fa50a7e699e1cf137fa995c9bf55c7f6b`
* Version metadata: `0.6.1`
* Storage contract: format `2`, reader `8`, writer `8`
* Storage migration: none required
* Private `.local` corpus committed: NO

The release automatically requeues retryable durable operations interrupted
without a cancel request, bounded by the existing three-attempt limit. The web
task list now treats `interrupted` as terminal. It also adds a Windows trial
guide. There is no storage migration, parser rewrite, graph architecture, or
breaking API change.

## CI evidence

| Gate | Result | Evidence |
|---|---|---|
| Branch push CI | PASS | [35498225099](https://github.com/shutu-ai/Shutu-Knowledge/actions/runs/35498225099), final attempt at `84043580c043126ee000e0dc08edfa70e2f6234e` |
| Pull request CI | PASS | [35498237036](https://github.com/shutu-ai/Shutu-Knowledge/actions/runs/35498237036), same SHA |
| Post-merge master CI | PASS | [35500801245](https://github.com/shutu-ai/Shutu-Knowledge/actions/runs/35500801245) at `7026da6fa50a7e699e1cf137fa995c9bf55c7f6b` |
| Build / Go test / race | PASS | PR, master, and Tag CI |
| Web build / typecheck / tests | PASS | PR, master, and Tag CI |
| Browser E2E | PASS | PR, master, and Tag CI |
| Benchmark smoke | PASS | PR, master, and Tag CI |
| Storage-writer guard | PASS | PR, master, and Tag CI |
| Architecture / portability gates | PASS | PR, master, and Tag CI |
| Tag CI | PASS | [35502074080](https://github.com/shutu-ai/Shutu-Knowledge/actions/runs/35502074080) |
| GitHub Release | PASS | [Shutu Knowledge v0.6.1](https://github.com/shutu-ai/Shutu-Knowledge/releases/tag/v0.6.1) |

The first branch push attempt failed in the pre-existing
`TestAddFilesUsesBoundedParallelIngestion` concurrency assertion with
`observed concurrency 1, want 2..5`; no data race was reported. The same SHA
passed the complete independent PR CI. Only the failed push job was retried with
no source, test, or gate change; the full run then passed.

## Runtime fixes

* A retryable durable operation interrupted without a durable cancel request is
  returned to `queued` in the same control transaction.
* The existing maximum of three attempts remains enforced.
* Explicit cancellation remains `cancelled` and is not replayed.
* Business failures remain `failed` and do not automatically replay.
* The web task list treats `interrupted` as terminal and no longer counts it as
  an active task.

## Formal package

The formal Windows package was rebuilt from clean merged master source
`7026da6fa50a7e699e1cf137fa995c9bf55c7f6b`:

```text
filename: shutu-knowledge-0.6.1-windows-amd64.zip
size: 12660686 bytes
SHA-256: ab774c9e7beeee05cdb85c56331ea4257090f82f9d0550399d0d1e16083f9292
binary SHA-256: f4e07c1a1eee26e1376e69b12c05e8ef303ff473b33928d15782a89636380eca
```

`BUILD-METADATA.json` recorded:

```text
version: 0.6.1
platform: windows-amd64
git_sha: 7026da6fa50a7e699e1cf137fa995c9bf55c7f6b
storage contract: 2/8/8
```

Formal package smoke passed extraction, startup, runtime status, document import,
OCR import, embedding, retrieval, offline restart, and post-restart retrieval.
A separate packaged temporal context smoke passed `CURRENT`, `AS_OF_VERSION`,
and `RANGE_HISTORY`.

## Tag evidence

`v0.6.1` is an annotated tag and was pushed once:

* Tag object: `a288798600d8535be0f16b6bbd43fd1a7500351e`
* Target commit: `7026da6fa50a7e699e1cf137fa995c9bf55c7f6b`
* Tag moved or recreated after push: NO

## Tag CI evidence

[Tag CI run 35502074080](https://github.com/shutu-ai/Shutu-Knowledge/actions/runs/35502074080)
completed with every required job PASS:

| Job | Result |
|---|---|
| build | PASS |
| release-package | PASS |
| runtime-release | PASS |
| release-host (windows-latest) | PASS |
| release-host (ubuntu-latest) | PASS |
| release-host (macos-latest) | PASS |

Tag CI rebuilt the tagged source successfully. Its `BUILD-METADATA.json`
matched version `0.6.1`, platform `windows-amd64`, release source
`7026da6fa50a7e699e1cf137fa995c9bf55c7f6b`, and storage contract `2/8/8`.
Go build artifacts are not assumed byte-reproducible; the locally smoke-tested
archive above is the published GitHub asset.

## GitHub Release evidence

* Release: [Shutu Knowledge v0.6.1](https://github.com/shutu-ai/Shutu-Knowledge/releases/tag/v0.6.1)
* Title: `Shutu Knowledge v0.6.1`
* Draft: false
* Prerelease: false
* Published at: `2026-09-20T09:43:27Z`
* Asset: `shutu-knowledge-0.6.1-windows-amd64.zip`
* Asset size: `12660686` bytes
* Asset state: uploaded
* GitHub-reported digest: `sha256:ab774c9e7beeee05cdb85c56331ea4257090f82f9d0550399d0d1e16083f9292`
* Post-download SHA-256: `ab774c9e7beeee05cdb85c56331ea4257090f82f9d0550399d0d1e16083f9292`

## Release status

```text
READY
```

All product, CI, package-integrity, Tag CI, GitHub Release, and post-download
asset-verification gates pass.
