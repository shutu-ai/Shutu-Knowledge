package mcp

import (
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/shutu-ai/shutu-knowledge/internal/agentquery"
	"github.com/shutu-ai/shutu-knowledge/internal/knowledge"
	"github.com/shutu-ai/shutu-knowledge/internal/version"
)

// server owns the adapter collaborators. The SDK server is the protocol
// surface; this struct only binds shared application services to handlers.
type server struct {
	mcpServer  *mcp.Server
	knowledge  *knowledge.Service
	agentQuery *agentquery.Service
	traces     *traceRegistry
	limiter    *toolLimiter
}

// Options binds the adapter to one application instance.
type Options struct {
	Knowledge  *knowledge.Service
	AgentQuery *agentquery.Service
}

// New builds the read-only Shutu-Knowledge MCP server with all nine tools,
// read-only resource templates, and the two workflow prompts.
func New(options Options) (*mcp.Server, error) {
	if options.Knowledge == nil || options.AgentQuery == nil {
		return nil, fmt.Errorf("mcp: knowledge and agentquery services are required")
	}
	impl := &mcp.Implementation{
		Name:    "shutu-knowledge",
		Version: version.Current().Version,
	}
	s := &server{
		mcpServer: mcp.NewServer(impl, &mcp.ServerOptions{
			Instructions: "Search, explore, verify, and compare Shutu-Knowledge with auditable citations. " +
				"Evidence remains authoritative; semantic results are a separately labeled lane.",
		}),
		knowledge:  options.Knowledge,
		agentQuery: options.AgentQuery,
		traces:     newTraceRegistry(),
		limiter:    newToolLimiter(),
	}
	s.registerQueryTools()
	s.registerGraphTools()
	s.registerEvidenceTools()
	s.registerCompareTools()
	s.registerDiagnosticTools()
	s.registerResources()
	s.registerPrompts()
	return s.mcpServer, nil
}
