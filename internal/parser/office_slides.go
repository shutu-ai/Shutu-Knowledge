package parser

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"

	"github.com/shutu-ai/shutu-knowledge/internal/documentir"
)

var slideNamePattern = regexp.MustCompile(`ppt/slides/slide([0-9]+)\.xml$`)

type pptxShape struct {
	Text    string
	Picture bool
}

type pptxTableCell struct {
	Text string
	Row  int
	Col  int
}

func pptxIR(entries map[string][]byte, text string) *documentir.Document {
	d := &documentir.Document{IRVersion: documentir.Version, Parser: "pptx", ParserVersion: "builtin-v1"}
	d.Nodes = append(d.Nodes, documentir.Node{ID: "tmp:document", Type: documentir.TypeDocument, Order: 0, Text: strings.TrimSpace(text), SourceAnchor: documentir.SourceAnchor{Kind: "pptx", LogicalPath: "document"}, Parser: "pptx", ParserVersion: "builtin-v1"})
	var slides []struct {
		index int
		name  string
	}
	for name := range entries {
		if match := slideNamePattern.FindStringSubmatch(name); match != nil {
			index := parseIndex(match[1])
			slides = append(slides, struct {
				index int
				name  string
			}{index: index, name: name})
		}
	}
	sort.Slice(slides, func(i, j int) bool { return slides[i].index < slides[j].index })
	for _, slide := range slides {
		shapes, cells, fallback := parsePPTXStructure(entries[slide.name])
		if len(shapes) == 0 && len(cells) == 0 && strings.TrimSpace(fallback) != "" {
			for _, line := range strings.Split(fallback, "\n") {
				if line = strings.TrimSpace(line); line != "" {
					shapes = append(shapes, pptxShape{Text: line})
				}
			}
		}
		slideText := strings.TrimSpace(fallback)
		if len(shapes) > 0 {
			var parts []string
			for _, shape := range shapes {
				if strings.TrimSpace(shape.Text) != "" {
					parts = append(parts, strings.TrimSpace(shape.Text))
				}
			}
			slideText = strings.TrimSpace(strings.Join(parts, "\n"))
		}
		n := slide.index
		slideID := fmt.Sprintf("tmp:slide/%d", n)
		title := strings.TrimSpace(strings.SplitN(slideText, "\n", 2)[0])
		metadata := map[string]string{"title": title}
		d.Nodes = append(d.Nodes, documentir.Node{ID: slideID, Type: documentir.TypeSlide, ParentID: "tmp:document", Order: n, Text: slideText, HeadingPath: []string{title}, Metadata: metadata, SlideNumber: n, SourceAnchor: documentir.SourceAnchor{Kind: "pptx", Slide: n, LogicalPath: fmt.Sprintf("slide/%d", n)}, Parser: "pptx", ParserVersion: "builtin-v1"})
		order := 0
		for shapeIndex, shape := range shapes {
			order++
			logical := fmt.Sprintf("slide/%d/shape/%d", n, shapeIndex+1)
			typ := documentir.TypeBlock
			if shape.Picture {
				typ = documentir.TypeFigure
			}
			shapeMetadata := map[string]string{}
			if shape.Picture {
				shapeMetadata["figure_id"] = logical
				shapeMetadata["image_reference"] = logical
				if slideText != "" {
					shapeMetadata["nearby_text"] = slideText
				}
			}
			d.Nodes = append(d.Nodes, documentir.Node{ID: "tmp:" + logical, Type: typ, ParentID: slideID, Order: order, Text: strings.TrimSpace(shape.Text), SlideNumber: n, Metadata: shapeMetadata, SourceAnchor: documentir.SourceAnchor{Kind: "pptx", Slide: n, Shape: fmt.Sprintf("%d", shapeIndex+1), LogicalPath: logical}, Parser: "pptx", ParserVersion: "builtin-v1"})
			if shape.Picture {
				imageLogical := logical + "/image"
				d.Nodes = append(d.Nodes, documentir.Node{ID: "tmp:" + imageLogical, Type: documentir.TypeImage, ParentID: "tmp:" + logical, Order: 1, SlideNumber: n, Metadata: map[string]string{"figure_id": logical, "image_reference": logical, "nearby_text": slideText}, SourceAnchor: documentir.SourceAnchor{Kind: "pptx", Slide: n, Shape: fmt.Sprintf("%d", shapeIndex+1), LogicalPath: imageLogical}, Parser: "pptx", ParserVersion: "builtin-v1"})
			}
		}
		if len(cells) > 0 {
			order++
			tableID := fmt.Sprintf("tmp:slide/%d/table/1", n)
			tableText := make([]string, 0, len(cells))
			for _, cell := range cells {
				tableText = append(tableText, strings.TrimSpace(cell.Text))
			}
			d.Nodes = append(d.Nodes, documentir.Node{ID: tableID, Type: documentir.TypeTable, ParentID: slideID, Order: order, Text: strings.Join(tableText, "\t"), SlideNumber: n, SourceAnchor: documentir.SourceAnchor{Kind: "pptx", Slide: n, Shape: "table-1", LogicalPath: tableID[4:]}, Parser: "pptx", ParserVersion: "builtin-v1"})
			rowNumbers := map[int]bool{}
			for _, cell := range cells {
				rowNumbers[cell.Row] = true
			}
			rows := make([]int, 0, len(rowNumbers))
			for row := range rowNumbers {
				rows = append(rows, row)
			}
			sort.Ints(rows)
			for _, rowNumber := range rows {
				order++
				rowID := fmt.Sprintf("%s/row/%d", tableID, rowNumber)
				rowText := make([]string, 0)
				for _, cell := range cells {
					if cell.Row == rowNumber {
						rowText = append(rowText, strings.TrimSpace(cell.Text))
					}
				}
				d.Nodes = append(d.Nodes, documentir.Node{ID: rowID, Type: documentir.TypeTableRow, ParentID: tableID, Order: order, Text: strings.Join(rowText, "\t"), SlideNumber: n, SourceAnchor: documentir.SourceAnchor{Kind: "pptx", Slide: n, Shape: "table-1", LogicalPath: rowID[4:]}, Parser: "pptx", ParserVersion: "builtin-v1"})
				cellOrder := 0
				for _, cell := range cells {
					if cell.Row != rowNumber {
						continue
					}
					cellOrder++
					logical := fmt.Sprintf("slide/%d/table/1/row/%d/cell/%d", n, rowNumber, cellOrder)
					metadata := map[string]string{"row": fmt.Sprintf("%d", cell.Row), "column": fmt.Sprintf("%d", cell.Col), "position": fmt.Sprintf("%d,%d", cell.Row, cell.Col), "row_span": "1", "col_span": "1"}
					d.Nodes = append(d.Nodes, documentir.Node{ID: "tmp:" + logical, Type: documentir.TypeTableCell, ParentID: rowID, Order: cellOrder, Text: strings.TrimSpace(cell.Text), SlideNumber: n, Metadata: metadata, SourceAnchor: documentir.SourceAnchor{Kind: "pptx", Slide: n, Shape: "table-1", LogicalPath: logical}, Parser: "pptx", ParserVersion: "builtin-v1"})
				}
			}
		}
		notesName := fmt.Sprintf("ppt/notesSlides/notesSlide%d.xml", n)
		if notes := strings.TrimSpace(pptxText(entries[notesName])); notes != "" {
			order++
			logical := fmt.Sprintf("slide/%d/speaker-notes", n)
			d.Nodes = append(d.Nodes, documentir.Node{ID: "tmp:" + logical, Type: documentir.TypeFootnote, ParentID: slideID, Order: order, Text: notes, SlideNumber: n, Metadata: map[string]string{"role": "speaker_notes"}, SourceAnchor: documentir.SourceAnchor{Kind: "pptx", Slide: n, LogicalPath: logical}, Parser: "pptx", ParserVersion: "builtin-v1"})
		}
	}
	return d
}

func parsePPTXStructure(data []byte) ([]pptxShape, []pptxTableCell, string) {
	if len(data) == 0 {
		return nil, nil, ""
	}
	decoder := xml.NewDecoder(bytes.NewReader(data))
	var shapes []pptxShape
	var cells []pptxTableCell
	var allText, shapeText, cellText strings.Builder
	inText, inShape, inCell, inTable := false, false, false, false
	shapeDepth, row, col := 0, 0, 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}
		switch t := token.(type) {
		case xml.StartElement:
			switch localName(t.Name.Local) {
			case "sp":
				if !inShape {
					inShape = true
					shapeDepth = 0
					shapeText.Reset()
				}
				shapeDepth++
			case "pic":
				shapes = append(shapes, pptxShape{Picture: true})
			case "tbl":
				inTable = true
			case "tr":
				row++
				col = 0
			case "tc":
				if inTable {
					inCell = true
					col++
					cellText.Reset()
				}
			case "t":
				inText = true
			}
		case xml.EndElement:
			switch localName(t.Name.Local) {
			case "t":
				inText = false
			case "tc":
				if inCell {
					cells = append(cells, pptxTableCell{Text: strings.TrimSpace(cellText.String()), Row: row, Col: col})
					inCell = false
				}
			case "tbl":
				inTable = false
			case "sp":
				if inShape {
					shapeDepth--
					if shapeDepth == 0 {
						shapes = append(shapes, pptxShape{Text: strings.TrimSpace(shapeText.String())})
						inShape = false
					}
				}
			}
		case xml.CharData:
			if inText {
				value := string(t)
				allText.WriteString(value)
				if inShape {
					shapeText.WriteString(value)
				}
				if inCell {
					cellText.WriteString(value)
				}
			}
		}
	}
	return shapes, cells, allText.String()
}

func pptxText(data []byte) string {
	_, _, text := parsePPTXStructure(data)
	return text
}

// parsePptx extracts a:t text runs per slide, in slide order.
func parsePptx(entries map[string][]byte) (string, error) {
	type slide struct {
		index int
		name  string
	}
	var slides []slide
	for name := range entries {
		if match := slideNamePattern.FindStringSubmatch(name); match != nil {
			index := 0
			for _, r := range match[1] {
				index = index*10 + int(r-'0')
			}
			slides = append(slides, slide{index: index, name: name})
		}
	}
	if len(slides) == 0 {
		return "", fmt.Errorf("PPTX contains no extractable slides")
	}
	sort.Slice(slides, func(i, j int) bool { return slides[i].index < slides[j].index })
	var out strings.Builder
	for _, s := range slides {
		var runs strings.Builder
		inT := false
		decoder := xml.NewDecoder(bytes.NewReader(entries[s.name]))
		for {
			token, err := decoder.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				return "", fmt.Errorf("PPTX %s: %w", s.name, err)
			}
			switch t := token.(type) {
			case xml.StartElement:
				if localName(t.Name.Local) == "t" {
					inT = true
				}
			case xml.EndElement:
				if localName(t.Name.Local) == "t" {
					inT = false
				}
			case xml.CharData:
				if inT {
					runs.Write(t)
				}
			}
		}
		text := strings.TrimSpace(runs.String())
		if text != "" {
			out.WriteString(text)
			out.WriteString("\n\n")
		}
	}
	text := strings.TrimSpace(out.String())
	if text == "" {
		return "", fmt.Errorf("PPTX contains no extractable slide text")
	}
	return text, nil
}

// parseXlsx extracts sheet rows (tab-joined cells) honoring shared strings,
// inline strings, booleans, and literal values.
func parseXlsx(entries map[string][]byte) (string, error) {
	shared := parseSharedStrings(entries["xl/sharedStrings.xml"])
	var sheetNames []string
	for name := range entries {
		if strings.HasPrefix(name, "xl/worksheets/sheet") && strings.HasSuffix(name, ".xml") {
			sheetNames = append(sheetNames, name)
		}
	}
	if len(sheetNames) == 0 {
		return "", fmt.Errorf("XLSX contains no extractable cells")
	}
	sort.Strings(sheetNames)
	var out strings.Builder
	for _, name := range sheetNames {
		decoder := xml.NewDecoder(bytes.NewReader(entries[name]))
		var cell strings.Builder
		var row strings.Builder
		cellType := ""
		inCell := false
		inInline := false
		inValue := false
		rowHasText := false
		flushCell := func() {
			text := strings.TrimSpace(cell.String())
			cell.Reset()
			if text != "" {
				if rowHasText {
					row.WriteString("\t")
				}
				row.WriteString(text)
				rowHasText = true
			}
			cellType = ""
			inCell = false
			inInline = false
			inValue = false
		}
		flushRow := func() {
			flushCell()
			if rowHasText {
				out.WriteString(row.String())
				out.WriteString("\n")
			}
			row.Reset()
			rowHasText = false
		}
		for {
			token, err := decoder.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				return "", fmt.Errorf("XLSX %s: %w", name, err)
			}
			switch t := token.(type) {
			case xml.StartElement:
				switch localName(t.Name.Local) {
				case "c":
					flushCell()
					inCell = true
					cellType = attrValue(t.Attr, "t")
				case "is":
					inInline = true
				case "v":
					inValue = true
				}
			case xml.EndElement:
				switch localName(t.Name.Local) {
				case "c":
					if inCell {
						flushCell()
					}
				case "is":
					inInline = false
				case "v":
					inValue = false
				case "row":
					flushRow()
				}
			case xml.CharData:
				switch {
				case inInline:
					cell.Write(t)
				case inValue && inCell:
					switch cellType {
					case "s":
						if idx := parseIndex(string(t)); idx >= 0 && idx < len(shared) {
							cell.WriteString(shared[idx])
						}
					case "b":
						if strings.TrimSpace(string(t)) == "1" {
							cell.WriteString("1")
						} else {
							cell.WriteString("0")
						}
					default:
						cell.Write(t)
					}
				}
			}
		}
	}
	text := strings.TrimRight(out.String(), "\n")
	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("XLSX contains no extractable cells")
	}
	return text, nil
}

type xlsxCell struct {
	Ref     string
	Value   string
	Formula string
}

type xlsxRow struct {
	Cells []xlsxCell
}

func (r xlsxRow) Text() string {
	values := make([]string, 0, len(r.Cells))
	for _, cell := range r.Cells {
		values = append(values, cell.Value)
	}
	return strings.Join(values, "\t")
}

func (r xlsxRow) Representation(sheet xlsxStructuredSheet) string {
	if len(sheet.Rows) == 0 || len(r.Cells) == 0 || r.Range() == sheet.Rows[0].Range() {
		return r.Text()
	}
	parts := make([]string, 0, len(r.Cells))
	for index, cell := range r.Cells {
		value := strings.TrimSpace(cell.Value)
		if value == "" && cell.Formula == "" {
			continue
		}
		if header := sheet.HeaderFor(index); header != "" {
			parts = append(parts, header+"="+value)
		} else {
			parts = append(parts, cell.Ref+"="+value)
		}
	}
	return strings.Join(parts, " | ")
}

func (r xlsxRow) Range() string {
	if len(r.Cells) == 0 {
		return ""
	}
	return r.Cells[0].Ref + ":" + r.Cells[len(r.Cells)-1].Ref
}

type xlsxStructuredSheet struct {
	Name        string
	Rows        []xlsxRow
	Merges      []string
	mergedCells map[string]struct{}
}

func (s xlsxStructuredSheet) Text() string {
	rows := make([]string, 0, len(s.Rows))
	for _, row := range s.Rows {
		if value := strings.TrimSpace(row.Text()); value != "" {
			rows = append(rows, value)
		}
	}
	return strings.Join(rows, "\n")
}

func (s xlsxStructuredSheet) Columns() string {
	if len(s.Rows) == 0 {
		return ""
	}
	values := make([]string, 0, len(s.Rows[0].Cells))
	for _, cell := range s.Rows[0].Cells {
		if value := strings.TrimSpace(cell.Value); value != "" {
			values = append(values, value)
		}
	}
	return strings.Join(values, ", ")
}

func (s xlsxStructuredSheet) UsedRange() string {
	var refs []string
	for _, row := range s.Rows {
		for _, cell := range row.Cells {
			if cell.Ref != "" {
				refs = append(refs, cell.Ref)
			}
		}
	}
	if len(refs) == 0 {
		return ""
	}
	minCol, minRow, maxCol, maxRow := 1<<30, 1<<30, 0, 0
	for _, ref := range refs {
		col, row := xlsxCellPosition(ref)
		if col == 0 || row == 0 {
			continue
		}
		if col < minCol {
			minCol = col
		}
		if row < minRow {
			minRow = row
		}
		if col > maxCol {
			maxCol = col
		}
		if row > maxRow {
			maxRow = row
		}
	}
	if maxCol == 0 || maxRow == 0 {
		return ""
	}
	return fmt.Sprintf("%s%d:%s%d", columnName(minCol), minRow, columnName(maxCol), maxRow)
}

func (s xlsxStructuredSheet) HeaderFor(index int) string {
	if len(s.Rows) == 0 || index < 0 || index >= len(s.Rows[0].Cells) {
		return ""
	}
	return strings.TrimSpace(s.Rows[0].Cells[index].Value)
}

func (s *xlsxStructuredSheet) indexMergedCells() {
	if s.mergedCells != nil {
		return
	}
	s.mergedCells = make(map[string]struct{}, len(s.Merges))
	for _, merge := range s.Merges {
		parts := strings.SplitN(merge, ":", 2)
		if len(parts) != 2 {
			continue
		}
		startCol, startRow := xlsxCellPosition(parts[0])
		endCol, endRow := xlsxCellPosition(parts[1])
		if startCol == 0 || startRow == 0 || endCol == 0 || endRow == 0 {
			continue
		}
		for row := startRow; row <= endRow; row++ {
			for col := startCol; col <= endCol; col++ {
				s.mergedCells[fmt.Sprintf("%s%d", columnName(col), row)] = struct{}{}
			}
		}
	}
}

func (s *xlsxStructuredSheet) IsMerged(ref string) bool {
	s.indexMergedCells()
	_, ok := s.mergedCells[ref]
	return ok
}

func xlsxStructuredSheets(entries map[string][]byte) []xlsxStructuredSheet {
	var names []string
	for name := range entries {
		if strings.HasPrefix(name, "xl/worksheets/sheet") && strings.HasSuffix(name, ".xml") {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	workbookNames := xlsxSheetNames(entries)
	shared := parseSharedStrings(entries["xl/sharedStrings.xml"])
	out := make([]xlsxStructuredSheet, 0, len(names))
	for index, name := range names {
		sheetName := fmt.Sprintf("Sheet%d", index+1)
		if index < len(workbookNames) && strings.TrimSpace(workbookNames[index]) != "" {
			sheetName = workbookNames[index]
		}
		sheet := xlsxStructuredSheet{Name: sheetName, Rows: parseXLSXRows(entries[name], shared), Merges: parseXLSXMerges(entries[name])}
		sheet.indexMergedCells()
		out = append(out, sheet)
	}
	return out
}

func parseXLSXRows(data []byte, shared []string) []xlsxRow {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	var rows []xlsxRow
	var current xlsxCell
	var row xlsxRow
	inCell, inValue, inFormula, inInline := false, false, false, false
	cellType := ""
	flushCell := func() {
		if inCell {
			if current.Ref == "" {
				current.Ref = fmt.Sprintf("%s%d", columnName(len(row.Cells)+1), len(rows)+1)
			}
			if cellType == "s" {
				if index := parseIndex(current.Value); index >= 0 && index < len(shared) {
					current.Value = shared[index]
				}
			} else if cellType == "b" {
				if strings.TrimSpace(current.Value) == "1" {
					current.Value = "1"
				} else {
					current.Value = "0"
				}
			}
			row.Cells = append(row.Cells, current)
		}
		current = xlsxCell{}
		cellType, inCell, inValue, inFormula, inInline = "", false, false, false, false
	}
	flushRow := func() {
		flushCell()
		if len(row.Cells) > 0 {
			rows = append(rows, row)
		}
		row = xlsxRow{}
	}
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}
		switch t := token.(type) {
		case xml.StartElement:
			switch localName(t.Name.Local) {
			case "row":
				if len(row.Cells) > 0 {
					flushRow()
				}
			case "c":
				flushCell()
				inCell = true
				current.Ref = attrValue(t.Attr, "r")
				cellType = attrValue(t.Attr, "t")
			case "v":
				inValue = true
			case "f":
				inFormula = true
			case "is":
				inInline = true
			case "t":
				if inInline {
					inValue = true
				}
			}
		case xml.EndElement:
			switch localName(t.Name.Local) {
			case "v":
				inValue = false
			case "f":
				inFormula = false
			case "is":
				inInline = false
			case "c":
				flushCell()
			case "row":
				flushRow()
			}
		case xml.CharData:
			if inFormula {
				current.Formula += string(t)
			}
			if inValue && inCell {
				current.Value += string(t)
			}
		}
	}
	flushRow()
	return rows
}

func parseXLSXMerges(data []byte) []string {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	var out []string
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}
		if start, ok := token.(xml.StartElement); ok && localName(start.Name.Local) == "mergeCell" {
			if ref := attrValue(start.Attr, "ref"); ref != "" {
				out = append(out, ref)
			}
		}
	}
	return out
}

func xlsxCellPosition(ref string) (int, int) {
	ref = strings.TrimSpace(ref)
	col, row := 0, 0
	for _, r := range ref {
		if r >= 'A' && r <= 'Z' {
			col = col*26 + int(r-'A'+1)
			continue
		}
		if r >= 'a' && r <= 'z' {
			col = col*26 + int(r-'a'+1)
			continue
		}
		if r >= '0' && r <= '9' {
			row = row*10 + int(r-'0')
		}
	}
	return col, row
}

func stringsToXLSXRows(text string) []xlsxRow {
	var rows []xlsxRow
	for rowIndex, line := range strings.Split(strings.TrimSpace(text), "\n") {
		var cells []xlsxCell
		for colIndex, value := range strings.Split(line, "\t") {
			cells = append(cells, xlsxCell{Ref: fmt.Sprintf("%s%d", columnName(colIndex+1), rowIndex+1), Value: strings.TrimSpace(value)})
		}
		rows = append(rows, xlsxRow{Cells: cells})
	}
	return rows
}

func parseSharedStrings(data []byte) []string {
	if len(data) == 0 {
		return nil
	}
	decoder := xml.NewDecoder(bytes.NewReader(data))
	var out []string
	var current strings.Builder
	inSI := false
	inT := false
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return out
		}
		switch t := token.(type) {
		case xml.StartElement:
			switch localName(t.Name.Local) {
			case "si":
				inSI = true
				current.Reset()
			case "t":
				inT = true
			}
		case xml.EndElement:
			switch localName(t.Name.Local) {
			case "si":
				out = append(out, current.String())
				inSI = false
			case "t":
				inT = false
			}
		case xml.CharData:
			if inSI && inT {
				current.Write(t)
			}
		}
	}
	return out
}

func parseIndex(value string) int {
	value = strings.TrimSpace(value)
	if value == "" {
		return -1
	}
	index := 0
	for _, r := range value {
		if r < '0' || r > '9' {
			return -1
		}
		index = index*10 + int(r-'0')
	}
	return index
}

// parseEpub converts xhtml/html pages into text, skipping nav/toc/cover.
func parseEpub(entries map[string][]byte) (string, string, error) {
	var names []string
	for name := range entries {
		lower := strings.ToLower(name)
		if strings.HasSuffix(lower, ".xhtml") || strings.HasSuffix(lower, ".html") || strings.HasSuffix(lower, ".htm") {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return "", "", fmt.Errorf("EPUB contains no extractable pages")
	}
	sort.Strings(names)
	var out strings.Builder
	title := ""
	for _, name := range names {
		base := strings.ToLower(pathBase(name))
		if strings.Contains(base, "nav") || strings.Contains(base, "toc") || strings.Contains(base, "cover") {
			continue
		}
		pageTitle, text := HTMLToText(string(entries[name]))
		if title == "" && pageTitle != "" {
			title = pageTitle
		}
		if strings.TrimSpace(text) != "" {
			out.WriteString(text)
			out.WriteString("\n\n")
		}
	}
	text := strings.TrimSpace(out.String())
	if text == "" {
		return "", "", fmt.Errorf("EPUB contains no extractable pages")
	}
	return title, text, nil
}

func pathBase(name string) string {
	if i := strings.LastIndexAny(name, "/\\"); i >= 0 {
		return name[i+1:]
	}
	return name
}
