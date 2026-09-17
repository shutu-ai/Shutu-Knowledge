package main

import (
	"context"
	"errors"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestFormatListenErrorExplainsAddressConflict(t *testing.T) {
	err := formatListenError("127.0.0.1:8080", errors.New("wrapped: "+syscall.EADDRINUSE.Error()))
	if errors.Is(err, syscall.EADDRINUSE) {
		t.Fatalf("unrelated error was classified as address conflict: %v", err)
	}

	err = formatListenError("127.0.0.1:8080", syscall.EADDRINUSE)
	message := err.Error()
	for _, want := range []string{"127.0.0.1:8080", "address already in use", "server.addr"} {
		if !strings.Contains(message, want) {
			t.Fatalf("listen error missing %q: %s", want, message)
		}
	}
}

type blockingHTTPShutdowner struct{}

func (blockingHTTPShutdowner) Shutdown(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

func TestShutdownServerWithTimeoutBoundsUncooperativeHandler(t *testing.T) {
	started := time.Now()
	err := shutdownServerWithTimeout(blockingHTTPShutdowner{}, 20*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown error = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("shutdown exceeded bounded test budget: %s", elapsed)
	}
}
