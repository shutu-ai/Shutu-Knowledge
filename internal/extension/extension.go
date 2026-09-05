// Package extension is the ONLY component that imports the shutu-agent
// public Extension SDK. It adapts Knowledge Core calls onto Extension
// Protocol v1 (initialize/health/context/tool/event/shutdown) and owns the
// manifest identity. Knowledge Core below this package stays agent-agnostic.
package extension

import (
	"context"
	"fmt"
	"io"

	"github.com/shutu-ai/shutu-agent/sdk/extension"
	"github.com/shutu-ai/shutu-knowledge/internal/app"
	"github.com/shutu-ai/shutu-knowledge/internal/version"
)

// ExtensionID is the stable manifest identity used in discovery grants.
const ExtensionID = "shutu-knowledge"

// Manifest builds the v1 manifest for this extension. Phase 1 declares
// health and lifecycle only; tools, contextProvider, and web capabilities
// are enabled in their own phases so the contract surface grows honestly.
func Manifest() extension.Manifest {
	var m extension.Manifest
	m.ID = ExtensionID
	m.Name = "Shutu Knowledge"
	m.Version = version.Version
	m.Description = "Knowledge bases, documents, hybrid retrieval, and model tools for shutu-agent"
	m.ExtensionAPI = extension.APIVersion
	m.Capabilities.Lifecycle = true
	m.Capabilities.Health = true
	m.Transport.Type = "stdio"
	m.Transport.Command = "shutu-knowledge"
	m.Transport.Args = []string{"extension"}
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
	manifest := Manifest()
	server := extension.NewServer(extension.ServerCallbacks{
		Manifest: manifest,
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
	})
	return server.Run(ctx, in, out)
}
