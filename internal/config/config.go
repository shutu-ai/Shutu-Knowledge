// Package config implements Knowledge-owned configuration: built-in
// defaults, an optional config.yaml in the data domain, then environment
// overrides. The Agent never owns or stores these business fields.
package config

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// DataHome returns the Knowledge data domain root. SHUTU_KNOWLEDGE_HOME
// overrides the default ~/.shutu/knowledge.
func DataHome() (string, error) {
	if fromEnv := strings.TrimSpace(os.Getenv("SHUTU_KNOWLEDGE_HOME")); fromEnv != "" {
		return fromEnv, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home: %w", err)
	}
	return filepath.Join(home, ".shutu", "knowledge"), nil
}

// Config is the full Knowledge configuration surface. Layering: Defaults()
// -> file (config.yaml in the data domain) -> environment. Later phases
// extend the typed surface; unknown file fields are rejected so typos fail
// loudly instead of silently changing behavior.
type Config struct {
	Database struct {
		// Path of the SQLite file. Empty = <data home>/knowledge.db.
		Path string `yaml:"path" json:"path"`
	} `yaml:"database" json:"database"`

	Logging struct {
		// Level: debug, info, warn, error.
		Level string `yaml:"level" json:"level"`
	} `yaml:"logging" json:"logging"`

	Server struct {
		// Addr of the standalone HTTP server (serve mode).
		Addr string `yaml:"addr" json:"addr"`
	} `yaml:"server" json:"server"`

	Embedding struct {
		Provider string `yaml:"provider" json:"provider"` // openai | ollama | local | none
		BaseURL  string `yaml:"baseUrl" json:"baseUrl"`
		Model    string `yaml:"model" json:"model"`
		APIKey   string `yaml:"apiKey" json:"-"`
		Batch    int    `yaml:"batch" json:"batch"`
	} `yaml:"embedding" json:"embedding"`

	Rerank struct {
		Model      string `yaml:"model" json:"model"`
		BaseURL    string `yaml:"baseUrl" json:"baseUrl"`
		APIKey     string `yaml:"apiKey" json:"-"`
		TimeoutMS  int    `yaml:"timeoutMs" json:"timeoutMs"`
		Enabled    bool   `yaml:"enabled" json:"enabled"`
		Required   bool   `yaml:"required" json:"required"`
		QueueLimit int    `yaml:"queueLimit" json:"queueLimit"`
	} `yaml:"rerank" json:"rerank"`

	Captioning struct {
		Provider string `yaml:"provider" json:"provider"` // off | openai | ollama
		BaseURL  string `yaml:"baseUrl" json:"baseUrl"`
		Model    string `yaml:"model" json:"model"`
		APIKey   string `yaml:"apiKey" json:"-"`
	} `yaml:"captioning" json:"captioning"`

	Chunking struct {
		Smart             bool    `yaml:"smart" json:"smart"`
		Separator         string  `yaml:"separator" json:"separator"`
		Size              int     `yaml:"size" json:"size"`
		Overlap           int     `yaml:"overlap" json:"overlap"`
		Semantic          bool    `yaml:"semantic" json:"semantic"`
		SemanticThreshold float64 `yaml:"semanticThreshold" json:"semanticThreshold"`
		TokenLimit        int     `yaml:"tokenLimit" json:"tokenLimit"`
	} `yaml:"chunking" json:"chunking"`

	Retrieval struct {
		TopK             int     `yaml:"topK" json:"topK"`
		Mode             string  `yaml:"mode" json:"mode"` // auto | hybrid | vector | lexical
		SimilarityMin    float64 `yaml:"similarityMin" json:"similarityMin"`
		MMR              bool    `yaml:"mmr" json:"mmr"`
		MMRDiversity     float64 `yaml:"mmrDiversity" json:"mmrDiversity"`
		RRFVectorWeight  float64 `yaml:"rrfVectorWeight" json:"rrfVectorWeight"`
		SiblingChunks    int     `yaml:"siblingChunks" json:"siblingChunks"`
		ContextTimeoutMS int     `yaml:"contextTimeoutMs" json:"contextTimeoutMs"`
		SearchTimeoutMS  int     `yaml:"searchTimeoutMs" json:"searchTimeoutMs"`
	} `yaml:"retrieval" json:"retrieval"`

	// Processing is the deployment-wide document processor default. A base
	// can override provider/host/key; empty base fields inherit these values.
	Processing struct {
		Provider string `yaml:"provider" json:"provider"` // builtin | mineru
		APIKey   string `yaml:"apiKey" json:"-"`
		APIHost  string `yaml:"apiHost" json:"apiHost"`
	} `yaml:"processing" json:"processing"`

	// Workflow carries import defaults that bases may override.
	Workflow struct {
		ConflictStrategy string `yaml:"conflictStrategy" json:"conflictStrategy"` // keep | replace | rename
		URLRefreshHours  int    `yaml:"urlRefreshHours" json:"urlRefreshHours"`
	} `yaml:"workflow" json:"workflow"`

	// AutoRetrieve is the Agent-visible automatic retrieval default.
	AutoRetrieve struct {
		Enabled bool `yaml:"enabled" json:"enabled"`
		// Weight is the per-base seat cap (0 excludes a base).
		Weight int `yaml:"weight" json:"weight"`
	} `yaml:"autoRetrieve" json:"autoRetrieve"`

	Jobs struct {
		ImportWorkers   int  `yaml:"importWorkers" json:"importWorkers"`
		ResumeInterrupt bool `yaml:"resumeInterrupt" json:"resumeInterrupt"`
	} `yaml:"jobs" json:"jobs"`

	Models struct {
		CacheDir            string `yaml:"cacheDir" json:"cacheDir"`
		HFEndpoint          string `yaml:"hfEndpoint" json:"hfEndpoint"`
		WorkerIdleTimeoutMS int    `yaml:"workerIdleTimeoutMs" json:"workerIdleTimeoutMs"`
	} `yaml:"models" json:"models"`

	// Scheduler bounds long-running work across independent resource lanes.
	// These limits protect SQLite, disk, model helpers, and remote endpoints
	// from one workload monopolizing the process.
	Scheduler struct {
		IO          int `yaml:"io" json:"io"`
		DBWrite     int `yaml:"dbWrite" json:"dbWrite"`
		Disk        int `yaml:"disk" json:"disk"`
		Network     int `yaml:"network" json:"network"`
		Model       int `yaml:"model" json:"model"`
		ModelWaitMS int `yaml:"modelWaitMs" json:"modelWaitMs"`
		Maintenance int `yaml:"maintenance" json:"maintenance"`
		MaxPerBase  int `yaml:"maxPerBase" json:"maxPerBase"`
		// MemoryBytes and DiskBytes cap the aggregate declared peak footprint
		// of concurrently executing durable operations.
		MemoryBytes       int64 `yaml:"memoryBytes" json:"memoryBytes"`
		DiskBytes         int64 `yaml:"diskBytes" json:"diskBytes"`
		DiskLowWaterBytes int64 `yaml:"diskLowWaterBytes" json:"diskLowWaterBytes"`
		// UploadBytes caps the aggregate bytes retained by uploading, complete,
		// and bound staging sessions.
		UploadBytes int64 `yaml:"uploadBytes" json:"uploadBytes"`
		// TempBytes caps in-process temporary staging reservations.
		TempBytes  int64 `yaml:"tempBytes" json:"tempBytes"`
		QueueLimit int   `yaml:"queueLimit" json:"queueLimit"`
		// IdempotencyRetentionHours bounds the terminal response retry window.
		// Zero retains bindings indefinitely.
		IdempotencyRetentionHours int `yaml:"idempotencyRetentionHours" json:"idempotencyRetentionHours"`
	} `yaml:"scheduler" json:"scheduler"`

	Maintenance struct {
		// FTSAutoOptimize merges FTS5 index segments after startup.
		FTSAutoOptimize bool `yaml:"ftsAutoOptimize" json:"ftsAutoOptimize"`
		// Vacuum is enabled only when the main database reaches the threshold.
		Vacuum            bool `yaml:"vacuum" json:"vacuum"`
		VacuumThresholdMB int  `yaml:"vacuumThresholdMB" json:"vacuumThresholdMB"`
		// Quarantine retention window. Zero retains recovery copies until an
		// explicit purge; otherwise expired copies are removed by maintenance.
		QuarantineRetentionHours int `yaml:"quarantineRetentionHours" json:"quarantineRetentionHours"`
	} `yaml:"maintenance" json:"maintenance"`

	Runtime struct {
		// HelperCommand is the default optional ML helper. It speaks the
		// line-delimited JSON contract implemented by internal/runtime.
		HelperCommand string `yaml:"helperCommand" json:"helperCommand"`
		// Offline prevents the managed runtime from reaching remote model or
		// language-data sources. It is intended for an already-installed
		// runtime cache and makes offline restart an explicit product mode.
		Offline bool `yaml:"offline" json:"offline"`
		// Capability commands override HelperCommand when deployments split
		// model runtimes. All commands are optional.
		EmbeddingHelper  string `yaml:"embeddingHelper" json:"embeddingHelper"`
		RerankHelper     string `yaml:"rerankHelper" json:"rerankHelper"`
		OCRHelper        string `yaml:"ocrHelper" json:"ocrHelper"`
		StartupTimeoutMS int    `yaml:"startupTimeoutMs" json:"startupTimeoutMs"`
		RequestTimeoutMS int    `yaml:"requestTimeoutMs" json:"requestTimeoutMs"`
		IdleTimeoutMS    int    `yaml:"idleTimeoutMs" json:"idleTimeoutMs"`
	} `yaml:"runtime" json:"runtime"`

	OCR struct {
		Enabled bool `yaml:"enabled"`
		// Mode distinguishes native extraction, OCR fallback, and forced
		// OCR: auto (fallback when native extraction fails) | forced | off.
		Mode string `yaml:"mode" json:"mode"`
		// Helper is the optional external OCR command template; empty
		// disables OCR entirely. Example: "ocr-helper {input}".
		Helper string `yaml:"helper" json:"helper"`
		// FallbackHelper is an optional secondary OCR command, typically a
		// deployment-provided Tesseract wrapper. It runs only when the
		// primary isolated runtime/helper fails.
		FallbackHelper string `yaml:"fallbackHelper" json:"fallbackHelper"`
		// RenderHelper optionally rasterizes a complete PDF to a bounded
		// JSON/PNG response before OCR. It is deployment-supplied; empty
		// leaves Knowledge using the PDF-envelope and embedded-raster paths.
		RenderHelper string `yaml:"renderHelper" json:"renderHelper"`
		// RenderTimeoutMS bounds one full-page renderer process.
		RenderTimeoutMS int `yaml:"renderTimeoutMs" json:"renderTimeoutMs"`
		TimeoutMS       int `yaml:"timeoutMs" json:"timeoutMs"`
	} `yaml:"ocr" json:"ocr"`

	Helpers struct {
		// LegacyOffice converts .doc/.ppt/.xls via an optional external
		// runtime (e.g. "anydoc {input} {format}"); empty = unsupported.
		LegacyOffice string `yaml:"legacyOffice" json:"legacyOffice"`
		// ContentConverter is the optional content-signature fallback for
		// PDFs that expose no healthy native text and cannot be OCR'd.
		ContentConverter string `yaml:"contentConverter" json:"contentConverter"`
		// ImageDecoder decodes optional heavyweight PDF codecs (JBIG2 and
		// JPEG 2000) from a JSON envelope and writes PNG to stdout. Empty
		// leaves those codecs unsupported.
		ImageDecoder string `yaml:"imageDecoder" json:"imageDecoder"`
		// ImageDecoderTimeoutMS bounds one image-decoder process.
		ImageDecoderTimeoutMS int `yaml:"imageDecoderTimeoutMs" json:"imageDecoderTimeoutMs"`
	} `yaml:"helpers" json:"helpers"`
}

// Defaults returns the built-in configuration (parity with dsh deployment
// defaults in cordis.patch.yml).
func Defaults() Config {
	var c Config
	c.Logging.Level = "info"
	c.Server.Addr = "127.0.0.1:7730"
	c.Embedding.Provider = "none"
	c.Embedding.Batch = 32
	c.Rerank.TimeoutMS = 60000
	c.Rerank.QueueLimit = 16
	c.Captioning.Provider = "off"
	c.Chunking.Smart = true
	c.Chunking.Separator = "\n\n"
	c.Chunking.Size = 800
	c.Chunking.Overlap = 100
	c.Chunking.SemanticThreshold = 0.75
	c.Retrieval.TopK = 4
	c.Retrieval.Mode = "auto"
	// MMR remains disabled by default; when explicitly enabled, this is the
	// relevance weight in the standard MMR formula.
	c.Retrieval.MMRDiversity = 0.75
	c.Retrieval.RRFVectorWeight = 1
	c.Retrieval.SiblingChunks = 1
	c.Retrieval.ContextTimeoutMS = 4000
	c.Retrieval.SearchTimeoutMS = 25000
	c.Processing.Provider = "builtin"
	c.Processing.APIHost = "https://mineru.net"
	c.Workflow.ConflictStrategy = "rename"
	c.AutoRetrieve.Enabled = true
	c.AutoRetrieve.Weight = 3
	c.Jobs.ImportWorkers = 5
	c.Jobs.ResumeInterrupt = true
	c.Scheduler.IO = 2
	c.Scheduler.DBWrite = 1
	c.Scheduler.Disk = 1
	c.Scheduler.Network = 2
	c.Scheduler.Model = 1
	c.Scheduler.ModelWaitMS = 2000
	c.Scheduler.Maintenance = 1
	c.Scheduler.MaxPerBase = 2
	c.Scheduler.MemoryBytes = 2 << 30
	c.Scheduler.DiskBytes = 10 << 30
	c.Scheduler.DiskLowWaterBytes = 256 << 20
	c.Scheduler.UploadBytes = 512 << 20
	c.Scheduler.TempBytes = 1 << 30
	c.Scheduler.QueueLimit = 1000
	c.Scheduler.IdempotencyRetentionHours = 168
	c.Models.WorkerIdleTimeoutMS = 60000
	c.Runtime.StartupTimeoutMS = 10000
	c.Runtime.RequestTimeoutMS = 60000
	c.Runtime.IdleTimeoutMS = 300000
	c.Maintenance.FTSAutoOptimize = true
	c.Maintenance.Vacuum = true
	c.Maintenance.VacuumThresholdMB = 256
	c.Maintenance.QuarantineRetentionHours = 168
	c.OCR.Mode = "auto"
	c.OCR.TimeoutMS = 120000
	c.OCR.RenderTimeoutMS = 120000
	c.Helpers.ImageDecoderTimeoutMS = 120000
	return c
}

// Load resolves the effective configuration from defaults, the optional
// config.yaml in the data domain, and environment overrides.
func Load() (Config, error) {
	cfg := Defaults()
	home, err := DataHome()
	if err != nil {
		return cfg, err
	}
	file := filepath.Join(home, "config.yaml")
	if data, err := os.ReadFile(file); err == nil {
		decoder := yaml.NewDecoder(strings.NewReader(string(data)))
		decoder.KnownFields(true)
		if err := decoder.Decode(&cfg); err != nil {
			return cfg, fmt.Errorf("parse %s: %w", file, err)
		}
	} else if !os.IsNotExist(err) {
		return cfg, fmt.Errorf("read %s: %w", file, err)
	}
	applyEnv(&cfg)
	cfg.clamp()
	return cfg, nil
}

// Normalized validates ranges without changing the on-disk file layout.
func (c Config) Normalized() Config {
	c.clamp()
	return c
}

// Save writes the Knowledge-owned config file with private file permissions.
func Save(path string, c Config) error {
	return SaveContext(context.Background(), path, c)
}

// SaveContext atomically writes the Knowledge-owned config file while
// honoring cancellation between filesystem operations.
func SaveContext(ctx context.Context, path string, c Config) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	c = c.Normalized()
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".config-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	removeTemporary := true
	defer func() {
		if removeTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := ctx.Err(); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	removeTemporary = false
	return nil
}

func applyEnv(cfg *Config) {
	if v := os.Getenv("SHUTU_KNOWLEDGE_LOG_LEVEL"); v != "" {
		cfg.Logging.Level = v
	}
	if v := os.Getenv("SHUTU_KNOWLEDGE_ADDR"); v != "" {
		cfg.Server.Addr = v
	}
	if v := os.Getenv("KNOWLEDGE_API_KEY"); v != "" {
		cfg.Embedding.APIKey = v
	}
	if v := os.Getenv("KNOWLEDGE_RERANK_API_KEY"); v != "" {
		cfg.Rerank.APIKey = v
	}
	if v := os.Getenv("SHUTU_KNOWLEDGE_CAPTION_API_KEY"); v != "" {
		cfg.Captioning.APIKey = v
	}
	if v := os.Getenv("HF_ENDPOINT"); v != "" {
		cfg.Models.HFEndpoint = v
	}
	if v := os.Getenv("SHUTU_KNOWLEDGE_RUNTIME_HELPER"); v != "" {
		cfg.Runtime.HelperCommand = v
	}
	if v := os.Getenv("SHUTU_KNOWLEDGE_OFFLINE"); v != "" {
		cfg.Runtime.Offline = strings.EqualFold(strings.TrimSpace(v), "1") || strings.EqualFold(strings.TrimSpace(v), "true") || strings.EqualFold(strings.TrimSpace(v), "yes")
	}
	if v := os.Getenv("SHUTU_KNOWLEDGE_EMBEDDING_HELPER"); v != "" {
		cfg.Runtime.EmbeddingHelper = v
	}
	if v := os.Getenv("SHUTU_KNOWLEDGE_RERANK_HELPER"); v != "" {
		cfg.Runtime.RerankHelper = v
	}
	if v := os.Getenv("SHUTU_KNOWLEDGE_OCR_HELPER"); v != "" {
		cfg.Runtime.OCRHelper = v
	}
	if v := os.Getenv("SHUTU_KNOWLEDGE_OCR_FALLBACK_HELPER"); v != "" {
		cfg.OCR.FallbackHelper = v
	}
	if v := os.Getenv("SHUTU_KNOWLEDGE_OCR_RENDER_HELPER"); v != "" {
		cfg.OCR.RenderHelper = v
	}
	if v := os.Getenv("SHUTU_KNOWLEDGE_IMAGE_DECODER"); v != "" {
		cfg.Helpers.ImageDecoder = v
	}
}

// DatabasePath resolves the SQLite path against the data domain.
func (c Config) DatabasePath(home string) string {
	if p := strings.TrimSpace(c.Database.Path); p != "" {
		if filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(home, p)
	}
	return filepath.Join(home, "knowledge.db")
}

// RawStoreDir resolves the raw source directory (next to the database).
func (c Config) RawStoreDir(home string) string {
	return filepath.Join(filepath.Dir(c.DatabasePath(home)), "raw")
}

func (c *Config) clamp() {
	c.Embedding.Batch = clampInt(c.Embedding.Batch, 1, 512, 32)
	c.Rerank.TimeoutMS = clampInt(c.Rerank.TimeoutMS, 10000, 300000, 60000)
	c.Rerank.QueueLimit = clampInt(c.Rerank.QueueLimit, 1, 64, 16)
	c.Chunking.Size = clampInt(c.Chunking.Size, 64, 100000, 800)
	c.Chunking.Overlap = clampInt(c.Chunking.Overlap, 0, c.Chunking.Size-1, 0)
	c.Chunking.SemanticThreshold = clampFloat(c.Chunking.SemanticThreshold, 0, 1, 0.75)
	c.Chunking.TokenLimit = clampInt(c.Chunking.TokenLimit, 0, 1000000, 0)
	c.Retrieval.TopK = clampInt(c.Retrieval.TopK, 1, 50, 4)
	c.Retrieval.SimilarityMin = clampFloat(c.Retrieval.SimilarityMin, 0, 1, 0)
	c.Retrieval.MMRDiversity = clampFloat(c.Retrieval.MMRDiversity, 0, 1, 0)
	c.Retrieval.RRFVectorWeight = clampFloat(c.Retrieval.RRFVectorWeight, 0.1, 5, 1)
	c.Retrieval.SiblingChunks = clampInt(c.Retrieval.SiblingChunks, 0, 3, 1)
	c.Retrieval.ContextTimeoutMS = clampInt(c.Retrieval.ContextTimeoutMS, 500, 60000, 4000)
	c.Retrieval.SearchTimeoutMS = clampInt(c.Retrieval.SearchTimeoutMS, 500, 600000, 25000)
	c.Workflow.URLRefreshHours = clampInt(c.Workflow.URLRefreshHours, 0, 24*365, 0)
	c.AutoRetrieve.Weight = clampInt(c.AutoRetrieve.Weight, 0, 5, 3)
	c.Jobs.ImportWorkers = clampInt(c.Jobs.ImportWorkers, 1, 32, 5)
	c.Scheduler.IO = clampInt(c.Scheduler.IO, 1, 64, 2)
	c.Scheduler.DBWrite = clampInt(c.Scheduler.DBWrite, 1, 4, 1)
	c.Scheduler.Disk = clampInt(c.Scheduler.Disk, 1, 16, 1)
	c.Scheduler.Network = clampInt(c.Scheduler.Network, 1, 16, 2)
	c.Scheduler.Model = clampInt(c.Scheduler.Model, 1, 8, 1)
	c.Scheduler.ModelWaitMS = clampInt(c.Scheduler.ModelWaitMS, 100, 60000, 2000)
	c.Scheduler.Maintenance = clampInt(c.Scheduler.Maintenance, 1, 2, 1)
	c.Scheduler.MaxPerBase = clampInt(c.Scheduler.MaxPerBase, 1, 16, 2)
	c.Scheduler.MemoryBytes = clampInt64(c.Scheduler.MemoryBytes, 64<<20, 100<<30, 2<<30)
	c.Scheduler.DiskBytes = clampInt64(c.Scheduler.DiskBytes, 256<<20, 100<<30, 10<<30)
	c.Scheduler.DiskLowWaterBytes = clampInt64(c.Scheduler.DiskLowWaterBytes, 16<<20, 100<<30, 256<<20)
	c.Scheduler.UploadBytes = clampInt64(c.Scheduler.UploadBytes, 100<<20, 100<<30, 512<<20)
	c.Scheduler.TempBytes = clampInt64(c.Scheduler.TempBytes, 1<<20, 100<<30, 1<<30)
	c.Scheduler.QueueLimit = clampInt(c.Scheduler.QueueLimit, 1, 100000, 1000)
	c.Scheduler.IdempotencyRetentionHours = clampInt(c.Scheduler.IdempotencyRetentionHours, 0, 87600, 168)
	c.Models.WorkerIdleTimeoutMS = clampInt(c.Models.WorkerIdleTimeoutMS, 0, 24*3600*1000, 60000)
	c.Runtime.StartupTimeoutMS = clampInt(c.Runtime.StartupTimeoutMS, 1000, 120000, 10000)
	c.Runtime.RequestTimeoutMS = clampInt(c.Runtime.RequestTimeoutMS, 1000, 600000, 60000)
	c.Runtime.IdleTimeoutMS = clampInt(c.Runtime.IdleTimeoutMS, 0, 24*3600*1000, 300000)
	c.Maintenance.VacuumThresholdMB = clampInt(c.Maintenance.VacuumThresholdMB, 16, 4096, 256)
	c.Maintenance.QuarantineRetentionHours = clampInt(c.Maintenance.QuarantineRetentionHours, 0, 87600, 168)
	c.OCR.RenderTimeoutMS = clampInt(c.OCR.RenderTimeoutMS, 1000, 600000, 120000)
	c.Helpers.ImageDecoderTimeoutMS = clampInt(c.Helpers.ImageDecoderTimeoutMS, 1000, 600000, 120000)
	switch c.Embedding.Provider {
	case "openai", "ollama", "local", "none":
	default:
		c.Embedding.Provider = "none"
	}
	switch c.Retrieval.Mode {
	case "auto", "hybrid", "vector", "lexical":
	default:
		c.Retrieval.Mode = "auto"
	}
	switch c.Processing.Provider {
	case "builtin", "mineru":
	default:
		c.Processing.Provider = "builtin"
	}
	switch c.Workflow.ConflictStrategy {
	case "keep", "replace", "rename":
	default:
		c.Workflow.ConflictStrategy = "rename"
	}
	switch c.Captioning.Provider {
	case "off", "openai", "ollama":
	default:
		c.Captioning.Provider = "off"
	}
}

func clampInt(v, lo, hi, fallback int) int {
	if v < lo {
		return fallback
	}
	if v > hi {
		return hi
	}
	return v
}

func clampInt64(v, lo, hi, fallback int64) int64 {
	if v < lo {
		return fallback
	}
	if v > hi {
		return hi
	}
	return v
}

func clampFloat(v, lo, hi, fallback float64) float64 {
	if v < lo || v > hi {
		return fallback
	}
	return v
}

// WriteDefault writes the default config to path (used by doctor --init and tests).
func WriteDefault(path string) error {
	data, err := yaml.Marshal(Defaults())
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// EnvPreview returns non-secret effective overrides for doctor output.
func EnvPreview() string {
	parts := make([]string, 0, 4)
	for _, name := range []string{"SHUTU_KNOWLEDGE_HOME", "SHUTU_KNOWLEDGE_LOG_LEVEL", "HF_ENDPOINT"} {
		if v := os.Getenv(name); v != "" {
			parts = append(parts, name+"="+v)
		}
	}
	for _, name := range []string{"KNOWLEDGE_API_KEY", "KNOWLEDGE_RERANK_API_KEY", "SHUTU_KNOWLEDGE_CAPTION_API_KEY"} {
		if _, ok := os.LookupEnv(name); ok {
			parts = append(parts, name+"=<set>")
		}
	}
	return strings.Join(parts, ", ")
}

// FormatInt is a tiny helper for doctor output.
func FormatInt(v int) string { return strconv.Itoa(v) }
