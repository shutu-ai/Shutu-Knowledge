package web

import (
	"context"
	"net/http"
	"testing"

	"github.com/shutu-ai/shutu-knowledge/internal/knowledge"
)

func TestSemanticAPICompileSearchContextWikiAndProvenance(t *testing.T) {
	server := newTestServer(t)
	base, err := server.app.Knowledge.CreateBase("Semantic API", "", "", knowledge.BaseConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.app.Knowledge.AddTextDocument(context.Background(), base.ID, "Vector Retrieval",
		"# Vector Retrieval\n\nThe vector service uses 1024-dimensional vectors."); err != nil {
		t.Fatal(err)
	}

	code, payload := call(t, server, "GET", "/api/bases/"+base.ID+"/semantic", nil)
	if code != http.StatusNotFound {
		t.Fatalf("uncompiled semantic status = %d body=%v", code, payload)
	}

	code, payload = call(t, server, "POST", "/api/bases/"+base.ID+"/semantic/compile", map[string]any{})
	if code != http.StatusOK {
		t.Fatalf("compile status = %d body=%v", code, payload)
	}
	compiled := valueMap(t, payload)
	if compiled["state"] != "active" {
		t.Fatalf("compiled state = %v", compiled["state"])
	}

	code, payload = call(t, server, "POST", "/api/bases/"+base.ID+"/semantic/search", map[string]any{
		"query": "vector retrieval", "topK": 5,
	})
	if code != http.StatusOK {
		t.Fatalf("semantic search status = %d body=%v", code, payload)
	}
	searchResult := valueMap(t, payload)
	if searchResult["routing"] == nil {
		t.Fatalf("semantic search lacks routing diagnostics: %v", searchResult)
	}

	code, payload = call(t, server, "POST", "/api/bases/"+base.ID+"/semantic/context", map[string]any{
		"query": "The vector service uses 1024-dimensional vectors.", "tokenBudget": 2048,
	})
	if code != http.StatusOK {
		t.Fatalf("semantic context status = %d body=%v", code, payload)
	}
	contextPackage := valueMap(t, payload)
	if contextPackage["routing"] == nil || contextPackage["renderedContext"] == "" {
		t.Fatalf("semantic context package = %v", contextPackage)
	}

	code, payload = call(t, server, "GET", "/api/bases/"+base.ID+"/semantic/wiki", nil)
	if code != http.StatusOK {
		t.Fatalf("semantic wiki status = %d body=%v", code, payload)
	}
	wiki := valueMap(t, payload)
	if wiki["regenerable"] != true {
		t.Fatalf("semantic wiki = %v", wiki)
	}
	wikiPages, _ := wiki["pages"].([]any)
	if len(wikiPages) == 0 || wikiPages[0].(map[string]any)["markdown"] == "" {
		t.Fatalf("semantic wiki markdown = %v", wiki["pages"])
	}

	searchValue := searchResult["hits"].([]any)
	if len(searchValue) == 0 {
		t.Fatal("semantic search returned no hits")
	}
	firstHit := searchValue[0].(map[string]any)
	unit := firstHit["unit"].(map[string]any)
	unitID := unit["id"].(string)
	code, payload = call(t, server, "GET", "/api/semantic/units/"+unitID+"/evidence", nil)
	if code != http.StatusOK {
		t.Fatalf("semantic evidence status = %d body=%v", code, payload)
	}
	sources, ok := payload["value"].([]any)
	if !ok || len(sources) == 0 {
		t.Fatalf("semantic evidence = %v", payload["value"])
	}
}
