// Package server wires the Playback Service to the HTTP transport.
package server

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"vibesync/apps/playback-service/internal/app"
	"vibesync/apps/playback-service/internal/config"
	playbackv1connect "vibesync/gen/go/vibesync/playback/v1/playbackv1connect"
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

	connectPath, connectHandler := playbackv1connect.NewPlaybackServiceHandler(
		opts.Service,
		vbrpc.New(vbrpc.Options{ServiceName: "playback-service"})...,
	)

	mux := http.NewServeMux()
	mux.Handle(connectPath, vbweb.WrapCORS(connectHandler, opts.Config.Web.CORSAllowOrigins))
	mux.Handle("/", engine)
	return mux, engine
}
