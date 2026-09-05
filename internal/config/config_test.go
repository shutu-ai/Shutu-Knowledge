package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultsClamp(t *testing.T) {
	cfg := Defaults()
	cfg.clamp()
	if cfg.Chunking.Size != 800 || cfg.Retrieval.TopK != 4 || cfg.Embedding.Provider != "none" {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	// Out-of-range values clamp to the bound instead of failing the process.
	cfg.Retrieval.TopK = 9999
	cfg.Chunking.Overlap = 100000
	cfg.clamp()
	if cfg.Retrieval.TopK != 50 || cfg.Chunking.Overlap != 799 {
		t.Fatalf("clamping failed: %+v", cfg)
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SHUTU_KNOWLEDGE_HOME", home)
	file := filepath.Join(home, "config.yaml")
	content := "unknownField: 1\n"
	if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil {
		t.Fatal("expected error for unknown config field")
	}
}

func TestLoadEnvOverridesAndSecretPreview(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SHUTU_KNOWLEDGE_HOME", home)
	t.Setenv("SHUTU_KNOWLEDGE_LOG_LEVEL", "debug")
	t.Setenv("KNOWLEDGE_API_KEY", "secret-value")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Logging.Level != "debug" {
		t.Fatalf("env override failed: %+v", cfg.Logging)
	}
	if cfg.Embedding.APIKey != "secret-value" {
		t.Fatal("api key override failed")
	}
	if got := EnvPreview(); got == "" || filepath.IsAbs(got) && !contains(got, "<set>") && !contains(got, "SHUTU_KNOWLEDGE_HOME") {
		t.Fatalf("unexpected env preview: %q", got)
	}
}

func TestDatabaseAndRawPaths(t *testing.T) {
	cfg := Defaults()
	home := t.TempDir()
	if got := cfg.DatabasePath(home); got != filepath.Join(home, "knowledge.db") {
		t.Fatalf("database path: %s", got)
	}
	if got := cfg.RawStoreDir(home); got != filepath.Join(home, "raw") {
		t.Fatalf("raw dir: %s", got)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
