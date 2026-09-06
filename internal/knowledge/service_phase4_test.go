package knowledge

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shutu-ai/shutu-knowledge/internal/caption"
	"github.com/shutu-ai/shutu-knowledge/internal/config"
	"github.com/shutu-ai/shutu-knowledge/internal/parser"
)

func nativeTextPDF(t *testing.T) []byte {
	t.Helper()
	var offsets [6]int64
	build := func(body string) []byte {
		return []byte(body)
	}
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>",
		"<< /Length 55 >>\nstream\nBT /F1 24 Tf 72 720 Td (native OCR guard) Tj ET\nendstream",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n")
	for index, object := range objects {
		offsets[index+1] = int64(out.Len())
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", index+1, object)
	}
	xrefOffset := int64(out.Len())
	out.WriteString("xref\n0 6\n0000000000 65535 f \n")
	for index := 1; index <= 5; index++ {
		fmt.Fprintf(&out, "%010d 00000 n \n", offsets[index])
	}
	out.WriteString("trailer\n<< /Size 6 /Root 1 0 R >>\nstartxref\n")
	fmt.Fprintf(&out, "%d\n%%%%EOF\n", xrefOffset)
	return build(out.String())
}

// Gate H requires OCR failure isolation. This PDF has a usable text layer,
// while the helper is deliberately unavailable; import must keep the native
// text instead of panicking or losing the document.
func TestOCRFailureKeepsNativeText(t *testing.T) {
	service, _ := newSearchFixture(t)
	service.global.OCR.Mode = "forced"
	service.ocr = parser.NoopHelper{}
	document := &Document{FileName: "native.pdf"}

	text, _, err := service.parseFileContent(
		context.Background(), document, BaseConfig{}, nativeTextPDF(t),
	)
	if err != nil {
		t.Fatalf("OCR failure must not discard native text: %v", err)
	}
	if !bytes.Contains([]byte(text), []byte("native OCR guard")) {
		t.Fatalf("native text missing after OCR failure: %q", text)
	}
}

func fragmentedPDF(t *testing.T) []byte {
	t.Helper()

	// Four isolated text objects make the plain-text layer one glyph per
	// line; their vertical coordinates also prevent coordinate reassembly.
	var content bytes.Buffer
	for index, glyph := range []string{"R", "e", "b", "u"} {
		y := 720 - index*20
		fmt.Fprintf(&content, "BT /F1 12 Tf 72 %d Td (%s) Tj ET\n", y, glyph)
	}
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", content.Len(), content.String()),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n")
	offsets := make([]int64, len(objects)+1)
	for index, object := range objects {
		offsets[index+1] = int64(out.Len())
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", index+1, object)
	}
	xref := int64(out.Len())
	out.WriteString(fmt.Sprintf("xref\n0 %d\n0000000000 65535 f \n", len(objects)+1))
	for index := 1; index <= len(objects); index++ {
		fmt.Fprintf(&out, "%010d 00000 n \n", offsets[index])
	}
	out.WriteString(fmt.Sprintf("trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref))
	return out.Bytes()
}

func TestFragmentedPDFRequestsOCRButKeepsNativeText(t *testing.T) {
	service, _ := newSearchFixture(t)
	service.global.OCR.Mode = "auto"
	service.ocr = parser.NoopHelper{}
	document := &Document{FileName: "fragmented.pdf"}
	text, _, err := service.parseFileContent(
		context.Background(), document, BaseConfig{}, fragmentedPDF(t),
	)
	if err != nil {
		t.Fatalf("fragmented OCR failure must preserve native text: %v", err)
	}
	if got := strings.Join(strings.Fields(text), ""); got != "Rebu" {
		t.Fatalf("native text: %q", text)
	}
}

type recordingOCRHelper struct {
	calls []string
}

func (h *recordingOCRHelper) Available() bool { return true }

func (h *recordingOCRHelper) Run(_ context.Context, format string, _ []byte) (string, error) {
	h.calls = append(h.calls, format)
	if format != "png" {
		return "", fmt.Errorf("PDF rasterization is unavailable")
	}
	return "中 文", nil
}

func scannedImagePDF(t *testing.T) []byte {
	t.Helper()
	raw := bytes.Repeat([]byte{255, 255, 255}, 4)
	var compressed bytes.Buffer
	writer := zlib.NewWriter(&compressed)
	if _, err := writer.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	content := "q 20 0 0 20 10 10 cm /Im1 Do Q"
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /Resources << /XObject << /Im1 5 0 R >> >> /Contents 4 0 R >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content),
		fmt.Sprintf(
			"<< /Type /XObject /Subtype /Image /Width 2 /Height 2 /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /FlateDecode /Length %d >>\nstream\n%s\nendstream",
			compressed.Len(), compressed.String(),
		),
	}
	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n")
	offsets := make([]int64, len(objects)+1)
	for index, object := range objects {
		offsets[index+1] = int64(out.Len())
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", index+1, object)
	}
	xref := int64(out.Len())
	out.WriteString(fmt.Sprintf("xref\n0 %d\n0000000000 65535 f \n", len(objects)+1))
	for index := 1; index <= len(objects); index++ {
		fmt.Fprintf(&out, "%010d 00000 n \n", offsets[index])
	}
	out.WriteString(fmt.Sprintf("trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref))
	return out.Bytes()
}

func TestEmbeddedRasterFallbackRunsWhenPDFEnvelopeOCRFails(t *testing.T) {
	service, _ := newSearchFixture(t)
	service.global.OCR.Mode = "forced"
	helper := &recordingOCRHelper{}
	service.ocr = helper

	text, _, err := service.parseFileContent(
		context.Background(), &Document{FileName: "scanned.pdf"}, BaseConfig{}, scannedImagePDF(t),
	)
	if err != nil {
		t.Fatal(err)
	}
	if text != "中文" {
		t.Fatalf("embedded raster OCR text: %q", text)
	}
	if len(helper.calls) != 2 || helper.calls[0] != "pdf" || helper.calls[1] != "png" {
		t.Fatalf("OCR format calls: %q", helper.calls)
	}
}

func TestTesseractFallbackRunsAfterPrimaryRuntimeFailure(t *testing.T) {
	service, _ := newSearchFixture(t)
	t.Setenv("SHUTU_KNOWLEDGE_HELPER_PROCESS", "1")
	t.Setenv("SHUTU_KNOWLEDGE_HELPER_FORMAT", "pdf")
	service.global.OCR.Mode = "forced"
	service.global.OCR.FallbackHelper = knowledgeHelperCommand(t)
	service.SetOCRArtifactProvider(func() (string, bool) { return "/models/ocr", true })
	service.SetRuntime(&fakeKnowledgeRuntime{fail: true})
	if service.ocr == nil || !service.ocr.Available() {
		t.Fatal("OCR fallback chain was not selected")
	}

	text, _, err := service.parseFileContent(
		context.Background(), &Document{FileName: "fallback.pdf"}, BaseConfig{}, nativeTextPDF(t),
	)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(text) != "converted:pdf" {
		t.Fatalf("Tesseract fallback text: %q", text)
	}
}

type fakeContentHelper struct {
	format string
	text   string
	failed bool
}

func (h *fakeContentHelper) Available() bool { return true }

func (h *fakeContentHelper) Run(_ context.Context, format string, _ []byte) (string, error) {
	h.format = format
	if h.failed || h.text == "" {
		return "", fmt.Errorf("no content signature result")
	}
	return h.text, nil
}

func TestContentConverterFallbackForFragmentedText(t *testing.T) {
	service, _ := newSearchFixture(t)
	service.global.OCR.Mode = "auto"
	content := &fakeContentHelper{text: "# Recovered by signature"}
	service.content = content
	document := &Document{FileName: "fragmented.pdf"}

	text, _, err := service.parseFileContent(
		context.Background(), document, BaseConfig{}, fragmentedPDF(t),
	)
	if err != nil || text != "# Recovered by signature" {
		t.Fatalf("content fallback: %q %v", text, err)
	}
	if content.format != "pdf" {
		t.Fatalf("content format hint: %q", content.format)
	}
}

func TestContentConverterFallbackForEmptyText(t *testing.T) {
	service, _ := newSearchFixture(t)
	service.global.OCR.Mode = "auto"
	content := &fakeContentHelper{text: "# Signature wins"}
	service.content = content
	document := &Document{FileName: "empty.pdf"}

	text, _, err := service.parseFileContent(
		context.Background(), document, BaseConfig{}, []byte("%PDF-not-a-parser-input"),
	)
	if err != nil || text != "# Signature wins" {
		t.Fatalf("empty-text content fallback: %q %v", text, err)
	}
	if content.format != "pdf" {
		t.Fatalf("content format hint: %q", content.format)
	}
}

func TestImageCaptioningAppendsBestEffortText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"quarterly revenue chart"}}]}`))
	}))
	defer server.Close()

	service, _ := newSearchFixture(t)
	service.global.Captioning.Provider = "openai"
	service.global.Captioning.Model = "vision-model"
	service.global.Captioning.BaseURL = server.URL
	service.captionOptions = &caption.Options{
		HTTPClient: server.Client(),
		ExtractImages: func([]byte) ([]parser.PDFImage, error) {
			return []parser.PDFImage{{Page: 1, Width: 200, Height: 160, PNG: []byte("fake-png")}}, nil
		},
	}
	document := &Document{FileName: "figures.pdf"}

	got := service.appendImageCaptions(
		context.Background(), document, fragmentedPDF(t), "native revenue text",
	)
	if !strings.Contains(got, "native revenue text") ||
		!strings.Contains(got, "quarterly revenue chart") ||
		!strings.Contains(got, "第 1 页图表描述") {
		t.Fatalf("captioned text: %q", got)
	}
}

func TestImageCaptioningFailureKeepsParsedText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "vision unavailable", http.StatusBadGateway)
	}))
	defer server.Close()

	service, _ := newSearchFixture(t)
	service.global.Captioning.Provider = "openai"
	service.global.Captioning.Model = "vision-model"
	service.global.Captioning.BaseURL = server.URL
	service.captionOptions = &caption.Options{
		HTTPClient: server.Client(),
		ExtractImages: func([]byte) ([]parser.PDFImage, error) {
			return []parser.PDFImage{{Page: 1, Width: 200, Height: 160, PNG: []byte("fake-png")}}, nil
		},
	}
	document := &Document{FileName: "figures.pdf"}

	got := service.appendImageCaptions(
		context.Background(), document, fragmentedPDF(t), "native revenue text",
	)
	if got != "native revenue text" {
		t.Fatalf("failed captioning changed text: %q", got)
	}
}

func TestContentConverterFallbackAfterOCRSuccess(t *testing.T) {
	service, _ := newSearchFixture(t)
	service.global.OCR.Mode = "auto"
	service.ocr = fakeHelper{}
	content := &fakeContentHelper{text: "# Recovered by signature"}
	service.content = content
	document := &Document{FileName: "fragmented.pdf"}

	text, _, err := service.parseFileContent(
		context.Background(), document, BaseConfig{}, fragmentedPDF(t),
	)
	if err != nil || text != "converted:pdf" {
		t.Fatalf("OCR priority: %q %v", text, err)
	}
	if content.format != "" {
		t.Fatalf("content converter should not run after OCR success: %q", content.format)
	}
}

func TestURLImportAndRefresh(t *testing.T) {
	pageVersion := 1
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		page := fmt.Sprintf("<html><head><title>Page v%d</title></head><body><h1>Head v%d</h1><p>body text</p></body></html>", pageVersion, pageVersion)
		_, _ = w.Write([]byte(page))
	}))
	defer server.Close()

	service, _ := newSearchFixture(t)
	base, err := service.CreateBase("Web", "", "", BaseConfig{})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := service.AddUrlDocument(context.Background(), base.ID, server.URL+"/page", "")
	if err != nil {
		t.Fatalf("import url: %v", err)
	}
	if doc.SourceType != "url" || !strings.Contains(doc.URL, "/page") {
		t.Fatalf("doc metadata: %+v", doc)
	}
	if doc.Title != "Page v1" {
		t.Fatalf("title from page: %q", doc.Title)
	}
	if doc.ContentHash == "" || doc.ChunkCount == 0 {
		t.Fatalf("import state: %+v", doc)
	}

	// Unchanged refresh skips re-chunking.
	changed, refreshed, err := service.RefreshUrlDocument(context.Background(), doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if changed || refreshed.ContentHash != doc.ContentHash {
		t.Fatalf("unchanged refresh: changed=%v", changed)
	}

	// Changed content rebuilds the document (title and text follow v2).
	pageVersion = 2
	changed, refreshed, err = service.RefreshUrlDocument(context.Background(), doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !changed || refreshed.Title != "Page v2" {
		t.Fatalf("changed refresh: %v %+v", changed, refreshed)
	}
	chunks, err := service.ListChunks(doc.ID, 0, 0)
	if err != nil || len(chunks) == 0 || !strings.Contains(chunks[0].Context+chunks[0].Text, "Page v2") {
		t.Fatalf("refreshed chunks: %v", err)
	}

	// Non-URL documents refuse refresh.
	other, err := service.AddTextDocument(context.Background(), base.ID, "note", "text")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.RefreshUrlDocument(context.Background(), other.ID); err == nil {
		t.Fatal("text doc should refuse refresh")
	}
}

func TestDirectoryTreeIncrementalImport(t *testing.T) {
	service, _ := newSearchFixture(t)
	base, _ := service.CreateBase("Docs", "", "", BaseConfig{})
	root := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.md", "first document body")
	write("sub/b.txt", "second document body")
	write("skip.xyz", "unsupported format")

	jobID, err := service.ImportDirectoryTree(context.Background(), base.ID, root)
	if err != nil {
		t.Fatal(err)
	}
	waitJob(t, service, jobID)

	docs, err := service.ListDocuments(base.ID)
	if err != nil {
		t.Fatal(err)
	}
	titles := map[string]bool{}
	var subDir DocumentSummary
	for _, doc := range docs {
		titles[doc.Title] = true
		if doc.SourceType == "directory" {
			if doc.Title == "sub" {
				subDir = doc
			}
		}
	}
	if !titles["a.md"] || !titles["sub/b.txt"] || titles["skip.xyz"] || subDir.ID == "" {
		t.Fatalf("tree after first import: %+v", docs)
	}
	files, _ := service.ListDocuments(base.ID)
	for _, doc := range files {
		if doc.Title == "sub/b.txt" && doc.ParentDirID != subDir.ID {
			t.Fatalf("nested file parent: %+v", doc)
		}
	}

	// Incremental rescan: change a.md, add c.md, remove sub/b.txt.
	write("a.md", "first document body changed")
	write("c.md", "third document body")
	if err := os.Remove(filepath.Join(root, "sub", "b.txt")); err != nil {
		t.Fatal(err)
	}
	jobID, err = service.ImportDirectoryTree(context.Background(), base.ID, root)
	if err != nil {
		t.Fatal(err)
	}
	waitJob(t, service, jobID)

	docs, _ = service.ListDocuments(base.ID)
	seen := map[string]string{}
	for _, doc := range docs {
		if doc.SourceType == "file" {
			seen[doc.Title] = doc.Status
		}
	}
	if seen["a.md"] != StatusReady || seen["c.md"] != StatusReady {
		t.Fatalf("rescan state: %+v", seen)
	}
	if _, ok := seen["sub/b.txt"]; ok {
		t.Fatal("removed file should disappear")
	}
	docs, _ = service.ListDocuments(base.ID)
	var emptySub bool
	for _, doc := range docs {
		if doc.ID == subDir.ID && doc.SourceType == "directory" {
			emptySub = true
		}
	}
	if !emptySub {
		t.Fatal("existing nested directory should survive a rescan")
	}
	var changedDoc DocumentSummary
	for _, doc := range docs {
		if doc.Title == "a.md" {
			changedDoc = doc
		}
	}
	full, _, err := service.GetDocument(changedDoc.ID, false)
	if err != nil || !strings.Contains(full.RawText, "changed") {
		t.Fatalf("changed content not rebuilt: %v %q", err, full.RawText)
	}
}

func TestDirectoryRescanRepointFailureAndRecursiveDelete(t *testing.T) {
	service, _ := newSearchFixture(t)
	base, _ := service.CreateBase("Trees", "", "", BaseConfig{})
	rootA := t.TempDir()
	rootB := t.TempDir()
	write := func(root, rel, content string) {
		t.Helper()
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(rootA, "nested/old.txt", "old tracked content")
	jobID, err := service.ImportDirectoryTree(context.Background(), base.ID, rootA)
	if err != nil {
		t.Fatal(err)
	}
	waitJob(t, service, jobID)
	docs, err := service.ListDocuments(base.ID)
	if err != nil {
		t.Fatal(err)
	}
	var rootDoc DocumentSummary
	for _, doc := range docs {
		if doc.SourceType == "directory" && doc.SourcePath == rootA {
			rootDoc.ID = doc.ID
		}
	}
	if rootDoc.ID == "" {
		t.Fatalf("root directory was not tracked: %+v", docs)
	}

	// Repointing is explicit and non-destructive; the next rescan syncs.
	write(rootB, "new.txt", "new tracked content")
	if _, err := service.RepointSource(rootDoc.ID, filepath.Join(rootB, "missing")); err == nil {
		t.Fatal("repoint accepted a missing path")
	}
	repointed, err := service.RepointSource(rootDoc.ID, rootB)
	if err != nil || repointed.SourcePath != rootB {
		t.Fatalf("repoint directory: %+v %v", repointed, err)
	}
	jobID, err = service.RescanDirectory(rootDoc.ID)
	if err != nil {
		t.Fatal(err)
	}
	waitJob(t, service, jobID)
	docs, _ = service.ListDocuments(base.ID)
	byTitle := map[string]DocumentSummary{}
	for _, doc := range docs {
		byTitle[doc.Title] = doc
	}
	if _, ok := byTitle["old.txt"]; ok {
		t.Fatal("missing descendant survived rescan")
	}
	if doc := byTitle["new.txt"]; doc.Status != StatusReady || doc.SourcePath != filepath.Join(rootB, "new.txt") {
		t.Fatalf("new descendant: %+v", doc)
	}

	// Per-entry failures remain visible, while good entries still import.
	failureRoot := t.TempDir()
	write(failureRoot, "empty.md", "   ")
	write(failureRoot, "good.txt", "usable content")
	jobID, err = service.ImportDirectoryTree(context.Background(), base.ID, failureRoot)
	if err != nil {
		t.Fatal(err)
	}
	waitJobAllowFailed(t, service, jobID)
	docs, _ = service.ListDocuments(base.ID)
	byTitle = map[string]DocumentSummary{}
	for _, doc := range docs {
		byTitle[doc.Title] = doc
	}
	if failed := byTitle["empty.md"]; failed.Status != StatusFailed || failed.ErrorCode != ErrParseFailed {
		t.Fatalf("failed entry: %+v", failed)
	}
	if good := byTitle["good.txt"]; good.Status != StatusReady {
		t.Fatalf("good sibling: %+v", good)
	}

	// Deleting a directory recursively removes files beneath it.
	if _, err := service.DeleteDirectoryRecursive(rootDoc.ID); err != nil {
		t.Fatal(err)
	}
	docs, _ = service.ListDocuments(base.ID)
	for _, doc := range docs {
		if doc.ID == rootDoc.ID || doc.Title == "new.txt" {
			t.Fatalf("recursive delete left a descendant: %+v", doc)
		}
	}
}

func TestSemanticChunkingMergesAndFallsBack(t *testing.T) {
	service, _ := newSearchFixture(t)
	base, err := service.CreateBase("Semantic", "", "", BaseConfig{SemanticChunk: boolPtr(true), SemanticThreshold: 0.5, ChunkSize: 800})
	if err != nil {
		t.Fatal(err)
	}
	// Three segments: two coherent + one unrelated (distinct fake vector).
	content := "database paragraph one\n\ndatabase paragraph two\n\ncooking paragraph three"
	doc, err := service.AddTextDocument(context.Background(), base.ID, "Semantic Doc", content)
	if err != nil {
		t.Fatal(err)
	}
	chunks, err := service.ListChunks(doc.ID, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 2 {
		t.Fatalf("expected 2 merged chunks, got %d", len(chunks))
	}
	// Merged vectors are stored inline (dimensions visible in stats).
	stats, err := service.Stats(base.ID)
	if err != nil || !stats.Embedded || stats.Dimensions != 2 {
		t.Fatalf("inline vectors: %+v %v", stats, err)
	}

	// Fallback: provider failure degrades to structural chunking.
	service.SetProviders(&fakeEmbedder{model: "a", failNth: 99, dimSeq: []int{2}}, nil)
	broken := &fakeEmbedder{model: "a"}
	broken.failNth = 1
	service.SetProviders(broken, nil)
	fallbackDoc, err := service.AddTextDocument(context.Background(), base.ID, "Fallback", "database one\n\ndatabase two")
	if err != nil {
		t.Fatal(err)
	}
	if fallbackDoc.ChunkCount < 1 || fallbackDoc.ErrorCode != "" {
		t.Fatalf("fallback import: %+v", fallbackDoc)
	}
}

func TestLegacyHelperRegistration(t *testing.T) {
	fake := &fakeHelper{}
	registry := newTestRegistry(fake)
	result, err := registry.Parse("legacy.doc", []byte("binary"))
	if err != nil || result.Text != "converted:doc" {
		t.Fatalf("legacy helper parse: %+v %v", result, err)
	}
	// Without a helper the format stays unsupported.
	registry = newTestRegistry(nil)
	if _, err := registry.Parse("legacy.doc", []byte("binary")); err == nil {
		t.Fatal("expected unsupported without helper")
	}
	// ExecHelper without a template is unavailable.
	var runner fakeRunner = fakeHelper{}
	_ = runner
	exec := newExecHelper("")
	if exec.Available() {
		t.Fatal("empty template must be unavailable")
	}
}

type fakeHelper struct{}

func (fakeHelper) Available() bool { return true }
func (fakeHelper) Run(_ context.Context, format string, _ []byte) (string, error) {
	return "converted:" + format, nil
}

type fakeRunner interface {
	Available() bool
}

func TestMineruClientFlow(t *testing.T) {
	var uploadPath string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/file-urls/batch", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 0,
			"data": map[string]any{"batch_id": "b1", "file_urls": []string{"http://" + r.Host + "/upload"}},
		})
	})
	mux.HandleFunc("/api/v4/extract-results/batch/", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 0,
			"data": map[string]any{"extract_result": []map[string]any{{"state": "done", "full_zip_url": "http://" + r.Host + "/result.zip"}}},
		})
	})
	mux.HandleFunc("/upload", func(w http.ResponseWriter, r *http.Request) {
		uploadPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/result.zip", func(w http.ResponseWriter, _ *http.Request) {
		zipBytes := zipWithMarkdown()
		_, _ = w.Write(zipBytes)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	markdown, err := parser.ExtractPDFWithMineru(context.Background(), "scan.pdf", []byte("%PDF-fake"), parser.MineruSettings{
		APIKey:  "k",
		APIHost: server.URL,
		Client:  server.Client(),
	})
	if err != nil {
		t.Fatalf("mineru flow: %v", err)
	}
	if !strings.Contains(markdown, "# Recovered") {
		t.Fatalf("markdown: %q", markdown)
	}
	if uploadPath != "/upload" {
		t.Fatalf("upload path: %s", uploadPath)
	}
}

func TestOCRModeResolution(t *testing.T) {
	service, _ := newSearchFixture(t)
	service.global.OCR.Mode = "auto"
	if got := service.resolveOCRMode(BaseConfig{}); got != "auto" {
		t.Fatalf("global default: %s", got)
	}
	if got := service.resolveOCRMode(BaseConfig{OCRMode: "forced"}); got != "forced" {
		t.Fatalf("base override: %s", got)
	}
	if got := service.resolveOCRMode(BaseConfig{OCRMode: "off"}); got != "off" {
		t.Fatalf("base off: %s", got)
	}
	service.global.OCR.Mode = "off"
	if got := service.resolveOCRMode(BaseConfig{}); got != "off" {
		t.Fatalf("global off: %s", got)
	}
	if got := service.resolveOCRMode(BaseConfig{OCRMode: "weird"}); got != "auto" {
		t.Fatalf("invalid falls to auto: %s", got)
	}
	// Auto-refresh respects the per-base interval (0 = off).
	if failures := service.RefreshStaleURLs(context.Background(), time.Now().UnixMilli()); len(failures) != 0 {
		t.Fatalf("no bases with interval: %v", failures)
	}
}

func boolPointer(value bool) *bool { return &value }

func TestResolveBaseConfigLayering(t *testing.T) {
	global := config.Defaults()
	global.Processing.Provider = "mineru"
	global.Processing.APIKey = "global-mineru-key"
	global.Processing.APIHost = "https://global.mineru.example"
	global.Workflow.ConflictStrategy = "replace"
	global.Workflow.URLRefreshHours = 48
	global.AutoRetrieve.Enabled = false
	global.AutoRetrieve.Weight = 2

	resolved := ResolveBaseConfig(global, BaseConfig{})
	if resolved.Processor != "mineru" || resolved.MineruAPIKey != "global-mineru-key" ||
		resolved.MineruAPIHost != "https://global.mineru.example" {
		t.Fatalf("processing defaults were not inherited: %+v", resolved)
	}
	if resolved.ConflictStrategy != "replace" || resolved.URLRefreshHours != 48 {
		t.Fatalf("workflow defaults were not inherited: %+v", resolved)
	}
	if resolved.AutoRetrieve == nil || *resolved.AutoRetrieve ||
		resolved.AutoRetrieveMax == nil || *resolved.AutoRetrieveMax != 2 {
		t.Fatalf("auto-retrieve defaults were not inherited: %+v", resolved)
	}

	zero := 0
	resolved = ResolveBaseConfig(global, BaseConfig{
		Processor:        "builtin",
		ConflictStrategy: "keep",
		URLRefreshHours:  3,
		AutoRetrieve:     boolPointer(true),
		AutoRetrieveMax:  &zero,
	})
	if resolved.Processor != "builtin" || resolved.ConflictStrategy != "keep" ||
		resolved.URLRefreshHours != 3 || !*resolved.AutoRetrieve || *resolved.AutoRetrieveMax != 0 {
		t.Fatalf("explicit base overrides were not preserved: %+v", resolved)
	}

	nine := 9
	resolved = ResolveBaseConfig(global, BaseConfig{AutoRetrieveMax: &nine})
	if resolved.AutoRetrieveMax == nil || *resolved.AutoRetrieveMax != 5 {
		t.Fatalf("auto-retrieve weight was not clamped: %+v", resolved.AutoRetrieveMax)
	}
}
