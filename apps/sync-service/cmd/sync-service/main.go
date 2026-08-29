// Command sync-service is the VibeSync Sync Service entrypoint.
//
// Wire order (consumer + producer):
//  1. config → observability
//  2. postgres pool → migrations
//  3. redis (presence + relay leader election)
//  4. events: consumer (room.created.v1) + producer (sync.updated.v1) + relay
//  5. presence implementation
//  6. repositories → room manager → app service
//  7. consumer handler + goroutine (with idempotency middleware)
//  8. http server (connect + gin)
//  9. SetReady(true) → serve
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

	"vibesync/apps/sync-service/internal/app"
	"vibesync/apps/sync-service/internal/config"
	"vibesync/apps/sync-service/internal/infra/events"
	"vibesync/apps/sync-service/internal/infra/migrate"
	"vibesync/apps/sync-service/internal/infra/postgres"
	vbredis "vibesync/apps/sync-service/internal/infra/redis"
	"vibesync/apps/sync-service/internal/infra/server"
	"vibesync/apps/sync-service/internal/ports"
	vbconfig "vibesync/libs/config"
	vbid "vibesync/libs/id"
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
		fmt.Fprintf(os.Stderr, "sync-service: fatal: %v\n", err)
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
		Service:      "sync-service",
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
	logger.Info("sync-service starting",
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

	// 4. Redis (presence + relay leader election).
	redisClient, redisErr := vbredis.NewClient(cfg.Redis.Addr, cfg.Redis.Password, cfg.Redis.DB)
	if redisErr != nil {
		logger.Warn("redis unavailable; presence + relay disabled", "err", redisErr)
	}
	defer func() {
		if redisClient != nil {
			_ = redisClient.Close()
		}
	}()

	// 5. Events: consumer (room.created.v1) + producer + relay.
	var consumer *events.Consumer
	if len(cfg.Kafka.Brokers) > 0 && cfg.Kafka.ConsumerTopic != "" {
		consumer = events.NewConsumer(events.ConsumerOptions{
			Brokers: cfg.Kafka.Brokers,
			GroupID: cfg.Kafka.ConsumerGroupID,
			Topic:   cfg.Kafka.ConsumerTopic,
		}, logger)
	}
	defer func() {
		if consumer != nil {
			_ = consumer.Close()
		}
	}()

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

	// 6. Presence implementation (the first concrete redislib.Presence).
	var presence ports.Presence
	if redisClient != nil {
		presence = vbredis.NewPresence(redisClient.Raw())
	}

	// 7. App service.
	svc := app.New(app.Deps{
		Cfg:      *cfg,
		Pool:     pool,
		States:   postgres.NewSyncStateRepo(),
		Outbox:   postgres.NewOutboxWriter(),
		Presence: presence,
		Clock:    systemClock{},
		IDGen:    ulidGen{},
		Logger:   app.NewSlogAdapter(logger),
	})

	// 8. Consumer handler + goroutine (with idempotency middleware).
	if consumer != nil {
		handler := app.NewRoomCreatedHandler(svc.Manager(), app.NewSlogAdapter(logger))
		var chained vbkafka.Handler = handler
		if redisClient != nil {
			kv := &redisKV{rdb: redisClient.Raw()}
			chained = vbkafka.Chain(
				vbkafka.IdempotencyMiddleware(kv, 24*time.Hour),
			)(handler)
		}
		go func() {
			if err := consumer.Run(ctx, chained); err != nil && !errors.Is(err, context.Canceled) {
				logger.Error("consumer stopped", "err", err)
			}
		}()
		logger.Info("consumer started", "topic", cfg.Kafka.ConsumerTopic, "group", cfg.Kafka.ConsumerGroupID)
	}

	// 9. HTTP server.
	httpHandler, _ := server.New(server.Options{Config: *cfg, Service: svc})
	httpSrv := &http.Server{
		Addr:              cfg.Web.Addr,
		Handler:           httpHandler,
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

	// 10. Mark ready, wait for signal, graceful shutdown.
	vbweb.SetReady(true)
	logger.Info("sync-service ready")

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
	logger.Info("sync-service stopped")
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

// systemClock implements ports.Clock.
type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now().UTC() }
func (systemClock) NowMs() int64   { return time.Now().UTC().UnixMilli() }

// ulidGen implements ports.IDGen.
type ulidGen struct{}

func (ulidGen) New() string { return vbid.New() }

// redisKV adapts go-redis to the kafka.KV interface (SetNX).
type redisKV struct{ rdb *redis.Client }

// SetNX satisfies the kafka.KV interface used by the idempotency middleware.
func (k *redisKV) SetNX(ctx context.Context, key, value string, ttl time.Duration) (bool, error) {
	return k.rdb.SetNX(ctx, key, value, ttl).Result()
}
