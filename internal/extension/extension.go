// Package extension is the ONLY component that imports the shutu-agent
// public Extension SDK. It adapts Knowledge Core calls onto Extension
// Protocol v1 (initialize/health/context/tool/event/shutdown) and owns the
// manifest identity. Knowledge Core below this package stays agent-agnostic.
package extension

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/shutu-ai/shutu-agent/sdk/extension"
	"github.com/shutu-ai/shutu-knowledge/internal/app"
	"github.com/shutu-ai/shutu-knowledge/internal/version"
	"github.com/shutu-ai/shutu-knowledge/internal/web"
)

// ExtensionID is the stable manifest identity used in discovery grants.
const ExtensionID = "shutu-knowledge"

// Manifest builds the v1 manifest for this extension.
func Manifest() extension.Manifest {
	var m extension.Manifest
	m.ID = ExtensionID
	m.Name = "Shutu Knowledge"
	m.Version = version.Version
	m.Description = "Knowledge bases, documents, hybrid retrieval, and model tools for shutu-agent"
	m.ExtensionAPI = extension.APIVersion
	m.Capabilities.Lifecycle = true
	m.Capabilities.Health = true
	m.Capabilities.Tools = true
	m.Capabilities.ContextProvider = true
	m.Capabilities.Web = true
	m.Transport.Type = "stdio"
	m.Transport.Command = "shutu-knowledge"
	m.Transport.Args = []string{"extension"}
	m.Tools.Definitions = toolDefinitions()
	m.ContextProvider = contextProviderConfig()
	m.Web.Enabled = true
	m.Web.Route = "/extensions/shutu-knowledge/"
	m.Web.Title = "Knowledge"
	m.Web.Icon = "library"
	m.Web.NavigationEnabled = boolPtr(true)
	m.Web.NavigationGroup = "Tools"
	m.Web.Order = 40
	m.Permissions = permissions()
	m.Health.Enabled = true
	m.Health.TimeoutMS = 1000
	m.Lifecycle.Enabled = true
	m.Lifecycle.StartupTimeoutMS = 10000
	m.Lifecycle.ShutdownTimeoutMS = 5000
	m.Lifecycle.RestartPolicy = extension.RestartOnFailure
	m.Lifecycle.MaxRestarts = 3
	return m
}

// Run serves Extension Protocol v1 over the provided stdio streams.
func Run(ctx context.Context, app *app.App, in io.Reader, out io.Writer) error {
	webServer := web.New(app)
	addr, err := webServer.Listen("127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("start extension web service: %w", err)
	}
	webBaseURL := "http://" + addr.String()
	stop := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
		case <-stop:
		}
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = webServer.Shutdown(shutdownCtx)
	}()

	manifest := Manifest()
	server := extension.NewServer(extension.ServerCallbacks{
		Manifest: manifest,
		WebBaseURL: func() string {
			return webBaseURL
		},
		Health: func(ctx context.Context) (extension.HealthResult, error) {
			report := app.Health.Snapshot(ctx)
			detail := ""
			for i, c := range report.Components {
				if i > 0 {
					detail += ";"
				}
				detail += fmt.Sprintf("%s=%s", c.Name, c.Status)
				if c.Detail != "" {
					detail += "(" + c.Detail + ")"
				}
			}
			return extension.HealthResult{
				Ready:  report.Ready,
				Status: report.Status,
				Detail: detail,
			}, nil
		},
		ProvideContext: func(ctx context.Context, request extension.ContextRequest) (extension.ContextResult, error) {
			return ProvideContext(ctx, app, request)
		},
		CallTool: func(ctx context.Context, request extension.ToolCallRequest) (extension.ToolCallResult, error) {
			return CallTool(ctx, app, request)
		},
	})
	runErr := server.Run(ctx, in, out)
	close(stop)
	if shutdownErr := webServer.Shutdown(context.Background()); shutdownErr != nil {
		return errors.Join(runErr, shutdownErr)
	}
	return runErr
}
