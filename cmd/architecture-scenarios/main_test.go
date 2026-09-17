package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPercentileUsesNearestRankAndSortsAtCaller(t *testing.T) {
	values := []float64{1, 2, 3, 4, 5}
	if got := percentile(values, 0.50); got != 3 {
		t.Fatalf("p50 = %v, want 3", got)
	}
	if got := percentile(values, 0.95); got != 5 {
		t.Fatalf("p95 = %v, want 5", got)
	}
	if got := percentile(nil, 0.95); got != 0 {
		t.Fatalf("empty percentile = %v, want 0", got)
	}
}

func TestOperationAndDocumentIDsAreExtractedFromEnvelope(t *testing.T) {
	value := map[string]any{
		"ok": true,
		"value": map[string]any{
			"jobId": "op-42", "documentId": "doc-7",
		},
	}
	if got := operationID(value); got != "op-42" {
		t.Fatalf("operation ID = %q", got)
	}
	if got := documentID(value); got != "doc-7" {
		t.Fatalf("document ID = %q", got)
	}
}

func TestSanitizeStatusDropsContentAndSecrets(t *testing.T) {
	status := map[string]any{
		"ready":        true,
		"status":       "ready",
		"apiKey":       "must-not-appear",
		"capabilities": map[string]any{"operationsV1": true},
		"scheduler": map[string]any{
			"active": 2.0, "path": "C:/private", "message": "raw text",
		},
		"ignoredContent": "document bytes",
	}
	clean := sanitizeStatus(status)
	if clean["ready"] != true {
		t.Fatalf("ready was not retained: %+v", clean)
	}
	scheduler, ok := clean["scheduler"].(map[string]any)
	if !ok || scheduler["active"] != 2.0 {
		t.Fatalf("scheduler metrics missing: %+v", clean)
	}
	if _, exists := scheduler["path"]; exists {
		t.Fatalf("path leaked into report: %+v", clean)
	}
	if _, exists := clean["apiKey"]; exists {
		t.Fatalf("api key leaked into report: %+v", clean)
	}
}

func TestFinishAggregatesRequestOutcomes(t *testing.T) {
	started := time.Now().UTC().Add(-2 * time.Second)
	r := &runner{report: scenarioReport{
		StartedAt: started.Format(time.RFC3339Nano),
		Status:    "passed", Summaries: map[string]requestSummary{},
	}}
	r.addRequest(requestSample{Scenario: "search", Outcome: "success", DurationMS: 10})
	r.addRequest(requestSample{Scenario: "search", Outcome: "rejected", DurationMS: 20})
	r.addRequest(requestSample{Scenario: "search", Outcome: "timeout", DurationMS: 30})
	r.finish(time.Now().UTC())
	summary := r.report.Summaries["search"]
	if summary.Count != 3 || summary.Success != 1 || summary.Rejected != 1 || summary.Timeouts != 1 {
		t.Fatalf("summary = %+v", summary)
	}
	if summary.P50MS != 20 || summary.P99MS != 30 || summary.ThroughputPS <= 0 {
		t.Fatalf("latency/throughput summary = %+v", summary)
	}
	if r.report.ReportFingerprint == "" {
		t.Fatal("report fingerprint is empty")
	}
}

func TestFinishMarksUnrunScenarioIncomplete(t *testing.T) {
	r := &runner{report: scenarioReport{
		StartedAt: time.Now().UTC().Add(-time.Second).Format(time.RFC3339Nano),
		Status:    "passed",
	}}
	r.addUnrun("disk-critical", "requires a dedicated persistent volume")
	r.finish(time.Now().UTC())
	if r.report.Status != "incomplete" {
		t.Fatalf("status = %q, want incomplete", r.report.Status)
	}
}

func TestResourcePeaksAggregateSanitizedSnapshots(t *testing.T) {
	peaks := resourcePeaksFromSnapshots([]metricSnapshot{
		{Status: map[string]any{"scheduler": map[string]any{
			"limits":      map[string]any{"diskBytes": 999.0, "tempBytes": 999.0, "uploadBytes": 999.0},
			"resources":   map[string]any{"peakRssBytes": 90.0, "walBytes": 12.0, "tempBytes": 3.0, "diskFreeBytes": 100.0},
			"activeTotal": 2.0, "queuedTotal": 5.0,
		}}},
		{Metrics: map[string]any{"resources": map[string]any{
			"peakRssBytes": 140.0, "walBytes": 20.0, "tempBytes": 7.0, "diskFreeBytes": 80.0,
		}}},
	})
	if peaks.PeakRSSBytes != 140 || peaks.PeakWALBytes != 20 || peaks.PeakTempBytes != 7 || peaks.PeakDiskBytes != 0 || peaks.MinDiskFreeBytes != 80 || peaks.PeakActive != 2 || peaks.PeakQueued != 5 {
		t.Fatalf("resource peaks = %+v", peaks)
	}
}

func TestWriteReportIsPrivateAndValidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "report.json")
	report := scenarioReport{SchemaVersion: 1, Status: "passed", Scenarios: []string{"status"}}
	if err := writeReport(path, report); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var decoded scenarioReport
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.SchemaVersion != 1 {
		t.Fatalf("decoded report = %+v", decoded)
	}
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(path); err != nil || info.Mode().Perm()&0077 != 0 {
			t.Fatalf("report permissions = %v, err=%v", info.Mode().Perm(), err)
		}
	}
}

func TestParseScenariosRejectsUnknownAndDeduplicates(t *testing.T) {
	got, err := parseScenarios("status, status,dual-reindex")
	if err != nil || len(got) != 2 || got[0] != "status" || got[1] != "dual-reindex" {
		t.Fatalf("scenarios=%v err=%v", got, err)
	}
	if _, err := parseScenarios("status,nope"); err == nil {
		t.Fatal("unknown scenario unexpectedly accepted")
	}
}

func TestContinuousScenariosGetIndependentDurationContexts(t *testing.T) {
	parent := context.Background()
	first, cancelFirst := contextForScenario(parent, "continuous-search", time.Millisecond)
	defer cancelFirst()
	<-first.Done()

	second, cancelSecond := contextForScenario(parent, "page-switch", time.Second)
	defer cancelSecond()
	select {
	case <-second.Done():
		t.Fatal("second continuous scenario inherited the first scenario deadline")
	default:
	}

	discrete, cancelDiscrete := contextForScenario(parent, "single-reindex", time.Millisecond)
	defer cancelDiscrete()
	select {
	case <-discrete.Done():
		t.Fatal("discrete scenario unexpectedly received a continuous deadline")
	default:
	}
}

func TestValidSearchPayloadRequiresVersionedHit(t *testing.T) {
	valid := map[string]any{"ok": true, "value": map[string]any{
		"hits": []any{map[string]any{
			"chunkId": "chunk-1", "docId": "doc-1", "baseId": "base-1",
			"indexGeneration": 2.0, "sourceVersion": 3.0,
		}},
	}}
	if !validSearchPayload(valid) {
		t.Fatal("versioned search hit should be valid")
	}
	for _, invalid := range []map[string]any{
		{"ok": true, "value": map[string]any{"hits": []any{}}},
		{"ok": true, "value": map[string]any{"hits": []any{map[string]any{
			"chunkId": "chunk-1", "docId": "doc-1", "baseId": "base-1",
		}}}},
	} {
		if validSearchPayload(invalid) {
			t.Fatalf("invalid search payload accepted: %+v", invalid)
		}
	}
}

func TestRunContinuousAllowsExpectedRejectionsWithSuccessfulWork(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			writer.WriteHeader(http.StatusTooManyRequests)
			_, _ = writer.Write([]byte(`{"ok":false,"error":{"code":"queue_full"}}`))
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"ok":true,"value":{"hits":[{"chunkId":"chunk-1","docId":"doc-1","baseId":"base-1","indexGeneration":1,"sourceVersion":1}]}}`))
	}))
	defer server.Close()

	// The scenario itself must have enough room to observe both the expected
	// rejection and the subsequent successful request. This is a test-harness
	// budget, not the production request or scenario threshold; 20ms allowed
	// the first local request to consume the whole budget under -race.
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	r := &runner{baseURL: server.URL, client: server.Client(), requestTimeout: time.Second}
	err := r.runContinuous(ctx, "continuous-search", "q", "base-1", "", 1, "/api/search", func(string) (string, any) {
		return http.MethodPost, map[string]any{"query": "q"}
	})
	if err != nil {
		t.Fatalf("continuous search: %v", err)
	}
	var rejected, success bool
	for _, sample := range r.report.Requests {
		if sample.Kind != "work" {
			continue
		}
		rejected = rejected || sample.Outcome == "rejected"
		success = success || sample.Outcome == "success"
	}
	if !rejected || !success {
		t.Fatalf("work outcomes missing expected rejection/success: %+v", r.report.Requests)
	}
}

func TestRunContinuousFailsWhenAllWorkIsRejected(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusTooManyRequests)
		_, _ = writer.Write([]byte(`{"ok":false,"error":{"code":"queue_full"}}`))
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
	defer cancel()
	r := &runner{baseURL: server.URL, client: server.Client(), requestTimeout: time.Second}
	err := r.runContinuous(ctx, "continuous-search", "q", "base-1", "", 1, "/api/search", func(string) (string, any) {
		return http.MethodPost, map[string]any{"query": "q"}
	})
	if err == nil || !strings.Contains(err.Error(), "no successful work request") {
		t.Fatalf("error = %v, want no-success failure", err)
	}
}

func TestSearchRequestRejectsEmptyHitsDespiteHTTP200(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"ok":true,"value":{"hits":[]}}`))
	}))
	defer server.Close()
	r := &runner{baseURL: server.URL, client: server.Client(), requestTimeout: time.Second}
	if _, err := r.request(context.Background(), "continuous-search", http.MethodPost, "/api/search", "base-1", map[string]any{}, "work"); err == nil {
		t.Fatal("empty search response unexpectedly accepted")
	}
	if len(r.report.Requests) != 1 || r.report.Requests[0].Outcome != "error" || r.report.Requests[0].ErrorCode != "invalid_search_result" {
		t.Fatalf("request sample = %+v", r.report.Requests)
	}
}

func TestBaseURLValidationShapeCanDropUserInfo(t *testing.T) {
	requestURL, safeURL, err := normalizeBaseURL("https://user:secret@example.test:8443/extensions/x?token=redact")
	if err != nil {
		t.Fatal(err)
	}
	if requestURL != "https://user:secret@example.test:8443/extensions/x?token=redact" || safeURL != "https://example.test:8443/extensions/x" {
		t.Fatalf("request/safe URL = %q / %q", requestURL, safeURL)
	}
}

func TestRunReindexPollsDurableOperationWithoutPersistingResponse(t *testing.T) {
	var operationCalls int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/api/bases/base-a/reindex":
			_, _ = writer.Write([]byte(`{"ok":true,"value":{"jobId":"op-1","operationId":"op-1","submitted":true}}`))
		case request.Method == http.MethodGet && request.URL.Path == "/api/operations/op-1":
			operationCalls++
			state := "running"
			if operationCalls > 1 {
				state = "succeeded"
			}
			_, _ = writer.Write([]byte(`{"ok":true,"value":{"id":"op-1","type":"reindex_base","baseId":"base-a","state":"` + state + `","runTimeMs":3}}`))
		case request.Method == http.MethodGet && request.URL.Path == "/api/operations/op-1/events":
			_, _ = writer.Write([]byte(`{"ok":true,"value":{"events":[{"id":1,"stateRevision":1,"kind":"queued","createdAt":10},{"id":2,"stateRevision":2,"kind":"phase","createdAt":11,"payload":{"phase":"reindexing","completed":2,"total":5}},{"id":3,"stateRevision":3,"kind":"succeeded","createdAt":12}]}}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	r := &runner{
		baseURL:          server.URL,
		client:           server.Client(),
		requestTimeout:   time.Second,
		pollInterval:     time.Millisecond,
		operationTimeout: time.Second,
		report: scenarioReport{
			Status:    "passed",
			Summaries: map[string]requestSummary{},
		},
	}
	if err := r.runReindex(context.Background(), "single-reindex", "base-a"); err != nil {
		t.Fatal(err)
	}
	if len(r.report.Operations) != 1 || r.report.Operations[0].State != "succeeded" {
		t.Fatalf("operations = %+v", r.report.Operations)
	}
	trace := r.report.Operations[0].EventTrace
	if len(trace) != 3 || trace[0].Kind != "queued" || trace[1].Kind != "phase" || trace[2].Kind != "succeeded" || trace[1].Phase != "reindexing" || trace[1].Completed != 2 || trace[1].Total != 5 || trace[1].ElapsedSincePrevious != 1 {
		t.Fatalf("event trace = %+v", trace)
	}
	if got := r.report.Operations[0].EventKinds; len(got) != 3 || got[0] != "phase" || got[1] != "queued" || got[2] != "succeeded" {
		t.Fatalf("event kinds = %+v", got)
	}
	if len(r.report.Requests) < 2 || r.report.Requests[0].OperationID != "op-1" {
		t.Fatalf("requests = %+v", r.report.Requests)
	}
}

func TestEventTraceKeepsOrderAndOnlyKnownProgressFields(t *testing.T) {
	trace := eventTrace(map[string]any{"events": []any{
		map[string]any{"id": 7.0, "stateRevision": 2.0, "kind": "submitted", "createdAt": 11.0, "payload": map[string]any{"secret": "drop"}},
		map[string]any{"id": 8.0, "stateRevision": 3.0, "kind": "phase", "createdAt": 12.0, "payload": map[string]any{"phase": "embedding", "completed": 2.0, "total": 5.0, "content": "drop"}},
	}})
	if len(trace) != 2 || trace[0].Kind != "submitted" || trace[1].Phase != "embedding" || trace[1].Completed != 2 || trace[1].Total != 5 || trace[1].ElapsedSincePrevious != 1 {
		t.Fatalf("trace = %+v", trace)
	}
}
