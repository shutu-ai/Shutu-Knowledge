package extension

import (
	"github.com/shutu-ai/shutu-agent/sdk/extension"
)

const (
	searchToolDescription = "Search imported knowledge bases for chunks relevant to a query. " +
		"Returns ranked excerpts with lane scores and ordered context windows. " +
		"USE THIS PROACTIVELY for facts, internal documents, numbers, or anything that may exist in imported " +
		"material, even when the user does not say \"knowledge base\". Quote returned evidence and cite its " +
		"document/base/chunk identifiers; if nothing relevant is returned, say so instead of guessing. " +
		"For hard queries, provide up to three alternate phrasings or translations in extraQueries."
)

func objectSchema(properties map[string]any, required ...string) map[string]any {
	schema := map[string]any{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func stringArray(description string) map[string]any {
	return map[string]any{
		"type":        "array",
		"items":       map[string]any{"type": "string"},
		"description": description,
	}
}

func stringProperty(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

func integerProperty(description string, minimum, maximum int) map[string]any {
	return map[string]any{
		"type":        "integer",
		"minimum":     minimum,
		"maximum":     maximum,
		"description": description,
	}
}

func tool(name, description string, input map[string]any, risk extension.ToolRisk, requiresApproval bool) extension.ToolDefinition {
	return extension.ToolDefinition{
		Name:             name,
		Description:      description,
		InputSchema:      input,
		OutputSchema:     objectSchema(map[string]any{}),
		Risk:             risk,
		RequiresApproval: requiresApproval,
	}
}

func boolPtr(value bool) *bool { return &value }

// toolDefinitions is the complete model-facing inventory from the Phase 0
// source audit. Approval is declared here; the Agent's approval policy stays
// authoritative and no tool bypasses it.
func toolDefinitions() []extension.ToolDefinition {
	return []extension.ToolDefinition{
		tool("knowledge_search", searchToolDescription, objectSchema(map[string]any{
			"query":         stringProperty("Search query."),
			"baseId":        stringProperty("Optional base id to restrict the search."),
			"topK":          map[string]any{"type": "integer", "minimum": 1, "maximum": 50, "description": "Number of results; defaults to configured topK."},
			"mode":          map[string]any{"type": "string", "enum": []string{"auto", "hybrid", "vector", "lexical"}, "description": "Retrieval mode."},
			"docIds":        stringArray("Optional document ids to include."),
			"titleIncludes": stringProperty("Optional case-insensitive document-title substring."),
			"sourceTypes":   map[string]any{"type": "array", "items": map[string]any{"type": "string", "enum": []string{"file", "text", "url", "directory"}}, "description": "Optional source types."},
			"updatedAfter":  map[string]any{"type": "integer", "description": "Epoch-ms lower bound on document update time."},
			"updatedBefore": map[string]any{"type": "integer", "description": "Epoch-ms upper bound on document update time."},
			"extraQueries":  stringArray("Up to three extra phrasings or translations for rank fusion."),
		}, "query"), extension.ToolRiskRead, false),
		tool("knowledge_list_bases", "List enabled knowledge bases, or outline one base's document tree when baseId is supplied.", objectSchema(map[string]any{
			"baseId": stringProperty("Optional base id to outline."),
		}), extension.ToolRiskRead, false),
		tool("knowledge_create_base", "Create a new knowledge base.", objectSchema(map[string]any{
			"name":        stringProperty("Short base name."),
			"description": stringProperty("What the base contains."),
		}, "name"), extension.ToolRiskWrite, false),
		tool("knowledge_delete_base", "Delete a knowledge base and every document, chunk, and raw source it owns. This is irreversible.", objectSchema(map[string]any{
			"baseId": stringProperty("Base id to delete."),
		}, "baseId"), extension.ToolRiskDestructive, true),
		tool("knowledge_add_document", "Add a text document to a base; the normal parse/chunk/embed pipeline runs automatically.", objectSchema(map[string]any{
			"baseId":  stringProperty("Target base id."),
			"title":   stringProperty("Document title."),
			"content": stringProperty("Full document text."),
		}, "baseId", "title", "content"), extension.ToolRiskWrite, false),
		tool("knowledge_list_documents", "List documents in one base, including document ids and counts.", objectSchema(map[string]any{
			"baseId": stringProperty("Base id."),
		}, "baseId"), extension.ToolRiskRead, false),
		tool("knowledge_delete_document", "Delete one document and its chunks and raw source. This is irreversible.", objectSchema(map[string]any{
			"baseId":     stringProperty("Owning base id, used to validate the request."),
			"documentId": stringProperty("Document id to delete."),
		}, "baseId", "documentId"), extension.ToolRiskDestructive, true),
		tool("knowledge_import_url", "Fetch a URL, extract its text, and import it as a document.", objectSchema(map[string]any{
			"baseId": stringProperty("Target base id."),
			"url":    stringProperty("HTTP or HTTPS URL to fetch."),
			"title":  stringProperty("Optional title; defaults to the page title."),
		}, "baseId", "url"), extension.ToolRiskWrite, false),
		tool("knowledge_refresh_url", "Re-fetch a URL document and update it only when origin content changed.", objectSchema(map[string]any{
			"documentId": stringProperty("URL document id."),
		}, "documentId"), extension.ToolRiskWrite, false),
		tool("knowledge_stats", "Report document, chunk, character, token, and embedding statistics for one base or all enabled bases.", objectSchema(map[string]any{
			"baseId": stringProperty("Optional base id."),
		}), extension.ToolRiskRead, false),
		tool("knowledge_get_document", "Read a document with chunk pagination, or continue around a search hit with an anchored token-bounded context window.", objectSchema(map[string]any{
			"documentId":    stringProperty("Document id."),
			"chunkOffset":   integerProperty("Zero-based page offset.", 0, 1_000_000),
			"chunkLimit":    integerProperty("Page size.", 1, 50),
			"anchorChunkId": stringProperty("Anchor chunk id from search; mutually exclusive with anchorIndex and pagination."),
			"anchorIndex":   integerProperty("Anchor chunk index.", 0, 1_000_000),
			"before":        integerProperty("Context chunks before anchor.", 0, 10),
			"after":         integerProperty("Context chunks after anchor.", 0, 10),
			"maxTokens":     integerProperty("Context budget.", 128, 4096),
			"focus":         stringProperty("Query or identifier used to center an oversized anchor."),
			"crossHeading":  map[string]any{"type": "boolean", "description": "Allow the window to cross heading boundaries."},
		}, "documentId"), extension.ToolRiskRead, false),
		tool("knowledge_read_document", "Read normalized document source text by character range, or grep it with a regular expression.", objectSchema(map[string]any{
			"documentId": stringProperty("Document id."),
			"charStart":  integerProperty("Start rune offset.", 0, 100_000_000),
			"charEnd":    integerProperty("End rune offset.", 0, 100_000_000),
			"pattern":    stringProperty("Go regular expression to locate text."),
			"maxMatches": integerProperty("Maximum matches returned.", 1, 200),
			"ignoreCase": map[string]any{"type": "boolean", "description": "Case-insensitive matching; defaults to true."},
		}, "documentId"), extension.ToolRiskRead, false),
		tool("knowledge_reindex_document", "Re-parse, re-chunk, and re-embed one document using current configuration.", objectSchema(map[string]any{
			"baseId":     stringProperty("Owning base id, used to validate the request."),
			"documentId": stringProperty("Document id to reindex."),
		}, "baseId", "documentId"), extension.ToolRiskWrite, false),
		tool("knowledge_reindex_base", "Re-parse, re-chunk, and re-embed every document in one base using current configuration.", objectSchema(map[string]any{
			"baseId": stringProperty("Base id to reindex."),
		}, "baseId"), extension.ToolRiskWrite, false),
	}
}

func contextProviderConfig() extension.ContextProviderConfig {
	return extension.ContextProviderConfig{
		Enabled:     true,
		Strategy:    extension.ContextOnUserInputChange,
		Required:    false,
		TimeoutMS:   4000,
		MaxChars:    8192,
		Priority:    80,
		Description: "Best-effort retrieval evidence for the current user input",
		Metadata:    map[string]string{"component": "knowledge", "mode": "auto-rag"},
	}
}

func permissions() []extension.Permission {
	return []extension.Permission{
		{Name: "session.id", Reason: "Stable provider cadence and request correlation"},
		{Name: "session.turn", Reason: "Turn-scoped retrieval correlation"},
		{Name: "user.input", Reason: "Build the automatic retrieval query"},
	}
}
