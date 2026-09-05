// Package config implements Knowledge-owned configuration: built-in
// defaults, an optional config.yaml in the data domain, then environment
// overrides. The Agent never owns or stores these business fields.
package config

import (
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
		Path string `yaml:"path"`
	} `yaml:"database"`

	Logging struct {
		// Level: debug, info, warn, error.
		Level string `yaml:"level"`
	} `yaml:"logging"`

	Server struct {
		// Addr of the standalone HTTP server (serve mode).
		Addr string `yaml:"addr"`
	} `yaml:"server"`

	Embedding struct {
		Provider string `yaml:"provider"` // openai | ollama | local | none
		BaseURL  string `yaml:"baseUrl"`
		Model    string `yaml:"model"`
		APIKey   string `yaml:"apiKey"`
		Batch    int    `yaml:"batch"`
	} `yaml:"embedding"`

	Rerank struct {
		Model      string `yaml:"model"`
		BaseURL    string `yaml:"baseUrl"`
		APIKey     string `yaml:"apiKey"`
		TimeoutMS  int    `yaml:"timeoutMs"`
		Enabled    bool   `yaml:"enabled"`
		Required   bool   `yaml:"required"`
		QueueLimit int    `yaml:"queueLimit"`
	} `yaml:"rerank"`

	Chunking struct {
		Smart             bool    `yaml:"smart"`
		Separator         string  `yaml:"separator"`
		Size              int     `yaml:"size"`
		Overlap           int     `yaml:"overlap"`
		Semantic          bool    `yaml:"semantic"`
		SemanticThreshold float64 `yaml:"semanticThreshold"`
		TokenLimit        int     `yaml:"tokenLimit"`
	} `yaml:"chunking"`

	Retrieval struct {
		TopK               int     `yaml:"topK"`
		Mode               string  `yaml:"mode"` // auto | hybrid | vector | lexical
		SimilarityMin      float64 `yaml:"similarityMin"`
		MMR                bool    `yaml:"mmr"`
		MMRDiversity       float64 `yaml:"mmrDiversity"`
		RRFVectorWeight    float64 `yaml:"rrfVectorWeight"`
		SiblingChunks      int     `yaml:"siblingChunks"`
		ContextTimeoutMS   int     `yaml:"contextTimeoutMs"`
	} `yaml:"retrieval"`

	Jobs struct {
		ImportWorkers   int `yaml:"importWorkers"`
		ResumeInterrupt bool `yaml:"resumeInterrupt"`
	} `yaml:"jobs"`

	Models struct {
		CacheDir           string `yaml:"cacheDir"`
		HFEndpoint         string `yaml:"hfEndpoint"`
		WorkerIdleTimeoutMS int   `yaml:"workerIdleTimeoutMs"`
	} `yaml:"models"`

	OCR struct {
		Enabled bool `yaml:"enabled"`
	} `yaml:"ocr"`
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
	c.Chunking.Smart = true
	c.Chunking.Separator = "\n\n"
	c.Chunking.Size = 800
	c.Chunking.Overlap = 100
	c.Chunking.SemanticThreshold = 0.75
	c.Retrieval.TopK = 4
	c.Retrieval.Mode = "auto"
	c.Retrieval.RRFVectorWeight = 1
	c.Retrieval.SiblingChunks = 1
	c.Retrieval.ContextTimeoutMS = 4000
	c.Jobs.ImportWorkers = 5
	c.Jobs.ResumeInterrupt = true
	c.Models.WorkerIdleTimeoutMS = 60000
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
	if v := os.Getenv("HF_ENDPOINT"); v != "" {
		cfg.Models.HFEndpoint = v
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
	c.Jobs.ImportWorkers = clampInt(c.Jobs.ImportWorkers, 1, 32, 5)
	c.Models.WorkerIdleTimeoutMS = clampInt(c.Models.WorkerIdleTimeoutMS, 0, 24*3600*1000, 60000)
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
	for _, name := range []string{"KNOWLEDGE_API_KEY", "KNOWLEDGE_RERANK_API_KEY"} {
		if _, ok := os.LookupEnv(name); ok {
			parts = append(parts, name+"=<set>")
		}
	}
	return strings.Join(parts, ", ")
}

// FormatInt is a tiny helper for doctor output.
func FormatInt(v int) string { return strconv.Itoa(v) }
