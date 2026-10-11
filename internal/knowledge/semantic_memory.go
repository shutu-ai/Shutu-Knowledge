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

// SemanticCompileProgress observes bounded compile phases. It is intentionally
// read-only: progress reporting must never change compiler semantics.
type SemanticCompileProgress func(phase string, completed, total int)

func reportSemanticProgress(progress SemanticCompileProgress, phase string, completed, total int) {
	if progress != nil {
		progress(phase, completed, total)
	}
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
	return s.activeSemanticCompilation(ctx, baseID)
}

// activeSemanticCompilation caches the immutable active graph in memory. Large
// product corpora otherwise reload tens of thousands of units and evidence
// links for every semantic search/context/wiki request.
func (s *Service) activeSemanticCompilation(ctx context.Context, baseID string) (semantic.Compilation, error) {
	s.semanticCacheMu.RLock()
	cached, ok := s.semanticActiveCache[baseID]
	s.semanticCacheMu.RUnlock()
	if ok {
		return cached, nil
	}
	compilation, err := s.semanticStore.GetActiveCompilation(ctx, baseID)
	if errors.Is(err, semantic.ErrNotFound) {
		return semantic.Compilation{}, ErrNotFound
	}
	if err != nil {
		return semantic.Compilation{}, err
	}
	s.cacheSemanticCompilation(compilation)
	return compilation, nil
}

func (s *Service) semanticCompilationByID(ctx context.Context, baseID string, generation int64) (semantic.Compilation, error) {
	key := semanticCacheKey(baseID, generation)
	s.semanticCacheMu.RLock()
	cached, ok := s.semanticIDCache[key]
	s.semanticCacheMu.RUnlock()
	if ok {
		return cached, nil
	}
	compilation, err := s.semanticStore.GetCompilation(ctx, baseID, generation)
	if errors.Is(err, semantic.ErrNotFound) {
		return semantic.Compilation{}, ErrNotFound
	}
	if err != nil {
		return semantic.Compilation{}, err
	}
	s.cacheSemanticCompilation(compilation)
	return compilation, nil
}

func semanticCacheKey(baseID string, generation int64) string {
	return fmt.Sprintf("%s:%d", baseID, generation)
}

func (s *Service) cacheSemanticCompilation(compilation semantic.Compilation) {
	s.semanticCacheMu.Lock()
	defer s.semanticCacheMu.Unlock()
	key := semanticCacheKey(compilation.BaseID, compilation.Generation)
	s.semanticIDCache[key] = compilation
	if compilation.State == semantic.CompilationActive {
		s.semanticActiveCache[compilation.BaseID] = compilation
	}
}

func (s *Service) invalidateSemanticCompilation(baseID string) {
	s.semanticCacheMu.Lock()
	defer s.semanticCacheMu.Unlock()
	delete(s.semanticActiveCache, baseID)
	prefix := baseID + ":"
	for key := range s.semanticIDCache {
		if strings.HasPrefix(key, prefix) {
			delete(s.semanticIDCache, key)
		}
	}
}

// CompileSemanticMemory consumes the durable dirty set and performs an
// incremental Fact/Concept/Topic/Summary recompile. Unchanged provenance is
// carried into a new immutable generation; only dirty/deleted document evidence
// is removed and rebuilt. The Evidence Index is never rebuilt.
func (s *Service) CompileSemanticMemory(ctx context.Context, baseID string) (semantic.Compilation, error) {
	return s.CompileSemanticMemoryWithProgress(ctx, baseID, nil)
}

// CountSemanticCompileDocumentsContext returns ready documents that can
// contribute Document IR to semantic compilation. It is the bounded progress
// denominator used when admitting a durable compile operation.
func (s *Service) CountSemanticCompileDocumentsContext(ctx context.Context, baseID string) (int, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, err := s.store.getBaseContext(ctx, baseID); err != nil {
		return 0, err
	}
	return s.store.countSemanticCompileDocumentsContext(ctx, baseID)
}

// CompileSemanticMemoryWithProgress is the durable-operation entry point. It
// serializes compilers for one service instance, avoids loading unchanged IR
// after a modern compilation, and reports load/compile/merge/publish phases.
func (s *Service) CompileSemanticMemoryWithProgress(ctx context.Context, baseID string, progress SemanticCompileProgress) (semantic.Compilation, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.semanticCompileMu.Lock()
	defer s.semanticCompileMu.Unlock()

	base, err := s.store.getBaseContext(ctx, baseID)
	if err != nil {
		return semantic.Compilation{}, err
	}
	if base.LifecycleState != LifecycleActive {
		return semantic.Compilation{}, ErrConflict
	}

	reportSemanticProgress(progress, "loading_semantic_metadata", 0, 0)
	documents, err := s.store.listDocumentMetadataContext(ctx, baseID)
	if err != nil {
		return semantic.Compilation{}, err
	}
	ready := make([]Document, 0, len(documents))
	documentByID := make(map[string]Document, len(documents))
	metadataSignatures := make(map[string]string, len(documents))
	for _, document := range documents {
		if document.Status != StatusReady {
			continue
		}
		ready = append(ready, document)
		documentByID[document.ID] = document
		// Directory roots are ready metadata rows but have no Document IR and
		// are intentionally outside SourceDocumentIDs. Do not treat their
		// always-missing signature as a dirty source on every no-op compile.
		if document.SourceType != "directory" {
			metadataSignatures[document.ID] = semanticSourceMetadataSignature(document)
		}
	}
	reportSemanticProgress(progress, "loading_semantic_metadata", len(ready), len(ready))

	previous, previousErr := s.semanticStore.GetActiveCompilation(ctx, baseID)
	if previousErr != nil && !errors.Is(previousErr, semantic.ErrNotFound) {
		return semantic.Compilation{}, previousErr
	}
	hasPrevious := previousErr == nil
	compilerChanged := hasPrevious && previous.CompilerVersion != semantic.BuiltinCompilerVersion
	pending, err := s.semanticStore.PendingDocumentChanges(ctx, baseID)
	if err != nil {
		return semantic.Compilation{}, err
	}

	priorMetadata := map[string]string{}
	if raw := previous.Metadata["source_metadata_signatures"]; raw != "" {
		if err := json.Unmarshal([]byte(raw), &priorMetadata); err != nil {
			return semantic.Compilation{}, fmt.Errorf("decode semantic source metadata signatures: %w", err)
		}
	}
	priorFingerprints := map[string]string{}
	if raw := previous.Metadata["source_fingerprints"]; raw != "" {
		if err := json.Unmarshal([]byte(raw), &priorFingerprints); err != nil {
			return semantic.Compilation{}, fmt.Errorf("decode semantic source fingerprints: %w", err)
		}
	}
	priorSource := make(map[string]bool, len(previous.SourceDocumentIDs))
	for _, documentID := range previous.SourceDocumentIDs {
		priorSource[documentID] = true
	}

	dirty := map[string]bool{}
	deleted := map[string]bool{}
	var loaded []semantic.SourceDocument
	loadedByID := map[string]semantic.SourceDocument{}
	currentIDs := make([]string, 0, len(ready))
	fingerprints := map[string]string{}
	useMetadataFastPath := hasPrevious && len(priorMetadata) > 0

	if useMetadataFastPath {
		for _, change := range pending {
			if _, exists := metadataSignatures[change.DocumentID]; exists {
				dirty[change.DocumentID] = true
			} else {
				deleted[change.DocumentID] = true
			}
		}
		for documentID, signature := range metadataSignatures {
			if priorMetadata[documentID] != signature {
				dirty[documentID] = true
			}
		}
		for documentID := range priorMetadata {
			if _, exists := metadataSignatures[documentID]; !exists {
				deleted[documentID] = true
			}
		}
		if compilerChanged {
			for documentID := range metadataSignatures {
				dirty[documentID] = true
			}
		}
		if len(dirty)+len(deleted) == 0 {
			if err := s.semanticStore.ResolveDocumentChanges(ctx, baseID, previous.Generation, now()); err != nil {
				return semantic.Compilation{}, err
			}
			reportSemanticProgress(progress, "ready", len(ready), len(ready))
			s.cacheSemanticCompilation(previous)
			return previous, nil
		}

		dirtyDocuments := make([]Document, 0, len(dirty))
		for _, document := range ready {
			if dirty[document.ID] {
				dirtyDocuments = append(dirtyDocuments, document)
			}
		}
		loaded, err = s.loadSemanticSourceDocuments(ctx, baseID, dirtyDocuments, progress)
		if err != nil {
			return semantic.Compilation{}, err
		}
		for _, source := range loaded {
			loadedByID[source.DocumentID] = source
		}
		for documentID := range metadataSignatures {
			_, wasSource := priorSource[documentID]
			_, isDirty := dirty[documentID]
			if wasSource && !isDirty || loadedByID[documentID].DocumentID != "" {
				currentIDs = append(currentIDs, documentID)
			}
		}
		for _, documentID := range currentIDs {
			if source, ok := loadedByID[documentID]; ok {
				fingerprints[documentID] = semantic.SourceFingerprint(source)
				continue
			}
			fingerprint, ok := priorFingerprints[documentID]
			if !ok {
				return semantic.Compilation{}, fmt.Errorf("semantic source %s lacks a reusable fingerprint", documentID)
			}
			fingerprints[documentID] = fingerprint
		}
	} else {
		var state *semanticSourceState
		state, err = s.loadSemanticSourceState(ctx, baseID, progress)
		if err != nil {
			return semantic.Compilation{}, err
		}
		loaded = state.documents
		for _, source := range loaded {
			loadedByID[source.DocumentID] = source
		}
		currentIDs = state.ids
		fingerprints = state.current
		dirty, deleted, err = s.semanticDirtyDocuments(ctx, baseID, state, previous, hasPrevious)
		if err != nil {
			return semantic.Compilation{}, err
		}
	}

	if !hasPrevious && len(loaded) == 0 {
		return semantic.Compilation{}, fmt.Errorf("no ready documents with Document IR in base %s", baseID)
	}
	if hasPrevious && !compilerChanged && len(dirty)+len(deleted) == 0 {
		if err := s.semanticStore.ResolveDocumentChanges(ctx, baseID, previous.Generation, now()); err != nil {
			return semantic.Compilation{}, err
		}
		reportSemanticProgress(progress, "ready", len(currentIDs), len(currentIDs))
		return previous, nil
	}

	generation, err := s.semanticStore.NextGeneration(ctx, baseID)
	if err != nil {
		return semantic.Compilation{}, err
	}
	timestamp := now()
	reportSemanticProgress(progress, "compiling_documents", 0, len(loaded))

	var compiled semantic.Compilation
	if !hasPrevious || compilerChanged {
		compiled, err = semantic.Compile(baseID, generation, loaded, timestamp)
	} else {
		dirtyDocuments := make([]semantic.SourceDocument, 0, len(loaded))
		for _, documentID := range currentIDs {
			if dirty[documentID] {
				if source, ok := loadedByID[documentID]; ok {
					dirtyDocuments = append(dirtyDocuments, source)
				}
			}
		}
		var delta semantic.Compilation
		if len(dirtyDocuments) > 0 {
			delta, err = semantic.Compile(baseID, generation, dirtyDocuments, timestamp)
		} else {
			delta = emptySemanticDelta(baseID, generation, timestamp)
		}
		if err == nil {
			reportSemanticProgress(progress, "merging_semantic_memory", len(dirtyDocuments), len(dirtyDocuments))
			compiled, err = semantic.MergeIncremental(previous, delta, dirty, deleted, currentIDs, timestamp)
		}
	}
	if err != nil {
		return semantic.Compilation{}, err
	}
	reportSemanticProgress(progress, "compiling_documents", len(loaded), len(loaded))

	// MergeIncremental combines metadata maps, but a dirty delta legitimately
	// contains only dirty fingerprints. Rebuild both complete maps here so an
	// unchanged document remains reusable on the next no-op/incremental run.
	encodedFingerprints, err := json.Marshal(fingerprints)
	if err != nil {
		return semantic.Compilation{}, fmt.Errorf("marshal semantic fingerprints: %w", err)
	}
	currentMetadata := make(map[string]string, len(currentIDs))
	for _, documentID := range currentIDs {
		if document, ok := documentByID[documentID]; ok {
			currentMetadata[documentID] = semanticSourceMetadataSignature(document)
		}
	}
	encodedMetadata, err := json.Marshal(currentMetadata)
	if err != nil {
		return semantic.Compilation{}, fmt.Errorf("marshal semantic metadata signatures: %w", err)
	}
	compiled.Metadata["source_fingerprints"] = string(encodedFingerprints)
	compiled.Metadata["source_metadata_signatures"] = string(encodedMetadata)

	reportSemanticProgress(progress, "validating_semantic_memory", len(loaded), len(loaded))
	if err := s.semanticStore.CreateCompilation(ctx, compiled); err != nil {
		return semantic.Compilation{}, err
	}
	reportSemanticProgress(progress, "publishing_semantic_memory", len(loaded), len(loaded))
	if err := s.semanticStore.ActivateCompilation(ctx, baseID, generation); err != nil {
		return semantic.Compilation{}, err
	}
	s.invalidateSemanticCompilation(baseID)
	if err := s.semanticStore.ResolveDocumentChanges(ctx, baseID, generation, timestamp); err != nil {
		return semantic.Compilation{}, err
	}
	reportSemanticProgress(progress, "ready", len(currentIDs), len(currentIDs))
	active, err := s.activeSemanticCompilation(ctx, baseID)
	if err != nil {
		return semantic.Compilation{}, err
	}
	s.cacheSemanticCompilation(active)
	return active, nil
}

// GetSemanticCompilationByID returns one immutable compilation generation for
// an active base. It lets protocol-independent callers pin a query to a
// snapshot rather than silently reading the active generation twice.
func (s *Service) GetSemanticCompilationByID(ctx context.Context, baseID string, generation int64) (semantic.Compilation, error) {
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
	if generation <= 0 {
		return semantic.Compilation{}, ErrNotFound
	}
	compilation, err := s.semanticCompilationByID(ctx, baseID, generation)
	if errors.Is(err, semantic.ErrNotFound) {
		return semantic.Compilation{}, ErrNotFound
	}
	return compilation, err
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

func semanticSourceMetadataSignature(document Document) string {
	return fmt.Sprintf("%d:%d:%s", document.ActiveIndexGen, document.SourceVersion, document.Title)
}

func (s *Service) loadSemanticSourceState(ctx context.Context, baseID string, progress SemanticCompileProgress) (*semanticSourceState, error) {
	documents, err := s.store.listDocumentMetadataContext(ctx, baseID)
	if err != nil {
		return nil, err
	}
	ready := make([]Document, 0, len(documents))
	for _, document := range documents {
		if document.Status == StatusReady {
			ready = append(ready, document)
		}
	}
	sources, err := s.loadSemanticSourceDocuments(ctx, baseID, ready, progress)
	if err != nil {
		return nil, err
	}
	state := &semanticSourceState{
		documents: sources,
		byID:      make(map[string]semantic.SourceDocument, len(sources)),
		current:   make(map[string]string, len(sources)),
	}
	for _, source := range sources {
		state.byID[source.DocumentID] = source
		state.current[source.DocumentID] = semantic.SourceFingerprint(source)
		state.ids = append(state.ids, source.DocumentID)
	}
	return state, nil
}

func (s *Service) loadSemanticSourceDocuments(ctx context.Context, baseID string, documents []Document, progress SemanticCompileProgress) ([]semantic.SourceDocument, error) {
	irs, err := s.store.listDocumentIRsContext(ctx, documents)
	if err != nil {
		return nil, fmt.Errorf("load document IR batch: %w", err)
	}
	links, err := s.store.chunkIDsByNodes(ctx, documents)
	if err != nil {
		return nil, fmt.Errorf("load evidence link batch: %w", err)
	}
	out := make([]semantic.SourceDocument, 0, len(documents))
	for _, document := range documents {
		ir := irs[document.ID]
		if len(ir.Nodes) == 0 {
			// Legacy 0.2-era rows remain searchable but cannot be compiled
			// without fabricating Document IR provenance.
			continue
		}
		source := semantic.SourceDocument{
			BaseID: baseID, DocumentID: document.ID, Title: document.Title,
			IndexGeneration: document.ActiveIndexGen, SourceVersion: document.SourceVersion,
			UpdatedAt: document.UpdatedAt, TemporalMetadata: sourceTemporalMetadata(&ir),
			IR: &ir, ChunkIDsByNode: links[document.ID],
		}
		out = append(out, source)
		reportSemanticProgress(progress, "loading_document_ir", len(out), len(documents))
	}
	return out, nil
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
	compilation, err := s.activeSemanticCompilation(ctx, baseID)
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
	compilation, err := s.activeSemanticCompilation(ctx, baseID)
	if errors.Is(err, semantic.ErrNotFound) {
		return semantic.ContextPackage{}, ErrNotFound
	}
	if err != nil {
		return semantic.ContextPackage{}, err
	}
	return s.compileKnowledgeContextWithCompilation(ctx, baseID, compilation, query, tokenBudget, nil)
}

// CompileKnowledgeContextByID compiles context against one immutable semantic
// generation. Evidence search still resolves current chunk content through the
// base; each evidence item retains its original generation/version identity.
func (s *Service) CompileKnowledgeContextByID(ctx context.Context, baseID string, generation int64, query string, tokenBudget int) (semantic.ContextPackage, error) {
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
	if generation <= 0 {
		return semantic.ContextPackage{}, ErrNotFound
	}
	compilation, err := s.semanticCompilationByID(ctx, baseID, generation)
	if errors.Is(err, semantic.ErrNotFound) {
		return semantic.ContextPackage{}, ErrNotFound
	}
	if err != nil {
		return semantic.ContextPackage{}, err
	}
	return s.compileKnowledgeContextWithCompilation(ctx, baseID, compilation, query, tokenBudget, nil)
}

func (s *Service) compileKnowledgeContextWithCompilation(ctx context.Context, baseID string, compilation semantic.Compilation, query string, tokenBudget int, temporal *semantic.TemporalQuery) (semantic.ContextPackage, error) {
	memory, err := semantic.SearchCompilation(compilation, semantic.SearchOptions{Query: query, TopK: 8, Temporal: temporal})
	if err != nil {
		return semantic.ContextPackage{}, err
	}
	queries := []string{query}
	plan := semantic.RouteQuery(query)
	if temporal != nil {
		plan.Temporal = *temporal
	}
	// An implicit CURRENT policy must not turn the corpus-wide latest version
	// into an extra evidence query. In a product corpus most documents share
	// that version, so the old fallback preferred an arbitrary same-version
	// document over the exact hybrid hit. Version projection is reserved for
	// an explicitly requested/comparable version scope.
	targetVersions := make([]string, 0, 2)
	explicitVersionScope := len(plan.Temporal.Versions) > 0
	if explicitVersionScope {
		targetVersions = append(targetVersions, plan.Temporal.Versions...)
		for _, version := range plan.Temporal.Versions {
			if len(queries) >= 6 {
				break
			}
			queries = append(queries, version+" release")
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
		BaseID: baseID, Query: query, TopK: 8, Mode: "hybrid",
	})
	if err != nil {
		return semantic.ContextPackage{}, err
	}
	// Title expansion is a fallback for abstract/global queries. Running it in
	// addition to an exact hit lets a shared-version title outscore the exact
	// requested chunk, so preserve the primary retrieval result when it exists.
	if len(search.Hits) == 0 && len(queries) > 1 {
		search, err = s.Search(ctx, SearchRequest{
			BaseID: baseID, Query: query, Queries: queries, TopK: 8, Mode: "hybrid",
		})
		if err != nil {
			return semantic.ContextPackage{}, err
		}
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
	if plan.Temporal.Intent == semantic.TemporalRangeHistory {
		historyEvidence, err := s.historyRangeEvidence(ctx, compilation, query, plan.Temporal)
		if err != nil {
			return semantic.ContextPackage{}, err
		}
		return semantic.CompileContext(compilation, historyEvidence, semantic.ContextCompileOptions{
			Query: query, TokenBudget: tokenBudget, TopK: 8, FactTopK: 4, Temporal: temporal,
		})
	}
	var targetedEvidence []semantic.ContextEvidence
	if explicitVersionScope {
		targetedEvidence, err = s.versionEvidence(ctx, compilation, query, targetVersions)
		if err != nil {
			return semantic.ContextPackage{}, err
		}
		evidenceItems = append(targetedEvidence, evidenceItems...)
	}
	return semantic.CompileContext(compilation, evidenceItems, semantic.ContextCompileOptions{
		Query: query, TokenBudget: tokenBudget, TopK: 8, FactTopK: 4, Temporal: temporal,
	})
}

// CompileKnowledgeContextWithPolicy is the policy-aware active-generation form
// used by the shared query application.
func (s *Service) CompileKnowledgeContextWithPolicy(ctx context.Context, baseID, query string, tokenBudget int, temporal *semantic.TemporalQuery) (semantic.ContextPackage, error) {
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
	compilation, err := s.activeSemanticCompilation(ctx, baseID)
	if errors.Is(err, semantic.ErrNotFound) {
		return semantic.ContextPackage{}, ErrNotFound
	}
	if err != nil {
		return semantic.ContextPackage{}, err
	}
	return s.compileKnowledgeContextWithCompilation(ctx, baseID, compilation, query, tokenBudget, temporal)
}

// CompileKnowledgeContextByIDWithPolicy is the policy-aware immutable
// generation form used by the shared query application.
func (s *Service) CompileKnowledgeContextByIDWithPolicy(ctx context.Context, baseID string, generation int64, query string, tokenBudget int, temporal *semantic.TemporalQuery) (semantic.ContextPackage, error) {
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
	if generation <= 0 {
		return semantic.ContextPackage{}, ErrNotFound
	}
	compilation, err := s.semanticCompilationByID(ctx, baseID, generation)
	if errors.Is(err, semantic.ErrNotFound) {
		return semantic.ContextPackage{}, ErrNotFound
	}
	if err != nil {
		return semantic.ContextPackage{}, err
	}
	return s.compileKnowledgeContextWithCompilation(ctx, baseID, compilation, query, tokenBudget, temporal)
}

// historyRangeEvidence projects one provenanced representative for each selected
// historical range bucket. It reuses exact Knowledge Unit sources and never
// fabricates timeline content.
func (s *Service) historyRangeEvidence(ctx context.Context, compilation semantic.Compilation, query string, temporal semantic.TemporalQuery) ([]semantic.ContextEvidence, error) {
	if temporal.Range == nil {
		return nil, nil
	}
	representatives := semantic.SelectHistoricalRepresentatives(compilation, *temporal.Range, 8, query, semantic.SearchTerms(query))
	if len(representatives) == 0 {
		return nil, nil
	}
	evidenceItems := make([]semantic.ContextEvidence, 0, len(representatives))
	seen := map[string]bool{}
	for rank, unit := range representatives {
		sources, err := semantic.ResolveEvidence(compilation.Units, unit.ID)
		if err != nil {
			return nil, err
		}
		for _, source := range sources {
			key := unit.Version + "\x00" + source.DocumentID + "\x00" + source.ChunkID + "\x00" + source.NodeID
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
			text = semantic.ClipHistoryEvidenceText(text, 120)
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
				Version: unit.Version, TemporalStatus: "history",
				Score: 1000 - float64(rank),
			})
			break
		}
	}
	return evidenceItems, nil
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
				text = semantic.ClipHistoryEvidenceText(text, 120)
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
	compilation, err := s.activeSemanticCompilation(ctx, baseID)
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
