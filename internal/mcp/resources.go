package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/shutu-ai/shutu-knowledge/internal/agentquery"
	"github.com/shutu-ai/shutu-knowledge/internal/semantic"
)

// Resource URIs are logical identifiers, never filesystem paths. Every
// template stays inside the authorized scope by resolving through Knowledge
// Core, and unknown identities fail with ResourceNotFoundError.

const resourceURIPrefix = "shutu-knowledge://base/"

const (
	manifestTemplate = resourceURIPrefix + "{base_id}/manifest"
	unitTemplate     = resourceURIPrefix + "{base_id}/generation/{generation}/unit/{unit_id}"
	evidenceTemplate = resourceURIPrefix + "{base_id}/document/{doc_id}/evidence/{locator}"
	topicTemplate    = resourceURIPrefix + "{base_id}/generation/{generation}/topic/{topic_id}"
)

func (s *server) registerResources() {
	s.mcpServer.AddResourceTemplate(&mcp.ResourceTemplate{
		Name: "base-manifest", MIMEType: "application/json",
		URITemplate: manifestTemplate,
		Description: "Read-only manifest of one knowledge base inside the authorized scope.",
	}, s.resourceManifest)
	s.mcpServer.AddResourceTemplate(&mcp.ResourceTemplate{
		Name: "semantic-unit", MIMEType: "application/json",
		URITemplate: unitTemplate,
		Description: "One compiled semantic unit with its resolved evidence closure.",
	}, s.resourceUnit)
	s.mcpServer.AddResourceTemplate(&mcp.ResourceTemplate{
		Name: "document-evidence", MIMEType: "application/json",
		URITemplate: evidenceTemplate,
		Description: "Exact evidence excerpt for one document locator (chunk id or node id).",
	}, s.resourceEvidence)
	s.mcpServer.AddResourceTemplate(&mcp.ResourceTemplate{
		Name: "semantic-topic", MIMEType: "application/json",
		URITemplate: topicTemplate,
		Description: "One topic unit plus the units related to it in the pinned generation.",
	}, s.resourceTopic)
}

// parseResourceURI splits a logical resource URI into its segments. It never
// interprets the result as a filesystem path.
func parseResourceURI(rawURI, suffix string) ([]string, error) {
	if !strings.HasPrefix(rawURI, resourceURIPrefix) {
		return nil, fmt.Errorf("unsupported resource URI %q", rawURI)
	}
	rest := strings.TrimPrefix(rawURI, resourceURIPrefix)
	if suffix != "" {
		if !strings.HasSuffix(rest, suffix) {
			return nil, fmt.Errorf("resource URI %q does not match the template", rawURI)
		}
		rest = strings.TrimSuffix(rest, suffix)
	}
	segments := strings.Split(rest, "/")
	for _, segment := range segments {
		if strings.TrimSpace(segment) == "" {
			return nil, fmt.Errorf("resource URI %q has an empty identity segment", rawURI)
		}
	}
	return segments, nil
}

func textResource(uri, text string) (*mcp.ReadResourceResult, error) {
	return &mcp.ReadResourceResult{
		Contents: []*mcp.ResourceContents{{
			URI: uri, MIMEType: "application/json", Text: text,
		}},
	}, nil
}

func resourceError(format string, args ...any) error {
	return fmt.Errorf(format, args...)
}

func (s *server) resourceManifest(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	segments, err := parseResourceURI(req.Params.URI, "/manifest")
	if err != nil || len(segments) != 1 {
		return nil, mcp.ResourceNotFoundError(req.Params.URI)
	}
	baseID := segments[0]
	capabilities, err := s.agentQuery.Capabilities(ctx)
	if err != nil {
		return nil, err
	}
	for _, base := range capabilities.Bases {
		if base.BaseID != baseID {
			continue
		}
		manifest := map[string]any{
			"base_id": base.BaseID, "display_name": base.Name,
			"document_count": base.DocumentCount, "chunk_count": base.ChunkCount,
			"semantic_ready":         base.SemanticReady,
			"semantic_generation":    base.SemanticGeneration,
			"structured_ready":       base.StructuredReady,
			"structured_model_count": base.StructuredModelCount,
			"modes":                  modeNames(capabilities.Modes),
		}
		encoded, err := json.Marshal(manifest)
		if err != nil {
			return nil, err
		}
		return textResource(req.Params.URI, string(encoded))
	}
	return nil, mcp.ResourceNotFoundError(req.Params.URI)
}

func (s *server) resourceUnit(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	segments, err := parseResourceURI(req.Params.URI, "")
	if err != nil || len(segments) != 5 || segments[1] != "generation" || segments[3] != "unit" {
		return nil, mcp.ResourceNotFoundError(req.Params.URI)
	}
	baseID, generation, unitID := segments[0], segments[2], segments[4]
	compilation, err := s.semanticCompilation(ctx, baseID, agentquery.Generation(generation))
	if err != nil {
		return nil, mcp.ResourceNotFoundError(req.Params.URI)
	}
	unit, ok := compilationUnitByID(compilation, unitID)
	if !ok {
		return nil, mcp.ResourceNotFoundError(req.Params.URI)
	}
	evidence, err := semantic.ResolveEvidence(compilation.Units, unit.ID)
	if err != nil {
		return nil, err
	}
	view := map[string]any{
		"uri": req.Params.URI,
		"unit": map[string]any{
			"id": unit.ID, "type": string(unit.Type), "title": unit.Title,
			"canonical_key": unit.CanonicalKey, "content": unit.Content,
			"status": string(unit.Status), "version": unit.Version,
			"confidence": unit.Confidence, "generation": unit.Generation,
		},
		"evidence": evidencePointers(evidence),
	}
	encoded, err := json.Marshal(view)
	if err != nil {
		return nil, err
	}
	return textResource(req.Params.URI, string(encoded))
}

func (s *server) resourceEvidence(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	segments, err := parseResourceURI(req.Params.URI, "")
	if err != nil || len(segments) != 5 || segments[1] != "document" || segments[3] != "evidence" {
		return nil, mcp.ResourceNotFoundError(req.Params.URI)
	}
	baseID, documentID, locator := segments[0], segments[2], segments[4]
	scope, err := s.agentQuery.ResolveScope(ctx, []string{baseID})
	if err != nil {
		return nil, mcp.ResourceNotFoundError(req.Params.URI)
	}
	view, err := s.resolveEvidenceExcerpt(ctx, scope, semantic.EvidenceSource{
		DocumentID: documentID, ChunkID: locator,
	}, 4000)
	if err != nil {
		return nil, mcp.ResourceNotFoundError(req.Params.URI)
	}
	encoded, err := json.Marshal(view)
	if err != nil {
		return nil, err
	}
	return textResource(req.Params.URI, string(encoded))
}

func (s *server) resourceTopic(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	segments, err := parseResourceURI(req.Params.URI, "")
	if err != nil || len(segments) != 5 || segments[1] != "generation" || segments[3] != "topic" {
		return nil, mcp.ResourceNotFoundError(req.Params.URI)
	}
	baseID, generation, topicID := segments[0], segments[2], segments[4]
	compilation, err := s.semanticCompilation(ctx, baseID, agentquery.Generation(generation))
	if err != nil {
		return nil, mcp.ResourceNotFoundError(req.Params.URI)
	}
	unit, ok := compilationUnitByID(compilation, topicID)
	if !ok || unit.Type != semantic.UnitTopic {
		return nil, mcp.ResourceNotFoundError(req.Params.URI)
	}
	related := []map[string]any{}
	for _, relation := range compilation.Relations {
		if relation.SubjectUnitID != topicID && relation.ObjectUnitID != topicID {
			continue
		}
		otherID := relation.ObjectUnitID
		if relation.ObjectUnitID == topicID {
			otherID = relation.SubjectUnitID
		}
		other, ok := compilationUnitByID(compilation, otherID)
		if !ok {
			continue
		}
		related = append(related, map[string]any{
			"relation": string(relation.Type), "unit_id": other.ID,
			"kind": string(other.Type), "title": other.Title,
		})
	}
	evidence, err := semantic.ResolveEvidence(compilation.Units, unit.ID)
	if err != nil {
		return nil, err
	}
	view := map[string]any{
		"uri": req.Params.URI,
		"topic": map[string]any{
			"id": unit.ID, "title": unit.Title,
			"canonical_key": unit.CanonicalKey, "content": unit.Content,
			"generation": unit.Generation,
		},
		"related":  related,
		"evidence": evidencePointers(evidence),
	}
	encoded, err := json.Marshal(view)
	if err != nil {
		return nil, err
	}
	return textResource(req.Params.URI, string(encoded))
}

func (s *server) registerPrompts() {
	s.mcpServer.AddPrompt(&mcp.Prompt{
		Name: "cited-research", Title: "Cited research",
		Description: "Research a question with citations resolved through Shutu-Knowledge tools.",
		Arguments: []*mcp.PromptArgument{
			{Name: "question", Required: true, Description: "the research question"},
		},
	}, func(ctx context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		question := strings.TrimSpace(req.Params.Arguments["question"])
		if question == "" {
			return nil, resourceError("cited-research prompt requires a question argument")
		}
		text := "Research the question with Shutu-Knowledge tools. " +
			"Start with knowledge_search, resolve decisive claims with knowledge_evidence, " +
			"and only state conclusions that carry exact document/chunk citations. " +
			"Mark anything without resolved evidence as unverified.\n\nQuestion: " + question
		return &mcp.GetPromptResult{
			Description: "Evidence-cited research workflow",
			Messages: []*mcp.PromptMessage{{
				Role: "user", Content: &mcp.TextContent{Text: text},
			}},
		}, nil
	})
	s.mcpServer.AddPrompt(&mcp.Prompt{
		Name: "temporal-compare", Title: "Temporal compare",
		Description: "Compare one topic between two source versions with before/after citations.",
		Arguments: []*mcp.PromptArgument{
			{Name: "topic", Required: true, Description: "the topic to compare"},
			{Name: "from_version", Required: true, Description: "older source version identity"},
			{Name: "to_version", Required: true, Description: "newer source version identity"},
		},
	}, func(ctx context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		topic := strings.TrimSpace(req.Params.Arguments["topic"])
		fromVersion := strings.TrimSpace(req.Params.Arguments["from_version"])
		toVersion := strings.TrimSpace(req.Params.Arguments["to_version"])
		if topic == "" || fromVersion == "" || toVersion == "" {
			return nil, resourceError("temporal-compare prompt requires topic, from_version, and to_version arguments")
		}
		text := fmt.Sprintf("Compare the topic %q between source versions %q and %q using "+
			"knowledge_compare_versions plus knowledge_search with as_of version policies. "+
			"Report added, changed, and removed facts, each with version-scoped citations; "+
			"mark inconclusive differences as uncertain instead of guessing.", topic, fromVersion, toVersion)
		return &mcp.GetPromptResult{
			Description: "Version comparison workflow",
			Messages: []*mcp.PromptMessage{{
				Role: "user", Content: &mcp.TextContent{Text: text},
			}},
		}, nil
	})
}
