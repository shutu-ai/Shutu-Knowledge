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

func (zipOfficeParser) Extensions() []string { return []string{"docx", "pptx", "xlsx", "epub"} }

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
	case "xlsx":
		text, err := parseXlsx(entries)
		return officeResult("xlsx", text, err, entries), err
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
		for index, part := range strings.Split(strings.TrimSpace(text), "\n\n") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			n := index + 1
			slideTitle := strings.TrimSpace(strings.SplitN(part, "\n", 2)[0])
			d.Nodes = append(d.Nodes, documentir.Node{ID: fmt.Sprintf("tmp:slide/%d", n), Type: documentir.TypeSlide, ParentID: "tmp:document", Order: n, Text: part, HeadingPath: []string{slideTitle}, Metadata: map[string]string{"title": slideTitle}, SlideNumber: n, SourceAnchor: documentir.SourceAnchor{Kind: "pptx", Slide: n, LogicalPath: fmt.Sprintf("slide/%d", n)}, Parser: format, ParserVersion: "builtin-v1"})
			d.Nodes = append(d.Nodes, documentir.Node{ID: fmt.Sprintf("tmp:slide/%d/block/1", n), Type: documentir.TypeBlock, ParentID: fmt.Sprintf("tmp:slide/%d", n), Order: 1, Text: part, SlideNumber: n, SourceAnchor: documentir.SourceAnchor{Kind: "pptx", Slide: n, Shape: "1", LogicalPath: fmt.Sprintf("slide/%d/shape/1", n)}, Parser: format, ParserVersion: "builtin-v1"})
		}
		return d
	}
	// XLSX text is tab-separated rows. Preserve sheet/cell positions in the
	// initial backend-independent projection; a future workbook relationship
	// reader can replace the stable Sheet1 fallback without changing callers.
	sheetName := "Sheet1"
	if names := xlsxSheetNames(entries); len(names) > 0 {
		sheetName = names[0]
	}
	sheetID := "tmp:sheet/1"
	d.Nodes = append(d.Nodes, documentir.Node{ID: sheetID, Type: documentir.TypeSheet, ParentID: "tmp:document", Order: 1, SheetName: sheetName, SourceAnchor: documentir.SourceAnchor{Kind: "xlsx", Sheet: sheetName, LogicalPath: "sheet/1"}, Parser: format, ParserVersion: "builtin-v1"})
	d.Nodes[len(d.Nodes)-1].Text = strings.TrimSpace(text)
	for rowIndex, line := range strings.Split(strings.TrimSpace(text), "\n") {
		for colIndex, value := range strings.Split(line, "\t") {
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			cell := fmt.Sprintf("%s%d", columnName(colIndex+1), rowIndex+1)
			d.Nodes = append(d.Nodes, documentir.Node{ID: fmt.Sprintf("tmp:cell/%s", cell), Type: documentir.TypeTableCell, ParentID: sheetID, Order: rowIndex*1000 + colIndex, Text: value, SheetName: sheetName, SourceAnchor: documentir.SourceAnchor{Kind: "xlsx", Sheet: sheetName, CellRange: cell, LogicalPath: "sheet/1/cell/" + cell}, Parser: format, ParserVersion: "builtin-v1"})
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
	var inP, inT, inCell bool
	var style string
	var current strings.Builder
	order := 0
	headingPath := []string{}
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
			case "p":
				inP = true
				current.Reset()
				style = ""
			case "pStyle":
				style = attrValue(t.Attr, "val")
			case "t":
				inT = true
			case "tab":
				if inP {
					current.WriteString("\t")
				}
			case "tc":
				inCell = true
			}
		case xml.EndElement:
			switch localName(t.Name.Local) {
			case "t":
				inT = false
			case "tc":
				inCell = false
			case "p":
				value := strings.TrimSpace(current.String())
				if value == "" {
					inP = false
					continue
				}
				order++
				isHeading := strings.Contains(strings.ToLower(style), "heading")
				typ := documentir.TypeParagraph
				if isHeading {
					typ = documentir.TypeHeading
					headingPath = append(headingPath[:minInt(len(headingPath), 1)], value)
				}
				if inCell {
					typ = documentir.TypeTableCell
				}
				path := fmt.Sprintf("paragraph/%d", order)
				if inCell {
					path = fmt.Sprintf("table/cell/%d", order)
				}
				d.Nodes = append(d.Nodes, documentir.Node{ID: fmt.Sprintf("tmp:%s", path), Type: typ, ParentID: "tmp:document", Order: order, Text: value, HeadingPath: append([]string(nil), headingPath...), SourceAnchor: documentir.SourceAnchor{Kind: "docx", Paragraph: order, LogicalPath: path}, Parser: "docx", ParserVersion: "builtin-v1"})
				inP = false
			}
		case xml.CharData:
			if inP && inT {
				current.Write(t)
			}
		}
	}
	return d
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
