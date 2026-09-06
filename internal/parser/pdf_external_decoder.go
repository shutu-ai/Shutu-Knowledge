package parser

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"

	pdftext "github.com/ledongthuc/pdf"
)

type pdfImageDecodeRequest struct {
	Format  string `json:"format"`
	Data    string `json:"data"`
	Globals string `json:"globals,omitempty"`
}

// decodeExternalPDFImage invokes the optional deployment-owned image decoder.
// Terminal codec bytes are passed after any preceding ASCII/RunLength/LZW/
// Flate filters. JBIG2 global segments are forwarded in the JSON envelope.
func decodeExternalPDFImage(
	document []byte,
	item pdftext.Value,
	width, height int,
	terminal string,
	options PDFImageOptions,
) (image.Image, error) {
	if options.Decode == nil {
		return nil, fmt.Errorf("no optional %s image decoder is configured", terminal)
	}
	encoded, err := readPDFImageStream(document, item)
	if err != nil {
		return nil, err
	}
	codecData, err := decodePDFImageFilters(document, encoded, item, terminal)
	if err != nil {
		return nil, err
	}
	if len(codecData) == 0 {
		return nil, fmt.Errorf("%s image stream is empty", terminal)
	}

	request := pdfImageDecodeRequest{
		Format: externalPDFFormatName(terminal),
		Data:   base64.StdEncoding.EncodeToString(codecData),
	}
	if globals := item.Key("JBIG2Globals"); terminal == "JBIG2Decode" && !globals.IsNull() {
		globalsEncoded, err := readPDFImageStream(document, globals)
		if err != nil {
			return nil, err
		}
		globalsData, err := decodePDFImageFilters(document, globalsEncoded, globals, "")
		if err != nil {
			return nil, err
		}
		request.Globals = base64.StdEncoding.EncodeToString(globalsData)
	}
	envelope, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("encode image decoder request: %w", err)
	}
	if len(envelope) > maxPDFImageStreamBytes {
		return nil, fmt.Errorf("image decoder request exceeds %d bytes", maxPDFImageStreamBytes)
	}

	decodedPNG, err := options.Decode(options.context(), request.Format, envelope)
	if err != nil {
		return nil, fmt.Errorf("decode %s image: %w", request.Format, err)
	}
	config, err := png.DecodeConfig(bytes.NewReader(decodedPNG))
	if err != nil {
		return nil, fmt.Errorf("decode %s helper PNG: %w", request.Format, err)
	}
	if config.Width != width || config.Height != height {
		return nil, fmt.Errorf("%s helper dimensions %dx%d do not match PDF image %dx%d", request.Format, config.Width, config.Height, width, height)
	}
	decoded, err := png.Decode(bytes.NewReader(decodedPNG))
	if err != nil {
		return nil, fmt.Errorf("decode %s helper PNG: %w", request.Format, err)
	}
	return decoded, nil
}

func externalPDFFormatName(terminal string) string {
	if terminal == "JBIG2Decode" {
		return "jbig2"
	}
	return "jpx"
}
