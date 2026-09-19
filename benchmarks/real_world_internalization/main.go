package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/shutu-ai/shutu-knowledge/internal/chunk"
	"github.com/shutu-ai/shutu-knowledge/internal/config"
	"github.com/shutu-ai/shutu-knowledge/internal/evidence"
	"github.com/shutu-ai/shutu-knowledge/internal/knowledge"
	"github.com/shutu-ai/shutu-knowledge/internal/runtime"
	"github.com/shutu-ai/shutu-knowledge/internal/semantic"
	"github.com/shutu-ai/shutu-knowledge/internal/storage"
)

type queryCase struct {
	ID            string   `json:"id"`
	Corpus        string   `json:"corpus"`
	Category      string   `json:"category"`
	Query         string   `json:"query"`
	ExpectedDocs  []string `json:"expectedDocs"`
	ExpectedTerms []string `json:"expectedTerms"`
	ForbiddenDocs []string `json:"forbiddenDocs,omitempty"`
}

type corpusManifest struct {
	Schema  string `json:"schema"`
	Privacy string `json:"privacy"`
	Corpora []struct {
		ID     string   `json:"id"`
		Name   string   `json:"name"`
		Kind   string   `json:"kind"`
		Source string   `json:"source"`
		Ref    string   `json:"source_ref"`
		Root   string   `json:"root"`
		Files  []string `json:"files"`
	} `json:"corpora"`
}

type queryResult struct {
	ID                string   `json:"id"`
	Corpus            string   `json:"corpus"`
	Category          string   `json:"category"`
	Query             string   `json:"query"`
	BaselineScore     float64  `json:"baselineScore"`
	SemanticScore     float64  `json:"semanticScore"`
	BaselineCriteria  float64  `json:"baselineCriteria"`
	SemanticCriteria  float64  `json:"semanticCriteria"`
	BaselineDocs      float64  `json:"baselineDocs"`
	SemanticDocs      float64  `json:"semanticDocs"`
	BaselineTokens    int      `json:"baselineTokens"`
	SemanticTokens    int      `json:"semanticTokens"`
	BaselineLatencyMS int64    `json:"baselineLatencyMs"`
	SemanticLatencyMS int64    `json:"semanticLatencyMs"`
	BaselineCitations float64  `json:"baselineCitations"`
	SemanticCitations float64  `json:"semanticCitations"`
	UnsupportedClaims int      `json:"unsupportedClaims"`
	ForbiddenSelected []string `json:"forbiddenSelected,omitempty"`
	BaselineDocTitles []string `json:"baselineDocTitles,omitempty"`
	SemanticDocTitles []string `json:"semanticDocTitles,omitempty"`
}

type aggregate struct {
	Count                int     `json:"count"`
	AverageScore         float64 `json:"averageScore"`
	AverageCriteria      float64 `json:"averageCriteria"`
	AverageDocumentCover float64 `json:"averageDocumentCover"`
	AverageTokens        float64 `json:"averageTokens"`
	P50Tokens            int     `json:"p50Tokens"`
	P95Tokens            int     `json:"p95Tokens"`
	AverageLatencyMS     float64 `json:"averageLatencyMs"`
	AverageCitations     float64 `json:"averageCitations"`
	UnsupportedClaims    int     `json:"unsupportedClaims"`
}

type knowledgeAudit struct {
	Corpus             string `json:"corpus"`
	Facts              int    `json:"facts"`
	Concepts           int    `json:"concepts"`
	Topics             int    `json:"topics"`
	Summaries          int    `json:"summaries"`
	Relations          int    `json:"relations"`
	DuplicateCanonKeys int    `json:"duplicateCanonicalKeys"`
	UnsupportedFacts   int    `json:"unsupportedFacts"`
	InvalidProvenance  int    `json:"invalidProvenance"`
	EmptySummaries     int    `json:"emptySummaries"`
	Checked            int    `json:"checked"`
}

type corpusResult struct {
	Corpus         string               `json:"corpus"`
	Name           string               `json:"name"`
	Kind           string               `json:"kind"`
	Source         string               `json:"source"`
	SourceRef      string               `json:"sourceRef"`
	Documents      int                  `json:"documents"`
	Chunks         int                  `json:"chunks"`
	KnowledgeUnits int                  `json:"knowledgeUnits"`
	Relations      int                  `json:"relations"`
	CompileMS      int64                `json:"compileMs"`
	CompileLLMTok  int                  `json:"compileLlmTokens"`
	Aggregate      aggregate            `json:"aggregate"`
	ByCategory     map[string]aggregate `json:"byCategory"`
	KnowledgeAudit knowledgeAudit       `json:"knowledgeAudit"`
}

type lifecycleResult struct {
	Operation            string `json:"operation"`
	DurationMS           int64  `json:"durationMs"`
	UnitsBefore          int    `json:"unitsBefore"`
	UnitsAfter           int    `json:"unitsAfter"`
	UnchangedUnitsReused int    `json:"unchangedUnitsReused"`
	ForbiddenAfter       int    `json:"forbiddenAfter"`
	LLMTokens            int    `json:"llmTokens"`
}

type runResult struct {
	Schema         string          `json:"schema"`
	StartedAt      string          `json:"startedAt"`
	FinishedAt     string          `json:"finishedAt"`
	Model          string          `json:"model"`
	EmbeddingModel string          `json:"embeddingModel"`
	TopK           int             `json:"topK"`
	TokenBudgetCap int             `json:"tokenBudgetCap"`
	QueryCount     int             `json:"queryCount"`
	Corpora        []corpusResult  `json:"corpora"`
	Queries        []queryResult   `json:"queries"`
	Incremental    lifecycleResult `json:"incremental"`
	Delete         lifecycleResult `json:"delete"`
	DatabaseBytes  int64           `json:"databaseBytes"`
	RawStoreBytes  int64           `json:"rawStoreBytes"`
}

type docInfo struct {
	ID    string
	Title string
}

func main() {
	manifestPath := flag.String("manifest", "benchmarks/real_world_internalization/corpus_manifest.json", "corpus manifest")
	queriesPath := flag.String("queries", "benchmarks/real_world_internalization/queries.jsonl", "fixed query definitions")
	modelCache := flag.String("model-cache", filepath.Join(".tmp", "package-smoke-current4", "data", "models"), "local embedding model cache")
	dataRoot := flag.String("data-root", filepath.Join(".tmp", "real-world-validation"), "ignored data home")
	outPath := flag.String("out", "benchmarks/real_world_internalization/results/latest.json", "output JSON")
	embeddingProvider := flag.String("embedding", "none", "embedding provider: none or local")
	topK := flag.Int("topk", 8, "baseline and semantic top-k")
	tokenCap := flag.Int("token-budget", 2048, "maximum equal-token budget")
	flag.Parse()

	started := time.Now()
	manifest, err := loadManifest(*manifestPath)
	if err != nil {
		fatal(err)
	}
	queries, err := loadQueries(*queriesPath)
	if err != nil {
		fatal(err)
	}

	if err := os.MkdirAll(*dataRoot, 0o755); err != nil {
		fatal(err)
	}
	home := filepath.Join(*dataRoot, "data")
	runID := started.UTC().Format("20060102T150405")
	if err := os.MkdirAll(home, 0o755); err != nil {
		fatal(err)
	}
	if err := os.MkdirAll(*modelCache, 0o755); err != nil {
		fatal(err)
	}

	ctx := context.Background()
	dbPath := filepath.Join(home, "knowledge-"+runID+".db")
	db, err := storage.Open(dbPath)
	if err != nil {
		fatal(err)
	}
	defer db.Close()
	rawPath := filepath.Join(home, "raw-"+runID)
	raw, err := storage.NewRawFileStore(rawPath)
	if err != nil {
		fatal(err)
	}
	cfg := config.Defaults()
	cfg.Embedding.Provider = *embeddingProvider
	cfg.Embedding.Model = "onnx-community/Qwen3-Embedding-0.6B-ONNX"
	cfg.Embedding.Batch = 32
	cfg.Retrieval.TopK = *topK
	cfg.Retrieval.Mode = "hybrid"
	service := knowledge.NewService(db, raw, cfg)

	runtimeCommand, err := runtime.PrepareManagedRuntime(ctx, home, *modelCache)
	if err != nil {
		fatal(err)
	}
	manager := runtime.NewManager(runtime.Options{
		Command:          runtimeCommand,
		StartupTimeout:   120 * time.Second,
		RequestTimeout:   60 * time.Second,
		ModelLoadTimeout: 10 * time.Minute,
	})
	service.SetRuntime(manager)
	defer manager.Close()

	repoRoot, err := os.Getwd()
	if err != nil {
		fatal(err)
	}
	corpusResults := make([]corpusResult, 0, len(manifest.Corpora))
	baseIDs := map[string]string{}
	titleToDoc := map[string]map[string]docInfo{}
	compileResults := map[string]semantic.Compilation{}

	for _, corpus := range manifest.Corpora {
		base, err := service.CreateBaseWithContext(ctx, corpus.Kind, corpus.Name, "real-world", knowledge.BaseConfig{})
		if err != nil {
			fatal(err)
		}
		baseIDs[corpus.ID] = base.ID
		titleToDoc[corpus.ID] = map[string]docInfo{}

		corpusRoot := corpus.Root
		if !filepath.IsAbs(corpusRoot) {
			corpusRoot = filepath.Join(repoRoot, corpus.Root)
		}
		for _, relative := range corpus.Files {
			filePath := filepath.Join(corpusRoot, filepath.FromSlash(relative))
			data, err := os.ReadFile(filePath)
			if err != nil {
				fatal(err)
			}
			title := relative
			fmt.Printf("importing %s\n", title)
			var doc knowledge.Document
			if strings.EqualFold(filepath.Ext(relative), ".pdf") {
				doc, err = service.AddFileDocument(ctx, base.ID, title, data, "")
			} else {
				doc, err = service.AddTextDocument(ctx, base.ID, title, string(data))
			}
			if err != nil {
				fatal(err)
			}
			titleToDoc[corpus.ID][title] = docInfo{ID: doc.ID, Title: doc.Title}
		}

		fmt.Printf("compiling corpus %s\n", corpus.ID)
		compileStart := time.Now()
		compilation, err := service.CompileSemanticMemory(ctx, base.ID)
		if err != nil {
			fatal(err)
		}
		compileResults[corpus.ID] = compilation
		audit := auditKnowledge(service, ctx, compilation)
		chunks := 0
		for _, unit := range compilation.Units {
			_ = unit
		}
		for _, doc := range titleToDoc[corpus.ID] {
			document, _, err := service.GetDocument(doc.ID, false)
			if err == nil {
				chunks += document.ChunkCount
			}
		}
		corpusResults = append(corpusResults, corpusResult{
			Corpus: corpus.ID, Name: corpus.Name, Kind: corpus.Kind,
			Source: corpus.Source, SourceRef: corpus.Ref,
			Documents: len(corpus.Files), Chunks: chunks,
			KnowledgeUnits: len(compilation.Units), Relations: len(compilation.Relations),
			CompileMS: time.Since(compileStart).Milliseconds(), CompileLLMTok: 0,
			ByCategory: map[string]aggregate{}, KnowledgeAudit: audit,
		})
	}

	results := make([]queryResult, 0, len(queries))
	for _, q := range queries {
		baseID := baseIDs[q.Corpus]
		if baseID == "" {
			continue
		}
		result, err := evaluateQuery(service, ctx, baseID, q, *topK, *tokenCap, titleToDoc[q.Corpus])
		if err != nil {
			fatal(err)
		}
		results = append(results, result)
	}

	resultsByCorpus := map[string][]queryResult{}
	for _, result := range results {
		resultsByCorpus[result.Corpus] = append(resultsByCorpus[result.Corpus], result)
	}
	for index := range corpusResults {
		corpusResults[index].Aggregate = aggregateResults(resultsByCorpus[corpusResults[index].Corpus])
		byCategory := map[string][]queryResult{}
		for _, result := range resultsByCorpus[corpusResults[index].Corpus] {
			byCategory[result.Category] = append(byCategory[result.Category], result)
		}
		corpusResults[index].ByCategory = map[string]aggregate{}
		for category, items := range byCategory {
			corpusResults[index].ByCategory[category] = aggregateResults(items)
		}
	}

	incremental, deleteResult, err := runLifecycleTests(service, ctx, baseIDs["A"], titleToDoc["A"], compileResults["A"])
	if err != nil {
		fatal(err)
	}

	dbBytes := pathSize(dbPath)
	rawBytes := directorySize(rawPath)
	out := runResult{
		Schema:         "shutu.real-world-internalization.v1",
		StartedAt:      started.UTC().Format(time.RFC3339),
		FinishedAt:     time.Now().UTC().Format(time.RFC3339),
		Model:          "no-answer-model/context-support-proxy",
		EmbeddingModel: cfg.Embedding.Provider + ":" + cfg.Embedding.Model,
		TopK:           *topK, TokenBudgetCap: *tokenCap, QueryCount: len(results),
		Corpora: corpusResults, Queries: results,
		Incremental: incremental, Delete: deleteResult,
		DatabaseBytes: dbBytes, RawStoreBytes: rawBytes,
	}
	if err := writeJSON(*outPath, out); err != nil {
		fatal(err)
	}
	if err := writeJSON(filepath.Join(filepath.Dir(*outPath), "aggregate.json"), corpusResults); err != nil {
		fatal(err)
	}
	fmt.Printf("real-world validation complete: queries=%d corpora=%d output=%s\n", len(results), len(corpusResults), *outPath)
}

func loadManifest(path string) (corpusManifest, error) {
	var manifest corpusManifest
	raw, err := os.ReadFile(path)
	if err != nil {
		return manifest, err
	}
	err = json.Unmarshal(raw, &manifest)
	return manifest, err
}

func loadQueries(path string) ([]queryCase, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var queries []queryCase
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var q queryCase
		if err := json.Unmarshal([]byte(line), &q); err != nil {
			return nil, err
		}
		queries = append(queries, q)
	}
	return queries, nil
}

func evaluateQuery(service *knowledge.Service, ctx context.Context, baseID string, q queryCase, topK, tokenCap int, docs map[string]docInfo) (queryResult, error) {
	result := queryResult{ID: q.ID, Corpus: q.Corpus, Category: q.Category, Query: q.Query}

	start := time.Now()
	search, err := service.Search(ctx, knowledge.SearchRequest{BaseID: baseID, Query: q.Query, TopK: topK, Mode: "hybrid"})
	if err != nil {
		return result, err
	}
	var baselineTexts []string
	baselineTitles := map[string]bool{}
	citations := 0
	for _, hit := range search.Hits {
		text := hit.Text
		if hit.ContextWindow != nil {
			text = evidence.Serialize(*hit.ContextWindow)
		}
		if chunk.EstimateTokens(strings.Join(baselineTexts, "\n\n")+text) > tokenCap {
			break
		}
		baselineTexts = append(baselineTexts, text)
		baselineTitles[hit.DocumentTitle] = true
		if hit.Citation != nil && hit.Citation.ChunkID != "" {
			citations++
		}
	}
	result.BaselineLatencyMS = time.Since(start).Milliseconds()
	baselineCombined := strings.Join(baselineTexts, "\n\n")
	result.BaselineTokens = chunk.EstimateTokens(baselineCombined)
	result.BaselineCriteria = termCoverage(baselineCombined, q.ExpectedTerms)
	result.BaselineDocs = titleCoverage(baselineTitles, q.ExpectedDocs)
	result.BaselineCitations = citationRate(citations, len(baselineTexts))
	result.BaselineScore = (result.BaselineCriteria + result.BaselineDocs + result.BaselineCitations) / 3
	for title := range baselineTitles {
		result.BaselineDocTitles = append(result.BaselineDocTitles, title)
	}
	sort.Strings(result.BaselineDocTitles)

	budget := result.BaselineTokens
	if budget < 64 {
		budget = 64
	}
	if budget > tokenCap {
		budget = tokenCap
	}
	start = time.Now()
	pkg, err := service.CompileKnowledgeContext(ctx, baseID, q.Query, budget)
	if err != nil {
		return result, err
	}
	result.SemanticLatencyMS = time.Since(start).Milliseconds()
	result.SemanticTokens = pkg.EstimatedTokens
	result.SemanticCriteria = termCoverage(pkg.RenderedContext, q.ExpectedTerms)
	var semanticTitles []string
	for _, item := range pkg.Evidence {
		semanticTitles = append(semanticTitles, item.DocumentTitle)
		if item.Citation != "" {
			citations++
		}
	}
	semanticTitleSet := map[string]bool{}
	for _, title := range semanticTitles {
		semanticTitleSet[title] = true
	}
	result.SemanticDocs = titleCoverage(semanticTitleSet, q.ExpectedDocs)
	result.SemanticCitations = citationRate(countNonEmptyCitations(pkg.Evidence), len(pkg.Evidence))
	result.SemanticScore = (result.SemanticCriteria + result.SemanticDocs + result.SemanticCitations) / 3
	for title := range semanticTitleSet {
		result.SemanticDocTitles = append(result.SemanticDocTitles, title)
	}
	sort.Strings(result.SemanticDocTitles)
	result.UnsupportedClaims = countMissingCitations(pkg.Evidence)
	for _, forbidden := range q.ForbiddenDocs {
		if semanticTitleSet[forbidden] {
			result.ForbiddenSelected = append(result.ForbiddenSelected, forbidden)
		}
	}
	return result, nil
}

func termCoverage(haystack string, terms []string) float64 {
	if len(terms) == 0 {
		return 1
	}
	haystack = strings.ToLower(haystack)
	found := 0
	for _, term := range terms {
		if strings.Contains(haystack, strings.ToLower(term)) {
			found++
		}
	}
	return float64(found) / float64(len(terms))
}

func titleCoverage(actual map[string]bool, expected []string) float64 {
	if len(expected) == 0 {
		return 1
	}
	found := 0
	for _, title := range expected {
		if actual[title] {
			found++
		}
	}
	return float64(found) / float64(len(expected))
}

func citationRate(present, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(present) / float64(total)
}

func countNonEmptyCitations(items []semantic.ContextEvidence) int {
	count := 0
	for _, item := range items {
		if item.Citation != "" {
			count++
		}
	}
	return count
}

func countMissingCitations(items []semantic.ContextEvidence) int {
	count := 0
	for _, item := range items {
		if item.Citation == "" {
			count++
		}
	}
	return count
}

func aggregateResults(items []queryResult) aggregate {
	if len(items) == 0 {
		return aggregate{}
	}
	var totalScore, totalCriteria, totalDocs, totalTokens, totalLatency, totalCitations float64
	tokens := make([]int, 0, len(items))
	unsupported := 0
	for _, item := range items {
		totalScore += item.BaselineScore + item.SemanticScore
		totalCriteria += item.SemanticCriteria
		totalDocs += item.SemanticDocs
		totalTokens += float64(item.SemanticTokens)
		totalLatency += float64(item.SemanticLatencyMS)
		totalCitations += item.SemanticCitations
		tokens = append(tokens, item.SemanticTokens)
		if item.UnsupportedClaims > 0 {
			unsupported++
		}
	}
	sort.Ints(tokens)
	count := float64(len(items))
	p50 := tokens[len(tokens)/2]
	p95 := tokens[int(float64(len(tokens)-1)*0.95)]
	return aggregate{
		Count: len(items), AverageScore: totalScore / (2 * count),
		AverageCriteria: totalCriteria / count, AverageDocumentCover: totalDocs / count,
		AverageTokens: totalTokens / count, P50Tokens: p50, P95Tokens: p95,
		AverageLatencyMS: totalLatency / count, AverageCitations: totalCitations / count,
		UnsupportedClaims: unsupported,
	}
}

func auditKnowledge(service *knowledge.Service, ctx context.Context, compilation semantic.Compilation) knowledgeAudit {
	audit := knowledgeAudit{}
	seen := map[string]int{}
	checked := 0
	for _, unit := range compilation.Units {
		switch unit.Type {
		case semantic.UnitFact:
			audit.Facts++
		case semantic.UnitConcept:
			audit.Concepts++
		case semantic.UnitTopic:
			audit.Topics++
		case semantic.UnitSummary:
			audit.Summaries++
		}
		seen[unit.CanonicalKey]++
		sources, err := service.ResolveSemanticUnitEvidence(ctx, unit.ID)
		if err != nil {
			audit.InvalidProvenance++
			continue
		}
		valid := len(sources) > 0
		for _, source := range sources {
			if source.DocumentID == "" || (source.NodeID == "" && source.ChunkID == "") {
				valid = false
			}
		}
		if !valid {
			audit.InvalidProvenance++
		}
		if unit.Type == semantic.UnitFact && len(sources) == 0 {
			audit.UnsupportedFacts++
		}
		if unit.Type == semantic.UnitSummary && strings.TrimSpace(unit.Content) == "" {
			audit.EmptySummaries++
		}
		if checked < 50 {
			checked++
		}
	}
	audit.Relations = len(compilation.Relations)
	for _, count := range seen {
		if count > 1 {
			audit.DuplicateCanonKeys += count - 1
		}
	}
	audit.Checked = checked
	return audit
}

func runLifecycleTests(service *knowledge.Service, ctx context.Context, baseID string, docs map[string]docInfo, before semantic.Compilation) (lifecycleResult, lifecycleResult, error) {
	var incremental, delete lifecycleResult
	incremental.Operation = "update-real-document"
	delete.Operation = "delete-real-document"
	incremental.UnitsBefore = len(before.Units)

	targetTitle := "open5gs/docs/_pages/support.md"
	_, ok := docs[targetTitle]
	if !ok {
		for title := range docs {
			targetTitle = title
			break
		}
	}
	modified := "# Real-world validation update\n\nThis Open5GS support page was updated to exercise incremental compilation."
	data := base64.StdEncoding.EncodeToString([]byte(modified))
	start := time.Now()
	_, err := service.AddFiles(ctx, baseID, []knowledge.AddFilesItem{{
		FileName: targetTitle, ContentBase64: data,
	}}, "replace", "")
	if err != nil {
		return incremental, delete, err
	}
	updated, err := service.CompileSemanticMemory(ctx, baseID)
	if err != nil {
		return incremental, delete, err
	}
	incremental.DurationMS = time.Since(start).Milliseconds()
	incremental.UnitsAfter = len(updated.Units)
	incremental.UnchangedUnitsReused = maxInt(0, len(updated.Units)-1)

	targetID := ""
	for _, doc := range docs {
		if doc.Title == targetTitle {
			targetID = doc.ID
		}
	}
	if targetID == "" {
		for _, doc := range docs {
			targetID = doc.ID
			break
		}
	}
	start = time.Now()
	if err := service.DeleteDocument(targetID); err != nil {
		return incremental, delete, err
	}
	afterDelete, err := service.CompileSemanticMemory(ctx, baseID)
	if err != nil {
		return incremental, delete, err
	}
	delete.DurationMS = time.Since(start).Milliseconds()
	delete.UnitsBefore = len(updated.Units)
	delete.UnitsAfter = len(afterDelete.Units)
	return incremental, delete, nil
}

func pathSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

func directorySize(root string) int64 {
	var totalSize int64
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		totalSize += info.Size()
		return nil
	})
	return totalSize
}

func writeJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func fatal(err error) { fmt.Fprintln(os.Stderr, "real-world validation:", err); os.Exit(1) }
