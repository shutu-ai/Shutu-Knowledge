package parser

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	pdftext "github.com/ledongthuc/pdf"
)

func pdfImageDictionary(document []byte, item pdftext.Value) ([]byte, error) {
	offset, err := pdfStreamOffset(item)
	if err != nil {
		return nil, err
	}
	searchStart := max(0, int(offset)-1024*1024)
	window := document[searchStart:int(offset)]
	marker := bytes.LastIndex(window, []byte(" 0 obj"))
	if marker < 0 {
		return nil, fmt.Errorf("PDF image object header was not found")
	}
	headerStart := bytes.Index(window[marker:], []byte("<<"))
	if headerStart < 0 {
		return nil, fmt.Errorf("PDF image object has no dictionary")
	}
	headerStart += marker
	headerEnd, err := pdfClosingDelimiter(window[headerStart:], []byte("<<"), []byte(">>"))
	if err != nil {
		return nil, err
	}
	return window[headerStart : headerStart+headerEnd+2], nil
}

func pdfClosingDelimiter(data, open, close []byte) (int, error) {
	depth := 0
	for index := 0; index < len(data); index++ {
		switch {
		case data[index] == '(':
			index += pdfLiteralStringLength(data[index:]) - 1
		case bytes.HasPrefix(data[index:], open):
			depth++
			index += len(open) - 1
		case bytes.HasPrefix(data[index:], close):
			depth--
			if depth == 0 {
				return index, nil
			}
			index += len(close) - 1
		}
	}
	return 0, fmt.Errorf("PDF dictionary is not closed")
}

func pdfLiteralStringLength(data []byte) int {
	depth := 1
	for index := 1; index < len(data); index++ {
		switch data[index] {
		case '\\':
			index++
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return index + 1
			}
		}
	}
	return len(data)
}

func pdfDictionaryValue(dictionary []byte, key string) ([]byte, bool) {
	search := append([]byte("/"), []byte(key)...)
	for index := 0; index+len(search) <= len(dictionary); {
		found := bytes.Index(dictionary[index:], search)
		if found < 0 {
			return nil, false
		}
		found += index
		before := byte(0)
		if found > 0 {
			before = dictionary[found-1]
		}
		after := byte(0)
		if found+len(search) < len(dictionary) {
			after = dictionary[found+len(search)]
		}
		if pdfIsDelimiter(before) && pdfIsDelimiter(after) {
			value, ok := pdfNextValue(dictionary[found+len(search):])
			if ok {
				return value, true
			}
		}
		index = found + 1
	}
	return nil, false
}

func pdfIsDelimiter(value byte) bool {
	return value == 0 || value == ' ' || value == '\t' || value == '\n' || value == '\r' || value == '\f' ||
		value == '<' || value == '>' || value == '(' || value == ')' || value == '[' || value == ']' ||
		value == '{' || value == '}' || value == '/' || value == '%'
}

func pdfNextValue(data []byte) ([]byte, bool) {
	data = pdfTrimSpace(data)
	if len(data) == 0 {
		return nil, false
	}
	switch {
	case data[0] == '<' && len(data) > 1 && data[1] == '<':
		end, err := pdfClosingDelimiter(data, []byte("<<"), []byte(">>"))
		if err != nil {
			return nil, false
		}
		return data[:end+2], true
	case data[0] == '[':
		end, err := pdfClosingDelimiter(data, []byte("["), []byte("]"))
		if err != nil {
			return nil, false
		}
		return data[:end+1], true
	case data[0] == '(':
		return data[:pdfLiteralStringLength(data)], true
	case data[0] == '<':
		end := bytes.IndexByte(data, '>')
		if end < 0 {
			return nil, false
		}
		return data[:end+1], true
	case data[0] == '/':
		length := 1
		for length < len(data) && !pdfIsDelimiter(data[length]) {
			length++
		}
		return data[:length], true
	default:
		length := 0
		for length < len(data) && !pdfIsDelimiter(data[length]) {
			length++
		}
		if length == 0 {
			return nil, false
		}
		return data[:length], true
	}
}

func pdfTrimSpace(data []byte) []byte {
	return bytes.TrimLeft(data, "\x00\t\n\f\r ")
}

func pdfArrayValues(raw []byte) [][]byte {
	if len(raw) < 2 || raw[0] != '[' || raw[len(raw)-1] != ']' {
		return nil
	}
	values := make([][]byte, 0, 4)
	data := pdfTrimSpace(raw[1 : len(raw)-1])
	for len(data) > 0 {
		value, ok := pdfNextValue(data)
		if !ok {
			return nil
		}
		values = append(values, value)
		data = pdfTrimSpace(data[len(value):])
	}
	return values
}

func pdfDictionaryNumber(dictionary []byte, key string, fallback int64) int64 {
	raw, ok := pdfDictionaryValue(dictionary, key)
	if !ok {
		return fallback
	}
	value, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
	if err != nil {
		return fallback
	}
	return value
}

func pdfRawNumber(raw []byte, fallback int64) int64 {
	value, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
	if err != nil {
		return fallback
	}
	return value
}

func pdfDecodePDFString(raw []byte) ([]byte, error) {
	if len(raw) < 2 {
		return nil, fmt.Errorf("short PDF string")
	}
	if raw[0] == '<' && raw[len(raw)-1] == '>' {
		hex := bytes.ReplaceAll(bytes.ReplaceAll(raw[1:len(raw)-1], []byte("\n"), nil), []byte("\r"), nil)
		if len(hex)%2 != 0 {
			hex = append(hex, '0')
		}
		output := make([]byte, len(hex)/2)
		for index := 0; index < len(output); index++ {
			_, err := fmt.Sscanf(string(hex[index*2:index*2+2]), "%02x", &output[index])
			if err != nil {
				return nil, fmt.Errorf("decode PDF hex string: %w", err)
			}
		}
		return output, nil
	}
	if raw[0] != '(' || raw[len(raw)-1] != ')' {
		return nil, fmt.Errorf("invalid PDF string")
	}
	output := make([]byte, 0, len(raw))
	for index := 1; index < len(raw)-1; index++ {
		value := raw[index]
		if value != '\\' {
			output = append(output, value)
			continue
		}
		index++
		if index >= len(raw)-1 {
			return nil, fmt.Errorf("truncated PDF string escape")
		}
		switch raw[index] {
		case 'n':
			output = append(output, '\n')
		case 'r':
			output = append(output, '\r')
		case 't':
			output = append(output, '\t')
		case 'b':
			output = append(output, '\b')
		case 'f':
			output = append(output, '\f')
		default:
			output = append(output, raw[index])
		}
	}
	return output, nil
}
