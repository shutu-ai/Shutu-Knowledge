package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
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
	cfg.Maintenance.VacuumThresholdMB = 99999
	cfg.clamp()
	if cfg.Retrieval.TopK != 50 || cfg.Chunking.Overlap != 799 || cfg.Maintenance.VacuumThresholdMB != 4096 {
		t.Fatalf("clamping failed: %+v", cfg)
	}
	cfg.Processing.Provider = "unsupported"
	cfg.Workflow.ConflictStrategy = "detect"
	cfg.Workflow.URLRefreshHours = -1
	cfg.AutoRetrieve.Weight = 99
	cfg.Captioning.Provider = "vision"
	cfg.clamp()
	if cfg.Processing.Provider != "builtin" || cfg.Workflow.ConflictStrategy != "rename" ||
		cfg.Workflow.URLRefreshHours != 0 || cfg.AutoRetrieve.Weight != 5 || cfg.Captioning.Provider != "off" {
		t.Fatalf("new field normalization failed: %+v %+v %+v %+v", cfg.Processing, cfg.Workflow, cfg.AutoRetrieve, cfg.Captioning)
	}
}

func TestRepositoryDefaultConfigMatchesBuiltInDefaults(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve config test source path")
	}
	path := filepath.Join(filepath.Dir(sourceFile), "..", "..", "config.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read repository default config: %v", err)
	}
	var configured Config
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&configured); err != nil {
		t.Fatalf("parse repository default config: %v", err)
	}
	if configured != Defaults() {
		t.Fatalf("repository config drifted from built-in defaults: %#v", configured)
	}
}

func TestProcessingWorkflowAndAutoRetrievePersist(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SHUTU_KNOWLEDGE_HOME", home)
	cfg := Defaults()
	cfg.Processing.Provider = "mineru"
	cfg.Processing.APIKey = "mineru-secret"
	cfg.Processing.APIHost = "https://mineru.example"
	cfg.Workflow.ConflictStrategy = "replace"
	cfg.Workflow.URLRefreshHours = 49
	cfg.AutoRetrieve.Enabled = false
	cfg.AutoRetrieve.Weight = 1
	cfg.OCR.FallbackHelper = "tesseract-wrapper {input} {format}"
	cfg.OCR.RenderHelper = "pdf-render-helper {input} {format}"
	cfg.OCR.RenderTimeoutMS = 999999
	cfg.Helpers.ContentConverter = "anydoc-helper {input} {format}"
	cfg.Helpers.ImageDecoder = "image-decode-helper {input} {format}"
	cfg.Captioning.Provider = "openai"
	cfg.Captioning.Model = "vision-model"
	cfg.Captioning.BaseURL = "https://vision.example"
	cfg.Captioning.APIKey = "caption-secret"
	path := filepath.Join(home, "config.yaml")
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Processing.Provider != "mineru" || loaded.Processing.APIKey != "mineru-secret" ||
		loaded.Processing.APIHost != "https://mineru.example" {
		t.Fatalf("processing did not persist: %+v", loaded.Processing)
	}
	if loaded.Workflow.ConflictStrategy != "replace" || loaded.Workflow.URLRefreshHours != 49 {
		t.Fatalf("workflow did not persist: %+v", loaded.Workflow)
	}
	if loaded.AutoRetrieve.Enabled || loaded.AutoRetrieve.Weight != 1 {
		t.Fatalf("auto retrieve did not persist: %+v", loaded.AutoRetrieve)
	}
	if loaded.OCR.FallbackHelper != "tesseract-wrapper {input} {format}" {
		t.Fatalf("OCR fallback did not persist: %+v", loaded.OCR)
	}
	if loaded.OCR.RenderHelper != "pdf-render-helper {input} {format}" || loaded.OCR.RenderTimeoutMS != 600000 {
		t.Fatalf("OCR renderer did not persist/normalize: %+v", loaded.OCR)
	}
	if loaded.Helpers.ContentConverter != "anydoc-helper {input} {format}" {
		t.Fatalf("content converter did not persist: %+v", loaded.Helpers)
	}
	if loaded.Helpers.ImageDecoder != "image-decode-helper {input} {format}" {
		t.Fatalf("image decoder did not persist: %+v", loaded.Helpers)
	}
	if loaded.Captioning.Provider != "openai" || loaded.Captioning.Model != "vision-model" ||
		loaded.Captioning.BaseURL != "https://vision.example" || loaded.Captioning.APIKey != "caption-secret" {
		t.Fatalf("captioning did not persist: %+v", loaded.Captioning)
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
	t.Setenv("SHUTU_KNOWLEDGE_RUNTIME_HELPER", "helper --default")
	t.Setenv("SHUTU_KNOWLEDGE_EMBEDDING_HELPER", "helper --embedding")
	t.Setenv("SHUTU_KNOWLEDGE_RERANK_HELPER", "helper --rerank")
	t.Setenv("SHUTU_KNOWLEDGE_OCR_HELPER", "helper --ocr")
	t.Setenv("SHUTU_KNOWLEDGE_OCR_FALLBACK_HELPER", "tesseract {input}")
	t.Setenv("SHUTU_KNOWLEDGE_OCR_RENDER_HELPER", "pdf-render {input} {format}")
	t.Setenv("SHUTU_KNOWLEDGE_IMAGE_DECODER", "image-decode {input} {format}")
	t.Setenv("SHUTU_KNOWLEDGE_CAPTION_API_KEY", "caption-secret")
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
	if cfg.Runtime.HelperCommand != "helper --default" ||
		cfg.Runtime.EmbeddingHelper != "helper --embedding" ||
		cfg.Runtime.RerankHelper != "helper --rerank" ||
		cfg.Runtime.OCRHelper != "helper --ocr" {
		t.Fatalf("runtime helper overrides failed: %+v", cfg.Runtime)
	}
	if cfg.OCR.FallbackHelper != "tesseract {input}" {
		t.Fatalf("OCR fallback override failed: %+v", cfg.OCR)
	}
	if cfg.OCR.RenderHelper != "pdf-render {input} {format}" || cfg.OCR.RenderTimeoutMS != 120000 {
		t.Fatalf("OCR renderer override/default failed: %+v", cfg.OCR)
	}
	if cfg.Helpers.ImageDecoder != "image-decode {input} {format}" {
		t.Fatalf("image decoder override failed: %+v", cfg.Helpers)
	}
	if cfg.Captioning.APIKey != "caption-secret" {
		t.Fatal("caption api key override failed")
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
