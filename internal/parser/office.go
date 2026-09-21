package parser

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/shutu-ai/shutu-knowledge/internal/documentir"
)

// maxArchiveBytes caps the total uncompressed size accepted from an office
// archive (zip-bomb guard).
const maxArchiveBytes = 256 << 20

// zipOfficeParser handles the OOXML/EPUB container formats: the document is
// a zip; text is extracted from well-known XML parts. Legacy OLE formats
// (.doc/.ppt/.xls) intentionally register no parser here and surface as
// unsupported until the optional external helper lands in a later phase.
type zipOfficeParser struct{}

func (zipOfficeParser) Extensions() []string { return []string{"docx", "pptx", "xlsx", "xlsm", "epub"} }

func (p zipOfficeParser) Parse(fileName string, data []byte) (Result, error) {
	entries, err := readArchive(data)
	if err != nil {
		return Result{}, err
	}
	switch ExtensionOf(fileName) {
	case "docx":
		text, err := parseDocx(entries)
		return officeResult("docx", text, err, entries), err
	case "pptx":
		text, err := parsePptx(entries)
		return officeResult("pptx", text, err, entries), err
	case "xlsx", "xlsm":
		text, err := parseXlsx(entries)
		return officeResult(ExtensionOf(fileName), text, err, entries), err
	case "epub":
		title, text, err := parseEpub(entries)
		return Result{Title: title, Text: text, IR: documentir.FromText(title, text, "epub", "builtin-v1"), Parser: "epub", ParserVersion: "builtin-v1"}, err
	}
	return Result{}, &UnsupportedError{Ext: ExtensionOf(fileName)}
}

func officeResult(format, text string, err error, entries map[string][]byte) Result {
	if err != nil {
		return Result{Parser: format, ParserVersion: "builtin-v1"}
	}
	return Result{Text: text, IR: officeIR(format, text, entries), Parser: format, ParserVersion: "builtin-v1"}
}

func officeIR(format, text string, entries map[string][]byte) *documentir.Document {
	if format == "docx" {
		return docxIR(entries, text)
	}
	d := &documentir.Document{IRVersion: documentir.Version, Parser: format, ParserVersion: "builtin-v1"}
	d.Nodes = append(d.Nodes, documentir.Node{ID: "tmp:document", Type: documentir.TypeDocument, Order: 0, Text: strings.TrimSpace(text), SourceAnchor: documentir.SourceAnchor{Kind: format, LogicalPath: "document"}, Parser: format, ParserVersion: "builtin-v1"})
	if format == "pptx" {
		return pptxIR(entries, text)
	}
	// XLSX gets a sheet/table/row/cell projection. The text projection remains
	// the legacy tab-separated output, while formulas, headers, merged ranges,
	// and used-range metadata stay attached to the corresponding cells.
	sheets := xlsxStructuredSheets(entries)
	if len(sheets) == 0 {
		sheets = []xlsxStructuredSheet{{Name: "Sheet1", Rows: stringsToXLSXRows(text)}}
	}
	order := 0
	for sheetIndex, sheet := range sheets {
		order++
		sheetID := fmt.Sprintf("tmp:sheet/%d", sheetIndex+1)
		sheetNode := documentir.Node{ID: sheetID, Type: documentir.TypeSheet, ParentID: "tmp:document", Order: order, SheetName: sheet.Name, SourceAnchor: documentir.SourceAnchor{Kind: "xlsx", Sheet: sheet.Name, LogicalPath: fmt.Sprintf("sheet/%d", sheetIndex+1)}, Parser: format, ParserVersion: "builtin-v1"}
		sheetNode.Text = strings.TrimSpace(sheet.Text())
		sheetNode.Metadata = map[string]string{"used_range": sheet.UsedRange()}
		d.Nodes = append(d.Nodes, sheetNode)
		if len(sheet.Rows) == 0 {
			continue
		}
		order++
		tableID := fmt.Sprintf("tmp:sheet/%d/table/1", sheetIndex+1)
		tableNodeIndex := len(d.Nodes)
		d.Nodes = append(d.Nodes, documentir.Node{ID: tableID, Type: documentir.TypeTable, ParentID: sheetID, Order: order, SheetName: sheet.Name, Metadata: map[string]string{"table_like": "true", "used_range": sheet.UsedRange()}, SourceAnchor: documentir.SourceAnchor{Kind: "xlsx", Sheet: sheet.Name, LogicalPath: fmt.Sprintf("sheet/%d/table/1", sheetIndex+1)}, Parser: format, ParserVersion: "builtin-v1"})
		for rowIndex, row := range sheet.Rows {
			order++
			if rowIndex == 0 {
				d.Nodes[tableNodeIndex].Text = fmt.Sprintf("Table: %s; Columns: %s", sheet.Name, sheet.Columns())
			}
			rowID := fmt.Sprintf("tmp:sheet/%d/row/%d", sheetIndex+1, rowIndex+1)
			rowNode := documentir.Node{ID: rowID, Type: documentir.TypeTableRow, ParentID: tableID, Order: order, Text: row.Representation(sheet), SheetName: sheet.Name, SourceAnchor: documentir.SourceAnchor{Kind: "xlsx", Sheet: sheet.Name, CellRange: row.Range(), LogicalPath: fmt.Sprintf("sheet/%d/row/%d", sheetIndex+1, rowIndex+1)}, Parser: format, ParserVersion: "builtin-v1"}
			d.Nodes = append(d.Nodes, rowNode)
			for cellIndex, cell := range row.Cells {
				if strings.TrimSpace(cell.Value) == "" && cell.Formula == "" {
					continue
				}
				metadata := map[string]string{}
				if cell.Formula != "" {
					metadata["formula"] = cell.Formula
				}
				if rowIndex == 0 {
					metadata["header"] = "true"
				} else if header := sheet.HeaderFor(cellIndex); header != "" {
					metadata["header_value"] = header
				}
				if sheet.IsMerged(cell.Ref) {
					metadata["merged"] = "true"
				}
				metadata["position"] = cell.Ref
				metadata["row_span"] = "1"
				metadata["col_span"] = "1"
				logical := fmt.Sprintf("sheet/%d/cell/%s", sheetIndex+1, cell.Ref)
				d.Nodes = append(d.Nodes, documentir.Node{ID: "tmp:" + logical, Type: documentir.TypeTableCell, ParentID: rowID, Order: cellIndex, Text: cell.Value, SheetName: sheet.Name, Metadata: metadata, SourceAnchor: documentir.SourceAnchor{Kind: "xlsx", Sheet: sheet.Name, CellRange: cell.Ref, LogicalPath: logical}, Parser: format, ParserVersion: "builtin-v1"})
			}
		}
	}
	return d
}

func xlsxSheetNames(entries map[string][]byte) []string {
	data := entries["xl/workbook.xml"]
	if len(data) == 0 {
		return nil
	}
	decoder := xml.NewDecoder(bytes.NewReader(data))
	var names []string
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return names
		}
		if start, ok := token.(xml.StartElement); ok && localName(start.Name.Local) == "sheet" {
			if name := attrValue(start.Attr, "name"); name != "" {
				names = append(names, name)
			}
		}
	}
	return names
}

func docxIR(entries map[string][]byte, text string) *documentir.Document {
	d := &documentir.Document{IRVersion: documentir.Version, Parser: "docx", ParserVersion: "builtin-v1"}
	d.Nodes = append(d.Nodes, documentir.Node{ID: "tmp:document", Type: documentir.TypeDocument, Order: 0, Text: strings.TrimSpace(text), SourceAnchor: documentir.SourceAnchor{Kind: "docx", LogicalPath: "document"}, Parser: "docx", ParserVersion: "builtin-v1"})
	data := entries["word/document.xml"]
	if len(data) == 0 {
		return d
	}
	decoder := xml.NewDecoder(bytes.NewReader(data))
	var inP, inT, inCell, inList bool
	var style string
	var current, cellText strings.Builder
	order := 0
	cellOrder := 0
	headingPath := []string{}
	tableID, rowID, listID := "", "", ""
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return d
		}
		switch t := token.(type) {
		case xml.StartElement:
			switch localName(t.Name.Local) {
			case "tbl":
				order++
				tableID = fmt.Sprintf("tmp:table/%d", order)
				d.Nodes = append(d.Nodes, documentir.Node{ID: tableID, Type: documentir.TypeTable, ParentID: "tmp:document", Order: order, SourceAnchor: documentir.SourceAnchor{Kind: "docx", LogicalPath: fmt.Sprintf("table/%d", order)}, Parser: "docx", ParserVersion: "builtin-v1"})
			case "tr":
				order++
				rowID = fmt.Sprintf("%s/row/%d", tableID, order)
				d.Nodes = append(d.Nodes, documentir.Node{ID: rowID, Type: documentir.TypeTableRow, ParentID: tableID, Order: order, SourceAnchor: documentir.SourceAnchor{Kind: "docx", LogicalPath: fmt.Sprintf("table/%d/row/%d", order, order)}, Parser: "docx", ParserVersion: "builtin-v1"})
			case "tc":
				inCell = true
				cellOrder++
				cellText.Reset()
			case "p":
				inP = true
				current.Reset()
				style = ""
				inList = false
			case "pStyle":
				style = attrValue(t.Attr, "val")
			case "numPr":
				inList = true
				if listID == "" {
					order++
					listID = fmt.Sprintf("tmp:list/%d", order)
					d.Nodes = append(d.Nodes, documentir.Node{ID: listID, Type: documentir.TypeList, ParentID: "tmp:document", Order: order, SourceAnchor: documentir.SourceAnchor{Kind: "docx", LogicalPath: fmt.Sprintf("list/%d", order)}, Parser: "docx", ParserVersion: "builtin-v1"})
				}
			case "t":
				inT = true
			case "tab":
				if inP {
					current.WriteString("\t")
				}
			case "br":
				if inP {
					current.WriteString("\n")
				}
			}
		case xml.EndElement:
			switch localName(t.Name.Local) {
			case "t":
				inT = false
			case "p":
				value := strings.TrimSpace(current.String())
				if value == "" {
					inP = false
					continue
				}
				order++
				if inCell {
					if cellText.Len() > 0 {
						cellText.WriteString(" ")
					}
					cellText.WriteString(value)
					inP = false
					continue
				}
				styleLower := strings.ToLower(style)
				isHeading := strings.Contains(styleLower, "heading")
				typ := documentir.TypeParagraph
				if isHeading {
					typ = documentir.TypeHeading
					level := docxHeadingLevel(styleLower)
					if level > len(headingPath)+1 {
						level = len(headingPath) + 1
					}
					if level < 1 {
						level = 1
					}
					headingPath = append(headingPath[:minInt(level-1, len(headingPath))], value)
				} else if strings.Contains(styleLower, "caption") {
					typ = documentir.TypeCaption
				}
				parent := "tmp:document"
				if inList {
					parent = listID
					typ = documentir.TypeListItem
				}
				logical := fmt.Sprintf("paragraph/%d", order)
				d.Nodes = append(d.Nodes, documentir.Node{ID: "tmp:" + logical, Type: typ, ParentID: parent, Order: order, Text: value, HeadingPath: append([]string(nil), headingPath...), SourceAnchor: documentir.SourceAnchor{Kind: "docx", Paragraph: order, Section: strings.Join(headingPath, " / "), LogicalPath: logical}, Parser: "docx", ParserVersion: "builtin-v1"})
				inP = false
			case "tc":
				if strings.TrimSpace(cellText.String()) != "" {
					logical := fmt.Sprintf("table/cell/%d", cellOrder)
					d.Nodes = append(d.Nodes, documentir.Node{ID: "tmp:" + logical, Type: documentir.TypeTableCell, ParentID: rowID, Order: cellOrder, Text: strings.TrimSpace(cellText.String()), Metadata: map[string]string{"position": fmt.Sprintf("%d", cellOrder), "row_span": "1", "col_span": "1"}, SourceAnchor: documentir.SourceAnchor{Kind: "docx", Paragraph: order, LogicalPath: logical}, Parser: "docx", ParserVersion: "builtin-v1"})
				}
				cellText.Reset()
				inCell = false
			case "tr":
				rowID = ""
			case "tbl":
				tableID = ""
			case "sectPr":
				order++
				logical := fmt.Sprintf("section/%d", order)
				d.Nodes = append(d.Nodes, documentir.Node{ID: "tmp:" + logical, Type: documentir.TypeSection, ParentID: "tmp:document", Order: order, HeadingPath: append([]string(nil), headingPath...), SourceAnchor: documentir.SourceAnchor{Kind: "docx", Section: strings.Join(headingPath, " / "), LogicalPath: logical}, Parser: "docx", ParserVersion: "builtin-v1"})
			}
		case xml.CharData:
			if inP && inT {
				current.Write(t)
			}
		}
	}
	return d
}

func docxHeadingLevel(style string) int {
	level := 0
	place := 1
	for i := len(style) - 1; i >= 0 && style[i] >= '0' && style[i] <= '9'; i-- {
		level += int(style[i]-'0') * place
		place *= 10
	}
	if level == 0 {
		return 1
	}
	return level
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func columnName(value int) string {
	if value <= 0 {
		return "A"
	}
	var out string
	for value > 0 {
		value--
		out = string(rune('A'+value%26)) + out
		value /= 26
	}
	return out
}

func readArchive(data []byte) (map[string][]byte, error) {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("archive parsing failed: %w", err)
	}
	entries := make(map[string][]byte, len(reader.File))
	total := int64(0)
	for _, file := range reader.File {
		if file.UncompressedSize64 > maxArchiveBytes {
			return nil, fmt.Errorf("archive entry too large: %s", file.Name)
		}
		total += int64(file.UncompressedSize64)
		if total > maxArchiveBytes {
			return nil, fmt.Errorf("archive too large to unpack (%d MB uncompressed)", total>>20)
		}
		rc, err := file.Open()
		if err != nil {
			return nil, fmt.Errorf("open archive entry %s: %w", file.Name, err)
		}
		content, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			return nil, fmt.Errorf("read archive entry %s: %w", file.Name, err)
		}
		entries[path.Clean(strings.ReplaceAll(file.Name, "\\", "/"))] = content
	}
	return entries, nil
}

// parseDocx extracts paragraph text from word/document.xml.
func parseDocx(entries map[string][]byte) (string, error) {
	data, ok := entries["word/document.xml"]
	if !ok {
		return "", fmt.Errorf("DOCX contains no word/document.xml")
	}
	decoder := xml.NewDecoder(bytes.NewReader(data))
	var out strings.Builder
	depthP := 0
	inT := false
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("DOCX document.xml: %w", err)
		}
		switch t := token.(type) {
		case xml.StartElement:
			switch localName(t.Name.Local) {
			case "p":
				depthP++
			case "t":
				inT = true
			case "tab":
				out.WriteString("\t")
			case "br":
				out.WriteString("\n")
			}
		case xml.EndElement:
			switch localName(t.Name.Local) {
			case "p":
				if depthP > 0 {
					depthP--
					out.WriteString("\n\n")
				}
			case "t":
				inT = false
			}
		case xml.CharData:
			if inT {
				out.Write(t)
			}
		}
	}
	text := normalizeWhitespace(out.String())
	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("DOCX contains no extractable text")
	}
	return text, nil
}

func localName(name string) string {
	if i := strings.IndexByte(name, ':'); i >= 0 {
		return name[i+1:]
	}
	return name
}

func attrValue(attrs []xml.Attr, local string) string {
	for _, attr := range attrs {
		if localName(attr.Name.Local) == local {
			return attr.Value
		}
	}
	return ""
}

func normalizeWhitespace(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	for strings.Contains(text, "\n\n\n") {
		text = strings.ReplaceAll(text, "\n\n\n", "\n\n")
	}
	return strings.TrimSpace(text)
}
