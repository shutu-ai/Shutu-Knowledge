package parser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	"github.com/shutu-ai/shutu-knowledge/internal/runtime"
)

type adapterCaller struct {
	capabilities map[string]bool
}

func (c adapterCaller) Configured(capability string) bool { return c.capabilities[capability] }

func (c adapterCaller) Call(_ context.Context, capability string, _ any, out any) error {
	var value any
	switch capability {
	case runtime.CapabilityPDFRender:
		value = map[string]any{"pages": []any{map[string]any{"page": 1, "png": testPNGBase64()}}}
	case runtime.CapabilityOffice:
		value = map[string]string{"text": "managed office text"}
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

func testPNGBase64() string {
	imageData := image.NewRGBA(image.Rect(0, 0, 2, 2))
	imageData.Set(0, 0, color.Black)
	imageData.Set(1, 1, color.White)
	var encoded strings.Builder
	if err := png.Encode(&stringWriter{builder: &encoded}, imageData); err != nil {
		panic(err)
	}
	return base64.StdEncoding.EncodeToString([]byte(encoded.String()))
}

type stringWriter struct{ builder *strings.Builder }

func (w *stringWriter) Write(data []byte) (int, error) { return w.builder.Write(data) }

func TestRuntimeRendererUsesValidatedPageEnvelope(t *testing.T) {
	helper := NewRuntimeRenderer(adapterCaller{capabilities: map[string]bool{runtime.CapabilityPDFRender: true}})
	if helper == nil || !helper.Available() {
		t.Fatal("managed renderer is unavailable")
	}
	pages, err := ParseRenderedPDFPages(mustDecodeRuntimeRenderer(t, helper))
	if err != nil || len(pages) != 1 || pages[0].Width != 2 || pages[0].Height != 2 {
		t.Fatalf("managed renderer pages: %+v %v", pages, err)
	}
}

func mustDecodeRuntimeRenderer(t *testing.T, helper *RuntimeRenderer) []byte {
	t.Helper()
	data, err := helper.DecodeLimit(context.Background(), "pdf", []byte("pdf"), MaxOCRRenderOutputBytes)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestRuntimeOfficeHelperUsesManagedPackageContract(t *testing.T) {
	helper := NewRuntimeOfficeHelper(adapterCaller{capabilities: map[string]bool{runtime.CapabilityOffice: true}})
	text, err := helper.Run(context.Background(), "doc", []byte("legacy"))
	if err != nil || text != "managed office text" {
		t.Fatalf("managed office result: %q %v", text, err)
	}
}
