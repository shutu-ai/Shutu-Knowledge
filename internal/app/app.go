// Package app wires the process-wide components: config, logger, storage,
// raw store, and the health registry. Both the standalone server and the
// Agent-managed extension loop build on it.
package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/shutu-ai/shutu-knowledge/internal/config"
	"github.com/shutu-ai/shutu-knowledge/internal/health"
	"github.com/shutu-ai/shutu-knowledge/internal/logging"
	"github.com/shutu-ai/shutu-knowledge/internal/storage"
)

// Logger is the slog logger type used across the project.
type Logger = logging.Logger

// App owns the shared runtime components.
type App struct {
	Home     string
	Config   config.Config
	Logger   *Logger
	DB       *storage.DB
	RawStore *storage.RawFileStore
	Health   *health.Registry
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
	return application, nil
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

// Close releases all resources.
func (a *App) Close() {
	if a.DB != nil {
		_ = a.DB.Close()
	}
}
