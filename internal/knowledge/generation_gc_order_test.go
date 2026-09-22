package knowledge

import (
	"context"
	"errors"
	"testing"
)

func TestRetiredGenerationGCFailsClosedDuringChunkDeletion(t *testing.T) {
	f := newFixture(t)
	base := f.createBase(t)
	ctx := context.Background()

	doc, err := f.service.AddTextDocument(ctx, base.ID, "Generation GC", "# Storage\n\nretired generation GC ordering")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.ReindexDocument(ctx, doc.ID); err != nil {
		t.Fatal(err)
	}
	current, err := f.service.store.getDocument(doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	const retiredGeneration = int64(1)
	if current.ActiveIndexGen != 2 {
		t.Fatalf("active generation = %d, want 2", current.ActiveIndexGen)
	}

	// GC removes mapping and chunk batches in separate short transactions.
	// Record mapping deletion as the delete fence. A chunk-first implementation
	// aborts deterministically instead of exposing a partial generation to a
	// newly started reader.
	if _, err := f.service.store.db.Exec(`CREATE TEMP TABLE retired_gc_fences
		(doc_id TEXT NOT NULL, generation INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.store.db.Exec(`CREATE TEMP TRIGGER retired_gc_mapping_removed
		AFTER DELETE ON document_generations
		BEGIN
			INSERT INTO retired_gc_fences(doc_id, generation)
			VALUES (OLD.doc_id, OLD.index_generation);
		END`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.store.db.Exec(`CREATE TEMP TRIGGER retired_gc_requires_mapping_fence
		BEFORE DELETE ON chunks
		WHEN NOT EXISTS (
			SELECT 1 FROM retired_gc_fences
			WHERE doc_id = OLD.doc_id AND generation = OLD.index_generation
		)
		BEGIN
			SELECT RAISE(ABORT, 'generation mapping not removed before chunks');
		END`); err != nil {
		t.Fatal(err)
	}
	expired := now() - retiredGenerationRetentionMS - 1
	if _, err := f.service.store.db.Exec(`UPDATE chunks SET created_at = ?
		WHERE doc_id = ? AND index_generation = ?`, expired, doc.ID, retiredGeneration); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.store.db.Exec(`UPDATE document_generations SET created_at = ?
		WHERE doc_id = ? AND index_generation = ?`, expired, doc.ID, retiredGeneration); err != nil {
		t.Fatal(err)
	}

	if _, err := f.service.store.pruneRetiredGenerations(doc.ID, current.ActiveIndexGen); err != nil {
		t.Fatal(err)
	}
	var oldChunks, oldMappings int
	if err := f.service.store.db.QueryRow("SELECT COUNT(*) FROM chunks WHERE doc_id = ? AND index_generation = ?", doc.ID, retiredGeneration).Scan(&oldChunks); err != nil {
		t.Fatal(err)
	}
	if err := f.service.store.db.QueryRow("SELECT COUNT(*) FROM document_generations WHERE doc_id = ? AND index_generation = ?", doc.ID, retiredGeneration).Scan(&oldMappings); err != nil {
		t.Fatal(err)
	}
	if oldChunks != 0 || oldMappings != 0 {
		t.Fatalf("retired generation remained: chunks=%d mappings=%d", oldChunks, oldMappings)
	}

	oldSourceVersion := current.SourceVersion - 1
	oldGenerationValue := retiredGeneration
	_, err = f.service.GetDocumentContext(ctx, doc.ID, ContextOptions{
		AnchorIndex:     new(int),
		SourceVersion:   &oldSourceVersion,
		IndexGeneration: &oldGenerationValue,
	})
	if !errors.Is(err, ErrHistoricalEvidenceExpired) {
		t.Fatalf("post-GC historical context error = %v, want ErrHistoricalEvidenceExpired", err)
	}
}
