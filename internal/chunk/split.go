package chunk

import (
	"math"
	"strings"
)

type block struct {
	text    string
	heading string
}

// splitBlocks splits into paragraph blocks while tracking the active
// markdown heading path and protecting code fences: blank lines and headings
// inside a fence never break the block, and a closer needs at least as many
// fence characters as the opener.
func splitBlocks(text string) []block {
	var blocks []block
	var current strings.Builder
	var headings []string // index i holds the level-(i+1) title
	fence := ""           // opener string while inside a fence

	flush := func() {
		trimmed := strings.TrimSpace(current.String())
		if trimmed != "" {
			blocks = append(blocks, block{text: trimmed, heading: headingPath(headings)})
		}
		current.Reset()
	}

	for _, line := range strings.Split(text, "\n") {
		if fence != "" {
			current.WriteString(line)
			current.WriteString("\n")
			if fenceClose(fence, line) {
				fence = ""
			}
			continue
		}
		if opener := fenceOpen(line); opener != "" {
			flush()
			fence = opener
			current.WriteString(line)
			current.WriteString("\n")
			continue
		}
		if level, title, ok := matchHeading(line); ok {
			flush()
			for len(headings) < level {
				headings = append(headings, "")
			}
			headings = headings[:level]
			headings[level-1] = title
			continue
		}
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		current.WriteString(line)
		current.WriteString("\n")
	}
	flush()
	return blocks
}

func headingPath(headings []string) string {
	var parts []string
	for _, h := range headings {
		if strings.TrimSpace(h) != "" {
			parts = append(parts, h)
		}
	}
	return strings.Join(parts, " > ")
}

// fenceOpen returns the fence opener (a run of >= 3 backticks or tildes) at
// line start, or "" when the line opens no fence.
func fenceOpen(line string) string {
	trimmed := strings.TrimLeft(line, " \t")
	if trimmed == "" {
		return ""
	}
	marker := trimmed[0]
	if marker != '`' && marker != '~' {
		return ""
	}
	count := 0
	for count < len(trimmed) && trimmed[count] == marker {
		count++
	}
	if count >= 3 {
		return strings.Repeat(string(marker), count)
	}
	return ""
}

// fenceClose reports whether line closes the given fence opener: a run of
// the same fence character at least as long as the opener (GitHub rule).
func fenceClose(fence, line string) bool {
	trimmed := strings.TrimLeft(line, " \t")
	trimmed = strings.TrimRight(trimmed, " \t\r")
	if len(trimmed) < len(fence) {
		return false
	}
	return strings.Trim(trimmed, fence[:1]) == ""
}

// matchHeading matches `#{1,6} title` and reports the heading level.
func matchHeading(line string) (level int, title string, ok bool) {
	trimmed := strings.TrimLeft(line, " \t")
	i := 0
	for i < len(trimmed) && trimmed[i] == '#' {
		i++
	}
	if i == 0 || i > 6 || i >= len(trimmed) || (trimmed[i] != ' ' && trimmed[i] != '\t') {
		return 0, "", false
	}
	return i, strings.TrimSpace(trimmed[i:]), true
}

// windowBlock windows one long block at the best scored break within the
// final 22% of the budget; scores decay toward the window end (0.7 factor).
func windowBlock(text string, size, overlap int) []string {
	var out []string
	start := 0
	for start < len(text) {
		end := start + size
		if end >= len(text) {
			out = append(out, strings.TrimSpace(text[start:]))
			break
		}
		windowStart := maxInt(start+1, end-maxInt(1, int(math.Round(float64(size)*0.22))))
		cut := maxInt(findCut(text, end, windowStart), start+1)
		out = append(out, strings.TrimSpace(text[start:cut]))
		next := maxInt(cut-overlap, start+1)
		if next <= start {
			break
		}
		start = next
	}
	return out
}

// Break-point scores: markdown boundaries ranked by structural quality, then
// blank lines, CJK sentence marks, list items, and bare newlines. The cut
// lands at the boundary; CJK sentence punctuation cuts after the mark.
var breakScores = map[string]float64{
	"h1": 100, "h2": 90, "h3": 80, "h4": 70, "h5": 60, "h6": 50,
	"fence": 80, "rule": 60,
	"paragraph": 20, "sentence": 8,
	"list": 5, "olist": 5, "newline": 1,
}

const decayFactor = 0.7

func findCut(text string, end, min int) int {
	windowSize := maxInt(1, end-min)
	bestPos := -1
	bestScore := -1.0
	consider := func(pos int, score float64) {
		if pos <= min || pos > end {
			return
		}
		decayed := score * math.Pow(decayFactor, float64(pos-min)/float64(windowSize))
		if decayed > bestScore {
			bestScore = decayed
			bestPos = pos
		}
	}

	// Line-start boundaries (headings, fences, rules, lists, blank lines).
	lineStart := min
	for lineStart <= end {
		nl := strings.IndexByte(text[minInt(lineStart, len(text)):minInt(end, len(text))], '\n')
		var line string
		var lineEnd int
		if nl < 0 {
			line = text[lineStart:minInt(end, len(text))]
			lineEnd = end
		} else {
			line = text[lineStart : lineStart+nl]
			lineEnd = lineStart + nl
		}
		trimmed := strings.TrimLeft(line, " \t")
		switch {
		case trimmed == "":
			consider(lineStart+nl+1, breakScores["paragraph"])
		default:
			if level, _, ok := matchHeading(trimmed); ok {
				consider(lineEnd, breakScores["h"+string(rune('0'+level))])
			} else if fenceOpen(trimmed) != "" {
				consider(lineEnd, breakScores["fence"])
			} else if isRule(trimmed) {
				consider(lineEnd, breakScores["rule"])
			} else if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") {
				consider(lineEnd, breakScores["list"])
			} else if isOrderedList(trimmed) {
				consider(lineEnd, breakScores["olist"])
			}
		}
		if nl < 0 {
			break
		}
		lineStart = lineEnd + 1
	}

	// CJK sentence punctuation anywhere in the window (cut after the mark).
	for i := min; i < end && i < len(text); {
		r, size := decodeRune(text[i:])
		if r == '。' || r == '！' || r == '？' {
			consider(i+size, breakScores["sentence"])
		}
		i += size
	}

	if bestPos > min {
		return bestPos
	}
	return end
}

func decodeRune(s string) (rune, int) {
	for i, r := range s {
		if i == 0 {
			return r, len(string(r))
		}
		break
	}
	return ' ', 1
}

func isRule(line string) bool {
	for _, marker := range []byte{'-', '*', '_'} {
		if len(line) >= 3 && strings.Trim(line, string(marker)) == "" {
			return true
		}
	}
	return false
}

func isOrderedList(line string) bool {
	i := 0
	for i < len(line) && line[i] >= '0' && line[i] <= '9' {
		i++
	}
	return i > 0 && i+1 < len(line) && line[i] == '.' && (line[i+1] == ' ' || line[i+1] == '\t')
}

// splitAtPreferredBoundary splits text at the last preferred boundary within
// +/-25% of the midpoint: blank line, CJK periods, comma, comma-space, space.
func splitAtPreferredBoundary(text string) (string, string, bool) {
	mid := len(text) / 2
	radius := maxInt(1, len(text)/4)
	lo := maxInt(0, mid-radius)
	hi := minInt(len(text), mid+radius)
	window := text[lo:hi]
	for _, sep := range []string{"\n\n", "。", "！", "？", "，", ", ", " "} {
		idx := strings.LastIndex(window, sep)
		if idx < 0 {
			continue
		}
		cut := lo + idx + len(sep)
		left := strings.TrimSpace(text[:cut])
		right := strings.TrimSpace(text[cut:])
		if left != "" && right != "" {
			return left, right, true
		}
	}
	return "", "", false
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
