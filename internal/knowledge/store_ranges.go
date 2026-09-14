package knowledge

import (
	"context"
	"database/sql"
)

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// IndexRange is one [FromIdx, ToIdx] document slice.
type IndexRange struct {
	DocID   string
	FromIdx int
	ToIdx   int
}

type generationIdentity struct {
	Generation    int64
	SourceVersion int64
	ChunkCount    int
	RawFilePath   string
	ContentHash   string
}

// generationByID resolves the durable generation/source pair. The pair is
// authoritative; callers must never receive current evidence after asking for
// an unavailable historical version.
func (s *store) generationByID(ctx context.Context, q queryRunner, docID string, generation int64) (generationIdentity, error) {
	var identity generationIdentity
	err := q.QueryRowContext(ctx, `SELECT index_generation, source_version, chunk_count,
		COALESCE(raw_file_path, ''), COALESCE(content_hash, '')
		FROM document_generations WHERE doc_id = ? AND index_generation = ?`,
		docID, generation).Scan(
		&identity.Generation, &identity.SourceVersion, &identity.ChunkCount,
		&identity.RawFilePath, &identity.ContentHash)
	if err == sql.ErrNoRows {
		return generationIdentity{}, ErrHistoricalEvidenceExpired
	}
	if err != nil {
		return generationIdentity{}, err
	}
	return identity, nil
}

func (s *store) listChunksByGenerationRange(ctx context.Context, q queryRunner, docID string, generation, fromIdx, toIdx int64) ([]Chunk, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT c.id, c.doc_id, c.base_id, c.idx, c.text, COALESCE(c.heading, ''), COALESCE(c.context, ''),
		 COALESCE(c.embedding_text_hash, ''), c.created_at, d.source_version, c.index_generation
		 FROM chunks c JOIN documents d ON d.id = c.doc_id
		 WHERE c.doc_id = ? AND d.lifecycle_state = ? AND c.index_generation = ?
		 AND c.idx BETWEEN ? AND ? ORDER BY c.idx`,
		docID, LifecycleActive, generation, fromIdx, toIdx,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Chunk
	for rows.Next() {
		var c Chunk
		var heading, contextText, hash sql.NullString
		if err := rows.Scan(&c.ID, &c.DocID, &c.BaseID, &c.Index, &c.Text, &heading, &contextText, &hash,
			&c.CreatedAt, &c.SourceVersion, &c.IndexGeneration); err != nil {
			return nil, err
		}
		c.Heading = heading.String
		c.Context = contextText.String
		c.EmbeddingHash = hash.String
		c.EmbeddingText = searchTextOf(c)
		out = append(out, c)
	}
	return out, rows.Err()
}

// ListChunksByIndexRanges fetches several document ranges as one operation
// (context-window assembly around multiple hits), deduplicated by chunk id.
func (s *store) ListChunksByIndexRanges(ctx context.Context, q queryRunner, ranges []IndexRange) ([]Chunk, error) {
	seen := map[string]bool{}
	var out []Chunk
	for _, r := range ranges {
		chunks, err := s.listChunksByIndexRange(ctx, q, r.DocID, r.FromIdx, r.ToIdx)
		if err != nil {
			return nil, err
		}
		for _, c := range chunks {
			if seen[c.ID] {
				continue
			}
			seen[c.ID] = true
			out = append(out, c)
		}
	}
	return out, nil
}

func (s *store) listChunksByIndexRange(ctx context.Context, q queryRunner, docID string, fromIdx, toIdx int) ([]Chunk, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT c.id, c.doc_id, c.base_id, c.idx, c.text, COALESCE(c.heading, ''), COALESCE(c.context, ''),
		 COALESCE(c.embedding_text_hash, ''), c.created_at
		 FROM chunks c JOIN documents d ON d.id = c.doc_id
		 WHERE c.doc_id = ? AND d.lifecycle_state = ? AND c.index_generation = d.active_index_generation
		 AND c.idx BETWEEN ? AND ? ORDER BY c.idx`,
		docID, LifecycleActive, fromIdx, toIdx,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Chunk
	for rows.Next() {
		var c Chunk
		var heading, contextText, hash sql.NullString
		if err := rows.Scan(&c.ID, &c.DocID, &c.BaseID, &c.Index, &c.Text, &heading, &contextText, &hash, &c.CreatedAt); err != nil {
			return nil, err
		}
		c.Heading = heading.String
		c.Context = contextText.String
		c.EmbeddingHash = hash.String
		c.EmbeddingText = searchTextOf(c)
		out = append(out, c)
	}
	return out, rows.Err()
}
