package knowledge

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/shutu-ai/shutu-knowledge/internal/chunk"

	"github.com/shutu-ai/shutu-knowledge/internal/parser"
)

// Document extraction quality statuses. Operation completion (status=ready)
// and extraction quality are deliberately independent: a completed import of
// a catastrophically under-extracted document must stay observable.
const (
	QualityUnknown    = "UNKNOWN"
	QualityGood       = "GOOD"
	QualityWarning    = "WARNING"
	QualityLow        = "LOW_QUALITY"
	QualityIncomplete = "INCOMPLETE"
)

// qualitySoftDensityPerPage is the soft chars-per-page floor for text PDFs.
// Image-heavy but legitimate documents can fall below it without being
// broken; the catastrophic detector below handles the true loss zone.
const qualitySoftDensityPerPage = 100.0

// classifyExtractionQuality maps extraction diagnostics to a persisted
// quality status. The reference corpus failure this guards against: a 3,285
// page PDF reported ready with 2,740 extracted characters.
func classifyExtractionQuality(
	pagesTotal int, method string, chars int, healthy bool, partialOCR bool, warnings []string,
) (string, float64, []string) {
	status := QualityGood
	allWarnings := append([]string(nil), warnings...)

	if pagesTotal <= 0 {
		// Non-PDF or unknown page counts: text presence decides.
		switch {
		case chars <= 0:
			status = QualityIncomplete
			allWarnings = append(allWarnings, "NO_TEXT_EXTRACTED")
		case !healthy:
			status = QualityLow
		}
		return status, qualityScore(chars, status), allWarnings
	}

	charsPerPage := float64(chars) / float64(pagesTotal)

	// Silent catastrophic loss detector: many pages, almost no text, and
	// incomplete coverage may never report GOOD.
	if pagesTotal >= 100 && charsPerPage < 40 {
		status = QualityIncomplete
		allWarnings = append(allWarnings, "CATASTROPHIC_TEXT_LOSS")
	} else {
		switch {
		case chars <= 0:
			status = QualityIncomplete
			allWarnings = append(allWarnings, "NO_TEXT_EXTRACTED")
		case !healthy:
			status = QualityLow
			allWarnings = append(allWarnings, "FRAGMENTED_EXTRACTION")
		case partialOCR:
			status = QualityWarning
			allWarnings = append(allWarnings, "OCR_PAGE_LIMIT_REACHED")
		case charsPerPage < qualitySoftDensityPerPage && method != "ocr":
			// Legitimate image-heavy PDFs exist; keep them visible but flagged.
			status = QualityWarning
			allWarnings = append(allWarnings, "LOW_TEXT_DENSITY")
		}
	}

	return status, qualityScore(chars, status), allWarnings
}

// qualityScore is a coarse 0..1 confidence value for ranking and display.
func qualityScore(chars int, status string) float64 {
	switch status {
	case QualityGood:
		return 1
	case QualityWarning:
		return 0.7
	case QualityLow:
		return 0.3
	case QualityIncomplete:
		return 0
	}
	return 0
}

// ocrCandidateWins applies the OCR replacement safety rule: OCR may only
// replace native text when it is itself readable AND measurably improves on
// the native extraction. Worse or shorter OCR must never overwrite better
// native evidence.
func ocrCandidateWins(nativeText string, nativeHealthy bool, ocrText string, pagesTotal int) bool {
	if strings.TrimSpace(ocrText) == "" {
		return false
	}
	nativeChars := len([]rune(nativeText))
	ocrQuality := parser.EvaluatePDFTextQuality(ocrText, pagesTotal)
	if !ocrQuality.Healthy() && !ocrQuality.DenseEnoughForOCR() {
		// OCR output that is itself fragmented cannot justify replacement.
		return false
	}
	if nativeChars == 0 {
		return true
	}
	if !nativeHealthy {
		// Native is unusable; readable OCR with comparable coverage wins.
		return ocrQuality.Chars >= nativeChars/4
	}
	// Native healthy: only a substantially fuller OCR result may win, which
	// in practice never happens for text-layer PDFs.
	return ocrQuality.Chars > nativeChars*2
}

// qualityPartialInt encodes the partial flag for storage.
func qualityPartialInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

// extractionMethodOf names the winning extraction candidate for the quality
// model: native, reassembled, fallback, ocr, or image.
func extractionMethodOf(parsed parser.Result) string {
	if parsed.PDFQuality == nil {
		if parsed.Parser == "image" {
			return "image"
		}
		return "native"
	}
	if strings.Contains(parsed.ParserVersion, "reassemble") {
		return "reassembled"
	}
	return "native"
}

// recordExtractionQuality classifies one finished extraction and persists the
// result on the document. It is the single write path for quality metadata so
// status, warnings, partial flags, and Incomplete always agree.
func (s *Service) recordExtractionQuality(doc *Document, text, method string, nativeHealthy bool, ocrPages int, ocrPartial bool) {
	chars := len([]rune(text))
	healthy := nativeHealthy
	if method == "ocr" || method == "fallback" {
		q := parser.EvaluatePDFTextQuality(text, doc.PagesTotal)
		// OCR of sparse or diagram-heavy pages yields short text that the
		// prose line gates misjudge; density is the fair lens for OCR output.
		healthy = q.Healthy() || q.DenseEnoughForOCR()
	}
	status, score, warnings := classifyExtractionQuality(doc.PagesTotal, method, chars, healthy, ocrPartial, doc.QualityWarnings)
	doc.ExtractionMethod = method
	doc.QualityStatus = status
	doc.QualityScore = score
	doc.QualityWarnings = warnings
	doc.QualityPartial = ocrPartial
	doc.PagesOCR = ocrPages
	doc.Incomplete = status == QualityIncomplete
}

// pieceMeaningfulRunes counts letters, digits, and CJK characters: the
// content that survives fragment filtering. Short chunks made of punctuation
// and layout debris (e.g. "ESN F5: eRe", "kr 3 3 / S :") carry almost none.
func pieceMeaningfulRunes(text string) int {
	count := 0
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			count++
		}
	}
	return count
}

// minimumMeaningfulRunesPerChunk is the fragment threshold: chunks with fewer
// meaningful letters/digits are merged into a neighbor instead of standing
// alone in the index. Chunks like CLI commands, error codes, or parameter
// values keep well above this threshold, so they are never discarded.
const minimumMeaningfulRunesPerChunk = 10

// hygienizeChunks merges fragment chunks into their neighbors. Merging — not
// deletion — is the default so no potentially meaningful short evidence is
// lost; deletion only happens for a fragment that cannot anchor anywhere.
func hygienizeChunks(pieces []chunk.Piece) []chunk.Piece {
	if len(pieces) <= 1 {
		return pieces
	}
	out := make([]chunk.Piece, 0, len(pieces))
	held := ""
	for _, piece := range pieces {
		text := strings.TrimSpace(piece.Text)
		if text == "" {
			continue
		}
		if pieceMeaningfulRunes(text) >= minimumMeaningfulRunesPerChunk {
			if held != "" {
				piece.Text = held + "\n" + text
				held = ""
			}
			out = append(out, piece)
			continue
		}
		// Fragment: merge backward into a healthy neighbor when possible,
		// otherwise hold it for the next healthy chunk.
		if len(out) > 0 && pieceMeaningfulRunes(out[len(out)-1].Text) >= minimumMeaningfulRunesPerChunk {
			prev := out[len(out)-1]
			prev.Text = strings.TrimSpace(prev.Text) + "\n" + text
			out[len(out)-1] = prev
			continue
		}
		if held != "" {
			held += "\n" + text
		} else {
			held = text
		}
	}
	if held != "" && len(out) > 0 {
		prev := out[len(out)-1]
		prev.Text = strings.TrimSpace(prev.Text) + "\n" + held
		out[len(out)-1] = prev
	}
	if len(out) == 0 {
		// Degenerate corpus: keep the original pieces rather than dropping
		// everything.
		return pieces
	}
	return out
}

// parseQualityWarnings decodes the persisted warning list.
func parseQualityWarnings(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func formatQualityWarnings(warnings []string) string {
	return strings.Join(warnings, ",")
}

// QualitySummary aggregates per-document quality statuses for an import
// operation result.
type QualitySummary struct {
	Total      int `json:"total"`
	Good       int `json:"good"`
	Warning    int `json:"warning"`
	LowQuality int `json:"lowQuality"`
	Incomplete int `json:"incomplete"`
}

func (s QualitySummary) String() string {
	return fmt.Sprintf("%d imported: %d good, %d warning, %d low_quality, %d incomplete",
		s.Total, s.Good, s.Warning, s.LowQuality, s.Incomplete)
}

// Observe folds one status into the summary.
func (s *QualitySummary) Observe(status string) {
	s.Total++
	switch status {
	case QualityGood:
		s.Good++
	case QualityWarning:
		s.Warning++
	case QualityLow:
		s.LowQuality++
	case QualityIncomplete:
		s.Incomplete++
	}
}
