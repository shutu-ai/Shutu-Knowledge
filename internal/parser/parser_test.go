package parser

import (
	"archive/zip"
	"bytes"
	"compress/zlib"
	"context"
	"encoding/ascii85"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"

	pdftext "github.com/ledongthuc/pdf"
	"golang.org/x/text/encoding/simplifiedchinese"
)

func zipFixture(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	for name, content := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func pdfFromObjects(t *testing.T, objects []string) []byte {
	t.Helper()
	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n")
	offsets := make([]int64, len(objects)+1)
	for index, object := range objects {
		offsets[index+1] = int64(out.Len())
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", index+1, object)
	}
	xrefOffset := int64(out.Len())
	out.WriteString(fmt.Sprintf("xref\n0 %d\n0000000000 65535 f \n", len(objects)+1))
	for index := 1; index <= len(objects); index++ {
		fmt.Fprintf(&out, "%010d 00000 n \n", offsets[index])
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xrefOffset)
	return out.Bytes()
}

func parseOK(t *testing.T, fileName string, data []byte) Result {
	t.Helper()
	result, err := NewRegistry().Parse(fileName, data)
	if err != nil {
		t.Fatalf("parse %s: %v", fileName, err)
	}
	return result
}

func TestParseDocx(t *testing.T) {
	docx := zipFixture(t, map[string]string{
		"word/document.xml": `<?xml version="1.0"?><w:document xmlns:w="w"><w:body><w:p><w:r><w:t>First paragraph</w:t></w:r></w:p><w:p><w:r><w:t>第二段</w:t><w:br/><w:t>same para</w:t></w:r></w:p></w:body></w:document>`,
	})
	result := parseOK(t, "report.docx", docx)
	if !strings.Contains(result.Text, "First paragraph") || !strings.Contains(result.Text, "第二段") || !strings.Contains(result.Text, "same para") {
		t.Fatalf("docx text: %q", result.Text)
	}
}

func TestParsePptx(t *testing.T) {
	pptx := zipFixture(t, map[string]string{
		"ppt/slides/slide1.xml": `<p:sld xmlns:p="p"><p:txBody><a:t xmlns:a="a">Slide one title</a:t></p:txBody></p:sld>`,
		"ppt/slides/slide2.xml": `<p:sld xmlns:p="p"><p:txBody><a:t xmlns:a="a">Second slide</a:t></p:txBody></p:sld>`,
		"ppt/theme/theme1.xml":  `<a:theme xmlns:a="a"><a:t>theme text must be ignored</a:t></a:theme>`,
	})
	result := parseOK(t, "deck.pptx", pptx)
	if strings.Contains(result.Text, "theme text") {
		t.Fatalf("theme leaked into text: %q", result.Text)
	}
	index1 := strings.Index(result.Text, "Slide one title")
	index2 := strings.Index(result.Text, "Second slide")
	if index1 < 0 || index2 < 0 || index1 > index2 {
		t.Fatalf("slide order wrong: %q", result.Text)
	}
}

func TestParseXlsx(t *testing.T) {
	xlsx := zipFixture(t, map[string]string{
		"xl/sharedStrings.xml":     `<sst><si><t>Name</t></si><si><r><t>Rich</t></r><r><t>Text</t></r></si></sst>`,
		"xl/worksheets/sheet1.xml": `<worksheet><row><c t="s"><v>0</v></c><c t="s"><v>1</v></c></row><row><c><v>42</v></c><c t="b"><v>1</v></c></row></worksheet>`,
	})
	result := parseOK(t, "data.xlsx", xlsx)
	lines := strings.Split(result.Text, "\n")
	if len(lines) < 2 {
		t.Fatalf("xlsx text: %q", result.Text)
	}
	if lines[0] != "Name\tRichText" {
		t.Fatalf("shared string row: %q", lines[0])
	}
	if lines[1] != "42\t1" {
		t.Fatalf("value row: %q", lines[1])
	}
}

func TestParseEpub(t *testing.T) {
	epub := zipFixture(t, map[string]string{
		"OEBPS/nav.xhtml":      `<html><body><nav><a>Chapter 1</a></nav></body></html>`,
		"OEBPS/chapter1.xhtml": `<html><head><title>My Book</title></head><body><h1>Chapter 1</h1><p>Hello epub</p></body></html>`,
	})
	result := parseOK(t, "book.epub", epub)
	if result.Title != "My Book" {
		t.Fatalf("epub title: %q", result.Title)
	}
	if !strings.Contains(result.Text, "# Chapter 1") || !strings.Contains(result.Text, "Hello epub") {
		t.Fatalf("epub text: %q", result.Text)
	}
	if strings.Contains(result.Text, "Chapter 1</a>") {
		t.Fatalf("nav leaked: %q", result.Text)
	}
}

func pdfFixture(t *testing.T, content string) []byte {
	t.Helper()
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n")
	offsets := make([]int64, len(objects)+1)
	for index, object := range objects {
		offsets[index+1] = int64(out.Len())
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", index+1, object)
	}
	xrefOffset := int64(out.Len())
	out.WriteString(fmt.Sprintf("xref\n0 %d\n0000000000 65535 f \n", len(objects)+1))
	for index := 1; index <= len(objects); index++ {
		fmt.Fprintf(&out, "%010d 00000 n \n", offsets[index])
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xrefOffset)
	return out.Bytes()
}

func TestPDFAverageLineLength(t *testing.T) {
	if got := AverageLineLength("\nshort\n\nadequate line\n"); got != 9 {
		t.Fatalf("average: %v", got)
	}
	if got := AverageLineLength(" \n \n"); got != 0 {
		t.Fatalf("empty average: %v", got)
	}
}

func TestPDFLayoutReassembly(t *testing.T) {
	content := "BT /F1 12 Tf 72 720 Td (R) Tj ET\n" +
		"BT /F1 12 Tf 80 720 Td (e) Tj ET\n" +
		"BT /F1 12 Tf 88 720 Td (built) Tj ET\n" +
		"BT /F1 12 Tf 110 720 Td ( layout) Tj ET\n"
	result := parseOK(t, "layout.pdf", pdfFixture(t, content))
	if result.NeedsOCR {
		t.Fatalf("healthy reassembly should not request OCR: %+v", result)
	}
	if strings.TrimSpace(result.Text) != "Rebuilt layout" {
		t.Fatalf("layout reassembly: %q", result.Text)
	}
}

func TestPDFUnhealthyLayerRequestsOCRandKeepsText(t *testing.T) {
	content := "BT /F1 12 Tf 72 720 Td (R) Tj ET\n" +
		"BT /F1 12 Tf 72 700 Td (e) Tj ET\n" +
		"BT /F1 12 Tf 72 680 Td (b) Tj ET\n" +
		"BT /F1 12 Tf 72 660 Td (u) Tj ET\n"
	data := pdfFixture(t, content)
	result := parseOK(t, "fragmented.pdf", data)
	if !result.NeedsOCR || strings.Join(strings.Fields(result.Text), "") != "Rebu" {
		t.Fatalf("fragmented native result: %+v", result)
	}
	if AverageLineLength(result.Text) >= 5 {
		t.Fatalf("fixture should have an unhealthy layer: %q", result.Text)
	}
}

func TestExtractPDFImages(t *testing.T) {
	raw := []byte{255, 0, 0, 0, 255, 0, 0, 0, 255, 255, 255, 255}
	var compressed bytes.Buffer
	writer := zlib.NewWriter(&compressed)
	if _, err := writer.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /Resources << /XObject << /Im1 5 0 R >> >> /Contents 4 0 R >>",
		"<< /Length 24 >>\nstream\nq 100 0 0 100 10 10 cm /Im1 Do Q\nendstream",
		fmt.Sprintf("<< /Type /XObject /Subtype /Image /Width 2 /Height 2 /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /FlateDecode /Length %d >>\nstream\n%s\nendstream", compressed.Len(), compressed.String()),
	}
	data := pdfFromObjects(t, objects)
	images, err := ExtractPDFImages(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 1 || images[0].Page != 1 || images[0].Width != 2 || images[0].Height != 2 {
		t.Fatalf("images: %+v", images)
	}
	decoded, err := png.Decode(bytes.NewReader(images[0].PNG))
	if err != nil {
		t.Fatal(err)
	}
	r, g, b, _ := decoded.At(0, 0).RGBA()
	if r>>8 != 255 || g != 0 || b != 0 {
		t.Fatalf("decoded pixel: %d %d %d", r>>8, g, b)
	}
	assertPNGPixel(t, images[0].PNG, 1, 0, color.RGBA{G: 255, A: 255})
	assertPNGPixel(t, images[0].PNG, 0, 1, color.RGBA{B: 255, A: 255})
	assertPNGPixel(t, images[0].PNG, 1, 1, color.RGBA{R: 255, G: 255, B: 255, A: 255})
}

func TestExtractPDFJPEGImages(t *testing.T) {
	source := image.NewRGBA(image.Rect(0, 0, 2, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			source.SetRGBA(x, y, redPixel())
		}
	}
	var jpegData bytes.Buffer
	if err := jpeg.Encode(&jpegData, source, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	raw := jpegData.String()
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /Resources << /XObject << /Im1 5 0 R /Im2 6 0 R >> >> /Contents 4 0 R >>",
		"<< /Length 8 >>\nstream\nq Q\nendstream",
		fmt.Sprintf("<< /Type /XObject /Subtype /Image /Width 2 /Height 2 /Filter /DCTDecode /Length %d >>\nstream\n%s\nendstream", len(raw), raw),
		fmt.Sprintf("<< /Type /XObject /Subtype /Image /Width 2 /Height 2 /Filter [/DCTDecode] /Length %d >>\nstream\n%s\nendstream", len(raw), raw),
	}

	images, err := ExtractPDFImages(pdfFromObjects(t, objects))
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 2 {
		t.Fatalf("image count: %d (%+v)", len(images), images)
	}
	for i, decodedImage := range images {
		if decodedImage.Page != 1 || decodedImage.Width != 2 || decodedImage.Height != 2 {
			t.Fatalf("image %d: %+v", i, decodedImage)
		}
		decoded, err := png.Decode(bytes.NewReader(decodedImage.PNG))
		if err != nil {
			t.Fatal(err)
		}
		r, g, b, _ := decoded.At(0, 0).RGBA()
		if !pixelNear(r>>8, 255) || !pixelNear(g>>8, 0) || !pixelNear(b>>8, 0) {
			t.Fatalf("image %d decoded pixel: %d %d %d", i, r>>8, g>>8, b>>8)
		}
	}
}

func TestExtractPDFSoftMaskImage(t *testing.T) {
	raw := []byte{255, 0, 0, 0, 255, 0, 0, 0, 255, 255, 255, 255}
	mask := []byte{0, 255, 255, 0}
	var compressed bytes.Buffer
	writer := zlib.NewWriter(&compressed)
	if _, err := writer.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name        string
		decode      string
		topLeft     color.RGBA
		topRight    color.RGBA
		bottomLeft  color.RGBA
		bottomRight color.RGBA
	}{
		{
			name:        "default",
			topLeft:     color.RGBA{A: 0},
			topRight:    color.RGBA{G: 255, A: 255},
			bottomLeft:  color.RGBA{B: 255, A: 255},
			bottomRight: color.RGBA{A: 0},
		},
		{
			name:        "inverted",
			decode:      " /Decode [1 0]",
			topLeft:     color.RGBA{R: 255, A: 255},
			topRight:    color.RGBA{G: 255, A: 0},
			bottomLeft:  color.RGBA{R: 255, A: 0},
			bottomRight: color.RGBA{R: 255, G: 255, B: 255, A: 255},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			objects := []string{
				"<< /Type /Catalog /Pages 2 0 R >>",
				"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
				"<< /Type /Page /Parent 2 0 R /Resources << /XObject << /Im1 5 0 R >> >> /Contents 4 0 R >>",
				"<< /Length 8 >>\nstream\nq Q\nendstream",
				fmt.Sprintf(
					"<< /Type /XObject /Subtype /Image /Width 2 /Height 2 /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /FlateDecode /SMask 6 0 R /Length %d >>\nstream\n%s\nendstream",
					compressed.Len(), compressed.String(),
				),
				fmt.Sprintf(
					"<< /Type /XObject /Subtype /Image /Width 2 /Height 2 /ColorSpace /DeviceGray /BitsPerComponent 8%s /Length %d >>\nstream\n%s\nendstream",
					test.decode, len(mask), string(mask),
				),
			}
			images, err := ExtractPDFImages(pdfFromObjects(t, objects))
			if err != nil {
				t.Fatal(err)
			}
			if len(images) != 1 || images[0].Width != 2 || images[0].Height != 2 {
				t.Fatalf("images: %+v", images)
			}
			assertPNGSoftMaskPixel(t, images[0].PNG, 0, 0, test.topLeft)
			assertPNGSoftMaskPixel(t, images[0].PNG, 1, 0, test.topRight)
			assertPNGSoftMaskPixel(t, images[0].PNG, 0, 1, test.bottomLeft)
			assertPNGSoftMaskPixel(t, images[0].PNG, 1, 1, test.bottomRight)
		})
	}
}

func TestExtractPDFCCITTImages(t *testing.T) {
	raw := encodeCCITTGroup3(t, 2, 2)
	for _, test := range []struct {
		name       string
		blackIsOne string
		firstValue uint8
	}{
		{name: "default", firstValue: 255},
		{name: "black-is-one", blackIsOne: " /BlackIs1 true", firstValue: 255},
	} {
		t.Run(test.name, func(t *testing.T) {
			objects := []string{
				"<< /Type /Catalog /Pages 2 0 R >>",
				"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
				"<< /Type /Page /Parent 2 0 R /Resources << /XObject << /Im1 5 0 R >> >> /Contents 4 0 R >>",
				"<< /Length 8 >>\nstream\nq Q\nendstream",
				fmt.Sprintf(
					"<< /Type /XObject /Subtype /Image /Width 2 /Height 2 /ColorSpace /DeviceGray /BitsPerComponent 1%s /Filter /CCITTFaxDecode /DecodeParms << /K 0 /Columns 2 /Rows 2 /EndOfLine true >> /Length %d >>\nstream\n%s\nendstream",
					test.blackIsOne, len(raw), raw,
				),
			}
			images, err := ExtractPDFImages(pdfFromObjects(t, objects))
			if err != nil {
				t.Fatal(err)
			}
			if len(images) != 1 || images[0].Width != 2 || images[0].Height != 2 {
				t.Fatalf("images: %+v", images)
			}
			decoded, err := png.Decode(bytes.NewReader(images[0].PNG))
			if err != nil {
				t.Fatal(err)
			}
			r, _, _, _ := decoded.At(0, 0).RGBA()
			if uint8(r>>8) != test.firstValue {
				t.Fatalf("first pixel: %d", r>>8)
			}
			r, _, _, _ = decoded.At(1, 0).RGBA()
			if uint8(r>>8) != 0 {
				t.Fatalf("second pixel: %d", r>>8)
			}
		})
	}
}

func TestExtractPDFImagesSkipsCorruptCCITT(t *testing.T) {
	raw := encodeCCITTGroup3(t, 2, 2)[:8]
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /Resources << /XObject << /Im1 5 0 R >> >> /Contents 4 0 R >>",
		"<< /Length 8 >>\nstream\nq Q\nendstream",
		fmt.Sprintf(
			"<< /Type /XObject /Subtype /Image /Width 2 /Height 2 /ColorSpace /DeviceGray /BitsPerComponent 1 /Filter /CCITTFaxDecode /DecodeParms << /K -1 /Columns 2 /Rows 2 >> /Length %d >>\nstream\n%s\nendstream",
			len(raw), raw,
		),
	}
	images, err := ExtractPDFImages(pdfFromObjects(t, objects))
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 0 {
		t.Fatalf("corrupt CCITT should be skipped: %+v", images)
	}
}

func TestExtractPDFSeparationImage(t *testing.T) {
	raw := []byte{0, 255}
	function := "<< /FunctionType 2 /Domain [0 1] /C0 [0 0 0] /C1 [1 0 0] /N 1 >>"
	object := fmt.Sprintf(
		"<< /Type /XObject /Subtype /Image /Width 2 /Height 1 /ColorSpace [/Separation /Spot /DeviceRGB 6 0 R] /BitsPerComponent 8 /Length %d >>\nstream\n%s\nendstream",
		len(raw), string(raw),
	)
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /Resources << /XObject << /Im1 5 0 R >> >> /Contents 4 0 R >>",
		"<< /Length 8 >>\nstream\nq Q\nendstream",
		object,
		function,
	}
	images, err := ExtractPDFImages(pdfFromObjects(t, objects))
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 1 {
		data := pdfFromObjects(t, objects)
		reader, readerErr := pdftext.NewReader(bytes.NewReader(data), int64(len(data)))
		if readerErr != nil {
			t.Fatal(readerErr)
		}
		item := reader.Page(1).Resources().Key("XObject").Key("Im1")
		_, decodeErr := decodePDFImage(data, item, PDFImageOptions{})
		t.Fatalf("images: %+v decode: %v item: %s", images, decodeErr, item.String())
	}
	assertPNGPixel(t, images[0].PNG, 0, 0, color.RGBA{A: 255})
	assertPNGPixel(t, images[0].PNG, 1, 0, color.RGBA{R: 255, A: 255})
}

func TestExtractPDFDeviceNImage(t *testing.T) {
	raw := []byte{255, 0, 255, 255}
	functionBody := "{ add 2 div }"
	function := fmt.Sprintf(
		"<< /FunctionType 4 /Domain [0 1 0 1] /Range [0 1] /Length %d >>\nstream\n%s\nendstream",
		len(functionBody), functionBody,
	)
	object := fmt.Sprintf(
		"<< /Type /XObject /Subtype /Image /Width 2 /Height 1 /ColorSpace [/DeviceN [/Cyan /Magenta] /DeviceGray 6 0 R] /BitsPerComponent 8 /Length %d >>\nstream\n%s\nendstream",
		len(raw), string(raw),
	)
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /Resources << /XObject << /Im1 5 0 R >> >> /Contents 4 0 R >>",
		"<< /Length 8 >>\nstream\nq Q\nendstream",
		object,
		function,
	}
	images, err := ExtractPDFImages(pdfFromObjects(t, objects))
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 1 {
		data := pdfFromObjects(t, objects)
		reader, readerErr := pdftext.NewReader(bytes.NewReader(data), int64(len(data)))
		if readerErr != nil {
			t.Fatal(readerErr)
		}
		item := reader.Page(1).Resources().Key("XObject").Key("Im1")
		_, decodeErr := decodePDFImage(data, item, PDFImageOptions{})
		t.Fatalf("images: %+v decode: %v item: %s", images, decodeErr, item.String())
	}
	assertPNGPixel(t, images[0].PNG, 0, 0, color.RGBA{R: 128, G: 128, B: 128, A: 255})
	assertPNGPixel(t, images[0].PNG, 1, 0, color.RGBA{R: 255, G: 255, B: 255, A: 255})
}

func TestExtractPDFImagesSkipsOversizedRaster(t *testing.T) {
	object := "<< /Type /XObject /Subtype /Image /Width 20000 /Height 20000 /ColorSpace /DeviceGray /BitsPerComponent 8 /Length 0 >>\nstream\n\nendstream"
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /Resources << /XObject << /Im1 5 0 R >> >> /Contents 4 0 R >>",
		"<< /Length 8 >>\nstream\nq Q\nendstream",
		object,
	}
	images, err := ExtractPDFImages(pdfFromObjects(t, objects))
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 0 {
		t.Fatalf("oversized raster should be skipped: %+v", images)
	}
}

func TestExtractPDFJPXWithOptionalDecoder(t *testing.T) {
	codecData := []byte{0x00, 0x00, 0x00, 0x0c, 0x6a, 0x50}
	object := fmt.Sprintf(
		"<< /Type /XObject /Subtype /Image /Width 2 /Height 1 /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /JPXDecode /Length %d >>\nstream\n%s\nendstream",
		len(codecData), string(codecData),
	)
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /Resources << /XObject << /Im1 5 0 R >> >> /Contents 4 0 R >>",
		"<< /Length 8 >>\nstream\nq Q\nendstream",
		object,
	}

	var requestedFormat string
	var requestLength int
	decoder := func(_ context.Context, format string, input []byte) ([]byte, error) {
		requestedFormat = format
		requestLength = len(input)
		return pngFixture(t), nil
	}
	images, err := ExtractPDFImages(
		pdfFromObjects(t, objects),
		WithPDFImageContext(context.Background()),
		WithPDFImageDecoder(decoder),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 1 {
		t.Fatalf("images: %+v", images)
	}
	if requestedFormat != "jpx" || requestLength == 0 {
		t.Fatalf("decoder request: format=%q length=%d", requestedFormat, requestLength)
	}
	assertPNGPixel(t, images[0].PNG, 0, 0, color.RGBA{R: 255, A: 255})
}

func TestExtractPDFJBIG2ForwardsGlobalsToOptionalDecoder(t *testing.T) {
	codecData := []byte{0x97, 0x4a, 0x42, 0x32}
	globalsData := []byte{0x01, 0x02}
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /Resources << /XObject << /Im1 5 0 R >> >> /Contents 4 0 R >>",
		"<< /Length 8 >>\nstream\nq Q\nendstream",
		fmt.Sprintf(
			"<< /Type /XObject /Subtype /Image /Width 2 /Height 1 /ColorSpace /DeviceGray /BitsPerComponent 1 /Filter /JBIG2Decode /JBIG2Globals 6 0 R /Length %d >>\nstream\n%s\nendstream",
			len(codecData), string(codecData),
		),
		fmt.Sprintf(
			"<< /Length %d >>\nstream\n%s\nendstream",
			len(globalsData), string(globalsData),
		),
	}

	decoder := func(_ context.Context, format string, input []byte) ([]byte, error) {
		if format != "jbig2" {
			return nil, fmt.Errorf("format %q", format)
		}
		var request struct {
			Data    string `json:"data"`
			Globals string `json:"globals"`
		}
		if err := json.Unmarshal(input, &request); err != nil {
			return nil, err
		}
		if got, err := base64.StdEncoding.DecodeString(request.Data); err != nil || !bytes.Equal(got, codecData) {
			return nil, fmt.Errorf("codec data %q %v", request.Data, err)
		}
		if got, err := base64.StdEncoding.DecodeString(request.Globals); err != nil || !bytes.Equal(got, globalsData) {
			return nil, fmt.Errorf("globals %q %v", request.Globals, err)
		}
		return pngFixture(t), nil
	}
	images, err := ExtractPDFImages(pdfFromObjects(t, objects), WithPDFImageDecoder(decoder))
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 1 {
		t.Fatalf("images: %+v", images)
	}
}

func TestExtractPDFExternalCodecFailsClosedWithoutDecoder(t *testing.T) {
	object := "<< /Type /XObject /Subtype /Image /Width 2 /Height 1 /ColorSpace /DeviceGray /BitsPerComponent 1 /Filter /JPXDecode /Length 1 >>\nstream\nx\nendstream"
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /Resources << /XObject << /Im1 5 0 R >> >> /Contents 4 0 R >>",
		"<< /Length 8 >>\nstream\nq Q\nendstream",
		object,
	}
	images, err := ExtractPDFImages(pdfFromObjects(t, objects))
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 0 {
		t.Fatalf("external codec should be skipped without decoder: %+v", images)
	}
}

func encodeCCITTGroup3(t *testing.T, width, height int) []byte {
	t.Helper()
	if width != 2 || height != 2 {
		t.Fatalf("fixture supports 2x2 only")
	}
	output := &ccittBitWriter{buffer: bytes.NewBuffer(nil)}
	output.writeCode(0b000000000001, 12)
	for row := 0; row < height; row++ {
		output.writeCode(0b000111, 6) // white run of one pixel
		output.writeCode(0b010, 3)    // black run of one pixel
		output.writeCode(0b000000000001, 12)
	}
	for row := 0; row < 5; row++ {
		output.writeCode(0b000000000001, 12)
	}
	output.Flush()
	return output.Bytes()
}

type ccittBitWriter struct {
	buffer  *bytes.Buffer
	current byte
	count   uint
}

func (writer *ccittBitWriter) writeCode(code uint16, bits int) {
	for index := bits - 1; index >= 0; index-- {
		writer.current |= byte((code>>uint(index))&1) << (7 - writer.count)
		writer.count++
		if writer.count == 8 {
			writer.buffer.WriteByte(writer.current)
			writer.current = 0
			writer.count = 0
		}
	}
}

func (writer *ccittBitWriter) Flush() {
	if writer.count == 0 {
		return
	}
	writer.buffer.WriteByte(writer.current)
	writer.current = 0
	writer.count = 0
}

func (writer *ccittBitWriter) Bytes() []byte {
	return writer.buffer.Bytes()
}

func TestExtractPDFImagesSkipsCorruptJPEG(t *testing.T) {
	source := image.NewRGBA(image.Rect(0, 0, 2, 2))
	source.SetRGBA(0, 0, redPixel())
	var jpegData bytes.Buffer
	if err := jpeg.Encode(&jpegData, source, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	raw := jpegData.Bytes()[:8]
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /Resources << /XObject << /Im1 5 0 R >> >> /Contents 4 0 R >>",
		"<< /Length 8 >>\nstream\nq Q\nendstream",
		fmt.Sprintf("<< /Type /XObject /Subtype /Image /Width 2 /Height 2 /Filter /DCTDecode /Length %d >>\nstream\n%s\nendstream", len(raw), raw),
	}

	images, err := ExtractPDFImages(pdfFromObjects(t, objects))
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 0 {
		t.Fatalf("corrupt JPEG should be skipped: %+v", images)
	}
}

func TestPDFImageBaseFilters(t *testing.T) {
	hexDecoded, err := decodeASCIIHex([]byte("48\t65 6C6C6f>"))
	if err != nil || string(hexDecoded) != "Hello" {
		t.Fatalf("ASCIIHex: %q %v", hexDecoded, err)
	}

	runLength, err := decodeRunLength([]byte{2, 'a', 'b', 'c', 0xfe, 'z', 0x80})
	if err != nil || string(runLength) != "abczzz" {
		t.Fatalf("RunLength: %q %v", runLength, err)
	}

	plain := []byte("ascii85 sample data")
	encodedBuffer := make([]byte, ascii85.MaxEncodedLen(len(plain)))
	encoded := encodedBuffer[:ascii85.Encode(encodedBuffer, plain)]
	ascii85Decoded, err := decodeASCII85(append(encoded, '~', '>'))
	if err != nil || !bytes.Equal(ascii85Decoded, plain) {
		t.Fatalf("ASCII85: %q %v", ascii85Decoded, err)
	}

	lzwEncoded := encodePDFLZW([]byte("TOBEORNOTTOBEORTOBEORNOT"))
	lzwDecoded, err := decodeLZW(lzwEncoded, pdfDecodeParameters{})
	if err != nil || string(lzwDecoded) != "TOBEORNOTTOBEORTOBEORNOT" {
		t.Fatalf("LZW: %q %v", lzwDecoded, err)
	}
}

func encodePDFLZW(input []byte) []byte {
	codes := make(map[string]uint32, 4096)
	for index := 0; index < 256; index++ {
		codes[string([]byte{byte(index)})] = uint32(index)
	}
	output := make([]byte, 0, len(input)+8)
	var buffer byte
	var bits int
	write := func(code uint32, width int) {
		for index := width - 1; index >= 0; index-- {
			buffer = buffer<<1 | byte((code>>index)&1)
			bits++
			if bits == 8 {
				output = append(output, buffer)
				buffer = 0
				bits = 0
			}
		}
	}
	write(256, 9)
	width := 9
	next := uint32(258)
	var current string
	for _, value := range input {
		candidate := current + string([]byte{value})
		if _, ok := codes[candidate]; ok {
			current = candidate
			continue
		}
		write(codes[current], width)
		if int(next) < 4096 {
			codes[candidate] = next
			next++
			if int(next)+1 >= 1<<width && width < 12 {
				width++
			}
		}
		current = string([]byte{value})
	}
	write(codes[current], width)
	write(257, width)
	if bits > 0 {
		for bits < 8 {
			buffer <<= 1
			bits++
		}
		output = append(output, buffer)
	}
	return output
}

func TestExtractPDFASCII85FlateImage(t *testing.T) {
	raw := []byte{255, 0, 0, 0, 255, 0, 0, 0, 255, 255, 255, 255}
	var compressed bytes.Buffer
	writer := zlib.NewWriter(&compressed)
	if _, err := writer.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	encodedBuffer := make([]byte, ascii85.MaxEncodedLen(compressed.Len()))
	encoded := encodedBuffer[:ascii85.Encode(encodedBuffer, compressed.Bytes())]
	encoded = append(encoded, '~', '>')
	ascii85Only, err := decodeASCII85(encoded)
	if err != nil || !bytes.Equal(ascii85Only, compressed.Bytes()) {
		t.Fatalf("ASCII85 chain: got %d bytes want %d: %v", len(ascii85Only), compressed.Len(), err)
	}
	object := fmt.Sprintf("<< /Type /XObject /Subtype /Image /Width 2 /Height 2 /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter [/ASCII85Decode /FlateDecode] /Length %d >>\nstream\n%s\nendstream", len(encoded), encoded)
	if _, err := decodeImageObject(t, object); err != nil {
		t.Fatalf("decode: %v", err)
	}
	data := pdfFromObjects(t, pdfImageObjects(object))
	reader, err := pdftext.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	item := reader.Page(1).Resources().Key("XObject").Key("Im1")
	extracted, err := readPDFImageStream(data, item)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(extracted, encoded) {
		t.Fatalf("extracted ASCII85: %q want %q", extracted, encoded)
	}
	decodedA85, err := decodeASCII85(extracted)
	if err != nil || !bytes.Equal(decodedA85, compressed.Bytes()) {
		t.Fatalf("extracted zlib: got %d bytes want %d: %v", len(decodedA85), compressed.Len(), err)
	}
	images, err := ExtractPDFImages(pdfFromObjects(t, pdfImageObjects(object)))
	if err != nil || len(images) != 1 {
		t.Fatalf("images: %+v %v", images, err)
	}
	assertPNGPixel(t, images[0].PNG, 0, 0, color.RGBA{R: 255, A: 255})
}

func decodeImageObject(t *testing.T, object string) (PDFImage, error) {
	t.Helper()
	data := pdfFromObjects(t, pdfImageObjects(object))
	reader, err := pdftext.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	item := reader.Page(1).Resources().Key("XObject").Key("Im1")
	image, err := decodePDFImage(data, item, PDFImageOptions{})
	if err != nil {
		t.Logf("item: %s", item.String())
	}
	return image, err
}

func TestExtractPDFIndexedOneBitImage(t *testing.T) {
	object := "<< /Type /XObject /Subtype /Image /Width 2 /Height 1 /ColorSpace [/Indexed /DeviceRGB 1 <FF000000FF00>] /BitsPerComponent 1 /Length 1 >>\nstream\n\x40\nendstream"
	if _, err := decodeImageObject(t, object); err != nil {
		t.Fatalf("decode: %v", err)
	}
	images, err := ExtractPDFImages(pdfFromObjects(t, pdfImageObjects(object)))
	if err != nil || len(images) != 1 {
		t.Fatalf("images: %+v %v", images, err)
	}
	assertPNGPixel(t, images[0].PNG, 0, 0, color.RGBA{R: 255, A: 255})
	assertPNGPixel(t, images[0].PNG, 1, 0, color.RGBA{G: 255, A: 255})
}

func TestExtractPDFPredictedFlateImage(t *testing.T) {
	raw := []byte{255, 0, 0, 0, 255, 0, 0, 0, 255, 255, 255, 255}
	predictedRows := []byte{}
	for row := 0; row < 2; row++ {
		encoded := []byte{2}
		for index := 0; index < 6; index++ {
			previous := byte(0)
			if row > 0 {
				previous = raw[(row-1)*6+index]
			}
			encoded = append(encoded, raw[row*6+index]-previous)
		}
		predictedRows = append(predictedRows, encoded...)
	}
	var compressed bytes.Buffer
	writer := zlib.NewWriter(&compressed)
	if _, err := writer.Write(predictedRows); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	object := fmt.Sprintf("<< /Type /XObject /Subtype /Image /Width 2 /Height 2 /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /FlateDecode /DecodeParms << /Predictor 12 /Colors 3 /BitsPerComponent 8 /Columns 2 >> /Length %d >>\nstream\n%s\nendstream", compressed.Len(), compressed.String())
	data := pdfFromObjects(t, pdfImageObjects(object))
	dictionary, err := pdfImageDictionary(data, mustPDFImageItem(t, data))
	if err != nil {
		t.Fatal(err)
	}
	parameters := pdfDecodeParametersAt(dictionary, 0, 1)
	if parameters.predictor != 12 || parameters.columns != 2 || parameters.colors != 3 {
		t.Fatalf("predictor parameters: %+v dictionary=%s", parameters, dictionary)
	}
	if _, err := decodeImageObject(t, object); err != nil {
		t.Fatalf("decode: %v", err)
	}
	images, err := ExtractPDFImages(pdfFromObjects(t, pdfImageObjects(object)))
	if err != nil || len(images) != 1 {
		t.Fatalf("images: %+v %v", images, err)
	}
	assertPNGPixel(t, images[0].PNG, 0, 0, color.RGBA{R: 255, A: 255})
	assertPNGPixel(t, images[0].PNG, 0, 1, color.RGBA{B: 255, A: 255})
}

func pdfImageObjects(imageObject string) []string {
	return []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /Resources << /XObject << /Im1 5 0 R >> >> /Contents 4 0 R >>",
		"<< /Length 8 >>\nstream\nq Q\nendstream",
		imageObject,
	}
}

func mustPDFImageItem(t *testing.T, data []byte) pdftext.Value {
	t.Helper()
	reader, err := pdftext.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	return reader.Page(1).Resources().Key("XObject").Key("Im1")
}

func assertPNGPixel(t *testing.T, data []byte, x, y int, want color.RGBA) {
	t.Helper()
	decoded, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	red, green, blue, alpha := decoded.At(x, y).RGBA()
	got := color.RGBA{R: uint8(red >> 8), G: uint8(green >> 8), B: uint8(blue >> 8), A: uint8(alpha >> 8)}
	if got != want {
		t.Fatalf("pixel %d,%d: got %+v want %+v", x, y, got, want)
	}
}

func assertPNGSoftMaskPixel(t *testing.T, data []byte, x, y int, want color.RGBA) {
	t.Helper()
	if want.A != 0 {
		assertPNGPixel(t, data, x, y, want)
		return
	}
	decoded, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, got := decoded.At(x, y).RGBA(); uint8(got>>8) != want.A {
		t.Fatalf("transparent alpha %d,%d: got %d want %d", x, y, got>>8, want.A)
	}
}

func pngFixture(t *testing.T) []byte {
	t.Helper()
	source := image.NewRGBA(image.Rect(0, 0, 2, 1))
	source.SetRGBA(0, 0, color.RGBA{R: 255, A: 255})
	source.SetRGBA(1, 0, color.RGBA{G: 255, A: 255})
	output := &bytes.Buffer{}
	if err := png.Encode(output, source); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func redPixel() color.RGBA {
	return color.RGBA{R: 255, G: 0, B: 0, A: 255}
}

func pixelNear(value, want uint32) bool {
	difference := int(value) - int(want)
	if difference < 0 {
		difference = -difference
	}
	return difference <= 2
}

func TestParseHTMLStripsChrome(t *testing.T) {
	html := `<html><head><title>Doc</title><style>body{}</style></head><body><nav>menu</nav><h1>Title</h1><script>alert(1)</script><p>Body text with  spaces</p><ul><li>one</li><li>two</li></ul></body></html>`
	result := parseOK(t, "page.html", []byte(html))
	if result.Title != "Doc" {
		t.Fatalf("title: %q", result.Title)
	}
	for _, banned := range []string{"alert(1)", "body{}", "menu"} {
		if strings.Contains(result.Text, banned) {
			t.Fatalf("chrome leaked (%s): %q", banned, result.Text)
		}
	}
	for _, want := range []string{"# Title", "Body text with spaces", "- one", "- two"} {
		if !strings.Contains(result.Text, want) {
			t.Fatalf("missing %q in %q", want, result.Text)
		}
	}
}

func TestParseHTMLHandlesUTF8BOM(t *testing.T) {
	source := append([]byte{0xef, 0xbb, 0xbf}, []byte(
		`<html><head><title>Doc</title></head><body><p>Body</p></body></html>`,
	)...)
	result := parseOK(t, "page.html", source)
	if result.Title != "Doc" || strings.Contains(result.Text, "Doc") {
		t.Fatalf("BOM changed document structure: title=%q text=%q", result.Title, result.Text)
	}
	if !strings.Contains(result.Text, "Body") {
		t.Fatalf("body missing: %q", result.Text)
	}
}

func TestDecodeTextGB18030Fallback(t *testing.T) {
	encoded, err := simplifiedchinese.GB18030.NewEncoder().Bytes([]byte("中文测试文本，用于编码回退验证。"))
	if err != nil {
		t.Fatal(err)
	}
	got := DecodeText(encoded)
	if !strings.Contains(got, "中文测试文本") {
		t.Fatalf("gb18030 decode failed: %q", got)
	}
}

func TestRegistryDispatchAndRejections(t *testing.T) {
	registry := NewRegistry()
	got := registry.SupportedExtensions()
	if len(got) < 12 || !contains(got, "pdf") || !contains(got, "epub") {
		t.Fatalf("supported: %v", got)
	}
	if _, err := registry.Parse("archive.rar", []byte("x")); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("expected unsupported error, got %v", err)
	}
	// Legacy OLE formats are intentionally unsupported until the optional
	// external helper phase.
	if _, err := registry.Parse("legacy.doc", []byte("x")); err == nil {
		t.Fatal("legacy .doc should be unsupported in phase 2")
	}
	// Empty text fails with a parse error, not success.
	if _, err := registry.Parse("empty.txt", []byte("   ")); err == nil {
		t.Fatal("empty text should error")
	}
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}
