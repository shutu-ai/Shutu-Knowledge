package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shutu-ai/shutu-knowledge/internal/jobs"
	"github.com/shutu-ai/shutu-knowledge/internal/knowledge"
)

func TestCachedWebAssetsRevalidateAfterRestartAndThroughProxy(t *testing.T) {
	s := newTestServer(t)
	assertCacheRoundTrip := func(handler http.Handler, target string) string {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, target, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-cache" {
			t.Fatalf("%s first response: code=%d headers=%v", target, rec.Code, rec.Header())
		}
		etag := rec.Header().Get("ETag")
		if len(etag) < 3 {
			t.Fatalf("%s missing build ETag: %v", target, rec.Header())
		}
		req = httptest.NewRequest(http.MethodGet, target, nil)
		req.Header.Set("If-None-Match", etag)
		rec = httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotModified || rec.Body.Len() != 0 {
			t.Fatalf("%s revalidation: code=%d body=%q", target, rec.Code, rec.Body.String())
		}
		return etag
	}

	etag := assertCacheRoundTrip(s.srv.Handler, "/")
	if assetETag := assertCacheRoundTrip(s.srv.Handler, "/app.js"); assetETag != etag {
		t.Fatalf("build ETags differ: shell=%s asset=%s", etag, assetETag)
	}

	// A new Server object models the HTTP listener created by a restarted
	// process. The same embedded build must keep the same revalidation token;
	// a changed build ID invalidates every old cached document.
	restarted := New(s.app)
	t.Cleanup(func() { _ = restarted.Shutdown(context.Background()) })
	if restartETag := assertCacheRoundTrip(restarted.srv.Handler, "/"); restartETag != etag {
		t.Fatalf("restart changed cache identity: before=%s after=%s", etag, restartETag)
	}
	if len(webBuild.BuildID) != 64 {
		t.Fatalf("invalid web build id: %q", webBuild.BuildID)
	}

	versionReq := httptest.NewRequest(http.MethodGet, "/api/version", nil)
	versionRec := httptest.NewRecorder()
	s.srv.Handler.ServeHTTP(versionRec, versionReq)
	var version map[string]any
	if err := json.Unmarshal(versionRec.Body.Bytes(), &version); err != nil {
		t.Fatal(err)
	}
	if version["webBuild"] != webBuild.BuildID {
		t.Fatalf("version web build: %v, want %q", version["webBuild"], webBuild.BuildID)
	}

	// Extension hosts strip their contribution prefix before forwarding. The
	// browser cache headers must survive so a cached contribution shell can
	// revalidate against the standalone backend's build identity.
	prefix := "/extensions/knowledge"
	proxy := http.NewServeMux()
	proxy.Handle(prefix+"/", http.StripPrefix(prefix, s.srv.Handler))
	if proxyETag := assertCacheRoundTrip(proxy, prefix+"/app.js"); proxyETag != etag {
		t.Fatalf("proxy changed cache identity: standalone=%s proxy=%s", etag, proxyETag)
	}
	versionReq = httptest.NewRequest(http.MethodGet, prefix+"/api/version", nil)
	versionRec = httptest.NewRecorder()
	proxy.ServeHTTP(versionRec, versionReq)
	if err := json.Unmarshal(versionRec.Body.Bytes(), &version); err != nil {
		t.Fatal(err)
	}
	if version["webBuild"] != webBuild.BuildID {
		t.Fatalf("proxied version web build: %v, want %q", version["webBuild"], webBuild.BuildID)
	}
}

func TestLegacyJobClientThroughExtensionProxyPrefix(t *testing.T) {
	s := newTestServer(t)
	prefix := "/extensions/knowledge"
	proxy := http.NewServeMux()
	proxy.Handle(prefix+"/", http.StripPrefix(prefix, s.srv.Handler))
	request := func(method, target string, body any) (int, map[string]any) {
		t.Helper()
		var reader *strings.Reader
		if body == nil {
			reader = strings.NewReader("")
		} else {
			encoded, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			reader = strings.NewReader(string(encoded))
		}
		req := httptest.NewRequest(method, prefix+target, reader)
		rec := httptest.NewRecorder()
		proxy.ServeHTTP(rec, req)
		var payload map[string]any
		if rec.Body.Len() > 0 {
			if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
				t.Fatalf("decode %s: %v", target, err)
			}
		}
		return rec.Code, payload
	}

	code, payload := request(http.MethodGet, "/api/status", nil)
	if code != http.StatusOK {
		t.Fatalf("proxied status: %d %v", code, payload)
	}
	code, payload = request(http.MethodPost, "/api/bases", map[string]any{"name": "Legacy"})
	if code != http.StatusOK {
		t.Fatalf("proxied base creation: %d %v", code, payload)
	}
	baseID := valueMap(t, payload)["id"].(string)
	code, payload = request(http.MethodPost, "/api/bases/"+baseID+"/documents", map[string]any{
		"title": "Legacy Note", "content": "legacy compatibility body",
	})
	if code != http.StatusAccepted {
		t.Fatalf("proxied document creation: %d %v", code, payload)
	}
	document := valueMap(t, payload)
	docID := document["documentId"].(string)
	createOperationID := document["operationId"].(string)
	deadline := time.Now().Add(5 * time.Second)
	for {
		code, payload = request(http.MethodGet, "/api/jobs/"+createOperationID, nil)
		if code != http.StatusOK {
			t.Fatalf("proxied document creation status: %d %v", code, payload)
		}
		if valueMap(t, payload)["status"] == jobs.StatusDone {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("proxied document creation timeout: %v", payload)
		}
		time.Sleep(10 * time.Millisecond)
	}
	code, payload = request(http.MethodPost, "/api/documents/"+docID+"/delete", nil)
	if code != http.StatusOK && code != http.StatusAccepted {
		t.Fatalf("proxied legacy delete job: %d %v", code, payload)
	}
	operation := valueMap(t, payload)
	jobID, _ := operation["jobId"].(string)
	if jobID == "" || jobID != operation["operationId"] {
		t.Fatalf("legacy jobId compatibility missing: %v", operation)
	}

	var snapshot map[string]any
	deadline = time.Now().Add(5 * time.Second)
	for {
		code, payload = request(http.MethodGet, "/api/jobs/"+jobID, nil)
		if code != http.StatusOK {
			t.Fatalf("legacy job status: %d %v", code, payload)
		}
		snapshot = valueMap(t, payload)
		if snapshot["status"] == jobs.StatusDone || snapshot["status"] == jobs.StatusFailed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("legacy job did not finish: %v", snapshot)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if snapshot["status"] != jobs.StatusDone || snapshot["id"] != jobID {
		t.Fatalf("legacy terminal job snapshot: %v", snapshot)
	}
	code, payload = request(http.MethodPost, "/api/jobs/"+jobID+"/cancel", map[string]any{})
	if code != http.StatusOK || valueMap(t, payload)["cancelled"] != true {
		t.Fatalf("legacy cancel compatibility: %d %v", code, payload)
	}

	req := httptest.NewRequest(http.MethodGet, prefix+"/", nil)
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Type"), "html") {
		t.Fatalf("proxied shell: status=%d type=%q", rec.Code, rec.Header().Get("Content-Type"))
	}
	req = httptest.NewRequest(http.MethodGet, prefix+"/app.js", nil)
	rec = httptest.NewRecorder()
	proxy.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Type"), "javascript") {
		t.Fatalf("proxied cached asset: status=%d type=%q", rec.Code, rec.Header().Get("Content-Type"))
	}
}

func TestLegacyBulkRoutesUseDurableOperations(t *testing.T) {
	s := newTestServer(t)
	request := func(method, target string, body any) (int, map[string]any) {
		t.Helper()
		var reader *strings.Reader
		if body == nil {
			reader = strings.NewReader("")
		} else {
			encoded, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			reader = strings.NewReader(string(encoded))
		}
		req := httptest.NewRequest(method, target, reader)
		rec := httptest.NewRecorder()
		s.srv.Handler.ServeHTTP(rec, req)
		var payload map[string]any
		if rec.Body.Len() > 0 {
			if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
				t.Fatalf("decode %s: %v", target, err)
			}
		}
		return rec.Code, payload
	}
	waitLegacyDone := func(jobID string) map[string]any {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			code, payload := request(http.MethodGet, "/api/jobs/"+jobID, nil)
			if code != http.StatusOK {
				t.Fatalf("legacy job status: %d %v", code, payload)
			}
			job := valueMap(t, payload)
			if job["status"] == jobs.StatusDone {
				return job
			}
			if job["status"] == jobs.StatusFailed {
				t.Fatalf("legacy job failed: %v", job)
			}
			if time.Now().After(deadline) {
				t.Fatalf("legacy job timeout: %v", job)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}

	code, payload := request(http.MethodPost, "/api/bases", map[string]any{"name": "Bulk Legacy"})
	if code != http.StatusOK {
		t.Fatalf("create base: %d %v", code, payload)
	}
	baseID := valueMap(t, payload)["id"].(string)
	ids := make([]string, 0, 2)
	for _, title := range []string{"Bulk One", "Bulk Two"} {
		ids = append(ids, createTextDocument(t, s, baseID, title, "bulk legacy operation body "+title))
	}

	code, payload = request(http.MethodPost, "/api/documents/reindex", map[string]any{"ids": ids})
	if code != http.StatusAccepted {
		t.Fatalf("bulk reindex status = %d, body=%v", code, payload)
	}
	reindex := valueMap(t, payload)
	if reindex["jobId"] == "" || reindex["operationId"] == "" || reindex["jobId"] != reindex["operationId"] {
		t.Fatalf("bulk reindex legacy contract: %v", reindex)
	}
	jobID := reindex["jobId"].(string)
	if _, err := s.app.Operations.Get(jobID); err != nil {
		t.Fatalf("bulk reindex is not durable: %v", err)
	}
	if job := waitLegacyDone(jobID); int(job["total"].(float64)) != 2 {
		t.Fatalf("bulk reindex totals: %v", job)
	}
	for _, id := range ids {
		if doc, _, err := s.app.Knowledge.GetDocument(id, false); err != nil || doc.Status != knowledge.StatusReady {
			t.Fatalf("reindexed document %s: %+v %v", id, doc, err)
		}
	}

	code, payload = request(http.MethodPost, "/api/documents/delete", map[string]any{"ids": ids})
	if code != http.StatusAccepted {
		t.Fatalf("bulk delete status = %d, body=%v", code, payload)
	}
	deleteOperation := valueMap(t, payload)
	deleteID := deleteOperation["jobId"].(string)
	if deleteOperation["operationId"] != deleteID {
		t.Fatalf("bulk delete legacy contract: %v", deleteOperation)
	}
	if _, err := s.app.Operations.Get(deleteID); err != nil {
		t.Fatalf("bulk delete is not durable: %v", err)
	}
	if job := waitLegacyDone(deleteID); int(job["total"].(float64)) != 2 {
		t.Fatalf("bulk delete totals: %v", job)
	}
	for _, id := range ids {
		if _, _, err := s.app.Knowledge.GetDocument(id, false); err != knowledge.ErrNotFound {
			t.Fatalf("document %s still readable after delete: %v", id, err)
		}
	}
}
