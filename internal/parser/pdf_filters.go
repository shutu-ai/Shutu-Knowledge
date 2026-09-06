package parser

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"io"

	pdftext "github.com/ledongthuc/pdf"
)

func pdfFilterNames(item pdftext.Value) ([]string, error) {
	filter := item.Key("Filter")
	switch filter.Kind() {
	case pdftext.Null:
		return nil, nil
	case pdftext.Name:
		return []string{filter.Name()}, nil
	case pdftext.Array:
		names := make([]string, 0, filter.Len())
		for index := 0; index < filter.Len(); index++ {
			names = append(names, filter.Index(index).Name())
		}
		return names, nil
	default:
		return nil, fmt.Errorf("invalid PDF image filter")
	}
}

func decodePDFImageFilters(document, data []byte, item pdftext.Value, terminal string) ([]byte, error) {
	names, err := pdfFilterNames(item)
	if err != nil {
		return nil, err
	}
	dictionary, err := pdfImageDictionary(document, item)
	if err != nil {
		return nil, err
	}
	for index := 0; index < len(names); index++ {
		if terminal != "" && canonicalPDFFilter(names[index]) == terminal {
			return data, nil
		}
		data, err = applyPDFFilter(data, names[index], pdfDecodeParametersAt(dictionary, index, len(names)))
		if err != nil {
			return nil, err
		}
	}
	if terminal != "" {
		return nil, fmt.Errorf("PDF image has no terminal %s filter", terminal)
	}
	return data, nil
}

func canonicalPDFFilter(name string) string {
	switch name {
	case "DCT":
		return "DCTDecode"
	case "CCF", "CCITT":
		return "CCITTFaxDecode"
	case "JBIG2", "JBIG2Decode":
		return "JBIG2Decode"
	case "JP2", "JPX", "JPXDecode":
		return "JPXDecode"
	default:
		return name
	}
}

func pdfTerminalImageFilter(filters []string) (string, error) {
	terminal := ""
	for _, filter := range filters {
		canonical := canonicalPDFFilter(filter)
		switch canonical {
		case "DCTDecode", "CCITTFaxDecode", "JBIG2Decode", "JPXDecode":
			if terminal != "" {
				return "", fmt.Errorf("PDF image has multiple terminal filters")
			}
			terminal = canonical
		}
	}
	return terminal, nil
}

type pdfDecodeParameters struct {
	predictor int64
	columns   int64
	colors    int64
	bits      int64
	early     int64
}

func pdfDecodeParametersAt(dictionary []byte, index, count int) pdfDecodeParameters {
	parameters := pdfDecodeParameters{predictor: 1, columns: 1, colors: 1, bits: 8, early: 1}
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
	parameters.predictor = pdfDictionaryNumber(raw, "Predictor", parameters.predictor)
	parameters.columns = pdfDictionaryNumber(raw, "Columns", parameters.columns)
	parameters.colors = pdfDictionaryNumber(raw, "Colors", parameters.colors)
	parameters.bits = pdfDictionaryNumber(raw, "BitsPerComponent", parameters.bits)
	parameters.early = pdfDictionaryNumber(raw, "EarlyChange", parameters.early)
	return parameters
}

func applyPDFFilter(data []byte, name string, parameters pdfDecodeParameters) ([]byte, error) {
	switch name {
	case "FlateDecode", "Fl":
		return decodeFlate(data, parameters)
	case "ASCIIHexDecode", "AHx":
		return decodeASCIIHex(data)
	case "ASCII85Decode", "A85":
		return decodeASCII85(data)
	case "LZWDecode", "LZW":
		return decodeLZW(data, parameters)
	case "RunLengthDecode", "RL":
		return decodeRunLength(data)
	default:
		return nil, fmt.Errorf("unsupported PDF image filter %q", name)
	}
}

func boundedDecoded(value []byte, err error) ([]byte, error) {
	if err != nil {
		return nil, err
	}
	if len(value) > maxPDFImageStreamBytes {
		return nil, fmt.Errorf("decoded image stream exceeds %d bytes", maxPDFImageStreamBytes)
	}
	return value, nil
}

func decodeFlate(data []byte, parameters pdfDecodeParameters) ([]byte, error) {
	reader, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode Flate: %w", err)
	}
	defer reader.Close()
	decoded, err := io.ReadAll(io.LimitReader(reader, maxPDFImageStreamBytes+1))
	if err != nil {
		return nil, fmt.Errorf("decode Flate: %w", err)
	}
	decoded, err = boundedDecoded(decoded, nil)
	if err != nil {
		return nil, err
	}
	return decodePredictor(decoded, parameters)
}

func decodePredictor(data []byte, parameters pdfDecodeParameters) ([]byte, error) {
	switch parameters.predictor {
	case 0, 1:
		return data, nil
	case 2:
		return decodeTIFFPredictor(data, parameters)
	case 10, 11, 12, 13, 14, 15:
		return decodePNGPredictor(data, parameters)
	default:
		return nil, fmt.Errorf("unsupported image predictor %d", parameters.predictor)
	}
}

func predictorParameters(parameters pdfDecodeParameters) (columns, colors, bits int64) {
	return max64(1, parameters.columns), max64(1, parameters.colors), max64(1, parameters.bits)
}

func decodeTIFFPredictor(data []byte, parameters pdfDecodeParameters) ([]byte, error) {
	columns, colors, bits := predictorParameters(parameters)
	if bits != 8 {
		return nil, fmt.Errorf("TIFF predictor supports 8-bit samples only")
	}
	if colors > 32 {
		return nil, fmt.Errorf("unsupported predictor color count %d", colors)
	}
	bytesPerSample := int(bits / 8)
	rowBytes := int(columns) * int(colors) * bytesPerSample
	if rowBytes == 0 || len(data)%rowBytes != 0 {
		return nil, fmt.Errorf("TIFF predictor row is truncated")
	}
	output := append([]byte(nil), data...)
	samplesPerRow := int(columns * colors)
	for row := 0; row < len(output)/rowBytes; row++ {
		rowOffset := row * rowBytes
		for sample := int(colors); sample < samplesPerRow; sample++ {
			offset := rowOffset + sample*bytesPerSample
			previous := rowOffset + (sample-int(colors))*bytesPerSample
			for byteIndex := 0; byteIndex < bytesPerSample; byteIndex++ {
				output[offset+byteIndex] += output[previous+byteIndex]
			}
		}
	}
	return output, nil
}

func decodePNGPredictor(data []byte, parameters pdfDecodeParameters) ([]byte, error) {
	columns, colors, bits := predictorParameters(parameters)
	bytesPerPixel := int((colors*bits + 7) / 8)
	bytesPerComponent := int((bits + 7) / 8)
	rowBytes := int(columns*colors) * bytesPerComponent
	if bytesPerPixel == 0 || rowBytes == 0 {
		return nil, fmt.Errorf("invalid PNG predictor geometry")
	}
	if columns > int64(maxPDFImageStreamBytes) {
		return nil, fmt.Errorf("PNG predictor row is too large")
	}
	output := make([]byte, 0, len(data))
	var previous []byte
	for offset := 0; offset < len(data); {
		filter := data[offset]
		offset++
		if offset+rowBytes > len(data) {
			return nil, fmt.Errorf("PNG predictor row is truncated")
		}
		row := append([]byte(nil), data[offset:offset+rowBytes]...)
		offset += rowBytes
		for index := range row {
			left := int32(0)
			if index >= bytesPerPixel {
				left = int32(row[index-bytesPerPixel])
			}
			up := int32(0)
			if previous != nil {
				up = int32(previous[index])
			}
			upperLeft := int32(0)
			if previous != nil && index >= bytesPerPixel {
				upperLeft = int32(previous[index-bytesPerPixel])
			}
			switch filter {
			case 0:
			case 1:
				row[index] += byte(left)
			case 2:
				row[index] += byte(up)
			case 3:
				row[index] += byte((left + up) / 2)
			case 4:
				row[index] += byte(pngPaeth(left, up, upperLeft))
			default:
				return nil, fmt.Errorf("unknown PNG predictor %d", filter)
			}
		}
		output = append(output, row...)
		previous = row
	}
	return boundedDecoded(output, nil)
}

func pngPaeth(left, up, upperLeft int32) int32 {
	value := left + up - upperLeft
	leftDistance := abs32(value - left)
	upDistance := abs32(value - up)
	cornerDistance := abs32(value - upperLeft)
	switch {
	case leftDistance <= upDistance && leftDistance <= cornerDistance:
		return left
	case upDistance <= cornerDistance:
		return up
	default:
		return upperLeft
	}
}

func abs32(value int32) int32 {
	if value < 0 {
		return -value
	}
	return value
}

func max64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}

func decodeASCIIHex(data []byte) ([]byte, error) {
	output := make([]byte, 0, len(data)/2)
	var current byte
	var count int
	for _, value := range data {
		switch {
		case value == '>':
			if count%2 != 0 {
				return nil, fmt.Errorf("odd ASCIIHex digit count")
			}
			return boundedDecoded(output, nil)
		case value >= '0' && value <= '9':
			value -= '0'
		case value >= 'A' && value <= 'F':
			value -= 'A' - 10
		case value >= 'a' && value <= 'f':
			value -= 'a' - 10
		case value == ' ' || value == '\t' || value == '\n' || value == '\r' || value == '\f' || value == 0:
			continue
		default:
			return nil, fmt.Errorf("invalid ASCIIHex digit")
		}
		if count%2 == 0 {
			current = value << 4
		} else {
			output = append(output, current|value)
		}
		count++
	}
	return nil, fmt.Errorf("ASCIIHex data has no end marker")
}

func decodeASCII85(data []byte) ([]byte, error) {
	output := make([]byte, 0, len(data)*4/5)
	var group []byte
	for index := 0; index < len(data); index++ {
		value := data[index]
		switch {
		case value == '~':
			if index+1 >= len(data) || data[index+1] != '>' {
				return nil, fmt.Errorf("invalid ASCII85 end marker")
			}
			decoded, _, err := flushASCII85Group(output, group)
			return boundedDecoded(decoded, err)
		case value >= '!' && value <= 'u':
			group = append(group, value-'!')
			if len(group) == 5 {
				decoded, _, err := flushASCII85Group(output, group)
				output = decoded
				group = nil
				if err != nil {
					return nil, err
				}
			}
		case value == 'z' && len(group) == 0:
			output = append(output, 0, 0, 0, 0)
		case value == ' ' || value == '\t' || value == '\n' || value == '\r' || value == '\f' || value == 0:
		default:
			return nil, fmt.Errorf("invalid ASCII85 character")
		}
	}
	return nil, fmt.Errorf("ASCII85 data has no end marker")
}

func flushASCII85Group(output, group []byte) ([]byte, bool, error) {
	if len(group) == 0 {
		return output, false, nil
	}
	if len(group) == 1 {
		return nil, false, fmt.Errorf("truncated ASCII85 group")
	}
	for index, value := range group {
		group[index] = value
	}
	var tuple uint32
	for index := 0; index < 5; index++ {
		value := byte(84)
		if index < len(group) {
			value = group[index]
		}
		tuple = tuple*85 + uint32(value)
	}
	plain := [4]byte{
		byte(tuple >> 24),
		byte(tuple >> 16),
		byte(tuple >> 8),
		byte(tuple),
	}
	return append(output, plain[:len(group)-1]...), true, nil
}

type pdfBitReader struct {
	data     []byte
	position int
}

func (reader *pdfBitReader) read(width int) (uint32, bool) {
	if width > 32 || reader.position+width > len(reader.data)*8 {
		return 0, false
	}
	var value uint32
	for index := 0; index < width; index++ {
		byteIndex := reader.position / 8
		bitIndex := 7 - reader.position%8
		value = value<<1 | uint32((reader.data[byteIndex]>>bitIndex)&1)
		reader.position++
	}
	return value, true
}

func decodeLZW(data []byte, parameters pdfDecodeParameters) ([]byte, error) {
	early := parameters.early != 0
	reader := &pdfBitReader{data: data}
	table := newLZWTable()
	width := 9
	nextCode := 258
	var previous []byte
	output := make([]byte, 0, len(data)*2)
	for {
		code, ok := reader.read(width)
		if !ok {
			return nil, fmt.Errorf("truncated LZW stream")
		}
		if code == 257 {
			break
		}
		if code == 256 {
			table = newLZWTable()
			width = 9
			nextCode = 258
			previous = nil
			continue
		}

		var entry []byte
		switch {
		case int(code) < len(table) && table[code] != nil:
			entry = table[code]
		case int(code) == len(table) && previous != nil:
			entry = append(append([]byte(nil), previous...), previous[0])
		default:
			return nil, fmt.Errorf("invalid LZW code %d", code)
		}
		output = append(output, entry...)
		if previous != nil && nextCode < 4096 {
			table = append(table, append(append([]byte(nil), previous...), entry[0]))
			nextCode++
			if nextCode+boolToInt(early) >= 1<<width && width < 12 {
				width++
			}
		}
		previous = append([]byte(nil), entry...)
	}
	return boundedDecoded(output, nil)
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func newLZWTable() [][]byte {
	table := make([][]byte, 0, 258)
	for index := 0; index < 256; index++ {
		table = append(table, []byte{byte(index)})
	}
	table = append(table, nil, nil)
	return table
}

func decodeRunLength(data []byte) ([]byte, error) {
	output := make([]byte, 0, len(data))
	for index := 0; index < len(data); {
		length := int(int8(data[index]))
		index++
		switch {
		case length == -128:
			continue
		case length >= 0:
			if index+length+1 > len(data) {
				return nil, fmt.Errorf("truncated RunLength stream")
			}
			output = append(output, data[index:index+length+1]...)
			index += length + 1
		default:
			if index >= len(data) {
				return nil, fmt.Errorf("truncated RunLength stream")
			}
			value := data[index]
			index++
			for count := 0; count < 1-length; count++ {
				output = append(output, value)
			}
		}
		if len(output) > maxPDFImageStreamBytes {
			return nil, fmt.Errorf("decoded image stream exceeds %d bytes", maxPDFImageStreamBytes)
		}
	}
	return output, nil
}
