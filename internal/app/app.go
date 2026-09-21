// Package app wires the process-wide components: config, logger, storage,
// raw store, and the health registry. Both the standalone server and the
// Agent-managed extension loop build on it.
package app

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/shutu-ai/shutu-knowledge/internal/config"
	"github.com/shutu-ai/shutu-knowledge/internal/health"
	"github.com/shutu-ai/shutu-knowledge/internal/knowledge"
	"github.com/shutu-ai/shutu-knowledge/internal/logging"
	"github.com/shutu-ai/shutu-knowledge/internal/models"
	"github.com/shutu-ai/shutu-knowledge/internal/operations"
	"github.com/shutu-ai/shutu-knowledge/internal/parser"
	"github.com/shutu-ai/shutu-knowledge/internal/runtime"
	"github.com/shutu-ai/shutu-knowledge/internal/scheduler"
	"github.com/shutu-ai/shutu-knowledge/internal/storage"
)

// Logger is the slog logger type used across the project.
type Logger = logging.Logger

// App owns the shared runtime components.
type App struct {
	Home           string
	Config         config.Config
	Logger         *Logger
	DB             *storage.DB
	RawStore       *storage.RawFileStore
	Health         *health.Registry
	Operations     *operations.Service
	Knowledge      *knowledge.Service
	Models         *models.Manager
	Ollama         *models.Ollama
	Runtime        runtime.Controller
	instanceLock   *storage.InstanceLock
	modelAdmission scheduler.Admission
	// ManagedRuntime reports whether Knowledge prepared its own pinned runtime
	// rather than using an explicitly configured deployment helper.
	ManagedRuntime    bool
	startupOnce       sync.Once
	startupDone       chan struct{}
	startupCancel     context.CancelFunc
	lifecycleMu       sync.RWMutex
	startupErr        error
	maintenanceOnce   sync.Once
	maintenanceDone   chan struct{}
	maintenanceCancel context.CancelFunc
	// deleteDocumentBoundary is a test-only synchronization point at the
	// durable fence/cleanup boundary. Production leaves it nil.
	deleteDocumentBoundary func()
}

// New resolves config, opens storage, and registers core health checkers.
func New(ctx context.Context) (*App, error) {
	return NewWithOptions(ctx, Options{})
}

// Options controls whether potentially expensive startup reconciliation runs
// before New returns. Agent extensions use deferred startup so their web
// endpoint can be published before a large database is scanned.
type Options struct {
	DeferStartupRecovery bool
}

// NewWithOptions resolves config, opens storage, and registers core health
// checkers with the requested startup behavior.
func NewWithOptions(ctx context.Context, options Options) (*App, error) {
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

	instanceLock, err := storage.AcquireInstanceLock(filepath.Join(home, "instance.lock.db"))
	if err != nil {
		return nil, err
	}

	db, err := storage.Open(cfg.DatabasePath(home))
	if err != nil {
		_ = instanceLock.Release()
		return nil, fmt.Errorf("open storage: %w", err)
	}
	raw, err := storage.NewRawFileStore(cfg.RawStoreDir(home))
	if err != nil {
		_ = db.Close()
		_ = instanceLock.Release()
		return nil, err
	}

	application := &App{Home: home, Config: cfg, Logger: logger, DB: db, RawStore: raw, Health: health.NewRegistry(), instanceLock: instanceLock}
	application.registerHealth()
	application.Knowledge = knowledge.NewService(db, raw, cfg)
	application.modelAdmission = scheduler.NewSemaphore(cfg.Scheduler.Model)
	application.Knowledge.SetSharedModelAdmission(application.modelAdmission)
	application.Operations, err = application.newOperationService()
	if err != nil {
		_ = db.Close()
		_ = instanceLock.Release()
		return nil, err
	}
	application.Models = models.NewManager(application.modelCacheDir(), cfg.Models.HFEndpoint, nil)
	application.Ollama = models.NewOllama("http://127.0.0.1:11434", nil)
	application.Runtime = application.newRuntimeManager()
	application.Knowledge.SetRuntime(application.Runtime)
	if strings.TrimSpace(cfg.Helpers.LegacyOffice) == "" {
		application.Knowledge.SetLegacyOfficeRunner(nil)
	}
	// The managed runtime owns Tesseract.js language data in its private
	// runtime cache; it must not be gated on the legacy PaddleOCR artifact
	// bundle exposed by the Models page. Explicit helper deployments retain
	// the artifact callback so their helper can receive modelPath.
	if !application.ManagedRuntime {
		application.Knowledge.SetOCRArtifactProvider(application.ocrArtifactPath)
	}
	application.registerOptionalHealth()
	// Start durable workers only after every dependency they may call has been
	// fully wired. Starting recovery dispatch before SetRuntime finishes lets a
	// recovered operation race provider configuration during a second-process
	// startup.
	if err := application.Operations.Start(ctx); err != nil {
		if application.Runtime != nil {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
			_ = closeRuntimeWithContext(cleanupCtx, application.Runtime)
			cleanupCancel()
		}
		_ = db.Close()
		_ = instanceLock.Release()
		return nil, err
	}
	if options.DeferStartupRecovery {
		return application, nil
	}
	application.runStartupRecovery(ctx)
	application.startupOnce.Do(func() {})
	return application, nil
}

// StartBackgroundRecovery defers lightweight document recovery until after the
// HTTP listener is available. Full storage reconciliation is intentionally not
// part of startup: it walks every raw file and counts chunks for every
// document, which can monopolize disk and the SQLite read pool for a large
// knowledge base. Reconciliation remains available as an explicit service
// operation for maintenance workflows.
func (a *App) StartBackgroundRecovery() {
	a.startupOnce.Do(func() {
		done := make(chan struct{})
		startupCtx, cancel := context.WithCancel(context.Background())
		a.lifecycleMu.Lock()
		a.startupDone = done
		a.startupCancel = cancel
		a.lifecycleMu.Unlock()
		go func() {
			defer close(done)
			a.runStartupRecovery(startupCtx)
		}()
	})
}

func (a *App) runStartupRecovery(ctx context.Context) {
	if resumed, failed, err := a.Knowledge.RecoverInterrupted(ctx); err != nil {
		a.lifecycleMu.Lock()
		a.startupErr = fmt.Errorf("storage recovery incomplete: %w", err)
		a.lifecycleMu.Unlock()
		a.Logger.Warn("startup recovery incomplete", "error", err)
	} else if resumed > 0 || failed > 0 {
		a.Logger.Info("startup recovery", "resumed", resumed, "failed", failed)
	}
}

func (a *App) startupError() error {
	a.lifecycleMu.RLock()
	defer a.lifecycleMu.RUnlock()
	return a.startupErr
}

// StartupInProgress reports whether deferred startup recovery is still
// running. It is deliberately a non-blocking check for HTTP and Agent health
// handlers.
func (a *App) StartupInProgress() bool {
	a.lifecycleMu.RLock()
	done := a.startupDone
	a.lifecycleMu.RUnlock()
	if done == nil {
		return false
	}
	select {
	case <-done:
		return false
	default:
		return true
	}
}

// HealthSnapshot keeps readiness probes non-blocking while deferred startup
// work runs. The database-backed checks run once recovery has completed.
func (a *App) HealthSnapshot(ctx context.Context) health.Report {
	if a.StartupInProgress() {
		return health.Report{
			Ready:  false,
			Status: "starting",
			Components: []health.Component{{
				Name:   "startup-recovery",
				Status: "ok",
				Detail: "storage recovery is running in the background",
			}},
		}
	}
	if err := a.startupError(); err != nil {
		return health.Report{
			Ready:  false,
			Status: "unhealthy: startup-recovery",
			Components: []health.Component{{
				Name:   "startup-recovery",
				Status: "failed",
				Detail: err.Error(),
			}},
		}
	}
	return a.Health.Snapshot(ctx)
}

// StartBackgroundMaintenance defers potentially long-running SQLite vacuum
// work until the service is listening. Extension protocol initialization must
// not wait for maintenance of a large database.
func (a *App) StartBackgroundMaintenance() {
	a.maintenanceOnce.Do(func() {
		done := make(chan struct{})
		maintenanceCtx, cancel := context.WithCancel(context.Background())
		a.lifecycleMu.Lock()
		a.maintenanceDone = done
		a.maintenanceCancel = cancel
		a.lifecycleMu.Unlock()
		go func() {
			defer close(done)
			payload, err := json.Marshal(storageMaintenanceCommand{
				SQLiteOnly:      true,
				OptimizeFTS:     a.Config.Maintenance.FTSAutoOptimize,
				Vacuum:          a.Config.Maintenance.Vacuum,
				VacuumThreshold: int64(a.Config.Maintenance.VacuumThresholdMB) * 1024 * 1024,
			})
			if err != nil {
				a.Logger.Warn("storage maintenance admission failed", "error", err)
				return
			}
			op, err := a.Operations.Submit(maintenanceCtx, operations.Request{
				Type:                 "maintenance_storage",
				CommandSchemaVersion: operations.CommandSchemaV1,
				Payload:              payload,
				IdempotencyKey: fmt.Sprintf("startup-storage-maintenance-v1-%t-%t-%d",
					a.Config.Maintenance.FTSAutoOptimize, a.Config.Maintenance.Vacuum,
					a.Config.Maintenance.VacuumThresholdMB),
				ResourceClass: operations.ResourceMaintenance,
				Priority:      -100,
			})
			if err != nil {
				a.Logger.Warn("storage maintenance admission failed", "error", err)
				return
			}
			for {
				switch op.State {
				case operations.StateSucceeded:
					a.Logger.Info("storage maintenance operation completed", "operationId", op.ID)
					return
				case operations.StateFailed, operations.StateCancelled:
					a.Logger.Warn("storage maintenance incomplete", "operationId", op.ID,
						"state", op.State, "error", op.ErrorMessage)
					return
				}
				timer := time.NewTimer(100 * time.Millisecond)
				select {
				case <-maintenanceCtx.Done():
					return
				case <-timer.C:
				}
				next, getErr := a.Operations.GetContext(maintenanceCtx, op.ID)
				if getErr != nil {
					a.Logger.Warn("storage maintenance status unavailable", "operationId", op.ID, "error", getErr)
					return
				}
				op = next
			}
		}()
	})
}

func (a *App) registerOptionalHealth() {
	if !a.ManagedRuntime {
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
		a.Health.Register(health.CheckerFunc{
			CheckName: "runtime-pdf-render",
			Level:     health.Optional,
			Fn: func(ctx context.Context) error {
				return a.probeRuntime(ctx, runtime.CapabilityPDFRender)
			},
		})
	}
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

	// Keep absent optional integrations visible to doctor and the health
	// endpoint. An omitted command is a degraded capability, never a ready
	// runtime, while the core service remains healthy.
	registerUnavailable := func(name, detail string) {
		a.Health.Register(health.CheckerFunc{
			CheckName: name,
			Level:     health.Optional,
			Fn:        func(context.Context) error { return fmt.Errorf("%s", detail) },
		})
	}
	if strings.TrimSpace(a.Config.Helpers.LegacyOffice) == "" {
		if !a.ManagedRuntime {
			office := parser.NewLibreOfficeHelper()
			a.Health.Register(health.CheckerFunc{
				CheckName: "helper-legacy-office",
				Level:     health.Optional,
				Fn: func(context.Context) error {
					if !office.Available() {
						return fmt.Errorf("LibreOffice is not detected (AUTO_MANAGED_EXTERNAL_RUNTIME); install LibreOffice and rerun doctor")
					}
					return nil
				},
			})
		}
		// LLM enrichment is deliberately optional. The deterministic built-in
		// summaries remain part of the core path even when no enrichment provider
		// is configured, so this must report degraded rather than fail readiness.
		a.Health.Register(health.CheckerFunc{
			CheckName: "document-enrichment",
			Level:     health.Optional,
			Fn: func(context.Context) error {
				return fmt.Errorf("optional LLM enrichment unavailable; built-in deterministic understanding is active")
			},
		})
	}
	if strings.TrimSpace(a.Config.Helpers.ContentConverter) == "" {
		registerUnavailable("helper-content-converter", "PDF content converter is not configured (MANUAL_EXTERNAL_RUNTIME)")
	}
	if strings.TrimSpace(a.Config.Helpers.ImageDecoder) == "" && !a.ManagedRuntime {
		registerUnavailable("helper-image-decoder", "JBIG2/JPX image decoder is not configured (MANUAL_EXTERNAL_RUNTIME)")
	}
	if strings.TrimSpace(a.Config.OCR.Helper) == "" && strings.TrimSpace(a.Config.Runtime.OCRHelper) == "" && strings.TrimSpace(a.Config.Runtime.HelperCommand) == "" &&
		(a.Runtime == nil || !a.Runtime.Configured(runtime.CapabilityOCR)) {
		registerUnavailable("helper-ocr", "OCR runtime is not configured (MANUAL_EXTERNAL_RUNTIME)")
	}
	if strings.TrimSpace(a.Config.OCR.RenderHelper) == "" && (a.Runtime == nil || !a.Runtime.Configured(runtime.CapabilityPDFRender)) {
		registerUnavailable("helper-ocr-render", "full-page PDF renderer is not configured (MANUAL_EXTERNAL_RUNTIME)")
	}
	if strings.TrimSpace(a.Config.OCR.FallbackHelper) == "" && (a.Runtime == nil || !a.Runtime.Configured(runtime.CapabilityOCR)) {
		registerUnavailable("helper-ocr-fallback", "OCR fallback runtime is not configured (MANUAL_EXTERNAL_RUNTIME)")
	}

	// MinerU is configured per base. Doctor/health reports configuration
	// problems without making an outbound API call on every snapshot.
	a.Health.Register(health.CheckerFunc{
		CheckName: "processor-mineru",
		Level:     health.Optional,
		Fn: func(ctx context.Context) error {
			bases, err := a.Knowledge.ListBasesContext(ctx)
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
		return fmt.Errorf("%s runtime is not configured; install or configure a Knowledge-owned runtime", capability)
	}
	_, err := a.Runtime.Probe(ctx, capability)
	return err
}

func (a *App) ocrArtifactPath() (string, bool) {
	if a.Models == nil {
		return "", false
	}
	model, err := a.Models.OCRStatus()
	if err != nil || model.Status != "installed" {
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
	command := strings.TrimSpace(a.Config.Runtime.HelperCommand)
	a.ManagedRuntime = false
	if command == "" && os.Getenv("SHUTU_KNOWLEDGE_DISABLE_MANAGED_RUNTIME") != "1" && strings.TrimSpace(a.Config.Runtime.EmbeddingHelper) == "" &&
		strings.TrimSpace(a.Config.Runtime.RerankHelper) == "" && strings.TrimSpace(a.Config.Runtime.OCRHelper) == "" {
		managed, err := runtime.PrepareManagedRuntime(context.Background(), a.Home, a.modelCacheDir())
		if err != nil {
			a.Logger.Warn("managed runtime unavailable", "error", err)
		} else {
			command = managed
			a.ManagedRuntime = true
			if a.Config.Runtime.Offline {
				command += " --offline"
			}
		}
	}
	startupTimeout := duration(a.Config.Runtime.StartupTimeoutMS, 10000)
	// The first managed request may perform npm ci into the private data home.
	// Do not let the normal helper handshake budget kill that installation.
	if a.ManagedRuntime && startupTimeout < 120*time.Second {
		startupTimeout = 120 * time.Second
	}
	return runtime.NewManager(runtime.Options{
		Command:          command,
		EmbeddingCommand: a.Config.Runtime.EmbeddingHelper,
		RerankCommand:    a.Config.Runtime.RerankHelper,
		OCRCommand:       a.Config.Runtime.OCRHelper,
		StartupTimeout:   startupTimeout,
		RequestTimeout:   duration(a.Config.Runtime.RequestTimeoutMS, 60000),
		ModelLoadTimeout: func() time.Duration {
			// A first install may download over a gigabyte of model weights;
			// keep normal inference timeouts short while giving model lifecycle
			// operations an explicit, cancellable budget.
			configured := duration(a.Config.Runtime.RequestTimeoutMS, 60000)
			if configured < 30*time.Minute {
				return 30 * time.Minute
			}
			return configured
		}(),
		IdleTimeout: duration(a.Config.Runtime.IdleTimeoutMS, 300000),
		// Managed ONNX runtimes can retain native allocator arenas across
		// requests. A bounded process generation keeps long import jobs from
		// growing without bound while still amortizing model loads.
		MaxRequestsPerProcess: func() int {
			if a.ManagedRuntime {
				return 64
			}
			return 0
		}(),
	})
}

func (a *App) registerHealth() {
	a.Health.Register(health.CheckerFunc{
		CheckName: "database",
		Level:     health.Critical,
		Fn: func(ctx context.Context) error {
			return a.DB.PingContext(ctx)
		},
	})
	a.Health.Register(health.CheckerFunc{
		CheckName: "schema",
		Level:     health.Critical,
		Fn: func(ctx context.Context) error {
			v, err := storage.SchemaVersionContext(ctx, a.DB.ReadDB())
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
	a.Health.Register(health.CheckerFunc{
		CheckName: "storage-format",
		Level:     health.Critical,
		Fn: func(ctx context.Context) error {
			_, err := storage.StorageFormatContext(ctx, a.DB.ReadDB())
			return err
		},
	})
	// Document intelligence is a core, local capability. Keep its storage
	// checks critical so doctor and /api/status distinguish a missing 0.3
	// foundation from optional model enrichment.
	for _, checkName := range []string{"document-parser", "document-ir", "structured-index"} {
		name := checkName
		a.Health.Register(health.CheckerFunc{
			CheckName: name,
			Level:     health.Critical,
			Fn: func(ctx context.Context) error {
				version, err := storage.SchemaVersionContext(ctx, a.DB.ReadDB())
				if err != nil {
					return err
				}
				if version < 18 {
					return fmt.Errorf("document intelligence migration unavailable (schema version %d)", version)
				}
				return nil
			},
		})
	}
	// Index and model subsystems register in their own phases; optional
	// checks degrade without flipping readiness.
}

// UpdateConfig persists runtime settings in the Knowledge data domain and
// applies them without requiring a process restart.
func (a *App) UpdateConfig(cfg config.Config) error {
	return a.UpdateConfigWithContext(context.Background(), cfg)
}

// UpdateConfigWithContext persists and applies runtime settings inside the
// request or durable-operation cancellation boundary.
func (a *App) UpdateConfigWithContext(ctx context.Context, cfg config.Config) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	cfg = cfg.Normalized()
	if err := config.SaveContext(ctx, filepath.Join(a.Home, "config.yaml"), cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	if a.Runtime != nil {
		closeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		if err := closeRuntimeWithContext(closeCtx, a.Runtime); err != nil {
			cancel()
			return fmt.Errorf("runtime shutdown incomplete: %w", err)
		}
		cancel()
	}
	a.Config = cfg
	if a.Knowledge != nil {
		a.Knowledge.SetGlobalConfig(cfg)
	}
	if a.Models != nil {
		a.Models = models.NewManager(a.modelCacheDir(), cfg.Models.HFEndpoint, nil)
	}
	if a.Runtime != nil {
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

// ResolveModelCacheDir lets transport adapters persist an absolute migration
// target so recovery does not reinterpret it after configuration changes.
func (a *App) ResolveModelCacheDir(path string) string {
	return a.resolveModelCacheDir(path)
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
	return a.MigrateModelCacheContext(context.Background(), target, removeSource)
}

// MigrateModelCacheContext keeps the model-cache move inside the durable
// operation context. The new config is persisted only after the target cache
// has been copied and verified, but before an explicitly requested source
// removal, so replay never has to infer ownership from a half-copied cache.
func (a *App) MigrateModelCacheContext(ctx context.Context, target string, removeSource bool) (models.MigrationResult, error) {
	resolved := a.resolveModelCacheDir(target)
	nextConfig := a.Config
	nextConfig.Models.CacheDir = resolved
	if modelCachePathsEqual(a.Models.Root(), resolved) {
		if err := config.SaveContext(ctx, filepath.Join(a.Home, "config.yaml"), nextConfig); err != nil {
			return models.MigrationResult{}, fmt.Errorf("save migrated model cache: %w", err)
		}
		a.Config = nextConfig
		modelCount, err := a.Models.CountContext(ctx)
		if err != nil {
			return models.MigrationResult{}, err
		}
		return models.MigrationResult{SourceDir: resolved, TargetDir: resolved, ModelCount: modelCount}, nil
	}
	result, err := a.Models.MigrateContext(ctx, resolved, removeSource, func() error {
		if err := config.SaveContext(ctx, filepath.Join(a.Home, "config.yaml"), nextConfig); err != nil {
			return fmt.Errorf("save migrated model cache: %w", err)
		}
		return nil
	})
	if err != nil {
		return result, err
	}
	a.Config = nextConfig
	a.Models = models.NewManager(resolved, a.Config.Models.HFEndpoint, nil)
	return result, nil
}

func modelCachePathsEqual(left, right string) bool {
	left, _ = filepath.Abs(filepath.Clean(left))
	right, _ = filepath.Abs(filepath.Clean(right))
	if filepath.Separator == '\\' {
		return strings.EqualFold(left, right)
	}
	return left == right
}

// LocalModelView is a cache model plus the latest persisted reranker test.
type LocalModelView struct {
	models.Model
	SelfTest *knowledge.RerankSelfTest `json:"selfTest,omitempty"`
}

// LocalModelsPage is the bounded control-plane view returned by the model
// picker. Runtime/configured entries may be appended to the first page, while
// manifest-backed cache entries are paged by the model manager.
type LocalModelsPage struct {
	Models     []LocalModelView `json:"models"`
	NextOffset int              `json:"nextOffset"`
	HasMore    bool             `json:"hasMore"`
}

type managedRuntimeState struct {
	Components map[string]managedRuntimeComponent `json:"components"`
}

type managedRuntimeComponent struct {
	Lifecycle string `json:"lifecycle"`
	Ready     bool   `json:"ready"`
	Model     string `json:"model"`
	Runtime   string `json:"runtime"`
	LastError string `json:"lastError"`
}

// cachedManagedModels restores model entries before the managed helper starts.
// Transformers.js stores managed artifacts below <model>/<revision> and does
// not create the manifest consumed by models.Manager, so the persisted runtime
// state is the durable catalog while the cache directory proves it is local.
func cachedManagedModels(home, cacheDir string) []LocalModelView {
	data, err := os.ReadFile(filepath.Join(home, "runtime", "runtime-state.json"))
	if err != nil {
		return nil
	}
	var state managedRuntimeState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil
	}
	views := make([]LocalModelView, 0, 2)
	for capability, component := range state.Components {
		kind := ""
		switch capability {
		case runtime.CapabilityEmbedding:
			kind = models.KindEmbedding
		case runtime.CapabilityRerank:
			kind = models.KindRerank
		default:
			continue
		}
		id := strings.TrimPrefix(strings.TrimSpace(component.Model), "local:")
		if id == "" || !safeModelCachePath(cacheDir, id) {
			continue
		}
		if _, err := os.Stat(filepath.Join(cacheDir, filepath.FromSlash(id))); err != nil {
			continue
		}
		lifecycle := strings.ToUpper(strings.TrimSpace(component.Lifecycle))
		if lifecycle == "" {
			lifecycle = models.LifecycleInstalled
		}
		status := "installed"
		if lifecycle == models.LifecycleFailed {
			status = "incomplete"
		}
		views = append(views, LocalModelView{Model: models.Model{
			ID: id, Kind: kind, Status: status, Lifecycle: lifecycle,
			Ready: component.Ready, Runtime: "RUNTIME_CACHED", LastError: component.LastError,
		}})
	}
	return views
}

// ManagedModelCapability identifies a managed-runtime model without probing
// the helper. Model removal is a control-plane operation; it must classify a
// cached model from configuration or the persisted runtime catalog rather than
// running a potentially expensive health/inference call in an HTTP handler.
func (a *App) ManagedModelCapability(id string) string {
	if !a.ManagedRuntime {
		return ""
	}
	id = strings.TrimPrefix(strings.TrimSpace(id), "local:")
	if id == "" {
		return ""
	}
	candidates := []struct {
		id, capability string
	}{
		{id: a.Config.Embedding.Model, capability: runtime.CapabilityEmbedding},
		{id: a.Config.Rerank.Model, capability: runtime.CapabilityRerank},
	}
	for _, candidate := range candidates {
		if strings.TrimPrefix(strings.TrimSpace(candidate.id), "local:") == id {
			return candidate.capability
		}
	}
	for _, view := range cachedManagedModels(a.Home, a.modelCacheDir()) {
		if view.ID != id {
			continue
		}
		if view.Kind == models.KindEmbedding {
			return runtime.CapabilityEmbedding
		}
		if view.Kind == models.KindRerank {
			return runtime.CapabilityRerank
		}
	}
	return ""
}

func safeModelCachePath(root, id string) bool {
	root = filepath.Clean(root)
	candidate := filepath.Join(root, filepath.FromSlash(id))
	rel, err := filepath.Rel(root, candidate)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func (a *App) ListLocalModels() ([]LocalModelView, error) {
	page, err := a.ListLocalModelsPageContext(context.Background(), "", 0, 0)
	return page.Models, err
}

// ListLocalModelsForBase includes the selected base's effective model
// configuration. Base overrides must be visible to the model picker even when
// the global model configuration points at a different model.
func (a *App) ListLocalModelsForBase(baseID string) ([]LocalModelView, error) {
	page, err := a.ListLocalModelsPageContext(context.Background(), baseID, 0, 0)
	return page.Models, err
}

func (a *App) listLocalModels(baseID string) ([]LocalModelView, error) {
	page, err := a.ListLocalModelsPageContext(context.Background(), baseID, 0, 0)
	return page.Models, err
}

// ListLocalModelsPage returns a bounded local-model view. The legacy methods
// above retain their response shape, while HTTP callers can advance through
// manifest-backed cache entries using nextOffset/hasMore.
func (a *App) ListLocalModelsPage(baseID string, limit, offset int) (LocalModelsPage, error) {
	return a.ListLocalModelsPageContext(context.Background(), baseID, limit, offset)
}

// ListLocalModelsPageContext returns a bounded local-model view while keeping
// filesystem, metadata, and runtime status reads inside the caller context.
func (a *App) ListLocalModelsPageContext(ctx context.Context, baseID string, limit, offset int) (LocalModelsPage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	modelPage, err := a.Models.ListPageContext(ctx, limit, offset)
	if err != nil {
		return LocalModelsPage{}, err
	}
	items := modelPage.Models
	custom, err := a.Knowledge.ListCustomRerankersContext(ctx)
	if err != nil {
		return LocalModelsPage{}, err
	}
	views := make([]LocalModelView, 0, len(items)+len(custom))
	seen := map[string]bool{}
	for _, model := range items {
		seen[model.ID] = true
		view := LocalModelView{Model: model}
		if model.Kind == models.KindRerank {
			test, err := a.Knowledge.GetRerankSelfTestContext(ctx,
				model.ID, model.SizeBytes, model.Downloaded, len(model.Artifacts),
			)
			if err != nil {
				return LocalModelsPage{}, err
			}
			view.SelfTest = &test
		}
		views = append(views, view)
	}
	configuredEmbeddingModel := a.Config.Embedding.Model
	configuredRerankModel := a.Config.Rerank.Model
	embeddingLocal := a.Config.Embedding.Provider == "local"
	rerankEnabled := a.Config.Rerank.Enabled
	if baseID != "" {
		base, baseErr := a.Knowledge.GetBaseWithContext(ctx, baseID)
		if baseErr != nil {
			return LocalModelsPage{}, baseErr
		}
		effective := knowledge.ResolveBaseConfig(a.Config, base.Config)
		configuredEmbeddingModel = effective.EmbeddingModel
		configuredRerankModel = effective.RerankModel
		embeddingLocal = effective.EmbeddingProvider == "local"
		rerankEnabled = effective.RerankEnabled != nil && *effective.RerankEnabled
	}
	// Non-manifest entries are a small compatibility catalog. Keep them on the
	// first page only so advancing manifest pages never duplicates them.
	if offset == 0 {
		for _, item := range custom {
			if !seen[item.ID] {
				views = append(views, LocalModelView{Model: models.Model{
					ID: item.ID, Kind: models.KindRerank, Status: "registered",
					Lifecycle: models.LifecycleNotInstalled, Runtime: models.LifecycleRuntimeMiss,
					Artifacts: []string{}, Downloaded: 0,
				}})
			}
		}
		if a.Runtime != nil {
			runtimeStatus := map[string]runtime.Health{}
			if snapshotter, ok := a.Runtime.(runtime.StatusSnapshotter); ok {
				runtimeStatus = snapshotter.CachedStatus()
			} else {
				// Compatibility controllers predating StatusSnapshotter may only
				// expose a live status call; production Manager uses the bounded
				// cached path above.
				runtimeStatus = a.Runtime.Status(ctx)
			}
			addManaged := func(id, kind, capability string) {
				id = strings.TrimPrefix(strings.TrimSpace(id), "local:")
				if id == "" || !a.ManagedRuntime || seen[id] {
					return
				}
				health := runtimeStatus[capability]
				lifecycle := models.LifecycleNotInstalled
				if health.Lifecycle != "" {
					lifecycle = health.Lifecycle
				} else if health.Details != nil && health.Details["lifecycle"] != "" {
					lifecycle = health.Details["lifecycle"]
				}
				lastError := ""
				runtimePath := health.Path
				remediation := health.Remediation
				lastError = health.LastError
				if health.Details != nil {
					if lastError == "" {
						lastError = health.Details["error"]
					}
				}
				status := "not-downloaded"
				switch {
				case health.Lifecycle == models.LifecycleFailed:
					status = "incomplete"
				case health.Ready || (health.Model != "" && health.Lifecycle != models.LifecycleNotInstalled) || health.Lifecycle == models.LifecycleInstalled || health.Lifecycle == models.LifecycleLoading || health.Lifecycle == models.LifecycleReady:
					status = "installed"
					if health.Ready {
						lifecycle = models.LifecycleReady
					}
				}
				var selfTest *knowledge.RerankSelfTest
				if kind == models.KindRerank {
					test, err := a.Knowledge.GetRerankSelfTestContext(ctx, id, 0, 0, 0)
					if err == nil && test.CheckedAt > 0 {
						selfTest = &test
					}
				}
				views = append(views, LocalModelView{Model: models.Model{
					ID: id, Kind: kind, Status: status, Lifecycle: lifecycle,
					Ready: health.Ready, Runtime: health.Version, RuntimePath: runtimePath,
					LastError: lastError, Remediation: remediation,
				}, SelfTest: selfTest})
				seen[id] = true
			}
			addConfiguredManaged := func(configuredID, kind, capability string, useConfigured bool) {
				if !useConfigured {
					return
				}
				id := strings.TrimPrefix(strings.TrimSpace(configuredID), "local:")
				if id == "" || !a.ManagedRuntime || seen[id] {
					return
				}
				health := runtimeStatus[capability]
				healthModel := strings.TrimPrefix(strings.TrimSpace(health.Model), "local:")
				if healthModel == id && health.Lifecycle != models.LifecycleNotInstalled {
					addManaged(id, kind, capability)
					return
				}
				status := "not-downloaded"
				lifecycle := models.LifecycleNotInstalled
				runtimeStatusValue := models.LifecycleRuntimeMiss
				if _, statErr := os.Stat(filepath.Join(a.modelCacheDir(), filepath.FromSlash(id))); statErr == nil {
					status = "installed"
					lifecycle = models.LifecycleInstalled
					runtimeStatusValue = "RUNTIME_CACHED"
				}
				views = append(views, LocalModelView{Model: models.Model{
					ID: id, Kind: kind, Status: status, Lifecycle: lifecycle,
					Runtime: runtimeStatusValue,
				}})
				seen[id] = true
			}
			// The currently loaded runtime model is useful even when the selected
			// base points at another cached model. Add both entries so the picker can
			// distinguish a loaded model from an installed-but-not-loaded one.
			health := runtimeStatus[runtime.CapabilityEmbedding]
			if strings.TrimSpace(health.Model) != "" && health.Lifecycle != models.LifecycleNotInstalled {
				addManaged(health.Model, models.KindEmbedding, runtime.CapabilityEmbedding)
			}
			health = runtimeStatus[runtime.CapabilityRerank]
			if strings.TrimSpace(health.Model) != "" && health.Lifecycle != models.LifecycleNotInstalled {
				addManaged(health.Model, models.KindRerank, runtime.CapabilityRerank)
			}
			addConfiguredManaged(configuredEmbeddingModel, models.KindEmbedding, runtime.CapabilityEmbedding, embeddingLocal)
			addConfiguredManaged(configuredRerankModel, models.KindRerank, runtime.CapabilityRerank, rerankEnabled)
			if a.ManagedRuntime {
				for _, cached := range cachedManagedModels(a.Home, a.modelCacheDir()) {
					if !seen[cached.ID] {
						views = append(views, cached)
						seen[cached.ID] = true
					}
				}
			}
		}
	}
	page := LocalModelsPage{Models: views, HasMore: modelPage.HasMore}
	if modelPage.HasMore {
		page.NextOffset = modelPage.NextOffset
	}
	return page, nil
}

// SelfTestReranker asks the isolated helper to score relevant and irrelevant
// samples. A passing test requires finite, ordered, differentiated scores.
func (a *App) SelfTestReranker(ctx context.Context, id string) (knowledge.RerankSelfTest, error) {
	return a.SelfTestRerankerWithProgress(ctx, id, nil)
}

// SelfTestRerankerWithProgress performs the same readiness check while
// exposing managed-runtime loading phases to the Web job layer.
func (a *App) SelfTestRerankerWithProgress(ctx context.Context, id string, report func(runtime.ModelProgress)) (knowledge.RerankSelfTest, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return knowledge.RerankSelfTest{}, err
	}
	id = strings.TrimPrefix(strings.TrimSpace(id), "local:")
	model, err := a.Models.GetContext(ctx, id)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return knowledge.RerankSelfTest{}, ctxErr
		}
		// The managed runtime owns its Transformers.js cache and does not
		// publish the manifest used by the generic artifact manager. Its live
		// health state is therefore the source of truth for managed models.
		if !a.ManagedRuntime || a.Runtime == nil {
			return knowledge.RerankSelfTest{}, fmt.Errorf("local reranker artifacts are not downloaded")
		}
		health := a.Runtime.Status(ctx)[runtime.CapabilityRerank]
		healthModel := strings.TrimPrefix(strings.TrimSpace(health.Model), "local:")
		if healthModel != id || health.Lifecycle == "" || health.Lifecycle == models.LifecycleNotInstalled || health.Lifecycle == models.LifecycleFailed {
			return knowledge.RerankSelfTest{}, fmt.Errorf("local reranker artifacts are not downloaded")
		}
		model = models.Model{
			ID: id, Kind: models.KindRerank, Status: "installed",
			Lifecycle: health.Lifecycle, Ready: health.Ready,
			Runtime: health.Version, RuntimePath: health.Path,
			LastError: health.LastError, Remediation: health.Remediation,
		}
	}
	if model.Kind != models.KindRerank || model.Status != "installed" {
		return knowledge.RerankSelfTest{}, fmt.Errorf("local reranker artifacts are incomplete")
	}
	if a.Runtime == nil || !a.Runtime.Configured(runtime.CapabilityRerank) {
		return knowledge.RerankSelfTest{}, fmt.Errorf("local rerank runtime is not configured")
	}
	if report != nil {
		report(runtime.ModelProgress{Phase: "preparing", Percent: 5})
		if a.ManagedRuntime {
			if progressive, ok := a.Runtime.(runtime.ProgressiveManagedModelController); ok {
				if _, err := progressive.LoadModelWithProgress(ctx, runtime.CapabilityRerank, id, report); err != nil {
					return knowledge.RerankSelfTest{}, fmt.Errorf("rerank model load failed: %w", err)
				}
			} else {
				report(runtime.ModelProgress{Phase: "loading", Percent: 25})
			}
		} else {
			report(runtime.ModelProgress{Phase: "loading", Percent: 25})
		}
		report(runtime.ModelProgress{Phase: "testing", Percent: 75})
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
		if err := a.Knowledge.SaveRerankSelfTestContext(ctx, result); err != nil {
			return result, err
		}
		return result, fmt.Errorf("%s", result.Error)
	}
	result.Healthy = true
	result.Scores = payload.Scores
	result.Current = true
	if err := a.Knowledge.SaveRerankSelfTestContext(ctx, result); err != nil {
		return result, err
	}
	if report != nil {
		report(runtime.ModelProgress{Phase: "ready", Percent: 100})
	}
	return result, nil
}

// Close releases all resources.
func (a *App) Close() {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	a.lifecycleMu.RLock()
	startupCancel := a.startupCancel
	maintenanceCancel := a.maintenanceCancel
	startupDone := a.startupDone
	maintenanceDone := a.maintenanceDone
	a.lifecycleMu.RUnlock()
	if startupCancel != nil {
		startupCancel()
	}
	if maintenanceCancel != nil {
		maintenanceCancel()
	}
	waitDone := func(done chan struct{}) {
		if done == nil {
			return
		}
		select {
		case <-done:
		case <-shutdownCtx.Done():
			a.Logger.Warn("shutdown wait exceeded deadline")
		}
	}
	waitDone(startupDone)
	waitDone(maintenanceDone)
	if a.Operations != nil {
		a.Operations.StopWithContext(shutdownCtx)
	}
	if a.Runtime != nil {
		if err := closeRuntimeWithContext(shutdownCtx, a.Runtime); err != nil {
			a.Logger.Warn("runtime shutdown incomplete", "error", err)
		}
	}
	if a.DB != nil {
		if err := a.DB.CloseWithContext(shutdownCtx); err != nil {
			a.Logger.Warn("database shutdown incomplete", "error", err)
		}
	}
	if a.instanceLock != nil {
		if err := a.instanceLock.ReleaseWithContext(shutdownCtx); err != nil {
			a.Logger.Warn("instance lock release incomplete", "error", err)
		}
	}
}

// closeRuntimeWithContext keeps shutdown bounded even for compatibility
// controllers that only expose the historical Close method. The goroutine is
// intentionally isolated from application teardown: once the deadline is
// reached, DB and instance-lock cleanup must not wait for an uncooperative
// helper process.
func closeRuntimeWithContext(ctx context.Context, controller runtime.Controller) error {
	if controller == nil {
		return nil
	}
	if closer, ok := controller.(interface{ CloseWithContext(context.Context) error }); ok {
		return closer.CloseWithContext(ctx)
	}
	done := make(chan struct{})
	go func() {
		controller.Close()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
