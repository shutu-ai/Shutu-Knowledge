package parser

import (
	"math"
	"sort"
	"strings"
	"unicode"
)

// PDFTextQuality is the single quality evaluation shared by every PDF text
// candidate: native plain extraction, layout reassembly, strong native
// fallback, and OCR output. The v0.6.1 ingestion path scattered ad-hoc
// thresholds (average line length, replacement-rune veto) across branches and
// rejected whole documents for a ~1% damaged glyph ratio. All candidates now
// pass through this evaluator so selection decisions compare like with like.
type PDFTextQuality struct {
	// Chars is the rune count of the candidate text after whitespace
	// normalization; it is the coverage proxy used for candidate ranking.
	Chars int
	// Pages is the reference page count supplied by the caller (0 when
	// unknown, which disables page-normalized metrics).
	Pages int
	// TextPages counts pages that contributed non-empty text (best effort:
	// whole-candidate evaluation uses Pages as the denominator when the
	// per-page split is unavailable).
	TextPages int
	// CharsPerPage is the coverage density proxy: Chars divided by reference
	// page count (0 when the page count is unknown).
	CharsPerPage float64

	ReplacementRuneCount int
	ReplacementRatio     float64
	PrintableRatio       float64
	CJKRatio             float64
	AlnumRatio           float64

	AverageLineLength float64
	MedianLineLength  float64
	// FragmentRatio is the share of non-empty lines shorter than 5 runes.
	// Per-glyph PDF text objects (one glyph per text item) produce ~1.0.
	FragmentRatio float64
	// EmptyPageRatio is the share of reference pages with no text. It stays
	// 0 when page-level data is unavailable.
	EmptyPageRatio float64

	// warnings records non-fatal quality findings retained on the Result.
	warnings []string
}

// pdfHealthyReplacementRatio bounds tolerable damaged glyphs. A one-glyph
// veto is gone: ~1% damaged ToUnicode entries must not destroy a document
// whose remaining 99% extracted cleanly.
const pdfHealthyReplacementRatio = 0.05

// pdfHealthyFragmentRatio bounds tolerated short-line share. Native per-glyph
// streams fragment at ~1.0; readable prose stays far below it.
const pdfHealthyFragmentRatio = 0.5

// pdfMinHealthyAverageLine is the minimum mean line length for readable text.
const pdfMinHealthyAverageLine = 5.0

// pdfMinHealthyPrintableRatio bounds control-character damage.
const pdfMinHealthyPrintableRatio = 0.8

// EvaluatePDFTextQuality scores one candidate text against a reference page
// count (0 when unknown). It is intentionally side-effect free so every
// extraction path can call it cheaply.
func EvaluatePDFTextQuality(text string, pages int) PDFTextQuality {
	q := PDFTextQuality{Pages: pages}
	if text == "" {
		q.ReplacementRatio = 1
		q.EmptyPageRatio = 1
		return q
	}

	var total, printable, cjk, alnum, replacement int
	var lineRunes []int
	var lineTotal int
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		runes := 0
		for _, r := range trimmed {
			total++
			switch {
			case r == unicode.ReplacementChar:
				replacement++
			case unicode.IsPrint(r):
				printable++
			}
			if unicode.Is(unicode.Han, r) {
				cjk++
			}
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				alnum++
			}
			runes++
		}
		lineRunes = append(lineRunes, runes)
		lineTotal += runes
	}

	if total == 0 {
		q.ReplacementRatio = 1
		q.EmptyPageRatio = 1
		return q
	}

	q.Chars = total
	q.ReplacementRuneCount = replacement
	q.ReplacementRatio = float64(replacement) / float64(total)
	q.PrintableRatio = float64(printable) / float64(total)
	q.CJKRatio = float64(cjk) / float64(total)
	q.AlnumRatio = float64(alnum) / float64(total)

	if len(lineRunes) > 0 {
		q.AverageLineLength = float64(lineTotal) / float64(len(lineRunes))
		sorted := append([]int(nil), lineRunes...)
		sort.Ints(sorted)
		q.MedianLineLength = float64(sorted[len(sorted)/2])
		fragments := 0
		for _, n := range lineRunes {
			if n < 5 {
				fragments++
			}
		}
		q.FragmentRatio = float64(fragments) / float64(len(lineRunes))
	}

	if pages > 0 {
		q.CharsPerPage = float64(total) / float64(pages)
	}

	return q
}

// EvaluatePDFTextQualityPerPage evaluates a candidate whose text is already
// split per page, refining text-page coverage metrics that whole-document
// evaluation cannot see.
func EvaluatePDFTextQualityPerPage(pageTexts []string) PDFTextQuality {
	joined := strings.Join(pageTexts, "\n\n")
	q := EvaluatePDFTextQuality(joined, len(pageTexts))
	empty := 0
	textPages := 0
	for _, page := range pageTexts {
		if strings.TrimSpace(page) == "" {
			empty++
			continue
		}
		textPages++
	}
	q.Pages = len(pageTexts)
	q.TextPages = textPages
	if len(pageTexts) > 0 {
		q.EmptyPageRatio = float64(empty) / float64(len(pageTexts))
	}
	return q
}

// addWarning appends a deduplicated quality warning.
func (q *PDFTextQuality) addWarning(warning string) {
	for _, existing := range q.warnings {
		if existing == warning {
			return
		}
	}
	q.warnings = append(q.warnings, warning)
}

// Warnings returns the quality warnings collected during evaluation.
func (q PDFTextQuality) Warnings() []string {
	return q.warnings
}

// Healthy reports whether the candidate is trustworthy primary evidence:
// enough text, low glyph damage, and line structure compatible with prose
// (or at least not with one-glyph-per-item fragmentation).
func (q PDFTextQuality) Healthy() bool {
	if q.Chars == 0 {
		return false
	}
	if q.ReplacementRatio > pdfHealthyReplacementRatio {
		return false
	}
	if q.PrintableRatio < pdfMinHealthyPrintableRatio {
		return false
	}
	// Fragmented per-glyph streams fail the line-structure gates; prose and
	// reassembled layouts pass both. The average gate alone is not enough:
	// "R/e/built/ layout" style fragments average 3-6 runes per line.
	if q.AverageLineLength < pdfMinHealthyAverageLine {
		return false
	}
	if q.FragmentRatio > pdfHealthyFragmentRatio {
		return false
	}
	return true
}

// BetterThan ranks two candidates for selection. Healthy beats unhealthy;
// among healthy candidates coverage (chars) dominates, then glyph damage,
// then line structure. Among unhealthy candidates the less fragmented one
// still wins so layout reassembly is preferred over raw per-glyph text.
func (q PDFTextQuality) BetterThan(other PDFTextQuality) bool {
	qHealthy, otherHealthy := q.Healthy(), other.Healthy()
	if qHealthy != otherHealthy {
		return qHealthy
	}
	if qHealthy {
		if q.Chars != other.Chars {
			// Prefer the fuller extraction, tolerating 2% noise either way
			// so line-structure quality can decide near-ties.
			if q.Chars > other.Chars && q.Chars >= other.Chars*98/100 {
				return true
			}
			if other.Chars > q.Chars {
				return false
			}
		}
		if math.Abs(q.ReplacementRatio-other.ReplacementRatio) > 0.005 {
			return q.ReplacementRatio < other.ReplacementRatio
		}
		return q.MedianLineLength >= other.MedianLineLength
	}
	// Neither healthy: prefer fewer replacement runes and longer lines.
	if q.ReplacementRatio != other.ReplacementRatio {
		return q.ReplacementRatio < other.ReplacementRatio
	}
	return q.MedianLineLength >= other.MedianLineLength
}

// DenseEnoughForOCR reports whether short-but-content-rich OCR output is
// acceptable evidence. OCR of diagram-heavy or sparse pages legitimately
// yields few characters per page, and the prose line-structure gates misjudge
// such output: two dense CJK characters on an otherwise empty page are real
// content, not fragmentation.
func (q PDFTextQuality) DenseEnoughForOCR() bool {
	if q.Chars == 0 {
		return false
	}
	if q.CJKRatio+q.AlnumRatio < 0.5 {
		return false
	}
	if q.Pages > 0 && q.CharsPerPage < 1 {
		return false
	}
	return true
}

// stripReplacementRunes removes isolated U+FFFD characters so a small damaged
// glyph share does not leak noise into chunks and the FTS index.
func stripReplacementRunes(text string) string {
	if !strings.ContainsRune(text, unicode.ReplacementChar) {
		return text
	}
	var b strings.Builder
	b.Grow(len(text))
	for _, r := range text {
		if r == unicode.ReplacementChar {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
