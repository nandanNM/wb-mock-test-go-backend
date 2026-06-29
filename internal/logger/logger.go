// Package logger provides a configured structured logger built on log/slog.
//
// In production it emits JSON (machine-parseable for log aggregators like
// Loki, Datadog or CloudWatch). In development it emits human-readable text.
package logger

import (
	"context"
	"log/slog"
	"os"
	"strings"
)

// ctxKey is an unexported type to avoid context key collisions.
type ctxKey struct{}

var loggerKey = ctxKey{}

// New builds a slog.Logger. When json is true, output is structured JSON;
// otherwise it is human-friendly text. The level string is parsed leniently.
func New(level string, json bool) *slog.Logger {
	opts := &slog.HandlerOptions{
		Level:     parseLevel(level),
		AddSource: false,
	}

	var handler slog.Handler
	if json {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		handler = slog.NewTextHandler(os.Stdout, opts)
	}

	return slog.New(handler)
}

// WithContext returns a copy of ctx carrying the supplied logger.
func WithContext(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey, l)
}

// FromContext extracts the request-scoped logger from ctx, falling back to the
// default logger so callers never have to nil-check.
func FromContext(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(loggerKey).(*slog.Logger); ok && l != nil {
		return l
	}
	return slog.Default()
}

func parseLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
