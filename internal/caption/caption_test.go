package caption

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shutu-ai/shutu-knowledge/internal/parser"
)

func pngFixture(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.SetRGBA(0, 0, color.RGBA{R: 255, A: 255})
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestOpenAICaptionRequestAndResponse(t *testing.T) {
	var authorization, contentType string
	var request map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path: %s", r.URL.Path)
		}
		authorization = r.Header.Get("Authorization")
		contentType = r.Header.Get("Content-Type")
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"Revenue chart"}}]}`))
	}))
	defer server.Close()

	text, err := CaptionImage(context.Background(), pngFixture(t), Config{
		Provider: "openai",
		Model:    "vision-model",
		BaseURL:  server.URL + "/v1",
		APIKey:   "secret-key",
	}, Options{HTTPClient: server.Client(), RequestTimeout: 15 * time.Second})
	if err != nil || text != "Revenue chart" {
		t.Fatalf("caption: %q %v", text, err)
	}
	if authorization != "Bearer secret-key" || contentType != "application/json" {
		t.Fatalf("headers: %q %q", authorization, contentType)
	}
	if request["model"] != "vision-model" {
		t.Fatalf("model: %v", request["model"])
	}
}

func TestOllamaCaptionSendsBase64Image(t *testing.T) {
	var request struct {
		Model    string `json:"model"`
		Stream   bool   `json:"stream"`
		Messages []struct {
			Content string   `json:"content"`
			Images  []string `json:"images"`
		} `json:"messages"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Fatalf("path: %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(`{"message":{"content":"亚洲销售趋势"}}`))
	}))
	defer server.Close()

	text, err := CaptionImage(context.Background(), pngFixture(t), Config{
		Provider: "ollama",
		Model:    "llava",
		BaseURL:  server.URL,
	}, Options{HTTPClient: server.Client(), RequestTimeout: 15 * time.Second})
	if err != nil || text != "亚洲销售趋势" {
		t.Fatalf("caption: %q %v", text, err)
	}
	if request.Model != "llava" || request.Stream {
		t.Fatalf("request: %+v", request)
	}
	want := base64.StdEncoding.EncodeToString(pngFixture(t))
	if len(request.Messages) != 1 || request.Messages[0].Images[0] != want {
		t.Fatalf("image payload: %+v", request.Messages)
	}
}

func TestPDFCaptionFilteringAndBestEffortFailure(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		if requests == 1 {
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"first figure"}}]}`))
			return
		}
		http.Error(w, "model unavailable", http.StatusBadGateway)
	}))
	defer server.Close()

	good := pngFixture(t)
	extractor := func([]byte) ([]parser.PDFImage, error) {
		return []parser.PDFImage{
			{Page: 1, Width: 20, Height: 20, PNG: good},
			{Page: 1, Width: 200, Height: 160, PNG: good},
			{Page: 2, Width: 160, Height: 200, PNG: good},
			{Page: 3, Width: 3000, Height: 3000, PNG: good},
		}, nil
	}
	result := PDFImages(context.Background(), []byte("pdf"), Config{
		Provider: "openai", Model: "vision", BaseURL: server.URL,
	}, Options{HTTPClient: server.Client(), RequestTimeout: 15 * time.Second, ExtractImages: extractor})

	if result.ImageCount != 2 || result.Failures != 1 || requests != 2 {
		t.Fatalf("result: %+v", result)
	}
	if !strings.Contains(result.Text, "[文档图表描述]") ||
		!strings.Contains(result.Text, "第 1 页图表描述") ||
		!strings.Contains(result.Text, "first figure") {
		t.Fatalf("caption text: %q", result.Text)
	}
}

func TestCaptionDisabledDoesNotExtract(t *testing.T) {
	called := false
	result := PDFImages(context.Background(), nil, Config{Provider: "off"}, Options{
		ExtractImages: func([]byte) ([]parser.PDFImage, error) {
			called = true
			return nil, nil
		},
	})
	if called || result.Text != "" || result.ImageCount != 0 {
		t.Fatalf("disabled result: %+v called=%v", result, called)
	}
}
