package knowledge

import (
	"bytes"
	"compress/zlib"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/shutu-ai/shutu-knowledge/internal/chunk"
)

// pdfFixture builds a minimal single-page PDF around one content stream.
func pdfFixture(t *testing.T, content string) []byte {
	t.Helper()
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n")
	offsets := make([]int64, len(objects)+1)
	for index, object := range objects {
		offsets[index+1] = int64(out.Len())
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", index+1, object)
	}
	xrefOffset := int64(out.Len())
	out.WriteString(fmt.Sprintf("xref\n0 %d\n0000000000 65535 f \n", len(objects)+1))
	for index := 1; index <= len(objects); index++ {
		fmt.Fprintf(&out, "%010d 00000 n \n", offsets[index])
	}
	out.WriteString(fmt.Sprintf("trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xrefOffset))
	return out.Bytes()
}

// scannedImagePDFPages builds an image-only PDF with n pages.
func scannedImagePDFPages(t *testing.T, n int) []byte {
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

	kids := make([]string, 0, n)
	pageObjects := make([]string, 0, n)
	imageObject := fmt.Sprintf(
		"<< /Type /XObject /Subtype /Image /Width 2 /Height 2 /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /FlateDecode /Length %d >>\nstream\n%s\nendstream",
		compressed.Len(), compressed.String(),
	)
	for i := 0; i < n; i++ {
		pageObjNum := 5 + 2*i // image object then page object
		kids = append(kids, fmt.Sprintf("%d 0 R", pageObjNum+1))
		imageNum := pageObjNum
		pageObjects = append(pageObjects, fmt.Sprintf(
			"<< /Type /Page /Parent 2 0 R /Resources << /XObject << /Im%d %d 0 R >> >> /Contents %d 0 R >>",
			i+1, imageNum, pageObjNum+2,
		))
	}
	_ = imageObject
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), n),
	}
	for i := 0; i < n; i++ {
		content := fmt.Sprintf("q 20 0 0 20 10 10 cm /Im%d Do Q", i+1)
		imageObjNum := 5 + 2*i
		// object numbering: content=4? Simpler fixed layout below.
		_ = content
		_ = imageObjNum
	}
	// Fixed layout: objects 3..: for each page: content stream, image, page.
	objectNum := 3
	kids = nil
	for i := 0; i < n; i++ {
		content := fmt.Sprintf("q 20 0 0 20 10 10 cm /Im%d Do Q", i+1)
		objects = append(objects,
			fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content), // content
			imageObject, // image (shared)
		)
		pageObj := objectNum + 2
		objects = append(objects, fmt.Sprintf(
			"<< /Type /Page /Parent 2 0 R /Resources << /XObject << /Im%d %d 0 R >> >> /Contents %d 0 R >>",
			i+1, objectNum+1, objectNum,
		))
		kids = append(kids, fmt.Sprintf("%d 0 R", pageObj))
		objectNum += 3
	}
	objects[1] = fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), n)
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

// garbageOCRHelper answers PDF-envelope OCR with low-quality text so tests
// can prove that worse OCR no longer replaces better native evidence.
type garbageOCRHelper struct{}

func (h *garbageOCRHelper) Available() bool { return true }

func (h *garbageOCRHelper) Run(_ context.Context, format string, _ []byte) (string, error) {
	if format != "png" {
		return "OCR", nil
	}
	return "OCR", nil
}

// perGlyphTextPDF builds a multi-glyph PDF whose plain extraction fragments
// into one-glyph lines (the SmartCare failure shape).
func perGlyphTextPDF(t *testing.T, runes string) []byte {
	t.Helper()
	var content strings.Builder
	content.WriteString("BT /F1 12 Tf ET\n")
	x := 72.0
	for _, r := range runes {
		fmt.Fprintf(&content, "BT /F1 12 Tf %.0f 720 Td (%c) Tj ET\n", x, r)
		x += 10
	}
	return pdfFixture(t, content.String())
}

// Fixture D: OCR returns something non-empty but much worse than the
// fragmented-but-substantial native layer. The native text must survive.
func TestWorseOCRDoesNotReplaceFragmentedNative(t *testing.T) {
	f := newFixture(t)
	_ = f.createBase(t)
	service := f.service
	service.global.OCR.Mode = "auto"
	service.ocr = &garbageOCRHelper{}

	data := perGlyphTextPDF(t, "Guard native evidence with care even when fragmented")
	doc := &Document{FileName: "native.pdf"}
	text, _, err := service.parseFileContent(context.Background(), doc, BaseConfig{}, data)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "Guardnative") && !strings.Contains(strings.Join(strings.Fields(text), ""), "Guardnative") {
		t.Fatalf("native text replaced by OCR: %q", text)
	}
	if doc.ExtractionMethod == "ocr" {
		t.Fatalf("worse OCR must not win selection: %+v", doc)
	}
}

// Fixture E: a three-page scanned document where the renderer only covers
// two pages must surface partial OCR instead of a silent GOOD.
func TestPartialOCRSurfacesWarningNotGood(t *testing.T) {
	f := newFixture(t)
	_ = f.createBase(t)
	service := f.service
	configureOCRRenderer(t, service, "png")
	service.ocr = &recordingOCRHelper{}

	data := scannedImagePDFPages(t, 3)
	doc := &Document{FileName: "partial.pdf"}
	if _, _, err := service.parseFileContent(context.Background(), doc, BaseConfig{}, data); err != nil {
		t.Fatal(err)
	}
	if doc.PagesTotal != 3 {
		t.Fatalf("pages total: %d", doc.PagesTotal)
	}
	if doc.PagesOCR != 2 {
		t.Fatalf("pages ocr: %d", doc.PagesOCR)
	}
	if !doc.QualityPartial {
		t.Fatalf("partial flag missing: %+v", doc)
	}
	if doc.QualityStatus != QualityWarning {
		t.Fatalf("quality status: %q", doc.QualityStatus)
	}
	if !containsWarning(doc.QualityWarnings, "OCR_PAGE_LIMIT_REACHED") {
		t.Fatalf("warnings: %v", doc.QualityWarnings)
	}
}

// Fixture F: a true scanned PDF goes to OCR and reports the OCR provenance.
func TestTrueScannedPDFUsesOCRWithQuality(t *testing.T) {
	f := newFixture(t)
	_ = f.createBase(t)
	service := f.service
	configureOCRRenderer(t, service, "png")
	service.ocr = &recordingOCRHelper{}

	doc := &Document{FileName: "scanned.pdf"}
	text, _, err := service.parseFileContent(context.Background(), doc, BaseConfig{}, scannedImagePDF(t))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(text) == "" {
		t.Fatal("OCR produced no text")
	}
	if doc.ExtractionMethod != "ocr" {
		t.Fatalf("extraction method: %q", doc.ExtractionMethod)
	}
	if doc.QualityStatus != QualityGood {
		t.Fatalf("quality status: %q (%v)", doc.QualityStatus, doc.QualityWarnings)
	}
}

func TestNonPDFNativeExtractionIsHealthy(t *testing.T) {
	f := newFixture(t)
	_ = f.createBase(t)
	doc := &Document{FileName: "financial.xlsx"}
	text, _, err := f.service.parseFileContent(context.Background(), doc, BaseConfig{}, goldenXLSX(t))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(text) == "" {
		t.Fatal("empty native text")
	}
	if doc.QualityStatus != QualityGood {
		t.Fatalf("quality status = %q, warnings=%v, want %s", doc.QualityStatus, doc.QualityWarnings, QualityGood)
	}
}

// Section 25: the silent catastrophic loss detector. 3,285 pages with 2,740
// characters must never classify as GOOD.
func TestCatastrophicLossDetector(t *testing.T) {
	status, _, warnings := classifyExtractionQuality(3285, "ocr", 2740, true, false, nil)
	if status != QualityIncomplete {
		t.Fatalf("status: %q", status)
	}
	if !containsWarning(warnings, "CATASTROPHIC_TEXT_LOSS") {
		t.Fatalf("warnings: %v", warnings)
	}

	// A healthy image-heavy but legitimately extracted document stays GOOD.
	status, _, _ = classifyExtractionQuality(10, "native", 8000, true, false, nil)
	if status != QualityGood {
		t.Fatalf("healthy document status: %q", status)
	}
}

func TestOCRReplacementSafetyRule(t *testing.T) {
	nativeSubstantial := strings.Repeat("原生证据文本足够长。", 50) // fragmented shape, large volume
	if !ocrCandidateWins("", false, "可读OCR文本内容足够丰富，覆盖完整。", 5) {
		t.Fatal("OCR over empty native must win")
	}
	if ocrCandidateWins(nativeSubstantial, false, "OCR", 5) {
		t.Fatal("tiny OCR must not replace a substantially larger native layer")
	}
	if !ocrCandidateWins("", false, strings.Repeat("页面渲染识别文本。", 100), 5) {
		t.Fatal("full OCR over empty native must win")
	}
	if ocrCandidateWins("healthy native", true, strings.Repeat("x", 10), 1) {
		t.Fatal("unhealthy OCR must never win")
	}
}

// Section 26: chunk hygiene merges fragments instead of indexing them alone,
// while short-but-meaningful chunks (codes, parameters) survive.
func TestChunkHygieneMergesFragments(t *testing.T) {
	pieces := []chunk.Piece{
		{Text: "A 注意"},
		{Text: "完整段落包含足够的正文内容，不会被合并丢弃。"},
		{Text: "kr 3 3 / S :"},
		{Text: "ACCESS_TYPE_ID='1'"},
	}
	got := hygienizeChunks(pieces)
	for _, piece := range got {
		if pieceMeaningfulRunes(piece.Text) < minimumMeaningfulRunesPerChunk {
			t.Fatalf("fragment survived alone: %q", piece.Text)
		}
	}
	joined := ""
	for _, piece := range got {
		joined += piece.Text
	}
	for _, want := range []string{"A 注意", "kr 3 3 / S :", "ACCESS_TYPE_ID='1'"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("content lost: %q missing from %q", want, joined)
		}
	}
}

func containsWarning(warnings []string, want string) bool {
	for _, w := range warnings {
		if w == want {
			return true
		}
	}
	return false
}
