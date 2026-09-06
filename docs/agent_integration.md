# Agent Integration

## Contract

`internal/extension` is the only package that imports
`github.com/shutu-ai/shutu-agent/sdk/extension`. It publishes:

- 14 Knowledge tools with read/write/destructive risk metadata,
- an `on_user_input_change` native context provider,
- health and lifecycle capabilities,
- a Web contribution at `/extensions/shutu-knowledge/`,
- minimal grants for session id, session turn, and user input,
- no event subscriptions for v1 core behavior.

In extension mode, Knowledge starts a loopback-only HTTP listener, reports the
ephemeral URL as `webBaseUrl`, and maps Agent requests to the same Knowledge
Core used by the standalone API.

## Deployment

The Agent discovers `extension.yaml` through its normal extension source or
directory configuration. `transport.command` must resolve to the Knowledge
binary. If the Agent passes a minimized child environment and the default data
home is unavailable, add a non-secret path through the v1 manifest:

```yaml
transport:
  type: stdio
  command: /opt/shutu/bin/shutu-knowledge
  args: ["extension"]
  env:
    - SHUTU_KNOWLEDGE_HOME=/var/lib/shutu-knowledge
```

Knowledge owns that directory and its SQLite/raw/model/cache state.

## Real-Process Evidence

A pinned read-only shutu-agent build and a freshly built Knowledge binary were
started as two separate processes. The test used an explicit extension source
and a temporary manifest with a non-secret `SHUTU_KNOWLEDGE_HOME`.

Observed results:

1. Agent discovery spawned the external Knowledge process.
2. Protocol initialize negotiated `shutu-extension/1`; health reported ready.
3. `/api/extensions` returned:

   ```json
   {
     "extensions": [{
       "extensionId": "shutu-knowledge",
       "title": "Knowledge",
       "route": "/extensions/shutu-knowledge/",
       "navigationEnabled": true,
       "ready": true
     }]
   }
   ```

4. The Agent tool catalog contained all prefixed tools, including
   `ext__shutu-knowledge__knowledge_search`; delete tools remained declared
   destructive and approval-required.
5. A document was imported through the Agent-authenticated reverse proxy at
   `/extensions/shutu-knowledge/api/...`; the proxy returned the Knowledge
   envelope and the document reached `ready`.
6. A real turn asked, "What is the retry budget?" Durable Agent events included
   the injected untrusted Knowledge evidence with source, document, chunk,
   title, and the `>>>` anchor marker. The deliberately unreachable LLM then
   failed, proving that injection happened before the model request.
7. Killing the external Knowledge process left the Agent health endpoint
   healthy. The next provider request attempted context/provide, observed the
   closed transport, and the extension host restarted Knowledge successfully
   with health ready.
8. Terminating the Agent terminated the managed Knowledge child; no orphan
   process remained.

The Agent extension event log recorded context success, context transport
failure, and lifecycle restart. This is the authoritative real-process
integration evidence for Gate D, E, F, G, and the process-isolation portion of
Gate H.
