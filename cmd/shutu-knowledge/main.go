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
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/shutu-ai/shutu-knowledge/internal/app"
	"github.com/shutu-ai/shutu-knowledge/internal/config"
	"github.com/shutu-ai/shutu-knowledge/internal/extension"
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
		fmt.Println(version.Version)
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
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
	application, err := app.New(ctx)
	if err != nil {
		return err
	}
	defer application.Close()
	server := web.New(application)
	addr, err := server.Listen(application.Config.Server.Addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", application.Config.Server.Addr, err)
	}
	application.Logger.Info("serve started", "addr", addr.String(), "home", application.Home)
	<-ctx.Done()
	return server.Shutdown(context.Background())
}

func cmdExtension(ctx context.Context) error {
	application, err := app.New(ctx)
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

	v, err := storage.SchemaVersion(application.DB.DB)
	if err != nil {
		return err
	}
	fmt.Println("schema version:", v)
	raws, err := application.RawStore.ListAll()
	if err != nil {
		return err
	}
	fmt.Println("raw store files:", len(raws))
	report := application.Health.Snapshot(ctx)
	out, _ := json.MarshalIndent(report.Components, "", "  ")
	fmt.Println("health:", report.Status)
	fmt.Println(string(out))
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
	return nil
}
