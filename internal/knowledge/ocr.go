package knowledge

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
)

var ocrSharpenKernel = [9]float64{
	0, -0.4, 0,
	-0.4, 2.6, -0.4,
	0, -0.4, 0,
}

// prepareOCRImage mirrors the upstream fallback preprocessing: low-resolution
// rasters are doubled, then converted to grayscale, contrast-stretched, and
// mildly sharpened before the deployment-supplied recognizer sees them.
func prepareOCRImage(pngData []byte) ([]byte, error) {
	source, err := png.Decode(bytes.NewReader(pngData))
	if err != nil {
		return nil, fmt.Errorf("decode OCR raster: %w", err)
	}
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	scaled := false
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("OCR raster has no pixels")
	}
	if width < 1200 && height < 800 {
		width, height = width*2, height*2
		scaled = true
	}

	sourceRGBA, ok := source.(*image.RGBA)
	if !ok {
		sourceRGBA = image.NewRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
		for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
			for x := bounds.Min.X; x < bounds.Max.X; x++ {
				sourceRGBA.Set(x-bounds.Min.X, y-bounds.Min.Y, source.At(x, y))
			}
		}
	}
	gray := make([]uint8, width*height)
	for y := 0; y < height; y++ {
		sourceY := y
		if scaled {
			sourceY = y / 2
		}
		for x := 0; x < width; x++ {
			sourceX := x
			if scaled {
				sourceX = x / 2
			}
			colorValue := sourceRGBA.RGBAAt(sourceX, sourceY)
			alpha := float64(colorValue.A) / 255
			if alpha == 0 {
				gray[y*width+x] = 255
				continue
			}
			// RGBA is alpha-premultiplied; undo that before compositing on white.
			red := float64(colorValue.R) / alpha
			green := float64(colorValue.G) / alpha
			blue := float64(colorValue.B) / alpha
			luma := 0.299*red + 0.587*green + 0.114*blue
			gray[y*width+x] = uint8(math.Round(luma*alpha + 255*(1-alpha)))
		}
	}
	minimum, maximum := 255, 0
	for _, value := range gray {
		minimum = min(minimum, int(value))
		maximum = max(maximum, int(value))
	}
	stretched := make([]uint8, len(gray))
	for index, value := range gray {
		if maximum == minimum {
			stretched[index] = value
			continue
		}
		stretched[index] = uint8(math.Round(float64(int(value)-minimum) * 255 / float64(maximum-minimum)))
	}

	sharpened := make([]uint8, len(stretched))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			var sum float64
			for kernelY := -1; kernelY <= 1; kernelY++ {
				for kernelX := -1; kernelX <= 1; kernelX++ {
					sampleX := min(width-1, max(0, x+kernelX))
					sampleY := min(height-1, max(0, y+kernelY))
					sum += float64(stretched[sampleY*width+sampleX]) * ocrSharpenKernel[(kernelY+1)*3+kernelX+1]
				}
			}
			sharpened[y*width+x] = uint8(min(255, max(0, int(sum+0.5))))
		}
	}

	output := image.NewRGBA(image.Rect(0, 0, width, height))
	for index, value := range sharpened {
		output.SetRGBA(index%width, index/width, color.RGBA{R: value, G: value, B: value, A: 255})
	}
	encoded := &bytes.Buffer{}
	if err := png.Encode(encoded, output); err != nil {
		return nil, fmt.Errorf("encode prepared OCR raster: %w", err)
	}
	return encoded.Bytes(), nil
}

func isCJKRune(value rune) bool {
	return (value >= 0x3400 && value <= 0x4dbf) || (value >= 0x4e00 && value <= 0x9fff)
}

func isHorizontalOCRSpace(value rune) bool {
	return value == ' ' || value == '\t' || value == 0x3000 || value == 0x00a0
}

// postprocessOCRText folds horizontal whitespace between CJK characters while
// preserving newlines and non-CJK spacing.
func postprocessOCRText(text string) string {
	runes := []rune(text)
	output := make([]rune, 0, len(runes))
	for index := 0; index < len(runes); {
		current := runes[index]
		output = append(output, current)
		if !isCJKRune(current) {
			index++
			continue
		}
		next := index + 1
		spaceEnd := next
		for spaceEnd < len(runes) && isHorizontalOCRSpace(runes[spaceEnd]) {
			spaceEnd++
		}
		if spaceEnd > next && spaceEnd < len(runes) && isCJKRune(runes[spaceEnd]) {
			index = spaceEnd
			continue
		}
		index++
	}
	return string(output)
}
