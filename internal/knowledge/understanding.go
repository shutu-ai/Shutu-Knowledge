package knowledge

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/shutu-ai/shutu-knowledge/internal/chunk"
	"github.com/shutu-ai/shutu-knowledge/internal/documentir"
	"github.com/shutu-ai/shutu-knowledge/internal/storage"
)

// DerivedKnowledge is optional, deterministic understanding metadata. It is
// never used as the primary evidence source.
type DerivedKnowledge struct {
	ID              string   `json:"id"`
	DocID           string   `json:"docId"`
	IndexGeneration int64    `json:"indexGeneration"`
	Kind            string   `json:"kind"`
	Content         string   `json:"content"`
	DerivedFrom     []string `json:"derivedFrom"`
	Model           string   `json:"model"`
	ModelVersion    string   `json:"modelVersion"`
	GeneratedAt     int64    `json:"generatedAt"`
}

func (s *Service) refreshDerivedKnowledge(ctx context.Context, doc *Document) error {
	if doc == nil || doc.IR == nil || doc.ActiveIndexGen == 0 {
		return nil
	}
	records := deriveKnowledge(doc.ID, doc.ActiveIndexGen, doc.IR)
	return s.store.replaceDerivedKnowledge(ctx, doc.ID, doc.ActiveIndexGen, records)
}

func deriveKnowledge(docID string, generation int64, ir *documentir.Document) []DerivedKnowledge {
	if ir == nil {
		return nil
	}
	text := strings.TrimSpace(ir.Text())
	var out []DerivedKnowledge
	if text != "" {
		out = append(out, newDerived(docID, generation, "document_summary", summarizeText(text), rootIDs(ir)))
	}
	if outline := documentOutline(ir); outline != "" {
		out = append(out, newDerived(docID, generation, "document_outline", outline, outlineIDs(ir)))
	}
	for i, node := range ir.Nodes {
		if node.Type != documentir.TypeHeading && node.Type != documentir.TypeSection {
			continue
		}
		parts := []string{strings.TrimSpace(node.Text)}
		from := []string{node.ID}
		for _, next := range ir.Nodes[i+1:] {
			if next.Type == documentir.TypeHeading || next.Type == documentir.TypeSection {
				break
			}
			if strings.TrimSpace(next.Text) != "" {
				parts = append(parts, strings.TrimSpace(next.Text))
				from = append(from, next.ID)
			}
			if len(parts) >= 3 {
				break
			}
		}
		out = append(out, newDerived(docID, generation, "section_summary", summarizeText(strings.Join(parts, " ")), from))
	}
	if concepts := keyConcepts(text); len(concepts) > 0 {
		out = append(out, newDerived(docID, generation, "key_concepts", strings.Join(concepts, ", "), rootIDs(ir)))
	}
	return out
}

func documentOutline(ir *documentir.Document) string {
	if ir == nil {
		return ""
	}
	var lines []string
	for _, node := range ir.Nodes {
		if node.Type != documentir.TypeHeading && node.Type != documentir.TypeSection {
			continue
		}
		label := strings.TrimSpace(node.Text)
		if label == "" {
			label = node.Type
		}
		level := len(node.HeadingPath)
		if level < 1 {
			level = 1
		}
		if level > 6 {
			level = 6
		}
		lines = append(lines, strings.Repeat("  ", level-1)+"- "+label)
	}
	return strings.Join(lines, "\n")
}

func outlineIDs(ir *documentir.Document) []string {
	var ids []string
	if ir == nil {
		return ids
	}
	for _, node := range ir.Nodes {
		if node.Type == documentir.TypeHeading || node.Type == documentir.TypeSection {
			ids = append(ids, node.ID)
		}
	}
	return ids
}

func newDerived(docID string, generation int64, kind, content string, from []string) DerivedKnowledge {
	seed := docID + "\x00" + strconv.FormatInt(generation, 10) + "\x00" + kind + "\x00" + content
	sum := sha256.Sum256([]byte(seed))
	return DerivedKnowledge{ID: "derived_" + hex.EncodeToString(sum[:16]), DocID: docID, IndexGeneration: generation, Kind: kind, Content: content, DerivedFrom: from, Model: "builtin", ModelVersion: "builtin-v1"}
}

func rootIDs(ir *documentir.Document) []string {
	if len(ir.Nodes) == 0 {
		return nil
	}
	return []string{ir.Nodes[0].ID}
}

func summarizeText(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if len([]rune(text)) > 280 {
		return string([]rune(text)[:280]) + "…"
	}
	return text
}

func keyConcepts(text string) []string {
	counts := map[string]int{}
	for _, word := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if len([]rune(word)) < 4 || chunk.EstimateTokens(word) <= 1 {
			continue
		}
		counts[word]++
	}
	words := make([]string, 0, len(counts))
	for word := range counts {
		words = append(words, word)
	}
	sort.Slice(words, func(i, j int) bool {
		if counts[words[i]] != counts[words[j]] {
			return counts[words[i]] > counts[words[j]]
		}
		return words[i] < words[j]
	})
	if len(words) > 8 {
		words = words[:8]
	}
	return words
}

func (s *store) replaceDerivedKnowledge(ctx context.Context, docID string, generation int64, records []DerivedKnowledge) error {
	return s.db.WriteTx(ctx, storage.NormalWrite, nil, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE derived_knowledge SET invalidated_at = ? WHERE doc_id = ? AND invalidated_at IS NULL`, now(), docID); err != nil {
			return err
		}
		for _, record := range records {
			from, _ := json.Marshal(record.DerivedFrom)
			generatedAt := now()
			if _, err := tx.ExecContext(ctx, `INSERT INTO derived_knowledge
				(id, doc_id, index_generation, kind, content, derived_from, model, model_version, provenance, created_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, record.ID, record.DocID, record.IndexGeneration, record.Kind, record.Content, string(from), record.Model, record.ModelVersion, `{}`, generatedAt); err != nil {
				return err
			}
		}
		return nil
	})
}
