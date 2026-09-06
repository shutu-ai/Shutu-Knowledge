package extension

import (
	"regexp"
	"strings"
)

var autoIdentifierToken = regexp.MustCompile(`[A-Za-z0-9][A-Za-z0-9._:/-]*[A-Za-z0-9]`)
var autoNumericToken = regexp.MustCompile(`\d{6,32}`)

var autoStopwords = map[string]bool{
	"a": true, "an": true, "the": true, "to": true, "of": true, "in": true, "on": true,
	"at": true, "for": true, "and": true, "or": true, "is": true, "are": true,
	"be": true, "it": true, "this": true, "that": true, "with": true, "from": true,
	"what": true, "how": true, "why": true, "when": true, "where": true, "which": true,
	"can": true, "should": true, "please": true, "help": true, "find": true,
	"search": true, "explain": true, "me": true, "you": true, "my": true,
	"请问": true, "帮我": true, "一下": true, "知道": true, "我想": true,
	"这个": true, "那个": true, "看看": true,
}

var autoGenericBigrams = map[string]bool{
	"什么": true, "是什": true, "怎么": true, "如何": true, "这个": true,
	"那个": true, "一下": true, "请问": true, "帮我": true, "知道": true,
	"好的": true, "收到": true, "谢谢": true, "哈哈": true, "是的": true,
}

var autoPronounParticles = []string{"那", "它", "呢", "这个", "那个", "上述"}

func autoSegmentFunction(r rune) bool {
	isWord := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
	isCJK := r >= 0x4e00 && r <= 0x9fff
	return !(isWord || isCJK)
}

func retrieveKeywords(text string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(value string) {
		if value != "" && !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	for _, segment := range strings.FieldsFunc(text, autoSegmentFunction) {
		if isASCIIToken(segment) {
			lower := strings.ToLower(segment)
			if len(lower) >= 2 && !autoStopwords[lower] {
				add(lower)
			}
			continue
		}
		runes := []rune(segment)
		for index := 0; index+1 < len(runes); index++ {
			add(string(runes[index : index+2]))
		}
	}
	return out
}

func isASCIIToken(value string) bool {
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			continue
		}
		return false
	}
	return value != ""
}

func topicKeywords(keywords []string) []string {
	out := make([]string, 0, len(keywords))
	for _, keyword := range keywords {
		if !autoGenericBigrams[keyword] {
			out = append(out, keyword)
		}
	}
	return out
}

func cleanAutoQuery(text string) string {
	var parts []string
	for _, segment := range strings.FieldsFunc(text, autoSegmentFunction) {
		if isASCIIToken(segment) {
			lower := strings.ToLower(segment)
			if len(lower) >= 2 && !autoStopwords[lower] {
				parts = append(parts, lower)
			}
		} else {
			cleaned := segment
			for _, particle := range autoPronounParticles {
				cleaned = strings.ReplaceAll(cleaned, particle, "")
			}
			if cleaned != "" {
				parts = append(parts, cleaned)
			}
		}
	}
	for _, identifier := range extractStrictIdentifiers(text) {
		found := false
		for _, part := range parts {
			if strings.EqualFold(part, identifier) {
				found = true
				break
			}
		}
		if !found {
			parts = append(parts, identifier)
		}
	}
	return boundQueryChars(strings.Join(parts, " "), autoQueryChars)
}

func extractStrictIdentifiers(text string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(value string) {
		key := strings.ToLower(value)
		if !seen[key] {
			seen[key] = true
			out = append(out, value)
		}
	}
	for _, loc := range autoNumericToken.FindAllStringIndex(text, -1) {
		value := text[loc[0]:loc[1]]
		if identifierBoundary(text, loc[0]-1) && identifierBoundary(text, loc[1]) {
			add(value)
		}
	}
	for _, value := range autoIdentifierToken.FindAllString(text, -1) {
		if len(value) < 3 || len(value) > 64 || !strings.ContainsFunc(value, func(r rune) bool { return r >= '0' && r <= '9' }) {
			continue
		}
		lower := strings.ToLower(value)
		if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "ftp://") || strings.HasPrefix(lower, "www.") {
			continue
		}
		add(value)
	}
	return out
}

func identifierBoundary(text string, index int) bool {
	if index < 0 || index >= len(text) {
		return true
	}
	r := rune(text[index])
	return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_')
}

func containsStrictIdentifier(text, identifier string) bool {
	haystack := strings.ToLower(text)
	needle := strings.ToLower(identifier)
	for start := 0; ; {
		index := strings.Index(haystack[start:], needle)
		if index < 0 {
			return false
		}
		index += start
		if identifierBoundary(haystack, index-1) && identifierBoundary(haystack, index+len(needle)) {
			return true
		}
		start = index + 1
	}
}

func sharesKeywords(query, content string, keywords []string) bool {
	if len(keywords) == 0 {
		return true
	}
	haystack := strings.ToLower(content)
	for _, keyword := range keywords {
		if strings.Contains(haystack, keyword) {
			return true
		}
	}
	return false
}

func boundQueryChars(text string, maxChars int) string {
	text = strings.TrimSpace(text)
	runes := []rune(text)
	if len(runes) <= maxChars {
		return text
	}
	if maxChars <= 2 {
		return string(runes[:maxChars])
	}
	head := maxChars * 6 / 10
	if head > 120 {
		head = 120
	}
	if head >= maxChars-1 {
		return string(runes[:maxChars])
	}
	tail := maxChars - head - 1
	return string(runes[:head]) + " " + string(runes[len(runes)-tail:])
}
