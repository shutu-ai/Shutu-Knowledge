package evidence

import (
	"strings"
)

var sentenceBoundary = func(r rune) bool {
	switch r {
	case '\n', '\r', '。', '！', '？', '；', '.', '!', '?', ';':
		return true
	}
	return false
}

const boundarySearchRadius = 96

// fitAnchorToBudget keeps the anchor; oversized anchors crop around the
// query focus at sentence boundaries.
func fitAnchorToBudget(anchor Excerpt, maxTokens int, focus string) Excerpt {
	if EstimateTokens(serializeExcerpt(anchor, true)) <= maxTokens {
		return anchor
	}
	return fitExcerptBySerializedBudget(anchor, maxTokens, "focus", true, focus)
}

// fitSide fills one side within its allowance, nearest chunk first; the
// first non-fitting excerpt is cropped (head side keeps tails, after side
// keeps heads) and the side ends there.
func fitSide(candidates []Excerpt, allowance int, side string) []Excerpt {
	if allowance <= 0 || len(candidates) == 0 {
		return nil
	}
	nearestFirst := candidates
	if side == "before" {
		nearestFirst = reversed(candidates)
	}
	var selected []Excerpt
	used := 0
	for _, candidate := range nearestFirst {
		available := allowance - used
		if available <= 0 {
			break
		}
		fullCost := EstimateTokens(serializeExcerpt(candidate, false))
		if fullCost <= available {
			selected = append(selected, candidate)
			used += fullCost
			continue
		}
		mode := "head"
		if side == "before" {
			mode = "tail"
		}
		fitted := fitExcerptBySerializedBudget(candidate, available, mode, false, "")
		if fitted.Text != "" {
			selected = append(selected, fitted)
		}
		break
	}
	if side == "before" {
		return reversed(selected)
	}
	return selected
}

func reversed(in []Excerpt) []Excerpt {
	out := make([]Excerpt, len(in))
	for i, v := range in {
		out[len(in)-1-i] = v
	}
	return out
}

func estimateSideTokens(excerpts []Excerpt) int {
	if len(excerpts) == 0 {
		return 0
	}
	parts := make([]string, 0, len(excerpts))
	for _, e := range excerpts {
		parts = append(parts, serializeExcerpt(e, false))
	}
	return EstimateTokens(strings.Join(parts, "\n\n"))
}

// fitExcerptBySerializedBudget binary-searches the longest crop whose
// serialized form fits the budget.
func fitExcerptBySerializedBudget(excerpt Excerpt, tokenBudget int, mode string, anchor bool, focus string) Excerpt {
	if tokenBudget <= 0 {
		return sliceExcerpt(excerpt, 0, 0)
	}
	if EstimateTokens(serializeExcerpt(excerpt, anchor)) <= tokenBudget {
		return excerpt
	}
	// An untrusted heading can itself consume the budget; drop it from this
	// bounded excerpt when metadata would eat everything (the canonical hit
	// retains the heading).
	source := excerpt
	empty := sliceExcerpt(excerpt, 0, 0)
	if excerpt.Heading != "" && EstimateTokens(serializeExcerpt(empty, anchor)) >= tokenBudget {
		withoutHeading := excerpt
		withoutHeading.Heading = ""
		source = withoutHeading
	}
	low, high := 0, len(source.Text)
	best := sliceExcerpt(source, 0, 0)
	for low <= high {
		length := (low + high) / 2
		candidate := cropExcerpt(source, length, mode, focus)
		if EstimateTokens(serializeExcerpt(candidate, anchor)) <= tokenBudget {
			best = candidate
			low = length + 1
		} else {
			high = length - 1
		}
	}
	return best
}

// cropExcerpt slices to `length` characters: head keeps the opening, tail
// keeps the ending, focus centres on the query/identifier range; cuts land
// on sentence boundaries where possible.
func cropExcerpt(excerpt Excerpt, length int, mode string, focus string) Excerpt {
	if length <= 0 {
		return sliceExcerpt(excerpt, 0, 0)
	}
	if length >= len(excerpt.Text) {
		return excerpt
	}
	switch mode {
	case "head":
		return sliceExcerpt(excerpt, 0, sentenceEnd(excerpt.Text, length))
	case "tail":
		return sliceExcerpt(excerpt, sentenceStart(excerpt.Text, len(excerpt.Text)-length), len(excerpt.Text))
	}
	rng, ok := focusRange(excerpt.Text, focus)
	if !ok {
		// Missing focus degrades to the opening instead of an arbitrary
		// middle slice (openings carry the definitions).
		return sliceExcerpt(excerpt, 0, sentenceEnd(excerpt.Text, length))
	}
	centre := (rng.start + rng.end) / 2
	start := maxInt(0, minInt(len(excerpt.Text)-length, centre-length/2))
	end := minInt(len(excerpt.Text), start+length)
	start = sentenceStart(excerpt.Text, start)
	if end-start > length {
		start = end - length
	}
	end = sentenceEnd(excerpt.Text, minInt(len(excerpt.Text), start+length))
	if end-start > length {
		end = start + length
	}
	if end-start > length {
		excess := end - start - length
		trimLeft := minInt(excess, maxInt(0, rng.start-start))
		start += trimLeft
		end -= excess - trimLeft
	}
	return sliceExcerpt(excerpt, start, end)
}

type textRange struct{ start, end int }

var genericFocusWords = map[string]bool{
	"请问": true, "什么": true, "如何": true, "怎么": true, "这个": true, "那个": true,
	"please": true, "what": true, "how": true, "this": true, "that": true,
}

// focusRange locates the focus text, falling back to the longest meaningful
// component (identifier, word, or CJK bigram) so generic prompt words never
// steal the centre.
func focusRange(text, focus string) (textRange, bool) {
	needle := strings.TrimSpace(focus)
	if needle == "" {
		return textRange{}, false
	}
	lowerText := strings.ToLower(text)
	lowerNeedle := strings.ToLower(needle)
	if exact := strings.Index(lowerText, lowerNeedle); exact >= 0 {
		return textRange{exact, exact + len(needle)}, true
	}
	best := ""
	for _, component := range focusComponents(lowerNeedle) {
		if genericFocusWords[component] || len([]rune(component)) < 2 {
			continue
		}
		if len(component) > len(best) {
			if idx := strings.Index(lowerText, component); idx >= 0 {
				best = component
				_ = idx
			}
		}
	}
	if best != "" {
		if idx := strings.Index(lowerText, best); idx >= 0 {
			return textRange{idx, idx + len(best)}, true
		}
	}
	return textRange{}, false
}

// focusComponents yields space-separated words plus CJK bigrams.
func focusComponents(needle string) []string {
	var out []string
	current := strings.Builder{}
	flush := func() {
		if current.Len() > 0 {
			out = append(out, current.String())
			current.Reset()
		}
	}
	for _, r := range needle {
		if r == ' ' || r == '\t' {
			flush()
			continue
		}
		current.WriteRune(r)
	}
	flush()
	// CJK bigrams from pure-CJK components.
	var withBigrams []string
	for _, component := range out {
		withBigrams = append(withBigrams, component)
		runes := []rune(component)
		if isMostlyCJK(runes) {
			for i := 0; i+1 < len(runes); i++ {
				withBigrams = append(withBigrams, string(runes[i:i+2]))
			}
		}
	}
	return withBigrams
}

func isMostlyCJK(runes []rune) bool {
	if len(runes) < 2 {
		return false
	}
	for _, r := range runes {
		if !sentenceBoundary(r) && (r < 0x3400 || r > 0x9fff) {
			return false
		}
	}
	return true
}

// sentenceStart walks back at most 96 chars to the first boundary.
func sentenceStart(text string, target int) int {
	floor := maxInt(0, target-boundarySearchRadius)
	index := minInt(target-1, len(text)-1)
	for ; index >= floor; index-- {
		r := []rune(text[index : index+1])[0]
		if sentenceBoundary(r) {
			return index + 1
		}
	}
	return target
}

// sentenceEnd walks forward at most 96 chars to the first boundary.
func sentenceEnd(text string, target int) int {
	ceiling := minInt(len(text), target+boundarySearchRadius)
	for index := maxInt(0, target); index < ceiling; index++ {
		r := []rune(text[index : index+1])[0]
		if sentenceBoundary(r) {
			return index + 1
		}
	}
	return target
}

func sliceExcerpt(excerpt Excerpt, start, end int) Excerpt {
	safeStart := maxInt(0, minInt(len(excerpt.Text), start))
	safeEnd := maxInt(safeStart, minInt(len(excerpt.Text), end))
	out := excerpt
	out.Text = excerpt.Text[safeStart:safeEnd]
	out.TextStart = excerpt.TextStart + safeStart
	out.TextEnd = excerpt.TextStart + safeEnd
	out.TruncatedStart = out.TruncatedStart || safeStart > 0
	out.TruncatedEnd = out.TruncatedEnd || safeEnd < len(excerpt.Text)
	return out
}

// removeAdjacentOverlap trims the >= 24-char exact suffix/prefix overlap
// between adjacent excerpts (chunk overlap must not be read twice): earlier
// chunks yield suffixes to the chunk closer to the anchor; later chunks lose
// duplicate prefixes.
func removeAdjacentOverlap(before []Excerpt, anchor Excerpt, after []Excerpt) ([]Excerpt, []Excerpt) {
	nextBefore := make([]Excerpt, len(before))
	copy(nextBefore, before)
	for i := 0; i+1 < len(nextBefore); i++ {
		if overlap := longestSuffixPrefix(nextBefore[i].Text, nextBefore[i+1].Text); overlap >= MinOverlapChars {
			nextBefore[i] = sliceExcerpt(nextBefore[i], 0, len(nextBefore[i].Text)-overlap)
		}
	}
	if len(nextBefore) > 0 {
		last := nextBefore[len(nextBefore)-1]
		if overlap := longestSuffixPrefix(last.Text, anchor.Text); overlap >= MinOverlapChars {
			nextBefore[len(nextBefore)-1] = sliceExcerpt(last, 0, len(last.Text)-overlap)
		}
	}
	nextAfter := make([]Excerpt, len(after))
	copy(nextAfter, after)
	previous := anchor
	for i := range nextAfter {
		if overlap := longestSuffixPrefix(previous.Text, nextAfter[i].Text); overlap >= MinOverlapChars {
			nextAfter[i] = sliceExcerpt(nextAfter[i], overlap, len(nextAfter[i].Text))
		}
		previous = nextAfter[i]
	}
	return nonEmpty(nextBefore), nonEmpty(nextAfter)
}

func nonEmpty(in []Excerpt) []Excerpt {
	out := in[:0]
	for _, e := range in {
		if e.Text != "" {
			out = append(out, e)
		}
	}
	return out
}

func longestSuffixPrefix(left, right string) int {
	maxLen := minInt(len(left), len(right))
	for length := maxLen; length >= MinOverlapChars; length-- {
		if left[len(left)-length:] == right[:length] {
			return length
		}
	}
	return 0
}

// enforceSerializedBudget shrinks the farthest excerpt first, dropping it
// once it cannot shrink further; the anchor is refit last.
func enforceSerializedBudget(window Window, maxTokens int, focus string) Window {
	before := append([]Excerpt{}, window.Before...)
	after := append([]Excerpt{}, window.After...)
	anchorExcerpt := window.Anchor
	next := window
	for next.EstimatedTokens > maxTokens && (len(before) > 0 || len(after) > 0) {
		beforeDistance := -1
		if len(before) > 0 {
			beforeDistance = next.AnchorIndex - before[0].Index
		}
		afterDistance := -1
		if len(after) > 0 {
			afterDistance = after[len(after)-1].Index - next.AnchorIndex
		}
		excess := next.EstimatedTokens - maxTokens
		if afterDistance >= beforeDistance {
			index := len(after) - 1
			excerpt := after[index]
			cost := EstimateTokens(serializeExcerpt(excerpt, false))
			fitted := fitExcerptBySerializedBudget(excerpt, maxInt(0, cost-excess-1), "head", false, "")
			if fitted.Text == "" || len(fitted.Text) >= len(excerpt.Text) {
				after = after[:index]
			} else {
				after[index] = fitted
			}
		} else {
			excerpt := before[0]
			cost := EstimateTokens(serializeExcerpt(excerpt, false))
			fitted := fitExcerptBySerializedBudget(excerpt, maxInt(0, cost-excess-1), "tail", false, "")
			if fitted.Text == "" || len(fitted.Text) >= len(excerpt.Text) {
				before = before[1:]
			} else {
				before[0] = fitted
			}
		}
		next = makeWindowRaw(window.AnchorChunkID, window.AnchorIndex, before, anchorExcerpt, after, window.HasMoreBefore, window.HasMoreAfter)
	}
	if next.EstimatedTokens > maxTokens {
		anchorExcerpt = fitExcerptBySerializedBudget(anchorExcerpt, maxTokens, "focus", true, focus)
		next = makeWindowRaw(window.AnchorChunkID, window.AnchorIndex, nil, anchorExcerpt, nil, window.HasMoreBefore, window.HasMoreAfter)
	}
	return next
}

func makeWindowRaw(anchorID string, anchorIndex int, before []Excerpt, anchor Excerpt, after []Excerpt, hasMoreBefore, hasMoreAfter bool) Window {
	draft := Window{
		AnchorChunkID: anchorID,
		AnchorIndex:   anchorIndex,
		Before:        before,
		Anchor:        anchor,
		After:         after,
		HasMoreBefore: hasMoreBefore,
		HasMoreAfter:  hasMoreAfter,
	}
	draft.EstimatedTokens = EstimateTokens(Serialize(draft))
	return draft
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
