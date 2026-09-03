// Command media-service is the VibeSync Media Service entrypoint.
//
// Wire order (producer pattern, like Room):
//  1. config → observability
//  2. postgres pool → migrations
//  3. redis (for relay leader election)
//  4. events (producer, publisher, relay)
//  5. repositories → app service
//  6. http server (connect + gin)
//  7. start relay goroutine
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

	"vibesync/apps/media-service/internal/app"
	"vibesync/apps/media-service/internal/config"
	"vibesync/apps/media-service/internal/infra/events"
	"vibesync/apps/media-service/internal/infra/migrate"
	"vibesync/apps/media-service/internal/infra/postgres"
	"vibesync/apps/media-service/internal/infra/roomclient"
	vbredis "vibesync/apps/media-service/internal/infra/redis"
	"vibesync/apps/media-service/internal/infra/server"
	vbconfig "vibesync/libs/config"
	vbid "vibesync/libs/id"
	vbobs "vibesync/libs/observability"
	vbweb "vibesync/libs/web"
)

var (
	buildVersion = "dev"
	buildCommit  = "unknown"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "media-service: fatal: %v\n", err)
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
		Service:      "media-service",
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
	logger.Info("media-service starting",
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

	// 4. Redis (for relay leader election). Best-effort.
	redisClient, redisErr := vbredis.NewClient(cfg.Redis.Addr, cfg.Redis.Password, cfg.Redis.DB)
	if redisErr != nil {
		logger.Warn("redis unavailable; outbox relay disabled", "err", redisErr)
	}
	defer func() {
		if redisClient != nil {
			_ = redisClient.Close()
		}
	}()

	// 5. Events: producer, publisher, relay.
	var producer *events.Producer
	if len(cfg.Kafka.Brokers) > 0 {
		producer = events.NewProducer(cfg.Kafka.Brokers)
	}
	defer func() {
		if producer != nil {
			_ = producer.Close()
		}
	}()
	switch {
	case producer != nil && redisClient != nil:
		publisher := events.NewPublisher(producer)
		locker := events.NewRedisLocker(redisClient.Raw())
		pendingPublisher := events.NewPendingPublisher(publisher)
		relay := events.NewRelay(pool, pendingPublisher, locker, events.RelayOptions{})
		go func() {
			if err := relay.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
				logger.Error("outbox relay stopped", "err", err)
			}
		}()
		logger.Info("outbox relay started")
	case producer == nil:
		logger.Warn("kafka producer not configured; events will queue in the outbox")
	default:
		logger.Warn("outbox relay not started (Redis unavailable)")
	}

	// 6. Repositories → app service.
	svc := app.New(app.Deps{
		Cfg:    *cfg,
		Pool:   pool,
		Media:  postgres.NewMediaRepo(),
		Queue:  postgres.NewQueueRepo(),
		Perms:  roomclient.NewClient(http.DefaultClient, "http://"+cfg.RoomServiceAddr),
		Outbox: postgres.NewOutboxWriter(),
		Clock:  systemClock{},
		IDGen:  ulidGen{},
	})

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
	logger.Info("media-service ready")

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
	logger.Info("media-service stopped")
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

// ulidGen generates canonical ULIDs via the shared id library.
type ulidGen struct{}

func (ulidGen) New() string { return vbid.New() }
