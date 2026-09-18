package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

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
			UpdatedAt: document.UpdatedAt, IR: &ir, ChunkIDsByNode: chunks,
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
	search, err := s.Search(ctx, SearchRequest{
		BaseID: baseID, Query: query, TopK: 8, Mode: "hybrid",
	})
	if err != nil {
		return semantic.ContextPackage{}, err
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
	return semantic.CompileContext(compilation, evidenceItems, semantic.ContextCompileOptions{
		Query: query, TokenBudget: tokenBudget, TopK: 8, FactTopK: 4,
	})
}

// PlanKnowledgeQuery returns deterministic AUTO-mode diagnostics. It does not
// call a model, mutate the query, or perform retrieval.
func (s *Service) PlanKnowledgeQuery(query string) semantic.QueryPlan {
	return semantic.RouteQuery(query)
}
