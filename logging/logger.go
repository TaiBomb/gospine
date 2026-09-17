// Package logging provides the process-wide JSON logger and the request
// scoped helpers built on top of it.
//
// Call InitLogger once at startup, then log through FromContext while serving
// a request, so every line carries the request and correlation ids.
package logging

import (
	"log/slog"
	"os"
	"strings"
)

// CustomLogger is a slog.Logger with a Fatal level.
type CustomLogger struct {
	*slog.Logger
}

// Fatal logs msg at error level and exits the process with status 1.
func (l *CustomLogger) Fatal(msg string, args ...any) {
	l.Logger.Error(msg, args...)
	os.Exit(1)
}

var (
	// GlobalLevel is the level every logger built by InitLogger filters on.
	GlobalLevel = &slog.LevelVar{}
	// Log is the process-wide logger; nil until InitLogger is called.
	Log *CustomLogger
)

// InitLogger sets Log and the slog default to a JSON logger on stdout, at info level.
func InitLogger() {
	GlobalLevel.Set(slog.LevelInfo)

	opts := &slog.HandlerOptions{
		Level:     GlobalLevel,
		AddSource: true,
	}

	handler := slog.NewJSONHandler(os.Stdout, opts)
	baseLogger := slog.New(handler)

	Log = &CustomLogger{baseLogger}

	slog.SetDefault(baseLogger)
}

// SetLevel changes GlobalLevel; an unknown level falls back to INFO.
func SetLevel(levelStr string) {
	var level slog.Level

	switch strings.ToUpper(levelStr) {
	case "INFO":
		level = slog.LevelInfo
	case "DEBUG":
		level = slog.LevelDebug
	case "WARN":
		level = slog.LevelWarn
	case "ERROR":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	GlobalLevel.Set(level)
}
