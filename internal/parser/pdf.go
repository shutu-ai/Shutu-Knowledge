package parser

import (
	"bytes"
	"fmt"

	pdftext "github.com/ledongthuc/pdf"
)

// pdfParser extracts the text layer of a PDF with a pure-Go reader. Scanned
// or corrupted text layers are handled by the OCR fallback in a later phase;
// here an empty extraction is an error the import layer classifies.
type pdfParser struct{}

func (pdfParser) Extensions() []string { return []string{"pdf"} }

func (pdfParser) Parse(_ string, data []byte) (Result, error) {
	reader, err := pdftext.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return Result{}, fmt.Errorf("PDF parsing failed: %w", err)
	}
	content, err := reader.GetPlainText()
	if err != nil {
		return Result{}, fmt.Errorf("PDF text extraction failed: %w", err)
	}
	var out bytes.Buffer
	if _, err := out.ReadFrom(content); err != nil {
		return Result{}, fmt.Errorf("PDF text extraction failed: %w", err)
	}
	if len(bytes.TrimSpace(out.Bytes())) == 0 {
		return Result{}, fmt.Errorf("PDF contains no extractable text (it may be scanned)")
	}
	return Result{Text: out.String()}, nil
}
