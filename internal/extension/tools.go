package extension

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/shutu-ai/shutu-agent/sdk/extension"
	"github.com/shutu-ai/shutu-knowledge/internal/app"
	"github.com/shutu-ai/shutu-knowledge/internal/evidence"
	"github.com/shutu-ai/shutu-knowledge/internal/knowledge"
)

// searchToolResult extends the core result without leaking Agent SDK DTOs.
type searchToolResult struct {
	knowledge.SearchResult
	Citations []string `json:"citations"`
}

type baseSummary struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Description   string `json:"description,omitempty"`
	DocumentCount int    `json:"documentCount"`
	ChunkCount    int    `json:"chunkCount"`
}

type documentSummary struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	SourceType  string `json:"sourceType"`
	Status      string `json:"status"`
	CharCount   int    `json:"charCount"`
	ChunkCount  int    `json:"chunkCount"`
	DirectoryID string `json:"parentDirectoryId,omitempty"`
}

type documentPage struct {
	ReadMode        string            `json:"readMode"`
	ID              string            `json:"id"`
	Title           string            `json:"title"`
	SourceType      string            `json:"sourceType"`
	CharCount       int               `json:"charCount"`
	ChunkCount      int               `json:"chunkCount"`
	NextChunkOffset int               `json:"nextChunkOffset,omitempty"`
	Truncated       bool              `json:"truncated"`
	Chunks          []knowledge.Chunk `json:"chunks,omitempty"`
	ContextWindow   *evidence.Window  `json:"contextWindow,omitempty"`
}

// CallTool routes the extension-local tool name, enforces the invocation
// scope, and converts expected service errors into a tool-level error. Only
// internal marshalling failures become RPC errors.
func CallTool(ctx context.Context, application *app.App, request extension.ToolCallRequest) (extension.ToolCallResult, error) {
	value, err := callTool(ctx, application, request)
	if err != nil {
		return extension.ToolCallResult{Error: err.Error()}, nil
	}
	return extension.ToolCallResult{Value: value}, nil
}

func callTool(ctx context.Context, application *app.App, request extension.ToolCallRequest) (any, error) {
	service := application.Knowledge
	if enabled, _, err := service.EnabledScope(); err != nil {
		return nil, err
	} else if !enabled {
		return nil, fmt.Errorf("knowledge invocation is disabled; enable it before calling knowledge tools")
	}
	switch request.Name {
	case "knowledge_search":
		var args struct {
			Query         string   `json:"query"`
			BaseID        string   `json:"baseId"`
			TopK          int      `json:"topK"`
			Mode          string   `json:"mode"`
			DocIDs        []string `json:"docIds"`
			TitleIncludes string   `json:"titleIncludes"`
			SourceTypes   []string `json:"sourceTypes"`
			UpdatedAfter  int64    `json:"updatedAfter"`
			UpdatedBefore int64    `json:"updatedBefore"`
			ExtraQueries  []string `json:"extraQueries"`
		}
		if err := decodeArguments(request.Arguments, &args); err != nil {
			return nil, err
		}
		if strings.TrimSpace(args.Query) == "" {
			return nil, fmt.Errorf("search query is required")
		}
		if args.BaseID != "" {
			if err := requireEnabledBase(service, args.BaseID); err != nil {
				return nil, err
			}
		}
		req := knowledge.SearchRequest{
			Query: args.Query, Queries: args.ExtraQueries, BaseID: args.BaseID,
			TopK: args.TopK, Mode: args.Mode,
		}
		if args.DocIDs != nil || args.TitleIncludes != "" || args.SourceTypes != nil || args.UpdatedAfter != 0 || args.UpdatedBefore != 0 {
			req.Filter = &knowledge.SearchFilter{
				DocIDs: args.DocIDs, TitleIncludes: args.TitleIncludes, SourceTypes: args.SourceTypes,
				UpdatedAfter: args.UpdatedAfter, UpdatedBefore: args.UpdatedBefore,
			}
		}
		result, err := service.Search(ctx, req)
		if err != nil {
			return nil, err
		}
		return searchToolResult{SearchResult: result, Citations: citations(result.Hits)}, nil

	case "knowledge_list_bases":
		var args struct {
			BaseID string `json:"baseId"`
		}
		if err := decodeArguments(request.Arguments, &args); err != nil {
			return nil, err
		}
		bases, err := enabledBases(service)
		if err != nil {
			return nil, err
		}
		if args.BaseID == "" {
			out := make([]baseSummary, 0, len(bases))
			for _, base := range bases {
				out = append(out, baseSummary{ID: base.ID, Name: base.Name, Description: base.Description, DocumentCount: base.DocumentCount, ChunkCount: base.ChunkCount})
			}
			return map[string]any{"bases": out}, nil
		}
		for _, base := range bases {
			if base.ID == args.BaseID {
				docs, err := service.ListDocuments(base.ID)
				if err != nil {
					return nil, err
				}
				return map[string]any{"documents": docs}, nil
			}
		}
		return nil, fmt.Errorf("knowledge base %s is not enabled", args.BaseID)

	case "knowledge_create_base":
		var args struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		if err := decodeArguments(request.Arguments, &args); err != nil {
			return nil, err
		}
		base, err := service.CreateBase(args.Name, args.Description, "", knowledge.BaseConfig{})
		if err != nil {
			return nil, err
		}
		return map[string]any{"id": base.ID, "name": base.Name}, nil

	case "knowledge_delete_base":
		var args struct {
			BaseID string `json:"baseId"`
		}
		if err := decodeArguments(request.Arguments, &args); err != nil {
			return nil, err
		}
		if err := requireEnabledBase(service, args.BaseID); err != nil {
			return nil, err
		}
		if err := service.DeleteBase(args.BaseID); err != nil {
			return nil, err
		}
		return map[string]any{"deleted": true}, nil

	case "knowledge_add_document":
		var args struct {
			BaseID  string `json:"baseId"`
			Title   string `json:"title"`
			Content string `json:"content"`
		}
		if err := decodeArguments(request.Arguments, &args); err != nil {
			return nil, err
		}
		if err := requireEnabledBase(service, args.BaseID); err != nil {
			return nil, err
		}
		doc, err := service.AddTextDocument(ctx, args.BaseID, args.Title, args.Content)
		if err != nil {
			return nil, err
		}
		return map[string]any{"id": doc.ID, "title": doc.Title, "chunkCount": doc.ChunkCount}, nil

	case "knowledge_list_documents":
		var args struct {
			BaseID string `json:"baseId"`
		}
		if err := decodeArguments(request.Arguments, &args); err != nil {
			return nil, err
		}
		if err := requireEnabledBase(service, args.BaseID); err != nil {
			return nil, err
		}
		docs, err := service.ListDocuments(args.BaseID)
		if err != nil {
			return nil, err
		}
		out := make([]documentSummary, 0, len(docs))
		for _, doc := range docs {
			out = append(out, documentSummary{ID: doc.ID, Title: doc.Title, SourceType: doc.SourceType, Status: doc.Status, CharCount: doc.CharCount, ChunkCount: doc.ChunkCount, DirectoryID: doc.ParentDirID})
		}
		return map[string]any{"documents": out}, nil

	case "knowledge_delete_document":
		var args struct {
			BaseID     string `json:"baseId"`
			DocumentID string `json:"documentId"`
		}
		if err := decodeArguments(request.Arguments, &args); err != nil {
			return nil, err
		}
		doc, err := requireEnabledDocument(service, args.DocumentID)
		if err != nil {
			return nil, err
		}
		if doc.BaseID != args.BaseID {
			return nil, fmt.Errorf("document %q does not belong to knowledge base %s", doc.Title, args.BaseID)
		}
		if err := service.DeleteDocument(doc.ID); err != nil {
			return nil, err
		}
		return map[string]any{"deleted": true}, nil

	case "knowledge_import_url":
		var args struct {
			BaseID string `json:"baseId"`
			URL    string `json:"url"`
			Title  string `json:"title"`
		}
		if err := decodeArguments(request.Arguments, &args); err != nil {
			return nil, err
		}
		if err := requireEnabledBase(service, args.BaseID); err != nil {
			return nil, err
		}
		doc, err := service.AddUrlDocument(ctx, args.BaseID, args.URL, args.Title)
		if err != nil {
			return nil, err
		}
		return map[string]any{"id": doc.ID, "title": doc.Title, "chunkCount": doc.ChunkCount}, nil

	case "knowledge_refresh_url":
		var args struct {
			DocumentID string `json:"documentId"`
		}
		if err := decodeArguments(request.Arguments, &args); err != nil {
			return nil, err
		}
		doc, err := requireEnabledDocument(service, args.DocumentID)
		if err != nil {
			return nil, err
		}
		changed, refreshed, err := service.RefreshUrlDocument(ctx, doc.ID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"changed": changed, "title": refreshed.Title, "chunkCount": refreshed.ChunkCount}, nil

	case "knowledge_stats":
		var args struct {
			BaseID string `json:"baseId"`
		}
		if err := decodeArguments(request.Arguments, &args); err != nil {
			return nil, err
		}
		if args.BaseID != "" {
			if err := requireEnabledBase(service, args.BaseID); err != nil {
				return nil, err
			}
			stats, err := service.Stats(args.BaseID)
			if err != nil {
				return nil, err
			}
			return stats, nil
		}
		bases, err := enabledBases(service)
		if err != nil {
			return nil, err
		}
		aggregate := knowledge.Stats{}
		for _, base := range bases {
			stats, err := service.Stats(base.ID)
			if err != nil {
				return nil, err
			}
			aggregate.DocumentCount += stats.DocumentCount
			aggregate.ChunkCount += stats.ChunkCount
			aggregate.CharCount += stats.CharCount
			aggregate.TokenCount += stats.TokenCount
			aggregate.Embedded = aggregate.Embedded || stats.Embedded
		}
		return aggregate, nil

	case "knowledge_get_document":
		var args struct {
			DocumentID    string  `json:"documentId"`
			ChunkOffset   *int    `json:"chunkOffset"`
			ChunkLimit    *int    `json:"chunkLimit"`
			AnchorChunkID *string `json:"anchorChunkId"`
			AnchorIndex   *int    `json:"anchorIndex"`
			Before        *int    `json:"before"`
			After         *int    `json:"after"`
			MaxTokens     *int    `json:"maxTokens"`
			Focus         *string `json:"focus"`
			CrossHeading  *bool   `json:"crossHeading"`
		}
		if err := decodeArguments(request.Arguments, &args); err != nil {
			return nil, err
		}
		doc, err := requireEnabledDocument(service, args.DocumentID)
		if err != nil {
			return nil, err
		}
		if args.AnchorChunkID != nil && args.AnchorIndex != nil {
			return nil, fmt.Errorf("provide exactly one of anchorChunkId or anchorIndex")
		}
		anchored := args.AnchorChunkID != nil || args.AnchorIndex != nil
		hasPagination := args.ChunkOffset != nil || args.ChunkLimit != nil
		hasContextControls := args.Before != nil || args.After != nil || args.MaxTokens != nil || args.Focus != nil || args.CrossHeading != nil
		if anchored && hasPagination {
			return nil, fmt.Errorf("anchor parameters cannot be mixed with chunkOffset or chunkLimit")
		}
		if !anchored && hasContextControls {
			return nil, fmt.Errorf("context controls require an anchor")
		}
		if anchored {
			opts := knowledge.ContextOptions{Focus: derefString(args.Focus), CrossHeading: derefBool(args.CrossHeading)}
			opts.AnchorChunkID = derefString(args.AnchorChunkID)
			opts.AnchorIndex = args.AnchorIndex
			opts.Before = args.Before
			opts.After = args.After
			opts.MaxTokens = derefInt(args.MaxTokens)
			window, err := service.GetDocumentContext(doc.ID, opts)
			if err != nil {
				return nil, err
			}
			page := documentPage{ReadMode: "context", ID: doc.ID, Title: doc.Title, SourceType: doc.SourceType, CharCount: doc.CharCount, ChunkCount: doc.ChunkCount}
			page.Truncated = window.HasMoreBefore || window.HasMoreAfter
			page.ContextWindow = window
			return page, nil
		}
		offset := derefClampInt(args.ChunkOffset, 0, doc.ChunkCount, 0)
		limit := derefClampInt(args.ChunkLimit, 1, 50, 20)
		chunks, err := service.ListChunks(doc.ID, limit, offset)
		if err != nil {
			return nil, err
		}
		next := offset + len(chunks)
		return documentPage{
			ReadMode: "page", ID: doc.ID, Title: doc.Title, SourceType: doc.SourceType,
			CharCount: doc.CharCount, ChunkCount: doc.ChunkCount,
			NextChunkOffset: next, Truncated: next < doc.ChunkCount, Chunks: chunks,
		}, nil

	case "knowledge_read_document":
		var args struct {
			DocumentID string `json:"documentId"`
			CharStart  *int   `json:"charStart"`
			CharEnd    *int   `json:"charEnd"`
			Pattern    string `json:"pattern"`
			MaxMatches *int   `json:"maxMatches"`
			IgnoreCase *bool  `json:"ignoreCase"`
		}
		if err := decodeArguments(request.Arguments, &args); err != nil {
			return nil, err
		}
		doc, err := requireEnabledDocument(service, args.DocumentID)
		if err != nil {
			return nil, err
		}
		full, _, err := service.GetDocument(doc.ID, false)
		if err != nil {
			return nil, err
		}
		runes := []rune(full.RawText)
		if args.Pattern != "" {
			expression := args.Pattern
			if derefBool(args.IgnoreCase, true) {
				expression = "(?i)" + expression
			}
			compiled, err := regexp.Compile(expression)
			if err != nil {
				return nil, err
			}
			maxMatches := derefClampInt(args.MaxMatches, 1, 200, 50)
			type match struct {
				Line      int    `json:"line"`
				CharStart int    `json:"charStart"`
				CharEnd   int    `json:"charEnd"`
				Snippet   string `json:"snippet"`
			}
			lineStarts := lineStartOffsets(full.RawText)
			var matches []match
			for _, loc := range compiled.FindAllStringIndex(full.RawText, maxMatches) {
				line := lineNumber(lineStarts, loc[0])
				lineStart := lineStarts[line-1]
				snippetEnd := loc[1]
				if snippetEnd-lineStart > 240 {
					snippetEnd = lineStart + 240
				}
				matches = append(matches, match{
					Line:      line,
					CharStart: len([]rune(full.RawText[:loc[0]])),
					CharEnd:   len([]rune(full.RawText[:loc[1]])),
					Snippet:   full.RawText[lineStart:snippetEnd],
				})
			}
			return map[string]any{"documentId": doc.ID, "title": doc.Title, "totalMatches": len(matches), "matches": matches}, nil
		}
		start := derefClampInt(args.CharStart, 0, len(runes), 0)
		defaultEnd := start + 20000
		end := derefClampInt(args.CharEnd, 0, len(runes), minInt(defaultEnd, len(runes)))
		if end < start {
			end = start
		}
		content := string(runes[start:end])
		return map[string]any{
			"documentId": doc.ID, "title": doc.Title, "totalChars": len(runes),
			"charStart": start, "charEnd": end, "content": content, "truncated": end < len(runes),
		}, nil

	case "knowledge_reindex_document":
		var args struct {
			BaseID     string `json:"baseId"`
			DocumentID string `json:"documentId"`
		}
		if err := decodeArguments(request.Arguments, &args); err != nil {
			return nil, err
		}
		doc, err := requireEnabledDocument(service, args.DocumentID)
		if err != nil {
			return nil, err
		}
		if doc.BaseID != args.BaseID {
			return nil, fmt.Errorf("document %q does not belong to knowledge base %s", doc.Title, args.BaseID)
		}
		reindexed, err := service.ReindexDocument(ctx, doc.ID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"id": reindexed.ID, "title": reindexed.Title, "chunkCount": reindexed.ChunkCount}, nil

	case "knowledge_reindex_base":
		var args struct {
			BaseID string `json:"baseId"`
		}
		if err := decodeArguments(request.Arguments, &args); err != nil {
			return nil, err
		}
		if err := requireEnabledBase(service, args.BaseID); err != nil {
			return nil, err
		}
		jobID, err := service.ReindexBase(ctx, args.BaseID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"jobId": jobID, "submitted": true}, nil

	default:
		return nil, fmt.Errorf("unknown tool %q", request.Name)
	}
}

func decodeArguments(arguments map[string]any, target any) error {
	if arguments == nil {
		return nil
	}
	encoded, err := json.Marshal(arguments)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(strings.NewReader(string(encoded)))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

func enabledBases(service *knowledge.Service) ([]knowledge.BaseSummary, error) {
	state, err := service.EnabledScopeState()
	if err != nil {
		return nil, err
	}
	if !state.Enabled {
		return nil, fmt.Errorf("knowledge invocation is disabled")
	}
	bases, err := service.ListBases()
	if err != nil {
		return nil, err
	}
	if !state.Explicit {
		return bases, nil
	}
	allowed := make(map[string]bool, len(state.BaseIDs))
	for _, id := range state.BaseIDs {
		allowed[id] = true
	}
	out := make([]knowledge.BaseSummary, 0, len(bases))
	for _, base := range bases {
		if allowed[base.ID] {
			out = append(out, base)
		}
	}
	return out, nil
}

func requireEnabledBase(service *knowledge.Service, baseID string) error {
	if _, err := service.GetBase(baseID); err != nil {
		return err
	}
	state, err := service.EnabledScopeState()
	if err != nil {
		return err
	}
	if !state.Enabled {
		return fmt.Errorf("knowledge invocation is disabled")
	}
	for _, id := range state.BaseIDs {
		if id == baseID {
			return nil
		}
	}
	if !state.Explicit {
		return nil
	}
	if len(state.BaseIDs) == 0 {
		return fmt.Errorf("knowledge base %s is not enabled", baseID)
	}
	return fmt.Errorf("knowledge base %s is not enabled", baseID)
}

func requireEnabledDocument(service *knowledge.Service, documentID string) (knowledge.Document, error) {
	doc, _, err := service.GetDocument(documentID, false)
	if err != nil {
		return knowledge.Document{}, err
	}
	if err := requireEnabledBase(service, doc.BaseID); err != nil {
		return knowledge.Document{}, err
	}
	return doc, nil
}

func citations(hits []knowledge.SearchHit) []string {
	out := make([]string, 0, len(hits))
	for _, hit := range hits {
		source := hit.DocumentTitle
		if hit.Heading != "" {
			source += " / " + hit.Heading
		}
		var quote strings.Builder
		for _, line := range strings.Split(hit.Text, "\n") {
			quote.WriteString("> ")
			quote.WriteString(line)
			quote.WriteString("\n")
		}
		out = append(out, strings.TrimRight(quote.String(), "\n")+fmt.Sprintf("\n> -- %s (baseId=%s; docId=%s; chunkId=%s)", source, hit.BaseID, hit.DocID, hit.ChunkID))
	}
	return out
}

func derefInt(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func derefBool(value *bool, fallback ...bool) bool {
	if value == nil {
		if len(fallback) > 0 {
			return fallback[0]
		}
		return false
	}
	return *value
}

func derefClampInt(value *int, minimum, maximum, fallback int) int {
	if value == nil {
		return fallback
	}
	if *value < minimum {
		return minimum
	}
	if *value > maximum {
		return maximum
	}
	return *value
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func lineStartOffsets(text string) []int {
	starts := []int{0}
	for offset, char := range text {
		if char == '\n' {
			starts = append(starts, offset+1)
		}
	}
	return starts
}

func lineNumber(starts []int, offset int) int {
	return sort.SearchInts(starts, offset+1)
}
