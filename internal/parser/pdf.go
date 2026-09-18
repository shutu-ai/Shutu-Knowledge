package parser

import (
	"bytes"
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode/utf8"

	pdftext "github.com/ledongthuc/pdf"
	"github.com/shutu-ai/shutu-knowledge/internal/documentir"
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
			return pdfResult(reassembled, data), nil
		}
		return Result{}, fmt.Errorf("PDF parsing failed: %w", plainErr)
	}

	// U+FFFD is produced by the PDF library when a font's ToUnicode map is
	// missing or broken. It is valid UTF-8, so a byte-validity check alone
	// cannot detect it. Keep the native text as a fallback, but mark it
	// unhealthy so the ingestion chain can try OCR/reconstruction.
	if AverageLineLength(plain) >= 5 && !hasReplacementRune(plain) {
		return pdfResult(plain, data), nil
	}
	reassembled, layoutErr := reassemblePDFLayout(data)
	if layoutErr == nil && strings.TrimSpace(reassembled) != "" &&
		AverageLineLength(reassembled) >= 12 && !hasReplacementRune(reassembled) {
		return pdfResult(reassembled, data), nil
	}
	// Preserve the native text for the caller's OCR/fallback chain.
	if strings.TrimSpace(plain) != "" {
		result := pdfResult(plain, data)
		result.NeedsOCR = true
		return result, nil
	}
	if plainErr != nil {
		return Result{}, fmt.Errorf("PDF parsing failed: %w", plainErr)
	}
	return Result{}, fmt.Errorf("PDF contains no healthy extractable text")
}

func pdfResult(text string, data []byte) Result {
	return Result{Text: text, IR: pdfIR(text, data), Parser: "pdf", ParserVersion: "builtin-v1"}
}

// pdfIR keeps page identity even though the legacy text projection remains a
// single string. Bounding boxes are intentionally omitted until the parser
// can prove coordinates for the selected text layer.
func pdfIR(text string, data []byte) *documentir.Document {
	d := &documentir.Document{IRVersion: documentir.Version, Parser: "pdf", ParserVersion: "builtin-v1"}
	d.Nodes = append(d.Nodes, documentir.Node{ID: "tmp:document", Type: documentir.TypeDocument, Order: 0, Text: strings.TrimSpace(text), SourceAnchor: documentir.SourceAnchor{Kind: "pdf", LogicalPath: "document"}, Parser: "pdf", ParserVersion: "builtin-v1"})
	reader, err := pdftext.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return d
	}
	for pageNumber := 1; pageNumber <= reader.NumPage(); pageNumber++ {
		blocks := pdfPageBlocks(reader.Page(pageNumber).Content().Text)
		lines := make([]string, 0, len(blocks))
		for _, block := range blocks {
			lines = append(lines, block.text)
		}
		pageText := strings.TrimSpace(strings.Join(lines, "\n"))
		if pageText == "" {
			continue
		}
		pageBBox := unionPDFBBoxes(blocks)
		anchor := documentir.SourceAnchor{Kind: "pdf", Page: pageNumber, LogicalPath: fmt.Sprintf("page/%d", pageNumber), BBox: pageBBox}
		pageID := fmt.Sprintf("tmp:page/%d", pageNumber)
		d.Nodes = append(d.Nodes, documentir.Node{ID: pageID, Type: documentir.TypePage, ParentID: "tmp:document", Order: pageNumber, Text: pageText, PageNumber: pageNumber, BBox: pageBBox, SourceAnchor: anchor, Parser: "pdf", ParserVersion: "builtin-v1"})
		for blockIndex, block := range blocks {
			logical := fmt.Sprintf("page/%d/block/%d", pageNumber, blockIndex+1)
			blockAnchor := documentir.SourceAnchor{Kind: "pdf", Page: pageNumber, Block: blockIndex + 1, LogicalPath: logical, BBox: block.bbox}
			if isPDFFigureCaption(block.text) {
				figureID := "tmp:" + logical + "/figure"
				d.Nodes = append(d.Nodes, documentir.Node{ID: figureID, Type: documentir.TypeFigure, ParentID: pageID, Order: blockIndex + 1, Text: block.text, PageNumber: pageNumber, BBox: block.bbox, Metadata: map[string]string{"figure_id": logical}, SourceAnchor: blockAnchor, Parser: "pdf", ParserVersion: "builtin-v1", Confidence: block.confidence})
				d.Nodes = append(d.Nodes, documentir.Node{ID: figureID + "/caption", Type: documentir.TypeCaption, ParentID: figureID, Order: 1, Text: block.text, PageNumber: pageNumber, BBox: block.bbox, SourceAnchor: documentir.SourceAnchor{Kind: "pdf", Page: pageNumber, Block: blockIndex + 1, LogicalPath: logical + "/caption", BBox: block.bbox}, Parser: "pdf", ParserVersion: "builtin-v1", Confidence: block.confidence})
				continue
			}
			if cells := splitPDFTableRow(block.text); len(cells) >= 2 {
				tableID := "tmp:" + logical + "/table"
				d.Nodes = append(d.Nodes, documentir.Node{ID: tableID, Type: documentir.TypeTable, ParentID: pageID, Order: blockIndex + 1, Text: block.text, PageNumber: pageNumber, BBox: block.bbox, Metadata: map[string]string{"detected": "heuristic"}, SourceAnchor: blockAnchor, Parser: "pdf", ParserVersion: "builtin-v1", Confidence: block.confidence})
				rowID := tableID + "/row/1"
				d.Nodes = append(d.Nodes, documentir.Node{ID: rowID, Type: documentir.TypeTableRow, ParentID: tableID, Order: 1, Text: block.text, PageNumber: pageNumber, BBox: block.bbox, SourceAnchor: documentir.SourceAnchor{Kind: "pdf", Page: pageNumber, Block: blockIndex + 1, LogicalPath: logical + "/table/row/1", BBox: block.bbox}, Parser: "pdf", ParserVersion: "builtin-v1", Confidence: block.confidence})
				for cellIndex, cell := range cells {
					cellLogical := fmt.Sprintf("%s/table/row/1/cell/%d", logical, cellIndex+1)
					d.Nodes = append(d.Nodes, documentir.Node{ID: "tmp:" + cellLogical, Type: documentir.TypeTableCell, ParentID: rowID, Order: cellIndex + 1, Text: cell, PageNumber: pageNumber, BBox: block.bbox, Metadata: map[string]string{"position": fmt.Sprintf("%d", cellIndex+1), "row_span": "1", "col_span": "1"}, SourceAnchor: documentir.SourceAnchor{Kind: "pdf", Page: pageNumber, Block: blockIndex + 1, LogicalPath: cellLogical, BBox: block.bbox}, Parser: "pdf", ParserVersion: "builtin-v1", Confidence: block.confidence})
				}
				continue
			}
			typ := documentir.TypeParagraph
			if block.heading {
				typ = documentir.TypeHeading
			}
			d.Nodes = append(d.Nodes, documentir.Node{ID: "tmp:" + logical, Type: typ, ParentID: pageID, Order: blockIndex + 1, Text: block.text, PageNumber: pageNumber, BBox: block.bbox, SourceAnchor: blockAnchor, Parser: "pdf", ParserVersion: "builtin-v1", Confidence: block.confidence})
		}
	}
	if len(d.Nodes) == 1 && strings.TrimSpace(text) != "" {
		fallback := documentir.FromText("", text, "pdf", "builtin-v1")
		return fallback
	}
	return d
}

func unionPDFBBoxes(blocks []pdfIRBlock) *documentir.BBox {
	var out *documentir.BBox
	for _, block := range blocks {
		if block.bbox == nil {
			continue
		}
		if out == nil {
			copy := *block.bbox
			out = &copy
			continue
		}
		if block.bbox.X1 < out.X1 {
			out.X1 = block.bbox.X1
		}
		if block.bbox.Y1 < out.Y1 {
			out.Y1 = block.bbox.Y1
		}
		if block.bbox.X2 > out.X2 {
			out.X2 = block.bbox.X2
		}
		if block.bbox.Y2 > out.Y2 {
			out.Y2 = block.bbox.Y2
		}
	}
	return out
}

type pdfIRBlock struct {
	text       string
	bbox       *documentir.BBox
	heading    bool
	confidence float64
	y          float64
}

func pdfPageBlocks(items []pdftext.Text) []pdfIRBlock {
	usable := make([]pdftext.Text, 0, len(items))
	heights := make([]float64, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.S) == "" {
			continue
		}
		usable = append(usable, item)
		h := item.FontSize
		if h <= 0 {
			h = 10
		}
		heights = append(heights, h)
	}
	if len(usable) == 0 {
		return nil
	}
	sort.Float64s(heights)
	tolerance := heights[len(heights)/2] * 0.6
	if tolerance <= 0 {
		tolerance = 6
	}
	bands := map[int][]pdftext.Text{}
	order := []int{}
	for _, item := range usable {
		band := int(math.Round(item.Y / tolerance))
		if _, ok := bands[band]; !ok {
			order = append(order, band)
		}
		bands[band] = append(bands[band], item)
	}
	sort.Ints(order)
	median := heights[len(heights)/2]
	out := make([]pdfIRBlock, 0, len(order))
	for _, band := range order {
		group := bands[band]
		sort.SliceStable(group, func(i, j int) bool { return group[i].X < group[j].X })
		var text strings.Builder
		minX, minY, maxX, maxY := math.MaxFloat64, math.MaxFloat64, -math.MaxFloat64, -math.MaxFloat64
		large := false
		for _, item := range group {
			text.WriteString(item.S)
			h := item.FontSize
			if h <= 0 {
				h = 10
			}
			if h > median*1.25 {
				large = true
			}
			if item.X < minX {
				minX = item.X
			}
			if item.Y < minY {
				minY = item.Y
			}
			if item.X+item.W > maxX {
				maxX = item.X + item.W
			}
			if item.Y+h > maxY {
				maxY = item.Y + h
			}
		}
		value := strings.TrimSpace(text.String())
		if value == "" {
			continue
		}
		confidence := 0.85
		if len(group) == 1 {
			confidence = 0.70
		}
		out = append(out, pdfIRBlock{text: value, bbox: &documentir.BBox{X1: minX, Y1: minY, X2: maxX, Y2: maxY}, heading: large || numberedHeading(value), confidence: confidence, y: group[0].Y})
	}
	return out
}

func numberedHeading(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	for _, r := range value {
		if r >= '0' && r <= '9' {
			continue
		}
		return r == '#' || r == '.'
	}
	return false
}

func isPDFFigureCaption(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	return strings.HasPrefix(lower, "figure") || strings.HasPrefix(lower, "fig.") || strings.HasPrefix(lower, "fig ")
}

func splitPDFTableRow(value string) []string {
	value = strings.TrimSpace(value)
	if strings.Contains(value, "|") {
		parts := strings.Split(value, "|")
		out := make([]string, 0, len(parts))
		for _, part := range parts {
			if part = strings.TrimSpace(part); part != "" {
				out = append(out, part)
			}
		}
		return out
	}
	if strings.Count(value, "\t") > 0 {
		parts := strings.Split(value, "\t")
		out := make([]string, 0, len(parts))
		for _, part := range parts {
			if part = strings.TrimSpace(part); part != "" {
				out = append(out, part)
			}
		}
		return out
	}
	return nil
}

func hasReplacementRune(text string) bool {
	for _, r := range text {
		if r == '\uFFFD' {
			return true
		}
	}
	return false
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
