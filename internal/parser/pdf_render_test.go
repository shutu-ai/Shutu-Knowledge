package parser

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/png"
	"strconv"
	"strings"
	"testing"
)

func tinyPNG(t *testing.T) []byte {
	t.Helper()
	var output bytes.Buffer
	if err := png.Encode(&output, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func pngHeaderOnly(width, height int) []byte {
	var output bytes.Buffer
	output.Write([]byte{137, 'P', 'N', 'G', '\r', '\n', 26, '\n'})
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:4], uint32(width))
	binary.BigEndian.PutUint32(ihdr[4:8], uint32(height))
	ihdr[8] = 8
	ihdr[9] = 6
	writeChunk := func(kind string, data []byte) {
		chunk := make([]byte, 4, 12+len(data))
		binary.BigEndian.PutUint32(chunk, uint32(len(data)))
		chunk = append(chunk, kind...)
		chunk = append(chunk, data...)
		binary.BigEndian.PutUint32(chunk[len(chunk)-4:], crc32.ChecksumIEEE(chunk[4:len(chunk)-4]))
		output.Write(chunk)
	}
	writeChunk("IHDR", ihdr)
	var idat bytes.Buffer
	writer := zlib.NewWriter(&idat)
	_ = writer.Close()
	writeChunk("IDAT", idat.Bytes())
	writeChunk("IEND", nil)
	return output.Bytes()
}

func encodedRenderedPage(t *testing.T, page int, pngData []byte) string {
	t.Helper()
	return `{"page":` + strconv.Itoa(page) + `,"png":"` + base64.StdEncoding.EncodeToString(pngData) + `"}`
}

func TestParseRenderedPDFPages(t *testing.T) {
	pngData := tinyPNG(t)
	output := []byte(`{"pages":[` + encodedRenderedPage(t, 1, pngData) + `,` + encodedRenderedPage(t, 3, pngData) + `]}`)
	pages, err := ParseRenderedPDFPages(output)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 2 || pages[0].Page != 1 || pages[1].Page != 3 {
		t.Fatalf("unexpected pages: %+v", pages)
	}
}

func TestParseRenderedPDFPagesRejectsInvalidResponses(t *testing.T) {
	pngData := tinyPNG(t)
	invalidPNG := []byte("not png")
	tests := []struct {
		name   string
		output string
	}{
		{name: "empty page list", output: `{"pages":[]}`},
		{name: "malformed JSON", output: `{"pages":[`},
		{name: "unknown field", output: `{"pages":[` + encodedRenderedPage(t, 1, pngData) + `],"helper":"renderer"}`},
		{name: "duplicate page", output: `{"pages":[` + encodedRenderedPage(t, 1, pngData) + `,` + encodedRenderedPage(t, 1, pngData) + `]}`},
		{name: "out of order page", output: `{"pages":[` + encodedRenderedPage(t, 2, pngData) + `,` + encodedRenderedPage(t, 1, pngData) + `]}`},
		{name: "page beyond limit", output: `{"pages":[` + encodedRenderedPage(t, MaxOCRRenderPages+1, pngData) + `]}`},
		{name: "invalid PNG", output: `{"pages":[` + encodedRenderedPage(t, 1, invalidPNG) + `]}`},
		{name: "page dimension too large", output: `{"pages":[` + encodedRenderedPage(t, 1, pngHeaderOnly(20001, 1)) + `]}`},
		{name: "total pixels too large", output: `{"pages":[` + encodedRenderedPage(t, 1, pngHeaderOnly(15000, 15000)) + `]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ParseRenderedPDFPages([]byte(test.output)); err == nil {
				t.Fatal("expected renderer response to be rejected")
			}
		})
	}
}

func TestParseRenderedPDFPagesRejectsTooManyPages(t *testing.T) {
	pages := make([]string, 0, MaxOCRRenderPages+1)
	for page := 1; page <= MaxOCRRenderPages+1; page++ {
		pages = append(pages, encodedRenderedPage(t, page, tinyPNG(t)))
	}
	output := `{"pages":[` + strings.Join(pages, ",") + `]}`
	if _, err := ParseRenderedPDFPages([]byte(output)); err == nil {
		t.Fatal("expected page limit to be enforced")
	}
}
