package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSplitCommandPreservesQuotedWindowsPaths(t *testing.T) {
	command := quoteCommandArg(`C:\Users\Test User\runtime\node.exe`) + " " + quoteCommandArg(`C:\Users\Test User\runtime\managed-bootstrap.mjs`)
	words, ok := splitCommand(command)
	if !ok || len(words) != 2 {
		t.Fatalf("split command: %v %v", words, ok)
	}
	if words[0] != `C:\Users\Test User\runtime\node.exe` || words[1] != `C:\Users\Test User\runtime\managed-bootstrap.mjs` {
		t.Fatalf("quoted path changed: %v", words)
	}
}

func TestPrepareManagedRuntimeMaterializesPinnedAssets(t *testing.T) {
	home := t.TempDir()
	command, err := PrepareManagedRuntime(context.Background(), home, filepath.Join(home, "model cache"))
	if err != nil {
		t.Fatal(err)
	}
	words, ok := splitCommand(command)
	if !ok || len(words) < 5 {
		t.Fatalf("managed command: %q", command)
	}
	if !strings.HasSuffix(words[1], "managed-bootstrap.mjs") {
		t.Fatalf("bootstrap command: %v", words)
	}
	for _, name := range []string{"managed-runtime.mjs", "managed-bootstrap.mjs", "package.json", "package-lock.json", "runtime-manifest.json"} {
		if _, err := os.Stat(filepath.Join(home, "runtime", name)); err != nil {
			t.Fatalf("managed asset %s: %v", name, err)
		}
	}
}
