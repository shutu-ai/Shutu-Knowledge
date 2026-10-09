package eval

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/shutu-ai/shutu-knowledge/internal/agentquery"
	"github.com/shutu-ai/shutu-knowledge/internal/config"
	"github.com/shutu-ai/shutu-knowledge/internal/knowledge"
	"github.com/shutu-ai/shutu-knowledge/internal/semantic"
	"github.com/shutu-ai/shutu-knowledge/internal/storage"
	"github.com/shutu-ai/shutu-knowledge/internal/version"
)

// Options configures one harness run.
type Options struct {
	// FixtureDir holds corpus/ (documents) and gold.jsonl.
	FixtureDir string
	// Mode is the default agentquery mode; gold entries may pin their own.
	Mode string
	// TopK is the retrieval depth used for recall computation.
	TopK int
	// Label identifies the arm in comparisons (for example "evidence-a").
	Label string
	// OutDir receives the frozen report files.
	OutDir string
}

// Fixture binds one loaded corpus to the identities the harness needs.
type Fixture struct {
	Service      *knowledge.Service
	BaseID       string
	DocIDByTitle map[string]string
}

// QuestionResult is the per-question harness record (one traces.jsonl line).
type QuestionResult struct {
	QuestionID      string   `json:"question_id"`
	Category        string   `json:"category"`
	Mode            string   `json:"mode"`
	ExpectedAbstain bool     `json:"expected_abstain"`
	HitDocuments    []string `json:"hit_documents"`
	MatchedLocators int      `json:"matched_locators"`
	TotalLocators   int      `json:"total_locators"`
	Recall          float64  `json:"recall"`
	FullCoverage    bool     `json:"full_coverage"`
	Abstained       bool     `json:"abstained"`
	Correct         bool     `json:"correct"`
	Degraded        bool     `json:"degraded"`
	Warnings        []string `json:"warnings,omitempty"`
	ElapsedMS       int64    `json:"elapsed_ms"`
	SemanticGen     int64    `json:"semantic_generation,omitempty"`
	TopScore        float64  `json:"top_score,omitempty"`
	Error           string   `json:"error,omitempty"`
}

// RunReport aggregates one run.
type RunReport struct {
	Label         string           `json:"label"`
	Mode          string           `json:"mode"`
	TopK          int              `json:"top_k"`
	Version       string           `json:"version"`
	GitCommit     string           `json:"git_commit,omitempty"`
	GoldQuestions int              `json:"gold_questions"`
	Timestamp     string           `json:"timestamp"`
	PerQuestion   []QuestionResult `json:"per_question"`
	Summary       Summary          `json:"summary"`
}

// Summary holds the aggregate metrics (plan §12.4 retrieval subset).
type Summary struct {
	EvidenceRecall   float64       `json:"evidence_recall"`
	FullCoverageRate float64       `json:"full_coverage_rate"`
	AbstainAccuracy  float64       `json:"abstain_accuracy"`
	AnswerAccuracy   float64       `json:"answer_accuracy"`
	MeanLatencyMS    float64       `json:"mean_latency_ms"`
	P95LatencyMS     int64         `json:"p95_latency_ms"`
	DegradedRuns     int           `json:"degraded_runs"`
	ByCategory       []CategoryRow `json:"by_category"`
}

// CategoryRow is one per-category aggregate.
type CategoryRow struct {
	Category       string  `json:"category"`
	Questions      int     `json:"questions"`
	EvidenceRecall float64 `json:"evidence_recall"`
	CorrectRate    float64 `json:"correct_rate"`
}

// LoadFixture builds an isolated knowledge service from fixture corpus files
// and compiles semantic memory, mirroring what an operator does before eval.
func LoadFixture(ctx context.Context, fixtureDir string) (*Fixture, error) {
	docIDByTitle := map[string]string{}
	corpusDir := filepath.Join(fixtureDir, "corpus")
	entries, err := os.ReadDir(corpusDir)
	if err != nil {
		return nil, fmt.Errorf("read corpus: %w", err)
	}
	home, err := os.MkdirTemp("", "shutu-eval-home-")
	if err != nil {
		return nil, err
	}
	db, err := storage.Open(filepath.Join(home, "knowledge.db"))
	if err != nil {
		return nil, err
	}
	raw, err := storage.NewRawFileStore(filepath.Join(home, "raw"))
	if err != nil {
		return nil, err
	}
	service := knowledge.NewService(db, raw, config.Defaults())
	base, err := service.CreateBase("Eval Fixture", "frozen eval corpus", "", knowledge.BaseConfig{})
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		content, err := os.ReadFile(filepath.Join(corpusDir, entry.Name()))
		if err != nil {
			return nil, err
		}
		title := strings.TrimSuffix(entry.Name(), ".md")
		document, err := service.AddTextDocument(ctx, base.ID, title, string(content))
		if err != nil {
			return nil, err
		}
		docIDByTitle[title] = document.ID
	}
	if _, err := service.CompileSemanticMemory(ctx, base.ID); err != nil {
		return nil, err
	}
	return &Fixture{Service: service, BaseID: base.ID, DocIDByTitle: docIDByTitle}, nil
}

// Run replays the gold set through the shared query layer and returns the
// aggregate report. It is deterministic for a frozen fixture.
func Run(ctx context.Context, fixture *Fixture, questions []GoldQuestion, options Options) (*RunReport, error) {
	agentQuery := agentquery.New(fixture.Service)
	mode := options.Mode
	if mode == "" {
		mode = "evidence"
	}
	topK := options.TopK
	if topK <= 0 {
		topK = 10
	}
	report := &RunReport{
		Label: options.Label, Mode: mode, TopK: topK,
		Version:       version.Current().Version,
		GoldQuestions: len(questions),
		Timestamp:     time.Now().UTC().Format(time.RFC3339),
		PerQuestion:   make([]QuestionResult, 0, len(questions)),
	}
	if commit, err := gitCommit(); err == nil {
		report.GitCommit = commit
	}
	for _, question := range questions {
		report.PerQuestion = append(report.PerQuestion,
			runQuestion(ctx, agentQuery, fixture, question, mode, topK))
	}
	summarize(report)
	return report, nil
}

func runQuestion(ctx context.Context, agentQuery *agentquery.Service, fixture *Fixture, question GoldQuestion, defaultMode string, topK int) QuestionResult {
	// The global mode decides the lane so A/B comparisons stay pure; the gold
	// set's per-question mode field is advisory metadata for the annotator.
	mode := defaultMode
	result := QuestionResult{
		QuestionID: question.QuestionID, Category: question.Category, Mode: mode,
		ExpectedAbstain: question.ExpectedAbstain,
		TotalLocators:   len(question.GoldEvidenceLocators),
	}
	startedAt := time.Now()
	defer func() { result.ElapsedMS = time.Since(startedAt).Milliseconds() }()

	search, err := agentQuery.Search(ctx, agentquery.Request{
		Query: question.Question, BaseIDs: []string{fixture.BaseID},
		Mode: agentquery.Mode(mode), TopK: topK,
	})
	result.Degraded = search.Degraded
	for _, diagnostic := range search.Diagnostics {
		if diagnostic.Code != "" {
			result.Warnings = append(result.Warnings, diagnostic.Code)
		}
	}
	if len(search.Snapshots) > 0 {
		result.SemanticGen = search.Snapshots[0].SemanticGeneration
	}
	if err != nil {
		result.Error = err.Error()
		result.Abstained = true
		return result
	}

	seenDocuments := map[string]bool{}
	if search.Evidence != nil {
		for _, hit := range search.Evidence.Hits {
			seenDocuments[hit.DocID] = true
		}
	}
	for docID := range seenDocuments {
		result.HitDocuments = append(result.HitDocuments, docID)
	}
	sort.Strings(result.HitDocuments)
	result.MatchedLocators = countMatchedLocators(search, fixture, question.GoldEvidenceLocators)
	if result.TotalLocators > 0 {
		result.Recall = float64(result.MatchedLocators) / float64(result.TotalLocators)
		result.FullCoverage = result.MatchedLocators == result.TotalLocators
	}
	result.TopScore = topScore(search)
	// Zero-score hits are noise, not evidence: abstention requires at least
	// one positively scored hit.
	result.Abstained = result.TopScore <= 0
	// Correctness: answerable questions must retrieve all gold evidence;
	// abstain questions must abstain.
	result.Correct = result.Abstained == question.ExpectedAbstain &&
		(question.ExpectedAbstain || result.FullCoverage)
	return result
}

func topScore(search agentquery.Result) float64 {
	top := 0.0
	if search.Evidence != nil {
		for _, hit := range search.Evidence.Hits {
			if hit.Score > top {
				top = hit.Score
			}
		}
	}
	for _, base := range search.Semantic {
		for _, hit := range base.Response.Hits {
			if hit.Score > top {
				top = hit.Score
			}
		}
	}
	return top
}

// countMatchedLocators checks gold evidence against retrieved content. The
// evidence lane matches needles inside chunk text; the semantic lane matches
// needles inside compiled unit titles/content and resolves the document via
// the fixture title map, never by guessing.
func countMatchedLocators(search agentquery.Result, fixture *Fixture, locators []EvidenceLocator) int {
	matched := 0
	for _, locator := range locators {
		if locatorMatches(search, fixture, locator) {
			matched++
		}
	}
	return matched
}

func locatorMatches(search agentquery.Result, fixture *Fixture, locator EvidenceLocator) bool {
	wantDocID := fixture.DocIDByTitle[strings.TrimSuffix(locator.Document, ".md")]
	if search.Evidence != nil {
		for _, hit := range search.Evidence.Hits {
			if !documentMatches(hit.DocumentTitle, hit.DocID, locator.Document, wantDocID) {
				continue
			}
			if strings.Contains(hit.Text, locator.Needle) {
				return true
			}
		}
	}
	for _, base := range search.Semantic {
		for _, hit := range base.Response.Hits {
			if !semanticUnitMatchesDocument(hit.Unit, fixture, locator.Document, wantDocID) {
				continue
			}
			if strings.Contains(hit.Unit.Title, locator.Needle) ||
				strings.Contains(hit.Unit.Content, locator.Needle) {
				return true
			}
		}
	}
	return false
}

func semanticUnitMatchesDocument(unit semantic.Unit, fixture *Fixture, document string, wantDocID string) bool {
	if wantDocID != "" {
		for _, source := range unit.Sources {
			if source.DocumentID == wantDocID {
				return true
			}
		}
		return false
	}
	title := strings.TrimSuffix(document, ".md")
	for _, source := range unit.Sources {
		if source.DocumentID != "" && fixture.DocIDByTitle[title] == source.DocumentID {
			return true
		}
	}
	return false
}

func documentMatches(title, docID, want string, wantDocID string) bool {
	if wantDocID != "" {
		return docID == wantDocID
	}
	if docID == want || title == want {
		return true
	}
	normalized := strings.ToLower(strings.TrimSuffix(want, ".md"))
	return strings.Contains(strings.ToLower(title), normalized)
}

func summarize(report *RunReport) {
	total := len(report.PerQuestion)
	if total == 0 {
		return
	}
	recallSum, coverage, abstainCorrect, answerCorrect := 0.0, 0, 0, 0
	latencies := make([]int64, 0, total)
	var latencySum int64
	byCategory := map[string]*CategoryRow{}
	order := []string{}
	for _, result := range report.PerQuestion {
		recallSum += result.Recall
		if result.FullCoverage {
			coverage++
		}
		if result.Abstained == result.ExpectedAbstain {
			abstainCorrect++
		}
		if result.Correct {
			answerCorrect++
		}
		latencies = append(latencies, result.ElapsedMS)
		latencySum += result.ElapsedMS
		if result.Degraded {
			report.Summary.DegradedRuns++
		}
		row, ok := byCategory[result.Category]
		if !ok {
			row = &CategoryRow{Category: result.Category}
			byCategory[result.Category] = row
			order = append(order, result.Category)
		}
		row.Questions++
		row.EvidenceRecall += result.Recall
		if result.Correct {
			row.CorrectRate++
		}
	}
	report.Summary.EvidenceRecall = recallSum / float64(total)
	report.Summary.FullCoverageRate = float64(coverage) / float64(total)
	report.Summary.AbstainAccuracy = float64(abstainCorrect) / float64(total)
	report.Summary.AnswerAccuracy = float64(answerCorrect) / float64(total)
	report.Summary.MeanLatencyMS = float64(latencySum) / float64(total)
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	p95Index := int(float64(len(latencies))*0.95) - 1
	if p95Index < 0 {
		p95Index = 0
	}
	if p95Index >= len(latencies) {
		p95Index = len(latencies) - 1
	}
	report.Summary.P95LatencyMS = latencies[p95Index]
	sort.Strings(order)
	for _, category := range order {
		row := byCategory[category]
		row.EvidenceRecall /= float64(row.Questions)
		row.CorrectRate /= float64(row.Questions)
		report.Summary.ByCategory = append(report.Summary.ByCategory, *row)
	}
}
