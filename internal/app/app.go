// Package app wires the process-wide components: config, logger, storage,
// raw store, and the health registry. Both the standalone server and the
// Agent-managed extension loop build on it.
package app

import (
	"context"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/shutu-ai/shutu-knowledge/internal/config"
	"github.com/shutu-ai/shutu-knowledge/internal/health"
	"github.com/shutu-ai/shutu-knowledge/internal/jobs"
	"github.com/shutu-ai/shutu-knowledge/internal/knowledge"
	"github.com/shutu-ai/shutu-knowledge/internal/logging"
	"github.com/shutu-ai/shutu-knowledge/internal/models"
	"github.com/shutu-ai/shutu-knowledge/internal/parser"
	"github.com/shutu-ai/shutu-knowledge/internal/runtime"
	"github.com/shutu-ai/shutu-knowledge/internal/storage"
)

// Logger is the slog logger type used across the project.
type Logger = logging.Logger

// App owns the shared runtime components.
type App struct {
	Home      string
	Config    config.Config
	Logger    *Logger
	DB        *storage.DB
	RawStore  *storage.RawFileStore
	Health    *health.Registry
	Jobs      *jobs.Manager
	Knowledge *knowledge.Service
	Models    *models.Manager
	Ollama    *models.Ollama
	Runtime   runtime.Controller
}

// New resolves config, opens storage, and registers core health checkers.
func New(ctx context.Context) (*App, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	home, err := config.DataHome()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(home, 0o755); err != nil {
		return nil, fmt.Errorf("create data home: %w", err)
	}
	logger := logging.New(cfg.Logging.Level)

	db, err := storage.Open(cfg.DatabasePath(home))
	if err != nil {
		return nil, fmt.Errorf("open storage: %w", err)
	}
	raw, err := storage.NewRawFileStore(cfg.RawStoreDir(home))
	if err != nil {
		_ = db.Close()
		return nil, err
	}

	application := &App{Home: home, Config: cfg, Logger: logger, DB: db, RawStore: raw, Health: health.NewRegistry()}
	application.registerHealth()
	application.Jobs = jobs.New(db, cfg.Jobs.ImportWorkers)
	application.Knowledge = knowledge.NewService(db, raw, cfg, application.Jobs)
	application.Jobs.SetFailureObserver(application.Knowledge.ObserveJobFailure)
	if err := application.Jobs.Start(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	application.Models = models.NewManager(application.modelCacheDir(), cfg.Models.HFEndpoint, nil)
	application.Ollama = models.NewOllama("http://127.0.0.1:11434", nil)
	application.Runtime = application.newRuntimeManager()
	application.Knowledge.SetRuntime(application.Runtime)
	application.Knowledge.SetOCRArtifactProvider(application.ocrArtifactPath)
	application.registerOptionalHealth()
	if resumed, failed, err := application.Knowledge.RecoverInterrupted(ctx); err != nil {
		logger.Warn("startup recovery incomplete", "error", err)
	} else if resumed > 0 || failed > 0 {
		logger.Info("startup recovery", "resumed", resumed, "failed", failed)
	}
	if removedRaw, fixedCounts, err := application.Knowledge.ReconcileStorage(); err != nil {
		logger.Warn("storage reconciliation incomplete", "error", err)
	} else if removedRaw > 0 || fixedCounts > 0 {
		logger.Info("storage reconciliation", "removedRaw", removedRaw, "fixedChunkCounts", fixedCounts)
	}
	if maintenance, err := application.DB.MaintainSQLite(
		cfg.Maintenance.FTSAutoOptimize,
		cfg.Maintenance.Vacuum,
		int64(cfg.Maintenance.VacuumThresholdMB)*1024*1024,
	); err != nil {
		logger.Warn("storage maintenance incomplete", "error", err)
	} else if maintenance.FTSOptimized || maintenance.Vacuumed {
		logger.Info("storage maintenance", "ftsOptimized", maintenance.FTSOptimized,
			"vacuumed", maintenance.Vacuumed, "databaseBytes", maintenance.DatabaseBytes)
	}
	return application, nil
}

func (a *App) registerOptionalHealth() {
	a.Health.Register(health.CheckerFunc{
		CheckName: "runtime-embedding",
		Level:     health.Optional,
		Fn: func(ctx context.Context) error {
			return a.probeRuntime(ctx, runtime.CapabilityEmbedding)
		},
	})
	a.Health.Register(health.CheckerFunc{
		CheckName: "runtime-rerank",
		Level:     health.Optional,
		Fn: func(ctx context.Context) error {
			return a.probeRuntime(ctx, runtime.CapabilityRerank)
		},
	})
	a.Health.Register(health.CheckerFunc{
		CheckName: "runtime-ocr",
		Level:     health.Optional,
		Fn: func(ctx context.Context) error {
			return a.probeRuntime(ctx, runtime.CapabilityOCR)
		},
	})
	if a.Config.Helpers.LegacyOffice != "" {
		helper := parser.ExecHelper{
			Template: a.Config.Helpers.LegacyOffice,
		}
		a.Health.Register(health.CheckerFunc{
			CheckName: "helper-legacy-office",
			Level:     health.Optional,
			Fn: func(context.Context) error {
				if !helper.Available() {
					return fmt.Errorf("legacy office helper is not on PATH")
				}
				return nil
			},
		})
	}
	if a.Config.Helpers.ContentConverter != "" {
		helper := parser.ExecHelper{
			Template: a.Config.Helpers.ContentConverter,
		}
		a.Health.Register(health.CheckerFunc{
			CheckName: "helper-content-converter",
			Level:     health.Optional,
			Fn: func(context.Context) error {
				if !helper.Available() {
					return fmt.Errorf("content converter helper is not on PATH")
				}
				return nil
			},
		})
	}
	if a.Config.Helpers.ImageDecoder != "" {
		helper := parser.ExecHelper{
			Template:  a.Config.Helpers.ImageDecoder,
			TimeoutMS: a.Config.Helpers.ImageDecoderTimeoutMS,
		}
		a.Health.Register(health.CheckerFunc{
			CheckName: "helper-image-decoder",
			Level:     health.Optional,
			Fn: func(context.Context) error {
				if !helper.Available() {
					return fmt.Errorf("image decoder helper is not on PATH")
				}
				return nil
			},
		})
	}
	if a.Config.OCR.Helper != "" {
		helper := parser.ExecHelper{
			Template:  a.Config.OCR.Helper,
			TimeoutMS: a.Config.OCR.TimeoutMS,
		}
		a.Health.Register(health.CheckerFunc{
			CheckName: "helper-ocr",
			Level:     health.Optional,
			Fn: func(context.Context) error {
				if !helper.Available() {
					return fmt.Errorf("OCR helper is not on PATH")
				}
				return nil
			},
		})
	}
	if a.Config.OCR.RenderHelper != "" {
		helper := parser.ExecHelper{
			Template:  a.Config.OCR.RenderHelper,
			TimeoutMS: a.Config.OCR.RenderTimeoutMS,
		}
		a.Health.Register(health.CheckerFunc{
			CheckName: "helper-ocr-render",
			Level:     health.Optional,
			Fn: func(context.Context) error {
				if !helper.Available() {
					return fmt.Errorf("OCR renderer helper is not on PATH")
				}
				return nil
			},
		})
	}
	if a.Config.OCR.FallbackHelper != "" {
		helper := parser.ExecHelper{
			Template:  a.Config.OCR.FallbackHelper,
			TimeoutMS: a.Config.OCR.TimeoutMS,
		}
		a.Health.Register(health.CheckerFunc{
			CheckName: "helper-ocr-fallback",
			Level:     health.Optional,
			Fn: func(context.Context) error {
				if !helper.Available() {
					return fmt.Errorf("OCR fallback helper is not on PATH")
				}
				return nil
			},
		})
	}

	// MinerU is configured per base. Doctor/health reports configuration
	// problems without making an outbound API call on every snapshot.
	a.Health.Register(health.CheckerFunc{
		CheckName: "processor-mineru",
		Level:     health.Optional,
		Fn: func(context.Context) error {
			bases, err := a.Knowledge.ListBases()
			if err != nil {
				return err
			}
			for _, base := range bases {
				cfg := knowledge.ResolveBaseConfig(a.Config, base.Config)
				if cfg.Processor != "mineru" || strings.TrimSpace(cfg.MineruAPIKey) == "" {
					continue
				}
				host := strings.TrimSpace(cfg.MineruAPIHost)
				if host == "" {
					host = "https://mineru.net"
				}
				if parsed, err := url.Parse(host); err != nil || parsed.Scheme != "https" || parsed.Host == "" {
					return fmt.Errorf("invalid mineru API host %q", host)
				}
			}
			return nil
		},
	})
}

func (a *App) probeRuntime(ctx context.Context, capability string) error {
	if a.Runtime == nil || !a.Runtime.Configured(capability) {
		return nil
	}
	_, err := a.Runtime.Probe(ctx, capability)
	return err
}

func (a *App) ocrArtifactPath() (string, bool) {
	if a.Models == nil {
		return "", false
	}
	model, err := a.Models.OCRStatus()
	if err != nil || model.Status != "ready" {
		return "", false
	}
	return filepath.Join(a.Models.Root(), filepath.FromSlash(models.OCRModelID)), true
}

func (a *App) newRuntimeManager() *runtime.Manager {
	duration := func(ms, fallback int) time.Duration {
		if ms <= 0 {
			ms = fallback
		}
		return time.Duration(ms) * time.Millisecond
	}
	return runtime.NewManager(runtime.Options{
		Command:          a.Config.Runtime.HelperCommand,
		EmbeddingCommand: a.Config.Runtime.EmbeddingHelper,
		RerankCommand:    a.Config.Runtime.RerankHelper,
		OCRCommand:       a.Config.Runtime.OCRHelper,
		StartupTimeout:   duration(a.Config.Runtime.StartupTimeoutMS, 10000),
		RequestTimeout:   duration(a.Config.Runtime.RequestTimeoutMS, 60000),
		IdleTimeout:      duration(a.Config.Runtime.IdleTimeoutMS, 300000),
	})
}

func (a *App) registerHealth() {
	a.Health.Register(health.CheckerFunc{
		CheckName: "database",
		Level:     health.Critical,
		Fn: func(context.Context) error {
			return a.DB.Ping()
		},
	})
	a.Health.Register(health.CheckerFunc{
		CheckName: "schema",
		Level:     health.Critical,
		Fn: func(context.Context) error {
			v, err := storage.SchemaVersion(a.DB.DB)
			if err != nil {
				return err
			}
			if v < 1 {
				return fmt.Errorf("schema not migrated (version %d)", v)
			}
			return nil
		},
	})
	a.Health.Register(health.CheckerFunc{
		CheckName: "storage-permissions",
		Level:     health.Critical,
		Fn: func(context.Context) error {
			probe := filepath.Join(a.Home, ".healthcheck")
			if err := os.WriteFile(probe, []byte("ok"), 0o644); err != nil {
				return fmt.Errorf("data home not writable: %w", err)
			}
			return os.Remove(probe)
		},
	})
	// Index and model subsystems register in their own phases; optional
	// checks degrade without flipping readiness.
}

// UpdateConfig persists runtime settings in the Knowledge data domain and
// applies them without requiring a process restart.
func (a *App) UpdateConfig(cfg config.Config) error {
	cfg = cfg.Normalized()
	if err := config.Save(filepath.Join(a.Home, "config.yaml"), cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	a.Config = cfg
	if a.Knowledge != nil {
		a.Knowledge.SetGlobalConfig(cfg)
	}
	if a.Models != nil {
		a.Models = models.NewManager(a.modelCacheDir(), cfg.Models.HFEndpoint, nil)
	}
	if a.Runtime != nil {
		a.Runtime.Close()
		a.Runtime = a.newRuntimeManager()
	}
	if a.Knowledge != nil {
		a.Knowledge.SetRuntime(a.Runtime)
	}
	return nil
}

func (a *App) modelCacheDir() string {
	return a.resolveModelCacheDir(a.Config.Models.CacheDir)
}

func (a *App) resolveModelCacheDir(path string) string {
	if path = strings.TrimSpace(path); path != "" {
		if filepath.IsAbs(path) {
			return filepath.Clean(path)
		}
		return filepath.Join(a.Home, path)
	}
	return filepath.Join(a.Home, "models")
}

func (a *App) PlanModelCacheMigration(target string) (models.MigrationPlan, error) {
	return a.Models.PlanMigration(a.resolveModelCacheDir(target))
}

func (a *App) MigrateModelCache(target string, removeSource bool) (models.MigrationResult, error) {
	resolved := a.resolveModelCacheDir(target)
	result, err := a.Models.Migrate(resolved, removeSource)
	if err != nil {
		return result, err
	}
	previous := a.Config.Models.CacheDir
	a.Config.Models.CacheDir = resolved
	if err := config.Save(filepath.Join(a.Home, "config.yaml"), a.Config); err != nil {
		a.Config.Models.CacheDir = previous
		return result, fmt.Errorf("save migrated model cache: %w", err)
	}
	a.Models = models.NewManager(resolved, a.Config.Models.HFEndpoint, nil)
	return result, nil
}

// LocalModelView is a cache model plus the latest persisted reranker test.
type LocalModelView struct {
	models.Model
	SelfTest *knowledge.RerankSelfTest `json:"selfTest,omitempty"`
}

// ListLocalModels augments artifact state with custom registrations and
// reranker self-test status. Registration and artifact readiness remain distinct.
func (a *App) ListLocalModels() ([]LocalModelView, error) {
	items, err := a.Models.List()
	if err != nil {
		return nil, err
	}
	custom, err := a.Knowledge.ListCustomRerankers()
	if err != nil {
		return nil, err
	}
	views := make([]LocalModelView, 0, len(items)+len(custom))
	seen := map[string]bool{}
	for _, model := range items {
		seen[model.ID] = true
		view := LocalModelView{Model: model}
		if model.Kind == models.KindRerank {
			test, err := a.Knowledge.GetRerankSelfTest(
				model.ID, model.SizeBytes, model.Downloaded, len(model.Artifacts),
			)
			if err != nil {
				return nil, err
			}
			view.SelfTest = &test
		}
		views = append(views, view)
	}
	for _, item := range custom {
		if !seen[item.ID] {
			views = append(views, LocalModelView{Model: models.Model{
				ID: item.ID, Kind: models.KindRerank, Status: "registered",
				Artifacts: []string{}, Downloaded: 0,
			}})
		}
	}
	return views, nil
}

// SelfTestReranker asks the isolated helper to score relevant and irrelevant
// samples. A passing test requires finite, ordered, differentiated scores.
func (a *App) SelfTestReranker(ctx context.Context, id string) (knowledge.RerankSelfTest, error) {
	id = strings.TrimPrefix(strings.TrimSpace(id), "local:")
	model, err := a.Models.Get(id)
	if err != nil {
		return knowledge.RerankSelfTest{}, fmt.Errorf("local reranker artifacts are not downloaded")
	}
	if model.Kind != models.KindRerank || model.Status != "ready" {
		return knowledge.RerankSelfTest{}, fmt.Errorf("local reranker artifacts are incomplete")
	}
	if a.Runtime == nil || !a.Runtime.Configured(runtime.CapabilityRerank) {
		return knowledge.RerankSelfTest{}, fmt.Errorf("local rerank runtime is not configured")
	}

	query := "How do I submit an expense report?"
	texts := []string{
		"Expense reimbursement requires an invoice and manager approval.",
		"Sunny weather is suitable for an outdoor walk.",
	}
	var payload struct {
		Scores []float64 `json:"scores"`
	}
	started := time.Now()
	err = a.Runtime.Call(ctx, runtime.CapabilityRerank, map[string]any{
		"model": id, "query": query, "documents": texts,
	}, &payload)
	if err != nil {
		return knowledge.RerankSelfTest{}, fmt.Errorf("rerank self-test failed: %w", err)
	}
	latency := time.Since(started).Milliseconds()
	result := knowledge.RerankSelfTest{
		ID: id, LatencyMS: latency, ArtifactBytes: model.SizeBytes,
		DownloadedAt: model.Downloaded, ArtifactCount: len(model.Artifacts),
	}
	healthy := len(payload.Scores) == 2
	if healthy {
		for _, score := range payload.Scores {
			if math.IsNaN(score) || math.IsInf(score, 0) || score < 0 || score > 1 {
				healthy = false
				break
			}
		}
	}
	if healthy {
		healthy = payload.Scores[0] > payload.Scores[1] &&
			payload.Scores[0]-payload.Scores[1] >= 1e-6
	}
	if !healthy {
		result.Error = "rerank self-test did not return two distinct valid scores in relevant-to-irrelevant order"
		if err := a.Knowledge.SaveRerankSelfTest(result); err != nil {
			return result, err
		}
		return result, fmt.Errorf("%s", result.Error)
	}
	result.Healthy = true
	result.Scores = payload.Scores
	result.Current = true
	if err := a.Knowledge.SaveRerankSelfTest(result); err != nil {
		return result, err
	}
	return result, nil
}

// Close releases all resources.
func (a *App) Close() {
	if a.Runtime != nil {
		a.Runtime.Close()
	}
	if a.Jobs != nil {
		a.Jobs.Stop()
	}
	if a.DB != nil {
		_ = a.DB.Close()
	}
}
