package parser

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/shutu-ai/shutu-knowledge/internal/runtime"
)

type fakeOCRRuntime struct {
	format    string
	data      string
	modelPath string
}

func (f *fakeOCRRuntime) Configured(capability string) bool {
	return capability == runtime.CapabilityOCR
}

func (f *fakeOCRRuntime) Call(_ context.Context, capability string, rawParams any, out any) error {
	if capability != runtime.CapabilityOCR {
		panic("unsupported capability " + capability)
	}
	params := rawParams.(map[string]any)
	f.format = params["format"].(string)
	f.data = params["data"].(string)
	f.modelPath, _ = params["modelPath"].(string)
	target := out.(*struct {
		Text       string  `json:"text"`
		Confidence float64 `json:"confidence"`
	})
	target.Text = "scanned"
	target.Confidence = 95
	return nil
}

func TestRuntimeOCRHelperSendsBytesAndReturnsText(t *testing.T) {
	helper := &fakeOCRRuntime{}
	runner := NewRuntimeHelper(helper)
	if runner == nil || !runner.Available() {
		t.Fatal("runtime helper was unavailable")
	}
	text, err := runner.Run(context.Background(), "pdf", []byte("pdf"))
	if err != nil || text != "scanned" {
		t.Fatalf("OCR runtime: %q %v", text, err)
	}
	if helper.format != "pdf" || helper.data != "cGRm" {
		t.Fatalf("request: %+v", helper)
	}
	if NewRuntimeHelper(nil) != nil {
		t.Fatal("nil runtime must produce no helper")
	}
}

func TestRuntimeOCRHelperGatesOnArtifactsAndPassesModelPath(t *testing.T) {
	helper := &fakeOCRRuntime{}
	path := ""
	ready := false
	runner := NewRuntimeHelperWithArtifacts(helper, func() (string, bool) {
		return path, ready
	})
	if runner == nil || runner.Available() {
		t.Fatal("incomplete OCR artifacts must make runtime helper unavailable")
	}

	path = filepath.Join("models", "ocr")
	ready = true
	if !runner.Available() {
		t.Fatal("complete OCR artifacts should enable runtime helper")
	}
	if _, err := runner.Run(context.Background(), "pdf", []byte("pdf")); err != nil {
		t.Fatal(err)
	}
	if helper.modelPath != path {
		t.Fatalf("model path: %q", helper.modelPath)
	}
}
