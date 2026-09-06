package knowledge

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/shutu-ai/shutu-knowledge/internal/config"
	"github.com/shutu-ai/shutu-knowledge/internal/parser"
)

// TestKnowledgeHelperProcessLegacy is launched as a child process to prove
// that the configured legacy converter is a real optional external runtime.
func TestKnowledgeHelperProcessLegacy(t *testing.T) {
	if os.Getenv("SHUTU_KNOWLEDGE_HELPER_PROCESS") != "1" {
		return
	}
	fmt.Fprintf(os.Stdout, "converted:%s", os.Getenv("SHUTU_KNOWLEDGE_HELPER_FORMAT"))
	os.Exit(0)
}

func TestKnowledgeHelperProcessImageDecoder(t *testing.T) {
	if os.Getenv("SHUTU_KNOWLEDGE_IMAGE_DECODER_PROCESS") != "png" {
		return
	}
	source := image.NewRGBA(image.Rect(0, 0, 1, 1))
	source.SetRGBA(0, 0, color.RGBA{R: 255, A: 255})
	var output bytes.Buffer
	if err := png.Encode(&output, source); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if _, err := os.Stdout.Write(output.Bytes()); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func knowledgeHelperCommand(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return strconv.Quote(filepath.ToSlash(exe)) + " -test.run=^TestKnowledgeHelperProcessLegacy$ {input} {format}"
}

func imageDecoderHelperCommand(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return strconv.Quote(filepath.ToSlash(exe)) + " -test.run=^TestKnowledgeHelperProcessImageDecoder$ {input} {format}"
}

func TestSetGlobalConfigHotReloadsLegacyHelper(t *testing.T) {
	f := newFixture(t)
	if _, err := f.service.parsers.Parse("legacy.doc", []byte("legacy payload")); err == nil ||
		!strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("helper should be absent before configuration: %v", err)
	}

	t.Setenv("SHUTU_KNOWLEDGE_HELPER_PROCESS", "1")
	t.Setenv("SHUTU_KNOWLEDGE_HELPER_FORMAT", "doc")
	cfg := config.Defaults()
	cfg.Helpers.LegacyOffice = knowledgeHelperCommand(t)
	f.service.SetGlobalConfig(cfg)

	result, err := f.service.parsers.Parse("legacy.doc", []byte("legacy payload"))
	if err != nil || result.Text != "converted:doc" {
		t.Fatalf("hot-loaded helper: %+v %v", result, err)
	}

	cfg.Helpers.LegacyOffice = ""
	f.service.SetGlobalConfig(cfg)
	if _, err := f.service.parsers.Parse("legacy.doc", []byte("legacy payload")); err == nil ||
		!strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("removed helper should fail closed: %v", err)
	}
}

func jpxPDF(t *testing.T) []byte {
	t.Helper()
	codecData := []byte{0x00, 0x00, 0x00, 0x0c, 0x6a, 0x50}
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /Resources << /XObject << /Im1 5 0 R >> >> /Contents 4 0 R >>",
		"<< /Length 8 >>\nstream\nq Q\nendstream",
		fmt.Sprintf(
			"<< /Type /XObject /Subtype /Image /Width 1 /Height 1 /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /JPXDecode /Length %d >>\nstream\n%s\nendstream",
			len(codecData), string(codecData),
		),
	}
	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n")
	offsets := make([]int64, len(objects)+1)
	for index, object := range objects {
		offsets[index+1] = int64(out.Len())
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", index+1, object)
	}
	xref := int64(out.Len())
	out.WriteString(fmt.Sprintf("xref\n0 %d\n0000000000 65535 f \n", len(objects)+1))
	for index := 1; index <= len(objects); index++ {
		fmt.Fprintf(&out, "%010d 00000 n \n", offsets[index])
	}
	out.WriteString(fmt.Sprintf("trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref))
	return out.Bytes()
}

func TestSetGlobalConfigHotReloadsImageDecoder(t *testing.T) {
	f := newFixture(t)
	cfg := config.Defaults()
	f.service.SetGlobalConfig(cfg)
	images, err := parser.ExtractPDFImages(jpxPDF(t), f.service.pdfImageOptions(context.Background())...)
	if err != nil || len(images) != 0 {
		t.Fatalf("image decoder should be absent: %+v %v", images, err)
	}

	t.Setenv("SHUTU_KNOWLEDGE_IMAGE_DECODER_PROCESS", "png")
	cfg.Helpers.ImageDecoder = imageDecoderHelperCommand(t)
	f.service.SetGlobalConfig(cfg)
	images, err = parser.ExtractPDFImages(jpxPDF(t), f.service.pdfImageOptions(context.Background())...)
	if err != nil || len(images) != 1 {
		t.Fatalf("hot-loaded image decoder: %+v %v", images, err)
	}
	if _, err := png.Decode(bytes.NewReader(images[0].PNG)); err != nil {
		t.Fatal(err)
	}

	cfg.Helpers.ImageDecoder = ""
	f.service.SetGlobalConfig(cfg)
	images, err = parser.ExtractPDFImages(jpxPDF(t), f.service.pdfImageOptions(context.Background())...)
	if err != nil || len(images) != 0 {
		t.Fatalf("removed image decoder should fail closed: %+v %v", images, err)
	}
}
