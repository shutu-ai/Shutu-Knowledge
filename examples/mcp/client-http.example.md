# Streamable HTTP client example

Start the server:

```bash
shutu-knowledge mcp --http 127.0.0.1:7801
```

Connect:

```go
client := mcp.NewClient(&mcp.Implementation{Name: "example-client"}, nil)
session, err := client.Connect(ctx,
    &mcp.StreamableClientTransport{Endpoint: "http://127.0.0.1:7801"}, nil)
```

The endpoint also works with any MCP-compatible HTTP client (for example MCP
Inspector): POST JSON-RPC to the URL; the handler speaks the streamable transport.
Reference implementation: `internal/mcp/server_test.go` (TestMCPToolsOverHTTP).