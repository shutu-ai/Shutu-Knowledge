package knowledge

import (
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"math"
	"sort"

	"github.com/shutu-ai/shutu-knowledge/internal/storage"
)

// VectorSearch brute-force scans scoped embeddings for one model space and
// ranks by cosine similarity (normalized vectors, so dot == cosine). Vectors
// with a mismatched dimension are skipped, never mixed.
func (s *store) VectorSearch(ctx context.Context, q queryRunner, queryVector []float64, baseIDs, docIDs []string, limit int, modelKey string) ([]LaneHit, error) {
	if len(queryVector) == 0 {
		return nil, nil
	}
	scope, scopeArgs, err := scopeSQL(baseIDs, docIDs)
	if err != nil {
		return nil, err
	}
	querySQL := laneSelect + `
		FROM chunks c JOIN documents d ON d.id = c.doc_id
		JOIN bases b ON b.id = c.base_id
		WHERE c.embedding IS NOT NULL AND d.lifecycle_state = 'active' AND b.lifecycle_state = 'active'
		AND c.index_generation = d.active_index_generation`
	queryArgs := []any{}
	if modelKey != "" {
		querySQL += ` AND c.embedding_model = ?`
		queryArgs = append(queryArgs, modelKey)
	}
	querySQL += scope
	queryArgs = append(queryArgs, scopeArgs...)
	rows, err := q.QueryContext(ctx, querySQL, queryArgs...)
	if err != nil {
		return nil, fmt.Errorf("vector lane: %w", err)
	}
	defer rows.Close()
	var hits []LaneHit
	for rows.Next() {
		var hit LaneHit
		var embedding []byte
		if err := rows.Scan(&hit.ID, &hit.DocID, &hit.BaseID, &hit.Index, &hit.Text, &hit.Heading,
			&hit.Context, &hit.EmbeddingHash, &hit.CreatedAt, &embedding,
			&hit.IndexGeneration, &hit.SourceVersion); err != nil {
			return nil, err
		}
		hit.Embedding = decodeEmbedding(embedding)
		if len(hit.Embedding) != len(queryVector) {
			continue
		}
		hit.HasEmbedding = true
		queryFloat32 := make([]float32, len(queryVector))
		for i, v := range queryVector {
			queryFloat32[i] = float32(v)
		}
		hit.Score = dotProduct(queryFloat32, hit.Embedding)
		hit.EmbeddingText = searchTextOf(hit.Chunk)
		hits = append(hits, hit)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

func dotProduct(a, b []float32) float64 {
	var sum float64
	for i := range a {
		sum += float64(a[i]) * float64(b[i])
	}
	if sum < 0 {
		return 0
	}
	if sum > 1 {
		return 1
	}
	return sum
}

// encodeEmbedding serializes a vector as little-endian float32 (portable).
func encodeEmbedding(vector []float64) []byte {
	out := make([]byte, 4*len(vector))
	for i, value := range vector {
		binary.LittleEndian.PutUint32(out[i*4:], math.Float32bits(float32(value)))
	}
	return out
}

func decodeEmbedding(blob []byte) []float32 {
	if len(blob)%4 != 0 {
		return nil
	}
	out := make([]float32, len(blob)/4)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(blob[i*4:]))
	}
	return out
}

// PutChunkVectors persists one embedded batch (crash-recovery write path:
// every landed batch stays). modelKey tags the vector space.
func (s *store) PutChunkVectors(docID string, generation int64, modelKey string, byHash map[string][]float64) error {
	return s.PutChunkVectorsContext(context.Background(), docID, generation, modelKey, byHash)
}

// PutChunkVectorsContext persists one embedded batch under the caller's
// cancellation boundary. Each batch is already bounded by the embedding
// worker, and the unique Storage Writer remains the only write path.
func (s *store) PutChunkVectorsContext(ctx context.Context, docID string, generation int64, modelKey string, byHash map[string][]float64) error {
	if len(byHash) == 0 {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return s.db.WriteTx(ctx, storage.NormalWrite, nil, func(tx *sql.Tx) error {
		for hash, vector := range byHash {
			if _, err := tx.ExecContext(ctx,
				`UPDATE chunks SET embedding = ?, embedding_model = ? WHERE doc_id = ? AND index_generation = ? AND embedding_text_hash = ?`,
				encodeEmbedding(vector), modelKey, docID, generation, hash,
			); err != nil {
				return err
			}
		}
		return nil
	})
}

// ListEmbeddingVectorsByHashes implements library-wide vector reuse: stored
// vectors for the given embedding-text hashes under one model.
func (s *store) ListEmbeddingVectorsByHashes(hashes []string, modelKey string) map[string][]float64 {
	out, _ := s.ListEmbeddingVectorsByHashesContext(context.Background(), hashes, modelKey)
	return out
}

// ListEmbeddingVectorsByHashesContext keeps each reuse query bounded and
// returns cancellation/database errors instead of silently continuing with a
// potentially expensive remote embedding pass.
func (s *store) ListEmbeddingVectorsByHashesContext(ctx context.Context, hashes []string, modelKey string) (map[string][]float64, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	out := map[string][]float64{}
	for i := 0; i < len(hashes); i += 500 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		end := minInt(i+500, len(hashes))
		batch := hashes[i:end]
		query := `SELECT embedding_text_hash, embedding FROM chunks
			WHERE embedding_model = ? AND embedding IS NOT NULL
			AND embedding_text_hash IN (` + placeholders(len(batch)) + `)`
		args := []any{modelKey}
		for _, hash := range batch {
			args = append(args, hash)
		}
		rows, err := s.db.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var hash string
			var blob []byte
			if err := rows.Scan(&hash, &blob); err == nil {
				vector := decodeEmbedding(blob)
				converted := make([]float64, len(vector))
				for j, v := range vector {
					converted[j] = float64(v)
				}
				out[hash] = converted
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return out, nil
}

// VectorModelCounts reports stored chunks per embedding-model tag (drift).
func (s *store) VectorModelCounts(baseID string) (map[string]int, error) {
	return s.VectorModelCountsContext(context.Background(), baseID)
}

// VectorModelCountsContext reports stored chunks per embedding-model tag while
// honoring the caller's cancellation boundary.
func (s *store) VectorModelCountsContext(ctx context.Context, baseID string) (map[string]int, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	clause := ""
	var args []any
	if baseID != "" {
		clause = " AND base_id = ?"
		args = append(args, baseID)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT COALESCE(embedding_model, ''), COUNT(*) FROM chunks WHERE embedding IS NOT NULL`+clause+` GROUP BY embedding_model`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var model string
		var count int
		if err := rows.Scan(&model, &count); err != nil {
			return nil, err
		}
		out[model] = count
	}
	return out, rows.Err()
}

// VectorDimensions reports the widest stored vector in scope.
func (s *store) VectorDimensions(baseID string) (int, error) {
	return s.VectorDimensionsContext(context.Background(), baseID)
}

// VectorDimensionsContext reports the widest stored vector while honoring ctx.
func (s *store) VectorDimensionsContext(ctx context.Context, baseID string) (int, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	clause := ""
	var args []any
	if baseID != "" {
		clause = " WHERE base_id = ?"
		args = append(args, baseID)
	}
	var dimensions sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT MAX(LENGTH(embedding) / 4) FROM chunks`+clause, args...).Scan(&dimensions); err != nil {
		return 0, err
	}
	return int(dimensions.Int64), nil
}
