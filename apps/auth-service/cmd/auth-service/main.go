// Command auth-service is the VibeSync Auth Service entrypoint.
//
// Wire order (per ADR Phase 4 plan):
//  1. config  → observability
//  2. postgres pool → migrations
//  3. redis → crypto (keycipher, jwt signer w/ bootstrap-key-on-empty-DB, password)
//  4. events (producer, publisher, relay)
//  5. oauth registry (spotify + google, gated by config)
//  6. repositories → app service
//  7. http server (connect + gin)
//  8. SetReady(true) → serve until signal
package main

import (
	"context"
	"strings"
	"crypto/rsa"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"

	"vibesync/apps/auth-service/internal/app"
	"vibesync/apps/auth-service/internal/config"
	"vibesync/apps/auth-service/internal/domain"
	"vibesync/apps/auth-service/internal/infra/crypto"
	"vibesync/apps/auth-service/internal/infra/events"
	"vibesync/apps/auth-service/internal/infra/migrate"
	"vibesync/apps/auth-service/internal/infra/oauth"
	"vibesync/apps/auth-service/internal/infra/postgres"
	vbredis "vibesync/apps/auth-service/internal/infra/redis"
	"vibesync/apps/auth-service/internal/infra/server"
	"vibesync/apps/auth-service/internal/ports"
	vbconfig "vibesync/libs/config"
	vbid "vibesync/libs/id"
	vbobs "vibesync/libs/observability"
	vbweb "vibesync/libs/web"
)

// buildVersion / buildCommit are stamped by the Dockerfile (-ldflags -X).
var (
	buildVersion = "dev"
	buildCommit  = "unknown"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "auth-service: fatal: %v\n", err)
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
		Service:      "auth-service",
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
	logger.Info("auth-service starting",
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

	// 4. Redis (best-effort).
	redisClient, redisErr := vbredis.NewClient(cfg.Redis.Addr, cfg.Redis.Password, cfg.Redis.DB)
	if redisErr != nil {
		logger.Warn("redis unavailable; rate limiting disabled", "err", redisErr)
	}
	defer func() {
		if redisClient != nil {
			_ = redisClient.Close()
		}
	}()

	// 5. Crypto: master key → keycipher; password hasher; JWT signer.
	masterKey := os.Getenv(cfg.Auth.KeyMasterEnv)
	if masterKey == "" {
		logger.Warn("master key not set; generating an ephemeral one (dev only)")
		mk, err := crypto.GenerateMasterKey()
		if err != nil {
			return fmt.Errorf("generate master key: %w", err)
		}
		masterKey = mk
	}
	cipher, err := crypto.NewKeyCipher(masterKey)
	if err != nil {
		return fmt.Errorf("keycipher: %w", err)
	}
	hasher := crypto.NewPasswordHasher()

	signer, err := bootstrapSigner(ctx, pool, cipher, logger)
	if err != nil {
		return fmt.Errorf("bootstrap signer: %w", err)
	}
	logger.Info("signer ready", "active_kid", signer.ActiveKID())

	// 6. Events: producer, publisher, relay (leader-elected via Redis).
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

	// 7. OAuth registry (nil if all providers disabled → password-only).
	registry := buildOAuthRegistry(cfg)
	if registry == nil {
		logger.Info("no OAuth providers enabled; password-only mode")
	} else {
		logger.Info("OAuth providers enabled", "providers", registry.Names())
	}

	// 8. Repositories → app service.
	svc := app.New(app.Deps{
		Cfg:      *cfg,
		Pool:     pool,
		Users:    postgres.NewUserRepo(),
		Sessions: postgres.NewSessionRepo(),
		Refresh:  postgres.NewRefreshRepo(),
		Keys:     postgres.NewSigningKeyRepo(),
		Flows:    postgres.NewOAuthFlowRepo(),
		Accounts: postgres.NewOAuthAccountRepo(),
		Outbox:   postgres.NewOutboxWriter(),
		Hasher:   hasher,
		Cipher:   cipher,
		Signer:   signer,
		Registry: registry,
		Clock:    systemClock{},
		IDGen:    ulidGen{},
	})

	// 9. HTTP server.
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

	// 10. Mark ready, wait for signal, graceful shutdown.
	vbweb.SetReady(true)
	logger.Info("auth-service ready")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	logger.Info("shutdown signal received")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		logger.Error("http shutdown error", "err", err)
	}
	logger.Info("auth-service stopped")
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
	// Secrets are resolved from env at the use site (never from Viper / YAML).
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

// bootstrapSigner loads or creates the active signing key. On an empty
// signing_keys table (first boot), generates a fresh key, encrypts it, and
// inserts it as active. Subsequent boots load the existing active key +
// retired set.
func bootstrapSigner(ctx context.Context, pool *postgres.Pool, cipher *crypto.KeyCipher, logger *slog.Logger) (*crypto.JWTSigner, error) {
	now := time.Now().UTC()
	keysRepo := postgres.NewSigningKeyRepo()

	var active domain.SigningKey
	var activePriv *rsa.PrivateKey

	err := func() error {
		tx, err := pool.BeginTx(ctx)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback(ctx) }()

		active, err = keysRepo.GetActive(ctx, tx)
		if err != nil {
			// Accept both the raw pgx not-found and our ports.NotFound wrapper
			// (scanSigningKey translates pgx.ErrNoRows → ports.NotFound).
			if !errors.Is(err, pgx.ErrNoRows) && !errors.Is(err, ports.ErrNotFound) {
				return fmt.Errorf("load active key: %w", err)
			}
			// No active key: bootstrap one.
			kid, err := domain.NewKID()
			if err != nil {
				return err
			}
			k, priv, err := domain.GenerateSigningKey(now, kid, cipher.Encrypt, crypto.RSAPublicKeyToJWK)
			if err != nil {
				return err
			}
			if err := keysRepo.Upsert(ctx, tx, k); err != nil {
				return err
			}
			active, activePriv = k, priv
			logger.Info("bootstrapped signing key", "kid", kid)
		} else {
			der, err := cipher.Decrypt(active.PrivateEncrypted)
			if err != nil {
				// Almost always a master-key mismatch: the stored key was
				// encrypted under a different VB_AUTH_KEY_MASTER (e.g. an
				// ephemeral dev key from a previous boot).
				return fmt.Errorf("decrypt active key: %w (master key mismatch? check VB_AUTH_KEY_MASTER)", err)
			}
			priv, err := crypto.RSAPrivateKeyFromPKCS8(der)
			if err != nil {
				return err
			}
			activePriv = priv
		}

		// Commit before constructing the signer (the signer holds no DB ref).
		return tx.Commit(ctx)
	}()
	if err != nil {
		return nil, err
	}

	// Load retired set for verification in a fresh read tx.
	var verifiable []crypto.VerifiableKey
	err = func() error {
		tx, err := pool.BeginTx(ctx)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback(ctx) }()
		stored, err := keysRepo.ListVerifiable(ctx, tx)
		if err != nil {
			return err
		}
		for _, k := range stored {
			if k.KID == active.KID {
				continue
			}
			verifiable = append(verifiable, crypto.NewVerifiableKey(k.KID, k.PublicJWK))
		}
		return tx.Commit(ctx)
	}()
	if err != nil {
		return nil, err
	}

	return crypto.NewJWTSigner(active, activePriv, verifiable)
}

// buildOAuthRegistry constructs the Registry from config. Returns nil if no
// providers are enabled. Each provider's credentials come from env at the use
// site (ClientIDEnv / ClientSecretEnv name the env vars).
// buildOAuthRegistry constructs the Registry from config. Returns nil if no
// providers are enabled. Each provider's credentials come from env at the use
// site (ClientIDEnv / ClientSecretEnv name the env vars).
func buildOAuthRegistry(cfg *config.Config) *oauth.Registry {
	redirect := cfg.OAuth.RedirectURIBase + "/api/v1/oauth/callback"

	// oauth.NewRegistry takes ports.OAuthProvider values; the concrete
	// SpotifyProvider / GoogleProvider satisfy it via their embedded baseProvider.
	// We build the variadic list conditionally per config.
	var providers []ports.OAuthProvider
	if cfg.OAuth.Spotify.Enabled {
		providers = append(providers, oauth.NewSpotifyProvider(oauth.Config{
			ClientID:     os.Getenv(cfg.OAuth.Spotify.ClientIDEnv),
			ClientSecret: os.Getenv(cfg.OAuth.Spotify.ClientSecretEnv),
			Scopes:       cfg.OAuth.Spotify.Scopes,
			RedirectURL:  redirect,
		}))
	}
	if cfg.OAuth.Google.Enabled {
		providers = append(providers, oauth.NewGoogleProvider(oauth.Config{
			ClientID:     os.Getenv(cfg.OAuth.Google.ClientIDEnv),
			ClientSecret: os.Getenv(cfg.OAuth.Google.ClientSecretEnv),
			Scopes:       cfg.OAuth.Google.Scopes,
			RedirectURL:  redirect,
		}))
	}
	if len(providers) == 0 {
		return nil
	}
	return oauth.NewRegistry(providers...)
}

// systemClock implements ports.Clock using time.Now.
type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now().UTC() }

// ulidGen implements ports.IDGen using libs/id.
type ulidGen struct{}

func (ulidGen) New() string { return vbid.New() }
