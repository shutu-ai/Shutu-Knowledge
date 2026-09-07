package parser

import (
	"bytes"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"path/filepath"
)

// imageParser validates raster image uploads and deliberately leaves text
// extraction to the configured OCR pipeline. Returning a parser result keeps
// image imports in the same parse/chunk/index lifecycle as PDFs while making
// the OCR requirement explicit instead of silently indexing an empty file.
type imageParser struct{}

func (imageParser) Extensions() []string { return []string{"png", "jpg", "jpeg"} }

func (imageParser) Parse(fileName string, data []byte) (Result, error) {
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return Result{}, fmt.Errorf("decode %s image: %w", filepath.Ext(fileName), err)
	}
	if config.Width <= 0 || config.Height <= 0 {
		return Result{}, fmt.Errorf("image has no pixels")
	}
	return Result{Title: filepath.Base(fileName), NeedsOCR: true}, nil
}
