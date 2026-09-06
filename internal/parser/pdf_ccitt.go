package parser

import (
	"bytes"
	"fmt"
	"image"
	"io"

	pdftext "github.com/ledongthuc/pdf"
	"golang.org/x/image/ccitt"
)

type pdfCCITTParameters struct {
	k                      int64
	columns                int64
	rows                   int64
	blackIsOne             bool
	encodedByteAlign       bool
	endOfLine              bool
	endOfBlock             bool
	damagedRowsBeforeError int64
}

func decodeCCITTPDFImage(document []byte, item pdftext.Value, width, height int) (image.Image, error) {
	dictionary, err := pdfImageDictionary(document, item)
	if err != nil {
		return nil, err
	}
	filters, err := pdfFilterNames(item)
	if err != nil {
		return nil, err
	}
	terminal, err := pdfTerminalImageFilter(filters)
	if err != nil || terminal != "CCITTFaxDecode" {
		return nil, fmt.Errorf("image is not a CCITT terminal image")
	}
	terminalIndex := -1
	for index, filter := range filters {
		if canonicalPDFFilter(filter) == terminal {
			terminalIndex = index
			break
		}
	}
	if terminalIndex != len(filters)-1 {
		return nil, fmt.Errorf("CCITT filter must be terminal")
	}
	parameters := pdfCCITTParametersAt(dictionary, terminalIndex, len(filters))
	if parameters.k > 0 {
		return nil, fmt.Errorf("CCITT Group3 2D images are not supported")
	}
	if parameters.endOfBlock || parameters.damagedRowsBeforeError > 0 {
		return nil, fmt.Errorf("CCITT recovery parameters are not supported")
	}
	if parameters.columns != 0 && parameters.columns != int64(width) {
		return nil, fmt.Errorf("CCITT columns %d do not match image width %d", parameters.columns, width)
	}
	if parameters.rows != 0 && parameters.rows != int64(height) {
		return nil, fmt.Errorf("CCITT rows %d do not match image height %d", parameters.rows, height)
	}

	encoded, err := readPDFImageStream(document, item)
	if err != nil {
		return nil, err
	}
	raw, err := decodePDFImageFilters(document, encoded, item, terminal)
	if err != nil {
		return nil, err
	}

	subformat := ccitt.Group3
	if parameters.k < 0 {
		subformat = ccitt.Group4
	} else if !parameters.endOfLine {
		return nil, fmt.Errorf("CCITT Group3 requires EndOfLine")
	}
	options := &ccitt.Options{
		Align:  parameters.encodedByteAlign,
		Invert: parameters.blackIsOne,
	}
	expected := pdfRowBytes(width, 1, 1) * height
	decoded := make([]byte, expected)
	reader := ccitt.NewReader(bytes.NewReader(raw), ccitt.MSB, subformat, width, height, options)
	if _, err := io.ReadFull(reader, decoded); err != nil {
		return nil, fmt.Errorf("decode CCITT image: %w", err)
	}
	space := pdfColorSpace{kind: "gray", components: 1, bits: 1}
	return samplesToImage(decoded, width, height, space)
}

func pdfCCITTParametersAt(dictionary []byte, index, count int) pdfCCITTParameters {
	parameters := pdfCCITTParameters{}
	raw, ok := pdfDictionaryValue(dictionary, "DecodeParms")
	if !ok {
		return parameters
	}
	if values := pdfArrayValues(raw); values != nil {
		if index < 0 || index >= len(values) {
			return parameters
		}
		raw = values[index]
	}
	if !bytes.HasPrefix(raw, []byte("<<")) {
		return parameters
	}
	parameters.k = pdfDictionaryNumber(raw, "K", 0)
	parameters.columns = pdfDictionaryNumber(raw, "Columns", 0)
	parameters.rows = pdfDictionaryNumber(raw, "Rows", 0)
	parameters.blackIsOne = pdfDictionaryBool(raw, "BlackIs1", false)
	parameters.encodedByteAlign = pdfDictionaryBool(raw, "EncodedByteAlign", false)
	parameters.endOfLine = pdfDictionaryBool(raw, "EndOfLine", false)
	parameters.endOfBlock = pdfDictionaryBool(raw, "EndOfBlock", false)
	parameters.damagedRowsBeforeError = pdfDictionaryNumber(raw, "DamagedRowsBeforeError", 0)
	return parameters
}

func pdfDictionaryBool(dictionary []byte, key string, fallback bool) bool {
	raw, ok := pdfDictionaryValue(dictionary, key)
	if !ok {
		return fallback
	}
	switch string(raw) {
	case "true":
		return true
	case "false":
		return false
	default:
		return fallback
	}
}
