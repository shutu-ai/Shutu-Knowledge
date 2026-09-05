package parser

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
)

var slideNamePattern = regexp.MustCompile(`ppt/slides/slide([0-9]+)\.xml$`)

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
