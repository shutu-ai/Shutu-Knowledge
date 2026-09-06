package parser

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"sort"
	"strconv"
	"strings"

	pdftext "github.com/ledongthuc/pdf"
)

// PDFImage is one decoded raster embedded in a PDF. PNG is the stable
// interchange format consumed by vision providers.
type PDFImage struct {
	Page   int
	Width  int
	Height int
	PNG    []byte
}

// PDFImageDecodeFunc decodes one bounded codec stream (for example JBIG2 or
// JPEG 2000) into PNG bytes. Implementations are optional and deployment-owned.
type PDFImageDecodeFunc func(ctx context.Context, format string, input []byte) ([]byte, error)

type PDFImageOptions struct {
	Context context.Context
	Decode  PDFImageDecodeFunc
}

type PDFImageOption func(*PDFImageOptions)

func WithPDFImageContext(ctx context.Context) PDFImageOption {
	return func(options *PDFImageOptions) { options.Context = ctx }
}

func WithPDFImageDecoder(decoder PDFImageDecodeFunc) PDFImageOption {
	return func(options *PDFImageOptions) { options.Decode = decoder }
}

func (options PDFImageOptions) context() context.Context {
	if options.Context != nil {
		return options.Context
	}
	return context.Background()
}

// ExtractPDFImages decodes image XObjects in page order. It deliberately
// skips unsupported encodings instead of failing the whole ingestion path.
func ExtractPDFImages(data []byte, options ...PDFImageOption) ([]PDFImage, error) {
	settings := PDFImageOptions{}
	for _, option := range options {
		option(&settings)
	}
	reader, err := pdftext.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("open PDF: %w", err)
	}
	out := make([]PDFImage, 0)
	rasterBytes := 0
	for page := 1; page <= reader.NumPage() && page <= maxPDFPages; page++ {
		images, err := extractPagePDFImages(data, reader.Page(page), page, &rasterBytes, settings)
		if err != nil {
			return nil, err
		}
		out = append(out, images...)
		if len(out) >= maxPDFImages {
			out = out[:maxPDFImages]
			break
		}
	}
	return out, nil
}

const (
	maxPDFImages           = 200
	maxPDFPages            = 100
	maxPDFImageStreamBytes = 64 << 20
	maxJPEGPDFImagePixels  = 4 << 20
	maxPDFImagePixels      = 32 << 20
	maxPDFRasterBytes      = 512 << 20
)

var endStreamMarker = []byte("endstream")

func extractPagePDFImages(document []byte, page pdftext.Page, pageID int, rasterBytes *int, options PDFImageOptions) ([]PDFImage, error) {
	return extractXObjectImages(document, page.Resources().Key("XObject"), pageID, rasterBytes, options)
}

func extractXObjectImages(document []byte, container pdftext.Value, pageID int, rasterBytes *int, options PDFImageOptions) ([]PDFImage, error) {
	if container.IsNull() {
		return nil, nil
	}
	names := append([]string(nil), container.Keys()...)
	sort.Strings(names)
	out := make([]PDFImage, 0)
	for _, name := range names {
		item := container.Key(name)
		switch item.Key("Subtype").Name() {
		case "Image":
			width := int(item.Key("Width").Int64())
			height := int(item.Key("Height").Int64())
			if width <= 0 || height <= 0 || width*height > maxPDFImagePixels || width*height*4 > maxPDFRasterBytes-*rasterBytes {
				continue
			}
			img, err := decodePDFImage(document, item, options)
			if err != nil {
				// PDFs commonly mix icons, masks, and encodings that a pure-Go
				// reader cannot decode. Vision captioning is best-effort.
				continue
			}
			img.Page = pageID
			pixels := img.Width * img.Height
			rgbaBytes := pixels * 4
			if pixels > maxPDFImagePixels || *rasterBytes+rgbaBytes > maxPDFRasterBytes {
				continue
			}
			*rasterBytes += rgbaBytes
			out = append(out, img)
		case "Form":
			nested, err := extractXObjectImages(document, item.Key("Resources").Key("XObject"), pageID, rasterBytes, options)
			if err != nil {
				return nil, err
			}
			out = append(out, nested...)
		}
		if len(out) >= maxPDFImages {
			out = out[:maxPDFImages]
			break
		}
	}
	return out, nil
}

func decodePDFImage(document []byte, item pdftext.Value, options PDFImageOptions) (PDFImage, error) {
	width := int(item.Key("Width").Int64())
	height := int(item.Key("Height").Int64())
	if width <= 0 || height <= 0 || width > 20_000 || height > 20_000 {
		return PDFImage{}, fmt.Errorf("unsupported image dimensions %dx%d", width, height)
	}

	filters, err := pdfFilterNames(item)
	if err != nil {
		return PDFImage{}, err
	}
	terminal, err := pdfTerminalImageFilter(filters)
	if err != nil {
		return PDFImage{}, err
	}
	if terminal == "DCTDecode" {
		base, err := decodeDCTPDFImage(document, item, width, height)
		return composePDFImageSoftMask(document, item, width, height, base, err)
	}
	if terminal == "CCITTFaxDecode" {
		base, err := decodeCCITTPDFImage(document, item, width, height)
		return composePDFImageSoftMask(document, item, width, height, base, err)
	}
	if terminal == "JBIG2Decode" || terminal == "JPXDecode" {
		base, err := decodeExternalPDFImage(document, item, width, height, terminal, options)
		return composePDFImageSoftMask(document, item, width, height, base, err)
	}

	bits := int(item.Key("BitsPerComponent").Int64())
	if bits == 0 {
		bits = 8
	}
	space, err := newPDFColorSpace(document, item, bits)
	if err != nil {
		return PDFImage{}, err
	}
	encoded, err := readPDFImageStream(document, item)
	if err != nil {
		return PDFImage{}, err
	}
	raw, err := decodePDFImageFilters(document, encoded, item, terminal)
	if err != nil {
		return PDFImage{}, err
	}
	expected := pdfImageSampleBytes(width, height, space)
	if len(raw) < expected {
		return PDFImage{}, fmt.Errorf("image sample buffer is short: %d < %d", len(raw), expected)
	}

	base, err := samplesToImage(raw, width, height, space)
	return composePDFImageSoftMask(document, item, width, height, base, err)
}

func decodeDCTPDFImage(document []byte, item pdftext.Value, width, height int) (image.Image, error) {
	encoded, err := readPDFImageStream(document, item)
	if err != nil {
		return nil, err
	}
	raw, err := decodePDFImageFilters(document, encoded, item, "DCTDecode")
	if err != nil {
		return nil, err
	}
	config, err := jpeg.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("decode JPEG header: %w", err)
	}
	if config.Width != width || config.Height != height {
		return nil, fmt.Errorf("JPEG dimensions %dx%d do not match PDF image %dx%d", config.Width, config.Height, width, height)
	}
	if config.Width*config.Height > maxJPEGPDFImagePixels {
		return nil, fmt.Errorf("JPEG image exceeds %d pixels", maxJPEGPDFImagePixels)
	}

	decoded, err := jpeg.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("decode JPEG: %w", err)
	}
	rgba := image.NewRGBA(image.Rect(0, 0, config.Width, config.Height))
	draw.Draw(rgba, rgba.Bounds(), decoded, decoded.Bounds().Min, draw.Src)
	return rgba, nil
}

func composePDFImageSoftMask(document []byte, item pdftext.Value, width, height int, base image.Image, baseErr error) (PDFImage, error) {
	if baseErr != nil {
		return PDFImage{}, baseErr
	}
	maskItem := item.Key("SMask")
	if !maskItem.IsNull() {
		mask, err := decodePDFSoftMask(document, maskItem)
		if err != nil {
			return PDFImage{}, err
		}
		base = composePDFAlpha(base, mask)
	}
	var pngBytes bytes.Buffer
	if err := png.Encode(&pngBytes, base); err != nil {
		return PDFImage{}, fmt.Errorf("encode image: %w", err)
	}
	return PDFImage{Width: width, Height: height, PNG: pngBytes.Bytes()}, nil
}

func decodePDFSoftMask(document []byte, item pdftext.Value) (*image.Alpha, error) {
	width := int(item.Key("Width").Int64())
	height := int(item.Key("Height").Int64())
	if width <= 0 || height <= 0 || width > 20_000 || height > 20_000 {
		return nil, fmt.Errorf("unsupported soft-mask dimensions %dx%d", width, height)
	}
	filters, err := pdfFilterNames(item)
	if err != nil {
		return nil, err
	}
	terminal, err := pdfTerminalImageFilter(filters)
	if err != nil {
		return nil, err
	}

	var decoded image.Image
	encoded, err := readPDFImageStream(document, item)
	if err != nil {
		return nil, err
	}
	switch terminal {
	case "DCTDecode":
		raw, err := decodePDFImageFilters(document, encoded, item, terminal)
		if err != nil {
			return nil, err
		}
		decoded, err = jpeg.Decode(bytes.NewReader(raw))
		if err != nil {
			return nil, fmt.Errorf("decode JPEG soft mask: %w", err)
		}
	case "CCITTFaxDecode":
		decoded, err = decodeCCITTPDFImage(document, item, width, height)
		if err != nil {
			return nil, err
		}
	default:
		bits := int(item.Key("BitsPerComponent").Int64())
		if bits == 0 {
			bits = 8
		}
		switch bits {
		case 1, 2, 4, 8, 16:
		default:
			return nil, fmt.Errorf("unsupported soft-mask bit depth %d", bits)
		}
		raw, err := decodePDFImageFilters(document, encoded, item, "")
		if err != nil {
			return nil, err
		}
		expected := pdfRowBytes(width, bits, 1) * height
		if len(raw) < expected {
			return nil, fmt.Errorf("soft-mask sample buffer is short: %d < %d", len(raw), expected)
		}
		space := pdfColorSpace{kind: "gray", components: 1, bits: bits}
		decoded, err = samplesToImage(raw, width, height, space)
		if err != nil {
			return nil, err
		}
	}

	bounds := decoded.Bounds()
	if bounds.Dx() != width || bounds.Dy() != height {
		return nil, fmt.Errorf("soft-mask dimensions %dx%d do not match image %dx%d", bounds.Dx(), bounds.Dy(), width, height)
	}
	alpha := image.NewAlpha(image.Rect(0, 0, width, height))
	inverted := pdfSoftMaskIsInverted(document, item)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			gray := color.GrayModel.Convert(decoded.At(bounds.Min.X+x, bounds.Min.Y+y)).(color.Gray)
			value := gray.Y
			if inverted {
				value = ^value
			}
			alpha.SetAlpha(x, y, color.Alpha{A: value})
		}
	}
	return alpha, nil
}

func pdfSoftMaskIsInverted(document []byte, item pdftext.Value) bool {
	dictionary, err := pdfImageDictionary(document, item)
	if err != nil {
		return false
	}
	raw, ok := pdfDictionaryValue(dictionary, "Decode")
	if !ok {
		return false
	}
	values := pdfArrayValues(raw)
	return len(values) == 2 && pdfRawNumber(values[0], 0) == 1 && pdfRawNumber(values[1], 1) == 0
}

func composePDFAlpha(base image.Image, mask *image.Alpha) image.Image {
	bounds := base.Bounds()
	output := image.NewRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	for y := 0; y < bounds.Dy(); y++ {
		for x := 0; x < bounds.Dx(); x++ {
			colorValue := base.At(bounds.Min.X+x, bounds.Min.Y+y)
			red, green, blue, _ := color.RGBAModel.Convert(colorValue).(color.RGBA).RGBA()
			alpha := uint8(mask.AlphaAt(x, y).A)
			output.SetRGBA(x, y, color.RGBA{R: uint8(red >> 8), G: uint8(green >> 8), B: uint8(blue >> 8), A: alpha})
		}
	}
	return output
}

func readPDFImageStream(document []byte, item pdftext.Value) ([]byte, error) {
	offset, err := pdfStreamOffset(item)
	if err != nil {
		return nil, err
	}
	length := item.Key("Length").Int64()
	if length > maxPDFImageStreamBytes {
		return nil, fmt.Errorf("image stream exceeds %d bytes", maxPDFImageStreamBytes)
	}
	if length <= 0 || offset < 0 || offset+length > int64(len(document)) {
		return readPDFStreamToEndMarker(document, offset)
	}
	return document[offset : offset+length], nil
}

func pdfStreamOffset(item pdftext.Value) (int64, error) {
	description := item.String()
	marker := strings.LastIndexByte(description, '@')
	if marker < 0 || marker == len(description)-1 {
		return 0, fmt.Errorf("PDF image stream has no offset")
	}
	offset, err := strconv.ParseInt(description[marker+1:], 10, 64)
	if err != nil || offset < 0 {
		return 0, fmt.Errorf("invalid PDF image stream offset")
	}
	return offset, nil
}

func readPDFStreamToEndMarker(document []byte, offset int64) ([]byte, error) {
	if offset < 0 || offset >= int64(len(document)) {
		return nil, fmt.Errorf("PDF image stream is outside document")
	}
	start := int(offset)
	end := min(len(document), start+maxPDFImageStreamBytes+len(endStreamMarker))
	marker := bytes.Index(document[start:end], endStreamMarker)
	if marker < 0 {
		return nil, fmt.Errorf("PDF image stream has no end marker")
	}
	return document[start : start+marker], nil
}
