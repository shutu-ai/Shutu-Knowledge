package parser

import (
	"bytes"
	"image"
	"image/png"
	"testing"
)

func TestImageParserMarksRasterForOCR(t *testing.T) {
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 8, 6))); err != nil {
		t.Fatal(err)
	}
	result, err := NewRegistry().Parse("scan.png", encoded.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if !result.NeedsOCR || result.Text != "" {
		t.Fatalf("image result: %+v", result)
	}
	if _, err := NewRegistry().Parse("broken.jpg", []byte("not an image")); err == nil {
		t.Fatal("broken image unexpectedly parsed")
	}
}
