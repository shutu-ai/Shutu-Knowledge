package safety

import (
	"regexp"
	"strings"
)

var (
	sensitiveKeyPattern = regexp.MustCompile(`(?i)(\b(?:api[_-]?key|access[_-]?token|authorization|password|passwd|secret|token)\b\s*[:=]\s*)(?:"[^"]*"|'[^']*'|[^\s,;]+)`)
	urlSecretPattern    = regexp.MustCompile(`(?i)([?&](?:api[_-]?key|access[_-]?token|authorization|password|passwd|secret|token)=)[^&#\s]+`)
	bearerPattern       = regexp.MustCompile(`(?i)\bBearer\s+[^\s,;]+`)
	secretPrefixPattern = regexp.MustCompile(`\b(?:sk-[A-Za-z0-9_-]{12,}|ghp_[A-Za-z0-9]{12,}|xox[baprs]-[A-Za-z0-9-]{12,})\b`)
	fileURLPattern      = regexp.MustCompile(`(?i)file://[^\s"'<>|]+`)
	windowsPathPattern  = regexp.MustCompile(`(?i)(?:[A-Za-z]:[\\/]|\\\\)[^\s"'<>|]+`)
	unixFieldPattern    = regexp.MustCompile(`(?i)(\b(?:path|file|directory|input|target|cache|root)\b\s*[:=]\s*)/(?:[^\s"'<>|]+)`)
	unixVerbPattern     = regexp.MustCompile(`(?i)(\b(?:open|read|write|remove|stat|rename|create|load|save)\b\s+)/[^\s"'<>|]+`)
)

// SafeErrorMessage bounds and redacts diagnostic text before it crosses a
// process, persistence, or HTTP boundary.
func SafeErrorMessage(message string) string {
	message = strings.NewReplacer("\r", " ", "\n", " ").Replace(strings.TrimSpace(message))
	message = sensitiveKeyPattern.ReplaceAllString(message, "$1[REDACTED]")
	message = urlSecretPattern.ReplaceAllString(message, "$1[REDACTED]")
	message = bearerPattern.ReplaceAllString(message, "Bearer [REDACTED]")
	message = secretPrefixPattern.ReplaceAllString(message, "[REDACTED]")
	message = fileURLPattern.ReplaceAllString(message, "[PATH_REDACTED]")
	message = windowsPathPattern.ReplaceAllString(message, "[PATH_REDACTED]")
	message = unixFieldPattern.ReplaceAllString(message, "$1[PATH_REDACTED]")
	message = unixVerbPattern.ReplaceAllString(message, "$1[PATH_REDACTED]")
	const maxRunes = 1024
	runes := []rune(message)
	if len(runes) > maxRunes {
		message = string(runes[:maxRunes-3]) + "..."
	}
	return message
}
