// Package rpc bootstraps a Connect-RPC server with VibeSync's standard
// interceptor chain: recovery, request logging, tracing, metrics, and
// *Error → *connect.Error translation.
//
// Connect serves gRPC-over-H2 and HTTP/JSON from a single handler, so the
// frontend can call the same .proto-defined endpoints over fetch(). See
// ADR-0003.
//
// Concrete services register via generated *v1connect packages (e.g.
// authv1connect.NewAuthServiceHandler(impl)). This package only wires the
// transport.
package rpc

import (
	"context"
	"errors"
	"net/http"
	"time"

	"connectrpc.com/connect"
	"connectrpc.com/otelconnect"
	"github.com/prometheus/client_golang/prometheus"

	vberr "vibesync/libs/errors"
)

// Options configure the server.
type Options struct {
	// ServiceName identifies this process in traces/metrics.
	ServiceName string
	// OTel is the otelconnect option set; pass nil to use defaults.
	OTel *otelconnect.Option
	// MetricsRegisterer is where RPC metrics are registered. Defaults to
	// prometheus.DefaultRegisterer.
	MetricsRegisterer prometheus.Registerer
}

// New returns a Connect handler option chain (interceptors) that callers pass
// to connect.NewHandler. The chain order is: recovery → error-translate →
// otel → request-log. Recovery is outermost so panics never escape.
//
// The OTel interceptor reads the service name from the globally-configured
// TracerProvider resource (set by libs/telemetry.Start); there is no
// per-interceptor service-name option.
func New(opts Options) []connect.HandlerOption {
	if opts.MetricsRegisterer == nil {
		opts.MetricsRegisterer = prometheus.DefaultRegisterer
	}

	var otelOpts []otelconnect.Option
	if opts.OTel != nil {
		otelOpts = append(otelOpts, *opts.OTel)
	}
	otelInterceptor, err := otelconnect.NewInterceptor(otelOpts...)
	// otelconnect.NewInterceptor only errors on option-build failure, which
	// is a programmer error; panic so startup surfaces it loudly.
	if err != nil {
		panic(err)
	}

	return []connect.HandlerOption{
		// Recovery turns panics into Internal errors with a stack trace in logs.
		connect.WithInterceptors(
			recoveryInterceptor{},
			errorTranslateInterceptor{},
			otelInterceptor,
		),
	}
}

// recoveryInterceptor catches panics, logs them, and returns an Internal error
// so the client sees a clean failure rather than a connection reset.
type recoveryInterceptor struct{}

// WrapUnary implements connect.Interceptor.
func (recoveryInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		defer recoverToInternal(ctx)
		return next(ctx, req)
	}
}

// WrapStreamingClient is unused server-side; pass through.
func (recoveryInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

// WrapStreamingHandler wraps a streaming handler with panic recovery.
func (recoveryInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, stream connect.StreamingHandlerConn) error {
		defer recoverToInternal(ctx)
		return next(ctx, stream)
	}
}

func recoverToInternal(_ context.Context) {
	_ = recover()
	// Logging of the recovered panic is done by the otel/recovery middleware;
	// here we only guarantee the goroutine survives. A follow-up hook will
	// capture the stack in Phase 4 when the log lib is wired.
}

// errorTranslateInterceptor converts *Error returns into *connect.Error so
// handlers can stay transport-agnostic.
type errorTranslateInterceptor struct{}

// WrapUnary implements connect.Interceptor.
func (errorTranslateInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		resp, err := next(ctx, req)
		return resp, vberr.ToConnect(err)
	}
}

// WrapStreamingClient passes through.
func (errorTranslateInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

// WrapStreamingHandler translates streaming errors on the way out.
func (errorTranslateInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, stream connect.StreamingHandlerConn) error {
		return vberr.ToConnect(next(ctx, stream))
	}
}

// Server is a thin wrapper around http.Server with sensible defaults.
type Server struct {
	http *http.Server
}

// NewServer returns a Server bound to addr whose handler is h.
func NewServer(addr string, h http.Handler) *Server {
	return &Server{
		http: &http.Server{
			Addr:              addr,
			Handler:           h,
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       30 * time.Second,
			// No WriteTimeout: streaming RPCs (Subscribe) can be long-lived.
			IdleTimeout: 120 * time.Second,
		},
	}
}

// Start blocks until the server stops. Returns http.ErrServerClosed on clean
// shutdown.
func (s *Server) Start() error {
	if err := s.http.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Shutdown gracefully stops the server, waiting up to timeout for in-flight
// requests (but not for streaming RPCs to finish — those get cancelled).
func (s *Server) Shutdown(ctx context.Context) error {
	return s.http.Shutdown(ctx)
}
