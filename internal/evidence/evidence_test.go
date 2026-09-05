package evidence

import (
	"strings"
	"testing"
)

type testChunk struct {
	id      string
	docID   string
	baseID  string
	index   int
	text    string
	heading string
}

func (c testChunk) EvidenceID() string      { return c.id }
func (c testChunk) EvidenceDocID() string   { return c.docID }
func (c testChunk) EvidenceBaseID() string  { return c.baseID }
func (c testChunk) EvidenceIndex() int      { return c.index }
func (c testChunk) EvidenceText() string    { return c.text }
func (c testChunk) EvidenceHeading() string { return c.heading }

func chunkList(texts ...string) []Chunk {
	out := make([]Chunk, 0, len(texts))
	for i, text := range texts {
		out = append(out, testChunk{id: string(rune('a' + i)), docID: "doc", baseID: "base", index: i, text: text, heading: "H"})
	}
	return out
}

func TestComposeOrderAndSerialization(t *testing.T) {
	chunks := chunkList("before text", "anchor text here", "after text")
	anchor := chunks[1]
	window := Compose(chunks, anchor, Options{})
	serialized := Serialize(window)
	if !strings.Contains(serialized, ">>> [H] anchor text here") {
		t.Fatalf("anchor marker missing: %q", serialized)
	}
	if strings.Index(serialized, "before text") > strings.Index(serialized, "after text") {
		t.Fatalf("reading order broken: %q", serialized)
	}
	if window.EstimatedTokens != EstimateTokens(serialized) {
		t.Fatalf("estimated tokens mismatch: %d", window.EstimatedTokens)
	}
	if window.Anchor.ChunkID != anchor.EvidenceID() || window.AnchorIndex != 1 {
		t.Fatalf("anchor identity: %+v", window)
	}
}

func TestComposeHeadingBoundary(t *testing.T) {
	chunks := []Chunk{
		testChunk{id: "a", docID: "d", baseID: "b", index: 0, text: "same heading", heading: "Intro"},
		testChunk{id: "b", docID: "d", baseID: "b", index: 1, text: "anchor", heading: "Intro"},
		testChunk{id: "c", docID: "d", baseID: "b", index: 2, text: "different heading", heading: "Usage"},
	}
	window := Compose(chunks, chunks[1], Options{})
	if len(window.Before) != 1 {
		t.Fatalf("same-heading before expected: %+v", window)
	}
	if len(window.After) != 0 {
		t.Fatalf("cross-heading after must be excluded: %+v", window)
	}
	if !window.HasMoreAfter {
		t.Fatal("hasMoreAfter should be true after heading boundary")
	}
	// CrossHeading opts in.
	window = Compose(chunks, chunks[1], Options{CrossHeading: true})
	if len(window.After) != 1 {
		t.Fatalf("crossHeading after expected: %+v", window)
	}
}

func TestComposeContiguousOnly(t *testing.T) {
	// Missing chunk 0: chunk 1 is the anchor, so nothing before it is contiguous.
	chunks := []Chunk{
		testChunk{id: "x", docID: "d", baseID: "b", index: 0, text: "unloaded gap filler", heading: "H"},
		testChunk{id: "b", docID: "d", baseID: "b", index: 1, text: "anchor", heading: "H"},
		testChunk{id: "c", docID: "d", baseID: "b", index: 2, text: "after", heading: "H"},
	}
	// Prefetched list skips index 0 (simulating a partial range fetch).
	prefetched := []Chunk{chunks[1], chunks[2]}
	window := Compose(prefetched, chunks[1], Options{})
	if len(window.Before) != 0 || !window.HasMoreBefore {
		t.Fatalf("gap must not bridge: %+v", window)
	}
}

func TestComposeBudgetShrinks(t *testing.T) {
	long := strings.Repeat("这是一段比较长的中文文本，用来填充Token预算。", 60)
	chunks := chunkList(long, "锚点内容", long)
	window := Compose(chunks, chunks[1], Options{MaxTokens: 200})
	if window.EstimatedTokens > 200 {
		t.Fatalf("budget exceeded: %d", window.EstimatedTokens)
	}
	if window.Anchor.Text != "锚点内容" {
		t.Fatal("anchor text mutated")
	}
}

func TestComposeAnchorCropsAroundFocus(t *testing.T) {
	opening := strings.Repeat("开头铺垫内容。", 120)
	middle := "目标标识符 ALPHA-123 出现在这里。"
	ending := strings.Repeat("结尾补充说明。", 120)
	anchorText := opening + middle + ending
	chunks := []Chunk{testChunk{id: "a", docID: "d", baseID: "b", index: 0, text: anchorText, heading: "H"}}
	window := Compose(chunks, chunks[0], Options{MaxTokens: 120, Focus: "ALPHA-123"})
	if !strings.Contains(window.Anchor.Text, "ALPHA-123") {
		t.Fatalf("focus must survive cropping: %q", window.Anchor.Text[:120])
	}
	if window.Anchor.TruncatedStart != true || window.Anchor.TruncatedEnd != true {
		t.Fatalf("truncation flags: %+v", window.Anchor)
	}
	// Offsets map the excerpt back into the canonical chunk text.
	cropped := anchorText[window.Anchor.TextStart:window.Anchor.TextEnd]
	if cropped != window.Anchor.Text {
		t.Fatalf("offsets do not map to canonical text: %d..%d", window.Anchor.TextStart, window.Anchor.TextEnd)
	}
}

func TestComposeOverlapDedup(t *testing.T) {
	suffix := strings.Repeat("S", 40)
	before := "prefix-content-" + suffix
	anchor := suffix + "-anchor-body"
	after := anchor + "-and-more"
	chunks := []Chunk{
		testChunk{id: "a", docID: "d", baseID: "b", index: 0, text: before, heading: "H"},
		testChunk{id: "b", docID: "d", baseID: "b", index: 1, text: anchor, heading: "H"},
		testChunk{id: "c", docID: "d", baseID: "b", index: 2, text: after, heading: "H"},
	}
	window := Compose(chunks, chunks[1], Options{})
	serialized := Serialize(window)
	if strings.Count(serialized, suffix) > 3 {
		t.Fatalf("overlap duplicated: %q", serialized)
	}
	if strings.Contains(window.Before[0].Text, suffix) {
		t.Fatalf("before chunk should yield its duplicate suffix: %q", window.Before[0].Text)
	}
	if !strings.HasPrefix(window.After[0].Text, "-and-more") || strings.Contains(window.After[0].Text, "-anchor-body") {
		t.Fatalf("after chunk should lose duplicate prefix: %q", window.After[0].Text)
	}
}

func TestComposeExplicitHasMoreOverrides(t *testing.T) {
	chunks := chunkList("only")
	window := Compose(chunks, chunks[0], Options{})
	if window.HasMoreBefore || window.HasMoreAfter {
		t.Fatalf("no hints expected: %+v", window)
	}
	yes := true
	window = Compose(chunks, chunks[0], Options{HasMoreBefore: &yes, HasMoreAfter: &yes})
	if !window.HasMoreBefore || !window.HasMoreAfter {
		t.Fatalf("explicit hints lost: %+v", window)
	}
}
