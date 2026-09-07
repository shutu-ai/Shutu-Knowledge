package parser

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLibreOfficeMissingIsActionable(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	helper := &LibreOfficeHelper{path: filepath.Join(t.TempDir(), "missing-soffice")}
	if helper.Available() {
		t.Fatal("missing LibreOffice unexpectedly reported available")
	}
	_, err := helper.Run(context.Background(), "doc", []byte("legacy"))
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "install libreoffice") {
		t.Fatalf("missing LibreOffice error is not actionable: %v", err)
	}
}

func TestLibreOfficeConversionHonorsCancellation(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHUTU_PARSER_HELPER_PROCESS", "1")
	t.Setenv("SHUTU_PARSER_HELPER_SLEEP", "1")
	helper := &LibreOfficeHelper{path: executable, extraArgs: []string{"-test.run=^TestHelperProcessLegacy$", "--"}}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err = helper.Run(ctx, "doc", []byte("legacy"))
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "context deadline") {
		t.Fatalf("LibreOffice cancellation was not surfaced: %v", err)
	}
}
