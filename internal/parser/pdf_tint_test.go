package parser

import (
	"strings"
	"testing"
)

func TestPDFSampledTintInterpolates(t *testing.T) {
	function := &pdfTintFunction{
		functionType:  0,
		inputCount:    1,
		outputCount:   1,
		domain:        []float64{0, 1},
		outputRange:   []float64{0, 1},
		sizes:         []int{2},
		bitsPerSample: 8,
		samples:       []byte{0, 255},
		sampleEncode:  []float64{0, 1},
		sampleDecode:  []float64{0, 1},
	}
	for _, test := range []struct {
		input float64
		want  float64
	}{
		{input: 0, want: 0},
		{input: 0.25, want: 0.25},
		{input: 0.75, want: 0.75},
		{input: 1, want: 1},
	} {
		got, err := function.eval([]float64{test.input})
		if err != nil {
			t.Fatalf("eval %.2f: %v", test.input, err)
		}
		if len(got) != 1 || got[0] != test.want {
			t.Fatalf("eval %.2f = %v, want [%v]", test.input, got, test.want)
		}
	}
}

func TestPDFStitchingTintUsesSubfunctions(t *testing.T) {
	low := &pdfTintFunction{functionType: 2, inputCount: 1, outputCount: 1, domain: []float64{0, 1}, c0: []float64{0}, c1: []float64{0}, exponent: 1}
	high := &pdfTintFunction{functionType: 2, inputCount: 1, outputCount: 1, domain: []float64{0, 1}, c0: []float64{0}, c1: []float64{1}, exponent: 1}
	function := &pdfTintFunction{
		functionType: 3,
		inputCount:   1,
		outputCount:  1,
		domain:       []float64{0, 1},
		bounds:       []float64{0.5},
		encode:       []float64{0, 1, 0, 1},
		subfunctions: []*pdfTintFunction{low, high},
	}
	for _, test := range []struct {
		input float64
		want  float64
	}{
		{input: 0, want: 0},
		{input: 0.25, want: 0},
		{input: 0.75, want: 0.5},
		{input: 1, want: 1},
	} {
		got, err := function.eval([]float64{test.input})
		if err != nil {
			t.Fatalf("eval %.2f: %v", test.input, err)
		}
		if len(got) != 1 || got[0] != test.want {
			t.Fatalf("eval %.2f = %v, want [%v]", test.input, got, test.want)
		}
	}
}

func TestPDFCalculatorTintRejectsStackOverflow(t *testing.T) {
	body := "{" + strings.Repeat(" 1", 101) + " }"
	operations, err := pdfTintLex([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	function := &pdfTintFunction{
		functionType: 4,
		inputCount:   1,
		outputCount:  1,
		domain:       []float64{0, 1},
		outputRange:  []float64{0, 1},
		operations:   operations,
	}
	if _, err := function.eval([]float64{0}); err == nil {
		t.Fatal("calculator stack overflow was accepted")
	}
}
