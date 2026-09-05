package parser

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"path"
	"strings"
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
		return Result{Text: text}, err
	case "pptx":
		text, err := parsePptx(entries)
		return Result{Text: text}, err
	case "xlsx":
		text, err := parseXlsx(entries)
		return Result{Text: text}, err
	case "epub":
		title, text, err := parseEpub(entries)
		return Result{Title: title, Text: text}, err
	}
	return Result{}, &UnsupportedError{Ext: ExtensionOf(fileName)}
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
