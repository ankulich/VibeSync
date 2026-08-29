// Package observability bundles log, telemetry, and metrics setup into a
// single entrypoint so each service's main() is uniform.
//
// Usage at process start:
//
//	obs, err := observability.Start(ctx, observability.Options{
//	    Service: "auth-service", Version: buildVersion,
//	    LogLevel: cfg.GetString("log.level"),
//	    LogFormat: observability.LogFormat(cfg.GetString("log.format")),
//	    OTLPEndpoint: cfg.GetString("otel.endpoint"),
//	    SampleRatio: cfg.GetFloat64("otel.sample_ratio"),
//	})
//	defer obs.Shutdown()
//
// The Prometheus /metrics handler is exposed by obs.MetricsHandler().
// See ADR-0008 for the full rationale.
package observability

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	vblog "vibesync/libs/log"
	vbtel "vibesync/libs/telemetry"
)

// LogFormat is a typed alias for the log package's Format.
type LogFormat = vblog.Format

// Options carries everything Start needs.
type Options struct {
	Service      string
	Version      string
	LogLevel     string // "debug"|"info"|"warn"|"error"
	LogFormat    LogFormat
	OTLPEndpoint string
	SampleRatio  float64
}

// Handle is the running bundle. Shutdown MUST be deferred on it.
type Handle struct {
	Telemetry *vbtel.Telemetry
	Logger    *slog.Logger
}

// Start initializes the logger, telemetry providers, and the Prometheus
// registry in one call. On failure the caller should abort startup.
func Start(ctx context.Context, opts Options) (*Handle, error) {
	logger := vblog.New(vblog.Options{
		Format:  opts.LogFormat,
		Level:   vblog.ParseLevel(opts.LogLevel),
		Service: opts.Service,
		Version: opts.Version,
	})

	tel, err := vbtel.Start(ctx, vbtel.Options{
		ServiceName:    opts.Service,
		ServiceVersion: opts.Version,
		OTLPEndpoint:   opts.OTLPEndpoint,
		SampleRatio:    opts.SampleRatio,
	})
	if err != nil {
		return nil, err
	}

	return &Handle{
		Telemetry: tel,
		Logger:    logger,
	}, nil
}

// Shutdown flushes telemetry. Safe to call multiple times.
func (h *Handle) Shutdown() {
	if h == nil || h.Telemetry == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	_ = h.Telemetry.Shutdown(ctx)
}

// MetricsHandler returns the HTTP handler for the /metrics endpoint. Exposed
// here so the web/rpc bootstrap can mount it without importing promhttp
// directly in every service.
func MetricsHandler() http.Handler {
	return promhttp.Handler()
}

const shutdownGrace = 10 * time.Second
