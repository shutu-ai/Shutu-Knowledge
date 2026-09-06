package parser

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image/png"
)

const (
	// Match the upstream page/raster ceilings while requiring each rendered
	// page to remain a bounded PNG.
	MaxOCRRenderPages      = 100
	maxOCRRenderPagePixels = 32 << 20
	maxOCRRenderPixels     = maxPDFRasterBytes / 4
	// Base64 plus JSON envelope overhead is bounded separately from decoded
	// pixels; the renderer may not stream an unbounded response into memory.
	MaxOCRRenderOutputBytes = 512 << 20
)

type RenderedPDFPage struct {
	Page   int
	Width  int
	Height int
	PNG    []byte
}

type renderedPDFPageEnvelope struct {
	Page int    `json:"page"`
	PNG  string `json:"png"`
}

type renderedPDFEnvelope struct {
	Pages []renderedPDFPageEnvelope `json:"pages"`
}

// ParseRenderedPDFPages validates the optional full-page renderer response.
// Pages must be ordered, uniquely numbered, within the upstream page ceiling,
// and collectively bounded so OCR cannot memory-spike on a forged response.
func ParseRenderedPDFPages(output []byte) ([]RenderedPDFPage, error) {
	var envelope renderedPDFEnvelope
	decoder := json.NewDecoder(bytes.NewReader(output))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil {
		return nil, fmt.Errorf("decode rendered PDF response: %w", err)
	}
	if len(envelope.Pages) == 0 || len(envelope.Pages) > MaxOCRRenderPages {
		return nil, fmt.Errorf("rendered PDF returned %d pages", len(envelope.Pages))
	}
	pages := make([]RenderedPDFPage, 0, len(envelope.Pages))
	totalPixels := 0
	previousPage := 0
	for index, entry := range envelope.Pages {
		if entry.Page <= previousPage || entry.Page > MaxOCRRenderPages {
			return nil, fmt.Errorf("rendered PDF pages are not ordered in 1..%d at index %d", MaxOCRRenderPages, index)
		}
		pngData, err := base64.StdEncoding.DecodeString(entry.PNG)
		if err != nil {
			return nil, fmt.Errorf("decode rendered PDF page %d: %w", entry.Page, err)
		}
		config, err := png.DecodeConfig(bytes.NewReader(pngData))
		if err != nil {
			return nil, fmt.Errorf("decode rendered PDF page %d PNG: %w", entry.Page, err)
		}
		if config.Width <= 0 || config.Height <= 0 || config.Width > 20_000 || config.Height > 20_000 ||
			config.Width*config.Height > maxOCRRenderPagePixels {
			return nil, fmt.Errorf("rendered PDF page %d has invalid dimensions %dx%d", entry.Page, config.Width, config.Height)
		}
		totalPixels += config.Width * config.Height
		if totalPixels > maxOCRRenderPixels {
			return nil, fmt.Errorf("rendered PDF exceeds %d total pixels", maxOCRRenderPixels)
		}
		pages = append(pages, RenderedPDFPage{
			Page: entry.Page, Width: config.Width, Height: config.Height, PNG: pngData,
		})
		previousPage = entry.Page
	}
	return pages, nil
}
