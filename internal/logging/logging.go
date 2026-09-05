// Package logging configures structured logging with safe defaults:
// document contents, retrieval evidence, and user inputs are never logged at
// info level or below; debug mode opts in explicitly via the config level.
package logging

import (
	"log/slog"
	"os"
	"strings"
)

// Logger is the structured logger type used across the project.
type Logger = slog.Logger

// New builds the process logger from the configured level.
func New(level string) *slog.Logger {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	handler := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: lvl})
	return slog.New(handler)
}
