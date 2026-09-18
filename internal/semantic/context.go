package semantic

import (
	"fmt"
	"sort"
	"strings"

	"github.com/shutu-ai/shutu-knowledge/internal/chunk"
)

const (
	DefaultContextTokenBudget = 4096
	contextCompilerVersion    = "semantic-evidence-v1"
)

// ContextCompileOptions controls one dynamic semantic+evidence package.
type ContextCompileOptions struct {
	Query       string
	Intent      string
	TokenBudget int
	TopK        int
	FactTopK    int
}

// ContextEvidence is the caller-owned exact evidence candidate. It is normally
// projected from the existing 0.3 SearchHit and never fabricated from summary
// text.
type ContextEvidence struct {
	ChunkID         string
	DocumentID      string
	DocumentTitle   string
	Heading         string
	Text            string
	Citation        string
	IndexGeneration int64
	SourceVersion   int64
	Score           float64
}

// ContextPackage is the query-specific compiled context supplied to an LLM.
type ContextPackage struct {
	Query            string             `json:"query"`
	Intent           string             `json:"intent,omitempty"`
	CompilerVersion  string             `json:"compilerVersion"`
	Generation       int64              `json:"generation"`
	KnowledgeSummary []string           `json:"knowledgeSummary,omitempty"`
	Concepts         []string           `json:"concepts,omitempty"`
	Relations        []string           `json:"relations,omitempty"`
	Facts            []string           `json:"facts,omitempty"`
	Evidence         []ContextEvidence  `json:"evidence,omitempty"`
	Citations        []string           `json:"citations,omitempty"`
	TokenBudget      int                `json:"tokenBudget"`
	EstimatedTokens  int                `json:"estimatedTokens"`
	RenderedContext  string             `json:"renderedContext"`
	Diagnostics      ContextDiagnostics `json:"diagnostics"`
}

// ContextDiagnostics makes selection and budget behavior auditable without
// exposing unrelated knowledge-base content.
type ContextDiagnostics struct {
	SemanticHits         int `json:"semanticHits"`
	FactHits             int `json:"factHits"`
	EvidenceCandidates   int `json:"evidenceCandidates"`
	EvidenceSelected     int `json:"evidenceSelected"`
	EvidenceDeduplicated int `json:"evidenceDeduplicated"`
	EvidenceDropped      int `json:"evidenceDropped"`
}

// CompileContext combines semantic orientation with exact evidence under one
// token budget. It does not replace an external citation and never promotes a
// generated summary to primary evidence.
func CompileContext(compilation Compilation, evidence []ContextEvidence, options ContextCompileOptions) (ContextPackage, error) {
	query := strings.TrimSpace(options.Query)
	if query == "" {
		return ContextPackage{}, fmt.Errorf("context compilation requires a query")
	}
	if strings.TrimSpace(compilation.BaseID) == "" || compilation.Generation <= 0 {
		return ContextPackage{}, fmt.Errorf("context compilation requires a semantic generation")
	}
	topK := clampContextCount(options.TopK, 8)
	factTopK := clampContextCount(options.FactTopK, 4)
	budget := options.TokenBudget
	if budget <= 0 {
		budget = DefaultContextTokenBudget
	}
	if budget < 512 {
		budget = 512
	}
	if budget > 32768 {
		budget = 32768
	}

	orientation, err := SearchCompilation(compilation, SearchOptions{Query: query, TopK: topK})
	if err != nil {
		return ContextPackage{}, err
	}
	facts, err := SearchCompilation(compilation, SearchOptions{Query: query, TopK: factTopK, Kinds: []UnitKind{UnitFact}})
	if err != nil {
		return ContextPackage{}, err
	}

	pkg := ContextPackage{
		Query: query, Intent: strings.TrimSpace(options.Intent),
		CompilerVersion: contextCompilerVersion, Generation: compilation.Generation,
		TokenBudget: budget,
	}
	seenSummary := map[string]bool{}
	for _, hit := range orientation.Hits {
		if hit.Unit.Type != UnitSummary && hit.Unit.Type != UnitTopic {
			continue
		}
		line := hit.Unit.Title + ": " + clipContextText(hit.Unit.Content, 480)
		if seenSummary[line] {
			continue
		}
		seenSummary[line] = true
		pkg.KnowledgeSummary = append(pkg.KnowledgeSummary, line)
	}
	seenConcept := map[string]bool{}
	for _, hit := range orientation.Hits {
		if hit.Unit.Type != UnitConcept {
			continue
		}
		line := fmt.Sprintf("%s (confidence %.2f, %d evidence pointers)", hit.Unit.Title,
			hit.Unit.Confidence, len(hit.Evidence))
		if seenConcept[line] {
			continue
		}
		seenConcept[line] = true
		pkg.Concepts = append(pkg.Concepts, line)
	}
	for _, hit := range facts.Hits {
		pkg.Facts = append(pkg.Facts, clipContextText(hit.Unit.Content, 320))
	}
	pkg.Relations = []string{}

	deduplicated := deduplicateContextEvidence(evidence)
	pkg.Diagnostics = ContextDiagnostics{
		SemanticHits: len(orientation.Hits), FactHits: len(facts.Hits),
		EvidenceCandidates: len(evidence), EvidenceSelected: 0,
		EvidenceDeduplicated: len(evidence) - len(deduplicated),
	}
	pkg.renderAndSelect(deduplicated)
	return pkg, nil
}

func (p *ContextPackage) renderAndSelect(evidence []ContextEvidence) {
	remaining := p.TokenBudget
	writeSection := func(header string, lines []string, allowance int) []string {
		if len(lines) == 0 || remaining <= chunk.EstimateTokens(header)+2 {
			return nil
		}
		out := make([]string, 0, len(lines))
		used := chunk.EstimateTokens(header) + 2
		for _, line := range lines {
			cost := chunk.EstimateTokens("- " + line)
			if used+cost > allowance || remaining-cost < 64 {
				break
			}
			out = append(out, line)
			used += cost
			remaining -= cost
		}
		if len(out) == 0 {
			return nil
		}
		remaining -= chunk.EstimateTokens(header) + 2
		return out
	}

	var builder strings.Builder
	builder.WriteString("# Knowledge Context\nQuery: ")
	builder.WriteString(p.Query)
	builder.WriteString("\n")
	remaining -= chunk.EstimateTokens("# Knowledge Context\nQuery: " + p.Query + "\n")

	orientation := writeSection("## Knowledge orientation", p.KnowledgeSummary, p.TokenBudget/4)
	concepts := writeSection("## Relevant concepts", p.Concepts, p.TokenBudget/10)
	relationLines := writeSection("## Relevant relations", p.Relations, p.TokenBudget/20)
	facts := writeSection("## Critical facts", p.Facts, p.TokenBudget/8)

	builder.WriteString("\n")
	for _, section := range []struct {
		header string
		lines  []string
	}{{"## Knowledge orientation", orientation}, {"## Relevant concepts", concepts},
		{"## Relevant relations", relationLines}, {"## Critical facts", facts}} {
		if len(section.lines) == 0 {
			continue
		}
		builder.WriteString(section.header + "\n")
		for _, line := range section.lines {
			builder.WriteString("- " + line + "\n")
		}
	}

	builder.WriteString("\n## Exact evidence\n")
	remaining -= chunk.EstimateTokens("\n## Exact evidence\n")
	citation := 1
	for _, item := range evidence {
		label := fmt.Sprintf("[%d]", citation)
		title := firstNonEmpty(item.DocumentTitle, item.DocumentID)
		heading := strings.TrimSpace(item.Heading)
		text := strings.TrimSpace(item.Text)
		header := label + " " + title
		if heading != "" {
			header += " / " + heading
		}
		cost := chunk.EstimateTokens(header+"\n") + chunk.EstimateTokens(text)
		if cost > remaining-32 {
			available := remaining - 32 - chunk.EstimateTokens(header+"\n")
			if available < 24 {
				break
			}
			text = clipToContextTokens(text, available)
			cost = chunk.EstimateTokens(header+"\n") + chunk.EstimateTokens(text)
			if cost > remaining {
				break
			}
		}
		if text == "" {
			break
		}
		builder.WriteString(header + "\n" + text + "\n")
		remaining -= cost
		p.Evidence = append(p.Evidence, item)
		citationText := firstNonEmpty(item.Citation, fmt.Sprintf("%s#%s", title, item.ChunkID))
		p.Citations = append(p.Citations, fmt.Sprintf("%s %s", label, citationText))
		citation++
	}
	if len(p.Evidence) > 0 {
		builder.WriteString("\n## Citations\n")
		for _, citation := range p.Citations {
			builder.WriteString(citation + "\n")
		}
	}
	p.RenderedContext = builder.String()
	p.EstimatedTokens = chunk.EstimateTokens(p.RenderedContext)
	p.Diagnostics.EvidenceSelected = len(p.Evidence)
	p.Diagnostics.EvidenceDropped = len(evidence) - len(p.Evidence)
	if p.EstimatedTokens > p.TokenBudget {
		// This is a defensive invariant failure. The structured package stays
		// populated so tests can diagnose the section that exceeded budget.
		p.RenderedContext = clipToContextTokens(p.RenderedContext, p.TokenBudget)
		p.EstimatedTokens = chunk.EstimateTokens(p.RenderedContext)
	}
}

func deduplicateContextEvidence(items []ContextEvidence) []ContextEvidence {
	seen := map[string]bool{}
	out := make([]ContextEvidence, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.DocumentID) == "" || strings.TrimSpace(item.Text) == "" {
			continue
		}
		key := item.DocumentID + "\x00" + firstNonEmpty(item.ChunkID, stableKey(item.Text))
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, item)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		if out[i].DocumentID != out[j].DocumentID {
			return out[i].DocumentID < out[j].DocumentID
		}
		return out[i].ChunkID < out[j].ChunkID
	})
	return out
}

func clampContextCount(value, fallback int) int {
	if value <= 0 {
		return fallback
	}
	if value > 20 {
		return 20
	}
	return value
}

func clipContextText(value string, characters int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= characters {
		return strings.TrimSpace(value)
	}
	return strings.TrimSpace(string(runes[:characters])) + "…"
}

func clipToContextTokens(value string, tokens int) string {
	if chunk.EstimateTokens(value) <= tokens {
		return value
	}
	runes := []rune(value)
	ratio := float64(tokens) / float64(chunk.EstimateTokens(value))
	target := int(float64(len(runes)) * ratio)
	if target < 0 {
		target = 0
	}
	if target > len(runes) {
		target = len(runes)
	}
	return string(runes[:target])
}
