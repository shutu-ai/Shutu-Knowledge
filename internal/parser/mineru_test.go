package parser

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMineruPollWaitHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()
	err := waitForMineruPoll(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("wait error = %v, want context cancellation", err)
	}
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
		t.Fatalf("canceled poll wait took %s", elapsed)
	}
}
