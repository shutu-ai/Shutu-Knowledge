package web

import (
	"net/http"
	"testing"
)

func TestDocumentChildrenPageIsBounded(t *testing.T) {
	s := newTestServer(t)
	_, basePayload := call(t, s, "POST", "/api/bases", map[string]any{"name": "Children"})
	baseID := valueMap(t, basePayload)["id"].(string)
	for index := range [2]struct{}{} {
		createTextDocument(t, s, baseID, "root "+string(rune('a'+index)), "bounded child body")
	}
	code, payload := call(t, s, "POST", "/api/bases/"+baseID+"/directories", map[string]any{"title": "Folder"})
	if code != http.StatusOK {
		t.Fatalf("create directory: %d %v", code, payload)
	}
	folderID := valueMap(t, payload)["id"].(string)
	code, _ = call(t, s, "POST", "/api/bases/"+baseID+"/directories", map[string]any{
		"title": "nested", "parentDirectoryId": folderID,
	})
	if code != http.StatusOK {
		t.Fatalf("create nested document: %d", code)
	}

	code, payload = call(t, s, "GET", "/api/bases/"+baseID+"/documents/children?limit=1", nil)
	if code != http.StatusOK {
		t.Fatalf("root children: %d %v", code, payload)
	}
	root := valueMap(t, payload)
	if root["total"].(float64) != 3 || root["limit"].(float64) != 1 || root["hasMore"] != true {
		t.Fatalf("root page: %v", root)
	}
	if documents := root["documents"].([]any); len(documents) != 1 {
		t.Fatalf("root document count: %v", documents)
	}
	if crumbs := root["breadcrumbs"].([]any); len(crumbs) != 0 {
		t.Fatalf("root breadcrumbs: %v", crumbs)
	}

	code, payload = call(t, s, "GET", "/api/bases/"+baseID+"/documents/children?parentId="+folderID, nil)
	if code != http.StatusOK {
		t.Fatalf("folder children: %d %v", code, payload)
	}
	folder := valueMap(t, payload)
	if folder["total"].(float64) != 1 || folder["hasMore"] != false {
		t.Fatalf("folder page: %v", folder)
	}
	if documents := folder["documents"].([]any); len(documents) != 1 {
		t.Fatalf("folder document count: %v", documents)
	}
	if crumbs := folder["breadcrumbs"].([]any); len(crumbs) != 1 {
		t.Fatalf("folder breadcrumbs: %v", crumbs)
	}

	code, _ = call(t, s, "GET", "/api/bases/"+baseID+"/documents/children?parentId=missing", nil)
	if code != http.StatusNotFound {
		t.Fatalf("missing parent: %d", code)
	}
}
