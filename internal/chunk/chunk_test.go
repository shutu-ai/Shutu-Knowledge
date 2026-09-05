package chunk

import (
	"strings"
	"testing"
)

func TestChunkHeadingPathAndFenceProtection(t *testing.T) {
	doc := "# Guide\n\nintro paragraph\n\n## Setup\n\nstep one\n\n```bash\n# not a heading\nblank lines\n\nstay together\n```\n\n## Usage\n\ncall it"
	pieces := Chunk(doc, 800, 100, Options{})
	if len(pieces) < 4 {
		t.Fatalf("expected blocks per heading, got %d: %+v", len(pieces), pieces)
	}
	if !strings.Contains(pieces[0].Heading, "Guide") {
		t.Fatalf("first heading: %q", pieces[0].Heading)
	}
	// The fenced "# not a heading" must stay inside one chunk, and the
	// heading path after the fence must be "Guide > Usage".
	var fenceChunk *Piece
	var usageChunk *Piece
	for i := range pieces {
		if strings.Contains(pieces[i].Text, "not a heading") {
			fenceChunk = &pieces[i]
		}
		if strings.Contains(pieces[i].Text, "call it") {
			usageChunk = &pieces[i]
		}
	}
	if fenceChunk == nil || usageChunk == nil {
		t.Fatalf("missing chunks: %+v", pieces)
	}
	if !strings.Contains(fenceChunk.Text, "stay together") {
		t.Fatalf("fence was split apart: %q", fenceChunk.Text)
	}
	if !strings.HasSuffix(usageChunk.Heading, "Usage") || !strings.Contains(usageChunk.Heading, "Guide") {
		t.Fatalf("usage heading path: %q", usageChunk.Heading)
	}
}

func TestChunkTokenBudgetsCJK(t *testing.T) {
	// 800-token budget on CJK text (~1.5 chars/token) -> ~1200-char windows.
	text := strings.Repeat("这是一段测试文本，用于验证分词预算。", 200)
	pieces := Chunk(text, 800, 100, Options{Smart: boolPtr(false)})
	if len(pieces) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(pieces))
	}
	for _, p := range pieces {
		if got := EstimateTokens(p.Text); got > 800+80 {
			t.Fatalf("chunk over token budget: %d tokens", got)
		}
	}
}

func TestChunkOverlapSharesText(t *testing.T) {
	text := strings.Repeat("sentence one. ", 300)
	pieces := Chunk(text, 200, 50, Options{Smart: boolPtr(false)})
	if len(pieces) < 3 {
		t.Fatalf("expected overlap windows, got %d", len(pieces))
	}
	suffix := pieces[0].Text
	suffix = suffix[maxInt(0, len(suffix)-40):]
	trimmedSuffix := strings.TrimRight(strings.TrimSpace(suffix), ".")
	if trimmedSuffix != "" && !strings.Contains(pieces[1].Text, trimmedSuffix) {
		t.Fatalf("expected overlap between consecutive chunks:\n%q\n%q", suffix, pieces[1].Text[:80])
	}
}

func TestRefineByTokenLimit(t *testing.T) {
	long := strings.Repeat("这是一个很长的句子，用来测试Token上限细分功能。", 60)
	pieces := RefineByTokenLimit([]Piece{{Text: long}}, 120, nil)
	if len(pieces) < 2 {
		t.Fatalf("expected refinement into multiple pieces, got %d", len(pieces))
	}
	for _, p := range pieces {
		if EstimateTokens(p.Text) > 120 {
			t.Fatalf("piece over limit: %d", EstimateTokens(p.Text))
		}
	}
}

func TestEstimateTokens(t *testing.T) {
	if got := EstimateTokens("hello world"); got < 2 || got > 4 {
		t.Fatalf("latin estimate: %d", got)
	}
	if got := EstimateTokens("你好世界再见再见再"); got < 4 || got > 6 {
		t.Fatalf("cjk estimate: %d", got)
	}
}

func TestHeadingStackReplacesDeeperLevels(t *testing.T) {
	doc := "## A\n\ntext a\n\n### B\n\ntext b\n\n## C\n\ntext c"
	pieces := Chunk(doc, 800, 100, Options{})
	var b, c *Piece
	for i := range pieces {
		switch {
		case strings.Contains(pieces[i].Text, "text b"):
			b = &pieces[i]
		case strings.Contains(pieces[i].Text, "text c"):
			c = &pieces[i]
		}
	}
	if b == nil || c == nil {
		t.Fatalf("missing pieces: %+v", pieces)
	}
	if b.Heading != "A > B" {
		t.Fatalf("b path: %q", b.Heading)
	}
	if c.Heading != "C" {
		t.Fatalf("c path (level reset expected): %q", c.Heading)
	}
}

func boolPtr(v bool) *bool { return &v }
