package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/shutu-ai/shutu-knowledge/internal/app"
	mcpserver "github.com/shutu-ai/shutu-knowledge/internal/mcp"
)

// cmdMCP runs the read-only MCP server over stdio (default) or streamable
// HTTP. The binary keeps one storage instance; concurrent serve/extension and
// mcp processes on the same data home fail on the instance lock by design.
func cmdMCP(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	httpAddr := fs.String("http", "", "serve streamable HTTP on this address (for example 127.0.0.1:7801) instead of stdio")
	if err := fs.Parse(args); err != nil {
		return err
	}
	application, err := app.NewWithOptions(ctx, app.Options{DeferStartupRecovery: true})
	if err != nil {
		return err
	}
	defer application.Close()
	server, err := mcpserver.New(mcpserver.Options{
		Knowledge:  application.Knowledge,
		AgentQuery: application.AgentQuery,
	})
	if err != nil {
		return err
	}
	if *httpAddr == "" {
		if err := server.Run(ctx, &mcp.StdioTransport{}); err != nil && !errors.Is(err, ctx.Err()) {
			return fmt.Errorf("mcp stdio server: %w", err)
		}
		return nil
	}
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	httpServer := &http.Server{
		Addr:              *httpAddr,
		Handler:           handler,
		ReadHeaderTimeout: 15 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()
	log.Printf("shutu-knowledge MCP listening on http://%s", *httpAddr)
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("mcp http server: %w", err)
	}
	return nil
}
