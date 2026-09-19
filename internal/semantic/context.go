package semantic

import (
	"fmt"
	"sort"
	"strings"

	"github.com/shutu-ai/shutu-knowledge/internal/chunk"
)

const (
	DefaultContextTokenBudget = 4096
	contextCompilerVersion    = "semantic-temporal-v1"
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
	Version         string `json:"version,omitempty"`
	TemporalStatus  string `json:"temporalStatus,omitempty"`
	ValidFrom       int64  `json:"validFrom,omitempty"`
	ValidTo         int64  `json:"validTo,omitempty"`
	Score           float64
}

// ContextPackage is the query-specific compiled context supplied to an LLM.
type ContextPackage struct {
	Query            string             `json:"query"`
	Intent           string             `json:"intent,omitempty"`
	Routing          *QueryPlan         `json:"routing,omitempty"`
	CompilerVersion  string             `json:"compilerVersion"`
	Generation       int64              `json:"generation"`
	KnowledgeSummary []string           `json:"knowledgeSummary,omitempty"`
	Concepts         []string           `json:"concepts,omitempty"`
	Relations        []string           `json:"relations,omitempty"`
	Facts            []string           `json:"facts,omitempty"`
	TemporalIntent   string             `json:"temporalIntent,omitempty"`
	ResolvedVersion  string             `json:"resolvedVersion,omitempty"`
	TemporalReason   string             `json:"temporalReason,omitempty"`
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
	routing := RouteQuery(query)
	intent := strings.TrimSpace(options.Intent)
	if intent == "" {
		intent = string(routing.Intent)
	}
	defaultTopK, defaultFactTopK := contextCountsForPlan(routing)
	topK := clampContextCount(options.TopK, defaultTopK)
	factTopK := clampContextCount(options.FactTopK, defaultFactTopK)
	budget := options.TokenBudget
	if budget <= 0 {
		budget = DefaultContextTokenBudget
	}
	if budget < 64 {
		budget = 64
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
		Query: query, Intent: intent, Routing: &routing,
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
	factContent := func(content string) {
		clipped := clipContextText(content, 320)
		for _, existing := range pkg.Facts {
			if existing == clipped {
				return
			}
		}
		if len(pkg.Facts) < 12 {
			pkg.Facts = append(pkg.Facts, clipped)
		}
	}
	pkg.TemporalIntent = string(routing.Temporal.Intent)
	resolvedVersion := ""
	if routing.Temporal.Intent == TemporalCurrent || routing.Temporal.Intent == TemporalValidity {
		resolved, resolvable := ResolveCurrentVersion(compilation)
		resolvedVersion = resolved
		if resolvable {
			pkg.ResolvedVersion = resolved
			pkg.TemporalReason = "latest comparable compiled source version; not file mtime"
		} else {
			pkg.TemporalReason = "Unable to determine authoritative latest version"
		}
	} else if len(routing.Temporal.Versions) > 0 {
		resolvedVersion = routing.Temporal.FromVersion
		pkg.ResolvedVersion = resolvedVersion
		pkg.TemporalReason = "explicit query version has selection precedence"
	}
	for _, hit := range facts.Hits {
		factContent(temporalFactLine(hit.Unit))
	}
	// Multi-hop orientation follows the activated concept/topic subtree rather
	// than relying solely on lexical overlap with every distant hop.
	if routing.Intent == IntentMultiHop {
		unitsByID := make(map[string]Unit, len(compilation.Units))
		for _, unit := range compilation.Units {
			unitsByID[unit.ID] = unit
		}
		visited := map[string]bool{}
		var collect func(unitID string)
		collect = func(unitID string) {
			if visited[unitID] {
				return
			}
			visited[unitID] = true
			unit, ok := unitsByID[unitID]
			if !ok {
				return
			}
			if unit.Type == UnitFact {
				factContent(unit.Content)
			}
			for _, derived := range unit.DerivedFrom {
				collect(derived)
			}
		}
		for _, hit := range orientation.Hits {
			collect(hit.Unit.ID)
		}

		type relationEdge struct {
			relation Relation
			otherID  string
		}
		adjacency := map[string][]relationEdge{}
		for _, relation := range compilation.Relations {
			if relation.Status != UnitActive {
				continue
			}
			adjacency[relation.SubjectUnitID] = append(adjacency[relation.SubjectUnitID], relationEdge{relation: relation, otherID: relation.ObjectUnitID})
			adjacency[relation.ObjectUnitID] = append(adjacency[relation.ObjectUnitID], relationEdge{relation: relation, otherID: relation.SubjectUnitID})
		}
		queue := make([]string, 0, len(orientation.Hits)+len(facts.Hits))
		for _, hit := range orientation.Hits {
			queue = append(queue, hit.Unit.ID)
		}
		for _, hit := range facts.Hits {
			queue = append(queue, hit.Unit.ID)
		}
		visitedRelations := map[string]bool{}
		for depth := 0; depth < 2 && len(queue) > 0; depth++ {
			next := make([]string, 0)
			for _, unitID := range queue {
				for _, edge := range adjacency[unitID] {
					if !visitedRelations[edge.relation.ID] {
						visitedRelations[edge.relation.ID] = true
						if len(pkg.Relations) < 8 {
							pkg.Relations = append(pkg.Relations, clipContextText(edge.relation.Statement, 320))
						}
					}
					if other, ok := unitsByID[edge.otherID]; ok && other.Type == UnitFact && other.Status == UnitActive {
						factContent(other.Content)
						next = append(next, other.ID)
					}
				}
			}
			queue = next
		}
	}
	if pkg.Relations == nil {
		pkg.Relations = []string{}
	}

	deduplicated := deduplicateContextEvidence(evidence)
	deduplicated = selectTemporalEvidence(deduplicated, routing.Temporal, resolvedVersion)
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
	evidenceReserve := p.TokenBudget * 3 / 4
	if evidenceReserve < 48 {
		evidenceReserve = 48
	}
	if evidenceReserve > 256 {
		evidenceReserve = 256
	}
	writeSection := func(header string, lines []string, allowance int) []string {
		if len(lines) == 0 || remaining <= chunk.EstimateTokens(header)+2 {
			return nil
		}
		out := make([]string, 0, len(lines))
		used := chunk.EstimateTokens(header) + 2
		for _, line := range lines {
			cost := chunk.EstimateTokens("- " + line)
			if used+cost > allowance || remaining-cost <= evidenceReserve {
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
	displayQuery := clipToContextTokens(p.Query, p.TokenBudget/4)
	builder.WriteString("# Knowledge Context\nQuery: ")
	builder.WriteString(displayQuery)
	builder.WriteString("\n")
	remaining -= chunk.EstimateTokens("# Knowledge Context\nQuery: " + displayQuery + "\n")

	orientation := writeSection("## Knowledge orientation", p.KnowledgeSummary, p.TokenBudget/4)
	temporal := writeSection("## Temporal selection", p.temporalLines(), p.TokenBudget/20)
	concepts := writeSection("## Relevant concepts", p.Concepts, p.TokenBudget/10)
	relationLines := writeSection("## Relevant relations", p.Relations, p.TokenBudget/20)
	facts := writeSection("## Critical facts", p.Facts, p.TokenBudget/8)

	builder.WriteString("\n")
	for _, section := range []struct {
		header string
		lines  []string
	}{{"## Knowledge orientation", orientation}, {"## Temporal selection", temporal}, {"## Relevant concepts", concepts},
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
		if heading != "" && !strings.EqualFold(heading, title) {
			header += " / " + heading
		}
		cost := chunk.EstimateTokens(header+"\n") + chunk.EstimateTokens(text)
		if cost > remaining-12 {
			available := remaining - 12 - chunk.EstimateTokens(header+"\n")
			if available < 12 {
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
	if len(p.Evidence) > 0 && remaining > p.TokenBudget/4 {
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

// selectTemporalEvidence re-ranks, and narrowly filters, exact evidence after
// the ordinary retrieval stage. History is only excluded when a requested or
// resolved replacement scope is present.
func selectTemporalEvidence(items []ContextEvidence, temporal TemporalQuery, resolved string) []ContextEvidence {
	if temporal.Intent == TemporalNone {
		return items
	}
	for i := range items {
		if items[i].Version == "" {
			sourceText := items[i].DocumentTitle + "\n" + items[i].Text
			items[i].Version, _, _, _, _ = ExtractTemporalSource(sourceText, nil)
		}
	}
	targets := append([]string(nil), temporal.Versions...)
	if resolved != "" && (temporal.Intent == TemporalCurrent || temporal.Intent == TemporalValidity) {
		targets = []string{resolved}
	}
	anyTarget := false
	for _, item := range items {
		if item.Version != "" && containsIdentity(targets, item.Version) {
			anyTarget = true
			break
		}
	}
	// Explicit and historical requests must not silently fall back to another
	// version when the requested scope has no retrieved evidence.
	if len(targets) > 0 && !anyTarget &&
		(temporal.Intent == TemporalExplicitVersion || temporal.Intent == TemporalHistorical) {
		return []ContextEvidence{}
	}
	if anyTarget {
		narrow := temporal.Intent == TemporalCurrent || temporal.Intent == TemporalValidity ||
			temporal.Intent == TemporalExplicitVersion || temporal.Intent == TemporalHistorical
		out := make([]ContextEvidence, 0, len(items))
		for i := range items {
			if items[i].Version != "" && containsIdentity(targets, items[i].Version) {
				items[i].Score += 8
				items[i].TemporalStatus = "selected"
				out = append(out, items[i])
				continue
			}
			// Scope narrowing is context selection, not physical deletion:
			// superseded/source-version units remain in semantic memory and can
			// be recalled by historical/evolution queries.
			if narrow && items[i].Version != "" {
				continue
			}
			items[i].Score *= 0.7
			out = append(out, items[i])
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
	} else if resolved != "" {
		for i := range items {
			if items[i].Version == resolved {
				items[i].Score += 6
				items[i].TemporalStatus = "selected"
			}
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Score != items[j].Score {
			return items[i].Score > items[j].Score
		}
		if items[i].DocumentID != items[j].DocumentID {
			return items[i].DocumentID < items[j].DocumentID
		}
		return items[i].ChunkID < items[j].ChunkID
	})
	return items
}

func temporalFactLine(unit Unit) string {
	prefix := ""
	if unit.Version != "" {
		prefix += "version=" + unit.Version
	}
	if status := unit.Metadata["temporal_status"]; status != "" {
		if prefix != "" {
			prefix += ","
		}
		prefix += "status=" + status
	}
	if prefix == "" {
		return unit.Content
	}
	return "[" + prefix + "] " + unit.Content
}

func (p *ContextPackage) temporalLines() []string {
	if p.TemporalIntent == "" || p.TemporalIntent == string(TemporalNone) {
		return nil
	}
	lines := []string{"intent=" + p.TemporalIntent}
	if p.ResolvedVersion != "" {
		lines = append(lines, "resolved_version="+p.ResolvedVersion)
	}
	if p.TemporalReason != "" {
		lines = append(lines, "reason="+p.TemporalReason)
	}
	return lines
}

func contextCountsForPlan(plan QueryPlan) (int, int) {
	switch plan.Intent {
	case IntentGlobal:
		return 12, 2
	case IntentCrossDocument, IntentComparison:
		return 10, 4
	case IntentMultiHop:
		return 10, 5
	case IntentTemporal:
		return 6, 5
	default:
		return 6, 5
	}
}

func deduplicateContextEvidence(items []ContextEvidence) []ContextEvidence {
	for i := range items {
		if items[i].Version == "" {
			items[i].Version, _, _, _, _ = ExtractTemporalSource(items[i].DocumentTitle, nil)
		}
		if items[i].Version != "" && items[i].TemporalStatus == "" {
			items[i].TemporalStatus = "source-scoped"
		}
	}
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
