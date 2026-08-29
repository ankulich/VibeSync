# ADR-0008: Observability stack

- **Status:** Accepted
- **Date:** 2026-07-26
- **Phase:** 1 (Architecture)

## Context

The prompt mandates OpenTelemetry, Prometheus, Grafana, and structured logs.
We need these to interoperate so that a request can be traced from the API
Gateway through every downstream service, with metrics and logs correlated
by trace ID.

## Decision

- **Logs:** the standard `log/slog` package, JSON to stdout, with trace/span
  IDs injected from the active OTel context (`libs/log`). One logger per
  process; per-request loggers carry request ID + trace ID.
- **Traces:** OpenTelemetry SDK with OTLP/gRPC exporter to a collector
  (`libs/telemetry`). The collector forwards to Jaeger for inspection.
  Sampling is configurable per environment (1.0 local, <1.0 prod).
- **Metrics:** Prometheus via `promhttp`. The OTel collector also exposes a
  Prometheus endpoint so OTel-instrumented metrics are scraped alongside
  direct Prometheus instrumentation.
- **Bootstrap:** `libs/observability.Start` wires all three in one call;
  each service's `main()` calls it at startup and defers `Shutdown`.

### Correlation

- Every log line emitted within a request context carries `trace_id` and
  `span_id` attributes (from the active span).
- Every RPC and HTTP handler propagates the W3C `traceparent` header.
- Request IDs (per `libs/web`) are independent of trace IDs but logged
  alongside them, so a user-supplied `X-Request-ID` survives even when
  tracing is sampled out.

## Consequences

- **Pros:** one import per service (`libs/observability`); trace/log
  correlation is automatic; the local stack (compose) gives a working
  Jaeger + Prometheus + Grafana out of the box.
- **Cons:** the OTel collector is another moving part in dev; mitigated by
  making telemetry optional (a service runs fine with the collector down —
  exports fail open).

## Alternatives considered

- **zap/zerolog instead of slog:** faster, but slog is the standard library,
  composes with OTel's handler bridge, and is fast enough at our volumes.
- **Direct Prometheus instrumentation only (no collector):** loses the
  ability to forward to multiple backends and to process metrics centrally.
  Rejected.
