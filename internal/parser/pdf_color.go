package parser

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"strings"

	pdftext "github.com/ledongthuc/pdf"
)

type pdfColorSpace struct {
	kind       string
	components int
	bits       int
	base       *pdfColorSpace
	tint       *pdfTintFunction
	palette    []byte
}

func newPDFColorSpace(document []byte, item pdftext.Value, bits int) (pdfColorSpace, error) {
	space := pdfColorSpace{bits: bits}
	switch bits {
	case 1, 2, 4, 8, 16:
	default:
		return space, fmt.Errorf("unsupported PDF image bit depth %d", bits)
	}

	dictionary, err := pdfImageDictionary(document, item)
	if err != nil {
		return space, err
	}
	rawColor, ok := pdfDictionaryValue(dictionary, "ColorSpace")
	if !ok {
		return space, fmt.Errorf("PDF image has no color space")
	}

	if rawColor[0] != '[' {
		name := strings.TrimPrefix(string(rawColor), "/")
		switch name {
		case "DeviceGray", "CalGray", "G":
			space.kind = "gray"
			space.components = 1
		case "DeviceRGB", "CalRGB", "RGB":
			space.kind = "rgb"
			space.components = 3
		case "DeviceCMYK", "CMYK":
			space.kind = "cmyk"
			space.components = 4
		case "Lab":
			space.kind = "lab"
			space.components = 3
		default:
			return space, fmt.Errorf("unsupported PDF color space %q", name)
		}
		return space, nil
	}

	parts := pdfArrayValues(rawColor)
	if len(parts) == 0 {
		return space, fmt.Errorf("PDF image color space is malformed")
	}
	switch family := strings.TrimPrefix(string(parts[0]), "/"); family {
	case "Separation", "DeviceN":
		return newPDFTintColorSpace(document, item, bits)
	case "ICCBased":
		profile := pdfArrayValues(parts[1])
		if len(profile) != 2 {
			return space, fmt.Errorf("ICC PDF color space is malformed")
		}
		components := int(pdfDictionaryNumber(profile[1], "N", 0))
		switch components {
		case 1:
			space.kind = "gray"
		case 3:
			space.kind = "rgb"
		case 4:
			space.kind = "cmyk"
		default:
			return space, fmt.Errorf("unsupported ICC component count %d", components)
		}
		space.components = components
	case "Indexed", "I":
		if len(parts) != 4 {
			return space, fmt.Errorf("indexed PDF color space is malformed")
		}
		high := pdfRawNumber(parts[2], -1)
		if high < 0 || high > 255 {
			return space, fmt.Errorf("invalid indexed PDF color maximum %d", high)
		}
		base, err := pdfBaseColorSpaceFromRaw(document, parts[1])
		if err != nil {
			return space, err
		}
		palette, err := pdfDecodePDFString(parts[3])
		if err != nil {
			return space, fmt.Errorf("read indexed PDF palette: %w", err)
		}
		expected := (int(high) + 1) * base.components
		if len(palette) < expected {
			return space, fmt.Errorf("indexed PDF palette is short: %d < %d", len(palette), expected)
		}
		space.kind = "indexed"
		space.base = &base
		space.palette = palette[:expected]
		space.components = 1
	case "Lab":
		space.kind = "lab"
		space.components = 3
	case "CalRGB":
		space.kind = "rgb"
		space.components = 3
	case "CalGray":
		space.kind = "gray"
		space.components = 1
	default:
		return space, fmt.Errorf("unsupported PDF color space family %q", family)
	}
	return space, nil
}

func newPDFTintColorSpace(document []byte, item pdftext.Value, bits int) (pdfColorSpace, error) {
	return newPDFTintColorSpaceFromValue(document, item.Key("ColorSpace"), bits)
}

func newPDFTintColorSpaceFromValue(document []byte, colorSpace pdftext.Value, bits int) (pdfColorSpace, error) {
	if colorSpace.Kind() != pdftext.Array || colorSpace.Len() < 4 {
		return pdfColorSpace{}, fmt.Errorf("PDF Separation/DeviceN color space is malformed")
	}
	family := colorSpace.Index(0).Name()
	inputCount := 1
	if family == "DeviceN" {
		names := colorSpace.Index(1)
		if names.Kind() != pdftext.Array || names.Len() == 0 {
			return pdfColorSpace{}, fmt.Errorf("PDF DeviceN color space has no component names")
		}
		inputCount = names.Len()
	} else if family != "Separation" {
		return pdfColorSpace{}, fmt.Errorf("unsupported tint color space family %q", family)
	}

	base, err := newPDFAlternateColorSpace(document, colorSpace.Index(2), 8)
	if err != nil {
		return pdfColorSpace{}, err
	}
	tint, err := newPDFTintFunction(document, colorSpace.Index(3), inputCount)
	if err != nil {
		return pdfColorSpace{}, err
	}
	if tint.outputCount != base.components {
		return pdfColorSpace{}, fmt.Errorf("PDF tint transform outputs %d components, alternate space expects %d", tint.outputCount, base.components)
	}
	return pdfColorSpace{kind: "tint", components: inputCount, bits: bits, base: &base, tint: tint}, nil
}

func newPDFAlternateColorSpace(document []byte, value pdftext.Value, bits int) (pdfColorSpace, error) {
	space := pdfColorSpace{bits: bits}
	switch value.Kind() {
	case pdftext.Name:
		switch name := value.Name(); name {
		case "DeviceGray", "CalGray", "G":
			space.kind, space.components = "gray", 1
		case "DeviceRGB", "CalRGB", "RGB":
			space.kind, space.components = "rgb", 3
		case "DeviceCMYK", "CMYK":
			space.kind, space.components = "cmyk", 4
		case "Lab":
			space.kind, space.components = "lab", 3
		default:
			return space, fmt.Errorf("unsupported PDF alternate color space %q", name)
		}
		return space, nil
	case pdftext.Array:
	default:
		return space, fmt.Errorf("unsupported PDF alternate color space kind %d", value.Kind())
	}

	switch family := value.Index(0).Name(); family {
	case "ICCBased":
		if value.Len() != 2 {
			return space, fmt.Errorf("PDF ICC alternate color space is malformed")
		}
		profile := value.Index(1)
		components := int(profile.Key("N").Int64())
		switch components {
		case 1:
			space.kind = "gray"
		case 3:
			space.kind = "rgb"
		case 4:
			space.kind = "cmyk"
		default:
			return space, fmt.Errorf("unsupported PDF ICC alternate component count %d", components)
		}
		space.components = components
	case "Separation", "DeviceN":
		return newPDFTintColorSpaceFromValue(document, value, bits)
	default:
		return space, fmt.Errorf("unsupported PDF alternate color space family %q", family)
	}
	return space, nil
}

func pdfBaseColorSpaceFromRaw(document []byte, raw []byte) (pdfColorSpace, error) {
	if raw[0] != '[' {
		switch strings.TrimPrefix(string(raw), "/") {
		case "DeviceGray", "CalGray", "G":
			return pdfColorSpace{kind: "gray", components: 1, bits: 8}, nil
		case "DeviceRGB", "CalRGB", "RGB":
			return pdfColorSpace{kind: "rgb", components: 3, bits: 8}, nil
		case "DeviceCMYK", "CMYK":
			return pdfColorSpace{kind: "cmyk", components: 4, bits: 8}, nil
		default:
			return pdfColorSpace{}, fmt.Errorf("unsupported indexed PDF base color space %q", string(raw))
		}
	}
	parts := pdfArrayValues(raw)
	if len(parts) == 2 && strings.TrimPrefix(string(parts[0]), "/") == "ICCBased" {
		profile := pdfArrayValues(parts[1])
		if len(profile) == 2 {
			switch pdfDictionaryNumber(profile[1], "N", 0) {
			case 1:
				return pdfColorSpace{kind: "gray", components: 1, bits: 8}, nil
			case 3:
				return pdfColorSpace{kind: "rgb", components: 3, bits: 8}, nil
			case 4:
				return pdfColorSpace{kind: "cmyk", components: 4, bits: 8}, nil
			}
		}
	}
	return pdfColorSpace{}, fmt.Errorf("unsupported indexed PDF base color space")
}

func pdfRowBytes(width, bits, components int) int {
	return (width*components*bits + 7) / 8
}

func pdfImageSampleBytes(width, height int, space pdfColorSpace) int {
	return pdfRowBytes(width, space.bits, space.components) * height
}

func pdfPixelColor(data []byte, row, pixel, width int, space pdfColorSpace) (color.RGBA, error) {
	rowOffset := row * pdfRowBytes(width, space.bits, space.components)
	if space.tint != nil {
		inputs := make([]float64, space.components)
		maximum := float64(uint64(1)<<uint(space.bits) - 1)
		for index := 0; index < space.components; index++ {
			raw, _, err := pdfSample(data, rowOffset, pixel, space.components, space.bits, index)
			if err != nil {
				return color.RGBA{}, err
			}
			inputs[index] = float64(raw) / maximum
		}
		outputs, err := space.tint.eval(inputs)
		if err != nil {
			return color.RGBA{}, err
		}
		return pdfTintOutputsToColor(outputs, *space.base, space.tint)
	}
	if space.kind == "indexed" {
		sample, _, err := pdfSample(data, rowOffset, pixel, 1, space.bits, 0)
		if err != nil {
			return color.RGBA{}, err
		}
		offset := int(sample) * space.base.components
		if offset+space.base.components > len(space.palette) {
			return color.RGBA{}, fmt.Errorf("indexed color outside palette")
		}
		return colorFromComponents(space.palette[offset:], 0, 0, *space.base)
	}
	return colorFromComponents(data, rowOffset, pixel, space)
}

func pdfTintOutputsToColor(outputs []float64, base pdfColorSpace, transform *pdfTintFunction) (color.RGBA, error) {
	if base.kind == "indexed" {
		maximum := len(base.palette)/base.components - 1
		indexValue := outputs[0]
		if transform != nil && len(transform.outputRange) >= 2 && transform.outputRange[1] != transform.outputRange[0] {
			low := transform.outputRange[0]
			high := transform.outputRange[1]
			indexValue = (pdfClampFloat(indexValue, low, high) - low) / (high - low)
		}
		index := int(math.Round(pdfClampFloat(indexValue, 0, 1) * float64(maximum)))
		offset := index * base.components
		if offset < 0 || offset+base.components > len(base.palette) {
			return color.RGBA{}, fmt.Errorf("PDF indexed tint output is outside palette")
		}
		return colorFromComponents(base.palette, offset, 0, *base.base)
	}
	if base.kind == "lab" {
		if len(outputs) != 3 {
			return color.RGBA{}, fmt.Errorf("PDF Lab tint output has %d components", len(outputs))
		}
		samples := make([]uint8, 3)
		for index, value := range outputs {
			low, high := 0.0, 1.0
			if transform != nil && index*2+1 < len(transform.outputRange) {
				low = transform.outputRange[index*2]
				high = transform.outputRange[index*2+1]
			}
			if index > 0 {
				low, high = -128, 127
			}
			value = pdfClampFloat(value, low, high)
			samples[index] = uint8(math.Round((value - low) * 255 / (high - low)))
		}
		red, green, blue := labToRGB(samples)
		return color.RGBA{R: red, G: green, B: blue, A: 255}, nil
	}
	normalized := make([]float64, len(outputs))
	for index, value := range outputs {
		low, high := 0.0, 1.0
		if transform != nil && index*2+1 < len(transform.outputRange) {
			low = transform.outputRange[index*2]
			high = transform.outputRange[index*2+1]
		}
		normalized[index] = pdfClampFloat(value, low, high)
		if high != low {
			normalized[index] = (value - low) / (high - low)
		}
	}
	if base.tint != nil {
		nextOutputs, err := base.tint.eval(normalized)
		if err != nil {
			return color.RGBA{}, err
		}
		return pdfTintOutputsToColor(nextOutputs, *base.base, base.tint)
	}
	base.bits = 8
	samples := make([]uint8, len(normalized))
	for index, value := range normalized {
		samples[index] = uint8(math.Min(255, math.Max(0, math.Round(value*255))))
	}
	return colorFromComponents(samples, 0, 0, base)
}

func colorFromComponents(data []byte, rowOffset, pixel int, space pdfColorSpace) (color.RGBA, error) {
	samples := make([]uint8, space.components)
	for index := range samples {
		_, scaled, err := pdfSample(data, rowOffset, pixel, space.components, space.bits, index)
		if err != nil {
			return color.RGBA{}, err
		}
		samples[index] = scaled
	}
	switch space.kind {
	case "gray":
		return color.RGBA{R: samples[0], G: samples[0], B: samples[0], A: 255}, nil
	case "rgb":
		return color.RGBA{R: samples[0], G: samples[1], B: samples[2], A: 255}, nil
	case "lab":
		red, green, blue := labToRGB(samples)
		return color.RGBA{R: red, G: green, B: blue, A: 255}, nil
	case "cmyk":
		c := int(samples[0])
		magenta := int(samples[1])
		yellow := int(samples[2])
		key := int(samples[3])
		return color.RGBA{
			R: uint8(255 - min(255, c+key)),
			G: uint8(255 - min(255, magenta+key)),
			B: uint8(255 - min(255, yellow+key)),
			A: 255,
		}, nil
	default:
		return color.RGBA{}, fmt.Errorf("unsupported PDF color space kind %q", space.kind)
	}
}

func pdfSample(data []byte, rowOffset, pixel, components, bits, component int) (uint64, uint8, error) {
	position := rowOffset*8 + (pixel*components+component)*bits
	reader := &pdfBitReader{data: data, position: position}
	sample, ok := reader.read(bits)
	if !ok {
		return 0, 0, fmt.Errorf("PDF image sample is outside data")
	}
	return uint64(sample), uint8(float64(sample) * sampleScale(bits)), nil
}

func sampleScale(bits int) float64 {
	return 255 / float64((uint(1)<<bits)-1)
}

func labToRGB(samples []uint8) (uint8, uint8, uint8) {
	lightness := float64(samples[0]) * 100 / 255
	aStar := float64(samples[1]) - 128
	bStar := float64(samples[2]) - 128

	fy := (lightness + 16) / 116
	fx := fy + aStar/500
	fz := fy - bStar/200
	x := labInverse(fx) * 0.96422
	y := labY(lightness)
	z := labInverse(fz) * 0.82521

	linearRed := 3.1338561*x - 1.6168667*y - 0.4906146*z
	linearGreen := -0.9787684*x + 1.9161415*y + 0.0334540*z
	linearBlue := 0.0719453*x - 0.2289914*y + 1.4052427*z
	return linearToSRGB(linearRed), linearToSRGB(linearGreen), linearToSRGB(linearBlue)
}

func labInverse(value float64) float64 {
	cubed := value * value * value
	if cubed > 216.0/24389.0 {
		return cubed
	}
	return (116*value - 16) / 24389.0 / 27.0
}

func labY(lightness float64) float64 {
	fy := (lightness + 16) / 116
	if fy*fy*fy > 216.0/24389.0 {
		return fy * fy * fy
	}
	return lightness / 24389.0 / 27.0
}

func samplesToImage(raw []byte, width, height int, space pdfColorSpace) (image.Image, error) {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			value, err := pdfPixelColor(raw, y, x, width, space)
			if err != nil {
				return img, err
			}
			img.SetRGBA(x, y, value)
		}
	}
	return img, nil
}

func linearToSRGB(value float64) uint8 {
	if value <= 0.0031308 {
		value *= 12.92
	} else {
		value = 1.055*math.Pow(value, 1/2.4) - 0.055
	}
	return clampByte(value * 255)
}

func clampByte(value float64) uint8 {
	return uint8(math.Min(255, math.Max(0, math.Round(value))))
}
