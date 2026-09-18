package knowledge

import (
	"context"
	"errors"
	"fmt"

	"github.com/shutu-ai/shutu-knowledge/internal/semantic"
)

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

// CompileSemanticMemory performs the deterministic PH2 full-base compilation:
// Document IR -> Facts/Concepts/Topics/Summaries. It is explicit and optional,
// requires no LLM, does not rebuild the evidence index, and publishes the
// result through the semantic store's atomic generation fence.
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

	documents, err := s.store.listDocumentMetadataContext(ctx, baseID)
	if err != nil {
		return semantic.Compilation{}, err
	}
	sources := make([]semantic.SourceDocument, 0, len(documents))
	for _, document := range documents {
		if document.Status != StatusReady {
			continue
		}
		ir, err := s.store.listDocumentIR(ctx, document.ID)
		if err != nil {
			return semantic.Compilation{}, fmt.Errorf("load document IR %s: %w", document.ID, err)
		}
		if len(ir.Nodes) == 0 {
			// Legacy 0.2-era rows may predate Document IR. They remain fully
			// searchable, but cannot be compiled without fabricating nodes.
			continue
		}
		chunks, err := s.store.chunkIDsByNode(ctx, document.ID, document.ActiveIndexGen)
		if err != nil {
			return semantic.Compilation{}, fmt.Errorf("load evidence links %s: %w", document.ID, err)
		}
		sources = append(sources, semantic.SourceDocument{
			BaseID: baseID, DocumentID: document.ID, Title: document.Title,
			IndexGeneration: document.ActiveIndexGen, SourceVersion: document.SourceVersion,
			UpdatedAt: document.UpdatedAt, IR: &ir, ChunkIDsByNode: chunks,
		})
	}
	if len(sources) == 0 {
		return semantic.Compilation{}, fmt.Errorf("no ready documents with Document IR in base %s", baseID)
	}

	generation, err := s.semanticStore.NextGeneration(ctx, baseID)
	if err != nil {
		return semantic.Compilation{}, err
	}
	compilation, err := semantic.Compile(baseID, generation, sources, now())
	if err != nil {
		return semantic.Compilation{}, err
	}
	if err := s.semanticStore.CreateCompilation(ctx, compilation); err != nil {
		return semantic.Compilation{}, err
	}
	if err := s.semanticStore.ActivateCompilation(ctx, baseID, generation); err != nil {
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
