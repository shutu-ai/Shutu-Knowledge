// Command architecture-scenarios runs bounded API scenarios and writes a
// metadata-only report for the architecture refactor acceptance gates.
// It never stores search hits, document content, or complete API responses.
package main

import (
	"context"
	"crypto/rand"
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
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	maxResponseBytes = 4 << 20
	defaultScenario  = "status"
)

type requestSample struct {
	Scenario     string   `json:"scenario"`
	Kind         string   `json:"kind"`
	Method       string   `json:"method"`
	Path         string   `json:"path"`
	BaseID       string   `json:"baseId,omitempty"`
	OperationID  string   `json:"operationId,omitempty"`
	HTTPStatus   int      `json:"httpStatus"`
	Outcome      string   `json:"outcome"` // success | rejected | error | timeout | cancelled
	DurationMS   float64  `json:"durationMs"`
	ErrorCode    string   `json:"errorCode,omitempty"`
	ResponseKeys []string `json:"responseKeys,omitempty"`
}

type operationSample struct {
	Scenario      string                 `json:"scenario"`
	ID            string                 `json:"id"`
	Type          string                 `json:"type,omitempty"`
	BaseID        string                 `json:"baseId,omitempty"`
	State         string                 `json:"state"`
	Attempt       int64                  `json:"attempt,omitempty"`
	QueueWaitMS   int64                  `json:"queueWaitMs,omitempty"`
	RunTimeMS     int64                  `json:"runTimeMs,omitempty"`
	ParseTimeMS   int64                  `json:"parseTimeMs,omitempty"`
	ModelTimeMS   int64                  `json:"modelTimeMs,omitempty"`
	DiskReadMS    int64                  `json:"diskReadMs,omitempty"`
	DBWaitMS      int64                  `json:"dbWaitMs,omitempty"`
	DBTransaction int64                  `json:"dbTransactionMs,omitempty"`
	FTSTimeMS     int64                  `json:"ftsTimeMs,omitempty"`
	VectorTimeMS  int64                  `json:"vectorTimeMs,omitempty"`
	Outcome       string                 `json:"outcome"` // succeeded | failed | cancelled | timeout
	ErrorCode     string                 `json:"errorCode,omitempty"`
	EventKinds    []string               `json:"eventKinds,omitempty"`
	EventTrace    []operationEventSample `json:"eventTrace,omitempty"`
}

// operationEventSample is deliberately smaller than the server event DTO.
// It retains only the fields needed to reconstruct lifecycle/phase order and
// timing; command payloads and error text never enter the scenario report.
type operationEventSample struct {
	ID                   int64  `json:"id"`
	Revision             int64  `json:"revision"`
	Kind                 string `json:"kind"`
	CreatedAt            int64  `json:"createdAt"`
	ElapsedSincePrevious int64  `json:"elapsedSincePreviousMs,omitempty"`
	Phase                string `json:"phase,omitempty"`
	Completed            int64  `json:"completed,omitempty"`
	Total                int64  `json:"total,omitempty"`
}

type metricSnapshot struct {
	At        string         `json:"at"`
	Status    map[string]any `json:"status,omitempty"`
	Metrics   map[string]any `json:"metrics,omitempty"`
	BaseStats map[string]any `json:"baseStats,omitempty"`
}

type requestSummary struct {
	Count         int     `json:"count"`
	Success       int     `json:"success"`
	Rejected      int     `json:"rejected"`
	Errors        int     `json:"errors"`
	Timeouts      int     `json:"timeouts"`
	Cancelled     int     `json:"cancelled"`
	ThroughputPS  float64 `json:"throughputPerSecond"`
	P50MS         float64 `json:"p50Ms"`
	P95MS         float64 `json:"p95Ms"`
	P99MS         float64 `json:"p99Ms"`
	MaxDurationMS float64 `json:"maxDurationMs"`
}

type resourcePeaks struct {
	PeakRSSBytes     float64 `json:"peakRssBytes"`
	PeakWALBytes     float64 `json:"peakWalBytes"`
	PeakTempBytes    float64 `json:"peakTempBytes"`
	PeakUploadBytes  float64 `json:"peakUploadBytes"`
	PeakDiskBytes    float64 `json:"peakDiskBytes"`
	MinDiskFreeBytes float64 `json:"minDiskFreeBytes"`
	PeakActive       float64 `json:"peakActive"`
	PeakQueued       float64 `json:"peakQueued"`
}

type scenarioReport struct {
	SchemaVersion     int                       `json:"schemaVersion"`
	Status            string                    `json:"status"` // passed | failed | incomplete
	StartedAt         string                    `json:"startedAt"`
	FinishedAt        string                    `json:"finishedAt"`
	ReportFingerprint string                    `json:"reportFingerprint"`
	BaseURL           string                    `json:"baseUrl"`
	Scenarios         []string                  `json:"scenarios"`
	Configuration     map[string]any            `json:"configuration"`
	RunnerHost        map[string]any            `json:"runnerHost"`
	Unrun             []map[string]any          `json:"unrun,omitempty"`
	Errors            []string                  `json:"errors,omitempty"`
	Requests          []requestSample           `json:"requests,omitempty"`
	Operations        []operationSample         `json:"operations,omitempty"`
	Summaries         map[string]requestSummary `json:"summaries,omitempty"`
	Snapshots         []metricSnapshot          `json:"snapshots,omitempty"`
	ResourcePeaks     resourcePeaks             `json:"resourcePeaks"`
}

type runner struct {
	baseURL          string
	client           *http.Client
	requestTimeout   time.Duration
	pollInterval     time.Duration
	operationTimeout time.Duration
	mu               sync.Mutex
	report           scenarioReport
}

// requestFailure preserves the bounded outcome classification for callers
// that need to distinguish expected overload responses from infrastructure
// errors. The report remains the authoritative place for the full sample.
type requestFailure struct {
	outcome string
	status  int
	code    string
}

func (e requestFailure) Error() string {
	if e.code != "" {
		return fmt.Sprintf("HTTP %d (%s)", e.status, e.code)
	}
	return fmt.Sprintf("HTTP %d", e.status)
}

func main() {
	baseURL := flag.String("base-url", "", "running Knowledge server URL, for example http://127.0.0.1:8765")
	baseID := flag.String("base-id", "", "primary knowledge base ID")
	baseID2 := flag.String("base-id-2", "", "second knowledge base ID for dual-base scenarios")
	scenarioFlag := flag.String("scenario", defaultScenario, "comma-separated scenarios: status,single-import,single-reindex,dual-reindex,import-delete,continuous-search,page-switch,restart,writer-lock,disk-critical")
	query := flag.String("query", "architecture", "search query for continuous-search")
	deleteDocumentID := flag.String("delete-document-id", "", "existing document ID in -base-id-2 for import-delete")
	concurrency := flag.Int("concurrency", 2, "workers for continuous scenarios")
	duration := flag.Duration("duration", 30*time.Second, "maximum duration for continuous scenarios")
	requestTimeout := flag.Duration("request-timeout", 30*time.Second, "per-request timeout")
	operationTimeout := flag.Duration("operation-timeout", 10*time.Minute, "maximum wait for one Durable Operation")
	pollInterval := flag.Duration("poll-interval", 250*time.Millisecond, "operation polling interval")
	sampleInterval := flag.Duration("sample-interval", 2*time.Second, "status/metrics sampling interval")
	cacheState := flag.String("cache-state", "unknown", "observed cache state: cold, warm, or unknown")
	output := flag.String("output", "", "metadata-only JSON report path")
	flag.Parse()

	if strings.TrimSpace(*baseURL) == "" {
		fatal("-base-url is required")
	}
	if *concurrency < 1 || *concurrency > 256 {
		fatal("-concurrency must be between 1 and 256")
	}
	if *duration <= 0 || *requestTimeout <= 0 || *operationTimeout <= 0 || *pollInterval <= 0 || *sampleInterval <= 0 {
		fatal("duration and timeout/sampling intervals must be positive")
	}
	if *output == "" {
		fatal("-output is required; keep the report outside data directories")
	}
	rawBaseURL, safeBaseURL, err := normalizeBaseURL(*baseURL)
	if err != nil {
		fatal(err.Error())
	}

	scenarios, err := parseScenarios(*scenarioFlag)
	if err != nil {
		fatal(err.Error())
	}
	started := time.Now().UTC()
	r := &runner{
		baseURL:          rawBaseURL,
		client:           &http.Client{},
		requestTimeout:   *requestTimeout,
		pollInterval:     *pollInterval,
		operationTimeout: *operationTimeout,
		report: scenarioReport{
			SchemaVersion: 1,
			Status:        "passed",
			StartedAt:     started.Format(time.RFC3339Nano),
			BaseURL:       safeBaseURL,
			Scenarios:     scenarios,
			Configuration: map[string]any{
				"baseId": *baseID, "baseId2": *baseID2, "querySha256": sha256Hex(*query),
				"deleteDocumentId": *deleteDocumentID,
				"concurrency":      *concurrency, "duration": duration.String(),
				"requestTimeout": requestTimeout.String(), "operationTimeout": operationTimeout.String(),
				"pollInterval": pollInterval.String(), "sampleInterval": sampleInterval.String(),
				"cacheState": *cacheState,
			},
			RunnerHost: map[string]any{
				"os": runtime.GOOS, "arch": runtime.GOARCH, "cpus": runtime.NumCPU(),
				"go": runtime.Version(), "pid": os.Getpid(),
			},
			Summaries: make(map[string]requestSummary),
		},
	}

	// The duration flag bounds each continuous scenario independently. A
	// single invocation commonly runs both continuous-search and page-switch;
	// sharing one timeout would let the first scenario consume the entire
	// budget and make the following scenario appear to have no work samples.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var sampler sync.WaitGroup
	sampler.Add(1)
	go func() {
		defer sampler.Done()
		r.sampleLoop(ctx, *baseID, *baseID2, *sampleInterval)
	}()

	for _, scenario := range scenarios {
		scenarioCtx, scenarioCancel := contextForScenario(ctx, scenario, *duration)
		err := r.runScenario(scenarioCtx, scenario, *baseID, *baseID2, *deleteDocumentID, *query, *concurrency)
		scenarioCancel()
		if err != nil {
			r.addError(scenario + ": " + err.Error())
		}
	}
	cancel()
	sampler.Wait()
	// A final snapshot is useful even when the workload ended before the first
	// periodic tick; it is still metadata-only.
	finalCtx, finalCancel := context.WithTimeout(context.Background(), *requestTimeout)
	r.sample(finalCtx, *baseID, *baseID2)
	finalCancel()
	r.finish(time.Now().UTC())
	if err := writeReport(*output, r.report); err != nil {
		fatal(err.Error())
	}
	if r.report.Status != "passed" {
		fmt.Fprintf(os.Stderr, "scenario report written with status=%s: %s\n", r.report.Status, *output)
		os.Exit(1)
	}
	fmt.Printf("scenario report written: %s requests=%d operations=%d fingerprint=%s\n", *output, len(r.report.Requests), len(r.report.Operations), r.report.ReportFingerprint)
}

func fatal(message string) {
	fmt.Fprintln(os.Stderr, "error:", message)
	os.Exit(2)
}

func parseScenarios(raw string) ([]string, error) {
	allowed := map[string]bool{
		"status": true, "single-import": true, "single-reindex": true, "dual-reindex": true,
		"import-delete": true, "continuous-search": true, "page-switch": true,
		"restart": true, "writer-lock": true, "disk-critical": true,
	}
	seen := map[string]bool{}
	var scenarios []string
	for _, item := range strings.Split(raw, ",") {
		name := strings.TrimSpace(item)
		if name == "" || seen[name] {
			continue
		}
		if !allowed[name] {
			return nil, fmt.Errorf("unknown scenario %q", name)
		}
		seen[name] = true
		scenarios = append(scenarios, name)
	}
	if len(scenarios) == 0 {
		return nil, errors.New("at least one scenario is required")
	}
	return scenarios, nil
}

func contextForScenario(parent context.Context, scenario string, duration time.Duration) (context.Context, context.CancelFunc) {
	if scenario == "continuous-search" || scenario == "page-switch" {
		return context.WithTimeout(parent, duration)
	}
	return parent, func() {}
}

func normalizeBaseURL(raw string) (string, string, error) {
	requestURL := strings.TrimRight(strings.TrimSpace(raw), "/")
	parsed, err := url.Parse(requestURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", "", errors.New("-base-url must be an absolute http(s) URL")
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return requestURL, strings.TrimRight(parsed.String(), "/"), nil
}

func (r *runner) runScenario(ctx context.Context, scenario, baseID, baseID2, deleteDocumentID, query string, concurrency int) error {
	switch scenario {
	case "status":
		_, err := r.request(ctx, scenario, "GET", "/api/status", "", nil, "control")
		return err
	case "single-reindex":
		if baseID == "" {
			return errors.New("-base-id is required")
		}
		return r.runReindex(ctx, scenario, baseID)
	case "single-import":
		if baseID == "" {
			return errors.New("-base-id is required")
		}
		return r.runImport(ctx, scenario, baseID)
	case "dual-reindex":
		if baseID == "" || baseID2 == "" {
			return errors.New("-base-id and -base-id-2 are required")
		}
		var wg sync.WaitGroup
		errs := make(chan error, 2)
		for _, id := range []string{baseID, baseID2} {
			id := id
			wg.Add(1)
			go func() { defer wg.Done(); errs <- r.runReindex(ctx, scenario, id) }()
		}
		wg.Wait()
		close(errs)
		return joinErrors(errs)
	case "import-delete":
		if baseID == "" || baseID2 == "" {
			return errors.New("-base-id and -base-id-2 are required")
		}
		return r.runImportDelete(ctx, scenario, baseID, baseID2, deleteDocumentID)
	case "continuous-search":
		if baseID == "" {
			return errors.New("-base-id is required")
		}
		return r.runContinuous(ctx, scenario, query, baseID, baseID2, concurrency, "/api/search", func(id string) (string, any) {
			body := map[string]any{"query": query, "baseId": id, "topK": 10, "mode": "hybrid"}
			return "POST", body
		})
	case "page-switch":
		if baseID == "" {
			return errors.New("-base-id is required")
		}
		return r.runContinuous(ctx, scenario, "", baseID, baseID2, concurrency, "", func(id string) (string, any) {
			return "GET", nil
		})
	case "restart", "writer-lock", "disk-critical":
		r.addUnrun(scenario, "requires process restart, OS file-lock, or dedicated-volume fault injection outside the HTTP client")
		return nil
	default:
		return fmt.Errorf("scenario %q is not implemented", scenario)
	}
}

func (r *runner) runReindex(ctx context.Context, scenario, baseID string) error {
	payload, err := r.request(ctx, scenario, "POST", "/api/bases/"+url.PathEscape(baseID)+"/reindex", baseID, map[string]any{}, "submit")
	if err != nil {
		return err
	}
	opID := operationID(payload)
	if opID == "" {
		return errors.New("accepted reindex response did not include operationId")
	}
	return r.waitOperation(ctx, scenario, opID, baseID)
}

func (r *runner) runImport(ctx context.Context, scenario, baseID string) error {
	_, opID, err := r.submitImport(ctx, scenario, baseID)
	if err != nil {
		return err
	}
	return r.waitOperation(ctx, scenario, opID, baseID)
}

func (r *runner) runImportDelete(ctx context.Context, scenario, baseID, baseID2, deleteDocumentID string) error {
	_, opID, err := r.submitImport(ctx, scenario, baseID)
	if err != nil {
		return err
	}
	if err := r.waitOperation(ctx, scenario, opID, baseID); err != nil {
		return err
	}
	// Cross-base deletion must be explicit. Never infer a document from the
	// import response or risk deleting a caller-owned object in the wrong base.
	if deleteDocumentID == "" {
		r.addUnrun("import-delete", "-delete-document-id is required and must identify an existing document in -base-id-2")
		return nil
	}
	deletePayload, err := r.request(ctx, scenario, "POST", "/api/documents/"+url.PathEscape(deleteDocumentID)+"/delete", baseID2, map[string]any{}, "submit")
	if err != nil {
		return err
	}
	deleteID := operationID(deletePayload)
	if deleteID == "" {
		return errors.New("accepted delete response did not include operationId")
	}
	return r.waitOperation(ctx, scenario, deleteID, baseID2)
}

func (r *runner) submitImport(ctx context.Context, scenario, baseID string) (any, string, error) {
	key := "architecture-scenario-" + randomSuffix()
	payload, err := r.request(ctx, scenario, "POST", "/api/operations", baseID, map[string]any{
		"type": "import_text", "commandSchemaVersion": 1,
		"target":         map[string]any{"baseId": baseID},
		"input":          map[string]any{"title": "architecture scenario probe", "content": "bounded scenario probe"},
		"idempotencyKey": key,
	}, "submit")
	if err != nil {
		return nil, "", err
	}
	opID := operationID(payload)
	if opID == "" {
		return nil, "", errors.New("accepted import response did not include operationId")
	}
	return payload, opID, nil
}

type continuousCall func(string) (string, any)

func (r *runner) runContinuous(ctx context.Context, scenario, query, baseID, baseID2 string, concurrency int, path string, makeCall continuousCall) error {
	ids := []string{baseID}
	if baseID2 != "" {
		ids = append(ids, baseID2)
	}
	var wg sync.WaitGroup
	errs := make(chan error, concurrency)
	for worker := 0; worker < concurrency; worker++ {
		worker := worker
		wg.Add(1)
		go func() {
			defer wg.Done()
			index := worker % len(ids)
			for {
				select {
				case <-ctx.Done():
					return
				default:
				}
				id := ids[index%len(ids)]
				index++
				if scenario == "page-switch" {
					pagePath := "/api/bases/" + url.PathEscape(id) + "/documents/children?parentId=&limit=50&offset=" + strconv.Itoa((index%4)*50)
					if _, err := r.request(ctx, scenario, "GET", pagePath, id, nil, "work"); err != nil {
						if ctx.Err() != nil || isExpectedWorkFailure(err) {
							continue
						}
						errs <- err
						return
					}
					continue
				}
				method, body := makeCall(id)
				if _, err := r.request(ctx, scenario, method, path, id, body, "work"); err != nil {
					if ctx.Err() != nil || isExpectedWorkFailure(err) {
						continue
					}
					errs <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	if err := joinErrors(errs); err != nil {
		return err
	}
	return r.requireSuccessfulWork(scenario)
}

func isExpectedWorkFailure(err error) bool {
	var failure requestFailure
	if !errors.As(err, &failure) {
		return false
	}
	return failure.outcome == "rejected" || failure.outcome == "timeout"
}

func (r *runner) requireSuccessfulWork(scenario string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	workRequests := 0
	successes := 0
	for _, sample := range r.report.Requests {
		if sample.Scenario != scenario || sample.Kind != "work" {
			continue
		}
		workRequests++
		if sample.Outcome == "success" {
			successes++
		}
	}
	if successes == 0 {
		return fmt.Errorf("scenario %s produced no successful work request (%d work requests)", scenario, workRequests)
	}
	return nil
}

func (r *runner) waitOperation(ctx context.Context, scenario, id, baseID string) error {
	deadline := time.NewTimer(r.operationTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(r.pollInterval)
	defer ticker.Stop()
	for {
		payload, err := r.request(ctx, scenario, "GET", "/api/operations/"+url.PathEscape(id), baseID, nil, "poll")
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return context.Canceled
			}
			return err
		}
		op := parseOperation(payload, scenario, id, baseID)
		if op.State == "succeeded" || op.State == "failed" || op.State == "cancelled" {
			if events, eventsErr := r.request(ctx, scenario, "GET", "/api/operations/"+url.PathEscape(id)+"/events?limit=500", baseID, nil, "event"); eventsErr == nil {
				op.EventKinds = eventKinds(events)
				op.EventTrace = eventTrace(events)
			}
			r.mu.Lock()
			r.report.Operations = append(r.report.Operations, op)
			r.mu.Unlock()
			if op.State != "succeeded" {
				return fmt.Errorf("operation %s ended %s", id, op.State)
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			r.addOperationTimeout(scenario, id, baseID)
			return fmt.Errorf("operation %s exceeded operation-timeout", id)
		case <-ticker.C:
		}
	}
}

func (r *runner) request(ctx context.Context, scenario, method, path, baseID string, body any, kind string) (any, error) {
	started := time.Now()
	requestCtx, cancel := context.WithTimeout(ctx, r.requestTimeout)
	defer cancel()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = strings.NewReader(string(encoded))
	}
	req, err := http.NewRequestWithContext(requestCtx, method, r.baseURL+path, reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := r.client.Do(req)
	sample := requestSample{Scenario: scenario, Kind: kind, Method: method, Path: path, BaseID: baseID, DurationMS: float64(time.Since(started).Microseconds()) / 1000}
	if err != nil {
		sample.Outcome = "error"
		if ctx.Err() != nil {
			sample.Outcome = "cancelled"
		} else if errors.Is(requestCtx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
			sample.Outcome = "timeout"
		}
		r.addRequest(sample)
		return nil, err
	}
	defer resp.Body.Close()
	data, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if len(data) > maxResponseBytes {
		data = data[:maxResponseBytes]
		readErr = fmt.Errorf("response exceeds %d bytes", maxResponseBytes)
	}
	var payload any
	if len(data) > 0 {
		_ = json.Unmarshal(data, &payload)
	}
	sample.HTTPStatus = resp.StatusCode
	sample.ResponseKeys = topLevelKeys(payload)
	if resp.StatusCode >= 200 && resp.StatusCode < 300 && readErr == nil {
		sample.Outcome = "success"
	} else if resp.StatusCode == http.StatusRequestTimeout || resp.StatusCode == http.StatusGatewayTimeout {
		sample.Outcome = "timeout"
	} else if resp.StatusCode == http.StatusConflict || resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable || resp.StatusCode == http.StatusRequestEntityTooLarge {
		sample.Outcome = "rejected"
	} else {
		sample.Outcome = "error"
	}
	sample.DurationMS = float64(time.Since(started).Microseconds()) / 1000
	sample.ErrorCode = errorCode(payload)
	if sample.Outcome == "success" && path == "/api/search" && !validSearchPayload(payload) {
		sample.Outcome = "error"
		sample.ErrorCode = "invalid_search_result"
	}
	if id := operationID(payload); id != "" {
		sample.OperationID = id
	}
	r.addRequest(sample)
	if sample.Outcome != "success" {
		return nil, requestFailure{outcome: sample.Outcome, status: resp.StatusCode, code: sample.ErrorCode}
	}
	return payload, nil
}

// validSearchPayload rejects HTTP-200 responses that contain no usable
// evidence. A status code alone must not turn an empty or unversioned search
// result into a successful architecture baseline sample.
func validSearchPayload(value any) bool {
	object, ok := unwrapEnvelope(value).(map[string]any)
	if !ok {
		return false
	}
	hits, ok := object["hits"].([]any)
	if !ok || len(hits) == 0 {
		return false
	}
	for _, raw := range hits {
		hit, ok := raw.(map[string]any)
		if !ok || strings.TrimSpace(stringValue(hit["chunkId"])) == "" ||
			strings.TrimSpace(stringValue(hit["docId"])) == "" ||
			strings.TrimSpace(stringValue(hit["baseId"])) == "" ||
			!positiveJSONNumber(hit["indexGeneration"]) || !positiveJSONNumber(hit["sourceVersion"]) {
			return false
		}
	}
	return true
}

func positiveJSONNumber(value any) bool {
	switch number := value.(type) {
	case float64:
		return number > 0
	case float32:
		return number > 0
	case int:
		return number > 0
	case int64:
		return number > 0
	case json.Number:
		parsed, err := strconv.ParseFloat(string(number), 64)
		return err == nil && parsed > 0
	default:
		return false
	}
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func (r *runner) sampleLoop(ctx context.Context, baseID, baseID2 string, interval time.Duration) {
	r.sample(ctx, baseID, baseID2)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.sample(ctx, baseID, baseID2)
		}
	}
}

func (r *runner) sample(ctx context.Context, baseID, baseID2 string) {
	statusPayload, statusErr := r.request(ctx, "sampling", "GET", "/api/status", "", nil, "sample")
	metricsPayload, metricsErr := r.request(ctx, "sampling", "GET", "/api/metrics", "", nil, "sample")
	snapshot := metricSnapshot{At: time.Now().UTC().Format(time.RFC3339Nano)}
	if statusErr == nil {
		snapshot.Status = sanitizeStatus(statusPayload)
	}
	if metricsErr == nil {
		snapshot.Metrics = sanitizeMap(unwrapEnvelope(metricsPayload))
	}
	if baseID != "" {
		if payload, err := r.request(ctx, "sampling", "GET", "/api/bases/"+url.PathEscape(baseID)+"/stats", baseID, nil, "sample"); err == nil {
			snapshot.BaseStats = map[string]any{baseID: sanitizeMap(unwrapEnvelope(payload))}
		}
	}
	if baseID2 != "" && baseID2 != baseID {
		if payload, err := r.request(ctx, "sampling", "GET", "/api/bases/"+url.PathEscape(baseID2)+"/stats", baseID2, nil, "sample"); err == nil {
			if snapshot.BaseStats == nil {
				snapshot.BaseStats = map[string]any{}
			}
			snapshot.BaseStats[baseID2] = sanitizeMap(unwrapEnvelope(payload))
		}
	}
	if statusErr == nil || metricsErr == nil {
		r.mu.Lock()
		r.report.Snapshots = append(r.report.Snapshots, snapshot)
		r.mu.Unlock()
	}
}

func (r *runner) addRequest(sample requestSample) {
	r.mu.Lock()
	r.report.Requests = append(r.report.Requests, sample)
	r.mu.Unlock()
}

func (r *runner) addOperationTimeout(scenario, id, baseID string) {
	r.mu.Lock()
	r.report.Operations = append(r.report.Operations, operationSample{Scenario: scenario, ID: id, BaseID: baseID, State: "timeout", Outcome: "timeout"})
	r.mu.Unlock()
}

func (r *runner) addUnrun(scenario, reason string) {
	r.mu.Lock()
	r.report.Unrun = append(r.report.Unrun, map[string]any{"scenario": scenario, "reason": reason})
	r.mu.Unlock()
}

func (r *runner) addError(message string) {
	r.mu.Lock()
	r.report.Errors = append(r.report.Errors, message)
	r.mu.Unlock()
}

func (r *runner) finish(finished time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.report.FinishedAt = finished.Format(time.RFC3339Nano)
	if len(r.report.Errors) > 0 {
		r.report.Status = "failed"
	} else if len(r.report.Unrun) > 0 {
		r.report.Status = "incomplete"
	}
	byScenario := map[string][]float64{}
	for _, sample := range r.report.Requests {
		byScenario[sample.Scenario] = append(byScenario[sample.Scenario], sample.DurationMS)
	}
	elapsed := finished.Sub(parseTime(r.report.StartedAt)).Seconds()
	if elapsed <= 0 {
		elapsed = 0.001
	}
	for scenario, values := range byScenario {
		sort.Float64s(values)
		summary := requestSummary{Count: len(values), ThroughputPS: float64(len(values)) / elapsed, P50MS: percentile(values, 0.50), P95MS: percentile(values, 0.95), P99MS: percentile(values, 0.99)}
		for _, sample := range r.report.Requests {
			if sample.Scenario != scenario {
				continue
			}
			switch sample.Outcome {
			case "success":
				summary.Success++
			case "rejected":
				summary.Rejected++
			case "timeout":
				summary.Timeouts++
			case "cancelled":
				summary.Cancelled++
			default:
				summary.Errors++
			}
			if sample.DurationMS > summary.MaxDurationMS {
				summary.MaxDurationMS = sample.DurationMS
			}
		}
		r.report.Summaries[scenario] = summary
	}
	r.report.ResourcePeaks = resourcePeaksFromSnapshots(r.report.Snapshots)
	r.report.ReportFingerprint = reportFingerprint(r.report)
}

func writeReport(path string, report scenarioReport) error {
	if dir := strings.TrimSpace(filepathDir(path)); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	tmp, err := os.CreateTemp(dirOrDot(path), ".architecture-scenarios-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	encoder := json.NewEncoder(tmp)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func reportFingerprint(report scenarioReport) string {
	copyReport := report
	copyReport.StartedAt = ""
	copyReport.FinishedAt = ""
	copyReport.ReportFingerprint = ""
	encoded, _ := json.Marshal(copyReport)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func operationID(value any) string { return findString(value, "operationId", "jobId") }
func documentID(value any) string  { return findString(value, "documentId") }

func findString(value any, keys ...string) string {
	allowed := map[string]bool{}
	for _, key := range keys {
		allowed[key] = true
	}
	var walk func(any) string
	walk = func(item any) string {
		switch typed := item.(type) {
		case map[string]any:
			for key, child := range typed {
				if allowed[key] {
					if text, ok := child.(string); ok && text != "" {
						return text
					}
				}
			}
			for _, child := range typed {
				if found := walk(child); found != "" {
					return found
				}
			}
		case []any:
			for _, child := range typed {
				if found := walk(child); found != "" {
					return found
				}
			}
		}
		return ""
	}
	return walk(value)
}

func parseOperation(value any, scenario, id, baseID string) operationSample {
	item := findMapWithState(value)
	op := operationSample{Scenario: scenario, ID: id, BaseID: baseID, State: "unknown", Outcome: "failed"}
	if item == nil {
		return op
	}
	op.Type, _ = item["type"].(string)
	if value, ok := item["baseId"].(string); ok && value != "" {
		op.BaseID = value
	}
	op.State, _ = item["state"].(string)
	if op.State == "" {
		op.State = "unknown"
	}
	op.Attempt = numberInt(item["attempt"])
	op.QueueWaitMS = numberInt(item["queueWaitMs"])
	op.RunTimeMS = numberInt(item["runTimeMs"])
	op.ParseTimeMS = numberInt(item["parseTimeMs"])
	op.ModelTimeMS = numberInt(item["modelTimeMs"])
	op.DiskReadMS = numberInt(item["diskReadMs"])
	op.DBWaitMS = numberInt(item["dbWaitMs"])
	op.DBTransaction = numberInt(item["dbTransactionMs"])
	op.FTSTimeMS = numberInt(item["ftsTimeMs"])
	op.VectorTimeMS = numberInt(item["vectorTimeMs"])
	op.Outcome = op.State
	op.ErrorCode = errorCode(item)
	return op
}

func findMapWithState(value any) map[string]any {
	switch typed := value.(type) {
	case map[string]any:
		if _, ok := typed["state"]; ok {
			return typed
		}
		for _, child := range typed {
			if found := findMapWithState(child); found != nil {
				return found
			}
		}
	case []any:
		for _, child := range typed {
			if found := findMapWithState(child); found != nil {
				return found
			}
		}
	}
	return nil
}

func eventKinds(value any) []string {
	seen := map[string]bool{}
	var walk func(any)
	walk = func(item any) {
		switch typed := item.(type) {
		case map[string]any:
			if kind, ok := typed["kind"].(string); ok && kind != "" {
				seen[kind] = true
			}
			for _, child := range typed {
				walk(child)
			}
		case []any:
			for _, child := range typed {
				walk(child)
			}
		}
	}
	walk(value)
	result := make([]string, 0, len(seen))
	for kind := range seen {
		result = append(result, kind)
	}
	sort.Strings(result)
	return result
}

func eventTrace(value any) []operationEventSample {
	var result []operationEventSample
	var walk func(any)
	walk = func(item any) {
		switch typed := item.(type) {
		case map[string]any:
			kind, ok := typed["kind"].(string)
			if ok && kind != "" {
				event := operationEventSample{
					ID: numberInt(typed["id"]), Revision: numberInt(typed["stateRevision"]),
					Kind: kind, CreatedAt: numberInt(typed["createdAt"]),
				}
				if payload, ok := typed["payload"].(map[string]any); ok {
					event.Phase, _ = payload["phase"].(string)
					event.Completed = numberInt(payload["completed"])
					event.Total = numberInt(payload["total"])
				}
				result = append(result, event)
				return
			}
			for _, child := range typed {
				walk(child)
			}
		case []any:
			for _, child := range typed {
				walk(child)
			}
		}
	}
	walk(value)
	for index := 1; index < len(result); index++ {
		if result[index].CreatedAt > 0 && result[index-1].CreatedAt > 0 {
			elapsed := result[index].CreatedAt - result[index-1].CreatedAt
			if elapsed > 0 {
				result[index].ElapsedSincePrevious = elapsed
			}
		}
	}
	return result
}

func unwrapEnvelope(value any) any {
	if object, ok := value.(map[string]any); ok {
		if nested, exists := object["value"]; exists {
			return nested
		}
	}
	return value
}

func errorCode(value any) string {
	if object, ok := value.(map[string]any); ok {
		if errObject, ok := object["error"].(map[string]any); ok {
			if code, ok := errObject["code"].(string); ok {
				return code
			}
		}
		if code, ok := object["errorCode"].(string); ok {
			return code
		}
	}
	return ""
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

func sanitizeStatus(value any) map[string]any {
	object, ok := unwrapEnvelope(value).(map[string]any)
	if !ok {
		return nil
	}
	result := map[string]any{}
	for _, key := range []string{"ready", "status", "capabilities", "storageFormat", "scheduler"} {
		if child, exists := object[key]; exists {
			result[key] = sanitizeNumbers(child, 0)
		}
	}
	return result
}

func sanitizeMap(value any) map[string]any {
	cleaned, _ := sanitizeNumbers(value, 0).(map[string]any)
	return cleaned
}

func resourcePeaksFromSnapshots(snapshots []metricSnapshot) resourcePeaks {
	peaks := resourcePeaks{MinDiskFreeBytes: -1}
	for _, snapshot := range snapshots {
		if scheduler, ok := snapshot.Status["scheduler"].(map[string]any); ok {
			// scheduler.limits contains configured budgets. Only
			// scheduler.resources is an observed footprint and may contribute to
			// peak resource values.
			addResourceNumbers(scheduler["resources"], &peaks)
			walkNumbers(map[string]any{
				"activeTotal": scheduler["activeTotal"],
				"queuedTotal": scheduler["queuedTotal"],
			}, func(key string, value float64) {
				updateQueuePeaks(&peaks, key, value)
			})
		}
		// Keep support for a future metrics endpoint that exposes an observed
		// resources object, while never scanning configuration limits.
		if resources, ok := snapshot.Metrics["resources"]; ok {
			addResourceNumbers(resources, &peaks)
		}
		if resources, ok := snapshot.BaseStats["resources"]; ok {
			addResourceNumbers(resources, &peaks)
		}
	}
	if peaks.MinDiskFreeBytes < 0 {
		peaks.MinDiskFreeBytes = 0
	}
	return peaks
}

func addResourceNumbers(value any, peaks *resourcePeaks) {
	walkNumbers(value, func(key string, value float64) {
		switch strings.ToLower(key) {
		case "peakrssbytes", "rssbytes":
			peaks.PeakRSSBytes = maxFloat(peaks.PeakRSSBytes, value)
		case "walbytes":
			peaks.PeakWALBytes = maxFloat(peaks.PeakWALBytes, value)
		case "tempbytes":
			peaks.PeakTempBytes = maxFloat(peaks.PeakTempBytes, value)
		case "uploadbytes":
			peaks.PeakUploadBytes = maxFloat(peaks.PeakUploadBytes, value)
		case "diskbytes":
			peaks.PeakDiskBytes = maxFloat(peaks.PeakDiskBytes, value)
		case "diskfreebytes":
			if peaks.MinDiskFreeBytes < 0 || value < peaks.MinDiskFreeBytes {
				peaks.MinDiskFreeBytes = value
			}
		case "activetotal", "active":
		case "queuedtotal", "queued":
		}
	})
}

func updateQueuePeaks(peaks *resourcePeaks, key string, value float64) {
	switch strings.ToLower(key) {
	case "activetotal", "active":
		peaks.PeakActive = maxFloat(peaks.PeakActive, value)
	case "queuedtotal", "queued":
		peaks.PeakQueued = maxFloat(peaks.PeakQueued, value)
	}
}

func walkNumbers(value any, visit func(string, float64)) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if number, ok := child.(float64); ok {
				visit(key, number)
			}
			walkNumbers(child, visit)
		}
	case []any:
		for _, child := range typed {
			walkNumbers(child, visit)
		}
	}
}

func maxFloat(left, right float64) float64 {
	if right > left {
		return right
	}
	return left
}

func sanitizeNumbers(value any, depth int) any {
	if depth > 8 {
		return nil
	}
	switch typed := value.(type) {
	case nil, bool, float64, string:
		if text, ok := typed.(string); ok && len(text) > 128 {
			return text[:128]
		}
		return typed
	case []any:
		out := make([]any, 0, len(typed))
		for _, child := range typed {
			out = append(out, sanitizeNumbers(child, depth+1))
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			lower := strings.ToLower(key)
			if strings.Contains(lower, "text") || strings.Contains(lower, "content") || strings.Contains(lower, "token") || strings.Contains(lower, "secret") || strings.Contains(lower, "password") || strings.Contains(lower, "cookie") || strings.Contains(lower, "authorization") || strings.Contains(lower, "credential") || strings.Contains(lower, "apikey") || lower == "key" || strings.Contains(lower, "error") || strings.Contains(lower, "message") || strings.Contains(lower, "path") {
				continue
			}
			out[key] = sanitizeNumbers(child, depth+1)
		}
		return out
	default:
		return nil
	}
}

func numberInt(value any) int64 {
	switch typed := value.(type) {
	case float64:
		return int64(typed)
	case int64:
		return typed
	case int:
		return int64(typed)
	case json.Number:
		number, _ := typed.Int64()
		return number
	default:
		return 0
	}
}

func percentile(values []float64, fraction float64) float64 {
	if len(values) == 0 {
		return 0
	}
	if fraction <= 0 {
		return values[0]
	}
	if fraction >= 1 {
		return values[len(values)-1]
	}
	index := int((float64(len(values)-1) * fraction) + 0.5)
	if index >= len(values) {
		index = len(values) - 1
	}
	return values[index]
}

func joinErrors(errs <-chan error) error {
	var messages []string
	for err := range errs {
		if err != nil {
			messages = append(messages, err.Error())
		}
	}
	if len(messages) == 0 {
		return nil
	}
	return errors.New(strings.Join(messages, "; "))
}

func randomSuffix() string {
	var bytes [8]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	return hex.EncodeToString(bytes[:])
}

func parseTime(value string) time.Time {
	parsed, _ := time.Parse(time.RFC3339Nano, value)
	return parsed
}

// These tiny helpers keep report writing platform-neutral without importing
// path/filepath in the command's public report model.
func filepathDir(path string) string {
	index := strings.LastIndexAny(path, `/\\`)
	if index < 0 {
		return "."
	}
	return path[:index]
}
func dirOrDot(path string) string {
	directory := filepathDir(path)
	if directory == "" {
		return "."
	}
	return directory
}
