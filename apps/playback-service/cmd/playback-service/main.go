// Command playback-service is the VibeSync Playback Service entrypoint.
//
// Wire order (consumer-only, like user-service):
//  1. config → observability
//  2. postgres pool → migrations
//  3. redis (for idempotency middleware)
//  4. kafka consumer (sync.updated.v1)
//  5. repositories → app service
//  6. consumer handler + goroutine (with idempotency middleware)
//  7. http server (connect + gin)
//  8. SetReady(true) → serve
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

	"github.com/redis/go-redis/v9"

	"vibesync/apps/playback-service/internal/app"
	"vibesync/apps/playback-service/internal/config"
	"vibesync/apps/playback-service/internal/infra/events"
	"vibesync/apps/playback-service/internal/infra/migrate"
	"vibesync/apps/playback-service/internal/infra/postgres"
	vbredis "vibesync/apps/playback-service/internal/infra/redis"
	"vibesync/apps/playback-service/internal/infra/server"
	vbconfig "vibesync/libs/config"
	vbkafka "vibesync/libs/kafka"
	vbobs "vibesync/libs/observability"
	vbweb "vibesync/libs/web"
)

var (
	buildVersion = "dev"
	buildCommit  = "unknown"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "playback-service: fatal: %v\n", err)
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
		Service: "playback-service", Version: buildVersion,
		LogLevel: cfg.Log.Level, LogFormat: vbobs.LogFormat(cfg.Log.Format),
		OTLPEndpoint: cfg.OTel.Endpoint, SampleRatio: cfg.OTel.SampleRatio,
	})
	if err != nil {
		return fmt.Errorf("observability: %w", err)
	}
	defer obs.Shutdown()
	logger := obs.Logger
	logger.Info("playback-service starting",
		"version", buildVersion, "commit", buildCommit)

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

	// 4. Redis (best-effort).
	redisClient, redisErr := vbredis.NewClient(cfg.Redis.Addr, cfg.Redis.Password, cfg.Redis.DB)
	if redisErr != nil {
		logger.Warn("redis unavailable; idempotency disabled", "err", redisErr)
	}
	defer func() {
		if redisClient != nil {
			_ = redisClient.Close()
		}
	}()

	// 5. Kafka consumer.
	var consumer *events.Consumer
	if len(cfg.Kafka.Brokers) > 0 && cfg.Kafka.Topic != "" {
		consumer = events.NewConsumer(events.Options{
			Brokers: cfg.Kafka.Brokers, GroupID: cfg.Kafka.ConsumerGroupID,
			Topic: cfg.Kafka.Topic,
		}, logger)
	}
	defer func() {
		if consumer != nil {
			_ = consumer.Close()
		}
	}()

	// 6. App service.
	svc := app.New(app.Deps{
		Cfg: *cfg, Pool: pool, Repo: postgres.NewPlaybackRepo(), Clock: systemClock{},
	})

	// 7. Consumer handler + goroutine.
	if consumer != nil {
		handler := app.NewSyncUpdatedHandler(svc.Cache(), logger)
		var chained vbkafka.Handler = handler
		if redisClient != nil {
			kv := &redisKV{rdb: redisClient.Raw()}
			chained = vbkafka.Chain(
				vbkafka.IdempotencyMiddleware(kv, 24*time.Hour),
			)(handler)
			logger.Info("consumer wired with idempotency middleware")
		}
		go func() {
			if err := consumer.Run(ctx, chained); err != nil && !errors.Is(err, context.Canceled) {
				logger.Error("consumer stopped", "err", err)
			}
		}()
		logger.Info("consumer started", "topic", cfg.Kafka.Topic, "group", cfg.Kafka.ConsumerGroupID)
	}

	// 8. HTTP server.
	handler, _ := server.New(server.Options{Config: *cfg, Service: svc})
	httpSrv := &http.Server{
		Addr: cfg.Web.Addr, Handler: handler,
		ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second,
		IdleTimeout: 120 * time.Second,
	}
	go func() {
		logger.Info("http listening", "addr", cfg.Web.Addr)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server error", "err", err)
			cancel()
		}
	}()

	// 9. Ready + serve.
	vbweb.SetReady(true)
	logger.Info("playback-service ready")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	logger.Info("shutdown signal received")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()
	_ = httpSrv.Shutdown(shutdownCtx)
	cancel()
	logger.Info("playback-service stopped")
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

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now().UTC() }

// redisKV adapts go-redis to the kafka.KV interface.
type redisKV struct{ rdb *redis.Client }

// SetNX satisfies the kafka.KV interface used by the idempotency middleware.
func (k *redisKV) SetNX(ctx context.Context, key, value string, ttl time.Duration) (bool, error) {
	return k.rdb.SetNX(ctx, key, value, ttl).Result()
}
