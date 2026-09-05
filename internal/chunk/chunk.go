// Package chunk splits parsed documents into retrieval-ready chunks. The
// behavior mirrors the audited reference: token budgets (not character
// budgets), heading-aware smart splitting with scored break points, code
// fence protection, delimiter-only mode, and token-limit refinement.
package chunk

import (
	"math"
	"strings"
)

// Piece is one chunk: its text plus the markdown heading path introducing it.
type Piece struct {
	Text    string
	Heading string
}

// Options controls splitting.
type Options struct {
	// Smart selects heading/paragraph-aware splitting (default true).
	Smart *bool
	// Separator is the split boundary when Smart is false (default "\n\n").
	Separator string
}

// Normalize collapses line endings and excessive blank lines.
func Normalize(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	for strings.Contains(text, "\n\n\n") {
		text = strings.ReplaceAll(text, "\n\n\n", "\n\n")
	}
	return strings.TrimSpace(text)
}

// Chunk splits text into overlapping windows of at most `size` tokens with
// `overlap` tokens shared between consecutive chunks. Both budgets are token
// counts converted through the document's measured chars-per-token ratio, so
// CJK and Latin content produce comparable token-sized chunks.
func Chunk(text string, size, overlap int, opts Options) []Piece {
	normalized := Normalize(text)
	if normalized == "" {
		return nil
	}
	if size < 64 {
		size = 64
	}
	if overlap < 0 {
		overlap = 0
	}
	if overlap > size-1 {
		overlap = size - 1
	}
	cpt := CharsPerToken(normalized)
	safeSize := maxInt(64, int(math.Round(float64(size)*cpt)))
	safeOverlap := minInt(int(math.Round(float64(overlap)*cpt)), safeSize-1)

	smart := opts.Smart == nil || *opts.Smart
	var pieces []Piece
	if !smart {
		sep := NormalizeSeparator(opts.Separator)
		for _, part := range strings.Split(normalized, sep) {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			pieces = append(pieces, windowOrKeep(part, safeSize, safeOverlap, "")...)
		}
	} else {
		for _, b := range splitBlocks(normalized) {
			pieces = append(pieces, windowOrKeep(b.text, safeSize, safeOverlap, b.heading)...)
		}
	}
	if len(pieces) == 0 {
		cut := minInt(safeSize, len(normalized))
		pieces = []Piece{{Text: normalized[:cut]}}
	}
	return pieces
}

// CharsPerToken estimates characters per token: CJK-heavy text costs ~1.5
// chars/token, Latin ~4. Deterministic; mirrors the service and context layers.
func CharsPerToken(text string) float64 {
	tokens := EstimateTokens(text)
	if tokens <= 0 {
		return 1
	}
	return float64(len(text)) / float64(tokens)
}

// EstimateTokens estimates token count: ceil(cjk/1.5 + latin/4).
func EstimateTokens(text string) int {
	if text == "" {
		return 0
	}
	cjk := 0
	runes := 0
	for _, r := range text {
		runes++
		if isCJK(r) {
			cjk++
		}
	}
	latin := runes - cjk
	return maxInt(1, int(math.Ceil(float64(cjk)/1.5+float64(latin)/4.0)))
}

func isCJK(r rune) bool {
	switch {
	case r >= 0x3040 && r <= 0x30ff, // hiragana / katakana
		r >= 0x3400 && r <= 0x4dbf, // CJK ext A
		r >= 0x4e00 && r <= 0x9fff, // CJK unified
		r >= 0xac00 && r <= 0xd7af: // hangul
		return true
	}
	return false
}

// SemanticSegments splits text into paragraph-level candidate segments
// (heading-aware, never windowed) for semantic chunking in a later phase.
func SemanticSegments(text, separator string) []Piece {
	normalized := Normalize(text)
	if normalized == "" {
		return nil
	}
	blocks := splitBlocks(normalized)
	if separator == "" {
		out := make([]Piece, 0, len(blocks))
		for _, b := range blocks {
			out = append(out, Piece{Text: b.text, Heading: b.heading})
		}
		return out
	}
	sep := NormalizeSeparator(separator)
	var out []Piece
	for _, b := range blocks {
		for _, part := range strings.Split(b.text, sep) {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			out = append(out, Piece{Text: part, Heading: b.heading})
		}
	}
	return out
}

// RefineByTokenLimit splits any chunk whose estimated tokens exceed the
// limit at the nearest preferred boundary around the midpoint, recursively.
// A limit of 0 is a no-op.
func RefineByTokenLimit(pieces []Piece, tokenLimit int, estimate func(string) int) []Piece {
	if tokenLimit <= 0 {
		return pieces
	}
	if estimate == nil {
		estimate = EstimateTokens
	}
	var out []Piece
	var refine func(piece Piece)
	refine = func(piece Piece) {
		if estimate(piece.Text) <= tokenLimit || len(piece.Text) < 40 {
			out = append(out, piece)
			return
		}
		left, right, ok := splitAtPreferredBoundary(piece.Text)
		if !ok {
			out = append(out, piece)
			return
		}
		refine(Piece{Text: left, Heading: piece.Heading})
		refine(Piece{Text: right, Heading: piece.Heading})
	}
	for _, piece := range pieces {
		refine(piece)
	}
	return out
}

func windowOrKeep(blockText string, size, overlap int, heading string) []Piece {
	if len(blockText) <= size {
		return []Piece{{Text: blockText, Heading: heading}}
	}
	var out []Piece
	for _, window := range windowBlock(blockText, size, overlap) {
		out = append(out, Piece{Text: window, Heading: heading})
	}
	return out
}

// NormalizeSeparator lets users type literal \n / \t escape sequences.
func NormalizeSeparator(separator string) string {
	decoded := strings.ReplaceAll(separator, `\n`, "\n")
	decoded = strings.ReplaceAll(decoded, `\t`, "\t")
	if decoded == "" {
		return "\n\n"
	}
	return decoded
}
