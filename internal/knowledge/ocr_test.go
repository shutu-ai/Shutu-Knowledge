package knowledge

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func TestPrepareOCRImageUpscalesAndGrayscales(t *testing.T) {
	source := image.NewRGBA(image.Rect(0, 0, 2, 2))
	source.SetRGBA(0, 0, color.RGBA{R: 255, A: 255})
	source.SetRGBA(1, 1, color.RGBA{B: 255, A: 255})
	encoded := &bytes.Buffer{}
	if err := png.Encode(encoded, source); err != nil {
		t.Fatal(err)
	}

	preparedData, err := prepareOCRImage(encoded.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := png.Decode(bytes.NewReader(preparedData))
	if err != nil {
		t.Fatal(err)
	}
	if got := prepared.Bounds(); got.Dx() != 4 || got.Dy() != 4 {
		t.Fatalf("prepared dimensions: %dx%d", got.Dx(), got.Dy())
	}
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			red, green, blue, alpha := prepared.At(x, y).RGBA()
			if alpha>>8 != 255 {
				t.Fatalf("alpha %d,%d: %d", x, y, alpha>>8)
			}
			if red>>8 != green>>8 || green>>8 != blue>>8 {
				t.Fatalf("not grayscale %d,%d: %d %d %d", x, y, red>>8, green>>8, blue>>8)
			}
		}
	}
}

func TestPostprocessOCRTextFoldsCJKSpacesOnly(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: "中 文\t测 试", want: "中文测试"},
		{input: "中\n文", want: "中\n文"},
		{input: "English text", want: "English text"},
		{input: "中, 文", want: "中, 文"},
	}
	for _, test := range tests {
		if got := postprocessOCRText(test.input); got != test.want {
			t.Fatalf("postprocess %q: got %q want %q", test.input, got, test.want)
		}
	}
}
