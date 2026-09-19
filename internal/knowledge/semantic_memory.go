package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/shutu-ai/shutu-knowledge/internal/documentir"
	"github.com/shutu-ai/shutu-knowledge/internal/evidence"
	"github.com/shutu-ai/shutu-knowledge/internal/semantic"
)

type semanticSourceState struct {
	documents []semantic.SourceDocument
	byID      map[string]semantic.SourceDocument
	current   map[string]string
	ids       []string
}

// GetSemanticCompilation returns the active optional semantic-memory generation.
// A 0.3 KB that has never compiled semantic memory returns ErrNotFound while
// all existing RAG behavior remains available.
func (s *Service) GetSemanticCompilation(ctx context.Context, baseID string) (semantic.Compilation, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, err := s.store.getBaseContext(ctx, baseID); err != nil {
		return semantic.Compilation{}, err
	}
	compilation, err := s.semanticStore.GetActiveCompilation(ctx, baseID)
	if errors.Is(err, semantic.ErrNotFound) {
		return semantic.Compilation{}, ErrNotFound
	}
	return compilation, err
}

// CompileSemanticMemory consumes the durable dirty set and performs an
// incremental Fact/Concept/Topic/Summary recompile. Unchanged provenance is
// carried into a new immutable generation; only dirty/deleted document evidence
// is removed and rebuilt. The Evidence Index is never rebuilt.
func (s *Service) CompileSemanticMemory(ctx context.Context, baseID string) (semantic.Compilation, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	base, err := s.store.getBaseContext(ctx, baseID)
	if err != nil {
		return semantic.Compilation{}, err
	}
	if base.LifecycleState != LifecycleActive {
		return semantic.Compilation{}, ErrConflict
	}

	state, err := s.loadSemanticSourceState(ctx, baseID)
	if err != nil {
		return semantic.Compilation{}, err
	}
	previous, previousErr := s.semanticStore.GetActiveCompilation(ctx, baseID)
	if previousErr != nil && !errors.Is(previousErr, semantic.ErrNotFound) {
		return semantic.Compilation{}, previousErr
	}
	hasPrevious := previousErr == nil

	dirty, deleted, err := s.semanticDirtyDocuments(ctx, baseID, state, previous, hasPrevious)
	if err != nil {
		return semantic.Compilation{}, err
	}
	generation, err := s.semanticStore.NextGeneration(ctx, baseID)
	if err != nil {
		return semantic.Compilation{}, err
	}
	timestamp := now()

	var compiled semantic.Compilation
	if !hasPrevious {
		if len(state.documents) == 0 {
			return semantic.Compilation{}, fmt.Errorf("no ready documents with Document IR in base %s", baseID)
		}
		compiled, err = semantic.Compile(baseID, generation, state.documents, timestamp)
	} else if len(dirty)+len(deleted) == 0 {
		if err := s.semanticStore.ResolveDocumentChanges(ctx, baseID, previous.Generation, timestamp); err != nil {
			return semantic.Compilation{}, err
		}
		return previous, nil
	} else {
		dirtyDocuments := make([]semantic.SourceDocument, 0, len(dirty))
		for documentID := range dirty {
			if document, ok := state.byID[documentID]; ok {
				dirtyDocuments = append(dirtyDocuments, document)
			}
		}
		var delta semantic.Compilation
		if len(dirtyDocuments) > 0 {
			delta, err = semantic.Compile(baseID, generation, dirtyDocuments, timestamp)
		} else {
			delta = emptySemanticDelta(baseID, generation, timestamp)
		}
		if err == nil {
			compiled, err = semantic.MergeIncremental(previous, delta, dirty, deleted, state.ids, timestamp)
		}
	}
	if err != nil {
		return semantic.Compilation{}, err
	}
	if err := s.semanticStore.CreateCompilation(ctx, compiled); err != nil {
		return semantic.Compilation{}, err
	}
	if err := s.semanticStore.ActivateCompilation(ctx, baseID, generation); err != nil {
		return semantic.Compilation{}, err
	}
	if err := s.semanticStore.ResolveDocumentChanges(ctx, baseID, generation, timestamp); err != nil {
		return semantic.Compilation{}, err
	}
	return s.semanticStore.GetActiveCompilation(ctx, baseID)
}

// ResolveSemanticUnitEvidence exposes the exact Document IR/chunk pointers
// behind a compiled semantic unit. It never substitutes a chunk search result.
func (s *Service) ResolveSemanticUnitEvidence(ctx context.Context, unitID string) ([]semantic.EvidenceSource, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	sources, err := s.semanticStore.ResolveUnitEvidence(ctx, unitID)
	if errors.Is(err, semantic.ErrNotFound) {
		return nil, ErrNotFound
	}
	return sources, err
}

func (s *Service) loadSemanticSourceState(ctx context.Context, baseID string) (*semanticSourceState, error) {
	documents, err := s.store.listDocumentMetadataContext(ctx, baseID)
	if err != nil {
		return nil, err
	}
	state := &semanticSourceState{
		byID:    map[string]semantic.SourceDocument{},
		current: map[string]string{},
	}
	for _, document := range documents {
		if document.Status != StatusReady {
			continue
		}
		ir, err := s.store.listDocumentIR(ctx, document.ID)
		if err != nil {
			return nil, fmt.Errorf("load document IR %s: %w", document.ID, err)
		}
		if len(ir.Nodes) == 0 {
			// Legacy 0.2-era rows remain searchable but cannot be compiled
			// without fabricating Document IR provenance.
			continue
		}
		chunks, err := s.store.chunkIDsByNode(ctx, document.ID, document.ActiveIndexGen)
		if err != nil {
			return nil, fmt.Errorf("load evidence links %s: %w", document.ID, err)
		}
		source := semantic.SourceDocument{
			BaseID: baseID, DocumentID: document.ID, Title: document.Title,
			IndexGeneration: document.ActiveIndexGen, SourceVersion: document.SourceVersion,
			UpdatedAt: document.UpdatedAt, TemporalMetadata: sourceTemporalMetadata(&ir),
			IR: &ir, ChunkIDsByNode: chunks,
		}
		state.documents = append(state.documents, source)
		state.byID[source.DocumentID] = source
		state.current[source.DocumentID] = semantic.SourceFingerprint(source)
		state.ids = append(state.ids, source.DocumentID)
	}
	return state, nil
}

func (s *Service) semanticDirtyDocuments(ctx context.Context, baseID string, state *semanticSourceState,
	previous semantic.Compilation, hasPrevious bool) (map[string]bool, map[string]bool, error) {
	dirty := map[string]bool{}
	deleted := map[string]bool{}
	pending, err := s.semanticStore.PendingDocumentChanges(ctx, baseID)
	if err != nil {
		return nil, nil, err
	}
	for _, change := range pending {
		if _, exists := state.byID[change.DocumentID]; exists {
			dirty[change.DocumentID] = true
		} else {
			deleted[change.DocumentID] = true
		}
	}
	if !hasPrevious {
		return dirty, deleted, nil
	}

	prior := map[string]string{}
	if raw := previous.Metadata["source_fingerprints"]; raw != "" {
		if err := json.Unmarshal([]byte(raw), &prior); err != nil {
			return nil, nil, fmt.Errorf("decode semantic source fingerprints: %w", err)
		}
	}
	if len(prior) == 0 {
		// A compilation produced before fingerprint metadata is always unsafe
		// to reuse; rebuild once, then incremental mode resumes.
		for documentID := range state.byID {
			dirty[documentID] = true
		}
		for _, documentID := range previous.SourceDocumentIDs {
			if _, exists := state.byID[documentID]; !exists {
				deleted[documentID] = true
			}
		}
		return dirty, deleted, nil
	}
	for documentID, fingerprint := range state.current {
		if prior[documentID] != fingerprint {
			dirty[documentID] = true
		}
	}
	for documentID := range prior {
		if _, exists := state.current[documentID]; !exists {
			deleted[documentID] = true
		}
	}
	return dirty, deleted, nil
}

func emptySemanticDelta(baseID string, generation, timestamp int64) semantic.Compilation {
	return semantic.Compilation{
		BaseID: baseID, Generation: generation, State: semantic.CompilationBuilding,
		Compiler: semantic.BuiltinCompiler, CompilerVersion: semantic.BuiltinCompilerVersion,
		Model: semantic.BuiltinModel, ModelVersion: semantic.BuiltinModelVersion,
		PromptVersion: semantic.BuiltinPromptVersion, CreatedAt: timestamp,
	}
}

func (s *Service) markSemanticDocumentUpdated(ctx context.Context, document *Document) error {
	if document == nil || document.Status != StatusReady {
		return nil
	}
	timestamp := now()
	return s.semanticStore.MarkDocumentChanged(ctx, semantic.DocumentChange{
		BaseID: document.BaseID, DocumentID: document.ID, ChangeType: "updated",
		IndexGeneration: document.ActiveIndexGen, SourceVersion: document.SourceVersion,
		ContentHash: document.ContentHash, CreatedAt: timestamp, UpdatedAt: timestamp,
	})
}

func (s *Service) markSemanticDocumentDeleted(ctx context.Context, baseID, documentID string) error {
	timestamp := now()
	return s.semanticStore.MarkDocumentChanged(ctx, semantic.DocumentChange{
		BaseID: baseID, DocumentID: documentID, ChangeType: "deleted",
		CreatedAt: timestamp, UpdatedAt: timestamp,
	})
}

// SearchSemanticMemory activates Concepts, Topics, and Summaries from the
// active compilation. It is an additional lane, not a replacement for the 0.3
// evidence search. Every hit carries its exact derived-from evidence closure.
func (s *Service) SearchSemanticMemory(ctx context.Context, baseID string, options semantic.SearchOptions) (semantic.SearchResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	base, err := s.store.getBaseContext(ctx, baseID)
	if err != nil {
		return semantic.SearchResponse{}, err
	}
	if base.LifecycleState != LifecycleActive {
		return semantic.SearchResponse{}, ErrConflict
	}
	options.Query = strings.TrimSpace(options.Query)
	compilation, err := s.semanticStore.GetActiveCompilation(ctx, baseID)
	if errors.Is(err, semantic.ErrNotFound) {
		return semantic.SearchResponse{}, ErrNotFound
	}
	if err != nil {
		return semantic.SearchResponse{}, err
	}
	return semantic.SearchCompilation(compilation, options)
}

// CompileKnowledgeContext combines active semantic orientation with the
// existing hybrid evidence search and returns one bounded ContextPackage.
// The 0.3 search pipeline remains the exact-evidence authority.
func (s *Service) CompileKnowledgeContext(ctx context.Context, baseID, query string, tokenBudget int) (semantic.ContextPackage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	base, err := s.store.getBaseContext(ctx, baseID)
	if err != nil {
		return semantic.ContextPackage{}, err
	}
	if base.LifecycleState != LifecycleActive {
		return semantic.ContextPackage{}, ErrConflict
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return semantic.ContextPackage{}, fmt.Errorf("knowledge context query is empty")
	}
	compilation, err := s.semanticStore.GetActiveCompilation(ctx, baseID)
	if errors.Is(err, semantic.ErrNotFound) {
		return semantic.ContextPackage{}, ErrNotFound
	}
	if err != nil {
		return semantic.ContextPackage{}, err
	}
	memory, err := s.SearchSemanticMemory(ctx, baseID, semantic.SearchOptions{Query: query, TopK: 8})
	if err != nil && !errors.Is(err, ErrNotFound) {
		return semantic.ContextPackage{}, err
	}
	queries := []string{query}
	// Temporal evidence tokens can be absent from the natural-language query
	// (especially “current”). Add the resolved scope as an additional bounded
	// retrieval query, then let semantic.CompileContext re-rank and filter.
	plan := semantic.RouteQuery(query)
	resolvedVersion := plan.Temporal.FromVersion
	if plan.Temporal.Intent == semantic.TemporalCurrent || plan.Temporal.Intent == semantic.TemporalValidity {
		if version, resolvable := semantic.ResolveCurrentVersion(compilation); resolvable {
			resolvedVersion = version
		}
	}
	targetVersions := make([]string, 0, 2)
	switch plan.Temporal.Intent {
	case semantic.TemporalEvolution, semantic.TemporalCompareVersions:
		targetVersions = append(targetVersions, plan.Temporal.Versions...)
		for _, version := range plan.Temporal.Versions {
			if len(queries) >= 6 {
				break
			}
			queries = append(queries, version+" release")
		}
	default:
		if resolvedVersion != "" {
			targetVersions = append(targetVersions, resolvedVersion)
			queries = append(queries, resolvedVersion+" release")
		}
	}
	for _, hit := range memory.Hits {
		if hit.Unit.Type != semantic.UnitConcept && hit.Unit.Type != semantic.UnitTopic {
			continue
		}
		if title := strings.TrimSpace(hit.Unit.Title); title != "" {
			queries = append(queries, title)
		}
		if len(queries) >= 6 {
			break
		}
	}
	search, err := s.Search(ctx, SearchRequest{
		BaseID: baseID, Query: query, Queries: queries, TopK: 8, Mode: "hybrid",
	})
	if err != nil {
		return semantic.ContextPackage{}, err
	}
	// Multi-query retrieval can dilute an exact version token. Run a small
	// targeted query for each requested scope and place those hits first so
	// semantic.CompileContext can enforce version scope without falling back.
	for _, version := range targetVersions {
		targeted, err := s.Search(ctx, SearchRequest{
			BaseID: baseID, Query: version + " release", TopK: 4, Mode: "hybrid",
		})
		if err != nil {
			return semantic.ContextPackage{}, err
		}
		search.Hits = append(targeted.Hits, search.Hits...)
	}
	evidenceItems := make([]semantic.ContextEvidence, 0, len(search.Hits))
	for _, hit := range search.Hits {
		text := hit.Text
		if hit.ContextWindow != nil {
			text = evidence.Serialize(*hit.ContextWindow)
		}
		citation := ""
		if hit.Citation != nil {
			citation = fmt.Sprintf("%s chunk=%s", hit.Citation.DocumentID, hit.Citation.ChunkID)
			if hit.Citation.Page > 0 {
				citation += fmt.Sprintf(" page=%d", hit.Citation.Page)
			}
			if hit.Citation.Section != "" {
				citation += " section=" + hit.Citation.Section
			}
		}
		evidenceItems = append(evidenceItems, semantic.ContextEvidence{
			ChunkID: hit.ChunkID, DocumentID: hit.DocID, DocumentTitle: hit.DocumentTitle,
			Heading: hit.Heading, Text: text, Citation: citation,
			IndexGeneration: hit.IndexGeneration, SourceVersion: hit.SourceVersion,
			Score: hit.Score,
		})
	}
	// Retrieval tokenizers often split versions such as 0.2.0. To guarantee
	// scope selection without inventing evidence, project the exact provenance
	// of version-matched Knowledge Units into the evidence candidate set.
	targetedEvidence, err := s.versionEvidence(ctx, compilation, query, targetVersions)
	if err != nil {
		return semantic.ContextPackage{}, err
	}
	evidenceItems = append(targetedEvidence, evidenceItems...)
	return semantic.CompileContext(compilation, evidenceItems, semantic.ContextCompileOptions{
		Query: query, TokenBudget: tokenBudget, TopK: 8, FactTopK: 4,
	})
}

// versionEvidence resolves exact evidence for units whose source version matches
// a requested temporal scope. It never fabricates content: text comes from the
// active chunk when available, otherwise from the provenanced Knowledge Unit.
func (s *Service) versionEvidence(ctx context.Context, compilation semantic.Compilation, query string, versions []string) ([]semantic.ContextEvidence, error) {
	if len(versions) == 0 {
		return nil, nil
	}
	wanted := make(map[string]bool, len(versions))
	for _, version := range versions {
		if version != "" {
			wanted[version] = true
		}
	}
	evidenceItems := make([]semantic.ContextEvidence, 0, len(versions))
	seen := make(map[string]bool)
	queryTerms := semantic.SearchTerms(query)
	for _, version := range versions {
		if !wanted[version] {
			continue
		}
		type candidate struct {
			index int
			score int
		}
		var candidates []candidate
		for i := range compilation.Units {
			unitVersion := compilation.Units[i].Version
			if order, comparable := semantic.CompareVersionIdentity(unitVersion, version); !comparable || order != 0 || compilation.Units[i].Status == semantic.UnitDeleted {
				continue
			}
			unit := compilation.Units[i]
			score := 0
			if len(queryTerms) > 0 {
				haystack := strings.ToLower(strings.Join(strings.Fields(unit.Title+"\n"+unit.Content), " "))
				for _, term := range queryTerms {
					if strings.Contains(haystack, term) {
						score++
					}
				}
				if score == 0 {
					continue
				}
			}
			candidates = append(candidates, candidate{index: i, score: score})
		}
		sort.SliceStable(candidates, func(i, j int) bool {
			if candidates[i].score != candidates[j].score {
				return candidates[i].score > candidates[j].score
			}
			return compilation.Units[candidates[i].index].CanonicalKey < compilation.Units[candidates[j].index].CanonicalKey
		})
		added := 0
		for _, candidate := range candidates {
			if added >= 4 {
				break
			}
			unit := compilation.Units[candidate.index]
			sources, err := semantic.ResolveEvidence(compilation.Units, unit.ID)
			if err != nil {
				return nil, err
			}
			for _, source := range sources {
				key := version + "\x00" + source.DocumentID + "\x00" + source.ChunkID + "\x00" + source.NodeID
				if seen[key] {
					continue
				}
				seen[key] = true
				title, chunks, err := s.GetDocumentWithContext(ctx, source.DocumentID, true)
				if err != nil {
					continue
				}
				text, heading, chunkID := "", "", source.ChunkID
				if source.ChunkID != "" {
					for _, chunk := range chunks {
						if chunk.ID == source.ChunkID {
							text, heading = chunk.Text, chunk.Heading
							break
						}
					}
				}
				if text == "" && source.NodeID != "" {
					for _, chunk := range chunks {
						for _, nodeID := range chunk.NodeIDs {
							if nodeID == source.NodeID {
								text, heading, chunkID = chunk.Text, chunk.Heading, chunk.ID
								break
							}
						}
						if text != "" {
							break
						}
					}
				}
				if text == "" {
					text = unit.Content
				}
				if chunkID == "" {
					chunkID = "node:" + source.NodeID
				}
				evidenceItems = append(evidenceItems, semantic.ContextEvidence{
					ChunkID: chunkID, DocumentID: source.DocumentID, DocumentTitle: title.Title,
					Heading: heading, Text: text,
					Citation:        fmt.Sprintf("%s chunk=%s", source.DocumentID, chunkID),
					IndexGeneration: source.IndexGeneration, SourceVersion: source.SourceVersion,
					Version: version, TemporalStatus: "targeted",
					Score: 1000 - float64(added),
				})
				added++
				if added >= 4 {
					break
				}
			}
		}
	}
	return evidenceItems, nil
}

// PlanKnowledgeQuery returns deterministic AUTO-mode diagnostics. It does not
// call a model, mutate the query, or perform retrieval.
func (s *Service) PlanKnowledgeQuery(query string) semantic.QueryPlan {
	return semantic.RouteQuery(query)
}

// GetSemanticWiki returns the deterministic, regenerable Living Wiki view for
// the active compilation. Wiki pages are projections of KnowledgeUnits and
// never become the source of truth.
func (s *Service) GetSemanticWiki(ctx context.Context, baseID string) (semantic.WikiView, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	base, err := s.store.getBaseContext(ctx, baseID)
	if err != nil {
		return semantic.WikiView{}, err
	}
	if base.LifecycleState != LifecycleActive {
		return semantic.WikiView{}, ErrConflict
	}
	compilation, err := s.semanticStore.GetActiveCompilation(ctx, baseID)
	if errors.Is(err, semantic.ErrNotFound) {
		return semantic.WikiView{}, ErrNotFound
	}
	if err != nil {
		return semantic.WikiView{}, err
	}
	return semantic.RenderWiki(compilation)
}

// sourceTemporalMetadata projects only the existing parser-supplied IR parse
// configuration into the temporal compiler input. It does not invent metadata.
func sourceTemporalMetadata(ir *documentir.Document) map[string]string {
	if ir == nil {
		return nil
	}
	keys := []string{"knowledge_version", "document_version", "release", "version", "temporal_version",
		"published_at", "published", "date", "revision_date", "effective_at", "effective",
		"valid_from", "source_authority", "source_priority"}
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		if value := strings.TrimSpace(ir.ParseConfig[key]); value != "" {
			out[key] = value
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
