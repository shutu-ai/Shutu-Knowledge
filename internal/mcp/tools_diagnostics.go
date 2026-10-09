package mcp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type diagnosticsInput struct {
	TraceID        string `json:"trace_id,omitempty" jsonschema:"look up one earlier tool execution"`
	IncludeDetails bool   `json:"include_details,omitempty" jsonschema:"include the full record for the requested trace"`
	Limit          int    `json:"limit,omitempty" jsonschema:"maximum recent traces to list (default 20, max 100)"`
}

func (s *server) registerDiagnosticTools() {
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "knowledge_diagnostics", Title: "Query diagnostics",
		Description: "Summarize recent tool executions: routing, degradation, warnings, and timing. Query text is never stored.",
	}, s.toolDiagnostics)
}

func (s *server) toolDiagnostics(ctx context.Context, req *mcp.CallToolRequest, input diagnosticsInput) (*mcp.CallToolResult, any, error) {
	return runTool(s, "knowledge_diagnostics", input, func(traceID string) (*Envelope, error) {
		if strings.TrimSpace(input.TraceID) != "" {
			record, ok := s.traces.lookup(strings.TrimSpace(input.TraceID))
			if !ok {
				return errorEnvelope(traceID, CodeInvalidArgument,
					fmt.Sprintf("trace %q is not in this process's recent window", input.TraceID)), nil
			}
			view := summarizeTrace(record)
			if input.IncludeDetails {
				view["warning_codes"] = record.WarningCodes
			}
			return okEnvelope(traceID, map[string]any{"trace": view}, &EnvelopeContext{}, nil, false), nil
		}
		limit := input.Limit
		if limit <= 0 {
			limit = 20
		}
		if limit > 100 {
			limit = 100
		}
		records := s.traces.recent(limit)
		summaries := make([]map[string]any, 0, len(records))
		for _, record := range records {
			summaries = append(summaries, summarizeTrace(record))
		}
		return okEnvelope(traceID, map[string]any{"traces": summaries}, &EnvelopeContext{}, nil, false), nil
	})
}

func summarizeTrace(record traceRecord) map[string]any {
	return map[string]any{
		"trace_id": record.TraceID, "tool": record.Tool,
		"elapsed_ms": record.ElapsedMS, "base_ids": record.BaseIDs,
		"effective_mode": record.EffectiveMode, "degraded": record.Degraded,
		"warning_codes": record.WarningCodes, "error_code": record.ErrorCode,
		"started_at": record.StartedAt.UTC().Format(time.RFC3339),
	}
}
