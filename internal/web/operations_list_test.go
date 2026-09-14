package web

import (
	"net/http"
	"strings"
	"testing"
)

func TestOperationListWithoutRowsReturnsJSONArray(t *testing.T) {
	s := newTestServer(t)

	code, _, raw := callRaw(t, s, "GET", "/api/operations?state=queued&limit=10", nil)
	if code != http.StatusOK {
		t.Fatalf("list status = %d, body=%s", code, raw)
	}
	if !strings.Contains(string(raw), `"operations":[]`) {
		t.Fatalf("empty operations JSON is not an array: %s", raw)
	}
}
