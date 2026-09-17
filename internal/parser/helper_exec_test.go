package parser

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestHelperProcessLegacy is executed as a child process by ExecHelper tests.
// It is a no-op in the parent test process.
func TestHelperProcessLegacy(t *testing.T) {
	if os.Getenv("SHUTU_PARSER_HELPER_PROCESS") != "1" {
		return
	}
	if os.Getenv("SHUTU_PARSER_HELPER_SLEEP") == "1" {
		time.Sleep(2 * time.Second)
		return
	}
	fmt.Fprintf(os.Stdout, "converted:%s", os.Getenv("SHUTU_PARSER_HELPER_FORMAT"))
	os.Exit(0)
}

func quotedHelperCommand(t *testing.T, testFunction string) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return strconv.Quote(filepath.ToSlash(exe)) + " -test.run=^" + testFunction + "$ {input} {format}"
}

func TestExecHelperRunsExternalProcess(t *testing.T) {
	t.Setenv("SHUTU_PARSER_HELPER_PROCESS", "1")
	t.Setenv("SHUTU_PARSER_HELPER_FORMAT", "doc")
	helper := ExecHelper{
		Template:  quotedHelperCommand(t, "TestHelperProcessLegacy"),
		TimeoutMS: 30_000,
	}
	if !helper.Available() {
		t.Fatal("external helper should be executable")
	}
	text, err := helper.Run(context.Background(), "doc", []byte("legacy payload"))
	if err != nil || text != "converted:doc" {
		t.Fatalf("external helper: %q %v", text, err)
	}
}

func TestRegistrySetLegacyHelperFailClosed(t *testing.T) {
	t.Setenv("SHUTU_PARSER_HELPER_PROCESS", "1")
	t.Setenv("SHUTU_PARSER_HELPER_FORMAT", "ppt")
	registry := NewRegistry()
	if _, err := registry.Parse("legacy.ppt", []byte("legacy")); err == nil {
		t.Fatal("expected no helper initially")
	}

	registry.SetLegacyHelper(ExecHelper{
		Template:  quotedHelperCommand(t, "TestHelperProcessLegacy"),
		TimeoutMS: 30_000,
	})
	result, err := registry.Parse("legacy.ppt", []byte("legacy payload"))
	if err != nil || result.Text != "converted:ppt" {
		t.Fatalf("installed helper: %+v %v", result, err)
	}

	registry.SetLegacyHelper(nil)
	if _, err := registry.Parse("legacy.ppt", []byte("legacy")); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("removed helper should fail closed: %v", err)
	}
}

type cancelAwareParserHelper struct {
	started chan struct{}
}

func (h *cancelAwareParserHelper) Available() bool { return true }

func (h *cancelAwareParserHelper) Run(ctx context.Context, _ string, _ []byte) (string, error) {
	close(h.started)
	<-ctx.Done()
	return "", ctx.Err()
}

func TestRegistryParseContextPropagatesCancellationToLegacyHelper(t *testing.T) {
	helper := &cancelAwareParserHelper{started: make(chan struct{})}
	registry := NewRegistry(WithLegacyHelper(helper))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := registry.ParseContext(ctx, "legacy.doc", []byte("legacy"))
		done <- err
	}()

	select {
	case <-helper.started:
	case <-time.After(time.Second):
		t.Fatal("legacy helper did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("parse error = %v, want context cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("parse did not stop after cancellation")
	}
}

func TestSplitCommandQuoting(t *testing.T) {
	words, ok := splitCommand(`"C:/Program Files/AnyDoc/anydoc.exe" "{input}" 'doc'`)
	if !ok || len(words) != 3 || words[0] != "C:/Program Files/AnyDoc/anydoc.exe" ||
		words[1] != "{input}" || words[2] != "doc" {
		t.Fatalf("words: %v ok=%v", words, ok)
	}
	if _, ok := splitCommand(`"unterminated {input}`); ok {
		t.Fatal("unterminated quote must be rejected")
	}
}
