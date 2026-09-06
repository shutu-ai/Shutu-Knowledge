package models

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func ocrTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	onnx := bytes.Repeat([]byte("ocr-weight"), 100_000)
	var dictionary strings.Builder
	dictionary.WriteString("other:\n  - ignored\ncharacter_dict:\n")
	for i := 0; i < 1200; i++ {
		dictionary.WriteString("  - '中'\n")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/PP-OCRv5_mobile_det_onnx/resolve/main/inference.onnx"),
			strings.HasSuffix(r.URL.Path, "/PP-OCRv5_mobile_rec_onnx/resolve/main/inference.onnx"):
			_, _ = w.Write(onnx)
		case strings.HasSuffix(r.URL.Path, "/PP-OCRv5_mobile_rec_onnx/resolve/main/inference.yml"):
			_, _ = w.Write([]byte(dictionary.String()))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func TestOCRBundleLifecycle(t *testing.T) {
	server := ocrTestServer(t)
	manager := NewManager(filepath.Join(t.TempDir(), "models"), server.URL, server.Client())

	status, err := manager.OCRStatus()
	if err != nil || status.Status != "not-downloaded" || len(status.Missing) != 3 {
		t.Fatalf("initial OCR status: %+v %v", status, err)
	}

	progress := make([]int, 0, 4)
	if err := manager.DownloadOCR(context.Background(), func(value int) { progress = append(progress, value) }); err != nil {
		t.Fatal(err)
	}
	if !equalProgress(progress, []int{0, 33, 66, 100}) {
		t.Fatalf("progress: %v", progress)
	}

	status, err = manager.OCRStatus()
	if err != nil || status.Status != "installed" || status.Lifecycle != LifecycleInstalled || status.Ready || status.Runtime != LifecycleRuntimeMiss || status.Kind != KindOCR || len(status.Missing) != 0 {
		t.Fatalf("downloaded OCR status: %+v %v", status, err)
	}
	dictionary, err := os.ReadFile(filepath.Join(manager.Root(), "PaddlePaddle", "PP-OCRv5-mobile", "ppocrv5_dict.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if dictionary[0] != '\n' || strings.Count(string(dictionary), "\n中") != 1200 {
		t.Fatalf("converted dictionary: first=%d entries=%d", dictionary[0], strings.Count(string(dictionary), "\n中"))
	}

	if err := manager.Remove(OCRModelID); err != nil {
		t.Fatal(err)
	}
	status, err = manager.OCRStatus()
	if err != nil || status.Status != "not-downloaded" {
		t.Fatalf("removed OCR status: %+v %v", status, err)
	}
}

func TestOCRDictionaryValidationRejectsMirrorPage(t *testing.T) {
	if _, err := parseOCRCharacterDict([]byte("character_dict:\n  - a\n  - b\n")); err == nil {
		t.Fatal("short non-CJK dictionary should fail")
	}
}

func equalProgress(actual, want []int) bool {
	if len(actual) != len(want) {
		return false
	}
	for index := range actual {
		if actual[index] != want[index] {
			return false
		}
	}
	return true
}
