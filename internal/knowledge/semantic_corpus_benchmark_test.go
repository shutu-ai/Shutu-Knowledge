package knowledge

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shutu-ai/shutu-knowledge/internal/semantic"
)

func TestSemanticCompilerCoversDocumentIntelligenceCorpus(t *testing.T) {
	service := newSemanticMemoryTestService(t)
	defer service.close()
	ctx := context.Background()
	base, err := service.CreateBase("Document Intelligence Semantic", "", "", BaseConfig{})
	if err != nil {
		t.Fatal(err)
	}
	files := []string{
		"english.md",
		"中文.md",
		filepath.Join("office", "structured.docx"),
		filepath.Join("office", "presentation.pptx"),
		filepath.Join("office", "spreadsheet.xlsx"),
	}
	ids := map[string]string{}
	for _, name := range files {
		data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "document_intelligence", filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		document, err := service.AddFileDocument(ctx, base.ID, filepath.Base(name), data, "")
		if err != nil {
			t.Fatal(err)
		}
		ids[name] = document.ID
	}
	compiled, err := service.CompileSemanticMemory(ctx, base.ID)
	if err != nil {
		t.Fatal(err)
	}
	summaries := 0
	provenanced := 0
	for _, unit := range compiled.Units {
		if unit.Type != semantic.UnitSummary {
			continue
		}
		summaries++
		if len(unit.ID) > 0 {
			provenanced++
		}
	}
	if summaries != len(files) || provenanced != len(files) {
		t.Fatalf("document corpus summaries=%d provenanced=%d, want %d", summaries, provenanced, len(files))
	}
	pkg, err := service.CompileKnowledgeContext(ctx, base.ID, "Summarize the document intelligence corpus", 2048)
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Routing == nil || pkg.Routing.Intent != semantic.IntentGlobal || len(pkg.Evidence) == 0 {
		t.Fatalf("document corpus context = %+v", pkg)
	}
	for _, evidence := range pkg.Evidence {
		if evidence.DocumentID == "" || evidence.ChunkID == "" {
			t.Fatalf("document corpus context lost exact chunk evidence: %+v", evidence)
		}
	}
}

func TestSemanticCompilerCoversSmartCareTelecomScenario(t *testing.T) {
	service := newSemanticMemoryTestService(t)
	defer service.close()
	ctx := context.Background()
	base, err := service.CreateBase("SmartCare Telecom Semantic", "", "", BaseConfig{})
	if err != nil {
		t.Fatal(err)
	}
	corpus := []struct{ title, text string }{
		{
			"SmartCare Dictionary 5.2",
			"# CCA Offset Registry\n\nVersion 5.2 supports the CCA_OFFSET restricted token. The CCA offset registry affects handover KQI aggregation.",
		},
		{
			"SmartCare Dictionary 6.0",
			"# CCA Offset Registry\n\nVersion 6.0 supports the CCA_OFFSET_V2 restricted token. The CCA offset registry affects handover KQI aggregation.",
		},
		{
			"Handover KQI Model",
			"# Handover KQI Aggregation\n\nHandover KQI aggregation uses the N2 interface. The N2 interface depends on AMF relocation.",
		},
		{
			"3GPP Causal Chain",
			"# AMF Relocation\n\nAMF relocation causes handover delay. Handover delay affects the handover KQI.",
		},
	}
	for _, item := range corpus {
		if _, err := service.AddTextDocument(ctx, base.ID, item.title, item.text); err != nil {
			t.Fatal(err)
		}
	}
	compiled, err := service.CompileSemanticMemory(ctx, base.ID)
	if err != nil {
		t.Fatal(err)
	}
	var currentVersion, supersededVersion bool
	for _, unit := range compiled.Units {
		content := strings.ToLower(unit.Content)
		if unit.Type != semantic.UnitFact || !strings.Contains(content, "restricted token") {
			continue
		}
		switch {
		case strings.Contains(content, "cca_offset_v2"):
			currentVersion = unit.Status == semantic.UnitActive
		case strings.Contains(content, "cca_offset") && !strings.Contains(content, "v2"):
			supersededVersion = unit.Status == semantic.UnitSuperseded && unit.SupersededBy != ""
		}
	}
	if !currentVersion || !supersededVersion {
		t.Fatalf("SmartCare version lifecycle current=%v superseded=%v", currentVersion, supersededVersion)
	}

	pkg, err := service.CompileKnowledgeContext(ctx, base.ID,
		"How does the CCA offset registry affect handover KQI aggregation?", 2048)
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Routing == nil || pkg.Routing.Intent != semantic.IntentMultiHop {
		t.Fatalf("SmartCare routing = %+v", pkg.Routing)
	}
	facts := strings.ToLower(strings.Join(pkg.Facts, "\n"))
	for _, required := range []string{
		"the cca offset registry affects handover kqi aggregation",
		"handover kqi aggregation uses the n2 interface",
		"the n2 interface depends on amf relocation",
		"amf relocation causes handover delay",
	} {
		if !strings.Contains(facts, required) {
			t.Fatalf("SmartCare causal chain missing %q in %q", required, facts)
		}
	}
	if len(pkg.Evidence) == 0 || len(pkg.Citations) == 0 {
		t.Fatalf("SmartCare context lacks evidence/citations: %+v", pkg)
	}
}

func TestSemanticCompilerCoversCodeKnowledgeScenario(t *testing.T) {
	service := newSemanticMemoryTestService(t)
	defer service.close()
	ctx := context.Background()
	base, err := service.CreateBase("Code Knowledge Semantic", "", "", BaseConfig{})
	if err != nil {
		t.Fatal(err)
	}
	corpus := []struct{ title, text string }{
		{
			"API Design",
			"# Operation API\n\nThe operation API submits durable writes through storage.Writer. storage.Writer serializes SQLite transactions.",
		},
		{
			"Storage Design",
			"# Storage Writer\n\nstorage.Writer owns one writer connection. The single writer avoids SQLite lock storms.",
		},
		{
			"Implementation Guide",
			"# Operation Implementation\n\nOperation retry uses the committed command key. The implementation depends on storage.Writer serialization.",
		},
	}
	for _, item := range corpus {
		if _, err := service.AddTextDocument(ctx, base.ID, item.title, item.text); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := service.CompileSemanticMemory(ctx, base.ID); err != nil {
		t.Fatal(err)
	}
	pkg, err := service.CompileKnowledgeContext(ctx, base.ID,
		"Why does the operation API use storage.Writer?", 2048)
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Routing == nil || pkg.Routing.Intent != semantic.IntentMultiHop {
		t.Fatalf("code knowledge routing = %+v", pkg.Routing)
	}
	facts := strings.ToLower(strings.Join(pkg.Facts, "\n"))
	for _, required := range []string{
		"the operation api submits durable writes through storage.writer",
		"storage.writer serializes sqlite transactions",
		"the single writer avoids sqlite lock storms",
	} {
		if !strings.Contains(facts, required) {
			t.Fatalf("code knowledge chain missing %q in %q", required, facts)
		}
	}
	for _, evidence := range pkg.Evidence {
		if evidence.ChunkID == "" {
			t.Fatalf("code knowledge evidence is not exact: %+v", evidence)
		}
	}
}
