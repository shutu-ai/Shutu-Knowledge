package knowledge

import "database/sql"

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

// ListChunksByIndexRanges fetches several document ranges as one operation
// (context-window assembly around multiple hits), deduplicated by chunk id.
func (s *store) ListChunksByIndexRanges(ranges []IndexRange) ([]Chunk, error) {
	seen := map[string]bool{}
	var out []Chunk
	for _, r := range ranges {
		chunks, err := s.listChunksByIndexRange(r.DocID, r.FromIdx, r.ToIdx)
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

func (s *store) listChunksByIndexRange(docID string, fromIdx, toIdx int) ([]Chunk, error) {
	rows, err := s.db.Query(
		`SELECT id, doc_id, base_id, idx, text, COALESCE(heading, ''), COALESCE(context, ''),
		 COALESCE(embedding_text_hash, ''), created_at
		 FROM chunks WHERE doc_id = ? AND idx BETWEEN ? AND ? ORDER BY idx`,
		docID, fromIdx, toIdx,
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
