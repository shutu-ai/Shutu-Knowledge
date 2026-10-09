package mcp

import (
	"context"
	"errors"
	"fmt"
	"time"

	"golang.org/x/sync/semaphore"
)

// Request limits implement plan §10.2.9: bounded query bytes, bounded
// concurrency, and a per-call deadline. Defaults are conservative; adapters do
// not expose knobs that can disable them.
const (
	maxQueryBytes  = 8192
	maxConcurrent  = 8
	acquireTimeout = 10 * time.Second
	callTimeout    = 120 * time.Second
)

var errQueryTooLarge = fmt.Errorf("query exceeds %d bytes", maxQueryBytes)

// toolLimiter bounds concurrent tool executions per server instance.
type toolLimiter struct {
	sem *semaphore.Weighted
}

func newToolLimiter() *toolLimiter {
	return &toolLimiter{sem: semaphore.NewWeighted(maxConcurrent)}
}

// acquire waits for a slot. Waiting longer than acquireTimeout reports
// RESOURCE_LIMIT instead of queueing without bound.
func (l *toolLimiter) acquire(ctx context.Context) (release func(), err error) {
	acquireCtx, cancel := context.WithTimeout(ctx, acquireTimeout)
	defer cancel()
	if err := l.sem.Acquire(acquireCtx, 1); err != nil {
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			return nil, errDeadline
		}
		return nil, err
	}
	return func() { l.sem.Release(1) }, nil
}

var errDeadline = errors.New("tool call deadline exceeded")

// checkQueryBounds enforces the request-size ceiling for text inputs.
func checkQueryBounds(query string) error {
	if len(query) > maxQueryBytes {
		return errQueryTooLarge
	}
	return nil
}
