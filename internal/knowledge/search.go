package knowledge

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/shutu-ai/shutu-knowledge/internal/chunk"
	"github.com/shutu-ai/shutu-knowledge/internal/evidence"
	"github.com/shutu-ai/shutu-knowledge/internal/rerank"
	"github.com/shutu-ai/shutu-knowledge/internal/retrieval"
)

// SearchFilter narrows a search to a subset of documents (ANDed). A nil
// slice is unrestricted; an explicitly empty slice matches nothing.
type SearchFilter struct {
	DocIDs        []string `json:"docIds,omitempty"`
	TitleIncludes string   `json:"titleIncludes,omitempty"`
	SourceTypes   []string `json:"sourceTypes,omitempty"`
	UpdatedAfter  int64    `json:"updatedAfter,omitempty"`
	UpdatedBefore int64    `json:"updatedBefore,omitempty"`
}

// SearchRequest is one search.
type SearchRequest struct {
	Query     string        `json:"query"`
	Queries   []string      `json:"queries,omitempty"`
	BaseID    string        `json:"baseId,omitempty"`
	BaseIDs   []string      `json:"baseIds,omitempty"`
	TopK      int           `json:"topK,omitempty"`
	Mode      string        `json:"mode,omitempty"`
	Threshold float64       `json:"threshold,omitempty"`
	MMR       bool          `json:"mmr,omitempty"`
	Filter    *SearchFilter `json:"filter,omitempty"`
}

// SearchHit is one ranked result with lane scores and its context window.
type SearchHit struct {
	ChunkID       string           `json:"chunkId"`
	DocID         string           `json:"docId"`
	BaseID        string           `json:"baseId"`
	DocumentTitle string           `json:"documentTitle"`
	Heading       string           `json:"heading,omitempty"`
	Index         int              `json:"index"`
	Text          string           `json:"text"`
	Score         float64          `json:"score"`
	VectorScore   float64          `json:"vectorScore,omitempty"`
	LexicalScore  float64          `json:"lexicalScore,omitempty"`
	RerankScore   float64          `json:"rerankScore,omitempty"`
	ContextWindow *evidence.Window `json:"contextWindow,omitempty"`
}

// RerankStatus explains what the reranker did for this search.
type RerankStatus struct {
	Provider       string        `json:"provider"`
	Model          string        `json:"model"`
	Status         string        `json:"status"` // applied | not_needed | degraded
	Attempted      bool          `json:"attempted"`
	Applied        bool          `json:"applied"`
	CandidateCount int           `json:"candidateCount"`
	ElapsedMS      int64         `json:"elapsedMs,omitempty"`
	Error          *rerank.Error `json:"error,omitempty"`
}

// SearchResult carries the ranked hits plus explainability metadata.
type SearchResult struct {
	Query     string        `json:"query"`
	Mode      string        `json:"mode"`
	Total     int           `json:"total"`
	Reranked  bool          `json:"reranked"`
	Rerank    *RerankStatus `json:"rerank,omitempty"`
	ElapsedMS int64         `json:"elapsedMs"`
	Hits      []SearchHit   `json:"hits"`
}

const defaultHitTokens = 768

// ContextOptions is an anchor continuation request: read around a chunk
// without re-searching (the model's "keep reading" path).
type ContextOptions struct {
	AnchorChunkID string `json:"anchorChunkId,omitempty"`
	AnchorIndex   *int   `json:"anchorIndex,omitempty"`
	Before        *int   `json:"before,omitempty"`
	After         *int   `json:"after,omitempty"`
	MaxTokens     int    `json:"maxTokens,omitempty"`
	Focus         string `json:"focus,omitempty"`
	CrossHeading  bool   `json:"crossHeading,omitempty"`
}

// GetDocumentContext composes an ordered window around an anchor chunk of
// one document. The anchor (by id or index) is authoritative; neighbors come
// from contiguous storage ranges.
func (s *Service) GetDocumentContext(docID string, opts ContextOptions) (*evidence.Window, error) {
	doc, _, err := s.GetDocument(docID, false)
	if err != nil {
		return nil, err
	}
	total := doc.ChunkCount
	var anchor Chunk
	switch {
	case opts.AnchorChunkID != "":
		chunks, err := s.store.listChunksByIndexRange(docID, 0, total+1)
		if err != nil {
			return nil, err
		}
		for _, c := range chunks {
			if c.ID == opts.AnchorChunkID {
				anchor = c
				break
			}
		}
		if anchor.ID == "" {
			return nil, fmt.Errorf("%w: anchor chunk %s", ErrNotFound, opts.AnchorChunkID)
		}
	case opts.AnchorIndex != nil:
		chunks, err := s.store.listChunksByIndexRange(docID, *opts.AnchorIndex, *opts.AnchorIndex)
		if err != nil {
			return nil, err
		}
		if len(chunks) == 0 {
			return nil, fmt.Errorf("%w: chunk index %d", ErrNotFound, *opts.AnchorIndex)
		}
		anchor = chunks[0]
	default:
		return nil, fmt.Errorf("anchorChunkId or anchorIndex is required")
	}
	radius := 64
	from := anchor.Index - radius
	if from < 0 {
		from = 0
	}
	to := anchor.Index + radius
	neighbors, err := s.store.listChunksByIndexRange(docID, from, to)
	if err != nil {
		return nil, err
	}
	window := evidence.Compose(toEvidence(neighbors), anchor, evidence.Options{
		Before:             opts.Before,
		After:              opts.After,
		MaxTokens:          opts.MaxTokens,
		Focus:              opts.Focus,
		CrossHeading:       opts.CrossHeading,
		DocumentChunkCount: total,
	})
	return &window, nil
}

func toEvidence(chunks []Chunk) []evidence.Chunk {
	out := make([]evidence.Chunk, 0, len(chunks))
	for _, c := range chunks {
		out = append(out, c)
	}
	return out
}

// Search executes the retrieval pipeline. Fail-closed scope rules: a disabled
// service, an explicitly empty base/doc scope, or an empty query return zero
// hits instead of expanding.
func (s *Service) Search(ctx context.Context, req SearchRequest) (SearchResult, error) {
	startedAt := Now()
	query := strings.TrimSpace(req.Query)
	if query == "" {
		return SearchResult{Query: req.Query, Mode: s.resolveMode(req.Mode, false), Hits: []SearchHit{}}, nil
	}
	baseIDs, err := s.resolveSearchScope(req)
	if err != nil {
		return SearchResult{}, err
	}
	if baseIDs != nil && len(baseIDs) == 0 {
		return SearchResult{Query: query, Mode: s.resolveMode(req.Mode, s.embedderActive()), Hits: []SearchHit{}}, nil
	}
	docIDs, err := s.resolveDocFilter(req.Filter)
	if err != nil {
		return SearchResult{}, err
	}
	if docIDs != nil && len(docIDs) == 0 {
		return SearchResult{Query: query, Mode: s.resolveMode(req.Mode, s.embedderActive()), Hits: []SearchHit{}}, nil
	}

	topK := req.TopK
	if topK <= 0 {
		topK = s.global.Retrieval.TopK
	}
	mode := s.resolveMode(req.Mode, s.embedderActive())
	poolSize := topK * 3
	if poolSize < 12 {
		poolSize = 12
	}

	variants := queryVariants(query, req.Queries)
	var variantOrders [][]string
	laneVectors := map[string]float64{}
	laneLexicals := map[string]float64{}
	embeddings := map[string][]float32{}
	chunkIDs := map[string]Chunk{}
	vectorAvailable := false

	for _, variant := range variants {
		lexicalHits, err := s.store.LexicalSearch(variant, baseIDs, docIDs, poolSize)
		if err != nil {
			return SearchResult{}, err
		}
		lexicalOrder := make([]string, 0, len(lexicalHits))
		for _, hit := range lexicalHits {
			if laneLexicals[hit.ID] < hit.Score {
				laneLexicals[hit.ID] = hit.Score
			}
			if _, seen := chunkIDs[hit.ID]; !seen {
				chunkIDs[hit.ID] = hit.Chunk
				if hit.HasEmbedding {
					embeddings[hit.ID] = hit.Embedding
				}
			}
			lexicalOrder = append(lexicalOrder, hit.ID)
		}
		var vectorOrder []string
		if mode == "hybrid" || mode == "vector" {
			queryVectors, err := s.embedder.Embed(ctx, []string{variant})
			if err != nil {
				// Query embedding failure degrades this variant to lexical.
				mode = "lexical"
			} else if len(queryVectors) == 1 {
				vectorHits, err := s.store.VectorSearch(queryVectors[0], baseIDs, docIDs, poolSize)
				if err != nil {
					return SearchResult{}, err
				}
				vectorAvailable = vectorAvailable || len(vectorHits) > 0
				for _, hit := range vectorHits {
					if laneVectors[hit.ID] < hit.Score {
						laneVectors[hit.ID] = hit.Score
					}
					if _, seen := chunkIDs[hit.ID]; !seen {
						chunkIDs[hit.ID] = hit.Chunk
					}
					if hit.HasEmbedding {
						embeddings[hit.ID] = hit.Embedding
					}
					vectorOrder = append(vectorOrder, hit.ID)
				}
				if mode == "vector" {
					variantOrders = append(variantOrders, vectorOrder)
					continue
				}
			}
		}
		if mode == "hybrid" {
			weights := []float64{s.global.Retrieval.RRFVectorWeight, 1}
			fused := retrieval.ReciprocalRankFusion([][]string{vectorOrder, lexicalOrder}, weights)
			ordered := make([]string, 0, len(fused))
			for id := range fused {
				ordered = append(ordered, id)
			}
			sort.SliceStable(ordered, func(i, j int) bool { return fused[ordered[i]] > fused[ordered[j]] })
			variantOrders = append(variantOrders, ordered)
		} else {
			variantOrders = append(variantOrders, lexicalOrder)
		}
	}

	// Cross-variant rank fusion; a single variant yields its own order.
	var globalOrder []string
	if len(variantOrders) == 1 {
		globalOrder = variantOrders[0]
	} else {
		fused := retrieval.ReciprocalRankFusion(variantOrders, nil)
		for id := range fused {
			globalOrder = append(globalOrder, id)
		}
		sort.SliceStable(globalOrder, func(i, j int) bool { return fused[globalOrder[i]] > fused[globalOrder[j]] })
	}

	// Materialize candidates in global order.
	ordered := make([]Chunk, 0, len(globalOrder))
	for _, id := range globalOrder {
		ordered = append(ordered, chunkIDs[id])
	}

	// MMR diversity pass over the pooled candidates.
	if req.MMR && s.global.Retrieval.MMR {
		lambda := s.global.Retrieval.MMRDiversity
		hits := make([]retrieval.RankedHit, 0, len(ordered))
		for _, c := range ordered {
			hits = append(hits, retrieval.RankedHit{ID: c.ID, Score: 1, Embedding: embeddings[c.ID]})
		}
		// Relevance is the current order; approximate with uniform relevance
		// and let similarity drive the reordering within the pool.
		var queryVector []float32
		if vecs, err := s.embedder.Embed(ctx, []string{query}); err == nil && len(vecs) == 1 {
			queryVector = toFloat32(vecs[0])
		}
		reordered := retrieval.MaximalMarginalRelevance(hits, queryVector, lambda, len(ordered))
		byID := map[string]Chunk{}
		for _, c := range ordered {
			byID[c.ID] = c
		}
		ordered = ordered[:0]
		for _, hit := range reordered {
			if c, ok := byID[hit.ID]; ok {
				ordered = append(ordered, c)
			}
		}
	}

	result := SearchResult{Query: query, Mode: mode}

	// Rerank stage (optional): strict validation upstream; any failure keeps
	// the current order and reports degraded.
	var rerankScores map[string]float64
	if s.reranker != nil && len(ordered) > 1 {
		rerankStart := Now()
		texts := make([]string, 0, len(ordered))
		for _, c := range ordered {
			texts = append(texts, clipToTokens(c.EmbeddingText, 352))
		}
		status := RerankStatus{Provider: "remote", Model: s.reranker.ModelKey(), Attempted: true, CandidateCount: len(ordered)}
		scores, err := s.reranker.Rerank(ctx, clipToTokens(query, 128), texts)
		status.ElapsedMS = Now().UnixMilli() - rerankStart.UnixMilli()
		if err != nil {
			status.Status = "degraded"
			if rerr, ok := err.(*rerank.Error); ok {
				status.Error = rerr
			} else {
				status.Error = &rerank.Error{Code: rerank.CodeProviderError, Message: err.Error()}
			}
			result.Rerank = &status
		} else {
			status.Status = "applied"
			status.Applied = true
			result.Rerank = &status
			rerankScores = map[string]float64{}
			for i, c := range ordered {
				rerankScores[c.ID] = scores[i]
			}
		}
	}

	// Final ordering: rerank scores when applied, otherwise fused order.
	if rerankScores != nil {
		sort.SliceStable(ordered, func(i, j int) bool {
			return rerankScores[ordered[i].ID] > rerankScores[ordered[j].ID]
		})
	}
	if rerankScores != nil {
		result.Reranked = true
	}

	// Threshold applies only to comparable relevance scores (rerank or pure
	// vector), never to rank-fusion order scores.
	final := make([]Chunk, 0, topK)
	for _, c := range ordered {
		if len(final) == topK {
			break
		}
		switch {
		case rerankScores != nil:
			if rerankScores[c.ID] < req.Threshold {
				continue
			}
		case mode == "vector":
			if laneVectors[c.ID] < req.Threshold {
				continue
			}
		}
		final = append(final, c)
	}

	// Context windows: batched neighbor fetch, then per-hit composition.
	siblings := s.global.Retrieval.SiblingChunks
	var ranges []IndexRange
	for _, c := range final {
		ranges = append(ranges, IndexRange{DocID: c.DocID, FromIdx: c.Index - siblings, ToIdx: c.Index + siblings})
	}
	neighbors := map[string][]evidence.Chunk{}
	if len(ranges) > 0 {
		fetched, err := s.store.ListChunksByIndexRanges(ranges)
		if err != nil {
			return SearchResult{}, err
		}
		for _, c := range fetched {
			neighbors[c.DocID] = append(neighbors[c.DocID], c)
		}
	}

	titles := s.documentTitles(final)
	for _, c := range final {
		hit := SearchHit{
			ChunkID:       c.ID,
			DocID:         c.DocID,
			BaseID:        c.BaseID,
			DocumentTitle: titles[c.DocID],
			Heading:       c.Heading,
			Index:         c.Index,
			Text:          c.Text,
			Score:         laneLexicals[c.ID],
			VectorScore:   laneVectors[c.ID],
			LexicalScore:  laneLexicals[c.ID],
		}
		if rerankScores != nil {
			hit.Score = rerankScores[c.ID]
			hit.RerankScore = rerankScores[c.ID]
		} else if mode == "vector" {
			hit.Score = laneVectors[c.ID]
		} else if mode == "hybrid" {
			// Report the fused relevance as the score summary.
			hit.Score = laneLexicals[c.ID]
		}
		window := evidence.Compose(neighbors[c.DocID], c, evidence.Options{
			Before:    &siblings,
			After:     &siblings,
			MaxTokens: defaultHitTokens,
			Focus:     query,
		})
		hit.ContextWindow = &window
		result.Hits = append(result.Hits, hit)
	}
	result.Total = len(result.Hits)
	result.ElapsedMS = Now().UnixMilli() - startedAt.UnixMilli()
	return result, nil
}

func (s *Service) embedderActive() bool {
	return s.global.Embedding.Provider != "none"
}

func (s *Service) resolveMode(requested string, vectorAvailable bool) string {
	switch requested {
	case "hybrid", "vector", "lexical":
		if requested == "vector" && !vectorAvailable {
			return "lexical"
		}
		return requested
	case "":
		if vectorAvailable {
			return "hybrid"
		}
		return "lexical"
	default:
		return s.resolveMode("", vectorAvailable)
	}
}

// resolveSearchScope intersects the request with the pinned enabled scope.
// Returns nil for "all bases", or a possibly-empty explicit set.
func (s *Service) resolveSearchScope(req SearchRequest) ([]string, error) {
	enabled, pinned, err := s.EnabledScope()
	if err != nil {
		return nil, err
	}
	if !enabled {
		return []string{}, nil
	}
	var requested []string
	if req.BaseID != "" {
		requested = []string{req.BaseID}
	} else if req.BaseIDs != nil {
		requested = req.BaseIDs
	}
	switch {
	case len(pinned) > 0 && requested == nil:
		return pinned, nil
	case len(pinned) > 0:
		allowed := map[string]bool{}
		for _, id := range pinned {
			allowed[id] = true
		}
		var intersected []string
		for _, id := range requested {
			if allowed[id] {
				intersected = append(intersected, id)
			}
		}
		return intersected, nil
	default:
		return requested, nil
	}
}

// resolveDocFilter converts metadata filters into document ids (nil = all).
func (s *Service) resolveDocFilter(filter *SearchFilter) ([]string, error) {
	if filter == nil {
		return nil, nil
	}
	var explicitSet map[string]bool
	if filter.DocIDs != nil {
		explicitSet = mapKey(filter.DocIDs)
		if len(explicitSet) == 0 {
			return []string{}, nil
		}
	}
	if filter.TitleIncludes == "" && len(filter.SourceTypes) == 0 && filter.UpdatedAfter == 0 && filter.UpdatedBefore == 0 {
		if explicitSet == nil {
			return nil, nil
		}
		return setValues(explicitSet), nil
	}
	clauses := " WHERE 1 = 1"
	var args []any
	if filter.TitleIncludes != "" {
		clauses += ` AND title LIKE ? ESCAPE '\'`
		args = append(args, "%"+escapeLike(filter.TitleIncludes)+"%")
	}
	if len(filter.SourceTypes) > 0 {
		clauses += fmt.Sprintf(" AND source_type IN (%s)", placeholders(len(filter.SourceTypes)))
		for _, st := range filter.SourceTypes {
			args = append(args, st)
		}
	}
	if filter.UpdatedAfter > 0 {
		clauses += " AND COALESCE(updated_at, created_at) >= ?"
		args = append(args, filter.UpdatedAfter)
	}
	if filter.UpdatedBefore > 0 {
		clauses += " AND COALESCE(updated_at, created_at) <= ?"
		args = append(args, filter.UpdatedBefore)
	}
	rows, err := s.store.db.Query(`SELECT id FROM documents`+clauses, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var matched []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		matched = append(matched, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if explicitSet == nil {
		return matched, nil
	}
	var out []string
	for _, id := range matched {
		if explicitSet[id] {
			out = append(out, id)
		}
	}
	return out, nil
}

func (s *Service) documentTitles(chunks []Chunk) map[string]string {
	out := map[string]string{}
	for _, c := range chunks {
		if _, ok := out[c.DocID]; ok {
			continue
		}
		doc, err := s.store.getDocument(c.DocID)
		if err == nil {
			out[c.DocID] = doc.Title
		}
	}
	return out
}

// queryVariants normalizes the primary plus extra phrasings (max 3 extras,
// deduplicated).
func queryVariants(query string, extras []string) []string {
	out := []string{strings.TrimSpace(query)}
	seen := map[string]bool{out[0]: true}
	count := 0
	for _, extra := range extras {
		extra = strings.TrimSpace(extra)
		if extra == "" || seen[extra] {
			continue
		}
		if count >= 3 {
			break
		}
		seen[extra] = true
		out = append(out, extra)
		count++
	}
	return out
}

// clipToTokens clips text to roughly the token budget on the tail.
func clipToTokens(text string, tokens int) string {
	if chunk.EstimateTokens(text) <= tokens {
		return text
	}
	cpt := float64(len(text)) / float64(chunk.EstimateTokens(text))
	target := int(float64(tokens) * cpt)
	if target >= len(text) {
		return text
	}
	return text[:target]
}

func mapKey(ids []string) map[string]bool {
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out
}

func setValues(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	return out
}

func toFloat32(in []float64) []float32 {
	out := make([]float32, len(in))
	for i, v := range in {
		out[i] = float32(v)
	}
	return out
}
