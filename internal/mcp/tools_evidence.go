package mcp

import (
	"context"
	"errors"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/shutu-ai/shutu-knowledge/internal/agentquery"
	"github.com/shutu-ai/shutu-knowledge/internal/knowledge"
	"github.com/shutu-ai/shutu-knowledge/internal/semantic"
)

type evidenceInput struct {
	BaseIDs    []string `json:"base_ids,omitempty" jsonschema:"knowledge bases that may contain the evidence"`
	UnitID     string   `json:"unit_id,omitempty" jsonschema:"semantic unit ID to resolve"`
	DocumentID string   `json:"document_id,omitempty" jsonschema:"document ID to read directly"`
	ChunkID    string   `json:"chunk_id,omitempty" jsonschema:"chunk ID to excerpt when reading a document directly"`
	Generation string   `json:"generation,omitempty" jsonschema:"semantic generation pin for unit evidence"`
	MaxChars   int      `json:"max_chars,omitempty" jsonschema:"excerpt character budget (default 1200, max 8000)"`
}

type contextInput struct {
	Query         string            `json:"query" jsonschema:"the question to compile context for"`
	BaseIDs       []string          `json:"base_ids,omitempty" jsonschema:"knowledge bases to compile"`
	Generation    string            `json:"generation,omitempty" jsonschema:"semantic generation: active or a positive integer"`
	VersionPolicy *mcpVersionPolicy `json:"version_policy,omitempty" jsonschema:"temporal scope policy"`
	TokenBudget   int               `json:"token_budget,omitempty" jsonschema:"maximum tokens per base package"`
}

func (s *server) registerEvidenceTools() {
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "knowledge_evidence", Title: "Resolve evidence",
		Description: "Resolve one semantic unit or document and chunk pointer to exact, version-verified source excerpts.",
	}, s.toolEvidence)
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "knowledge_context", Title: "Compile context",
		Description: "Compile one bounded, citation-bearing context package per base for a question.",
	}, s.toolContext)
}

func (s *server) toolEvidence(ctx context.Context, req *mcp.CallToolRequest, input evidenceInput) (*mcp.CallToolResult, any, error) {
	return runTool(s, "knowledge_evidence", input, func(traceID string) (*Envelope, error) {
		maxChars := input.MaxChars
		if maxChars <= 0 {
			maxChars = 1200
		}
		if maxChars > 8000 {
			maxChars = 8000
		}
		scope, err := s.agentQuery.ResolveScope(ctx, input.BaseIDs)
		if err != nil {
			code, message := mapAgentQueryError(err)
			return errorEnvelope(traceID, code, message), nil
		}
		if input.UnitID != "" {
			return s.evidenceFromUnit(ctx, traceID, scope, input)
		}
		if input.DocumentID != "" {
			source := semantic.EvidenceSource{DocumentID: input.DocumentID, ChunkID: input.ChunkID}
			view, resolveErr := s.resolveEvidenceExcerpt(ctx, scope, source, maxChars)
			if resolveErr != nil {
				return errorEnvelope(traceID, CodeEvidenceUnresolved, resolveErr.Error()), nil
			}
			return okEnvelope(traceID, view, &EnvelopeContext{BaseIDs: viewBaseIDs(view, scope)}, nil, false), nil
		}
		return errorEnvelope(traceID, CodeInvalidArgument, "unit_id or document_id is required"), nil
	})
}

func (s *server) evidenceFromUnit(ctx context.Context, traceID string, scope []string, input evidenceInput) (*Envelope, error) {
	var sources []semantic.EvidenceSource
	resolvedIn := ""
	for _, baseID := range scope {
		compilation, err := s.semanticCompilation(ctx, baseID, generationFromInput(input.Generation))
		if err != nil {
			continue
		}
		if _, ok := compilationUnitByID(compilation, input.UnitID); !ok {
			continue
		}
		evidence, resolveErr := semantic.ResolveEvidence(compilation.Units, input.UnitID)
		if resolveErr != nil {
			return errorEnvelope(traceID, CodeEvidenceUnresolved, resolveErr.Error()), nil
		}
		resolvedIn = baseID
		sources = evidence
		break
	}
	if resolvedIn == "" {
		return errorEnvelope(traceID, CodeUnitNotFound, fmt.Sprintf("unit %q was not found in the requested scope", input.UnitID)), nil
	}
	view, err := s.resolveEvidenceExcerpt(ctx, scope, sources[0], input.MaxChars)
	if err != nil {
		return errorEnvelope(traceID, CodeEvidenceUnresolved, err.Error()), nil
	}
	view["unit_id"] = input.UnitID
	view["base_id"] = resolvedIn
	view["supporting_pointers"] = evidencePointers(sources)
	return okEnvelope(traceID, view, &EnvelopeContext{BaseIDs: []string{resolvedIn}}, nil, false), nil
}

func viewBaseIDs(view map[string]any, scope []string) []string {
	if baseID, ok := view["base_id"].(string); ok && baseID != "" {
		return []string{baseID}
	}
	return scope
}

// resolveEvidenceExcerpt verifies one pointer against durable document state
// and returns a bounded excerpt. Version mismatch or a missing chunk fails
// closed instead of substituting a summary.
func (s *server) resolveEvidenceExcerpt(ctx context.Context, scope []string, source semantic.EvidenceSource, maxChars int) (map[string]any, error) {
	for _, baseID := range scope {
		document, chunks, err := s.knowledge.GetDocumentWithContext(ctx, source.DocumentID, true)
		if err != nil {
			if errors.Is(err, knowledge.ErrNotFound) {
				continue
			}
			return nil, err
		}
		if document.BaseID != baseID {
			continue
		}
		// The chunk listing is already fenced to the document's active index
		// generation, so identity verification compares the pointer against
		// the document generation instead of per-chunk columns.
		if source.IndexGeneration > 0 && source.SourceVersion > 0 &&
			(document.ActiveIndexGen != source.IndexGeneration || document.SourceVersion != source.SourceVersion) {
			return nil, fmt.Errorf("document %s is not available at index generation %d / source version %d; active generation is %d / %d",
				source.DocumentID, source.IndexGeneration, source.SourceVersion,
				document.ActiveIndexGen, document.SourceVersion)
		}
		view := map[string]any{
			"document_id":   document.ID,
			"document_name": document.Title,
			"base_id":       document.BaseID,
			"content_hash":  contentHash(document.ContentHash),
		}
		if source.ChunkID != "" {
			chunk, ok := chunkByID(chunks, source.ChunkID)
			if !ok {
				return nil, fmt.Errorf("chunk %s is not attached to document %s", source.ChunkID, source.DocumentID)
			}
			excerpt, truncated := clipText(chunk.Text, maxChars)
			view["chunk_id"] = chunk.ID
			view["chunk_index"] = chunk.Index
			view["heading"] = chunk.Heading
			view["index_generation"] = chunk.IndexGeneration
			view["source_version"] = chunk.SourceVersion
			view["excerpt"] = excerpt
			view["excerpt_truncated"] = truncated
			view["support_status"] = "resolved"
			if len(chunk.NodeIDs) > 0 {
				view["ir_node_ids"] = chunk.NodeIDs
			}
			return view, nil
		}
		if source.NodeID != "" {
			ir, irErr := s.knowledge.GetDocumentIR(ctx, document.ID)
			if irErr != nil {
				return nil, irErr
			}
			for _, node := range ir.Nodes {
				if node.ID == source.NodeID {
					excerpt, truncated := clipText(node.Text, maxChars)
					view["ir_node_id"] = node.ID
					view["excerpt"] = excerpt
					view["excerpt_truncated"] = truncated
					view["support_status"] = "resolved"
					return view, nil
				}
			}
			return nil, fmt.Errorf("IR node %s is not attached to document %s", source.NodeID, source.DocumentID)
		}
		if len(chunks) == 0 {
			return nil, fmt.Errorf("document %s has no indexed chunks", source.DocumentID)
		}
		chunk := chunks[0]
		excerpt, truncated := clipText(chunk.Text, maxChars)
		view["chunk_id"] = chunk.ID
		view["index_generation"] = chunk.IndexGeneration
		view["source_version"] = chunk.SourceVersion
		view["excerpt"] = excerpt
		view["excerpt_truncated"] = truncated
		view["support_status"] = "resolved"
		return view, nil
	}
	return nil, fmt.Errorf("document %s was not found in the requested scope", source.DocumentID)
}

func (s *server) toolContext(ctx context.Context, req *mcp.CallToolRequest, input contextInput) (*mcp.CallToolResult, any, error) {
	return runTool(s, "knowledge_context", input, func(traceID string) (*Envelope, error) {
		result, err := s.agentQuery.Context(ctx, agentquery.ContextRequest{
			Query: input.Query, BaseIDs: input.BaseIDs,
			Generation:    generationFromInput(input.Generation),
			VersionPolicy: versionPolicyFromInput(input.VersionPolicy),
			TokenBudget:   input.TokenBudget,
		})
		if err != nil {
			code, message := mapAgentQueryError(err)
			return errorEnvelope(traceID, code, message), nil
		}
		packages := make([]map[string]any, 0, len(result.Packages))
		for _, basePackage := range result.Packages {
			pkg := basePackage.Package
			packages = append(packages, map[string]any{
				"base_id":           basePackage.BaseID,
				"generation":        basePackage.Generation,
				"intent":            pkg.Intent,
				"routing":           pkg.Routing,
				"knowledge_summary": pkg.KnowledgeSummary,
				"concepts":          pkg.Concepts,
				"relations":         pkg.Relations,
				"facts":             pkg.Facts,
				"temporal_intent":   pkg.TemporalIntent,
				"resolved_version":  pkg.ResolvedVersion,
				"temporal_reason":   pkg.TemporalReason,
				"evidence":          contextEvidenceView(pkg.Evidence),
				"citations":         pkg.Citations,
				"rendered_context":  pkg.RenderedContext,
				"token_budget":      pkg.TokenBudget,
				"estimated_tokens":  pkg.EstimatedTokens,
				"diagnostics":       pkg.Diagnostics,
			})
		}
		return okEnvelope(traceID, map[string]any{
			"query": result.Query, "scope": result.Scope,
			"packages": packages, "degraded": result.Degraded,
		}, &EnvelopeContext{BaseIDs: result.Scope}, warningCodes(result.Diagnostics), result.Degraded), nil
	})
}

func contextEvidenceView(evidence []semantic.ContextEvidence) []map[string]any {
	out := make([]map[string]any, 0, len(evidence))
	for _, item := range evidence {
		out = append(out, map[string]any{
			"document_id": item.DocumentID, "chunk_id": item.ChunkID,
			"document_title": item.DocumentTitle, "heading": item.Heading,
			"citation": item.Citation, "text": item.Text, "score": item.Score,
			"index_generation": item.IndexGeneration, "source_version": item.SourceVersion,
			"version": item.Version, "temporal_status": item.TemporalStatus,
		})
	}
	return out
}
