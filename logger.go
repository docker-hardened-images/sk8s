package sk8s

import (
	"context"
	"io"
	"log"
	"log/slog"
	"testing"
)

type loggerContextKey struct{}

// slogSourceHandler creates a new slog.Handler that annotates log lines with file:line.
func slogSourceHandler(w io.Writer) slog.Handler {
	return slog.NewTextHandler(w, &slog.HandlerOptions{AddSource: true})
}

// LoggerFromT creates a *log.Logger that correlates output with *testing.T.
func LoggerFromT(t *testing.T) *log.Logger {
	return slog.NewLogLogger(slogSourceHandler(t.Output()), slog.LevelInfo)
}

// SloggerFromT creates a *slog.Logger that correlates output with *testing.T.
func SloggerFromT(t *testing.T) *slog.Logger {
	return slog.New(slogSourceHandler(t.Output()))
}

// ContextWithLoggerFromT embeds *testing.T log output into ctx for LoggerFromContext / SloggerFromContext.
func ContextWithLoggerFromT(ctx context.Context, t *testing.T) context.Context {
	tHandler := slogSourceHandler(t.Output())
	return context.WithValue(ctx, loggerContextKey{}, tHandler)
}

// LoggerFromContext returns a *log.Logger from ctx when ContextWithLoggerFromT was used; otherwise log.Default().
func LoggerFromContext(ctx context.Context) *log.Logger {
	handler, ok := ctx.Value(loggerContextKey{}).(slog.Handler)
	if ok && handler != nil {
		return slog.NewLogLogger(handler, slog.LevelInfo)
	}
	return log.Default()
}

// SloggerFromContext returns a *slog.Logger from ctx when ContextWithLoggerFromT was used; otherwise slog.Default().
func SloggerFromContext(ctx context.Context) *slog.Logger {
	handler, ok := ctx.Value(loggerContextKey{}).(slog.Handler)
	if ok && handler != nil {
		return slog.New(handler)
	}
	return slog.Default()
}
