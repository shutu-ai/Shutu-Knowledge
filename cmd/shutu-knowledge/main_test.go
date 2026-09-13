package main

import (
	"errors"
	"strings"
	"syscall"
	"testing"
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
