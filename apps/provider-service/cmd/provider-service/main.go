// Command provider-service is the VibeSync Provider Service entrypoint.
//
// Wire order (stateless wrapper pattern — no Kafka, no outbox):
//  1. config → observability
//  2. postgres pool → migrations (resolution cache)
//  3. redis (spotify client-credentials token cache, best-effort)
//  4. spotify client (if enabled)
//  5. youtube client (if enabled)
//  6. auth client → resolution cache repo → app service
//  7. http server (connect + gin)
//  8. SetReady(true) → serve until signal
package main

import (
	"context"
	"strings"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"vibesync/apps/provider-service/internal/app"
	"vibesync/apps/provider-service/internal/config"
	"vibesync/apps/provider-service/internal/infra/authclient"
	"vibesync/apps/provider-service/internal/infra/migrate"
	"vibesync/apps/provider-service/internal/infra/postgres"
	vbredis "vibesync/apps/provider-service/internal/infra/redis"
	"vibesync/apps/provider-service/internal/infra/server"
	"vibesync/apps/provider-service/internal/infra/spotify"
	"vibesync/apps/provider-service/internal/infra/youtube"
	vbconfig "vibesync/libs/config"
	vbobs "vibesync/libs/observability"
	vbweb "vibesync/libs/web"
)

var (
	buildVersion = "dev"
	buildCommit  = "unknown"
)

// The Redis client doubles as the Spotify token cache.
var _ spotify.Cache = (*vbredis.Client)(nil)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "provider-service: fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 1. Config.
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	// 2. Observability.
	obs, err := vbobs.Start(ctx, vbobs.Options{
		Service:      "provider-service",
		Version:      buildVersion,
		LogLevel:     cfg.Log.Level,
		LogFormat:    vbobs.LogFormat(cfg.Log.Format),
		OTLPEndpoint: cfg.OTel.Endpoint,
		SampleRatio:  cfg.OTel.SampleRatio,
	})
	if err != nil {
		return fmt.Errorf("observability: %w", err)
	}
	defer obs.Shutdown()
	logger := obs.Logger
	logger.Info("provider-service starting",
		"version", buildVersion, "commit", buildCommit,
		"db_host", cfg.DB.Host, "db_name", cfg.DB.Database)

	// 3. Postgres pool + migrations.
	pool, err := postgres.NewPool(ctx, postgresURL(cfg), cfg.DB.MaxConns)
	if err != nil {
		return fmt.Errorf("postgres: %w", err)
	}
	defer pool.Close()
	if err := migrate.Run(ctx, migrateURL(cfg)); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	logger.Info("migrations applied")

	// 4. Redis (spotify token cache). Best-effort.
	redisClient, redisErr := vbredis.NewClient(cfg.Redis.Addr, cfg.Redis.Password, cfg.Redis.DB)
	if redisErr != nil {
		logger.Warn("redis unavailable; spotify token cache disabled", "err", redisErr)
	}
	defer func() {
		if redisClient != nil {
			_ = redisClient.Close()
		}
	}()

	// 5-6. Provider clients, auth client, and the app service.
	httpClient := &http.Client{Timeout: 10 * time.Second}
	deps := app.Deps{
		Cfg:       *cfg,
		Pool:      pool,
		CacheRepo: postgres.NewResolutionCacheRepo(),
		Tokens:    authclient.NewClient(httpClient, "http://"+cfg.AuthServiceAddr),
		Clock:     systemClock{},
	}
	if cfg.Spotify.Enabled {
		var tokenCache spotify.Cache
		if redisClient != nil {
			tokenCache = redisClient
		}
		deps.Spotify = spotify.NewClient(httpClient, cfg.Spotify.ClientID, cfg.Spotify.ClientSecret, tokenCache)
		logger.Info("spotify provider enabled")
	} else {
		logger.Warn("spotify provider disabled")
	}
	if cfg.YouTube.Enabled {
		deps.YouTube = youtube.NewClient(httpClient)
		logger.Info("youtube provider enabled")
	} else {
		logger.Warn("youtube provider disabled")
	}
	svc := app.New(deps)

	// 7. HTTP server.
	handler, _ := server.New(server.Options{Config: *cfg, Service: svc})
	httpSrv := &http.Server{
		Addr:              cfg.Web.Addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	go func() {
		logger.Info("http listening", "addr", cfg.Web.Addr)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server error", "err", err)
			cancel()
		}
	}()

	// 8. Mark ready, wait for signal, graceful shutdown.
	vbweb.SetReady(true)
	logger.Info("provider-service ready")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	logger.Info("shutdown signal received")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		logger.Error("http shutdown error", "err", err)
	}
	cancel()
	logger.Info("provider-service stopped")
	return nil
}

func loadConfig() (*config.Config, error) {
	v, err := config.LoadViper(vbconfig.EnvLocal)
	if err != nil {
		return nil, err
	}
	var cfg config.Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}
	cfg.DB.Password = os.Getenv("VB_DB_PASSWORD")
	cfg.Redis.Password = os.Getenv("VB_REDIS_PASSWORD")
	if v := os.Getenv("VB_OAUTH_SPOTIFY_CLIENT_ID"); v != "" {
		cfg.Spotify.ClientID = v
	}
	if v := os.Getenv("VB_OAUTH_SPOTIFY_CLIENT_SECRET"); v != "" {
		cfg.Spotify.ClientSecret = v
	}
	return &cfg, nil
}

func postgresURL(cfg *config.Config) string {
	return fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=%s",
		cfg.DB.User, cfg.DB.Password, cfg.DB.Host, cfg.DB.Port,
		cfg.DB.Database, cfg.DB.SSLMode)
}

// migrateURL returns the connection URL for golang-migrate (which registers
// the pgx/v5 driver under the "pgx5" scheme, unlike pgxpool which uses
// "postgres").
func migrateURL(cfg *config.Config) string {
	u := postgresURL(cfg)
	return strings.Replace(u, "postgres://", "pgx5://", 1)
}

// systemClock returns the current UTC time.
type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now().UTC() }
