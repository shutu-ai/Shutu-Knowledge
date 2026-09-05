// Package evidence composes ordered, token-bounded context windows around a
// retrieved anchor chunk: before -> anchor -> after, heading-bounded by
// default, with adjacent-overlap deduplication and sentence-boundary
// cropping. The window describes what the model sees for THIS query; the
// canonical chunk text is never mutated.
package evidence

import (
	"sort"
	"strings"

	"github.com/shutu-ai/shutu-knowledge/internal/chunk"
)

// Defaults (audited reference values).
const (
	DefaultContextTokens = 768
	DefaultNeighbours    = 1
	MinOverlapChars      = 24
)

// Chunk is the minimal view the composer needs.
type Chunk interface {
	EvidenceID() string
	EvidenceDocID() string
	EvidenceBaseID() string
	EvidenceIndex() int
	EvidenceText() string
	EvidenceHeading() string
}

// Excerpt is one bounded excerpt from a canonical chunk. Offsets are UTF-16
// style indices relative to the canonical chunk text.
type Excerpt struct {
	ChunkID        string `json:"chunkId"`
	Index          int    `json:"index"`
	Heading        string `json:"heading,omitempty"`
	Text           string `json:"text"`
	TextStart      int    `json:"textStart"`
	TextEnd        int    `json:"textEnd"`
	TruncatedStart bool   `json:"truncatedStart"`
	TruncatedEnd   bool   `json:"truncatedEnd"`
}

// Window is the ordered evidence around one anchor.
type Window struct {
	AnchorChunkID   string    `json:"anchorChunkId"`
	AnchorIndex     int       `json:"anchorIndex"`
	Before          []Excerpt `json:"before"`
	Anchor          Excerpt   `json:"anchor"`
	After           []Excerpt `json:"after"`
	EstimatedTokens int       `json:"estimatedTokens"`
	HasMoreBefore   bool      `json:"hasMoreBefore"`
	HasMoreAfter    bool      `json:"hasMoreAfter"`
}

// Options controls one composition.
type Options struct {
	// Before/After: nil = default 1 neighbour per side; 0 disables a side.
	Before       *int
	After        *int
	MaxTokens    int
	Focus        string
	CrossHeading bool
	// DocumentChunkCount enables an exact HasMoreAfter when known.
	DocumentChunkCount int
	// Explicit availability hints override inference.
	HasMoreBefore *bool
	HasMoreAfter  *bool
}

// EstimateTokens uses the shared deterministic estimator.
func EstimateTokens(text string) int { return chunk.EstimateTokens(text) }

// Compose builds the deterministic window. chunks may be the whole document
// or a prefetched range; the anchor stays authoritative even when it was not
// part of the prefetched slice.
func Compose(chunks []Chunk, anchor Chunk, opts Options) Window {
	beforeLimit := clampSide(opts.Before, DefaultNeighbours)
	afterLimit := clampSide(opts.After, DefaultNeighbours)
	maxTokens := clampInt(opts.MaxTokens, 1, 1_000_000, DefaultContextTokens)

	byIndex := map[int]Chunk{}
	for _, c := range chunks {
		if c.EvidenceDocID() == anchor.EvidenceDocID() && c.EvidenceBaseID() == anchor.EvidenceBaseID() {
			byIndex[c.EvidenceIndex()] = c
		}
	}
	byIndex[anchor.EvidenceIndex()] = anchor
	ordered := make([]Chunk, 0, len(byIndex))
	for _, c := range byIndex {
		ordered = append(ordered, c)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].EvidenceIndex() != ordered[j].EvidenceIndex() {
			return ordered[i].EvidenceIndex() < ordered[j].EvidenceIndex()
		}
		return ordered[i].EvidenceID() < ordered[j].EvidenceID()
	})

	var earlier, later []Chunk
	for _, c := range ordered {
		switch {
		case c.EvidenceIndex() < anchor.EvidenceIndex():
			earlier = append(earlier, c)
		case c.EvidenceIndex() > anchor.EvidenceIndex():
			later = append(later, c)
		}
	}
	sameHeading := func(c Chunk) bool {
		return opts.CrossHeading || normalizeHeading(c.EvidenceHeading()) == normalizeHeading(anchor.EvidenceHeading())
	}
	eligibleBefore := takeWhile(contiguousTowardAnchor(earlier, anchor.EvidenceIndex(), -1), sameHeading)
	eligibleAfter := takeWhile(contiguousTowardAnchor(later, anchor.EvidenceIndex(), 1), sameHeading)

	var beforeChunks []Chunk
	for i := 0; i < beforeLimit && i < len(eligibleBefore); i++ {
		beforeChunks = append(beforeChunks, eligibleBefore[i])
	}
	// toReadingOrder: eligibleBefore is nearest-first; reverse for reading order.
	for i, j := 0, len(beforeChunks)-1; i < j; i, j = i+1, j-1 {
		beforeChunks[i], beforeChunks[j] = beforeChunks[j], beforeChunks[i]
	}
	afterChunks := eligibleAfter
	if len(afterChunks) > afterLimit {
		afterChunks = afterChunks[:afterLimit]
	}

	beforeExcerpts := excerptsOf(beforeChunks)
	afterExcerpts := excerptsOf(afterChunks)
	anchorExcerpt := excerptOf(anchor)
	anchorExcerpt = fitAnchorToBudget(anchorExcerpt, maxTokens, opts.Focus)
	beforeExcerpts, afterExcerpts = removeAdjacentOverlap(beforeExcerpts, anchorExcerpt, afterExcerpts)

	anchorOnly := makeWindow(anchor, nil, anchorExcerpt, nil, false, false)
	remaining := maxInt(0, maxTokens-anchorOnly.EstimatedTokens)
	beforeAllowance := remaining
	afterAllowance := remaining
	if len(afterExcerpts) > 0 && len(beforeExcerpts) > 0 {
		beforeAllowance = remaining / 2
		afterAllowance = remaining - beforeAllowance
	} else if len(afterExcerpts) > 0 {
		beforeAllowance = 0
	} else {
		afterAllowance = 0
	}
	fittedBefore := fitSide(beforeExcerpts, beforeAllowance, "before")
	fittedAfter := fitSide(afterExcerpts, afterAllowance, "after")

	// A short/absent side donates its unused allowance to the other side.
	usedBefore := estimateSideTokens(fittedBefore)
	if usedBefore < beforeAllowance && len(afterExcerpts) > 0 {
		fittedAfter = fitSide(afterExcerpts, afterAllowance+beforeAllowance-usedBefore, "after")
	}
	usedAfter := estimateSideTokens(fittedAfter)
	if usedAfter < afterAllowance && len(beforeExcerpts) > 0 {
		fittedBefore = fitSide(beforeExcerpts, beforeAllowance+afterAllowance-usedAfter, "before")
	}

	window := makeWindow(anchor, fittedBefore, anchorExcerpt, fittedAfter,
		boolValue(opts.HasMoreBefore), boolValue(opts.HasMoreAfter))
	window = enforceSerializedBudget(window, maxTokens, opts.Focus)

	first := window.Anchor
	if len(window.Before) > 0 {
		first = window.Before[0]
	}
	last := window.Anchor
	if len(window.After) > 0 {
		last = window.After[len(window.After)-1]
	}
	finalBefore := boolValue(opts.HasMoreBefore)
	if opts.HasMoreBefore == nil {
		finalBefore = first.TruncatedStart || first.Index > 0 || anyEarlier(earlier, first.Index)
	}
	finalAfter := boolValue(opts.HasMoreAfter)
	if opts.HasMoreAfter == nil {
		if opts.DocumentChunkCount > 0 {
			finalAfter = last.TruncatedEnd || last.Index < opts.DocumentChunkCount-1
		} else {
			finalAfter = last.TruncatedEnd || anyLater(later, last.Index)
		}
	}
	return makeWindow(anchor, window.Before, window.Anchor, window.After, finalBefore, finalAfter)
}

// Serialize renders the exact evidence in document order; ">>>" marks (but
// never moves) the canonical hit.
func Serialize(w Window) string {
	var parts []string
	for _, excerpt := range w.Before {
		parts = append(parts, serializeExcerpt(excerpt, false))
	}
	parts = append(parts, serializeExcerpt(w.Anchor, true))
	for _, excerpt := range w.After {
		parts = append(parts, serializeExcerpt(excerpt, false))
	}
	return strings.Join(parts, "\n\n")
}

func serializeExcerpt(e Excerpt, anchor bool) string {
	prefix := ""
	if anchor {
		prefix = ">>> "
	}
	if heading := strings.TrimSpace(e.Heading); heading != "" {
		prefix += "[" + heading + "] "
	}
	return prefix + e.Text
}

func excerptOf(c Chunk) Excerpt {
	return Excerpt{
		ChunkID:   c.EvidenceID(),
		Index:     c.EvidenceIndex(),
		Heading:   c.EvidenceHeading(),
		Text:      c.EvidenceText(),
		TextStart: 0,
		TextEnd:   len(c.EvidenceText()),
	}
}

func excerptsOf(chunks []Chunk) []Excerpt {
	out := make([]Excerpt, 0, len(chunks))
	for _, c := range chunks {
		out = append(out, excerptOf(c))
	}
	return out
}

func normalizeHeading(h string) string { return strings.TrimSpace(h) }

// contiguousTowardAnchor walks outward and stops at the first missing index,
// so a partial prefetch never silently bridges an unloaded chunk.
func contiguousTowardAnchor(chunks []Chunk, anchorIndex, direction int) []Chunk {
	byIndex := map[int]Chunk{}
	for _, c := range chunks {
		byIndex[c.EvidenceIndex()] = c
	}
	var out []Chunk
	for index := anchorIndex + direction; ; index += direction {
		c, ok := byIndex[index]
		if !ok {
			break
		}
		out = append(out, c)
	}
	return out
}

func takeWhile(values []Chunk, predicate func(Chunk) bool) []Chunk {
	var out []Chunk
	for _, v := range values {
		if !predicate(v) {
			break
		}
		out = append(out, v)
	}
	return out
}

func anyEarlier(earlier []Chunk, firstIndex int) bool {
	for _, c := range earlier {
		if c.EvidenceIndex() < firstIndex {
			return true
		}
	}
	return false
}

func anyLater(later []Chunk, lastIndex int) bool {
	for _, c := range later {
		if c.EvidenceIndex() > lastIndex {
			return true
		}
	}
	return false
}

func makeWindow(anchor Chunk, before []Excerpt, anchorExcerpt Excerpt, after []Excerpt, hasMoreBefore, hasMoreAfter bool) Window {
	draft := Window{
		AnchorChunkID: anchor.EvidenceID(),
		AnchorIndex:   anchor.EvidenceIndex(),
		Before:        before,
		Anchor:        anchorExcerpt,
		After:         after,
		HasMoreBefore: hasMoreBefore,
		HasMoreAfter:  hasMoreAfter,
	}
	draft.EstimatedTokens = EstimateTokens(Serialize(draft))
	return draft
}

func clampSide(v *int, fallback int) int {
	if v == nil {
		return fallback
	}
	if *v < 0 {
		return 0
	}
	if *v > 10 {
		return 10
	}
	return *v
}

func clampInt(v, lo, hi, fallback int) int {
	if v < lo {
		return fallback
	}
	if v > hi {
		return hi
	}
	return v
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func boolValue(p *bool) bool {
	return p != nil && *p
}
