// Package server wires the Auth use cases to the HTTP transport. Connect-RPC
// serves the API (gRPC + HTTP/JSON from one handler); Gin serves the
// HTTP-only endpoints (/metrics, /healthz, /readyz). Both share one listener
// via a single http.ServeMux.
package server

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"vibesync/apps/auth-service/internal/app"
	"vibesync/apps/auth-service/internal/config"
	authv1connect "vibesync/gen/go/vibesync/auth/v1/authv1connect"
	vbrpc "vibesync/libs/rpc"
	vbweb "vibesync/libs/web"
)

// Options configure the assembled HTTP server.
type Options struct {
	Config  config.Config
	Service *app.Service
}

// New constructs the http.Handler that serves both Connect (the API) and Gin
// (HTTP-only endpoints). Returns the composite handler plus the Gin engine
// (so main.go can register OAuth redirect routes if needed).
//
// Routing: a single http.ServeMux dispatches by path prefix. Connect owns
// "/vibesync.auth.v1.AuthService/..."; Gin owns everything else (/healthz,
// /readyz, /metrics, and future HTTP-only routes like OAuth callbacks).
func New(opts Options) (http.Handler, *gin.Engine) {
	engine := vbweb.New(vbweb.Options{
		Mode:               gin.ReleaseMode,
		CORSAllowOrigins:   opts.Config.Web.CORSAllowOrigins,
		RateLimitPerSecond: opts.Config.Web.RateLimitPerSecond,
	})

	// /metrics on the Gin engine so it shares the listener and middleware
	// (request-id, access log). Production may prefer a separate internal
	// listener; that's a future operational change, not a Phase 4 concern.
	engine.GET("/metrics", gin.WrapH(promhttp.Handler()))

	// Connect: the API. NewAuthServiceHandler returns the mount path (the
	// service's URL prefix) and an http.Handler.
	connectPath, connectHandler := authv1connect.NewAuthServiceHandler(
		opts.Service,
		vbrpc.New(vbrpc.Options{ServiceName: "auth-service"})...,
	)

	mux := http.NewServeMux()
	mux.Handle(connectPath, vbweb.WrapCORS(connectHandler, opts.Config.Web.CORSAllowOrigins))
	// Gin handles the rest. Mount it at "/" — the ServeMux prefers the more
	// specific Connect path because ServeMux matches longest prefix.
	mux.Handle("/", engine)

	return mux, engine
}
