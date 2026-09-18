package semantic

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// Default retrieval exposes the PH4 semantic-memory layer without replacing the
// existing evidence search. Facts can be requested explicitly for ablation or
// context assembly, but are not returned by the concept/topic/summary default.
var DefaultSearchKinds = []UnitKind{UnitConcept, UnitTopic, UnitSummary}

const semanticSearchScoringVersion = "lexical-v1"

// SearchOptions controls deterministic semantic-memory retrieval.
type SearchOptions struct {
	Query  string
	TopK   int
	Kinds  []UnitKind
	Intent QueryIntent
}

// SearchHit is one activated semantic unit and its exact evidence closure.
type SearchHit struct {
	Unit         Unit             `json:"unit"`
	Score        float64          `json:"score"`
	Coverage     float64          `json:"coverage"`
	MatchedTerms []string         `json:"matchedTerms,omitempty"`
	Evidence     []EvidenceSource `json:"evidence,omitempty"`
}

// SearchResponse includes reproducible routing diagnostics. It never reports a
// chunk as the semantic result: units and exact evidence are separate fields.
type SearchResponse struct {
	Query           string      `json:"query"`
	NormalizedQuery string      `json:"normalizedQuery"`
	Terms           []string    `json:"terms,omitempty"`
	RequestedKinds  []UnitKind  `json:"requestedKinds"`
	Generation      int64       `json:"generation"`
	ScoringVersion  string      `json:"scoringVersion"`
	Routing         *QueryPlan  `json:"routing,omitempty"`
	Hits            []SearchHit `json:"hits,omitempty"`
}

// SearchCompilation performs bounded lexical activation over an immutable
// semantic generation. It is deterministic, works offline, and follows every
// derived_from link to exact IR/chunk provenance.
func SearchCompilation(compilation Compilation, options SearchOptions) (SearchResponse, error) {
	if strings.TrimSpace(compilation.BaseID) == "" || compilation.Generation <= 0 {
		return SearchResponse{}, fmt.Errorf("semantic search requires a compilation scope")
	}
	routing := RouteQuery(options.Query)
	intent := options.Intent
	if intent == "" {
		intent = routing.Intent
	}
	normalized := normalizeSearchText(options.Query)
	terms := SearchTerms(options.Query)
	if len(terms) == 0 {
		return SearchResponse{}, fmt.Errorf("semantic search query is empty")
	}
	kinds := normalizedSearchKinds(options.Kinds)
	topK := options.TopK
	if topK <= 0 {
		topK = 10
	}
	if topK > 50 {
		topK = 50
	}

	type candidate struct {
		hit SearchHit
	}
	candidates := make([]candidate, 0, len(compilation.Units))
	kindSet := map[UnitKind]bool{}
	for _, kind := range kinds {
		kindSet[kind] = true
	}
	for _, unit := range compilation.Units {
		if unit.Status != UnitActive || !kindSet[unit.Type] {
			continue
		}
		score, coverage, matched := scoreSemanticUnit(unit, normalized, terms)
		if score <= 0 && intent == IntentGlobal && (unit.Type == UnitTopic || unit.Type == UnitSummary) {
			score = float64(len(unit.DerivedFrom)) * 0.1
			coverage = 1
			matched = []string{"global"}
		}
		if score <= 0 || len(matched) == 0 {
			continue
		}
		candidates = append(candidates, candidate{hit: SearchHit{
			Unit: unit, Score: score, Coverage: coverage, MatchedTerms: matched,
		}})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := candidates[i].hit, candidates[j].hit
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		if a.Coverage != b.Coverage {
			return a.Coverage > b.Coverage
		}
		if a.Unit.Type != b.Unit.Type {
			return semanticKindPriority(a.Unit.Type) < semanticKindPriority(b.Unit.Type)
		}
		return a.Unit.CanonicalKey < b.Unit.CanonicalKey
	})
	limit := topK
	if limit > len(candidates) {
		limit = len(candidates)
	}
	hits := make([]SearchHit, 0, limit)
	for _, candidate := range candidates[:limit] {
		evidence, err := ResolveEvidence(compilation.Units, candidate.hit.Unit.ID)
		if err != nil {
			return SearchResponse{}, err
		}
		hit := candidate.hit
		hit.Evidence = evidence
		hits = append(hits, hit)
	}
	return SearchResponse{
		Query: options.Query, NormalizedQuery: normalized, Terms: terms,
		RequestedKinds: kinds, Generation: compilation.Generation,
		ScoringVersion: semanticSearchScoringVersion, Routing: &routing, Hits: hits,
	}, nil
}

func normalizedSearchKinds(kinds []UnitKind) []UnitKind {
	if len(kinds) == 0 {
		kinds = DefaultSearchKinds
	}
	seen := map[UnitKind]bool{}
	out := make([]UnitKind, 0, len(kinds))
	for _, kind := range kinds {
		switch kind {
		case UnitFact, UnitConcept, UnitTopic, UnitSummary, UnitKnowledgePage:
		default:
			continue
		}
		if seen[kind] {
			continue
		}
		seen[kind] = true
		out = append(out, kind)
	}
	if len(out) == 0 {
		return append([]UnitKind(nil), DefaultSearchKinds...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func scoreSemanticUnit(unit Unit, normalizedQuery string, terms []string) (float64, float64, []string) {
	title := normalizeSearchText(unit.Title)
	canonical := normalizeSearchText(unit.CanonicalKey)
	content := normalizeSearchText(unit.Content)
	aliases := make([]string, 0, len(unit.Aliases))
	for _, alias := range unit.Aliases {
		aliases = append(aliases, normalizeSearchText(alias))
	}
	matched := make([]string, 0, len(terms))
	score := 0.0
	for _, term := range terms {
		termScore := 0.0
		switch {
		case strings.Contains(title, term):
			termScore += 4
			matched = append(matched, term)
		case strings.Contains(canonical, term):
			termScore += 3
			matched = append(matched, term)
		default:
			for _, alias := range aliases {
				if strings.Contains(alias, term) {
					termScore += 3
					matched = append(matched, term)
					break
				}
			}
		}
		if strings.Contains(content, term) {
			termScore += 1
			if termScore == 0 {
				matched = append(matched, term)
			}
		}
		score += termScore
	}
	if len(matched) == 0 {
		return 0, 0, nil
	}
	coverage := float64(len(matched)) / float64(len(terms))
	score *= coverage
	if strings.Contains(title, normalizedQuery) || strings.Contains(canonical, normalizedQuery) {
		score += 5
	} else if strings.Contains(content, normalizedQuery) {
		score += 2
	}
	if len(unit.DerivedFrom) > 0 {
		score += float64(minInt(len(unit.DerivedFrom), 8)) * 0.05
	}
	return score, coverage, matched
}

// SearchTerms returns deterministic lexical keys. Latin terms are kept whole;
// CJK runs become bigrams (or a single rune for a one-character run).
func SearchTerms(query string) []string {
	normalized := normalizeSearchText(query)
	seen := map[string]bool{}
	out := []string{}
	appendTerm := func(term string) {
		if term == "" || seen[term] {
			return
		}
		seen[term] = true
		out = append(out, term)
	}
	var latin []rune
	var cjk []rune
	flushLatin := func() {
		if len(latin) == 0 {
			return
		}
		if len(latin) >= 2 {
			appendTerm(string(latin))
		} else if len(normalized) == 1 {
			appendTerm(string(latin))
		}
		latin = nil
	}
	flushCJK := func() {
		if len(cjk) == 0 {
			return
		}
		if len(cjk) == 1 {
			appendTerm(string(cjk))
		}
		for i := 0; i+2 <= len(cjk); i++ {
			appendTerm(string(cjk[i : i+2]))
		}
		cjk = nil
	}
	for _, r := range normalized {
		switch {
		case unicode.Is(unicode.Han, r):
			flushLatin()
			cjk = append(cjk, r)
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			flushCJK()
			latin = append(latin, r)
		default:
			flushLatin()
			flushCJK()
		}
	}
	flushLatin()
	flushCJK()
	return out
}

func semanticKindPriority(kind UnitKind) int {
	switch kind {
	case UnitConcept:
		return 1
	case UnitTopic:
		return 2
	case UnitSummary:
		return 3
	case UnitFact:
		return 4
	default:
		return 5
	}
}

func normalizeSearchText(value string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(value))), " ")
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
