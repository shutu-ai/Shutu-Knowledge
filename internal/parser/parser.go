// Package parser implements the parser registry: dispatch on file extension
// or MIME type, never an unbounded if-chain in the caller. Every parser
// returns a normalized Result (title + text). Missing or corrupt content
// surfaces as an error naming the format, so the import layer can classify
// parse_failed without masking text it did extract.
package parser

import (
	"bytes"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// Result is the normalized parser output.
type Result struct {
	Title string
	Text  string
}

// Parser extracts text from one format family.
type Parser interface {
	// Extensions handled by this parser (lowercase, without dot).
	Extensions() []string
	// Parse converts raw bytes into normalized text.
	Parse(fileName string, data []byte) (Result, error)
}

// Registry dispatches documents to parsers by extension.
type Registry struct {
	byExt map[string]Parser
}

// NewRegistry registers the built-in parsers.
func NewRegistry() *Registry {
	r := &Registry{byExt: map[string]Parser{}}
	textParser := &textParser{}
	for _, ext := range []string{"txt", "md", "markdown", "mdx", "csv", "json", "log"} {
		r.byExt[ext] = textParser
	}
	html := &htmlParser{}
	for _, ext := range []string{"html", "htm"} {
		r.byExt[ext] = html
	}
	office := &zipOfficeParser{}
	r.byExt["docx"] = office
	r.byExt["pptx"] = office
	r.byExt["xlsx"] = office
	r.byExt["epub"] = office
	pdf := &pdfParser{}
	r.byExt["pdf"] = pdf
	return r
}

// SupportedExtensions lists every registered extension (sorted).
func (r *Registry) SupportedExtensions() []string {
	out := make([]string, 0, len(r.byExt))
	for ext := range r.byExt {
		out = append(out, ext)
	}
	sort.Strings(out)
	return out
}

// UnsupportedError reports a format no parser accepts (add-time rejection).
type UnsupportedError struct{ Ext string }

func (e *UnsupportedError) Error() string { return "unsupported document format: " + e.Ext }

// ExtensionOf returns the lowercased extension without the dot ("" if none).
func ExtensionOf(fileName string) string {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(fileName), "."))
	return ext
}

// Parse dispatches on the file's extension. A pdf MIME type with a mismatched
// extension still parses as PDF, mirroring the reference behavior.
func (r *Registry) Parse(fileName string, data []byte) (Result, error) {
	ext := ExtensionOf(fileName)
	parser, ok := r.byExt[ext]
	if !ok && (strings.EqualFold(filepath.Ext(fileName), "") && looksLikePDF(data)) {
		parser, ok = r.byExt["pdf"], true
	}
	if !ok {
		return Result{}, &UnsupportedError{Ext: ext}
	}
	result, err := parser.Parse(fileName, data)
	if err != nil {
		return result, fmt.Errorf("%s: %w", ext, err)
	}
	return result, nil
}

func looksLikePDF(data []byte) bool {
	return len(data) >= 5 && bytes.Equal(bytes.TrimSpace(data[:5]), []byte("%PDF-"))
}

// textParser decodes text-like formats: UTF-8 with a GB18030 fallback
// (Chinese txt/csv/log exports are often GBK; decoding them as UTF-8
// silently yields replacement garbage).
type textParser struct{}

func (textParser) Extensions() []string {
	return []string{"txt", "md", "markdown", "mdx", "csv", "json", "log"}
}

func (textParser) Parse(_ string, data []byte) (Result, error) {
	text := DecodeText(data)
	if strings.TrimSpace(text) == "" {
		return Result{}, fmt.Errorf("contains no extractable text")
	}
	return Result{Text: text}, nil
}

// DecodeText decodes bytes as UTF-8 (BOM-aware), falling back to GB18030
// when the UTF-8 decode produced bulk replacement characters.
func DecodeText(data []byte) string {
	if len(data) >= 3 && data[0] == 0xef && data[1] == 0xbb && data[2] == 0xbf {
		return string(data[3:])
	}
	utf8Text := string(data)
	if utf8.Valid(data) {
		return utf8Text
	}
	// Invalid UTF-8: prefer GB18030 (superset of GBK/GB2312) over garbage.
	if decoded, err := simplifiedchinese.GB18030.NewDecoder().Bytes(data); err == nil && utf8.Valid(decoded) {
		return string(decoded)
	}
	return utf8Text
}
