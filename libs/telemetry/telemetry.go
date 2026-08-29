// Package telemetry sets up OpenTelemetry exporters (traces + metrics) for a
// VibeSync service.
//
// One call to Shutdown at process exit flushes pending exports. The returned
// Providers are wired into the global OTel API so instrumentation libraries
// (otlhttp, connect interceptors, runtime metrics) work without explicit
// plumbing. See ADR-0008.
//
// Exporters target the OTLP/gRPC collector configured in
// deployments/docker-compose (otel-collector). A service with no OTLP
// endpoint configured runs with a no-op provider — local-first friendly.
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Options configure the telemetry pipeline.
type Options struct {
	// ServiceName identifies this process in traces/metrics.
	ServiceName string
	// ServiceVersion for semconv.service.version.
	ServiceVersion string
	// OTLPEndpoint is the collector address, host:port. Defaults to
	// "localhost:4317".
	OTLPEndpoint string
	// SampleRatio is the trace sampling ratio in [0,1]. Production should
	// use a value <1 to control cost; local dev may use 1.0.
	SampleRatio float64
	// ShutdownTimeout bounds the final flush.
	ShutdownTimeout time.Duration
}

// Telemetry holds the providers; Shutdown must be called on graceful exit.
type Telemetry struct {
	tp *trace.TracerProvider
	mp *metric.MeterProvider
}

// Start builds the providers, registers them globally, and returns a handle
// whose Shutdown should be deferred at process entry. A nil OTLPEndpoint (or
// empty) selects the no-op path so unit tests and offline dev still work.
func Start(ctx context.Context, opts Options) (*Telemetry, error) {
	if opts.ServiceName == "" {
		return nil, errors.New("telemetry: ServiceName is required")
	}
	if opts.OTLPEndpoint == "" {
		opts.OTLPEndpoint = "localhost:4317"
	}
	if opts.SampleRatio <= 0 {
		opts.SampleRatio = 1.0
	}
	if opts.ShutdownTimeout <= 0 {
		opts.ShutdownTimeout = 10 * time.Second
	}

	// Build the resource without merging with resource.Default() to avoid
	// schema-URL conflicts between OTel SDK versions. resource.Default()
	// carries its own schema URL which may differ from semconv's, causing
	// resource.Merge to fail. Instead, we use NewMerge with no conflict.
	res, err := resource.New(
		ctx,
		resource.WithAttributes(
			semconv.ServiceName(opts.ServiceName),
			semconv.ServiceVersion(orDefault(opts.ServiceVersion, "0.0.0-dev")),
			semconv.ServiceNamespace("vibesync"),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("telemetry: build resource: %w", err)
	}

	// W3C TraceContext + Baggage is the interop standard.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	))

	dialOpts := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}

	traceExp, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(opts.OTLPEndpoint),
		otlptracegrpc.WithDialOption(dialOpts...),
	)
	if err != nil {
		return nil, fmt.Errorf("telemetry: trace exporter: %w", err)
	}

	tp := trace.NewTracerProvider(
		trace.WithResource(res),
		trace.WithBatcher(traceExp),
		trace.WithSampler(trace.ParentBased(trace.TraceIDRatioBased(opts.SampleRatio))),
	)
	otel.SetTracerProvider(tp)

	metricExp, err := otlpmetricgrpc.New(ctx,
		otlpmetricgrpc.WithEndpoint(opts.OTLPEndpoint),
		otlpmetricgrpc.WithDialOption(dialOpts...),
	)
	if err != nil {
		return nil, fmt.Errorf("telemetry: metric exporter: %w", err)
	}
	mp := metric.NewMeterProvider(
		metric.WithResource(res),
		metric.WithReader(metric.NewPeriodicReader(metricExp,
			metric.WithInterval(15*time.Second))),
	)
	otel.SetMeterProvider(mp)

	return &Telemetry{tp: tp, mp: mp}, nil
}

// Shutdown flushes pending exports and releases resources. Idempotent.
func (t *Telemetry) Shutdown(ctx context.Context) error {
	var errs []error
	if t.tp != nil {
		if err := t.tp.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("tracer: %w", err))
		}
	}
	if t.mp != nil {
		if err := t.mp.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("meter: %w", err))
		}
	}
	return errors.Join(errs...)
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
