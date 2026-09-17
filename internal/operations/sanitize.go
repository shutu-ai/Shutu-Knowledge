package operations

import "github.com/shutu-ai/shutu-knowledge/internal/safety"

// SafeErrorMessage is kept in the operations package for callers that
// already depend on the operation error boundary; the implementation is
// shared with runtime and Web-facing diagnostics.
func SafeErrorMessage(message string) string {
	return safety.SafeErrorMessage(message)
}
