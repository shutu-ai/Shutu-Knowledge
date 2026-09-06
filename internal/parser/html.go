package parser

import (
	"fmt"
	"strings"

	"golang.org/x/net/html"
)

// htmlParser strips page chrome and converts the body into structure
// preserving text (headings become markdown-style lines) so the heading
// aware chunker keeps document structure.
type htmlParser struct{}

func (htmlParser) Extensions() []string { return []string{"html", "htm"} }

func (htmlParser) Parse(_ string, data []byte) (Result, error) {
	title, text := HTMLToText(DecodeText(data))
	if strings.TrimSpace(text) == "" {
		return Result{}, fmt.Errorf("contains no extractable text")
	}
	return Result{Title: title, Text: text}, nil
}

var skipElements = map[string]bool{
	"script": true, "style": true, "head": true, "noscript": true,
	"nav": true, "footer": true, "aside": true, "form": true,
	"iframe": true, "svg": true, "canvas": true, "template": true,
}

var blockElements = map[string]bool{
	"p": true, "div": true, "section": true, "article": true,
	"blockquote": true, "tr": true, "table": true,
	"pre": true, "ul": true, "ol": true, "dl": true, "dd": true, "dt": true,
	"header": true, "main": true, "figure": true, "figcaption": true,
}

// headingLike marks inline headings already emitted by the walker.
var listItems = map[string]bool{"li": true, "dt": true, "dd": true}

// hasBlockChild reports whether any descendant (before leaf text) is itself
// a block-level element; containers with block children must descend so each
// child emits its own line instead of collapsing into one paragraph.
func hasBlockChild(node *html.Node) bool {
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.Type != html.ElementNode {
			continue
		}
		tag := strings.ToLower(child.Data)
		if blockElements[tag] || listItems[tag] || (strings.HasPrefix(tag, "h") && len(tag) == 2 && tag[1] >= '1' && tag[1] <= '6') {
			return true
		}
		if hasBlockChild(child) {
			return true
		}
	}
	return false
}

// HTMLToText converts an HTML document into (title, text).
func HTMLToText(source string) (string, string) {
	doc, err := html.Parse(strings.NewReader(source))
	if err != nil {
		return "", stripTagsFallback(source)
	}
	var title string
	var out strings.Builder
	var walk func(node *html.Node, inSkip bool)
	writeLine := func(text string) {
		text = strings.TrimSpace(text)
		if text != "" {
			out.WriteString(text)
			out.WriteString("\n\n")
		}
	}
	walk = func(node *html.Node, inSkip bool) {
		if node.Type == html.ElementNode {
			tag := strings.ToLower(node.Data)
			if tag == "head" {
				// Extract <title> only.
				for child := node.FirstChild; child != nil; child = child.NextSibling {
					if child.Type == html.ElementNode && strings.ToLower(child.Data) == "title" {
						title = strings.TrimSpace(child.FirstChild.Data)
					}
				}
				return
			}
			if skipElements[tag] {
				return
			}
			if tag == "br" || tag == "hr" {
				return
			}
			switch {
			case strings.HasPrefix(tag, "h") && len(tag) == 2 && tag[1] >= '1' && tag[1] <= '6':
				text := nodeText(node)
				writeLine(strings.Repeat("#", int(tag[1]-'0')) + " " + text)
				return
			case listItems[tag]:
				writeLine("- " + nodeText(node))
				return
			case blockElements[tag]:
				if !hasBlockChild(node) {
					// Emit children as one line followed by a separator.
					var inner strings.Builder
					collectInline(node, &inner)
					writeLine(inner.String())
					return
				}
				// Otherwise descend so each block child emits its own line.
			}
		}
		if node.Type == html.TextNode {
			text := strings.TrimSpace(node.Data)
			if text != "" {
				out.WriteString(text)
				out.WriteString("\n\n")
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child, inSkip)
		}
	}
	walk(doc, false)
	return decodeEntities(title), strings.TrimSpace(out.String())
}

func collectInline(node *html.Node, out *strings.Builder) {
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.TextNode {
			text := strings.Join(strings.Fields(child.Data), " ")
			if text != "" {
				if out.Len() > 0 && !strings.HasSuffix(out.String(), " ") && !strings.HasSuffix(out.String(), "\n") {
					out.WriteString(" ")
				}
				out.WriteString(text)
			}
		} else if child.Type == html.ElementNode && !skipElements[strings.ToLower(child.Data)] {
			collectInline(child, out)
		}
	}
}

func nodeText(node *html.Node) string {
	var out strings.Builder
	collectInline(node, &out)
	return out.String()
}

// stripTagsFallback is the regex-free last resort when the HTML is so broken
// that parsing fails entirely.
func stripTagsFallback(source string) string {
	var out strings.Builder
	depth := 0
	for _, r := range source {
		switch {
		case r == '<':
			depth++
		case r == '>':
			if depth > 0 {
				depth--
			}
		case depth == 0:
			out.WriteRune(r)
		}
	}
	return strings.TrimSpace(out.String())
}

// decodeEntities resolves the common named entities the reference decoder
// handled (the parser already resolves numeric references).
func decodeEntities(text string) string {
	replacer := strings.NewReplacer(
		"&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`, "&apos;", "'",
		"&nbsp;", " ", "&mdash;", "—", "&ndash;", "–", "&hellip;", "…",
		"&copy;", "©", "&reg;", "®", "&trade;", "™",
	)
	return replacer.Replace(text)
}
