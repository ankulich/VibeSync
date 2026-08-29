// Package server wires the Media Service to the HTTP transport.
package server

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"vibesync/apps/media-service/internal/app"
	"vibesync/apps/media-service/internal/config"
	mediav1connect "vibesync/gen/go/vibesync/media/v1/mediav1connect"
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

	connectPath, connectHandler := mediav1connect.NewMediaServiceHandler(
		opts.Service,
		vbrpc.New(vbrpc.Options{ServiceName: "media-service"})...,
	)

	mux := http.NewServeMux()
	mux.Handle(connectPath, vbweb.WrapCORS(connectHandler, opts.Config.Web.CORSAllowOrigins))
	mux.Handle("/", engine)
	return mux, engine
}
