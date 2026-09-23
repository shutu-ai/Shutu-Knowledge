package schemamodel

import (
	"strings"
	"testing"
)

func TestCompileDocumentDetectsGenericDictionary(t *testing.T) {
	input := DocumentInput{
		BaseID: "base", DocumentID: "doc", Generation: 7, Title: "Dictionary.xlsx",
		SheetName: "Subscriber", SourcePath: "fixture.xlsx", Directory: "telecom",
		Rows: []SourceRow{
			{NodeID: "title", Text: "Subscriber definition", Range: "A1"},
			{NodeID: "header", Text: strings.Join([]string{"字段名称", "数据类型", "字段含义", "枚举值"}, "\t"), Range: "A2:D2"},
			{NodeID: "row1", Text: strings.Join([]string{"user_id", "STRING", "Unique user identifier", ""}, "\t"), Range: "A3:D3"},
			{NodeID: "row2", Text: strings.Join([]string{"status", "STRING", "User status", "1:active;2:inactive"}, "\t"), Range: "A4:D4"},
		},
	}
	model, detected := CompileDocument(input)
	if !detected {
		t.Fatal("dictionary was not detected")
	}
	if err := model.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if len(model.Tables) != 1 || len(model.Fields) != 2 {
		t.Fatalf("model counts: tables=%d fields=%d", len(model.Tables), len(model.Fields))
	}
	table := model.Tables[0]
	if table.Name != "Subscriber definition" || table.RegionAnchor != "A2:D2" {
		t.Fatalf("table identity/provenance: %+v", table)
	}
	if table.Source.Range != "A2:D2" || table.Source.Granularity != RangeProvenance {
		t.Fatalf("table source: %+v", table.Source)
	}
	var status Field
	for _, field := range model.Fields {
		if field.Name == "status" {
			status = field
		}
	}
	if status.ID == "" || status.DataType != "STRING" || status.Description != "User status" {
		t.Fatalf("status field: %+v", status)
	}
	if status.Enum == nil || len(status.Enum.Values) != 2 || status.Enum.Values[0].Label != "active" {
		t.Fatalf("status enum: %+v", status.Enum)
	}
	if status.Source.Range != "A4:D4" || status.Source.NodeID != "row2" {
		t.Fatalf("status provenance: %+v", status.Source)
	}
}

func TestCompileDocumentPreservesMultiRowHeaderAndSameNameScope(t *testing.T) {
	base := DocumentInput{BaseID: "base", DocumentID: "doc", Generation: 3, Title: "Workbook.xlsx", SheetName: "Mixed"}
	header := strings.Join([]string{"字段信息", "数据类型", "字段含义"}, "\t")
	row := strings.Join([]string{"status", "STRING", "state"}, "\t")
	input := base
	input.Rows = []SourceRow{
		{NodeID: "category", Text: "基本信息", Range: "A1"},
		{NodeID: "header", Text: header, Range: "A2:C2"},
		{NodeID: "data", Text: row, Range: "A3:C3"},
		{NodeID: "title2", Text: "Network state", Range: "A5"},
		{NodeID: "header2", Text: header, Range: "A6:C6"},
		{NodeID: "data2", Text: row, Range: "A7:C7"},
	}
	model, detected := CompileDocument(input)
	if !detected || len(model.Tables) != 2 || len(model.Fields) != 2 {
		t.Fatalf("detected=%v tables=%d fields=%d", detected, len(model.Tables), len(model.Fields))
	}
	if model.Tables[0].Name == model.Tables[1].Name {
		t.Fatalf("distinct regions incorrectly shared title: %+v", model.Tables)
	}
	if model.Fields[0].ID == model.Fields[1].ID || model.Fields[0].Identity() == model.Fields[1].Identity() {
		t.Fatalf("same-name fields were merged: %+v", model.Fields)
	}
	for _, field := range model.Fields {
		if field.Source.Range == "" || field.Scope.Table == "" {
			t.Fatalf("field lost identity/provenance: %+v", field)
		}
	}
}

func TestCompileDocumentRejectsOrdinaryTable(t *testing.T) {
	input := DocumentInput{
		BaseID: "base", DocumentID: "finance", Generation: 1, Title: "finance.xlsx", SheetName: "Sales",
		Rows: []SourceRow{
			{Text: strings.Join([]string{"Name", "Amount", "Date"}, "\t"), Range: "A1:C1"},
			{Text: strings.Join([]string{"Alice", "100", "2026-01-01"}, "\t"), Range: "A2:C2"},
		},
	}
	model, detected := CompileDocument(input)
	if detected || len(model.Tables) != 0 || len(model.Fields) != 0 {
		t.Fatalf("ordinary table entered schema mode: detected=%v model=%+v", detected, model)
	}
}

func TestCompileDocumentDerivesCautiousJoinCandidate(t *testing.T) {
	input := DocumentInput{
		BaseID: "base", DocumentID: "doc", Generation: 4, Title: "dictionary.xlsx", SheetName: "Users",
		Rows: []SourceRow{
			{Text: strings.Join([]string{"字段名称", "数据类型", "字段含义", "主键"}, "\t"), Range: "A1:D1"},
			{Text: strings.Join([]string{"subscriber_id", "STRING", "Subscriber identifier", "Y"}, "\t"), Range: "A2:D2"},
			{Text: strings.Join([]string{"call_date", "DATE", "Call date", ""}, "\t"), Range: "A3:D3"},
		},
	}
	model, detected := CompileDocument(input)
	if !detected {
		t.Fatal("dictionary not detected")
	}
	if len(model.KeyCandidates) == 0 {
		t.Fatal("no key candidates")
	}
	for _, candidate := range model.KeyCandidates {
		if strings.Contains(strings.ToLower(candidate.Reason), "must join") || strings.Contains(strings.ToLower(candidate.Reason), "foreign key") {
			t.Fatalf("candidate made unsupported constraint claim: %+v", candidate)
		}
	}
	var foundPrimary, foundTime bool
	for _, candidate := range model.KeyCandidates {
		switch candidate.Type {
		case KeyPrimary:
			foundPrimary = true
		case KeyTime:
			foundTime = true
		}
	}
	if !foundPrimary || !foundTime {
		t.Fatalf("expected primary/time candidates, got %+v", model.KeyCandidates)
	}
}

func TestFieldIdentityRequiresTable(t *testing.T) {
	scope := Scope{Workbook: "w", Sheet: "s", Table: "table_a"}
	left := Field{TableName: "table_a", TableID: "t-a", Scope: scope, Name: "status"}
	right := Field{TableName: "table_b", TableID: "t-b", Scope: scope, Name: "status"}
	if left.Identity() == right.Identity() {
		t.Fatal("same-name fields in different tables have identical identity")
	}
}
