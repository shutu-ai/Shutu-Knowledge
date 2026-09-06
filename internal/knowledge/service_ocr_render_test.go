package knowledge

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/shutu-ai/shutu-knowledge/internal/config"
)

// TestKnowledgeHelperProcessOCRRenderer is launched as a child process to
// exercise the real optional full-page renderer command contract.
func TestKnowledgeHelperProcessOCRRenderer(t *testing.T) {
	mode := os.Getenv("SHUTU_KNOWLEDGE_OCR_RENDER_PROCESS")
	if mode == "" {
		return
	}
	if mode == "malformed" {
		fmt.Fprint(os.Stdout, `{"pages":`)
		os.Exit(0)
	}
	source := image.NewRGBA(image.Rect(0, 0, 2, 2))
	source.SetRGBA(0, 0, color.RGBA{R: 255, A: 255})
	var pngData bytes.Buffer
	if err := png.Encode(&pngData, source); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	type rendererPage struct {
		Page int    `json:"page"`
		PNG  string `json:"png"`
	}
	pages := []rendererPage{
		{Page: 1, PNG: base64.StdEncoding.EncodeToString(pngData.Bytes())},
		{Page: 2, PNG: base64.StdEncoding.EncodeToString(pngData.Bytes())},
	}
	if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"pages": pages}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

func ocrRendererHelperCommand(t *testing.T, mode string) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return strconv.Quote(filepath.ToSlash(exe)) + " -test.run=^TestKnowledgeHelperProcessOCRRenderer$ {input} {format}"
}

func configureOCRRenderer(t *testing.T, service *Service, mode string) config.Config {
	t.Helper()
	cfg := config.Defaults()
	cfg.OCR.Mode = "forced"
	if mode != "" {
		t.Setenv("SHUTU_KNOWLEDGE_OCR_RENDER_PROCESS", mode)
		cfg.OCR.RenderHelper = ocrRendererHelperCommand(t, mode)
	}
	service.SetGlobalConfig(cfg)
	return cfg
}

func TestRenderedPDFPagesPreferredOverPDFEnvelope(t *testing.T) {
	service := newFixture(t).service
	configureOCRRenderer(t, service, "png")
	helper := &recordingOCRHelper{}
	service.ocr = helper
	if !service.ocrRendererAvailable() {
		t.Fatal("OCR renderer helper is not available")
	}

	text, _, err := service.parseFileContent(
		context.Background(), &Document{FileName: "scanned.pdf"}, BaseConfig{}, scannedImagePDF(t),
	)
	if err != nil {
		t.Fatal(err)
	}
	if text != "中文\n\n中文" {
		t.Fatalf("rendered page OCR text: %q", text)
	}
	if len(helper.calls) != 2 || helper.calls[0] != "png" || helper.calls[1] != "png" {
		t.Fatalf("rendered path should send PNG pages, got %q", helper.calls)
	}
}

func TestOCRRendererFailureFallsBackToEmbeddedRasters(t *testing.T) {
	service := newFixture(t).service
	configureOCRRenderer(t, service, "malformed")
	helper := &recordingOCRHelper{}
	service.ocr = helper

	text, _, err := service.parseFileContent(
		context.Background(), &Document{FileName: "scanned.pdf"}, BaseConfig{}, scannedImagePDF(t),
	)
	if err != nil {
		t.Fatal(err)
	}
	if text != "中文" {
		t.Fatalf("fallback OCR text: %q", text)
	}
	if len(helper.calls) != 2 || helper.calls[0] != "pdf" || helper.calls[1] != "png" {
		t.Fatalf("renderer failure fallback calls: %q", helper.calls)
	}
}

func TestSetGlobalConfigHotReloadsOCRRenderer(t *testing.T) {
	service := newFixture(t).service
	configureOCRRenderer(t, service, "")
	helper := &recordingOCRHelper{}
	service.ocr = helper

	if _, _, err := service.parseFileContent(
		context.Background(), &Document{FileName: "scanned.pdf"}, BaseConfig{}, scannedImagePDF(t),
	); err != nil {
		t.Fatal(err)
	}
	if len(helper.calls) != 2 || helper.calls[0] != "pdf" || helper.calls[1] != "png" {
		t.Fatalf("initial embedded-raster calls: %q", helper.calls)
	}
	helper.calls = nil
	configureOCRRenderer(t, service, "png")
	service.ocr = helper
	if _, _, err := service.parseFileContent(
		context.Background(), &Document{FileName: "scanned.pdf"}, BaseConfig{}, scannedImagePDF(t),
	); err != nil {
		t.Fatal(err)
	}
	if len(helper.calls) != 2 || helper.calls[0] != "png" || helper.calls[1] != "png" {
		t.Fatalf("hot-loaded renderer calls: %q", helper.calls)
	}

	helper.calls = nil
	configureOCRRenderer(t, service, "")
	service.ocr = helper
	if _, _, err := service.parseFileContent(
		context.Background(), &Document{FileName: "scanned.pdf"}, BaseConfig{}, scannedImagePDF(t),
	); err != nil {
		t.Fatal(err)
	}
	if len(helper.calls) != 2 || helper.calls[0] != "pdf" || helper.calls[1] != "png" {
		t.Fatalf("removed renderer fallback calls: %q", helper.calls)
	}
}
