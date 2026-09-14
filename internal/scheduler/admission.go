// Package scheduler provides admission primitives shared by callers that need
// to observe the same finite resource budget.
package scheduler

import (
	"context"
	"sync"
)

// Admission grants exclusive capacity and returns an idempotent release.
type Admission interface {
	Acquire(ctx context.Context) (func(), error)
}

// Semaphore is a bounded admission pool. Callers in different packages can
// share one instance to enforce a single process-wide resource limit.
type Semaphore struct {
	slots chan struct{}
}

// NewSemaphore creates a finite pool with at least one slot.
func NewSemaphore(capacity int) *Semaphore {
	if capacity < 1 {
		capacity = 1
	}
	return &Semaphore{slots: make(chan struct{}, capacity)}
}

// Acquire blocks until a slot, context cancellation, or caller deadline.
func (s *Semaphore) Acquire(ctx context.Context) (func(), error) {
	select {
	case s.slots <- struct{}{}:
		var once sync.Once
		return func() {
			once.Do(func() { <-s.slots })
		}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
