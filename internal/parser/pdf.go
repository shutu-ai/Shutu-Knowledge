package parser

import (
	"bytes"
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode/utf8"

	pdftext "github.com/ledongthuc/pdf"
)

// pdfParser extracts a PDF text layer with a pure-Go reader. Poorly shaped
// glyph streams are first rebuilt from coordinates; if OCR remains advisable
// the result is returned with NeedsOCR so the caller can try its configured
// OCR runtime without discarding the native text.
type pdfParser struct{}

func (pdfParser) Extensions() []string { return []string{"pdf"} }

func (pdfParser) Parse(_ string, data []byte) (Result, error) {
	plain, plainErr := extractPDFPlainText(data)
	if plainErr == nil && strings.TrimSpace(plain) == "" {
		plainErr = fmt.Errorf("PDF contains no extractable text (it may be scanned)")
	}

	// A malformed/fragmented layer can still be coordinate-reassembled even
	// when the simple plain-text walk returned no usable content.
	if plainErr != nil {
		reassembled, layoutErr := reassemblePDFLayout(data)
		if layoutErr == nil && AverageLineLength(reassembled) >= 12 {
			return Result{Text: reassembled}, nil
		}
		return Result{}, fmt.Errorf("PDF parsing failed: %w", plainErr)
	}

	if AverageLineLength(plain) >= 5 {
		return Result{Text: plain}, nil
	}
	reassembled, layoutErr := reassemblePDFLayout(data)
	if layoutErr == nil && strings.TrimSpace(reassembled) != "" && AverageLineLength(reassembled) >= 12 {
		return Result{Text: reassembled}, nil
	}
	// Preserve the native text for the caller's OCR/fallback chain.
	return Result{Text: plain, NeedsOCR: true}, nil
}

func extractPDFPlainText(data []byte) (string, error) {
	reader, err := pdftext.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("open PDF: %w", err)
	}
	content, err := reader.GetPlainText()
	if err != nil {
		return "", fmt.Errorf("PDF text extraction failed: %w", err)
	}
	var out bytes.Buffer
	if _, err := out.ReadFrom(content); err != nil {
		return "", fmt.Errorf("PDF text extraction failed: %w", err)
	}
	return out.String(), nil
}

// AverageLineLength mirrors the upstream text-layer health heuristic: one
// glyph per source line is common in coordinate-laid-out math PDFs.
func AverageLineLength(text string) float64 {
	var total, count int
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		total += utf8.RuneCountInString(line)
		count++
	}
	if count == 0 {
		return 0
	}
	return float64(total) / float64(count)
}

// reassemblePDFLayout rebuilds visual lines from text-item coordinates.
// The upstream algorithm clusters by y bands derived from the median glyph
// height, then sorts items in each band by x and joins pages with a blank
// line. Keeping the same behavior makes the output deterministic across
// different local OCR/runtime availability.
func reassemblePDFLayout(data []byte) (string, error) {
	reader, err := pdftext.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("open PDF: %w", err)
	}
	pageTexts := make([]string, 0, reader.NumPage())
	for pageNumber := 1; pageNumber <= reader.NumPage(); pageNumber++ {
		items := reader.Page(pageNumber).Content().Text
		usable := make([]pdftext.Text, 0, len(items))
		heights := make([]float64, 0, len(items))
		for _, item := range items {
			if item.S == "" {
				continue
			}
			height := math.Abs(item.FontSize)
			if height == 0 {
				height = 10
			}
			usable = append(usable, item)
			heights = append(heights, height)
		}
		if len(usable) == 0 {
			continue
		}
		sort.Float64s(heights)
		tolerance := heights[len(heights)/2] * 0.6
		if tolerance <= 0 {
			tolerance = 6
		}

		bands := map[int][]pdftext.Text{}
		bandNumbers := make([]int, 0)
		for _, item := range usable {
			band := int(math.Round(item.Y / tolerance))
			if _, ok := bands[band]; !ok {
				bandNumbers = append(bandNumbers, band)
			}
			bands[band] = append(bands[band], item)
		}
		sort.Ints(bandNumbers)

		lines := make([]string, 0, len(bandNumbers))
		for _, band := range bandNumbers {
			items := bands[band]
			sort.Slice(items, func(i, j int) bool { return items[i].X < items[j].X })
			line := make([]string, 0, len(items))
			for _, item := range items {
				line = append(line, item.S)
			}
			if strings.TrimSpace(strings.Join(line, "")) != "" {
				lines = append(lines, strings.Join(line, ""))
			}
		}
		if len(lines) > 0 {
			pageTexts = append(pageTexts, strings.Join(lines, "\n"))
		}
	}
	return strings.Join(pageTexts, "\n\n"), nil
}
