// Package server wires the User Service use cases to the HTTP transport.
// Connect-RPC serves the API (gRPC + HTTP/JSON); Gin serves /healthz, /readyz,
// /metrics. Mirrors the auth-service composition pattern.
package server

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"vibesync/apps/user-service/internal/app"
	"vibesync/apps/user-service/internal/config"
	userv1connect "vibesync/gen/go/vibesync/user/v1/userv1connect"
	vbrpc "vibesync/libs/rpc"
	vbweb "vibesync/libs/web"
)

// Options configure the assembled HTTP server.
type Options struct {
	Config  config.Config
	Service *app.Service
}

// New constructs the http.Handler that serves both Connect (the API) and Gin
// (HTTP-only endpoints). Returns the composite handler plus the Gin engine.
func New(opts Options) (http.Handler, *gin.Engine) {
	engine := vbweb.New(vbweb.Options{
		Mode:               gin.ReleaseMode,
		CORSAllowOrigins:   opts.Config.Web.CORSAllowOrigins,
		RateLimitPerSecond: opts.Config.Web.RateLimitPerSecond,
	})
	engine.GET("/metrics", gin.WrapH(promhttp.Handler()))

	connectPath, connectHandler := userv1connect.NewUserServiceHandler(
		opts.Service,
		vbrpc.New(vbrpc.Options{ServiceName: "user-service"})...,
	)

	mux := http.NewServeMux()
	mux.Handle(connectPath, vbweb.WrapCORS(connectHandler, opts.Config.Web.CORSAllowOrigins))
	mux.Handle("/", engine)
	return mux, engine
}
