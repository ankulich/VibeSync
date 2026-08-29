// Package server wires the Sync Service to the HTTP transport.
package server

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"vibesync/apps/sync-service/internal/app"
	"vibesync/apps/sync-service/internal/config"
	syncv1connect "vibesync/gen/go/vibesync/sync/v1/syncv1connect"
	vbrpc "vibesync/libs/rpc"
	vbweb "vibesync/libs/web"
)

// Options configure the assembled HTTP server.
type Options struct {
	Config  config.Config
	Service *app.Service
}

// New constructs the http.Handler (Connect + Gin).
func New(opts Options) (http.Handler, *gin.Engine) {
	engine := vbweb.New(vbweb.Options{
		Mode:               gin.ReleaseMode,
		CORSAllowOrigins:   opts.Config.Web.CORSAllowOrigins,
		RateLimitPerSecond: opts.Config.Web.RateLimitPerSecond,
	})
	engine.GET("/metrics", gin.WrapH(promhttp.Handler()))

	connectPath, connectHandler := syncv1connect.NewSyncServiceHandler(
		opts.Service,
		vbrpc.New(vbrpc.Options{ServiceName: "sync-service"})...,
	)

	mux := http.NewServeMux()
	mux.Handle(connectPath, vbweb.WrapCORS(connectHandler, opts.Config.Web.CORSAllowOrigins))
	mux.Handle("/", engine)
	return mux, engine
}
