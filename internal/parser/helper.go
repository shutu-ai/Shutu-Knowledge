package parser

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// HelperRunner runs an optional external runtime (OCR, legacy office
// converters, MinerU-style processors). Knowledge owns the dependency and
// its health; the Agent never sees it.
type HelperRunner interface {
	// Available reports whether the helper can run right now.
	Available() bool
	// Run converts input bytes into text. format is a format hint (file
	// extension without dot, e.g. "doc").
	Run(ctx context.Context, format string, input []byte) (string, error)
}

// ExecHelper runs a command template: words may contain {input} (replaced by
// a temp file carrying the bytes) and {format}. Without {input} the temp file
// path is appended. stdout is the converted text.
type ExecHelper struct {
	// Template is the command line, shell-style split (e.g.
	// "anydoc {input} {format}").
	Template string
	// Timeout bounds one conversion (default 120s).
	TimeoutMS int
}

// maxHelperOutputBytes keeps an optional process from forcing Knowledge to
// buffer an unbounded response. Binary image output and text output share it.
const maxHelperOutputBytes = 64 << 20

type boundedBuffer struct {
	limit   int
	current *bytes.Buffer
}

func (buffer *boundedBuffer) Write(data []byte) (int, error) {
	if buffer.current == nil {
		buffer.current = bytes.NewBuffer(make([]byte, 0, min(len(data), 64<<10)))
	}
	room := buffer.limit - buffer.current.Len()
	if room <= 0 {
		return 0, fmt.Errorf("helper output exceeds %d bytes", buffer.limit)
	}
	if len(data) > room {
		buffer.current.Write(data[:room])
		return room, fmt.Errorf("helper output exceeds %d bytes", buffer.limit)
	}
	return buffer.current.Write(data)
}

// Available resolves the command and reports executability.
func (h ExecHelper) Available() bool {
	words, ok := h.words()
	if !ok || len(words) == 0 {
		return false
	}
	_, err := exec.LookPath(words[0])
	return err == nil
}

func (h ExecHelper) words() ([]string, bool) {
	return splitCommand(h.Template)
}

// splitCommand supports quoted executables and arguments. Optional helper
// commands are frequently installed under platform-specific paths that
// contain spaces; those paths cannot be made usable by strings.Fields.
func splitCommand(template string) ([]string, bool) {
	template = strings.TrimSpace(template)
	if template == "" {
		return nil, false
	}
	var words []string
	var word strings.Builder
	var quote rune
	flush := func() {
		if word.Len() > 0 {
			words = append(words, word.String())
			word.Reset()
		}
	}
	for _, char := range template {
		switch {
		case quote != 0:
			if char == quote {
				quote = 0
				continue
			}
			word.WriteRune(char)
		case char == '"' || char == '\'':
			quote = char
		case char == ' ' || char == '\t' || char == '\n' || char == '\r':
			flush()
		default:
			word.WriteRune(char)
		}
	}
	if quote != 0 {
		return nil, false
	}
	flush()
	return words, len(words) > 0
}

// Run executes the helper. A non-zero exit or empty output is an error so
// callers can fall back to local parsing.
// Decode runs a helper that writes binary (normally PNG) data to stdout.
// Input is a self-contained envelope; callers must impose their own upstream
// byte bounds. Decode is used by optional image codecs, not text converters.
func (h ExecHelper) Decode(ctx context.Context, format string, input []byte) ([]byte, error) {
	words, ok := h.words()
	return h.execute(ctx, format, input, words, ok, maxHelperOutputBytes)
}

// DecodeLimit is Decode with an explicit response cap. Long-running renderers
// may return many PNG pages; callers must choose a limit consistent with their
// downstream decoded-pixel budget.
func (h ExecHelper) DecodeLimit(ctx context.Context, format string, input []byte, limit int) ([]byte, error) {
	words, ok := h.words()
	return h.execute(ctx, format, input, words, ok, limit)
}

// Run executes the helper. A non-zero exit or empty output is an error so
// callers can fall back to local parsing.
func (h ExecHelper) Run(ctx context.Context, format string, input []byte) (string, error) {
	words, ok := h.words()
	if !ok || len(words) == 0 {
		return "", fmt.Errorf("helper command is not configured")
	}
	output, err := h.execute(ctx, format, input, words, ok, maxHelperOutputBytes)
	if err != nil {
		return "", err
	}
	text := strings.TrimSpace(string(output))
	if text == "" {
		return "", fmt.Errorf("helper produced no text")
	}
	return text, nil
}

func (h ExecHelper) execute(ctx context.Context, format string, input []byte, words []string, ok bool, outputLimit int) ([]byte, error) {
	if !ok || len(words) == 0 {
		return nil, fmt.Errorf("helper command is not configured")
	}
	if !h.Available() {
		return nil, fmt.Errorf("helper command %q is not available", words[0])
	}
	tmp, err := os.CreateTemp("", "shutu-knowledge-helper-*."+format)
	if err != nil {
		return nil, fmt.Errorf("helper temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(input); err != nil {
		_ = tmp.Close()
		return nil, fmt.Errorf("helper temp write: %w", err)
	}
	_ = tmp.Close()

	timeout := h.TimeoutMS
	if timeout <= 0 {
		timeout = 120_000
	}
	callCtx, cancel := context.WithTimeout(ctx, timeDuration(timeout))
	defer cancel()
	args := make([]string, 0, len(words))
	sawInput := false
	for _, word := range words[1:] {
		switch {
		case strings.Contains(word, "{input}"):
			word = strings.ReplaceAll(word, "{input}", tmpPath)
			sawInput = true
		case strings.Contains(word, "{format}"):
			word = strings.ReplaceAll(word, "{format}", format)
		}
		args = append(args, word)
	}
	if !sawInput {
		args = append(args, tmpPath)
	}
	cmd := exec.CommandContext(callCtx, words[0], args...)
	var stdout, stderr boundedBuffer
	stdout.limit = outputLimit
	stderr.limit = 16 << 10
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	if err != nil {
		message := strings.TrimSpace(stderr.current.String())
		if message != "" {
			return nil, fmt.Errorf("helper failed: %w: %s", err, message)
		}
		return nil, fmt.Errorf("helper failed: %w", err)
	}
	if stdout.current == nil || stdout.current.Len() == 0 {
		return nil, fmt.Errorf("helper produced no output")
	}
	return stdout.current.Bytes(), nil
}

// NoopHelper is always unavailable (tests, disabled config).
type NoopHelper struct{}

func (NoopHelper) Available() bool { return false }
func (NoopHelper) Run(context.Context, string, []byte) (string, error) {
	return "", fmt.Errorf("helper disabled")
}

// FallbackHelper tries the primary runtime (typically PaddleOCR) and then a
// secondary command (typically Tesseract). A missing fallback is not an error;
// the primary failure remains the actionable result.
type FallbackHelper struct {
	Primary  HelperRunner
	Fallback HelperRunner
}

func (h FallbackHelper) Available() bool {
	return (h.Primary != nil && h.Primary.Available()) ||
		(h.Fallback != nil && h.Fallback.Available())
}

func (h FallbackHelper) Run(ctx context.Context, format string, input []byte) (string, error) {
	var primaryErr error
	if h.Primary != nil && h.Primary.Available() {
		text, err := h.Primary.Run(ctx, format, input)
		if err == nil {
			return text, nil
		}
		primaryErr = err
	} else {
		primaryErr = fmt.Errorf("primary OCR runtime is unavailable")
	}
	if h.Fallback == nil || !h.Fallback.Available() {
		return "", fmt.Errorf("%w; no OCR fallback is available", primaryErr)
	}
	text, fallbackErr := h.Fallback.Run(ctx, format, input)
	if fallbackErr != nil {
		return "", fmt.Errorf("%w; OCR fallback failed: %w", primaryErr, fallbackErr)
	}
	return text, nil
}
