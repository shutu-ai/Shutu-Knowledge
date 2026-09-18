# Shutu-Agent 0.4 Integration Requirements

Status: satisfied by the existing public Extension Platform v1 tool capability.
No Shutu-Agent core change or cross-repository modification is required.

## Required consumption contract

Shutu-Agent consumes the new read-only tool:

```text
knowledge_compile_context
```

Input:

* `baseId` (required): an enabled Knowledge base with an active semantic
  compilation;
* `query` (required): the user question;
* `tokenBudget` (optional): bounded estimate from 512 through 32768.

Output:

* deterministic intent/routing diagnostics;
* Topic/Summary orientation;
* relevant Concepts;
* critical Facts;
* exact evidence;
* citations;
* estimated/rendered token context.

The tool does not compile or mutate semantic memory. Compilation remains an
explicit Knowledge-side operation, so Agent reads cannot trigger hidden writes.

## Extension behavior

* Tool risk is `read` and approval is not required.
* Invocation scope is enforced by the existing enabled-base fence.
* A missing active semantic compilation returns a tool error; legacy
  `knowledge_search` remains available.
* Exact evidence and citations remain authoritative and are returned alongside
  semantic orientation.
* No new Agent permission, event subscription, context-provider contract, or
  SDK dependency is needed.
