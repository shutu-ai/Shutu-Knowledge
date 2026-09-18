package semantic

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/shutu-ai/shutu-knowledge/internal/documentir"
)

const (
	BuiltinCompiler           = "shutu-builtin"
	BuiltinCompilerVersion    = "0.4.0-ph2"
	BuiltinModel              = "deterministic"
	BuiltinModelVersion       = "v1"
	BuiltinPromptVersion      = "none"
	maxCompiledFactsPerDoc    = 128
	maxCompiledConceptsPerDoc = 24
	maxCompiledTopicsPerBase  = 64
	maxSummaryCharacters      = 2400
)

// SourceDocument is the compiler's isolated view of one active document
// generation. Chunk links are supplied by the caller from the existing
// chunk_node_links table; the compiler never guesses or rechunks evidence.
type SourceDocument struct {
	BaseID          string
	DocumentID      string
	Title           string
	IndexGeneration int64
	SourceVersion   int64
	UpdatedAt       int64
	IR              *documentir.Document
	ChunkIDsByNode  map[string][]string
}

type factCandidate struct {
	canonicalKey string
	content      string
	heading      string
	nodeType     string
	confidence   float64
	sources      []EvidenceSource
	conceptKeys  map[string]bool
}

type conceptCandidate struct {
	canonicalKey string
	title        string
	aliases      map[string]bool
	documents    map[string]bool
	headings     map[string]bool
	sources      []EvidenceSource
	factKeys     map[string]bool
	topicKey     string
}

type topicCandidate struct {
	canonicalKey string
	title        string
	aliases      map[string]bool
	conceptKeys  map[string]bool
}

// Compile produces a deterministic, offline PH2 semantic-memory projection:
// Facts, Concepts, Topics, and document Summaries. It does not call an LLM,
// does not modify Document IR, and does not create Wiki pages or benchmark-gated
// semantic relations.
func Compile(baseID string, generation int64, documents []SourceDocument, createdAt int64) (Compilation, error) {
	if strings.TrimSpace(baseID) == "" || generation <= 0 {
		return Compilation{}, fmt.Errorf("semantic compilation requires base and generation")
	}
	if createdAt <= 0 {
		return Compilation{}, fmt.Errorf("semantic compilation requires creation time")
	}
	if len(documents) == 0 {
		return Compilation{}, fmt.Errorf("semantic compilation requires at least one source document")
	}

	sourceIDs := make([]string, 0, len(documents))
	for i := range documents {
		doc := &documents[i]
		if err := validateSourceDocument(doc); err != nil {
			return Compilation{}, err
		}
		doc.BaseID = baseID
		sourceIDs = append(sourceIDs, doc.DocumentID)
	}
	sort.Strings(sourceIDs)
	sort.Slice(documents, func(i, j int) bool { return documents[i].DocumentID < documents[j].DocumentID })

	concepts := collectConcepts(documents)
	facts := collectFacts(documents, concepts)
	topics := collectTopics(documents, concepts)
	assignConceptTopics(concepts, topics)

	fingerprints := make(map[string]string, len(documents))
	for _, doc := range documents {
		fingerprints[doc.DocumentID] = SourceFingerprint(doc)
	}
	fingerprintJSON, err := json.Marshal(fingerprints)
	if err != nil {
		return Compilation{}, fmt.Errorf("marshal source fingerprints: %w", err)
	}
	compilation := Compilation{
		BaseID: baseID, Generation: generation, State: CompilationBuilding,
		Compiler: BuiltinCompiler, CompilerVersion: BuiltinCompilerVersion,
		Model: BuiltinModel, ModelVersion: BuiltinModelVersion,
		PromptVersion: BuiltinPromptVersion, SourceDocumentIDs: sourceIDs,
		Metadata:  map[string]string{"source_fingerprints": string(fingerprintJSON)},
		CreatedAt: createdAt,
	}

	unitByFactKey, factUnits := buildFactUnits(baseID, generation, facts, createdAt)
	conceptUnits := buildConceptUnits(baseID, generation, concepts, facts, unitByFactKey, createdAt)
	topicUnits := buildTopicUnits(baseID, generation, topics, createdAt)
	summaryUnits := buildSummaryUnits(baseID, generation, documents, concepts, facts, unitByFactKey, createdAt)

	compilation.Units = append(compilation.Units, factUnits...)
	compilation.Units = append(compilation.Units, conceptUnits...)
	compilation.Units = append(compilation.Units, topicUnits...)
	compilation.Units = append(compilation.Units, summaryUnits...)
	if err := compilation.Validate(); err != nil {
		return Compilation{}, err
	}
	return compilation, nil
}

// SourceFingerprint captures the immutable inputs that require recompilation:
// active IR generation, source version, title, and normalized IR text.
func SourceFingerprint(doc SourceDocument) string {
	if doc.IR == nil {
		return fmt.Sprintf("%d:%d:%s", doc.IndexGeneration, doc.SourceVersion, stableKey(doc.Title))
	}
	return fmt.Sprintf("%d:%d:%s", doc.IndexGeneration, doc.SourceVersion,
		stableKey(doc.Title+"\x00"+doc.IR.Text()))
}

func validateSourceDocument(doc *SourceDocument) error {
	if strings.TrimSpace(doc.BaseID) == "" || strings.TrimSpace(doc.DocumentID) == "" {
		return fmt.Errorf("semantic source document lacks base/document identity")
	}
	if doc.IndexGeneration <= 0 || doc.SourceVersion <= 0 {
		return fmt.Errorf("semantic source document %s lacks generation/version", doc.DocumentID)
	}
	if doc.IR == nil {
		return fmt.Errorf("semantic source document %s has no Document IR", doc.DocumentID)
	}
	if err := doc.IR.Validate(); err != nil {
		return fmt.Errorf("semantic source document %s IR: %w", doc.DocumentID, err)
	}
	return nil
}

func collectConcepts(documents []SourceDocument) map[string]*conceptCandidate {
	out := map[string]*conceptCandidate{}
	for _, doc := range documents {
		count := 0
		title := normalizeConceptKey(doc.Title)
		if isUsefulConceptKey(title) && count < maxCompiledConceptsPerDoc {
			addConceptCandidate(out, doc.Title, title, doc, "")
			count++
		}
		for _, node := range doc.IR.Nodes {
			if node.Type != documentir.TypeHeading && node.Type != documentir.TypeSection {
				continue
			}
			key := normalizeConceptKey(node.Text)
			if !isUsefulConceptKey(key) || count >= maxCompiledConceptsPerDoc {
				continue
			}
			heading := strings.Join(node.HeadingPath, " / ")
			addConceptCandidate(out, node.Text, key, doc, heading)
			count++
		}
	}
	return out
}

func addConceptCandidate(out map[string]*conceptCandidate, title, key string, doc SourceDocument, heading string) {
	candidate, ok := out[key]
	if !ok {
		candidate = &conceptCandidate{
			canonicalKey: key,
			title:        strings.TrimSpace(title),
			aliases:      map[string]bool{},
			documents:    map[string]bool{},
			headings:     map[string]bool{},
			factKeys:     map[string]bool{},
		}
		out[key] = candidate
	}
	candidate.aliases[strings.TrimSpace(title)] = true
	candidate.documents[doc.DocumentID] = true
	if strings.TrimSpace(heading) != "" {
		candidate.headings[heading] = true
	}
	candidate.sources = append(candidate.sources, EvidenceSource{
		DocumentID: doc.DocumentID, IndexGeneration: doc.IndexGeneration,
		SourceVersion: doc.SourceVersion, NodeID: rootNodeID(doc.IR),
		SourceAnchor: anchorMap(doc.IR), SourceOrder: len(candidate.sources),
	})
}

func collectFacts(documents []SourceDocument, concepts map[string]*conceptCandidate) map[string]*factCandidate {
	out := map[string]*factCandidate{}
	for _, doc := range documents {
		count := 0
		for _, node := range doc.IR.Nodes {
			if count >= maxCompiledFactsPerDoc {
				break
			}
			text := strings.TrimSpace(node.Text)
			if text == "" || node.Type == documentir.TypeDocument || node.Type == documentir.TypeHeading ||
				node.Type == documentir.TypeSection || node.Type == documentir.TypeHeader ||
				node.Type == documentir.TypeFooter || node.Type == documentir.TypeFootnote ||
				node.Type == documentir.TypeCode {
				continue
			}
			confidence := 0.68
			if node.Type == documentir.TypeTableCell || node.Type == documentir.TypeTableRow ||
				node.Type == documentir.TypeTable || node.Type == documentir.TypeCaption {
				confidence = 0.75
			}
			sentences := splitSentences(text)
			for _, sentence := range sentences {
				if count >= maxCompiledFactsPerDoc {
					break
				}
				content := cleanFact(sentence)
				if !isExplicitFact(content) {
					continue
				}
				key := "fact:" + stableKey(normalizeFactKey(content))
				candidate, ok := out[key]
				if !ok {
					candidate = &factCandidate{
						canonicalKey: key, content: content,
						heading:  strings.Join(node.HeadingPath, " / "),
						nodeType: string(node.Type), confidence: confidence,
						conceptKeys: map[string]bool{},
					}
					out[key] = candidate
				}
				if conceptKey := matchConceptKey(content, node, concepts); conceptKey != "" {
					candidate.conceptKeys[conceptKey] = true
					concepts[conceptKey].factKeys[key] = true
				}
				candidate.sources = append(candidate.sources, EvidenceSource{
					DocumentID: doc.DocumentID, IndexGeneration: doc.IndexGeneration,
					SourceVersion: doc.SourceVersion, NodeID: node.ID,
					ChunkID: firstChunkID(doc, node.ID), SourceAnchor: anchorOf(node),
					SourceOrder: len(candidate.sources),
				})
				count++
			}
		}
	}
	deleteEmptyConceptKeys(out)
	return out
}

func deleteEmptyConceptKeys(facts map[string]*factCandidate) {
	for _, fact := range facts {
		delete(fact.conceptKeys, "")
	}
}

func collectTopics(documents []SourceDocument, concepts map[string]*conceptCandidate) map[string]*topicCandidate {
	out := map[string]*topicCandidate{}
	for _, doc := range documents {
		titleKey := normalizeConceptKey(doc.Title)
		if isUsefulConceptKey(titleKey) && len(out) < maxCompiledTopicsPerBase {
			addTopicCandidate(out, doc.Title, titleKey)
		}
		for _, node := range doc.IR.Nodes {
			if len(out) >= maxCompiledTopicsPerBase {
				break
			}
			if node.Type != documentir.TypeHeading || len(node.HeadingPath) == 0 {
				continue
			}
			key := normalizeConceptKey(node.HeadingPath[0])
			if !isUsefulConceptKey(key) {
				continue
			}
			addTopicCandidate(out, node.HeadingPath[0], key)
		}
	}
	return out
}

func addTopicCandidate(out map[string]*topicCandidate, title, key string) {
	topic, ok := out[key]
	if !ok {
		topic = &topicCandidate{canonicalKey: key, title: strings.TrimSpace(title), aliases: map[string]bool{}, conceptKeys: map[string]bool{}}
		out[key] = topic
	}
	topic.aliases[strings.TrimSpace(title)] = true
}

func assignConceptTopics(concepts map[string]*conceptCandidate, topics map[string]*topicCandidate) {
	for _, concept := range concepts {
		bestKey := ""
		bestScore := 0
		for key := range topics {
			score := conceptTopicScore(concept.canonicalKey, key)
			if score > bestScore || (score == bestScore && score > 0 && key < bestKey) {
				bestScore = score
				bestKey = key
			}
		}
		concept.topicKey = bestKey
		if bestKey != "" {
			topics[bestKey].conceptKeys[concept.canonicalKey] = true
		}
	}
}

func buildFactUnits(baseID string, generation int64, facts map[string]*factCandidate, now int64) (map[string]string, []Unit) {
	keys := sortedMapKeys(facts)
	out := make([]Unit, 0, len(facts))
	byKey := map[string]string{}
	for _, key := range keys {
		fact := facts[key]
		if len(fact.sources) == 0 {
			continue
		}
		id := UnitID(baseID, generation, UnitFact, fact.canonicalKey)
		byKey[key] = id
		out = append(out, Unit{
			ID: id, BaseID: baseID, Generation: generation, Type: UnitFact,
			Title: clip(fact.content, 96), CanonicalKey: fact.canonicalKey, Content: fact.content,
			Confidence: fact.confidence, Status: UnitActive,
			Metadata: map[string]string{"nodeType": fact.nodeType, "heading": fact.heading},
			Sources:  fact.sources, CreatedAt: now, UpdatedAt: now,
		})
	}
	return byKey, out
}

func buildConceptUnits(baseID string, generation int64, concepts map[string]*conceptCandidate,
	facts map[string]*factCandidate, factIDs map[string]string, now int64) []Unit {
	keys := sortedMapKeys(concepts)
	out := make([]Unit, 0, len(concepts))
	for _, key := range keys {
		concept := concepts[key]
		id := UnitID(baseID, generation, UnitConcept, concept.canonicalKey)
		derived := sortedSetKeys(concept.factKeys)
		for _, factKey := range derived {
			if _, ok := facts[factKey]; ok {
				concept.factKeys[factKey] = true
			}
		}
		derivedIDs := make([]string, 0, len(concept.factKeys))
		for _, factKey := range sortedSetKeys(concept.factKeys) {
			if id, ok := factIDs[factKey]; ok {
				derivedIDs = append(derivedIDs, id)
			}
		}
		unit := Unit{
			ID: id, BaseID: baseID, Generation: generation, Type: UnitConcept,
			Title: concept.title, CanonicalKey: concept.canonicalKey,
			Content: conceptContent(concept), Aliases: sortedSetKeys(concept.aliases),
			Confidence: 0.72, Status: UnitActive, DerivedFrom: derivedIDs,
			CreatedAt: now, UpdatedAt: now,
		}
		if len(derivedIDs) == 0 {
			unit.Sources = concept.sources
		}
		out = append(out, unit)
	}
	return out
}

func buildTopicUnits(baseID string, generation int64, topics map[string]*topicCandidate, now int64) []Unit {
	keys := sortedMapKeys(topics)
	out := make([]Unit, 0, len(topics))
	for _, key := range keys {
		topic := topics[key]
		derived := make([]string, 0, len(topic.conceptKeys))
		for conceptKey := range topic.conceptKeys {
			derived = append(derived, UnitID(baseID, generation, UnitConcept, conceptKey))
		}
		sort.Strings(derived)
		out = append(out, Unit{
			ID:     UnitID(baseID, generation, UnitTopic, topic.canonicalKey),
			BaseID: baseID, Generation: generation, Type: UnitTopic,
			Title: topic.title, CanonicalKey: topic.canonicalKey,
			Content: topicContent(topic), Aliases: sortedSetKeys(topic.aliases),
			Confidence: 0.66, Status: UnitActive, DerivedFrom: derived,
			CreatedAt: now, UpdatedAt: now,
		})
	}
	return out
}

func buildSummaryUnits(baseID string, generation int64, documents []SourceDocument,
	concepts map[string]*conceptCandidate, facts map[string]*factCandidate, factIDs map[string]string, now int64) []Unit {
	out := make([]Unit, 0, len(documents))
	for _, doc := range documents {
		derived := map[string]bool{}
		docConcepts := make([]string, 0, 8)
		for key, concept := range concepts {
			if !concept.documents[doc.DocumentID] {
				continue
			}
			derived[UnitID(baseID, generation, UnitConcept, key)] = true
			docConcepts = append(docConcepts, concept.title)
			for factKey := range concept.factKeys {
				if fact, ok := facts[factKey]; ok && containsDocument(fact.sources, doc.DocumentID) {
					derived[factIDs[factKey]] = true
				}
			}
		}
		sort.Strings(docConcepts)
		unit := Unit{
			ID:     UnitID(baseID, generation, UnitSummary, fmt.Sprintf("summary:doc:%s:source:%d", doc.DocumentID, doc.SourceVersion)),
			BaseID: baseID, Generation: generation, Type: UnitSummary,
			Title: doc.Title, CanonicalKey: fmt.Sprintf("summary:doc:%s:source:%d", doc.DocumentID, doc.SourceVersion),
			Content: summaryContent(doc, docConcepts), Confidence: 0.7,
			Status: UnitActive, DerivedFrom: sortedSetKeys(derived),
			CreatedAt: now, UpdatedAt: now,
		}
		if len(unit.DerivedFrom) == 0 {
			unit.Sources = []EvidenceSource{{
				DocumentID: doc.DocumentID, IndexGeneration: doc.IndexGeneration,
				SourceVersion: doc.SourceVersion, NodeID: rootNodeID(doc.IR),
				SourceAnchor: anchorMap(doc.IR), SourceOrder: 0,
			}}
		}
		out = append(out, unit)
	}
	return out
}

func conceptContent(concept *conceptCandidate) string {
	var parts []string
	parts = append(parts, fmt.Sprintf("%s appears in %d source document(s).", concept.title, len(concept.documents)))
	if headings := sortedSetKeys(concept.headings); len(headings) > 0 {
		parts = append(parts, "Observed headings: "+strings.Join(headings, "; "))
	}
	return strings.Join(parts, " ")
}

func topicContent(topic *topicCandidate) string {
	titles := make([]string, 0, len(topic.conceptKeys))
	for key := range topic.conceptKeys {
		titles = append(titles, key)
	}
	sort.Strings(titles)
	if len(titles) == 0 {
		return topic.title + " has no compiled concepts yet."
	}
	return topic.title + " covers: " + strings.Join(titles, ", ") + "."
}

func summaryContent(doc SourceDocument, concepts []string) string {
	var builder strings.Builder
	builder.WriteString("# ")
	builder.WriteString(doc.Title)
	builder.WriteString("\n\nDeterministic summary compiled from the document outline and explicit facts.")
	if len(concepts) > 0 {
		builder.WriteString("\n\nKey concepts: ")
		builder.WriteString(strings.Join(concepts, ", "))
		builder.WriteString(".")
	}
	builder.WriteString("\n\nOutline:\n")
	outlineCount := 0
	for _, node := range doc.IR.Nodes {
		if node.Type != documentir.TypeHeading && node.Type != documentir.TypeSection {
			continue
		}
		if outlineCount >= 24 {
			break
		}
		builder.WriteString("- ")
		builder.WriteString(strings.TrimSpace(node.Text))
		builder.WriteString("\n")
		outlineCount++
	}
	return clip(builder.String(), maxSummaryCharacters)
}

func matchConceptKey(content string, node documentir.Node, concepts map[string]*conceptCandidate) string {
	normalized := normalizeConceptKey(content)
	best := ""
	bestLength := 0
	for key := range concepts {
		if key == "" || !strings.Contains(normalized, key) {
			continue
		}
		if len(key) > bestLength {
			best = key
			bestLength = len(key)
		}
	}
	if best != "" {
		return best
	}
	for _, heading := range node.HeadingPath {
		key := normalizeConceptKey(heading)
		if _, ok := concepts[key]; ok {
			return key
		}
	}
	return ""
}

func conceptTopicScore(conceptKey, topicKey string) int {
	conceptTokens := tokenSet(conceptKey)
	topicTokens := tokenSet(topicKey)
	score := 0
	for token := range conceptTokens {
		if topicTokens[token] {
			score++
		}
	}
	if strings.Contains(conceptKey, topicKey) || strings.Contains(topicKey, conceptKey) {
		score += 2
	}
	return score
}

func isExplicitFact(text string) bool {
	length := len([]rune(text))
	if length < 16 || length > 500 {
		return false
	}
	if strings.HasPrefix(strings.ToLower(text), "http://") || strings.HasPrefix(strings.ToLower(text), "https://") {
		return false
	}
	lower := strings.ToLower(text)
	english := []string{" is ", " are ", " was ", " were ", " uses ", " provides ", " requires ",
		" contains ", " returns ", " supports ", " enables ", " includes ", " means ",
		" belongs to ", " consists of ", " must ", " should ", " defaults to ", " can "}
	for _, marker := range english {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	chinese := []string{"是", "使用", "提供", "必须", "应该", "包括", "包含", "返回", "支持", "表示", "属于", "默认", "需要", "会"}
	for _, marker := range chinese {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return strings.Count(text, ":") == 1 && strings.Index(text, ":") > 2
}

func cleanFact(text string) string {
	return strings.TrimSpace(strings.Trim(strings.TrimSpace(text), " ;,;"))
}

func splitSentences(text string) []string {
	var out []string
	var current []rune
	for _, r := range text {
		switch r {
		case '.', '!', '?', '。', '！', '？', '\n':
			if len(current) > 0 {
				out = append(out, string(current))
				current = nil
			}
		default:
			current = append(current, r)
		}
	}
	if len(current) > 0 {
		out = append(out, string(current))
	}
	if len(out) == 0 {
		return []string{text}
	}
	return out
}

func normalizeConceptKey(value string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(value))), " ")
}

func normalizeFactKey(value string) string {
	var builder strings.Builder
	for _, r := range strings.ToLower(strings.Join(strings.Fields(value), " ")) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsSpace(r) {
			builder.WriteRune(r)
		}
	}
	return strings.Join(strings.Fields(builder.String()), " ")
}

func isUsefulConceptKey(value string) bool {
	length := len([]rune(value))
	return length >= 2 && length <= 96 && !strings.HasPrefix(value, "http")
}

func stableKey(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:16])
}

func anchorOf(node documentir.Node) map[string]any {
	if node.SourceAnchor == (documentir.SourceAnchor{}) {
		return nil
	}
	return map[string]any{
		"kind": node.SourceAnchor.Kind, "page": node.SourceAnchor.Page,
		"slide": node.SourceAnchor.Slide, "sheet": node.SourceAnchor.Sheet,
		"cell_range": node.SourceAnchor.CellRange, "section": node.SourceAnchor.Section,
	}
}

func anchorMap(ir *documentir.Document) map[string]any {
	if ir == nil || len(ir.Nodes) == 0 {
		return nil
	}
	return anchorOf(ir.Nodes[0])
}

func rootNodeID(ir *documentir.Document) string {
	if ir == nil || len(ir.Nodes) == 0 {
		return ""
	}
	return ir.Nodes[0].ID
}

func firstChunkID(doc SourceDocument, nodeID string) string {
	ids := doc.ChunkIDsByNode[nodeID]
	if len(ids) == 0 {
		return ""
	}
	sort.Strings(ids)
	return ids[0]
}

func containsDocument(sources []EvidenceSource, documentID string) bool {
	for _, source := range sources {
		if source.DocumentID == documentID {
			return true
		}
	}
	return false
}

func sortedMapKeys[T any](values map[string]T) []string {
	out := make([]string, 0, len(values))
	for key := range values {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func sortedSetKeys(values map[string]bool) []string {
	out := make([]string, 0, len(values))
	for key := range values {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func tokenSet(value string) map[string]bool {
	out := map[string]bool{}
	for _, token := range strings.Fields(value) {
		if len(token) > 1 {
			out[token] = true
		}
	}
	return out
}

func clip(value string, length int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= length {
		return strings.TrimSpace(value)
	}
	return strings.TrimSpace(string(runes[:length])) + "…"
}
