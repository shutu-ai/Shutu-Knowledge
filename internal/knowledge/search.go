package knowledge

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/shutu-ai/shutu-knowledge/internal/chunk"
	"github.com/shutu-ai/shutu-knowledge/internal/evidence"
	"github.com/shutu-ai/shutu-knowledge/internal/rerank"
	"github.com/shutu-ai/shutu-knowledge/internal/retrieval"
)

var (
	// ErrSearchTimeout reports that the synchronous retrieval exceeded its
	// end-to-end deadline. It must not be converted into zero hits.
	ErrSearchTimeout = errors.New("search deadline exceeded")
	// ErrModelSchedulerWait reports that the bounded wait for a shared model
	// slot expired before inference could start.
	ErrModelSchedulerWait = errors.New("model scheduler wait exceeded")
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
	Debug     bool          `json:"debug,omitempty"`
	Filter    *SearchFilter `json:"filter,omitempty"`
}

// RetrievalDiagnosticCandidate is one stage-local, source-attributed ranking
// record. It is emitted only when SearchRequest.Debug is true.
type RetrievalDiagnosticCandidate struct {
	ChunkID     string  `json:"chunkId"`
	DocID       string  `json:"docId"`
	BaseID      string  `json:"baseId"`
	Document    string  `json:"document"`
	SectionPath string  `json:"sectionPath,omitempty"`
	BM25Rank    int     `json:"bm25Rank,omitempty"`
	BM25Score   float64 `json:"bm25Score,omitempty"`
	VectorRank  int     `json:"vectorRank,omitempty"`
	VectorScore float64 `json:"vectorScore,omitempty"`
	RRFScore    float64 `json:"rrfScore,omitempty"`
	RerankScore float64 `json:"rerankScore,omitempty"`
	MMRScore    float64 `json:"mmrScore,omitempty"`
	FinalRank   int     `json:"finalRank,omitempty"`
}

// RetrievalDiagnostics exposes reproducible stage evidence for audits and
// regression tests without changing the default user-facing response.
type RetrievalDiagnostics struct {
	Query           string                         `json:"query"`
	NormalizedQuery string                         `json:"normalizedQuery"`
	BaseIDs         []string                       `json:"baseIds,omitempty"`
	DocIDs          []string                       `json:"docIds,omitempty"`
	BM25            []RetrievalDiagnosticCandidate `json:"bm25"`
	Vector          []RetrievalDiagnosticCandidate `json:"vector"`
	RRF             []RetrievalDiagnosticCandidate `json:"rrf"`
	Rerank          []RetrievalDiagnosticCandidate `json:"rerank,omitempty"`
	MMRInput        []RetrievalDiagnosticCandidate `json:"mmrInput,omitempty"`
	MMROutput       []RetrievalDiagnosticCandidate `json:"mmrOutput,omitempty"`
	FinalThreshold  float64                        `json:"finalThreshold"`
	Final           []RetrievalDiagnosticCandidate `json:"final"`
}

// SearchHit is one ranked result with lane scores and its context window.
type SearchHit struct {
	ChunkID         string           `json:"chunkId"`
	DocID           string           `json:"docId"`
	BaseID          string           `json:"baseId"`
	DocumentTitle   string           `json:"documentTitle"`
	Heading         string           `json:"heading,omitempty"`
	Index           int              `json:"index"`
	IndexGeneration int64            `json:"indexGeneration"`
	SourceVersion   int64            `json:"sourceVersion"`
	Text            string           `json:"text"`
	Score           float64          `json:"score"`
	VectorScore     float64          `json:"vectorScore,omitempty"`
	LexicalScore    float64          `json:"lexicalScore,omitempty"`
	FusionScore     float64          `json:"fusionScore,omitempty"`
	RerankScore     float64          `json:"rerankScore,omitempty"`
	ContextWindow   *evidence.Window `json:"contextWindow,omitempty"`
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
	Query       string                `json:"query"`
	Mode        string                `json:"mode"`
	Generations []SearchGeneration    `json:"generations,omitempty"`
	Total       int                   `json:"total"`
	Reranked    bool                  `json:"reranked"`
	Rerank      *RerankStatus         `json:"rerank,omitempty"`
	Diagnostics *RetrievalDiagnostics `json:"diagnostics,omitempty"`
	ElapsedMS   int64                 `json:"elapsedMs"`
	Hits        []SearchHit           `json:"hits"`
}

// SearchGeneration is one hit's source/generation identity. It makes evidence
// audits and mixed-generation regressions observable to callers.
type SearchGeneration struct {
	DocID           string `json:"docId"`
	BaseID          string `json:"baseId"`
	SourceVersion   int64  `json:"sourceVersion"`
	IndexGeneration int64  `json:"indexGeneration"`
}

const defaultHitTokens = 768

// defaultVectorRelevanceFloor is a conservative abstention floor for vector
// candidates when callers did not configure an explicit threshold. Lexical
// matches are not subjected to this model-dependent floor; vector-only
// evidence must clear it or it is not allowed to fill Final Context.
const defaultVectorRelevanceFloor = 0.35

// ContextOptions is an anchor continuation request: read around a chunk
// without re-searching (the model's "keep reading" path).
type ContextOptions struct {
	AnchorChunkID string `json:"anchorChunkId,omitempty"`
	AnchorIndex   *int   `json:"anchorIndex,omitempty"`
	// SourceVersion and IndexGeneration pin a separately issued continuation
	// to the exact historical search result. They must be supplied together.
	SourceVersion   *int64 `json:"sourceVersion,omitempty"`
	IndexGeneration *int64 `json:"indexGeneration,omitempty"`
	Before          *int   `json:"before,omitempty"`
	After           *int   `json:"after,omitempty"`
	MaxTokens       int    `json:"maxTokens,omitempty"`
	Focus           string `json:"focus,omitempty"`
	CrossHeading    bool   `json:"crossHeading,omitempty"`
}

// GetDocumentContext composes an ordered window around an anchor chunk of
// one document. The anchor (by id or index) is authoritative; neighbors come
// from contiguous storage ranges.
func (s *Service) GetDocumentContext(ctx context.Context, docID string, opts ContextOptions) (*evidence.Window, error) {
	if (opts.IndexGeneration == nil) != (opts.SourceVersion == nil) {
		return nil, fmt.Errorf("indexGeneration and sourceVersion must be supplied together")
	}
	doc, _, err := s.GetDocument(docID, false)
	if err != nil {
		if opts.IndexGeneration != nil && errors.Is(err, ErrNotFound) {
			// The mapping identifies the citation, but a tombstone forbids
			// serving it through the historical interface.
			if _, mapErr := s.store.generationByID(ctx, s.store.db.ReadDB(), docID,
				*opts.IndexGeneration); mapErr == nil {
				return nil, ErrHistoricalEvidenceExpired
			}
		}
		return nil, err
	}
	generation := doc.ActiveIndexGen
	sourceVersion := doc.SourceVersion
	total := doc.ChunkCount
	historical := opts.IndexGeneration != nil
	if historical {
		identity, err := s.store.generationByID(ctx, s.store.db.ReadDB(), docID, *opts.IndexGeneration)
		if err != nil {
			return nil, err
		}
		if identity.SourceVersion != *opts.SourceVersion {
			return nil, ErrHistoricalEvidenceExpired
		}
		generation = identity.Generation
		sourceVersion = identity.SourceVersion
		total = identity.ChunkCount
	}
	var anchor Chunk
	switch {
	case opts.AnchorChunkID != "":
		var chunks []Chunk
		var err error
		if historical {
			chunks, err = s.store.listChunksByGenerationRange(ctx, s.store.db.ReadDB(), docID, generation, 0, int64(total+1))
		} else {
			chunks, err = s.store.listChunksByIndexRange(ctx, s.store.db.ReadDB(), docID, 0, total+1)
		}
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
		var chunks []Chunk
		var err error
		if historical {
			index := int64(*opts.AnchorIndex)
			chunks, err = s.store.listChunksByGenerationRange(ctx, s.store.db.ReadDB(), docID, generation, index, index)
		} else {
			chunks, err = s.store.listChunksByIndexRange(ctx, s.store.db.ReadDB(), docID, *opts.AnchorIndex, *opts.AnchorIndex)
		}
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
	var neighbors []Chunk
	if historical {
		neighbors, err = s.store.listChunksByGenerationRange(ctx, s.store.db.ReadDB(), docID, generation, int64(from), int64(to))
	} else {
		neighbors, err = s.store.listChunksByIndexRange(ctx, s.store.db.ReadDB(), docID, from, to)
	}
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
	window.IndexGeneration = generation
	window.SourceVersion = sourceVersion
	return &window, nil
}

func toEvidence(chunks []Chunk) []evidence.Chunk {
	out := make([]evidence.Chunk, 0, len(chunks))
	for _, c := range chunks {
		out = append(out, c)
	}
	return out
}

// acquireSearchModelSlot bounds interactive embedding/rerank queueing. It
// shares one finite slot pool across providers, but never holds it while
// lexical/database work runs.
func (s *Service) acquireSearchModelSlot(ctx context.Context) (func(), error) {
	if s.sharedModelAdmission != nil {
		return s.sharedModelAdmission.Acquire(ctx)
	}
	wait := time.Duration(s.global.Scheduler.ModelWaitMS) * time.Millisecond
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case s.searchModelSlots <- struct{}{}:
		released := false
		return func() {
			if !released {
				released = true
				<-s.searchModelSlots
			}
		}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return nil, ErrModelSchedulerWait
	}
}

// Search executes the retrieval pipeline. Fail-closed scope rules: a disabled
// service, an explicitly empty base/doc scope, or an empty query return zero
// hits instead of expanding.
// Search records explicit scheduler failures before returning. The timeout and
// quota errors are contractual responses, never silent empty results.
func (s *Service) Search(ctx context.Context, req SearchRequest) (SearchResult, error) {
	result, err := s.search(ctx, req)
	if err != nil {
		s.recordMetric(func(m *MetricsSnapshot) {
			if errors.Is(err, ErrModelSchedulerWait) {
				m.ModelSchedulerWaits++
			}
			if errors.Is(err, ErrSearchTimeout) || errors.Is(err, context.DeadlineExceeded) {
				m.SearchTimeouts++
			}
		})
	}
	return result, err
}

func (s *Service) search(ctx context.Context, req SearchRequest) (SearchResult, error) {
	ctx, cancel := context.WithTimeout(
		ctx, time.Duration(s.global.Retrieval.SearchTimeoutMS)*time.Millisecond,
	)
	defer cancel()
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
	searchBaseIDs, providersByBase, embeddingActive, err := s.searchProviders(baseIDs)
	if err != nil {
		return SearchResult{}, err
	}

	// One WAL read transaction pins lexical recall, vector recall, context,
	// titles, and diagnostics to the same SQLite snapshot. Reindex or cleanup
	// committed during this bounded request cannot mix generations.
	snapshot, err := s.store.db.ReadDB().BeginTx(ctx, nil)
	if err != nil {
		return SearchResult{}, fmt.Errorf("begin search snapshot: %w", err)
	}
	defer func() { _ = snapshot.Rollback() }()
	var snapshotProbe int
	if err := snapshot.QueryRowContext(ctx, `SELECT 1`).Scan(&snapshotProbe); err != nil {
		return SearchResult{}, fmt.Errorf("pin search snapshot: %w", err)
	}

	topK := req.TopK
	if topK <= 0 {
		topK = s.global.Retrieval.TopK
	}
	if topK > 50 {
		topK = 50
	}
	mode := s.resolveMode(req.Mode, embeddingActive)
	poolSize := topK * 3
	if poolSize < 12 {
		poolSize = 12
	}

	variants := queryVariants(query, req.Queries)
	var variantOrders [][]string
	fusionScores := map[string]float64{}
	laneVectors := map[string]float64{}
	laneLexicals := map[string]float64{}
	embeddings := map[string][]float32{}
	chunkIDs := map[string]Chunk{}
	bm25Ranks := map[string]int{}
	vectorRanks := map[string]int{}
	bm25Order := map[string]bool{}
	vectorOrderSeen := map[string]bool{}
	var queryVector []float32
	vectorAvailable := false
	queryVectorsByModel := map[string][]float64{}
	seenGenerations := map[string]bool{}

	for _, variant := range variants {
		lexicalHits, err := s.store.LexicalSearch(ctx, snapshot, variant, baseIDs, docIDs, poolSize)
		if err != nil {
			return SearchResult{}, err
		}
		lexicalOrder := make([]string, 0, len(lexicalHits))
		for rank, hit := range lexicalHits {
			if previous, ok := bm25Ranks[hit.ID]; !ok || rank+1 < previous {
				bm25Ranks[hit.ID] = rank + 1
			}
			bm25Order[hit.ID] = true
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
			vectorQuerySucceeded := false
			for _, baseID := range searchBaseIDs {
				providers := providersByBase[baseID]
				if !providers.embeddingActive || providers.embedder == nil {
					continue
				}
				modelKey := providers.embedder.ModelKey()
				cacheKey := modelKey + "\x00" + variant
				queryVectorForModel, ok := queryVectorsByModel[cacheKey]
				if !ok {
					releaseModelSlot, acquireErr := s.acquireSearchModelSlot(ctx)
					if acquireErr != nil {
						return SearchResult{}, acquireErr
					}
					vectors, embedErr := providers.embedder.Embed(ctx, []string{variant})
					releaseModelSlot()
					if ctx.Err() != nil {
						return SearchResult{}, ErrSearchTimeout
					}
					if embedErr != nil || len(vectors) != 1 {
						continue
					}
					queryVectorForModel = vectors[0]
					queryVectorsByModel[cacheKey] = queryVectorForModel
				}
				if variant == query && queryVector == nil {
					queryVector = toFloat32(queryVectorForModel)
				}
				vectorHits, err := s.store.VectorSearch(ctx, snapshot, queryVectorForModel, []string{baseID}, docIDs, poolSize, modelKey)
				if err != nil {
					return SearchResult{}, err
				}
				vectorQuerySucceeded = true
				vectorAvailable = vectorAvailable || len(vectorHits) > 0
				for rank, hit := range vectorHits {
					if previous, ok := vectorRanks[hit.ID]; !ok || rank+1 < previous {
						vectorRanks[hit.ID] = rank + 1
					}
					vectorOrderSeen[hit.ID] = true
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
			}
			if !vectorQuerySucceeded {
				// Query embedding failure degrades this variant to lexical search.
				mode = "lexical"
				variantOrders = append(variantOrders, lexicalOrder)
				continue
			}
			if mode == "vector" {
				// A model switch can leave the active generation in another,
				// correctly isolated vector space. Do not label old-model
				// lexical evidence as a vector result; return it as an
				// explicit lexical degradation when it is available.
				if len(vectorOrder) == 0 && len(lexicalOrder) > 0 {
					mode = "lexical"
					variantOrders = append(variantOrders, lexicalOrder)
					continue
				}
				variantOrders = append(variantOrders, vectorOrder)
				continue
			}
		}
		if mode == "hybrid" {
			weights := []float64{s.global.Retrieval.RRFVectorWeight, 1}
			fused := retrieval.ReciprocalRankFusion([][]string{vectorOrder, lexicalOrder}, weights)
			fusionScores = fused
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
		fusionScores = fused
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

	result := SearchResult{Query: query, Mode: mode}
	var rerankStage []Chunk

	// Rerank stage (optional): strict validation upstream; any failure keeps
	// the current order and reports degraded.
	var rerankScores map[string]float64
	reranker := commonReranker(ordered, providersByBase)
	if reranker != nil && len(ordered) > 1 {
		releaseModelSlot, acquireErr := s.acquireSearchModelSlot(ctx)
		if acquireErr != nil {
			return SearchResult{}, acquireErr
		}
		rerankStart := Now()
		texts := make([]string, 0, len(ordered))
		for _, c := range ordered {
			texts = append(texts, clipToTokens(c.EmbeddingText, 352))
		}
		modelKey := reranker.ModelKey()
		providerName := "remote"
		if strings.HasPrefix(modelKey, "local-rerank:") {
			providerName = "local"
		}
		status := RerankStatus{Provider: providerName, Model: modelKey, Attempted: true, CandidateCount: len(ordered)}
		scores, err := reranker.Rerank(ctx, clipToTokens(query, 128), texts)
		releaseModelSlot()
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
		rerankStage = append([]Chunk(nil), ordered...)
	}
	if rerankScores != nil {
		result.Reranked = true
	}
	if rerankStage == nil {
		rerankStage = append([]Chunk(nil), ordered...)
	}

	// MMR is a post-relevance optional pass. Its relevance term must be the
	// actual reranker/vector/fusion score, never a uniform placeholder; its
	// floor also prevents diversity from admitting weak candidates. Reranking
	// first is important because adjacent chunks often have nearly identical
	// embeddings but very different answer relevance.
	relevance := make(map[string]float64, len(ordered))
	maxFusion := 0.0
	for _, score := range fusionScores {
		if score > maxFusion {
			maxFusion = score
		}
	}
	for _, c := range ordered {
		score := laneLexicals[c.ID]
		switch {
		case rerankScores != nil:
			score = rerankScores[c.ID]
		case mode == "vector":
			score = laneVectors[c.ID]
		case mode == "hybrid" && maxFusion > 0:
			score = fusionScores[c.ID] / maxFusion
		}
		relevance[c.ID] = score
	}
	// Threshold applies to the score that represents relevance for the active
	// mode. A zero threshold preserves historical behavior; MMR raises it to a
	// relative-to-best floor when explicitly enabled.
	threshold := req.Threshold
	if threshold <= 0 && mode == "vector" {
		threshold = s.global.Retrieval.SimilarityMin
		if threshold <= 0 {
			threshold = defaultVectorRelevanceFloor
		}
	}
	mmrEnabled := req.MMR && s.global.Retrieval.MMR && len(queryVector) > 0
	var mmrInput, mmrOutput []Chunk
	mmrScores := map[string]float64{}
	if mmrEnabled {
		lambda := s.global.Retrieval.MMRDiversity
		if lambda <= 0 {
			lambda = 0.75
		}
		best := 0.0
		for _, score := range relevance {
			if score > best {
				best = score
			}
		}
		floor := best * 0.60
		if threshold > floor {
			floor = threshold
		}
		threshold = floor
		eligible := make([]retrieval.RankedHit, 0, len(ordered))
		ineligible := make([]Chunk, 0)
		for _, c := range ordered {
			if relevance[c.ID] < floor {
				ineligible = append(ineligible, c)
				continue
			}
			eligible = append(eligible, retrieval.RankedHit{ID: c.ID, Score: relevance[c.ID], Embedding: embeddings[c.ID]})
		}
		mmrInput = append([]Chunk(nil), ordered...)
		reordered := retrieval.MaximalMarginalRelevance(eligible, queryVector, lambda, len(eligible))
		byID := make(map[string]Chunk, len(ordered))
		for _, c := range ordered {
			byID[c.ID] = c
		}
		ordered = ordered[:0]
		for _, hit := range reordered {
			mmrScores[hit.ID] = hit.MMRScore
			ordered = append(ordered, byID[hit.ID])
		}
		ordered = append(ordered, ineligible...)
		mmrOutput = append([]Chunk(nil), ordered...)
	}

	final := make([]Chunk, 0, topK)
	for _, c := range ordered {
		if len(final) == topK {
			break
		}
		if threshold > 0 && relevance[c.ID] < threshold {
			continue
		}
		if mode == "hybrid" && laneLexicals[c.ID] <= 0 && laneVectors[c.ID] < defaultVectorRelevanceFloor {
			continue
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
		fetched, err := s.store.ListChunksByIndexRanges(ctx, snapshot, ranges)
		if err != nil {
			return SearchResult{}, err
		}
		for _, c := range fetched {
			neighbors[c.DocID] = append(neighbors[c.DocID], c)
		}
	}

	titles, err := s.documentTitles(ctx, snapshot, final)
	if err != nil {
		return SearchResult{}, err
	}
	for _, c := range final {
		hit := SearchHit{
			ChunkID:         c.ID,
			DocID:           c.DocID,
			BaseID:          c.BaseID,
			DocumentTitle:   titles[c.DocID],
			Heading:         c.Heading,
			Index:           c.Index,
			IndexGeneration: c.IndexGeneration,
			SourceVersion:   c.SourceVersion,
			Text:            c.Text,
			Score:           laneLexicals[c.ID],
			VectorScore:     laneVectors[c.ID],
			LexicalScore:    laneLexicals[c.ID],
		}
		if rerankScores != nil {
			hit.Score = rerankScores[c.ID]
			hit.RerankScore = rerankScores[c.ID]
		} else if mode == "vector" {
			hit.Score = laneVectors[c.ID]
		} else if mode == "hybrid" {
			hit.Score = fusionScores[c.ID]
			hit.FusionScore = fusionScores[c.ID]
		}
		window := evidence.Compose(neighbors[c.DocID], c, evidence.Options{
			Before:    &siblings,
			After:     &siblings,
			MaxTokens: defaultHitTokens,
			Focus:     query,
		})
		hit.ContextWindow = &window
		result.Hits = append(result.Hits, hit)
		key := c.DocID
		if !seenGenerations[key] {
			seenGenerations[key] = true
			result.Generations = append(result.Generations, SearchGeneration{
				DocID: c.DocID, BaseID: c.BaseID,
				SourceVersion: c.SourceVersion, IndexGeneration: c.IndexGeneration,
			})
		}
	}
	result.Total = len(result.Hits)
	if req.Debug {
		diagnostics, err := s.makeRetrievalDiagnostics(
			ctx, snapshot, query, baseIDs, docIDs, chunkIDs, bm25Order, vectorOrderSeen,
			bm25Ranks, vectorRanks, globalOrder, rerankStage, mmrInput, mmrOutput,
			fusionScores, laneLexicals, laneVectors, rerankScores,
			mmrScores, threshold, final,
		)
		if err != nil {
			return SearchResult{}, err
		}
		result.Diagnostics = diagnostics
	}
	result.ElapsedMS = Now().UnixMilli() - startedAt.UnixMilli()
	// The snapshot may intentionally contain an object that was logically
	// deleted while retrieval was paused for model work. Lifecycle visibility
	// is a final cross-snapshot fence: it cannot resurrect pre-snapshot data,
	// but it prevents a delete committed during the request from being
	// returned to the caller.
	if len(result.Hits) > 0 {
		if err := s.excludeInvisibleResults(ctx, &result); err != nil {
			return SearchResult{}, err
		}
	}
	result.ElapsedMS = Now().UnixMilli() - startedAt.UnixMilli()
	s.recordMetric(func(m *MetricsSnapshot) {
		m.Searches++
		m.SearchDurationMS += result.ElapsedMS
		m.CandidateCount += int64(len(ordered))
		m.ContextCount += int64(len(result.Hits))
		if result.Rerank != nil {
			m.RerankDurationMS += result.Rerank.ElapsedMS
			if result.Rerank.Status == "degraded" {
				m.ModelErrors++
			}
		}
	})
	if result.Hits == nil {
		result.Hits = []SearchHit{}
	}
	return result, nil
}

func (s *Service) excludeInvisibleResults(ctx context.Context, result *SearchResult) error {
	ids := make([]string, 0, len(result.Hits))
	for _, hit := range result.Hits {
		ids = append(ids, hit.DocID)
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := s.store.db.ReadDB().QueryContext(ctx, `SELECT d.id FROM documents d
		JOIN bases b ON b.id = d.base_id
		WHERE d.id IN (`+placeholders+`) AND d.lifecycle_state = ? AND b.lifecycle_state = ?`,
		append(args, LifecycleActive, LifecycleActive)...)
	if err != nil {
		return err
	}
	defer rows.Close()
	visible := make(map[string]bool, len(ids))
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		visible[id] = true
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	_ = rows.Close()

	hits := result.Hits[:0]
	for _, hit := range result.Hits {
		if visible[hit.DocID] {
			hits = append(hits, hit)
		}
	}
	result.Hits = hits
	result.Generations = result.Generations[:0]
	for _, hit := range result.Hits {
		if !visible[hit.DocID] {
			continue
		}
		result.Generations = append(result.Generations, SearchGeneration{
			DocID: hit.DocID, BaseID: hit.BaseID,
			SourceVersion: hit.SourceVersion, IndexGeneration: hit.IndexGeneration,
		})
	}
	if result.Diagnostics != nil {
		result.Diagnostics.BM25 = filterDiagnosticCandidates(result.Diagnostics.BM25, visible)
		result.Diagnostics.Vector = filterDiagnosticCandidates(result.Diagnostics.Vector, visible)
		result.Diagnostics.RRF = filterDiagnosticCandidates(result.Diagnostics.RRF, visible)
		result.Diagnostics.Rerank = filterDiagnosticCandidates(result.Diagnostics.Rerank, visible)
		result.Diagnostics.MMRInput = filterDiagnosticCandidates(result.Diagnostics.MMRInput, visible)
		result.Diagnostics.MMROutput = filterDiagnosticCandidates(result.Diagnostics.MMROutput, visible)
		result.Diagnostics.Final = filterDiagnosticCandidates(result.Diagnostics.Final, visible)
	}
	result.Total = len(result.Hits)
	return nil
}

func filterDiagnosticCandidates(values []RetrievalDiagnosticCandidate, visible map[string]bool) []RetrievalDiagnosticCandidate {
	out := values[:0]
	for _, candidate := range values {
		if visible[candidate.DocID] {
			out = append(out, candidate)
		}
	}
	return out
}

func (s *Service) embedderActive() bool {
	return s.global.Embedding.Provider != "none"
}

func commonReranker(ordered []Chunk, providersByBase map[string]providerSet) rerank.Provider {
	var selected rerank.Provider
	for _, candidate := range ordered {
		providers := providersByBase[candidate.BaseID]
		if !providers.rerankerActive || providers.reranker == nil {
			return nil
		}
		if selected == nil {
			selected = providers.reranker
			continue
		}
		if selected.ModelKey() != providers.reranker.ModelKey() {
			// A single reranker cannot safely score candidates from bases that
			// explicitly selected different model spaces.
			return nil
		}
	}
	return selected
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
	state, err := s.EnabledScopeState()
	if err != nil {
		return nil, err
	}
	if !state.Enabled {
		return []string{}, nil
	}
	var requested []string
	if req.BaseID != "" {
		requested = []string{req.BaseID}
	} else if req.BaseIDs != nil {
		requested = req.BaseIDs
	}
	switch {
	case state.Explicit && len(state.BaseIDs) == 0:
		return []string{}, nil
	case len(state.BaseIDs) > 0 && requested == nil:
		return state.BaseIDs, nil
	case len(state.BaseIDs) > 0:
		allowed := map[string]bool{}
		for _, id := range state.BaseIDs {
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
	rows, err := s.store.db.Query(`SELECT id FROM documents WHERE lifecycle_state = 'active'`+clauses, args...)
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

func (s *Service) documentTitles(ctx context.Context, q queryRunner, chunks []Chunk) (map[string]string, error) {
	out := map[string]string{}
	for _, c := range chunks {
		if _, ok := out[c.DocID]; ok {
			continue
		}
		var title string
		if err := q.QueryRowContext(ctx, `SELECT title FROM documents WHERE id = ? AND lifecycle_state = ?`,
			c.DocID, LifecycleActive).Scan(&title); err != nil {
			if err == sql.ErrNoRows {
				continue
			}
			return nil, err
		}
		out[c.DocID] = title
	}
	return out, nil
}

func (s *Service) makeRetrievalDiagnostics(
	ctx context.Context, q queryRunner, query string, baseIDs, docIDs []string, chunks map[string]Chunk,
	bm25Set, vectorSet map[string]bool, bm25Ranks, vectorRanks map[string]int,
	rrfOrder []string, rerankOrder, mmrInput, mmrOutput []Chunk,
	rrfScores, bm25Scores, vectorScores, rerankScores map[string]float64,
	mmrScores map[string]float64, threshold float64, final []Chunk,
) (*RetrievalDiagnostics, error) {
	all := make([]Chunk, 0, len(chunks))
	for _, c := range chunks {
		all = append(all, c)
	}
	titles, err := s.documentTitles(ctx, q, all)
	if err != nil {
		return nil, err
	}
	fromIDs := func(ids []string) []Chunk {
		out := make([]Chunk, 0, len(ids))
		seen := map[string]bool{}
		for _, id := range ids {
			if seen[id] {
				continue
			}
			if c, ok := chunks[id]; ok {
				out = append(out, c)
				seen[id] = true
			}
		}
		return out
	}
	setChunks := func(set map[string]bool, ranks map[string]int, scores map[string]float64) []Chunk {
		ids := make([]string, 0, len(set))
		for id := range set {
			ids = append(ids, id)
		}
		sort.SliceStable(ids, func(i, j int) bool {
			if ranks[ids[i]] != ranks[ids[j]] {
				return ranks[ids[i]] < ranks[ids[j]]
			}
			if scores[ids[i]] != scores[ids[j]] {
				return scores[ids[i]] > scores[ids[j]]
			}
			return ids[i] < ids[j]
		})
		return fromIDs(ids)
	}
	finalRanks := map[string]int{}
	for i, c := range final {
		finalRanks[c.ID] = i + 1
	}
	toDiagnostics := func(list []Chunk) []RetrievalDiagnosticCandidate {
		out := make([]RetrievalDiagnosticCandidate, 0, len(list))
		for _, c := range list {
			out = append(out, RetrievalDiagnosticCandidate{
				ChunkID: c.ID, DocID: c.DocID, BaseID: c.BaseID,
				Document: titles[c.DocID], SectionPath: c.Heading,
				BM25Rank: bm25Ranks[c.ID], BM25Score: bm25Scores[c.ID],
				VectorRank: vectorRanks[c.ID], VectorScore: vectorScores[c.ID],
				RRFScore: rrfScores[c.ID], RerankScore: rerankScores[c.ID],
				MMRScore: mmrScores[c.ID], FinalRank: finalRanks[c.ID],
			})
		}
		return out
	}
	diagnostics := &RetrievalDiagnostics{
		Query: query, NormalizedQuery: strings.TrimSpace(query),
		BaseIDs: baseIDs, DocIDs: docIDs,
		BM25:     toDiagnostics(setChunks(bm25Set, bm25Ranks, bm25Scores)),
		Vector:   toDiagnostics(setChunks(vectorSet, vectorRanks, vectorScores)),
		RRF:      toDiagnostics(fromIDs(rrfOrder)),
		Rerank:   toDiagnostics(rerankOrder),
		MMRInput: toDiagnostics(mmrInput), MMROutput: toDiagnostics(mmrOutput),
		FinalThreshold: threshold, Final: toDiagnostics(final),
	}
	return diagnostics, nil
}

// queryVariants normalizes the primary plus extra phrasings (max 3 extras,
// deduplicated).
func queryVariants(query string, extras []string) []string {
	out := []string{clipToTokens(strings.TrimSpace(query), 128)}
	seen := map[string]bool{out[0]: true}
	count := 0
	for _, extra := range extras {
		extra = strings.TrimSpace(extra)
		if extra == "" || seen[extra] {
			continue
		}
		extra = clipToTokens(extra, 128)
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
	cpt := float64(len([]rune(text))) / float64(chunk.EstimateTokens(text))
	target := int(float64(tokens) * cpt)
	runes := []rune(text)
	if target >= len(runes) {
		return text
	}
	return string(runes[:target])
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
