package schemamodel

import (
	"strings"
	"testing"
)

func schemaModelForReasoning(t *testing.T, documentID string) Model {
	t.Helper()
	input := DocumentInput{
		BaseID: "base", DocumentID: documentID, Generation: 1, Title: documentID + ".xlsx",
		SheetName: "Dictionary",
		Rows: []SourceRow{
			{NodeID: "h", Text: strings.Join([]string{"字段名称", "数据类型", "字段含义"}, "\t"), Range: "A1:C1", Sheet: "Dictionary"},
			{NodeID: documentID + "-user", Text: strings.Join([]string{"user_id", "STRING", "User identifier"}, "\t"), Range: "A2:C2", Sheet: "Dictionary"},
			{NodeID: documentID + "-site", Text: strings.Join([]string{"site_id", "STRING", "Site identifier"}, "\t"), Range: "A3:C3", Sheet: "Dictionary"},
			{NodeID: documentID + "-traffic", Text: strings.Join([]string{"traffic_mb", "BIGINT", "Traffic volume"}, "\t"), Range: "A4:C4", Sheet: "Dictionary"},
		},
	}
	model, detected := CompileDocument(input)
	if !detected {
		t.Fatalf("%s was not detected", documentID)
	}
	if err := model.Validate(); err != nil {
		t.Fatal(err)
	}
	return model
}

func TestSearchModelsFindsSemanticBusinessConcept(t *testing.T) {
	model := schemaModelForReasoning(t, "doc")
	response := SearchModels([]Model{model}, "哪些字段表示用户标识？", 10)
	if len(response.Fields) == 0 || response.Fields[0].Field == nil || response.Fields[0].Field.Name != "user_id" {
		t.Fatalf("semantic field response: %+v", response)
	}
	if len(response.Concepts) == 0 || response.Concepts[0] != "User Identifier" {
		t.Fatalf("concept mapping: %+v", response.Concepts)
	}
	if response.Fields[0].Source.Range != "A2:C2" {
		t.Fatalf("semantic result provenance: %+v", response.Fields[0].Source)
	}
}

func TestCrossWorkbookConceptIntersectionUsesSetReasoning(t *testing.T) {
	models := []Model{schemaModelForReasoning(t, "doc-a"), schemaModelForReasoning(t, "doc-b")}
	result := ResolveConceptSets(models, "哪些表同时存在用户标识、站点和流量字段？", 20)
	if len(result.CandidateTables) != 2 {
		t.Fatalf("candidate tables: %+v", result.CandidateTables)
	}
	for _, table := range result.CandidateTables {
		if len(table.ConceptFields) != 3 {
			t.Fatalf("table does not contain all concepts: %+v", table)
		}
	}
	if len(result.Workbooks) != 2 {
		t.Fatalf("workbook grouping: %+v", result.Workbooks)
	}
}

func TestCandidateJoinsRemainProbabilistic(t *testing.T) {
	models := []Model{schemaModelForReasoning(t, "doc-a"), schemaModelForReasoning(t, "doc-b")}
	left := models[0].Tables[0].ID
	right := models[1].Tables[0].ID
	if left == right {
		t.Fatalf("expected distinct tables: %s/%s", left, right)
	}
	candidates := CandidateJoins(models, left, right, 5)
	if len(candidates) == 0 {
		t.Fatal("expected join candidates")
	}
	for _, candidate := range candidates {
		if strings.Contains(strings.ToLower(candidate.Reason), "must join") || candidate.Confidence > 0.85 {
			t.Fatalf("unsupported constraint claim: %+v", candidate)
		}
		if len(candidate.Evidence) == 0 {
			t.Fatalf("candidate lacks evidence: %+v", candidate)
		}
	}
}

func TestRequirementResolverExposesUnknownsAndNoSQL(t *testing.T) {
	models := []Model{schemaModelForReasoning(t, "doc")}
	result := ResolveDataRequirement(models, "过去30天有流量但没有语音业务的用户", 10, 5)
	if len(result.CandidateTables) == 0 {
		t.Fatalf("requirement result: %+v", result)
	}
	if len(result.Unknowns) == 0 {
		t.Fatal("resolver omitted unknowns")
	}
	if strings.Contains(strings.ToLower(result.Context), "select ") || strings.Contains(strings.ToLower(result.Context), "sql") {
		t.Fatalf("resolver generated SQL: %s", result.Context)
	}
	for _, diagnostic := range result.Diagnostics {
		if diagnostic == "sql_generation=disabled" {
			return
		}
	}
	t.Fatalf("missing SQL boundary diagnostic: %+v", result.Diagnostics)
}
