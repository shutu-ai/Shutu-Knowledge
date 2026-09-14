package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shutu-ai/shutu-knowledge/internal/version"
)

func TestVersionEndpointExposesBuildEnvelope(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/version", nil)

	(&Server{}).handleVersion(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	var got struct {
		version.Build
		WebBuild string `json:"webBuild"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Build != version.Current() {
		t.Fatalf("build = %+v, want %+v", got.Build, version.Current())
	}
	if len(got.WebBuild) != 64 {
		t.Fatalf("web build = %q, want a SHA-256 identity", got.WebBuild)
	}
}
