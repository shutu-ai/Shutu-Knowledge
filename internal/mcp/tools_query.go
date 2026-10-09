package mcp

import (
	"context"
	"sort"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/shutu-ai/shutu-knowledge/internal/agentquery"
	"github.com/shutu-ai/shutu-knowledge/internal/knowledge"
	"github.com/shutu-ai/shutu-knowledge/internal/semantic"
)

type listBasesInput struct {
	PageSize      int    `json:"page_size,omitempty" jsonschema:"maximum number of bases to return (default 20, max 100)"`
	Cursor        string `json:"cursor,omitempty" jsonschema:"opaque pagination cursor from the previous page"`
	IncludeStatus bool   `json:"include_status,omitempty" jsonschema:"include per-base readiness details"`
}

type capabilitiesInput struct {
	BaseIDs []string `json:"base_ids,omitempty" jsonschema:"restrict the report to these knowledge bases"`
}

type searchInput struct {
	Query             string            `json:"query" jsonschema:"the search query"`
	BaseIDs           []string          `json:"base_ids,omitempty" jsonschema:"knowledge bases to search; omitted means the enabled scope"`
	Mode              string            `json:"mode,omitempty" jsonschema:"evidence, semantic, hybrid, or auto (default auto)"`
	TopK              int               `json:"top_k,omitempty" jsonschema:"maximum number of hits"`
	VersionPolicy     *mcpVersionPolicy `json:"version_policy,omitempty" jsonschema:"temporal scope policy"`
	Generation        string            `json:"generation,omitempty" jsonschema:"semantic generation: active or a positive integer"`
	EvidenceMode      string            `json:"evidence_mode,omitempty" jsonschema:"evidence lane: auto, hybrid, vector, or lexical"`
	IncludeStructured bool              `json:"include_structured,omitempty" jsonschema:"include structured schema matches"`
}

func (s *server) registerQueryTools() {
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "knowledge_list_bases", Title: "List knowledge bases",
		Description: "List knowledge bases inside the authorized scope with readiness status and pagination.",
	}, s.toolListBases)
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "knowledge_capabilities", Title: "Knowledge capabilities",
		Description: "Report retrieval modes, per-base semantic and structured readiness, generation identities, and limits.",
	}, s.toolCapabilities)
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "knowledge_search", Title: "Knowledge search",
		Description: "Search evidence, semantic memory, structured models, or any hybrid of them under one stable contract.",
	}, s.toolSearch)
}

func (s *server) toolListBases(ctx context.Context, req *mcp.CallToolRequest, input listBasesInput) (*mcp.CallToolResult, any, error) {
	return runTool(s, "knowledge_list_bases", input, func(traceID string) (*Envelope, error) {
		capabilities, err := s.agentQuery.Capabilities(ctx)
		if err != nil {
			code, message := mapAgentQueryError(err)
			return errorEnvelope(traceID, code, message), nil
		}
		bases := append([]agentquery.BaseCapability(nil), capabilities.Bases...)
		sort.Slice(bases, func(i, j int) bool { return bases[i].BaseID < bases[j].BaseID })
		pageSize := input.PageSize
		if pageSize <= 0 {
			pageSize = 20
		}
		if pageSize > 100 {
			pageSize = 100
		}
		start := 0
		if input.Cursor != "" {
			found := false
			for i, base := range bases {
				if base.BaseID == input.Cursor {
					start = i + 1
					found = true
					break
				}
			}
			if !found {
				return errorEnvelope(traceID, CodeInvalidArgument, "cursor does not match any listed base id"), nil
			}
		}
		end := start + pageSize
		if end > len(bases) {
			end = len(bases)
		}
		type baseView struct {
			ID                   string   `json:"id"`
			DisplayName          string   `json:"display_name"`
			DocumentCount        int      `json:"document_count,omitempty"`
			ChunkCount           int      `json:"chunk_count,omitempty"`
			EvidenceReady        bool     `json:"evidence_ready"`
			SemanticStatus       string   `json:"semantic_status"`
			SemanticGeneration   int64    `json:"active_generation,omitempty"`
			StructuredModelCount int      `json:"structured_model_count,omitempty"`
			SupportedFeatures    []string `json:"supported_features"`
		}
		views := make([]baseView, 0, end-start)
		for _, base := range bases[start:end] {
			view := baseView{
				ID: base.BaseID, DisplayName: base.Name,
				DocumentCount: base.DocumentCount, ChunkCount: base.ChunkCount,
				EvidenceReady:        base.ChunkCount > 0,
				SemanticStatus:       "not_compiled",
				StructuredModelCount: base.StructuredModelCount,
				SupportedFeatures:    modeNames(capabilities.Modes),
			}
			if base.SemanticReady {
				view.SemanticStatus = "ready"
				view.SemanticGeneration = base.SemanticGeneration
			}
			views = append(views, view)
		}
		nextCursor := ""
		if end < len(bases) {
			nextCursor = bases[end-1].BaseID
		}
		return okEnvelope(traceID,
			map[string]any{"bases": views, "next_cursor": nextCursor},
			&EnvelopeContext{BaseIDs: scopeOf(capabilities.Bases)}, nil, false), nil
	})
}

func scopeOf(bases []agentquery.BaseCapability) []string {
	out := make([]string, 0, len(bases))
	for _, base := range bases {
		out = append(out, base.BaseID)
	}
	return out
}

func modeNames(modes []agentquery.Mode) []string {
	out := make([]string, 0, len(modes))
	for _, mode := range modes {
		out = append(out, string(mode))
	}
	return out
}

func (s *server) toolCapabilities(ctx context.Context, req *mcp.CallToolRequest, input capabilitiesInput) (*mcp.CallToolResult, any, error) {
	return runTool(s, "knowledge_capabilities", input, func(traceID string) (*Envelope, error) {
		capabilities, err := s.agentQuery.Capabilities(ctx)
		if err != nil {
			code, message := mapAgentQueryError(err)
			return errorEnvelope(traceID, code, message), nil
		}
		if len(input.BaseIDs) > 0 {
			known := map[string]bool{}
			for _, base := range capabilities.Bases {
				known[base.BaseID] = true
			}
			for _, id := range input.BaseIDs {
				if !known[id] {
					return errorEnvelope(traceID, CodeBaseNotFound, "knowledge base is not available: "+id), nil
				}
			}
			requested := map[string]bool{}
			for _, id := range input.BaseIDs {
				requested[id] = true
			}
			filtered := capabilities.Bases[:0]
			for _, base := range capabilities.Bases {
				if requested[base.BaseID] {
					filtered = append(filtered, base)
				}
			}
			capabilities.Bases = filtered
		}
		data := map[string]any{
			"modes":    modeNames(capabilities.Modes),
			"bases":    capabilities.Bases,
			"protocol": map[string]any{"transports": []string{"stdio", "streamable-http"}, "server": "shutu-knowledge-mcp"},
			"limits": map[string]any{
				"explore_max_hops": 4, "explore_max_nodes": 256, "explore_max_fan_out": 64,
				"explore_default_top_k": 8, "expand_max_roots": 8,
			},
		}
		return okEnvelope(traceID, data, &EnvelopeContext{BaseIDs: scopeOf(capabilities.Bases)}, nil, false), nil
	})
}

func (s *server) toolSearch(ctx context.Context, req *mcp.CallToolRequest, input searchInput) (*mcp.CallToolResult, any, error) {
	return runTool(s, "knowledge_search", input, func(traceID string) (*Envelope, error) {
		result, err := s.agentQuery.Search(ctx, agentquery.Request{
			Query: input.Query, BaseIDs: input.BaseIDs,
			Mode: agentquery.Mode(input.Mode), EvidenceMode: input.EvidenceMode,
			Generation:    generationFromInput(input.Generation),
			VersionPolicy: versionPolicyFromInput(input.VersionPolicy),
			TopK:          input.TopK, IncludeStructured: input.IncludeStructured,
		})
		if err != nil {
			code, message := mapAgentQueryError(err)
			return errorEnvelope(traceID, code, message), nil
		}
		data := map[string]any{
			"effective_mode":     string(result.Mode),
			"requested_mode":     string(result.Requested),
			"scope":              result.Scope,
			"evidence":           evidenceLaneView(result.Evidence),
			"semantic":           semanticLaneView(result.Semantic),
			"structured":         structuredLaneView(result.Structured),
			"snapshots":          result.Snapshots,
			"evidence_snapshots": result.EvidenceSnapshots,
			"degraded":           result.Degraded,
		}
		if result.Plan != nil {
			data["plan"] = result.Plan
		}
		context := &EnvelopeContext{BaseIDs: result.Scope, EffectiveMode: string(result.Mode)}
		if len(result.Snapshots) > 0 {
			context.SemanticGeneration = result.Snapshots[0].SemanticGeneration
		}
		return okEnvelope(traceID, data, context, warningCodes(result.Diagnostics), result.Degraded), nil
	})
}

func evidenceLaneView(result *knowledge.SearchResult) any {
	if result == nil {
		return nil
	}
	hits := make([]map[string]any, 0, len(result.Hits))
	for index, hit := range result.Hits {
		snippet, truncated := clipText(hit.Text, 600)
		view := map[string]any{
			"id":                hit.BaseID + ":" + hit.DocID + ":" + hit.ChunkID,
			"rank":              index + 1,
			"kind":              "evidence_chunk",
			"title":             hit.DocumentTitle,
			"snippet":           snippet,
			"snippet_truncated": truncated,
			"score":             hit.Score,
			"document_id":       hit.DocID,
			"base_id":           hit.BaseID,
			"chunk_id":          hit.ChunkID,
			"index_generation":  hit.IndexGeneration,
			"source_version":    hit.SourceVersion,
			"heading":           hit.Heading,
		}
		if len(hit.NodeIDs) > 0 {
			view["ir_node_ids"] = hit.NodeIDs
		}
		if hit.Citation != nil {
			view["citation"] = hit.Citation
		}
		hits = append(hits, view)
	}
	return map[string]any{
		"total": result.Total, "mode": result.Mode, "reranked": result.Reranked,
		"elapsed_ms": result.ElapsedMS, "hits": hits,
	}
}

func semanticLaneView(results []agentquery.BaseSemanticResult) []map[string]any {
	out := make([]map[string]any, 0, len(results))
	for _, result := range results {
		hits := make([]map[string]any, 0, len(result.Response.Hits))
		for _, hit := range result.Response.Hits {
			hits = append(hits, map[string]any{
				"unit_id":       hit.Unit.ID,
				"kind":          string(hit.Unit.Type),
				"title":         hit.Unit.Title,
				"content":       hit.Unit.Content,
				"status":        string(hit.Unit.Status),
				"version":       hit.Unit.Version,
				"score":         hit.Score,
				"coverage":      hit.Coverage,
				"matched_terms": hit.MatchedTerms,
				"evidence":      evidencePointers(hit.Evidence),
			})
		}
		out = append(out, map[string]any{
			"base_id": result.BaseID, "generation": result.Generation, "hits": hits,
		})
	}
	return out
}

func structuredLaneView(results []agentquery.BaseStructuredResult) []map[string]any {
	out := make([]map[string]any, 0, len(results))
	for _, result := range results {
		out = append(out, map[string]any{"base_id": result.BaseID, "response": result.Response})
	}
	return out
}

func evidencePointers(sources []semantic.EvidenceSource) []map[string]any {
	out := make([]map[string]any, 0, len(sources))
	for _, source := range sources {
		out = append(out, evidencePointer(source))
	}
	return out
}

func evidencePointer(source semantic.EvidenceSource) map[string]any {
	return map[string]any{
		"document_id":      source.DocumentID,
		"index_generation": source.IndexGeneration,
		"source_version":   source.SourceVersion,
		"node_id":          source.NodeID,
		"chunk_id":         source.ChunkID,
	}
}
