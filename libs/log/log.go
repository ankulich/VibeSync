// Package log configures the standard-library structured logger (log/slog)
// for VibeSync services.
//
// Design:
//   - One slog.Handler per process, JSON to stdout in containers, console
//     (colored) under local dev. Selected by config.log.format.
//   - Trace/span IDs are injected from the active OpenTelemetry context so
//     every log line emitted within a request is correlatable to the trace.
//   - Severity is gated by config.log.level (debug/info/warn/error).
//
// We deliberately use the standard log/slog rather than a third-party logger.
// It composes cleanly with OpenTelemetry's handler bridge and is fast enough
// for our volumes; see ADR-0008.
package log

import (
	"context"
	"log/slog"
	"os"
	"strings"

	"go.opentelemetry.io/otel/trace"
)

// Format selects the handler output style.
type Format string

const (
	// FormatJSON emits structured JSON records for containers and aggregators.
	FormatJSON Format = "json"
	// FormatConsole emits human-readable colored text for local development.
	FormatConsole Format = "console"
)

// Level wraps slog.Level so config can use a string.
type Level = slog.Level

// Options configures the logger.
type Options struct {
	Format Format
	Level  Level
	// Service is injected as a stable "service" attribute on every record,
	// so multi-service log streams are filterable downstream.
	Service string
	// Version is injected as "version" for log-based release correlation.
	Version string
}

// New returns a *slog.Logger configured per opts. The returned logger is
// safe for concurrent use and is the one every package in a service should
// obtain via log.From(ctx) within request scope.
func New(opts Options) *slog.Logger {
	handler := buildHandler(opts)
	l := slog.New(handler)
	slog.SetDefault(l)
	return l
}

func buildHandler(opts Options) slog.Handler {
	level := slog.LevelInfo
	if opts.Level != 0 {
		level = opts.Level
	}
	baseAttrs := []slog.Attr{}
	if opts.Service != "" {
		baseAttrs = append(baseAttrs, slog.String("service", opts.Service))
	}
	if opts.Version != "" {
		baseAttrs = append(baseAttrs, slog.String("version", opts.Version))
	}

	switch normalizeFormat(opts.Format) {
	case FormatConsole:
		return slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
			Level: level,
			ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
				return a
			},
		}).WithAttrs(baseAttrs)
	default: // json is the default in production
		return slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			Level: level,
		}).WithAttrs(baseAttrs)
	}
}

func normalizeFormat(f Format) Format {
	switch Format(strings.ToLower(string(f))) {
	case FormatConsole:
		return FormatConsole
	default:
		return FormatJSON
	}
}

// ParseLevel converts a config string ("debug","info","warn","error") to a
// slog.Level. Unknown values default to Info.
func ParseLevel(s string) Level {
	switch strings.ToLower(s) {
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

// From returns the logger injected into ctx by WithLogger, falling back to
// slog.Default(). The fallback path is for callers outside request scope
// (startup, background workers).
func From(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(ctxKey{}).(*slog.Logger); ok && l != nil {
		return traceCorrelate(ctx, l)
	}
	return traceCorrelate(ctx, slog.Default())
}

// WithLogger returns ctx with l attached so downstream callers pick it up
// via From. Most call sites don't need this: the OTel-instrumented HTTP/RPC
// handlers install a request logger automatically.
func WithLogger(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, l)
}

type ctxKey struct{}

// traceCorrelate attaches the active span's trace/span IDs as attributes.
// When no span is active the logger is returned unchanged — zero overhead.
func traceCorrelate(ctx context.Context, l *slog.Logger) *slog.Logger {
	span := trace.SpanFromContext(ctx)
	if !span.SpanContext().IsValid() {
		return l
	}
	sc := span.SpanContext()
	return l.With(
		slog.String("trace_id", sc.TraceID().String()),
		slog.String("span_id", sc.SpanID().String()),
	)
}
