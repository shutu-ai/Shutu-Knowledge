// Command retrieval-regression runs a fixed, metadata-only retrieval corpus
// against a running Knowledge server. Query text is read for the request but
// never written to the report; response bodies are used in memory only.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const maxResponseBytes = 16 << 20

type corpus struct {
	SchemaVersion int        `json:"schemaVersion"`
	Cases         []testCase `json:"cases"`
}

type testCase struct {
	ID             string            `json:"id"`
	Query          string            `json:"query"`
	BaseID         string            `json:"baseId,omitempty"`
	Mode           string            `json:"mode,omitempty"`
	TopK           int               `json:"topK,omitempty"`
	MMR            bool              `json:"mmr,omitempty"`
	ExpectedDocIDs []string          `json:"expectedDocIds,omitempty"`
	NegativeDocIDs []string          `json:"negativeDocIds,omitempty"`
	Context        bool              `json:"context,omitempty"`
	RawCitation    bool              `json:"rawCitation,omitempty"`
	ModelProfile   map[string]string `json:"modelProfile,omitempty"`
}

type hitEvidence struct {
	Rank            int     `json:"rank"`
	ChunkID         string  `json:"chunkId,omitempty"`
	DocID           string  `json:"docId"`
	BaseID          string  `json:"baseId"`
	Index           int     `json:"index"`
	IndexGeneration int64   `json:"indexGeneration"`
	SourceVersion   int64   `json:"sourceVersion"`
	Score           float64 `json:"score"`
	VectorScore     float64 `json:"vectorScore,omitempty"`
	LexicalScore    float64 `json:"lexicalScore,omitempty"`
	FusionScore     float64 `json:"fusionScore,omitempty"`
	RerankScore     float64 `json:"rerankScore,omitempty"`
}

type generationEvidence struct {
	DocID           string `json:"docId"`
	BaseID          string `json:"baseId"`
	SourceVersion   int64  `json:"sourceVersion"`
	IndexGeneration int64  `json:"indexGeneration"`
}

type diagnosticEvidence struct {
	Stage       string  `json:"stage"`
	ChunkID     string  `json:"chunkId,omitempty"`
	DocID       string  `json:"docId"`
	BaseID      string  `json:"baseId"`
	BM25Rank    int     `json:"bm25Rank,omitempty"`
	VectorRank  int     `json:"vectorRank,omitempty"`
	FinalRank   int     `json:"finalRank,omitempty"`
	BM25Score   float64 `json:"bm25Score,omitempty"`
	VectorScore float64 `json:"vectorScore,omitempty"`
	RRFScore    float64 `json:"rrfScore,omitempty"`
	RerankScore float64 `json:"rerankScore,omitempty"`
	MMRScore    float64 `json:"mmrScore,omitempty"`
}

type dependentCheck struct {
	Requested     bool     `json:"requested"`
	Outcome       string   `json:"outcome"` // passed | failed | unrun
	HTTPStatus    int      `json:"httpStatus,omitempty"`
	DurationMS    int64    `json:"durationMs,omitempty"`
	BytesObserved int64    `json:"bytesObserved,omitempty"`
	Generation    int64    `json:"indexGeneration,omitempty"`
	SourceVersion int64    `json:"sourceVersion,omitempty"`
	ResponseKeys  []string `json:"responseKeys,omitempty"`
	Reason        string   `json:"reason,omitempty"`
}

type caseReport struct {
	ID               string               `json:"id"`
	QuerySHA256      string               `json:"querySha256"`
	BaseID           string               `json:"baseId"`
	Mode             string               `json:"mode"`
	TopK             int                  `json:"topK"`
	MMR              bool                 `json:"mmr"`
	ModelProfile     map[string]string    `json:"modelProfile,omitempty"`
	Outcome          string               `json:"outcome"`
	HTTPStatus       int                  `json:"httpStatus,omitempty"`
	DurationMS       int64                `json:"durationMs,omitempty"`
	Total            int                  `json:"total,omitempty"`
	Hits             []hitEvidence        `json:"hits,omitempty"`
	Generations      []generationEvidence `json:"generations,omitempty"`
	Rerank           rerankEvidence       `json:"rerank,omitempty"`
	DiagnosticCounts map[string]int       `json:"diagnosticCounts,omitempty"`
	Diagnostics      []diagnosticEvidence `json:"diagnostics,omitempty"`
	Expected         map[string]int       `json:"expectedRanks,omitempty"`
	NegativeHits     []string             `json:"negativeHits,omitempty"`
	HitAt1           float64              `json:"hitAt1"`
	HitAt3           float64              `json:"hitAt3"`
	MRR              float64              `json:"mrr"`
	Context          dependentCheck       `json:"context"`
	RawCitation      dependentCheck       `json:"rawCitation"`
	Error            string               `json:"error,omitempty"`
}

type rerankEvidence struct {
	Provider       string `json:"provider,omitempty"`
	Model          string `json:"model,omitempty"`
	Status         string `json:"status,omitempty"`
	Attempted      bool   `json:"attempted"`
	Applied        bool   `json:"applied"`
	CandidateCount int    `json:"candidateCount,omitempty"`
	ElapsedMS      int64  `json:"elapsedMs,omitempty"`
}

type report struct {
	SchemaVersion     int          `json:"schemaVersion"`
	Status            string       `json:"status"` // passed | failed | incomplete
	StartedAt         string       `json:"startedAt"`
	FinishedAt        string       `json:"finishedAt"`
	ReportFingerprint string       `json:"reportFingerprint"`
	BaseURL           string       `json:"baseUrl"`
	InputFile         string       `json:"inputFile"`
	InputSHA256       string       `json:"inputSha256"`
	CaseCount         int          `json:"caseCount"`
	Cases             []caseReport `json:"cases"`
	Errors            []string     `json:"errors,omitempty"`
}

type searchResponse struct {
	Query       string               `json:"query"`
	Mode        string               `json:"mode"`
	Total       int                  `json:"total"`
	Rerank      rerankEvidence       `json:"rerank"`
	Generations []generationEvidence `json:"generations"`
	Hits        []hitEvidence        `json:"hits"`
	Diagnostics *diagnosticResponse  `json:"diagnostics"`
}

type diagnosticResponse struct {
	BM25      []diagnosticCandidate `json:"bm25"`
	Vector    []diagnosticCandidate `json:"vector"`
	RRF       []diagnosticCandidate `json:"rrf"`
	Rerank    []diagnosticCandidate `json:"rerank"`
	MMRInput  []diagnosticCandidate `json:"mmrInput"`
	MMROutput []diagnosticCandidate `json:"mmrOutput"`
	Final     []diagnosticCandidate `json:"final"`
}

type diagnosticCandidate struct {
	ChunkID     string  `json:"chunkId"`
	DocID       string  `json:"docId"`
	BaseID      string  `json:"baseId"`
	BM25Rank    int     `json:"bm25Rank"`
	VectorRank  int     `json:"vectorRank"`
	FinalRank   int     `json:"finalRank"`
	BM25Score   float64 `json:"bm25Score"`
	VectorScore float64 `json:"vectorScore"`
	RRFScore    float64 `json:"rrfScore"`
	RerankScore float64 `json:"rerankScore"`
	MMRScore    float64 `json:"mmrScore"`
}

type client struct {
	baseURL string
	http    *http.Client
	timeout time.Duration
}

func main() {
	baseURL := flag.String("base-url", "", "running Knowledge server URL")
	baseID := flag.String("base-id", "", "default knowledge base ID")
	input := flag.String("input", "", "fixed retrieval corpus JSON")
	output := flag.String("output", "", "metadata-only report JSON")
	timeout := flag.Duration("request-timeout", 30*time.Second, "per-request timeout")
	flag.Parse()
	if strings.TrimSpace(*baseURL) == "" || strings.TrimSpace(*input) == "" || strings.TrimSpace(*output) == "" {
		fatal("-base-url, -input, and -output are required")
	}
	if *timeout <= 0 {
		fatal("-request-timeout must be positive")
	}
	rawURL, safeURL, err := normalizeBaseURL(*baseURL)
	if err != nil {
		fatal(err.Error())
	}
	data, err := os.ReadFile(*input)
	if err != nil {
		fatal(fmt.Sprintf("read input: %v", err))
	}
	var inputCorpus corpus
	if err := json.Unmarshal(data, &inputCorpus); err != nil {
		fatal(fmt.Sprintf("decode input: %v", err))
	}
	if inputCorpus.SchemaVersion != 1 || len(inputCorpus.Cases) == 0 {
		fatal("input schemaVersion must be 1 and cases must be non-empty")
	}
	for i := range inputCorpus.Cases {
		if err := validateCase(&inputCorpus.Cases[i], *baseID); err != nil {
			fatal(fmt.Sprintf("case %d: %v", i, err))
		}
	}
	caseIDs := map[string]bool{}
	for _, test := range inputCorpus.Cases {
		if caseIDs[test.ID] {
			fatal(fmt.Sprintf("duplicate case id %q", test.ID))
		}
		caseIDs[test.ID] = true
	}
	started := time.Now().UTC()
	reportValue := report{SchemaVersion: 1, Status: "passed", StartedAt: started.Format(time.RFC3339Nano), BaseURL: safeURL, InputFile: filepath.Base(*input), InputSHA256: sha256HexBytes(data), CaseCount: len(inputCorpus.Cases)}
	c := &client{baseURL: rawURL, http: &http.Client{}, timeout: *timeout}
	for _, test := range inputCorpus.Cases {
		result := c.runCase(context.Background(), test, *baseID)
		reportValue.Cases = append(reportValue.Cases, result)
		if result.Outcome == "failed" {
			reportValue.Status = "failed"
		}
		if result.Outcome == "incomplete" && reportValue.Status == "passed" {
			reportValue.Status = "incomplete"
		}
	}
	reportValue.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
	reportValue.ReportFingerprint = fingerprint(reportValue)
	if err := writeReport(*output, reportValue); err != nil {
		fatal(err.Error())
	}
	if reportValue.Status != "passed" {
		fmt.Fprintf(os.Stderr, "retrieval report written with status=%s: %s\n", reportValue.Status, *output)
		os.Exit(1)
	}
	fmt.Printf("retrieval report written: %s cases=%d fingerprint=%s\n", *output, len(reportValue.Cases), reportValue.ReportFingerprint)
}

func fatal(message string) { fmt.Fprintln(os.Stderr, "error:", message); os.Exit(2) }

func normalizeBaseURL(raw string) (string, string, error) {
	requestURL := strings.TrimRight(strings.TrimSpace(raw), "/")
	parsed, err := url.Parse(requestURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", "", errors.New("-base-url must be an absolute http(s) URL")
	}
	parsed.User, parsed.RawQuery, parsed.Fragment = nil, "", ""
	return requestURL, strings.TrimRight(parsed.String(), "/"), nil
}

func validateCase(test *testCase, defaultBase string) error {
	test.ID = strings.TrimSpace(test.ID)
	test.Query = strings.TrimSpace(test.Query)
	test.BaseID = strings.TrimSpace(test.BaseID)
	if test.BaseID == "" {
		test.BaseID = defaultBase
	}
	if test.ID == "" || test.Query == "" || test.BaseID == "" {
		return errors.New("id, query, and baseId are required (baseId may come from -base-id)")
	}
	if test.Mode == "" {
		test.Mode = "hybrid"
	}
	if test.Mode != "auto" && test.Mode != "hybrid" && test.Mode != "lexical" && test.Mode != "vector" {
		return fmt.Errorf("mode %q must be auto, hybrid, lexical, or vector", test.Mode)
	}
	if test.TopK <= 0 {
		test.TopK = 10
	}
	if test.TopK > 50 {
		return errors.New("topK must be <= 50")
	}
	return nil
}

func (c *client) runCase(ctx context.Context, test testCase, defaultBase string) caseReport {
	result := caseReport{ID: test.ID, QuerySHA256: sha256HexBytes([]byte(test.Query)), BaseID: test.BaseID, Mode: test.Mode, TopK: test.TopK, MMR: test.MMR, ModelProfile: sanitizeModelProfile(test.ModelProfile), Expected: map[string]int{}, Context: dependentCheck{Outcome: "unrun"}, RawCitation: dependentCheck{Outcome: "unrun"}}
	started := time.Now()
	payload, status, body, _, err := c.do(ctx, http.MethodPost, "/api/search", test.BaseID, map[string]any{"query": test.Query, "baseId": test.BaseID, "topK": test.TopK, "mode": test.Mode, "mmr": test.MMR, "debug": true})
	result.HTTPStatus, result.DurationMS = status, time.Since(started).Milliseconds()
	if err != nil {
		result.Outcome, result.Error = "failed", err.Error()
		return result
	}
	value := unwrap(payload)
	encoded, _ := json.Marshal(value)
	var search searchResponse
	if err := json.Unmarshal(encoded, &search); err != nil {
		result.Outcome, result.Error = "failed", "search response contract: "+err.Error()
		return result
	}
	result.Total, result.Hits, result.Generations, result.Rerank = search.Total, search.Hits, search.Generations, search.Rerank
	if result.ModelProfile == nil {
		result.ModelProfile = map[string]string{}
	}
	if result.Rerank.Provider != "" {
		result.ModelProfile["rerankProvider"] = result.Rerank.Provider
	}
	if result.Rerank.Model != "" {
		result.ModelProfile["rerankModel"] = result.Rerank.Model
	}
	result.DiagnosticCounts, result.Diagnostics = diagnostics(search.Diagnostics)
	for rank := range result.Hits {
		result.Hits[rank].Rank = rank + 1
	}
	for _, expected := range unique(test.ExpectedDocIDs) {
		result.Expected[expected] = rankOf(result.Hits, expected)
	}
	for _, expected := range unique(test.ExpectedDocIDs) {
		if result.Expected[expected] == 0 {
			result.Outcome = "failed"
		}
	}
	for _, negative := range unique(test.NegativeDocIDs) {
		if rank := rankOf(result.Hits, negative); rank > 0 {
			result.NegativeHits = append(result.NegativeHits, negative)
		}
	}
	if len(result.NegativeHits) > 0 {
		result.Outcome = "failed"
	}
	result.HitAt1, result.HitAt3, result.MRR = retrievalScores(result.Expected, len(result.Hits))
	if result.Outcome == "" {
		result.Outcome = "passed"
	}
	if len(result.Hits) > 0 {
		anchor := result.Hits[0]
		if test.Context {
			result.Context = c.checkDependent(ctx, "/api/documents/"+url.PathEscape(anchor.DocID)+"/context?anchorChunkId="+url.QueryEscape(anchor.ChunkID)+"&indexGeneration="+fmt.Sprint(anchor.IndexGeneration)+"&sourceVersion="+fmt.Sprint(anchor.SourceVersion)+"&before=2&after=2&maxTokens=768", test.BaseID, false)
		}
		if test.RawCitation {
			result.RawCitation = c.checkDependent(ctx, "/api/documents/"+url.PathEscape(anchor.DocID)+"/raw?indexGeneration="+fmt.Sprint(anchor.IndexGeneration)+"&sourceVersion="+fmt.Sprint(anchor.SourceVersion)+"&inline=1", test.BaseID, true)
		}
	} else {
		if test.Context {
			result.Context = dependentCheck{Requested: true, Outcome: "unrun", Reason: "no search hit available as anchor"}
		}
		if test.RawCitation {
			result.RawCitation = dependentCheck{Requested: true, Outcome: "unrun", Reason: "no search hit available as anchor"}
		}
	}
	if result.Context.Outcome == "failed" || result.RawCitation.Outcome == "failed" {
		result.Outcome = "failed"
	}
	_ = body // response bytes are intentionally not retained after parsing
	return result
}

func (c *client) checkDependent(ctx context.Context, path, baseID string, binary bool) dependentCheck {
	started := time.Now()
	payload, status, body, headers, err := c.do(ctx, http.MethodGet, path, baseID, nil)
	check := dependentCheck{Requested: true, HTTPStatus: status, DurationMS: time.Since(started).Milliseconds(), BytesObserved: int64(len(body))}
	if generation, err := strconv.ParseInt(headers.Get("X-Index-Generation"), 10, 64); err == nil {
		check.Generation = generation
	}
	if version, err := strconv.ParseInt(headers.Get("X-Source-Version"), 10, 64); err == nil {
		check.SourceVersion = version
	}
	if err != nil {
		check.Outcome, check.Reason = "failed", err.Error()
		return check
	}
	if binary {
		if check.Generation == 0 || check.SourceVersion == 0 {
			check.Outcome, check.Reason = "failed", "raw citation response omitted generation/source headers"
			return check
		}
	} else if _, ok := unwrap(payload).(map[string]any); !ok {
		check.Outcome, check.Reason = "failed", "context response contract is not an object"
		return check
	}
	check.Outcome = "passed"
	if !binary {
		check.ResponseKeys = topLevelKeys(unwrap(payload))
	}
	return check
}

// do returns the decoded envelope, status, and in-memory response bytes. The
// caller may inspect metadata, but never writes body/content to the report.
func (c *client) do(parent context.Context, method, path, baseID string, body any) (any, int, []byte, http.Header, error) {
	ctx, cancel := context.WithTimeout(parent, c.timeout)
	defer cancel()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, 0, nil, nil, err
		}
		reader = strings.NewReader(string(encoded))
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return nil, 0, nil, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, nil, nil, err
	}
	defer resp.Body.Close()
	data, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if len(data) > maxResponseBytes {
		return nil, resp.StatusCode, data[:maxResponseBytes], resp.Header, errors.New("response exceeds metadata runner limit")
	}
	var payload any
	if len(data) > 0 {
		_ = json.Unmarshal(data, &payload)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return payload, resp.StatusCode, data, resp.Header, apiError(payload, resp.StatusCode, readErr)
	}
	if readErr != nil {
		return payload, resp.StatusCode, data, resp.Header, readErr
	}
	return payload, resp.StatusCode, data, resp.Header, nil
}

func apiError(payload any, status int, readErr error) error {
	if readErr != nil {
		return readErr
	}
	if object, ok := payload.(map[string]any); ok {
		if e, ok := object["error"].(map[string]any); ok {
			if code, ok := e["code"].(string); ok && code != "" {
				return fmt.Errorf("HTTP %d (%s)", status, code)
			}
		}
	}
	return fmt.Errorf("HTTP %d", status)
}

func unwrap(value any) any {
	if object, ok := value.(map[string]any); ok {
		if nested, exists := object["value"]; exists {
			return nested
		}
	}
	return value
}
func rankOf(hits []hitEvidence, docID string) int {
	for _, hit := range hits {
		if hit.DocID == docID {
			return hit.Rank
		}
	}
	return 0
}
func unique(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}

func sanitizeModelProfile(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	allowed := map[string]bool{"embeddingProvider": true, "embeddingModel": true, "embeddingDimension": true, "rerankProvider": true, "rerankModel": true}
	copyValues := make(map[string]string, len(values))
	for key, value := range values {
		if allowed[key] && strings.TrimSpace(value) != "" {
			copyValues[key] = strings.TrimSpace(value)
		}
	}
	if len(copyValues) == 0 {
		return nil
	}
	return copyValues
}

func retrievalScores(expected map[string]int, hitCount int) (float64, float64, float64) {
	if len(expected) == 0 {
		return 0, 0, 0
	}
	var best int
	for _, rank := range expected {
		if rank > 0 && (best == 0 || rank < best) {
			best = rank
		}
	}
	hitAt1, hitAt3, mrr := 0.0, 0.0, 0.0
	if best == 1 {
		hitAt1 = 1
	}
	if best >= 1 && best <= 3 {
		hitAt3 = 1
	}
	if best > 0 {
		mrr = 1.0 / float64(best)
	}
	_ = hitCount
	return hitAt1, hitAt3, mrr
}

func diagnostics(input *diagnosticResponse) (map[string]int, []diagnosticEvidence) {
	counts := map[string]int{}
	evidence := []diagnosticEvidence{}
	if input == nil {
		return counts, evidence
	}
	stages := []struct {
		name   string
		values []diagnosticCandidate
	}{
		{"bm25", input.BM25}, {"vector", input.Vector}, {"rrf", input.RRF}, {"rerank", input.Rerank}, {"mmrInput", input.MMRInput}, {"mmrOutput", input.MMROutput}, {"final", input.Final},
	}
	for _, stage := range stages {
		counts[stage.name] = len(stage.values)
		for _, item := range stage.values {
			evidence = append(evidence, diagnosticEvidence{Stage: stage.name, ChunkID: item.ChunkID, DocID: item.DocID, BaseID: item.BaseID, BM25Rank: item.BM25Rank, VectorRank: item.VectorRank, FinalRank: item.FinalRank, BM25Score: item.BM25Score, VectorScore: item.VectorScore, RRFScore: item.RRFScore, RerankScore: item.RerankScore, MMRScore: item.MMRScore})
		}
	}
	return counts, evidence
}

func topLevelKeys(value any) []string {
	object, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
func sha256HexBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}
func fingerprint(value report) string {
	value.StartedAt, value.FinishedAt, value.ReportFingerprint = "", "", ""
	encoded, _ := json.Marshal(value)
	return sha256HexBytes(encoded)
}

func writeReport(path string, value report) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".retrieval-regression-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	encoder := json.NewEncoder(tmp)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
