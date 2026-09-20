package parser

import (
	"strings"
	"testing"
)

// Fixture B semantics: a small damaged-glyph share must not reject an
// otherwise healthy extraction. The v0.6.1 binary veto discarded ~99% usable
// text because ~1% of glyphs had no ToUnicode mapping.
func TestEvaluatePDFTextQualityToleratesSmallReplacementRatio(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 99; i++ {
		b.WriteString("正常文本内容第九十九行内容\n")
	}
	b.WriteString(strings.Repeat("\uFFFD", 1) + "\n")
	healthy := EvaluatePDFTextQuality(b.String(), 100)
	if !healthy.Healthy() {
		t.Fatalf("1%% replacement ratio must stay healthy: %+v", healthy)
	}
	if healthy.ReplacementRatio <= 0 || healthy.ReplacementRatio >= 0.05 {
		t.Fatalf("replacement ratio: %v", healthy.ReplacementRatio)
	}

	var damaged strings.Builder
	for i := 0; i < 50; i++ {
		damaged.WriteString(strings.Repeat("\uFFFD", 30) + "\n")
	}
	broken := EvaluatePDFTextQuality(damaged.String(), 50)
	if broken.Healthy() {
		t.Fatalf("majority replacement glyphs must be unhealthy: %+v", broken)
	}
}

// Fixture A semantics: one glyph per text item is fragmented regardless of
// its raw character volume.
func TestEvaluatePDFTextQualityDetectsPerGlyphFragmentation(t *testing.T) {
	text := strings.Repeat("字\n", 500)
	q := EvaluatePDFTextQuality(text, 10)
	if q.Healthy() {
		t.Fatalf("per-glyph text must be unhealthy: %+v", q)
	}
	if q.FragmentRatio < 0.99 {
		t.Fatalf("fragment ratio: %v", q.FragmentRatio)
	}
}

func TestStripReplacementRunes(t *testing.T) {
	got := stripReplacementRunes(" healthy\uFFFDtext\uFFFD ")
	if got != " healthytext " {
		t.Fatalf("strip: %q", got)
	}
	if !strings.ContainsRune("a\uFFFD", '\uFFFD') {
		t.Fatal("precondition")
	}
}

func TestPDFTextQualityCandidateComparison(t *testing.T) {
	full := EvaluatePDFTextQuality(strings.Repeat("完整的长句子内容足够长。\n", 100), 10)
	shorter := EvaluatePDFTextQuality(strings.Repeat("较短的文本。\n", 50), 10)
	if !full.BetterThan(shorter) {
		t.Fatal("fuller healthy candidate should win")
	}
	if shorter.BetterThan(full) {
		t.Fatal("shorter candidate should not beat the fuller one")
	}

	fragmented := EvaluatePDFTextQuality(strings.Repeat("字\n", 100), 10)
	reassembled := EvaluatePDFTextQuality(strings.Repeat("重组后的可读文本行，内容完整。\n", 20), 10)
	if !reassembled.BetterThan(fragmented) {
		t.Fatal("reassembled should beat fragmented when native is unhealthy")
	}
}

// Fixture C end-to-end: fragmented plain extraction loses to the coordinate
// reassembly and the winning candidate is labelled honestly.
func TestPDFReassembledCandidateSelectedAndLabelled(t *testing.T) {
	content := "BT /F1 12 Tf 72 720 Td (R) Tj ET\n" +
		"BT /F1 12 Tf 80 720 Td (e) Tj ET\n" +
		"BT /F1 12 Tf 88 720 Td (built) Tj ET\n" +
		"BT /F1 12 Tf 110 720 Td ( layout) Tj ET\n"
	data := pdfFixture(t, content)
	result := parseOK(t, "layout.pdf", data)
	if result.NeedsOCR {
		t.Fatalf("reassembled result should not request OCR: %+v", result)
	}
	if strings.TrimSpace(result.Text) != "Rebuilt layout" {
		t.Fatalf("reassembled text: %q", result.Text)
	}
	if result.ParserVersion != "builtin-reassemble-v1" {
		t.Fatalf("parser version: %q", result.ParserVersion)
	}
	if result.PDFQuality == nil || result.PDFQuality.Chars == 0 {
		t.Fatalf("quality diagnostics missing: %+v", result.PDFQuality)
	}
}

func TestPDFQualityDiagnosticsAttached(t *testing.T) {
	content := "BT /F1 12 Tf 72 720 Td (Healthy native paragraph text for quality evaluation.) Tj ET"
	result := parseOK(t, "quality.pdf", pdfFixture(t, content))
	if result.PDFQuality == nil {
		t.Fatal("PDFQuality diagnostics missing")
	}
	if result.PDFQuality.Chars == 0 {
		t.Fatalf("quality chars: %+v", result.PDFQuality)
	}
}
