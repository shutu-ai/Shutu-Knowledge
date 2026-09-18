// Command shutu-knowledge is the single binary for all modes:
//
//	shutu-knowledge serve      standalone HTTP server (web/API)
//	shutu-knowledge extension  Extension Protocol v1 stdio loop (Agent-managed)
//	shutu-knowledge doctor     environment and storage diagnostics
//	shutu-knowledge version    print version
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/shutu-ai/shutu-knowledge/internal/app"
	"github.com/shutu-ai/shutu-knowledge/internal/config"
	"github.com/shutu-ai/shutu-knowledge/internal/extension"
	"github.com/shutu-ai/shutu-knowledge/internal/runtime"
	"github.com/shutu-ai/shutu-knowledge/internal/storage"
	"github.com/shutu-ai/shutu-knowledge/internal/version"
	"github.com/shutu-ai/shutu-knowledge/internal/web"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	switch os.Args[1] {
	case "serve":
		err = cmdServe(ctx)
	case "extension":
		err = cmdExtension(ctx)
	case "doctor":
		err = cmdDoctor(ctx)
	case "version":
		err = cmdVersion()
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func cmdVersion() error {
	buildJSON, err := json.MarshalIndent(version.Current(), "", "  ")
	if err != nil {
		return fmt.Errorf("encode version: %w", err)
	}
	fmt.Println(string(buildJSON))
	return nil
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage: shutu-knowledge <command>

commands:
  serve       run the standalone HTTP server
  extension   run the Extension Protocol v1 stdio loop (Agent-managed)
  doctor      check storage, migrations, and dependencies
  version     print version`)
}

func cmdServe(ctx context.Context) error {
	application, err := app.NewWithOptions(ctx, app.Options{DeferStartupRecovery: true})
	if err != nil {
		return err
	}
	defer application.Close()
	server := web.New(application)
	addr, err := server.Listen(application.Config.Server.Addr)
	if err != nil {
		return formatListenError(application.Config.Server.Addr, err)
	}
	application.StartBackgroundRecovery()
	application.StartBackgroundMaintenance()
	application.Logger.Info("serve started", "addr", addr.String(), "home", application.Home)
	<-ctx.Done()
	return shutdownServerWithTimeout(server, 15*time.Second)
}

type httpShutdowner interface {
	Shutdown(context.Context) error
}

// shutdownServerWithTimeout prevents an uncooperative HTTP handler from
// consuming the entire process shutdown path. App.Close has its own bounded
// cleanup budget after the listener is stopped.
func shutdownServerWithTimeout(server httpShutdowner, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return server.Shutdown(shutdownCtx)
}

func formatListenError(addr string, err error) error {
	if errors.Is(err, syscall.EADDRINUSE) {
		return fmt.Errorf("listen %s: address already in use; stop the existing Knowledge server or configure a different server.addr: %w", addr, err)
	}
	return fmt.Errorf("listen %s: %w", addr, err)
}

func cmdExtension(ctx context.Context) error {
	application, err := app.NewWithOptions(ctx, app.Options{DeferStartupRecovery: true})
	if err != nil {
		return err
	}
	defer application.Close()
	return extension.Run(ctx, application, os.Stdin, os.Stdout)
}

func cmdDoctor(ctx context.Context) error {
	initConfig := flag.Bool("init", false, "write the default config.yaml into the data home and exit")
	flag.CommandLine.Parse(os.Args[2:])
	home, err := config.DataHome()
	if err != nil {
		return err
	}
	fmt.Println("data home:", home)
	if *initConfig {
		if err := os.MkdirAll(home, 0o755); err != nil {
			return err
		}
		if err := config.WriteDefault(home + string(os.PathSeparator) + "config.yaml"); err != nil {
			return err
		}
		fmt.Println("wrote default config.yaml")
		return nil
	}

	application, err := app.New(ctx)
	if err != nil {
		return err
	}
	defer application.Close()

	v, err := storage.SchemaVersion(application.DB.ReadDB())
	if err != nil {
		return err
	}
	fmt.Println("schema version:", v)
	format, err := storage.StorageFormat(application.DB.ReadDB())
	if err != nil {
		return err
	}
	fmt.Printf("storage format: version=%d min_reader=%d min_writer=%d status=%s\n",
		format.FormatVersion, format.MinReaderVersion, format.MinWriterVersion, format.MigrationStatus)
	raws, err := application.RawStore.ListAll()
	if err != nil {
		return err
	}
	fmt.Println("raw store files:", len(raws))
	report := application.Health.Snapshot(ctx)
	out, _ := json.MarshalIndent(report.Components, "", "  ")
	fmt.Println("health:", report.Status)
	fmt.Println(string(out))
	coreReady := report.Ready
	enrichmentUnavailable := false
	for _, component := range report.Components {
		if component.Name == "document-enrichment" && component.Status == "degraded" {
			enrichmentUnavailable = true
		}
		if (component.Name == "document-parser" || component.Name == "document-ir" || component.Name == "structured-index") && component.Status != "ok" {
			coreReady = false
		}
	}
	if coreReady {
		fmt.Println("document intelligence: CORE READY")
	}
	if enrichmentUnavailable {
		fmt.Println("document enrichment: ENRICHMENT UNAVAILABLE (optional)")
	}
	var doctorRuntimeStatus map[string]runtime.Health
	if application.Runtime != nil {
		runtimeStatus := application.Runtime.Status(ctx)
		doctorRuntimeStatus = runtimeStatus
		runtimeOut, _ := json.MarshalIndent(runtimeStatus, "", "  ")
		fmt.Println("runtime status:")
		fmt.Println(string(runtimeOut))
		for _, capability := range []string{
			"embedding", "rerank", "ocr", "pdf_render", "office",
		} {
			health := runtimeStatus[capability]
			status := "WARN"
			if health.Ready {
				status = "PASS"
			} else if health.Status == "failed" || health.Lifecycle == "FAILED" {
				status = "FAIL"
			}
			fmt.Printf("runtime %-10s %-4s version=%q path=%q ready=%t lifecycle=%q error=%q remediation=%q\n",
				capability, status, health.Version, health.Path, health.Ready, health.Lifecycle, health.LastError, health.Remediation)
		}
	}
	localModels, err := application.ListLocalModels()
	if err != nil {
		return fmt.Errorf("inspect local models: %w", err)
	}
	modelOut, _ := json.MarshalIndent(localModels, "", "  ")
	fmt.Println("local models:")
	fmt.Println(string(modelOut))
	ocrModel, err := application.Models.OCRStatus()
	if err != nil {
		return fmt.Errorf("inspect OCR model: %w", err)
	}
	ocrOut, _ := json.MarshalIndent(ocrModel, "", "  ")
	fmt.Println("OCR model:")
	fmt.Println(string(ocrOut))
	fmt.Println("env:", config.EnvPreview())
	if !report.Ready {
		return fmt.Errorf("doctor found failed subsystems")
	}
	if doctorRuntimeStatus != nil {
		for capability, health := range doctorRuntimeStatus {
			if health.Status == "failed" || health.Lifecycle == "FAILED" {
				return fmt.Errorf("doctor found failed runtime %s: %s", capability, strings.TrimSpace(health.LastError))
			}
			if capability == "embedding" && application.Config.Embedding.Provider == "local" && !health.Ready {
				return fmt.Errorf("doctor found unavailable local embedding runtime: %s", health.Remediation)
			}
			if capability == "rerank" && application.Config.Rerank.Enabled && strings.HasPrefix(application.Config.Rerank.Model, "local:") && !health.Ready {
				return fmt.Errorf("doctor found unavailable local reranker runtime: %s", health.Remediation)
			}
		}
	}
	return nil
}
