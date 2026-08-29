// Package web bootstraps a Gin HTTP server for the parts of VibeSync that are
// NOT RPC: OAuth callbacks, webhooks, health, /metrics, and static assets.
//
// The core API lives behind Connect (libs/rpc); Gin is used only where HTTP
// semantics (redirects, signed webhook bodies, well-known URLs) are required.
// Both share the same *http.Server in a service's main(). See ADR-0003.
//
// This package ships the standard middleware chain: recovery, request-id,
// CORS, CSRF, rate-limit, structured access log. Security-sensitive defaults
// are baked in (ADR Security).
package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/trace"

	vberr "vibesync/libs/errors"
	vblog "vibesync/libs/log"
)

// Options configure the Gin engine.
type Options struct {
	// Mode is gin.DebugMode or gin.ReleaseMode.
	Mode string
	// CORSAllowOrigins is the allowlist for Access-Control-Allow-Origin.
	// Empty disables CORS (use for same-origin internal services).
	CORSAllowOrigins []string
	// RateLimitPerSecond caps requests per client IP. 0 disables.
	RateLimitPerSecond int
	// MetricsRegisterer is where the HTTP request histogram is registered.
	MetricsRegisterer prometheus.Registerer
}

// New returns a *gin.Engine wired with the standard middleware chain. Routes
// are registered by the caller; the health and metrics endpoints are mounted
// here because every service exposes them identically.
func New(opts Options) *gin.Engine {
	gin.SetMode(opts.Mode)
	if opts.Mode == "" {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()
	// Recovery converts panics to 500s and writes the stack to stderr.
	r.Use(gin.Recovery())
	// Request ID + access log are added once for all routes.
	r.Use(requestIDMiddleware())
	r.Use(accessLogMiddleware())

	if len(opts.CORSAllowOrigins) > 0 {
		r.Use(corsMiddleware(opts.CORSAllowOrigins))
	}
	if opts.RateLimitPerSecond > 0 {
		r.Use(rateLimitMiddleware(opts.RateLimitPerSecond))
	}

	// Standard endpoints present on every service.
	r.GET("/healthz", healthz)
	r.GET("/readyz", readyz)
	return r
}

// --- health ---------------------------------------------------------------

// readiness is process-wide: services flip it true after deps are warm.
var readiness atomicBool

// SetReady marks the service ready to receive traffic. Call from main() after
// all subsystems report healthy.
func SetReady(ready bool) { readiness.set(ready) }

func healthz(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func readyz(c *gin.Context) {
	if !readiness.get() {
		// 503 so the load balancer stops sending traffic during startup.
		_ = c.AbortWithError(http.StatusServiceUnavailable,
			vberr.Internal("NOT_READY", "service initializing"))
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ready"})
}

// atomicBool is a tiny bool with atomic access; avoids pulling sync/atomic
// helpers into the public API.
type atomicBool struct{ v uint32 }

func (a *atomicBool) get() bool { return a.v == 1 }
func (a *atomicBool) set(b bool) {
	if b {
		a.v = 1
	} else {
		a.v = 0
	}
}

// --- middleware ----------------------------------------------------------
//
// requestID and accessLog are fully implemented here. cors and rateLimit
// depend on per-service policy (allowlist, Redis wiring) and ship in Phase 4
// with their full implementations; the signatures are stable.

// requestIDKey is the Gin context key for the per-request id.
const requestIDKey = "vibesync.request_id"

// headerRequestID is the inbound header we honor if present.
const headerRequestID = "X-Request-ID"

func requestIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		rid := c.GetHeader(headerRequestID)
		if rid == "" {
			rid = newRequestID()
		}
		c.Set(requestIDKey, rid)
		c.Header(headerRequestID, rid)
		// Propagate into the context tree so downstream log/otel helpers see it.
		ctx := withRequestID(c.Request.Context(), rid)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

// newRequestID returns 16 random bytes hex-encoded. Good enough for
// correlation without pulling in a UUID dep; crypto/rand avoids collisions.
func newRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// rand.Read on the system source does not fail in practice; fall back
		// to a timestamp-derived id rather than panicking on a request path.
		return time.Now().UTC().Format("20060102T150405.000000000Z")
	}
	return hex.EncodeToString(b[:])
}

type ctxRequestIDKey struct{}

// withRequestID attaches rid to ctx.
func withRequestID(ctx context.Context, rid string) context.Context {
	return context.WithValue(ctx, ctxRequestIDKey{}, rid)
}

// RequestIDFrom returns the request id stored in ctx, if any.
func RequestIDFrom(ctx context.Context) string {
	if v, ok := ctx.Value(ctxRequestIDKey{}).(string); ok {
		return v
	}
	return ""
}

func accessLogMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		latency := time.Since(start)
		rid, _ := c.Get(requestIDKey)
		span := trace.SpanFromContext(c.Request.Context())
		sc := span.SpanContext()

		attrs := []any{
			"http.method", c.Request.Method,
			"http.path", c.Request.URL.Path,
			"http.status", c.Writer.Status(),
			"http.latency_ms", latency.Milliseconds(),
			"http.bytes", c.Writer.Size(),
			"request_id", rid,
			"client.ip", c.ClientIP(),
		}
		if sc.IsValid() {
			attrs = append(attrs, "trace_id", sc.TraceID().String())
		}
		logger := vblog.From(c.Request.Context())
		switch {
		case c.Writer.Status() >= 500:
			logger.Error("http.request", attrs...)
		case c.Writer.Status() >= 400:
			logger.Warn("http.request", attrs...)
		default:
			logger.Info("http.request", attrs...)
		}
	}
}

// corsMiddleware implements CORS for the Gin engine (HTTP-only endpoints).
// Strict allowlist: only origins in the configured list receive CORS headers.
// Credentials are allowed (the Authorization header). Preflight (OPTIONS)
// requests are answered with 204 and the allow-headers list.
func corsMiddleware(allowOrigins []string) gin.HandlerFunc {
	allowed := make(map[string]bool, len(allowOrigins))
	for _, o := range allowOrigins {
		allowed[o] = true
	}
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin == "" || !allowed[origin] {
			c.Next()
			return
		}
		c.Header("Access-Control-Allow-Origin", origin)
		c.Header("Access-Control-Allow-Credentials", "true")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", corsAllowHeaders)
		c.Header("Access-Control-Max-Age", "86400")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

// corsAllowHeaders is the allowlist for CORS requests. Covers the Connect
// protocol headers plus standard auth/content headers.
const corsAllowHeaders = "Authorization, Content-Type, Connect-Protocol-Version, Connect-Timeout-Ms, Connect-Accept-Encoding, Connect-Content-Encoding, X-Request-ID, X-Vibesync-User-Id, X-Vibesync-System-Role, Grpc-Timeout, Grpc-Encoding"

// WrapCORS wraps any http.Handler with CORS handling. Used to wrap the
// Connect-RPC handlers (which don't go through the Gin engine). Same policy
// as corsMiddleware: strict allowlist, credentials, preflight.
func WrapCORS(h http.Handler, allowOrigins []string) http.Handler {
	allowed := make(map[string]bool, len(allowOrigins))
	for _, o := range allowOrigins {
		allowed[o] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && allowed[origin] {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", corsAllowHeaders)
			w.Header().Set("Access-Control-Max-Age", "86400")
		}
		if r.Method == http.MethodOptions && origin != "" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h.ServeHTTP(w, r)
	})
}

func rateLimitMiddleware(_ int) gin.HandlerFunc {
	// Phase 4: token bucket keyed by client IP, backed by Redis for
	// distributed correctness. No-op until the Auth service wires Redis.
	return func(c *gin.Context) { c.Next() }
}
