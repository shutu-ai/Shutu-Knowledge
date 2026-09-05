// Package retrieval holds the ranking primitives: query tokenization
// (Latin words + CJK bigrams), corpus BM25, weighted Reciprocal Rank Fusion,
// Maximal Marginal Relevance, and the mode-based orchestration. Pure
// functions only: deterministic, store-free, fully unit-testable.
package retrieval

import (
	"math"
	"regexp"
	"sort"
	"strings"
)

// RRFK is the Reciprocal Rank Fusion constant (audited reference value).
const RRFK = 60.0

// BM25 parameters (audited reference values).
const (
	BM25K1 = 1.5
	BM25B  = 0.75
)

var latinWord = regexp.MustCompile(`[a-z0-9_]+`)
var cjkRun = regexp.MustCompile(`[\x{3040}-\x{30ff}\x{3400}-\x{4dbf}\x{4e00}-\x{9fff}\x{ac00}-\x{d7af}]+`)

// Tokenize splits text into lowercase Latin words (length > 1) plus CJK
// character bigrams, with single CJK chars as a fallback so short queries
// still match. Deterministic.
func Tokenize(text string) []string {
	lowered := strings.ToLower(text)
	var tokens []string
	for _, word := range latinWord.FindAllString(lowered, -1) {
		if len(word) > 1 {
			tokens = append(tokens, word)
		}
	}
	for _, run := range cjkRun.FindAllString(lowered, -1) {
		runes := []rune(run)
		if len(runes) == 1 {
			tokens = append(tokens, run)
			continue
		}
		for i := 0; i+1 < len(runes); i++ {
			tokens = append(tokens, string(runes[i:i+2]))
		}
	}
	return tokens
}

// CosineSimilarity between two (assumed L2-normalized) vectors, clamped [0,1].
func CosineSimilarity(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
	}
	if dot < 0 {
		return 0
	}
	if dot > 1 {
		dot = 1
	}
	return dot
}

// Bm25Scorer scores ids against query tokens over a fixed corpus.
type Bm25Scorer struct {
	docTokens map[string][]string
	df        map[string]int
	avgdl     float64
	n         int
}

// CorpusDoc is one scored document.
type CorpusDoc struct {
	ID   string
	Text string
}

// BuildBm25 builds a corpus scorer.
func BuildBm25(docs []CorpusDoc) *Bm25Scorer {
	s := &Bm25Scorer{docTokens: make(map[string][]string, len(docs)), df: map[string]int{}}
	total := 0
	for _, doc := range docs {
		tokens := Tokenize(doc.Text)
		s.docTokens[doc.ID] = tokens
		total += len(tokens)
		seen := map[string]bool{}
		for _, token := range tokens {
			if !seen[token] {
				seen[token] = true
				s.df[token]++
			}
		}
	}
	s.n = len(docs)
	if s.n > 0 {
		s.avgdl = float64(total) / float64(s.n)
	}
	return s
}

// Score computes the raw BM25 score of one document for the query tokens.
func (s *Bm25Scorer) Score(id string, queryTokens []string) float64 {
	tokens, ok := s.docTokens[id]
	if !ok || len(tokens) == 0 {
		return 0
	}
	tf := map[string]int{}
	for _, token := range tokens {
		tf[token]++
	}
	var sum float64
	for _, token := range queryTokens {
		documentFrequency, ok := s.df[token]
		if !ok {
			continue
		}
		idf := math.Log((float64(s.n-documentFrequency)+0.5)/(float64(documentFrequency)+0.5) + 1)
		termFrequency := tf[token]
		if termFrequency == 0 {
			continue
		}
		norm := float64(termFrequency) / (float64(termFrequency) + BM25K1*(1-BM25B+BM25B*float64(len(tokens))/s.avgdl))
		sum += idf * norm
	}
	return sum
}

// NormalizeBm25 maps an unbounded raw score into [0, 1).
func NormalizeBm25(raw float64) float64 {
	return raw / (raw + 1)
}

// ReciprocalRankFusion fuses ranked id lists with per-list weights
// (default 1); returns id -> fused score. Ties keep the input recall order
// downstream because the caller sorts stably.
func ReciprocalRankFusion(rankedLists [][]string, weights []float64) map[string]float64 {
	fused := map[string]float64{}
	for i, list := range rankedLists {
		weight := 1.0
		if i < len(weights) {
			weight = weights[i]
		}
		for index, id := range list {
			fused[id] += weight / (RRFK + float64(index) + 1)
		}
	}
	return fused
}

// RankedHit is one ranked candidate.
type RankedHit struct {
	ID           string
	Score        float64
	VectorScore  float64
	LexicalScore float64
	HasVector    bool
	HasLexical   bool
	Embedding    []float32
}

// MaximalMarginalRelevance reorders hits for diversity using
// lambda * relevance - (1 - lambda) * max-similarity-to-selected.
// Hits without embeddings keep their positions after the selected set.
func MaximalMarginalRelevance(hits []RankedHit, queryVector []float32, lambda float64, topK int) []RankedHit {
	var withEmbedding []RankedHit
	var without []RankedHit
	for _, hit := range hits {
		if len(hit.Embedding) == len(queryVector) && len(queryVector) > 0 {
			withEmbedding = append(withEmbedding, hit)
		} else {
			without = append(without, hit)
		}
	}
	if len(withEmbedding) < 2 {
		return hits
	}
	var selected []RankedHit
	remaining := withEmbedding
	target := topK
	if target > len(withEmbedding) {
		target = len(withEmbedding)
	}
	for len(selected) < target && len(remaining) > 0 {
		bestIndex := 0
		bestScore := math.Inf(-1)
		for i, candidate := range remaining {
			maxSim := 0.0
			for _, picked := range selected {
				sim := CosineSimilarity(candidate.Embedding, picked.Embedding)
				if sim > maxSim {
					maxSim = sim
				}
			}
			score := lambda*candidate.Score - (1-lambda)*maxSim
			if score > bestScore {
				bestScore = score
				bestIndex = i
			}
		}
		selected = append(selected, remaining[bestIndex])
		remaining = append(remaining[:bestIndex], remaining[bestIndex+1:]...)
	}
	out := append([]RankedHit{}, selected...)
	out = append(out, remaining...)
	out = append(out, without...)
	return out
}

// RankOptions controls one ranking pass.
type RankOptions struct {
	Mode        string // auto | hybrid | vector | lexical
	TopK        int
	Threshold   float64
	MMR         bool
	MMRLambda   float64
	QueryVector []float32
}

// Candidates is the scored candidate pool fed into Rank.
type Candidates struct {
	Docs      []CorpusDoc
	Embedding map[string][]float32
}

// Rank scores candidates with the selected strategy, applies the threshold
// and top-k. vector mode degrades to lexical when no vectors exist (fail to
// a usable lane, never to nothing).
func Rank(query string, candidates Candidates, opts RankOptions) []RankedHit {
	queryTokens := Tokenize(query)
	vectorAvailable := len(opts.QueryVector) > 0
	for _, doc := range candidates.Docs {
		if vec, ok := candidates.Embedding[doc.ID]; ok && len(vec) == len(opts.QueryVector) {
			vectorAvailable = true
			break
		}
	}
	mode := opts.Mode
	switch mode {
	case "hybrid", "vector":
		if !vectorAvailable {
			mode = "lexical"
		}
	case "lexical":
	default:
		if vectorAvailable {
			mode = "hybrid"
		} else {
			mode = "lexical"
		}
	}

	scorer := BuildBm25(candidates.Docs)
	lexical := map[string]float64{}
	for _, doc := range candidates.Docs {
		lexical[doc.ID] = NormalizeBm25(scorer.Score(doc.ID, queryTokens))
	}
	vector := map[string]float64{}
	if len(opts.QueryVector) > 0 {
		for _, doc := range candidates.Docs {
			if vec, ok := candidates.Embedding[doc.ID]; ok && len(vec) == len(opts.QueryVector) {
				vector[doc.ID] = CosineSimilarity(opts.QueryVector, vec)
			}
		}
	}

	var ranked []RankedHit
	switch mode {
	case "vector":
		for id, score := range vector {
			hit := RankedHit{ID: id, Score: score, VectorScore: score, LexicalScore: lexical[id], HasVector: true, HasLexical: true}
			hit.Embedding = candidates.Embedding[id]
			ranked = append(ranked, hit)
		}
	case "lexical":
		for id, score := range lexical {
			hit := RankedHit{ID: id, Score: score, VectorScore: vector[id], LexicalScore: score, HasLexical: true}
			hit.Embedding = candidates.Embedding[id]
			ranked = append(ranked, hit)
		}
	default: // hybrid
		vectorOrder := sortedIDs(vector)
		lexicalOrder := sortedIDs(lexical)
		fused := ReciprocalRankFusion([][]string{vectorOrder, lexicalOrder}, nil)
		maxFused := 2 / (RRFK + 1)
		for _, doc := range candidates.Docs {
			score := fused[doc.ID] / maxFused
			hit := RankedHit{ID: doc.ID, Score: score, VectorScore: vector[doc.ID], LexicalScore: lexical[doc.ID], HasVector: true, HasLexical: true}
			hit.Embedding = candidates.Embedding[doc.ID]
			ranked = append(ranked, hit)
		}
	}
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].Score > ranked[j].Score })
	if opts.MMR && opts.MMRLambda > 0 && len(opts.QueryVector) > 0 {
		pool := opts.TopK * 3
		if pool < 12 {
			pool = 12
		}
		ranked = MaximalMarginalRelevance(ranked, opts.QueryVector, opts.MMRLambda, pool)
	}
	out := ranked[:0]
	for _, hit := range ranked {
		if hit.Score >= opts.Threshold {
			out = append(out, hit)
		}
		if len(out) == opts.TopK {
			break
		}
	}
	return out
}

func sortedIDs(scores map[string]float64) []string {
	ids := make([]string, 0, len(scores))
	for id, score := range scores {
		if score > 0 {
			ids = append(ids, id)
		}
	}
	sort.SliceStable(ids, func(i, j int) bool { return scores[ids[i]] > scores[ids[j]] })
	return ids
}
