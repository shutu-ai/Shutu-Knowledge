package knowledge

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// LaneHit is one retrieval-lane candidate: the chunk plus its lane score.
type LaneHit struct {
	Chunk
	Score        float64
	HasEmbedding bool
	Embedding    []float32
}

// maxMatchTerms bounds the MATCH term count a long CJK question contributes.
const maxMatchTerms = 64

var unicodeWord = regexp.MustCompile(`[\p{L}\p{N}_]+`)

// ftsTerms compiles a query for the trigram index: whole words (>= 3 chars)
// and CJK trigram windows, capped; 1-2 char terms go to LIKE filters.
func ftsTerms(query string) (matchTerms, likeTerms []string) {
	seen := map[string]bool{}
	var words, trigrams []string
	for _, token := range unicodeWord.FindAllString(query, -1) {
		runes := []rune(token)
		start := 0
		for start < len(runes) {
			unsegmented := isUnsegmentedScript(runes[start])
			end := start + 1
			for end < len(runes) && isUnsegmentedScript(runes[end]) == unsegmented {
				end++
			}
			run := runes[start:end]
			if !unsegmented || len(run) <= 3 {
				words = append(words, string(run))
			} else {
				for i := 0; i+3 <= len(run); i++ {
					trigrams = append(trigrams, string(run[i:i+3]))
				}
			}
			start = end
		}
	}
	for _, word := range words {
		if len([]rune(word)) >= 3 {
			seen[word] = true
		}
	}
	for _, trigram := range trigrams {
		seen[trigram] = true
	}
	all := make([]string, 0, len(seen))
	for term := range seen {
		all = append(all, term)
	}
	sort.Strings(all)
	if len(all) > maxMatchTerms {
		all = all[:maxMatchTerms]
	}
	for _, token := range unicodeWord.FindAllString(query, -1) {
		if len([]rune(token)) < 3 {
			likeTerms = append(likeTerms, token)
		}
	}
	return all, likeTerms
}

func isUnsegmentedScript(r rune) bool {
	return unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r)
}

// scopeSQL builds scope clauses. A nil scope is unrestricted; an explicitly
// empty scope matches nothing (fail closed). Overly large scopes error
// loudly instead of silently truncating.
func scopeSQL(baseIDs, docIDs []string) (string, []any, error) {
	clauses := ""
	var args []any
	if baseIDs != nil {
		if len(baseIDs) == 0 {
			return " AND 0 = 1", nil, nil
		}
		if len(baseIDs) > 500 {
			return "", nil, fmt.Errorf("too many base ids in filter (%d > 500)", len(baseIDs))
		}
		clauses += " AND c.base_id IN (" + placeholders(len(baseIDs)) + ")"
		for _, id := range baseIDs {
			args = append(args, id)
		}
	}
	if docIDs != nil {
		if len(docIDs) == 0 {
			return " AND 0 = 1", nil, nil
		}
		if len(docIDs) > 500 {
			return "", nil, fmt.Errorf("too many document ids in filter (%d > 500)", len(docIDs))
		}
		clauses += " AND c.doc_id IN (" + placeholders(len(docIDs)) + ")"
		for _, id := range docIDs {
			args = append(args, id)
		}
	}
	return clauses, args, nil
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func escapeLike(term string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(term)
}

const laneSelect = `SELECT c.id, c.doc_id, c.base_id, c.idx, c.text, COALESCE(c.heading, ''),
	COALESCE(c.context, ''), COALESCE(c.embedding_text_hash, ''), c.created_at, c.embedding`

// LexicalSearch runs the FTS5 trigram BM25 lane. Nil scopes are unrestricted;
// explicitly empty scopes match nothing. Returns best-first with the score
// mapped into [0,1).
func (s *store) LexicalSearch(query string, baseIDs, docIDs []string, limit int) ([]LaneHit, error) {
	if strings.TrimSpace(query) == "" {
		return nil, nil
	}
	matchTerms, likeTerms := ftsTerms(query)
	if len(matchTerms) == 0 && len(likeTerms) == 0 {
		return nil, nil
	}
	scope, args, err := scopeSQL(baseIDs, docIDs)
	if err != nil {
		return nil, err
	}
	querySQL := laneSelect + `, bm25(chunk_fts) AS fts_score
		FROM chunk_fts JOIN chunks c ON c.rowid = chunk_fts.rowid
		WHERE 1 = 1` + scope
	if len(matchTerms) > 0 {
		quoted := make([]string, 0, len(matchTerms))
		for _, term := range matchTerms {
			quoted = append(quoted, `"`+strings.ReplaceAll(term, `"`, `""`)+`"`)
		}
		querySQL += " AND chunk_fts MATCH ?"
		args = append(args, strings.Join(quoted, " OR "))
	}
	for _, term := range likeTerms {
		querySQL += ` AND c.text LIKE ? ESCAPE '\'`
		args = append(args, "%"+escapeLike(term)+"%")
	}
	querySQL += " ORDER BY fts_score LIMIT ?"
	args = append(args, limit)
	rows, err := s.db.Query(querySQL, args...)
	if err != nil {
		return nil, fmt.Errorf("lexical lane: %w", err)
	}
	defer rows.Close()
	var hits []LaneHit
	for rows.Next() {
		var hit LaneHit
		var ftsScore float64
		var embedding []byte
		if err := rows.Scan(&hit.ID, &hit.DocID, &hit.BaseID, &hit.Index, &hit.Text, &hit.Heading,
			&hit.Context, &hit.EmbeddingHash, &hit.CreatedAt, &embedding, &ftsScore); err != nil {
			return nil, err
		}
		if len(embedding) > 0 {
			hit.Embedding = decodeEmbedding(embedding)
			hit.HasEmbedding = true
		}
		// FTS5 bm25() is negative-better; flip and map into [0,1).
		positive := math.Abs(ftsScore)
		hit.Score = positive / (positive + 1)
		hit.EmbeddingText = searchTextOf(hit.Chunk)
		hits = append(hits, hit)
	}
	return hits, rows.Err()
}
