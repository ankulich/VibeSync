// Command user-service is the VibeSync User Service entrypoint.
//
// Wire order (mirrors auth-service with consumer instead of producer/relay):
//  1. config → observability
//  2. postgres pool → migrations
//  3. redis (for idempotency middleware)
//  4. kafka consumer (consumer group)
//  5. repositories → app service + consumer handler
//  6. http server (connect + gin)
//  7. start consumer goroutine (before SetReady)
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

	"github.com/jackc/pgx/v5"

	"vibesync/apps/user-service/internal/app"
	"vibesync/apps/user-service/internal/config"
	"vibesync/apps/user-service/internal/infra/events"
	"vibesync/apps/user-service/internal/infra/migrate"
	"vibesync/apps/user-service/internal/infra/postgres"
	vbredis "vibesync/apps/user-service/internal/infra/redis"
	"vibesync/apps/user-service/internal/infra/server"
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
		fmt.Fprintf(os.Stderr, "user-service: fatal: %v\n", err)
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
		Service:      "user-service",
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
	logger.Info("user-service starting",
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

	// 4. Redis (for idempotency middleware). Best-effort: the consumer fails
	// open if Redis is down (handler is idempotent via upsert).
	redisClient, redisErr := vbredis.NewClient(cfg.Redis.Addr, cfg.Redis.Password, cfg.Redis.DB)
	if redisErr != nil {
		logger.Warn("redis unavailable; idempotency middleware disabled", "err", redisErr)
	}
	defer func() {
		if redisClient != nil {
			_ = redisClient.Close()
		}
	}()

	// 5. Kafka consumer (consumer group). Starts before SetReady so it drains
	// events from boot.
	var consumer *events.Consumer
	if len(cfg.Kafka.Brokers) > 0 && cfg.Kafka.Topic != "" {
		consumer = events.NewConsumer(events.Options{
			Brokers: cfg.Kafka.Brokers,
			GroupID: cfg.Kafka.ConsumerGroupID,
			Topic:   cfg.Kafka.Topic,
		}, logger)
	}
	defer func() {
		if consumer != nil {
			_ = consumer.Close()
		}
	}()

	// 6. Repositories → app service + consumer handler.
	userRepo := postgres.NewUserRepo()
	svc := app.New(app.Deps{
		Cfg:   *cfg,
		Pool:  pool,
		Users: userRepo,
		Clock: systemClock{},
	})

	// Consumer handler with idempotency middleware.
	var handler vbkafka.Handler
	// The consumer handler gets a TxRunner closure wrapping the pool's tx
	// lifecycle (same semantics as Service.withTx, but standalone so the
	// consumer doesn't depend on the Service struct).
	consumerHandler := app.NewUserCreatedHandler(userRepo, txRunner(pool), logger)
	if redisClient != nil {
		handler = vbkafka.Chain(
			vbkafka.IdempotencyMiddleware(redisClient, 24*time.Hour),
		)(consumerHandler)
		logger.Info("consumer wired with idempotency middleware")
	} else {
		handler = consumerHandler
		logger.Warn("consumer wired WITHOUT idempotency middleware (Redis unavailable); relying on upsert idempotency")
	}

	// Start the consumer goroutine.
	if consumer != nil {
		go func() {
			if err := consumer.Run(ctx, handler); err != nil && !errors.Is(err, context.Canceled) {
				logger.Error("consumer stopped", "err", err)
			}
		}()
		logger.Info("consumer started", "topic", cfg.Kafka.Topic, "group", cfg.Kafka.ConsumerGroupID)
	} else {
		logger.Warn("kafka consumer not configured; read model will not be populated")
	}

	// 7. HTTP server.
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

	// 8. Mark ready, wait for signal, graceful shutdown.
	vbweb.SetReady(true)
	logger.Info("user-service ready")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	logger.Info("shutdown signal received")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		logger.Error("http shutdown error", "err", err)
	}
	// cancel() stops the consumer goroutine.
	cancel()
	logger.Info("user-service stopped")
	return nil
}

// loadConfig builds the typed Config from layered YAML + env.
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

// postgresURL builds the pgx connection string from config.
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

// systemClock implements ports.Clock using time.Now.
type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now().UTC() }

// txRunner returns a TxRunner closure wrapping the pool's transaction lifecycle.
// Used by the consumer handler so it doesn't depend on the Service struct.
func txRunner(pool *postgres.Pool) app.TxRunner {
	return func(ctx context.Context, fn func(ctx context.Context, tx pgx.Tx) error) error {
		tx, err := pool.BeginTx(ctx)
		if err != nil {
			return err
		}
		committed := false
		defer func() {
			if !committed {
				_ = tx.Rollback(ctx)
			}
		}()
		if err := fn(ctx, tx); err != nil {
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		committed = true
		return nil
	}
}
