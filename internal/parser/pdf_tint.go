package parser

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	pdftext "github.com/ledongthuc/pdf"
)

type pdfTintFunction struct {
	functionType int64
	inputCount   int
	outputCount  int
	domain       []float64
	outputRange  []float64

	c0       []float64
	c1       []float64
	exponent float64

	subfunctions []*pdfTintFunction
	bounds       []float64
	encode       []float64

	sizes         []int
	bitsPerSample int
	samples       []byte
	sampleEncode  []float64
	sampleDecode  []float64

	operations []pdfTintToken
}

func newPDFTintFunction(document []byte, value pdftext.Value, inputCount int) (*pdfTintFunction, error) {
	if value.IsNull() || (value.Kind() != pdftext.Dict && value.Kind() != pdftext.Stream) {
		return nil, fmt.Errorf("PDF tint transform is not a function")
	}
	function := &pdfTintFunction{inputCount: inputCount}
	domain, err := pdfFloatArrayValue(value.Key("Domain"))
	if err != nil {
		return nil, fmt.Errorf("read PDF tint domain: %w", err)
	}
	if len(domain) != inputCount*2 {
		return nil, fmt.Errorf("invalid PDF tint domain size %d", len(domain))
	}
	function.domain = domain
	if rangeValue := value.Key("Range"); !rangeValue.IsNull() {
		ranges, err := pdfFloatArrayValue(rangeValue)
		if err != nil {
			return nil, fmt.Errorf("read PDF tint range: %w", err)
		}
		if len(ranges)%2 != 0 {
			return nil, fmt.Errorf("invalid PDF tint range size %d", len(ranges))
		}
		function.outputCount = len(ranges) / 2
		function.outputRange = ranges
	}

	switch functionType := value.Key("FunctionType").Int64(); functionType {
	case 0:
		return newPDFSampledTintFunction(document, value, function)
	case 2:
		return newPDFExponentialTintFunction(value, function)
	case 3:
		return newPDFStitchingTintFunction(document, value, function)
	case 4:
		return newPDFCalculatorTintFunction(document, value, function)
	default:
		return nil, fmt.Errorf("unsupported PDF tint function type %d", functionType)
	}
}

func pdfFloatArrayValue(value pdftext.Value) ([]float64, error) {
	if value.IsNull() {
		return nil, nil
	}
	if value.Kind() != pdftext.Array {
		return nil, fmt.Errorf("value is not an array")
	}
	output := make([]float64, value.Len())
	for index := range output {
		item := value.Index(index)
		if item.Kind() != pdftext.Integer && item.Kind() != pdftext.Real {
			return nil, fmt.Errorf("array item %d is not numeric", index)
		}
		output[index] = item.Float64()
	}
	return output, nil
}

func newPDFExponentialTintFunction(value pdftext.Value, function *pdfTintFunction) (*pdfTintFunction, error) {
	c0, err := pdfFloatArrayValue(value.Key("C0"))
	if err != nil {
		return nil, fmt.Errorf("read C0: %w", err)
	}
	c1, err := pdfFloatArrayValue(value.Key("C1"))
	if err != nil {
		return nil, fmt.Errorf("read C1: %w", err)
	}
	if len(c0) == 0 {
		c0 = []float64{0}
	}
	if len(c1) == 0 {
		c1 = []float64{1}
	}
	if len(c0) != len(c1) {
		return nil, fmt.Errorf("PDF exponential tint C0/C1 sizes differ: %d != %d", len(c0), len(c1))
	}
	function.functionType = 2
	function.outputCount = len(c1)
	function.c0 = c0
	function.c1 = c1
	function.exponent = value.Key("N").Float64()
	return function, nil
}

func newPDFStitchingTintFunction(document []byte, value pdftext.Value, function *pdfTintFunction) (*pdfTintFunction, error) {
	if function.inputCount != 1 {
		return nil, fmt.Errorf("PDF stitching tint requires one input")
	}
	functionsValue := value.Key("Functions")
	if functionsValue.Kind() != pdftext.Array || functionsValue.Len() == 0 {
		return nil, fmt.Errorf("PDF stitching tint has no functions")
	}
	bounds, err := pdfFloatArrayValue(value.Key("Bounds"))
	if err != nil {
		return nil, fmt.Errorf("read bounds: %w", err)
	}
	encode, err := pdfFloatArrayValue(value.Key("Encode"))
	if err != nil {
		return nil, fmt.Errorf("read encode: %w", err)
	}
	if len(bounds) != functionsValue.Len()-1 || len(encode) != functionsValue.Len()*2 {
		return nil, fmt.Errorf("invalid PDF stitching tint bounds or encode")
	}
	for index := 0; index < functionsValue.Len(); index++ {
		subfunction, err := newPDFTintFunction(document, functionsValue.Index(index), 1)
		if err != nil {
			return nil, err
		}
		function.subfunctions = append(function.subfunctions, subfunction)
	}
	function.outputCount = function.subfunctions[0].outputCount
	for _, subfunction := range function.subfunctions {
		if subfunction.outputCount != function.outputCount {
			return nil, fmt.Errorf("PDF stitching tint output sizes differ")
		}
	}
	function.functionType = 3
	function.bounds = bounds
	function.encode = encode
	return function, nil
}

func newPDFSampledTintFunction(document []byte, value pdftext.Value, function *pdfTintFunction) (*pdfTintFunction, error) {
	sizesValue := value.Key("Size")
	if sizesValue.Kind() != pdftext.Array || sizesValue.Len() != function.inputCount {
		return nil, fmt.Errorf("PDF sampled tint size dimensions do not match domain")
	}
	if function.outputCount == 0 {
		return nil, fmt.Errorf("PDF sampled tint has no range")
	}
	total := function.outputCount
	for index := 0; index < sizesValue.Len(); index++ {
		size := int(sizesValue.Index(index).Int64())
		if size <= 0 {
			return nil, fmt.Errorf("invalid PDF sampled tint size %d", size)
		}
		function.sizes = append(function.sizes, size)
		total *= size
	}
	bits := int(value.Key("BitsPerSample").Int64())
	switch bits {
	case 1, 2, 4, 8, 12, 16, 24, 32:
	default:
		return nil, fmt.Errorf("unsupported PDF sampled tint bit depth %d", bits)
	}
	if order := value.Key("Order").Int64(); order != 0 && order != 1 {
		return nil, fmt.Errorf("unsupported PDF sampled tint order %d", order)
	}
	sampleEncode, err := pdfFloatArrayValue(value.Key("Encode"))
	if err != nil {
		return nil, fmt.Errorf("read sample encode: %w", err)
	}
	if len(sampleEncode) == 0 {
		for _, size := range function.sizes {
			sampleEncode = append(sampleEncode, 0, float64(size-1))
		}
	}
	sampleDecode, err := pdfFloatArrayValue(value.Key("Decode"))
	if err != nil {
		return nil, fmt.Errorf("read sample decode: %w", err)
	}
	if len(sampleDecode) == 0 {
		sampleDecode = append([]float64(nil), function.outputRange...)
	}
	if len(sampleEncode) != function.inputCount*2 || len(sampleDecode) != function.outputCount*2 {
		return nil, fmt.Errorf("invalid PDF sampled tint encode or decode")
	}
	encoded, err := readPDFImageStream(document, value)
	if err != nil {
		return nil, err
	}
	raw, err := decodePDFImageFilters(document, encoded, value, "")
	if err != nil {
		return nil, err
	}
	if len(raw) < (total*bits+7)/8 {
		return nil, fmt.Errorf("PDF sampled tint data is short")
	}
	function.functionType = 0
	function.bitsPerSample = bits
	function.samples = raw
	function.sampleEncode = sampleEncode
	function.sampleDecode = sampleDecode
	return function, nil
}

func newPDFCalculatorTintFunction(document []byte, value pdftext.Value, function *pdfTintFunction) (*pdfTintFunction, error) {
	if function.outputCount == 0 {
		return nil, fmt.Errorf("PDF calculator tint has no range")
	}
	encoded, err := readPDFImageStream(document, value)
	if err != nil {
		return nil, err
	}
	raw, err := decodePDFImageFilters(document, encoded, value, "")
	if err != nil {
		return nil, err
	}
	operations, err := pdfTintLex(raw)
	if err != nil {
		return nil, err
	}
	function.functionType = 4
	function.operations = operations
	return function, nil
}

func (function *pdfTintFunction) eval(inputs []float64) ([]float64, error) {
	if len(inputs) != function.inputCount {
		return nil, fmt.Errorf("PDF tint input count %d does not match %d", len(inputs), function.inputCount)
	}
	inputs = append([]float64(nil), inputs...)
	for index := range inputs {
		inputs[index] = pdfClampFloat(inputs[index], function.domain[index*2], function.domain[index*2+1])
	}
	var outputs []float64
	var err error
	switch function.functionType {
	case 0:
		outputs, err = function.evalSampled(inputs)
	case 2:
		outputs = make([]float64, len(function.c1))
		power := math.Pow(inputs[0], function.exponent)
		for index := range outputs {
			outputs[index] = function.c0[index] + (function.c1[index]-function.c0[index])*power
		}
	case 3:
		outputs, err = function.evalStitched(inputs[0])
	case 4:
		outputs, err = function.evalCalculator(inputs)
	default:
		err = fmt.Errorf("unsupported PDF tint function type %d", function.functionType)
	}
	if err != nil {
		return nil, err
	}
	for index := range outputs {
		if index*2+1 < len(function.outputRange) {
			outputs[index] = pdfClampFloat(outputs[index], function.outputRange[index*2], function.outputRange[index*2+1])
		}
	}
	return outputs, nil
}

func (function *pdfTintFunction) evalSampled(inputs []float64) ([]float64, error) {
	if len(inputs) > 8 {
		return nil, fmt.Errorf("PDF sampled tint has too many inputs")
	}
	if len(function.sampleEncode) != len(inputs)*2 || len(function.sampleDecode) != function.outputCount*2 {
		return nil, fmt.Errorf("PDF sampled tint encode or decode dimensions are invalid")
	}
	coordinates := make([]int, len(inputs))
	weights := make([]float64, len(inputs))
	for index, input := range inputs {
		low := function.sampleEncode[index*2]
		high := function.sampleEncode[index*2+1]
		domainLow := function.domain[index*2]
		domainHigh := function.domain[index*2+1]
		position := low
		if domainHigh != domainLow {
			position = low + (high-low)*(input-domainLow)/(domainHigh-domainLow)
		}
		position = pdfClampFloat(position, 0, float64(function.sizes[index]-1))
		coordinates[index] = int(math.Floor(position))
		if coordinates[index] >= function.sizes[index]-1 {
			coordinates[index] = max(0, function.sizes[index]-2)
			weights[index] = 1
		} else {
			weights[index] = position - float64(coordinates[index])
		}
	}

	outputs := make([]float64, function.outputCount)
	for corner := 0; corner < 1<<len(inputs); corner++ {
		weight := 1.0
		sampleIndex := 0
		stride := function.outputCount
		for dimension := len(inputs) - 1; dimension >= 0; dimension-- {
			offset := (corner >> uint(dimension)) & 1
			coordinate := coordinates[dimension] + offset
			if offset == 1 {
				weight *= weights[dimension]
			} else {
				weight *= 1 - weights[dimension]
			}
			sampleIndex += coordinate * stride
			stride *= function.sizes[dimension]
		}
		for output := 0; output < function.outputCount; output++ {
			sample, err := function.sample(sampleIndex+output, output)
			if err != nil {
				return nil, err
			}
			outputs[output] += sample * weight
		}
	}
	return outputs, nil
}

func (function *pdfTintFunction) sample(index, output int) (float64, error) {
	reader := &pdfBitReader{data: function.samples, position: index * function.bitsPerSample}
	raw, ok := reader.read(function.bitsPerSample)
	if !ok {
		return 0, fmt.Errorf("PDF sampled tint sample is outside data")
	}
	maximum := float64(uint64(1)<<uint(function.bitsPerSample) - 1)
	low := function.sampleDecode[output*2]
	high := function.sampleDecode[output*2+1]
	return low + (high-low)*(float64(raw)/maximum), nil
}

func (function *pdfTintFunction) evalStitched(input float64) ([]float64, error) {
	boundIndex := len(function.bounds)
	for index, bound := range function.bounds {
		if input < bound {
			boundIndex = index
			break
		}
	}
	if boundIndex == len(function.bounds) {
		boundIndex = len(function.subfunctions) - 1
	}
	low := function.domain[0]
	high := function.domain[1]
	if boundIndex > 0 {
		low = function.bounds[boundIndex-1]
	}
	if boundIndex < len(function.bounds)-1 {
		high = function.bounds[boundIndex]
	}
	encodedLow := function.encode[boundIndex*2]
	encodedHigh := function.encode[boundIndex*2+1]
	encoded := encodedLow
	if high != low {
		encoded = encodedLow + (encodedHigh-encodedLow)*(input-low)/(high-low)
	}
	return function.subfunctions[boundIndex].eval([]float64{encoded})
}

func (function *pdfTintFunction) evalCalculator(inputs []float64) ([]float64, error) {
	stack := make([]pdfTintOperand, 0, len(inputs)+8)
	for _, input := range inputs {
		stack = append(stack, pdfTintOperand{kind: pdfTintNumberOperand, number: input})
	}
	steps := 0
	stack, err := pdfTintExecute(function.operations, stack, &steps)
	if err != nil {
		return nil, err
	}
	if len(stack) < function.outputCount {
		return nil, fmt.Errorf("PDF calculator tint left %d outputs, want %d", len(stack), function.outputCount)
	}
	start := len(stack) - function.outputCount
	outputs := make([]float64, 0, function.outputCount)
	for _, operand := range stack[start:] {
		if operand.kind != pdfTintNumberOperand {
			return nil, fmt.Errorf("PDF calculator tint output is not numeric: %#v", stack[start:])
		}
		outputs = append(outputs, operand.number)
	}
	return outputs, nil
}

type pdfTintTokenKind int

const (
	pdfTintNumber pdfTintTokenKind = iota
	pdfTintBoolean
	pdfTintProcedure
	pdfTintOperator
)

type pdfTintToken struct {
	kind      pdfTintTokenKind
	number    float64
	boolean   bool
	procedure []pdfTintToken
	operator  string
}

type pdfTintOperandKind int

const (
	pdfTintNumberOperand pdfTintOperandKind = iota
	pdfTintBooleanOperand
	pdfTintProcedureOperand
)

type pdfTintOperand struct {
	kind      pdfTintOperandKind
	number    float64
	boolean   bool
	procedure []pdfTintToken
}

func pdfTintLex(data []byte) ([]pdfTintToken, error) {
	tokens, _, err := pdfTintLexUntil(data, 0, false)
	if err != nil {
		return nil, err
	}
	if len(tokens) == 1 && tokens[0].kind == pdfTintProcedure {
		return tokens[0].procedure, nil
	}
	return tokens, nil
}

func pdfTintLexUntil(data []byte, start int, nested bool) ([]pdfTintToken, int, error) {
	tokens := make([]pdfTintToken, 0, 8)
	position := start
	for position < len(data) {
		char := data[position]
		switch {
		case char == ' ' || char == '\t' || char == '\n' || char == '\r' || char == '\f' || char == 0:
			position++
		case char == '%':
			for position < len(data) && data[position] != '\n' && data[position] != '\r' {
				position++
			}
		case char == '{':
			procedure, next, err := pdfTintLexUntil(data, position+1, true)
			if err != nil {
				return nil, position, err
			}
			tokens = append(tokens, pdfTintToken{kind: pdfTintProcedure, procedure: procedure})
			position = next
		case char == '}':
			if !nested {
				return nil, position, fmt.Errorf("unexpected PDF calculator brace")
			}
			return tokens, position + 1, nil
		default:
			end := position
			for end < len(data) {
				value := data[end]
				if value == ' ' || value == '\t' || value == '\n' || value == '\r' || value == '\f' ||
					value == 0 || value == '{' || value == '}' || value == '%' {
					break
				}
				end++
			}
			word := string(data[position:end])
			if number, parseErr := strconv.ParseFloat(strings.TrimPrefix(word, "+"), 64); parseErr == nil {
				tokens = append(tokens, pdfTintToken{kind: pdfTintNumber, number: number})
			} else if word == "true" || word == "false" {
				tokens = append(tokens, pdfTintToken{kind: pdfTintBoolean, boolean: word == "true"})
			} else {
				tokens = append(tokens, pdfTintToken{kind: pdfTintOperator, operator: word})
			}
			position = end
		}
	}
	if nested {
		return nil, position, fmt.Errorf("PDF calculator procedure is not closed")
	}
	return tokens, position, nil
}

func pdfTintExecute(tokens []pdfTintToken, stack []pdfTintOperand, steps *int) ([]pdfTintOperand, error) {
	for _, token := range tokens {
		*steps++
		if *steps > 10_000 || len(stack) > 100 {
			return stack, fmt.Errorf("PDF calculator resource limit exceeded")
		}
		var err error
		switch token.kind {
		case pdfTintNumber:
			stack = append(stack, pdfTintOperand{kind: pdfTintNumberOperand, number: token.number})
		case pdfTintBoolean:
			stack = append(stack, pdfTintOperand{kind: pdfTintBooleanOperand, boolean: token.boolean})
		case pdfTintProcedure:
			stack = append(stack, pdfTintOperand{kind: pdfTintProcedureOperand, procedure: token.procedure})
		case pdfTintOperator:
			stack, err = pdfTintOperatorExecute(token.operator, stack, steps)
			if err != nil {
				return stack, err
			}
		}
	}
	return stack, nil
}

func pdfTintOperatorExecute(operator string, stack []pdfTintOperand, steps *int) ([]pdfTintOperand, error) {
	pop := func() (pdfTintOperand, error) {
		if len(stack) == 0 {
			return pdfTintOperand{}, fmt.Errorf("PDF calculator stack underflow")
		}
		operand := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		return operand, nil
	}
	popNumber := func() (float64, error) {
		operand, err := pop()
		if err != nil {
			return 0, err
		}
		if operand.kind != pdfTintNumberOperand {
			return 0, fmt.Errorf("PDF calculator operand is not numeric")
		}
		return operand.number, nil
	}
	push := func(operand pdfTintOperand) ([]pdfTintOperand, error) {
		stack = append(stack, operand)
		if len(stack) > 100 {
			return stack, fmt.Errorf("PDF calculator stack overflow")
		}
		return stack, nil
	}
	pushNumber := func(value float64) ([]pdfTintOperand, error) {
		return push(pdfTintOperand{kind: pdfTintNumberOperand, number: value})
	}
	pushBoolean := func(value bool) ([]pdfTintOperand, error) {
		return push(pdfTintOperand{kind: pdfTintBooleanOperand, boolean: value})
	}
	binaryNumber := func(operation func(left, right float64) (float64, error)) ([]pdfTintOperand, error) {
		right, err := popNumber()
		if err != nil {
			return stack, err
		}
		left, err := popNumber()
		if err != nil {
			return stack, err
		}
		value, err := operation(left, right)
		if err != nil {
			return stack, err
		}
		return pushNumber(value)
	}

	switch operator {
	case "add":
		return binaryNumber(func(left, right float64) (float64, error) { return left + right, nil })
	case "sub":
		return binaryNumber(func(left, right float64) (float64, error) { return left - right, nil })
	case "mul":
		return binaryNumber(func(left, right float64) (float64, error) { return left * right, nil })
	case "div":
		return binaryNumber(func(left, right float64) (float64, error) {
			if right == 0 {
				return 0, fmt.Errorf("PDF calculator divide by zero")
			}
			return left / right, nil
		})
	case "idiv":
		return binaryNumber(func(left, right float64) (float64, error) {
			if right == 0 {
				return 0, fmt.Errorf("PDF calculator divide by zero")
			}
			return math.Trunc(left / right), nil
		})
	case "mod":
		return binaryNumber(func(left, right float64) (float64, error) {
			if right == 0 {
				return 0, fmt.Errorf("PDF calculator modulo zero")
			}
			return math.Mod(left, right), nil
		})
	case "min":
		return binaryNumber(func(left, right float64) (float64, error) { return math.Min(left, right), nil })
	case "max":
		return binaryNumber(func(left, right float64) (float64, error) { return math.Max(left, right), nil })
	case "atan":
		return binaryNumber(func(left, right float64) (float64, error) { return math.Atan2(left, right), nil })
	case "neg", "abs", "ceiling", "floor", "round", "truncate", "cvi", "cvr", "sqrt", "exp", "ln", "log", "sin", "cos":
		value, err := popNumber()
		if err != nil {
			return stack, err
		}
		switch operator {
		case "neg":
			value = -value
		case "abs":
			value = math.Abs(value)
		case "ceiling":
			value = math.Ceil(value)
		case "floor":
			value = math.Floor(value)
		case "round":
			value = math.Round(value)
		case "truncate", "cvi", "cvr":
			value = math.Trunc(value)
		case "sqrt":
			if value < 0 {
				return stack, fmt.Errorf("PDF calculator sqrt of negative number")
			}
			value = math.Sqrt(value)
		case "exp":
			value = math.Exp(value)
		case "ln":
			if value < 0 {
				return stack, fmt.Errorf("PDF calculator ln of negative number")
			}
			value = math.Log(value)
		case "log":
			if value < 0 {
				return stack, fmt.Errorf("PDF calculator log of negative number")
			}
			value = math.Log10(value)
		case "sin":
			value = math.Sin(value)
		case "cos":
			value = math.Cos(value)
		}
		return pushNumber(value)
	case "not":
		operand, err := pop()
		if err != nil {
			return stack, err
		}
		switch operand.kind {
		case pdfTintBooleanOperand:
			return pushBoolean(!operand.boolean)
		case pdfTintNumberOperand:
			return pushNumber(float64(^uint64(operand.number)))
		default:
			return stack, fmt.Errorf("PDF calculator not operand has wrong type")
		}
	case "dup":
		if len(stack) == 0 {
			return stack, fmt.Errorf("PDF calculator stack underflow")
		}
		return push(stack[len(stack)-1])
	case "exch":
		if len(stack) < 2 {
			return stack, fmt.Errorf("PDF calculator stack underflow")
		}
		stack[len(stack)-1], stack[len(stack)-2] = stack[len(stack)-2], stack[len(stack)-1]
		return stack, nil
	case "pop":
		_, err := pop()
		return stack, err
	case "copy":
		count, err := popNumber()
		if err != nil {
			return stack, err
		}
		countInt := int(count)
		if countInt < 0 || countInt > len(stack) {
			return stack, fmt.Errorf("PDF calculator stack count is invalid")
		}
		start := len(stack) - countInt
		return append(stack, append([]pdfTintOperand(nil), stack[start:]...)...), nil
	case "index":
		index, err := popNumber()
		if err != nil {
			return stack, err
		}
		indexInt := int(index)
		if indexInt < 0 || indexInt >= len(stack) {
			return stack, fmt.Errorf("PDF calculator index is outside stack")
		}
		return push(stack[len(stack)-1-indexInt])
	case "roll":
		amountValue, err := popNumber()
		if err != nil {
			return stack, err
		}
		countValue, err := popNumber()
		if err != nil {
			return stack, err
		}
		count := int(countValue)
		amount := int(amountValue)
		if count < 0 || count > len(stack) {
			return stack, fmt.Errorf("PDF calculator roll count is invalid")
		}
		start := len(stack) - count
		segment := append([]pdfTintOperand(nil), stack[start:]...)
		if len(segment) > 0 {
			amount %= len(segment)
			if amount < 0 {
				amount += len(segment)
			}
			rotated := append(append([]pdfTintOperand(nil), segment[len(segment)-amount:]...), segment[:len(segment)-amount]...)
			copy(stack[start:], rotated)
		}
		return stack, nil
	case "and", "or", "xor":
		right, err := pop()
		if err != nil {
			return stack, err
		}
		left, err := pop()
		if err != nil {
			return stack, err
		}
		if left.kind == pdfTintBooleanOperand && right.kind == pdfTintBooleanOperand {
			var value bool
			switch operator {
			case "and":
				value = left.boolean && right.boolean
			case "or":
				value = left.boolean || right.boolean
			default:
				value = left.boolean != right.boolean
			}
			return pushBoolean(value)
		}
		if left.kind != pdfTintNumberOperand || right.kind != pdfTintNumberOperand {
			return stack, fmt.Errorf("PDF calculator logical operands have wrong types")
		}
		leftValue, rightValue := uint64(left.number), uint64(right.number)
		var value uint64
		switch operator {
		case "and":
			value = leftValue & rightValue
		case "or":
			value = leftValue | rightValue
		default:
			value = leftValue ^ rightValue
		}
		return pushNumber(float64(value))
	case "eq", "ne", "gt", "ge", "lt", "le":
		right, err := popNumber()
		if err != nil {
			return stack, err
		}
		left, err := popNumber()
		if err != nil {
			return stack, err
		}
		var value bool
		switch operator {
		case "eq":
			value = left == right
		case "ne":
			value = left != right
		case "gt":
			value = left > right
		case "ge":
			value = left >= right
		case "lt":
			value = left < right
		default:
			value = left <= right
		}
		return pushBoolean(value)
	case "if", "ifelse":
		var falseProcedure pdfTintOperand
		if operator == "ifelse" {
			var err error
			falseProcedure, err = pop()
			if err != nil {
				return stack, err
			}
		}
		trueProcedure, err := pop()
		if err != nil {
			return stack, err
		}
		condition, err := pop()
		if err != nil {
			return stack, err
		}
		if condition.kind != pdfTintBooleanOperand || trueProcedure.kind != pdfTintProcedureOperand {
			return stack, fmt.Errorf("PDF calculator conditional operands have wrong types")
		}
		procedure := trueProcedure.procedure
		if operator == "ifelse" {
			if falseProcedure.kind != pdfTintProcedureOperand {
				return stack, fmt.Errorf("PDF calculator conditional operands have wrong types")
			}
			if !condition.boolean {
				procedure = falseProcedure.procedure
			}
		} else if !condition.boolean {
			return stack, nil
		}
		return pdfTintExecute(procedure, stack, steps)
	default:
		return stack, fmt.Errorf("unsupported PDF calculator operation %q", operator)
	}
}

func pdfClampFloat(value, low, high float64) float64 {
	if high < low {
		low, high = high, low
	}
	return math.Min(high, math.Max(low, value))
}
