# Stdio client example

```go
cmd := exec.Command("shutu-knowledge", "mcp")
cmd.Env = append(os.Environ(), "SHUTU_KNOWLEDGE_HOME=/path/to/data")
client := mcp.NewClient(&mcp.Implementation{Name: "example-client"}, nil)
session, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
// session.ListTools / session.CallTool("knowledge_search", ...)
```

The SDK (`github.com/modelcontextprotocol/go-sdk/mcp`) handles initialize,
tools/list, tools/call, resources, and prompts. Reference implementation:
`internal/mcp/server_test.go` (TestMCPStdioRealProcess).