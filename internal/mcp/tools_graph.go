package mcp

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/shutu-ai/shutu-knowledge/internal/agentquery"
)

type exploreInput struct {
	Query            string            `json:"query" jsonschema:"activation query that selects seed units"`
	BaseIDs          []string          `json:"base_ids,omitempty" jsonschema:"knowledge bases to explore"`
	Generation       string            `json:"generation,omitempty" jsonschema:"semantic generation: active or a positive integer"`
	VersionPolicy    *mcpVersionPolicy `json:"version_policy,omitempty" jsonschema:"temporal scope policy"`
	RelationTypes    []string          `json:"relation_types,omitempty" jsonschema:"allowlist of relation types to traverse"`
	Direction        string            `json:"direction,omitempty" jsonschema:"out, in, or both (default out)"`
	MaxHops          int               `json:"max_hops,omitempty" jsonschema:"maximum traversal hops (default 1, max 4)"`
	MaxNodes         int               `json:"max_nodes,omitempty" jsonschema:"maximum nodes per base graph (default 64, max 256)"`
	FanOut           int               `json:"fan_out,omitempty" jsonschema:"maximum newly discovered neighbors per node (default 16)"`
	TopK             int               `json:"top_k,omitempty" jsonschema:"maximum seed units per base (default 8, max 32)"`
	ExcludeConflicts bool              `json:"exclude_conflicts,omitempty" jsonschema:"skip contradicts relations"`
}

type expandInput struct {
	RootUnitIDs      []string          `json:"root_unit_ids" jsonschema:"exact unit IDs to expand from (max 8)"`
	BaseIDs          []string          `json:"base_ids,omitempty" jsonschema:"knowledge bases that may contain the roots"`
	Generation       string            `json:"generation,omitempty" jsonschema:"semantic generation: active or a positive integer"`
	VersionPolicy    *mcpVersionPolicy `json:"version_policy,omitempty" jsonschema:"temporal scope policy"`
	RelationTypes    []string          `json:"relation_types,omitempty" jsonschema:"allowlist of relation types to traverse"`
	Direction        string            `json:"direction,omitempty" jsonschema:"out, in, or both (default out)"`
	MaxHops          int               `json:"max_hops,omitempty" jsonschema:"maximum traversal hops (default 2, max 4)"`
	MaxNodes         int               `json:"max_nodes,omitempty" jsonschema:"maximum nodes per base graph (default 64, max 256)"`
	FanOut           int               `json:"fan_out,omitempty" jsonschema:"maximum newly discovered neighbors per node (default 16)"`
	ExcludeConflicts bool              `json:"exclude_conflicts,omitempty" jsonschema:"skip contradicts relations"`
}

func (s *server) registerGraphTools() {
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "knowledge_explore", Title: "Explore semantic graph",
		Description: "Activate semantic seeds from a query and walk typed relations with exact evidence per node and edge.",
	}, s.toolExplore)
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "knowledge_expand", Title: "Expand from unit",
		Description: "Expand one or more exact unit IDs through typed relations and return nodes, edges, paths, and provenance.",
	}, s.toolExpand)
}

func (s *server) toolExplore(ctx context.Context, req *mcp.CallToolRequest, input exploreInput) (*mcp.CallToolResult, any, error) {
	return runTool(s, "knowledge_explore", ctx, input, func(ctx context.Context, traceID string) (*Envelope, error) {
		if err := checkQueryBounds(input.Query); err != nil {
			return errorEnvelope(traceID, CodeResourceLimit, err.Error()), nil
		}

		relationTypes, err := relationTypesFromInput(input.RelationTypes)
		if err != nil {
			return errorEnvelope(traceID, CodeInvalidArgument, err.Error()), nil
		}
		result, err := s.agentQuery.Explore(ctx, agentquery.ExploreRequest{
			Query: input.Query, BaseIDs: input.BaseIDs,
			Generation:    generationFromInput(input.Generation),
			VersionPolicy: versionPolicyFromInput(input.VersionPolicy),
			RelationTypes: relationTypes,
			Direction:     agentquery.TraversalDirection(input.Direction),
			MaxHops:       input.MaxHops, MaxNodes: input.MaxNodes,
			FanOut: input.FanOut, TopK: input.TopK,
			ExcludeConflicts: input.ExcludeConflicts,
		})
		if err != nil {
			code, message := mapAgentQueryError(err)
			return errorEnvelope(traceID, code, message), nil
		}
		return okEnvelope(traceID, map[string]any{
			"query": result.Query, "scope": result.Scope,
			"graphs": graphViews(result.Graphs), "degraded": result.Degraded,
		}, &EnvelopeContext{BaseIDs: result.Scope}, warningCodes(result.Diagnostics), result.Degraded), nil
	})
}

func (s *server) toolExpand(ctx context.Context, req *mcp.CallToolRequest, input expandInput) (*mcp.CallToolResult, any, error) {
	return runTool(s, "knowledge_expand", ctx, input, func(ctx context.Context, traceID string) (*Envelope, error) {
		if len(input.RootUnitIDs) == 0 {
			return errorEnvelope(traceID, CodeInvalidArgument, "root_unit_ids is required"), nil
		}
		if len(input.RootUnitIDs) > 8 {
			return errorEnvelope(traceID, CodeInvalidArgument, "root_unit_ids accepts at most 8 roots"), nil
		}
		relationTypes, err := relationTypesFromInput(input.RelationTypes)
		if err != nil {
			return errorEnvelope(traceID, CodeInvalidArgument, err.Error()), nil
		}
		maxHops := input.MaxHops
		if maxHops <= 0 {
			maxHops = 2
		}
		roots := make([]map[string]any, 0, len(input.RootUnitIDs))
		overallDegraded := false
		warnings := []string{}
		scopeSeen := map[string]bool{}
		scopeOrder := []string{}
		for _, rootID := range input.RootUnitIDs {
			result, expandErr := s.agentQuery.Expand(ctx, agentquery.ExpandRequest{
				UnitID: strings.TrimSpace(rootID), BaseIDs: input.BaseIDs,
				Generation:    generationFromInput(input.Generation),
				VersionPolicy: versionPolicyFromInput(input.VersionPolicy),
				RelationTypes: relationTypes,
				Direction:     agentquery.TraversalDirection(input.Direction),
				MaxHops:       maxHops, MaxNodes: input.MaxNodes, FanOut: input.FanOut,
				ExcludeConflicts: input.ExcludeConflicts,
			})
			if expandErr != nil {
				code, message := mapAgentQueryError(expandErr)
				if code == CodeInternal {
					return nil, expandErr
				}
				roots = append(roots, map[string]any{
					"unit_id": rootID, "error": map[string]any{"code": code, "message": message},
				})
				overallDegraded = true
				continue
			}
			for _, scopeID := range result.Scope {
				if !scopeSeen[scopeID] {
					scopeSeen[scopeID] = true
					scopeOrder = append(scopeOrder, scopeID)
				}
			}
			warnings = append(warnings, warningCodes(result.Diagnostics)...)
			roots = append(roots, map[string]any{"unit_id": rootID, "graphs": graphViews(result.Graphs)})
		}
		sortStrings(scopeOrder)
		return okEnvelope(traceID, map[string]any{
			"roots": roots, "scope": scopeOrder, "degraded": overallDegraded,
		}, &EnvelopeContext{BaseIDs: scopeOrder}, dedupeStrings(warnings), overallDegraded), nil
	})
}

func graphViews(graphs []agentquery.BaseExploreGraph) []map[string]any {
	out := make([]map[string]any, 0, len(graphs))
	for _, graph := range graphs {
		nodes := make([]map[string]any, 0, len(graph.Nodes))
		for _, node := range graph.Nodes {
			nodes = append(nodes, map[string]any{
				"unit": map[string]any{
					"id": node.Unit.ID, "type": string(node.Unit.Type),
					"title": node.Unit.Title, "canonical_key": node.Unit.CanonicalKey,
					"content": node.Unit.Content, "status": string(node.Unit.Status),
					"version": node.Unit.Version, "confidence": node.Unit.Confidence,
				},
				"hops": node.Hops, "is_seed": node.IsSeed,
				"evidence":        evidencePointers(node.Evidence),
				"evidence_status": string(node.EvidenceStatus),
			})
		}
		edges := make([]map[string]any, 0, len(graph.Edges))
		for _, edge := range graph.Edges {
			edges = append(edges, map[string]any{
				"relation": map[string]any{
					"id": edge.Relation.ID, "type": string(edge.Relation.Type),
					"subject_unit_id": edge.Relation.SubjectUnitID,
					"object_unit_id":  edge.Relation.ObjectUnitID,
					"predicate":       edge.Relation.Predicate,
					"statement":       edge.Relation.Statement,
					"confidence":      edge.Relation.Confidence,
					"status":          string(edge.Relation.Status),
				},
				"evidence":        evidencePointers(edge.Evidence),
				"evidence_status": string(edge.EvidenceStatus),
				"conflict":        edge.Conflict,
			})
		}
		paths := make([]map[string]any, 0, len(graph.Paths))
		for _, path := range graph.Paths {
			paths = append(paths, map[string]any{
				"seed_unit_id": path.SeedUnitID, "unit_id": path.UnitID,
				"hops": path.Hops, "edge_ids": path.EdgeIDs,
			})
		}
		seeds := make([]map[string]any, 0, len(graph.Seeds))
		for _, seed := range graph.Seeds {
			seeds = append(seeds, map[string]any{"unit_id": seed.UnitID, "score": seed.Score})
		}
		view := map[string]any{
			"base_id": graph.BaseID, "generation": graph.Generation,
			"seeds": seeds, "nodes": nodes, "edges": edges,
			"paths": paths, "truncated": graph.Truncated,
		}
		if graph.Plan != nil {
			view["plan"] = graph.Plan
		}
		out = append(out, view)
	}
	return out
}
