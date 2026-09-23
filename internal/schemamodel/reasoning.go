package schemamodel

import (
	"fmt"
	"sort"
	"strings"
)

// SchemaQueryIntent is the minimum 0.7 routing vocabulary.
type SchemaQueryIntent string

const (
	IntentExactField      SchemaQueryIntent = "EXACT_FIELD"
	IntentExactTable      SchemaQueryIntent = "EXACT_TABLE"
	IntentEnumLookup      SchemaQueryIntent = "ENUM_LOOKUP"
	IntentSemanticField   SchemaQueryIntent = "SEMANTIC_FIELD"
	IntentTableDiscovery  SchemaQueryIntent = "TABLE_DISCOVERY"
	IntentCrossTable      SchemaQueryIntent = "CROSS_TABLE"
	IntentJoinCandidate   SchemaQueryIntent = "JOIN_CANDIDATE"
	IntentDataRequirement SchemaQueryIntent = "DATA_REQUIREMENT"
	IntentUnknown         SchemaQueryIntent = "UNKNOWN"
)

// EntityHit is a schema entity result, never an evidence chunk.
type EntityHit struct {
	EntityType string        `json:"entityType"`
	EntityID   string        `json:"entityId"`
	Title      string        `json:"title"`
	Content    string        `json:"content"`
	Score      float64       `json:"score"`
	Field      *Field        `json:"field,omitempty"`
	Table      *LogicalTable `json:"table,omitempty"`
	Concepts   []string      `json:"concepts,omitempty"`
	Source     SourceRef     `json:"source,omitempty"`
}

// SchemaSearchResponse separates lexical/entity hits from intent diagnostics.
type SchemaSearchResponse struct {
	Query       string            `json:"query"`
	Intent      SchemaQueryIntent `json:"intent"`
	Concepts    []string          `json:"concepts,omitempty"`
	Fields      []EntityHit       `json:"fields,omitempty"`
	Tables      []EntityHit       `json:"tables,omitempty"`
	TopK        int               `json:"topK"`
	Diagnostics []string          `json:"diagnostics,omitempty"`
}

// ClassifySchemaQuery uses explicit cues first; a fallback is still diagnostic,
// not a claim that the classification is authoritative.
func ClassifySchemaQuery(query string) SchemaQueryIntent {
	normalized := strings.ToLower(NormalizeText(query))
	switch {
	case strings.Contains(normalized, "可能通过") || strings.Contains(normalized, "关联") || strings.Contains(normalized, "join") || strings.Contains(normalized, "candidate key"):
		return IntentJoinCandidate
	case strings.Contains(normalized, "哪些表") || strings.Contains(normalized, "跨工作簿") || strings.Contains(normalized, "跨表"):
		return IntentCrossTable
	case strings.Contains(normalized, "需要哪些") || strings.Contains(normalized, "分析") || strings.Contains(normalized, "resolve"):
		return IntentDataRequirement
	case strings.Contains(normalized, "取值") || strings.Contains(normalized, "枚举") || strings.Contains(normalized, "enum"):
		return IntentEnumLookup
	case strings.Contains(normalized, "有哪些字段") || strings.Contains(normalized, " fields") || strings.Contains(normalized, "字段列表"):
		return IntentExactTable
	case strings.Contains(normalized, "是什么") || strings.Contains(normalized, "what is"):
		return IntentExactField
	case strings.Contains(normalized, "字段") || strings.Contains(normalized, "field"):
		return IntentSemanticField
	default:
		return IntentTableDiscovery
	}
}

// QueryConcepts maps business language to the small, evidence-derived concept
// vocabulary. It intentionally returns candidate concepts, not equivalences.
func QueryConcepts(query string) []string { return conceptsFor(query) }

// SearchModels performs bounded lexical/entity activation over schema models.
func SearchModels(models []Model, query string, topK int) SchemaSearchResponse {
	if topK <= 0 {
		topK = 10
	}
	if topK > 100 {
		topK = 100
	}
	intent := ClassifySchemaQuery(query)
	concepts := QueryConcepts(query)
	response := SchemaSearchResponse{Query: query, Intent: intent, Concepts: concepts, TopK: topK}
	normalized := strings.ToLower(NormalizeText(query))
	terms := queryTerms(query)
	type scored struct{ hit EntityHit }
	var fields, tables []scored
	for modelIndex := range models {
		fieldsByID := make(map[string]*Field, len(models[modelIndex].Fields))
		for i := range models[modelIndex].Fields {
			fieldsByID[models[modelIndex].Fields[i].ID] = &models[modelIndex].Fields[i]
		}
		for i := range models[modelIndex].Tables {
			table := &models[modelIndex].Tables[i]
			score := scoreText(query, terms, table.Name+" "+table.DisplayName+" "+table.Description)
			if len(concepts) > 0 {
				matches := 0
				for _, fieldID := range table.FieldIDs {
					field := fieldsByID[fieldID]
					if field == nil {
						continue
					}
					if intersects(field.Concepts, concepts) {
						matches++
					}
				}
				if matches > 0 {
					score += 2 + float64(matches)*0.1
				}
			}
			if score <= 0 {
				continue
			}
			tables = append(tables, scored{hit: EntityHit{
				EntityType: "TABLE", EntityID: table.ID, Title: table.Name, Content: tableRepresentation(*table),
				Score: score, Table: table, Source: table.Source,
			}})
		}
		for i := range models[modelIndex].Fields {
			field := &models[modelIndex].Fields[i]
			score := scoreText(query, terms, strings.Join([]string{field.Name, field.DisplayName, strings.Join(field.Aliases, " "), field.Description, field.DataType}, " "))
			if len(concepts) > 0 && intersects(field.Concepts, concepts) {
				score += 4
			}
			if intent == IntentEnumLookup && field.Enum != nil && (score > 0 || containsToken(normalized, NormalizeIdentifier(field.Name))) {
				score += 1
			}
			if score <= 0 {
				continue
			}
			fields = append(fields, scored{hit: EntityHit{
				EntityType: "FIELD", EntityID: field.ID, Title: field.Name, Content: fieldRepresentation(*field),
				Score: score, Field: field, Concepts: append([]string(nil), field.Concepts...), Source: field.Source,
			}})
		}
	}
	sort.SliceStable(fields, func(i, j int) bool {
		if fields[i].hit.Score != fields[j].hit.Score {
			return fields[i].hit.Score > fields[j].hit.Score
		}
		return fields[i].hit.EntityID < fields[j].hit.EntityID
	})
	sort.SliceStable(tables, func(i, j int) bool {
		if tables[i].hit.Score != tables[j].hit.Score {
			return tables[i].hit.Score > tables[j].hit.Score
		}
		return tables[i].hit.EntityID < tables[j].hit.EntityID
	})
	for _, item := range bounded(fields, topK) {
		response.Fields = append(response.Fields, item.hit)
	}
	for _, item := range bounded(tables, topK) {
		response.Tables = append(response.Tables, item.hit)
	}
	if len(concepts) > 0 {
		response.Diagnostics = append(response.Diagnostics, "concept_candidates="+strings.Join(concepts, ","))
	}
	return response
}

// TableConceptMatch is one candidate table and the fields that qualified it.
type TableConceptMatch struct {
	TableID       string             `json:"tableId"`
	TableName     string             `json:"tableName"`
	Workbook      string             `json:"workbook"`
	DocumentID    string             `json:"documentId"`
	ConceptFields map[string][]Field `json:"conceptFields"`
	Source        SourceRef          `json:"source"`
}

// ConceptSetResult exposes set reasoning evidence without pretending that
// similar concepts are equivalent.
type ConceptSetResult struct {
	Concept string  `json:"concept"`
	Fields  []Field `json:"fields,omitempty"`
	Missing string  `json:"missing,omitempty"`
}

// CrossWorkbookResult contains union, intersection and grouping results.
type CrossWorkbookResult struct {
	Concepts        []ConceptSetResult  `json:"concepts"`
	CandidateTables []TableConceptMatch `json:"candidateTables,omitempty"`
	Workbooks       []string            `json:"workbooks,omitempty"`
	Operation       string              `json:"operation"`
	Diagnostic      string              `json:"diagnostic,omitempty"`
}

// ResolveConceptSets groups schema fields by concept and then by table.
func ResolveConceptSets(models []Model, query string, maxPerConcept int) CrossWorkbookResult {
	concepts := QueryConcepts(query)
	if len(concepts) == 0 {
		concepts = nil
	}
	result := CrossWorkbookResult{Operation: "intersection", Concepts: make([]ConceptSetResult, 0, len(concepts))}
	if len(concepts) == 0 {
		result.Operation = "none"
		result.Diagnostic = "no known business concept in query"
		return result
	}
	tableFields := make(map[string]*TableConceptMatch)
	workbooks := make(map[string]bool)
	for _, model := range models {
		for _, conceptName := range concepts {
			set := ConceptSetResult{Concept: conceptName}
			for _, field := range model.Fields {
				if !intersects(field.Concepts, []string{conceptName}) {
					continue
				}
				if maxPerConcept > 0 && len(set.Fields) >= maxPerConcept {
					break
				}
				set.Fields = append(set.Fields, field)
				match, ok := tableFields[field.TableID]
				if !ok {
					table, found := model.TableByID(field.TableID)
					if !found {
						continue
					}
					match = &TableConceptMatch{TableID: table.ID, TableName: table.Name, Workbook: table.Scope.Workbook, DocumentID: table.DocumentID, ConceptFields: map[string][]Field{}, Source: table.Source}
					tableFields[table.ID] = match
				}
				match.ConceptFields[conceptName] = append(match.ConceptFields[conceptName], field)
			}
			if len(set.Fields) == 0 {
				set.Missing = "no field mapped to this concept"
			}
			result.Concepts = append(result.Concepts, set)
		}
	}
	for _, match := range tableFields {
		if len(match.ConceptFields) != len(concepts) {
			continue
		}
		result.CandidateTables = append(result.CandidateTables, *match)
		workbooks[match.Workbook] = true
	}
	for workbook := range workbooks {
		result.Workbooks = append(result.Workbooks, workbook)
	}
	sort.Strings(result.Workbooks)
	sort.SliceStable(result.CandidateTables, func(i, j int) bool { return result.CandidateTables[i].TableID < result.CandidateTables[j].TableID })
	return result
}

// JoinCandidateHit is probabilistic and explicitly not a foreign key.
type JoinCandidateHit struct {
	LeftField  Field       `json:"leftField"`
	RightField Field       `json:"rightField"`
	Confidence float64     `json:"confidence"`
	Reason     string      `json:"reason"`
	Evidence   []SourceRef `json:"evidence,omitempty"`
}

// CandidateJoins compares typed identifier-like fields across two tables.
func CandidateJoins(models []Model, leftTableID, rightTableID string, max int) []JoinCandidateHit {
	if max <= 0 {
		max = 10
	}
	var left, right *LogicalTable
	var leftModel, rightModel *Model
	for i := range models {
		if table, ok := models[i].TableByID(leftTableID); ok && left == nil {
			left, leftModel = &table, &models[i]
		}
		if table, ok := models[i].TableByID(rightTableID); ok && right == nil {
			right, rightModel = &table, &models[i]
		}
	}
	if left == nil || right == nil || leftModel == nil || rightModel == nil {
		return nil
	}
	candidates := make([]JoinCandidateHit, 0)
	for _, leftField := range leftModel.FieldsByID(leftTableID) {
		for _, rightField := range rightModel.FieldsByID(rightTableID) {
			score, reason := joinScore(leftField, rightField)
			if score <= 0 {
				continue
			}
			candidates = append(candidates, JoinCandidateHit{
				LeftField: leftField, RightField: rightField, Confidence: score, Reason: reason,
				Evidence: compactSourceRefs([]SourceRef{leftField.Source, rightField.Source}),
			})
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Confidence != candidates[j].Confidence {
			return candidates[i].Confidence > candidates[j].Confidence
		}
		return candidates[i].LeftField.ID < candidates[j].LeftField.ID
	})
	if len(candidates) > max {
		candidates = candidates[:max]
	}
	return candidates
}

func joinScore(left, right Field) (float64, string) {
	leftName, rightName := NormalizeIdentifier(left.Name), NormalizeIdentifier(right.Name)
	score := 0.0
	reasons := make([]string, 0, 3)
	if leftName == rightName {
		score += 0.45
		reasons = append(reasons, "same normalized field name")
	} else if strings.Contains(leftName, rightName) || strings.Contains(rightName, leftName) {
		score += 0.22
		reasons = append(reasons, "related field names")
	}
	leftConcepts, rightConcepts := map[string]bool{}, map[string]bool{}
	for _, concept := range left.Concepts {
		leftConcepts[concept] = true
	}
	for _, concept := range right.Concepts {
		if leftConcepts[concept] {
			score += 0.28
			reasons = append(reasons, "shared business concept "+concept)
		}
		rightConcepts[concept] = true
	}
	_ = rightConcepts
	if strings.EqualFold(strings.TrimSpace(left.DataType), strings.TrimSpace(right.DataType)) && left.DataType != "" {
		score += 0.12
		reasons = append(reasons, "compatible declared type")
	}
	if len(left.KeyHints) > 0 && len(right.KeyHints) > 0 {
		score += 0.08
		reasons = append(reasons, "both fields have identifier/key hints")
	}
	if score < 0.45 {
		return 0, ""
	}
	if score > 0.85 {
		score = 0.85
	}
	return score, "candidate only: " + strings.Join(reasons, "; ")
}

// ResolvedRequirement is the non-SQL boundary of 0.7.
type ResolvedRequirement struct {
	Query           string              `json:"query"`
	Intent          SchemaQueryIntent   `json:"intent"`
	Concepts        []ConceptSetResult  `json:"concepts"`
	CandidateTables []TableConceptMatch `json:"candidateTables,omitempty"`
	CandidateJoins  []JoinCandidateHit  `json:"candidateJoins,omitempty"`
	Unknowns        []string            `json:"unknowns,omitempty"`
	EvidenceCount   int                 `json:"evidenceCount"`
	Context         string              `json:"context,omitempty"`
	Diagnostics     []string            `json:"diagnostics,omitempty"`
}

// ResolveDataRequirement decomposes natural language into data-model candidates
// and explicit unknowns. It never generates SQL or claims executable filters.
func ResolveDataRequirement(models []Model, query string, maxPerConcept, maxTables int) ResolvedRequirement {
	sets := ResolveConceptSets(models, query, maxPerConcept)
	resolved := ResolvedRequirement{Query: query, Intent: IntentDataRequirement, Concepts: sets.Concepts, CandidateTables: sets.CandidateTables}
	for _, concept := range sets.Concepts {
		if concept.Missing != "" {
			resolved.Unknowns = append(resolved.Unknowns, concept.Concept+": "+concept.Missing)
		}
		resolved.EvidenceCount += len(concept.Fields)
	}
	if strings.Contains(strings.ToLower(query), "过去") || strings.Contains(strings.ToLower(query), "last") {
		resolved.Unknowns = append(resolved.Unknowns, "exact time-window criterion cannot be determined from a dictionary alone")
	}
	if strings.Contains(query, "没有") || strings.Contains(strings.ToLower(query), "without") {
		resolved.Unknowns = append(resolved.Unknowns, "negative business condition cannot be inferred as an exact filter from a dictionary alone")
	}
	limit := maxTables
	if limit <= 0 {
		limit = 5
	}
	for i, left := range resolved.CandidateTables {
		if len(resolved.CandidateJoins) >= 10 {
			break
		}
		for _, right := range resolved.CandidateTables[i+1:] {
			if len(resolved.CandidateJoins) >= 10 {
				break
			}
			resolved.CandidateJoins = append(resolved.CandidateJoins, CandidateJoins(models, left.TableID, right.TableID, 3)...)
		}
		if i+1 >= limit {
			break
		}
	}
	if len(resolved.CandidateTables) == 0 {
		resolved.Unknowns = append(resolved.Unknowns, "no table contains every requested concept")
	}
	resolved.Context = CompileRequirementContext(resolved, 8_000)
	resolved.Diagnostics = append(resolved.Diagnostics, "sql_generation=disabled", "join_assertions=forbidden")
	return resolved
}

// CompileRequirementContext creates a bounded structured projection instead of
// dumping schema or increasing TopK.
func CompileRequirementContext(requirement ResolvedRequirement, maxChars int) string {
	if maxChars <= 0 {
		maxChars = 8_000
	}
	var builder strings.Builder
	writeLine := func(format string, args ...any) {
		if builder.Len() >= maxChars {
			return
		}
		builder.WriteString(fmt.Sprintf(format, args...))
		builder.WriteString("\n")
	}
	writeLine("Requirement: %s", requirement.Query)
	for conceptIndex, concept := range requirement.Concepts {
		writeLine("Concept %d: %s", conceptIndex+1, concept.Concept)
		for fieldIndex, field := range concept.Fields {
			if fieldIndex >= 5 {
				writeLine("  +%d more candidate fields", len(concept.Fields)-5)
				break
			}
			writeLine("  Field: %s.%s; type=%s; meaning=%s", field.TableName, field.Name, field.DataType, field.Description)
			writeLine("  Evidence: %s", sourceLabel(field.Source))
		}
	}
	for tableIndex, table := range requirement.CandidateTables {
		if tableIndex >= 10 {
			writeLine("+%d more candidate tables", len(requirement.CandidateTables)-10)
			break
		}
		writeLine("Candidate table: %s; workbook=%s; concepts=%s", table.TableName, table.Workbook, strings.Join(conceptKeys(table.ConceptFields), ","))
	}
	for _, join := range requirement.CandidateJoins {
		writeLine("Candidate join (not a declared FK): %s.%s ~ %s.%s; confidence=%.2f; reason=%s", join.LeftField.TableName, join.LeftField.Name, join.RightField.TableName, join.RightField.Name, join.Confidence, join.Reason)
	}
	for _, unknown := range requirement.Unknowns {
		writeLine("Unknown: %s", unknown)
	}
	output := builder.String()
	if len(output) > maxChars {
		return output[:maxChars]
	}
	return output
}

func sourceLabel(source SourceRef) string {
	parts := make([]string, 0, 4)
	if source.DocumentTitle != "" {
		parts = append(parts, source.DocumentTitle)
	}
	if source.Sheet != "" {
		parts = append(parts, "sheet:"+source.Sheet)
	}
	if source.Range != "" {
		parts = append(parts, "range:"+source.Range)
	}
	if source.Granularity != "" {
		parts = append(parts, "granularity:"+source.Granularity)
	}
	return strings.Join(parts, "; ")
}

func conceptKeys(values map[string][]Field) []string {
	out := make([]string, 0, len(values))
	for key := range values {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func scoreText(query string, terms []string, target string) float64 {
	target = strings.ToLower(NormalizeText(target))
	score := 0.0
	for _, term := range terms {
		if strings.Contains(target, term) {
			score += 1
		}
	}
	if query != "" && strings.Contains(target, strings.ToLower(NormalizeText(query))) {
		score += 2
	}
	return score
}

func queryTerms(query string) []string {
	query = strings.ToLower(NormalizeText(query))
	terms := make([]string, 0, 8)
	current := make([]rune, 0, len(query))
	flush := func() {
		if len(current) > 1 {
			terms = append(terms, string(current))
		}
		current = current[:0]
	}
	for _, r := range query {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			current = append(current, r)
		case r >= 0x80:
			current = append(current, r)
			if len(current) >= 2 {
				flush()
			}
		default:
			flush()
		}
	}
	flush()
	return dedupeStrings(terms)
}

func containsToken(value, token string) bool { return token != "" && strings.Contains(value, token) }

func intersects(left, right []string) bool {
	for _, l := range left {
		for _, r := range right {
			if l == r {
				return true
			}
		}
	}
	return false
}

func bounded[T any](values []T, limit int) []T {
	if len(values) > limit {
		return values[:limit]
	}
	return values
}
