// Package config defines the typed configuration for the Auth Service.
//
// The struct is populated by libs/config (Viper) from layered YAML + env
// (prefix VB_). Secrets (DB password, OAuth client secrets, the key master)
// come ONLY from env; the sample YAML documents the env var names but never
// holds values. See ADR-0007.
package config

import (
	"fmt"
	"strconv"
	"time"

	"github.com/spf13/viper"

	vbconfig "vibesync/libs/config"
)

// Config is the full Auth Service configuration, populated via Viper
// Unmarshal. All durations are parsed as time.Duration by Viper.
type Config struct {
	DB    DBConfig    `mapstructure:"db"`
	Redis RedisConfig `mapstructure:"redis"`
	Kafka KafkaConfig `mapstructure:"kafka"`
	Auth  AuthConfig  `mapstructure:"auth"`
	OAuth OAuthConfig `mapstructure:"oauth"`
	Web   WebConfig   `mapstructure:"web"`
	Log   LogConfig   `mapstructure:"log"`
	OTel  OTelConfig  `mapstructure:"otel"`
}

// DBConfig is the Postgres connection. Password comes from env (VB_DB_PASSWORD).
type DBConfig struct {
	Host     string `mapstructure:"host"`
	Port     int    `mapstructure:"port"`
	User     string `mapstructure:"user"`
	Password string `mapstructure:"password"` // from VB_DB_PASSWORD env
	Database string `mapstructure:"database"` // logical DB name: "auth"
	SSLMode  string `mapstructure:"sslmode"`
	MaxConns int32  `mapstructure:"max_conns"`
}

// Addr returns "host:port" for connection strings.
func (c DBConfig) Addr() string {
	return formatHostPort(c.Host, c.Port)
}

// RedisConfig is the Redis connection.
type RedisConfig struct {
	Addr     string `mapstructure:"addr"`
	Password string `mapstructure:"password"` // from VB_REDIS_PASSWORD env (empty in local)
	DB       int    `mapstructure:"db"`
}

// KafkaConfig is the Kafka bootstrap.
type KafkaConfig struct {
	Brokers []string `mapstructure:"brokers"`
	Topic   string   `mapstructure:"topic"` // base topic prefix, default "vibesync"
}

// AuthConfig holds the auth-domain knobs.
type AuthConfig struct {
	AccessTokenTTL      time.Duration `mapstructure:"access_ttl"`
	RefreshTokenTTL     time.Duration `mapstructure:"refresh_ttl"`
	SessionTTL          time.Duration `mapstructure:"session_ttl"`
	OAuthFlowTTL        time.Duration `mapstructure:"oauth_flow_ttl"`
	KeyRotationInterval time.Duration `mapstructure:"key_rotation_interval"`
	// KeyMasterEnv is the env var name holding the AES-256 master key (32 bytes,
	// base64-encoded) used to encrypt signing keys at rest. The value is read
	// from the environment directly at crypto init time, never from Viper, so
	// it cannot leak into config dumps.
	KeyMasterEnv string `mapstructure:"key_master_env"`
	// Issuer is the JWT `iss` claim. Must match what token consumers expect.
	Issuer string `mapstructure:"issuer"`
}

// OAuthConfig holds the redirect base and per-provider settings.
type OAuthConfig struct {
	// RedirectURIBase is the host the provider redirects back to, e.g.
	// "https://api.vibesync.example". The full callback path is
	// RedirectURIBase + "/api/v1/oauth/callback".
	RedirectURIBase string `mapstructure:"redirect_uri_base"`
	// Spotify is the Spotify provider config. ClientID/ClientSecret are read
	// from env; the YAML documents the env var names.
	Spotify ProviderConfig `mapstructure:"spotify"`
	// Google is the Google/YouTube provider config.
	Google ProviderConfig `mapstructure:"google"`
}

// ProviderConfig is a single OAuth2 provider's settings.
//
// ClientIDEnv and ClientSecretEnv name the env vars that hold the credentials;
// the loader resolves them at startup. Putting the env-var names in config
// keeps the credential plumbing testable without hardcoding.
type ProviderConfig struct {
	ClientIDEnv     string   `mapstructure:"client_id_env"`
	ClientSecretEnv string   `mapstructure:"client_secret_env"`
	Scopes          []string `mapstructure:"scopes"`
	Enabled         bool     `mapstructure:"enabled"`
}

// WebConfig is the HTTP server surface.
type WebConfig struct {
	Addr               string   `mapstructure:"addr"`
	CORSAllowOrigins   []string `mapstructure:"cors_allow_origins"`
	RateLimitPerSecond int      `mapstructure:"rate_limit_per_second"`
}

// LogConfig configures structured logging.
type LogConfig struct {
	Level  string `mapstructure:"level"`
	Format string `mapstructure:"format"`
}

// OTelConfig configures OpenTelemetry exporters.
type OTelConfig struct {
	Endpoint    string  `mapstructure:"endpoint"`
	SampleRatio float64 `mapstructure:"sample_ratio"`
}

// Defaults returns the Viper defaults map for the Auth Service. Used by the
// config loader so a missing YAML key falls back to a sane value rather than
// zero.
func Defaults(serviceVersion string) map[string]any {
	return map[string]any{
		"db.host":                         "localhost",
		"db.port":                         5432,
		"db.user":                         "vibesync",
		"db.database":                     "auth",
		"db.sslmode":                      "disable",
		"db.max_conns":                    10,
		"redis.addr":                      "localhost:6379",
		"redis.db":                        0,
		"kafka.brokers":                   []string{"localhost:9094"},
		"kafka.topic":                     "vibesync",
		"auth.access_ttl":                 "15m",
		"auth.refresh_ttl":                "720h", // 30d
		"auth.session_ttl":                "720h",
		"auth.oauth_flow_ttl":             "10m",
		"auth.key_rotation_interval":      "168h", // 7d
		"auth.key_master_env":             "VB_AUTH_KEY_MASTER",
		"auth.issuer":                     "https://auth.vibesync.local",
		"oauth.redirect_uri_base":         "http://localhost:8080",
		"oauth.spotify.client_id_env":     "VB_OAUTH_SPOTIFY_CLIENT_ID",
		"oauth.spotify.client_secret_env": "VB_OAUTH_SPOTIFY_CLIENT_SECRET",
		"oauth.spotify.scopes":            []string{"user-read-email", "user-read-private"},
		"oauth.spotify.enabled":           false,
		"oauth.google.client_id_env":      "VB_OAUTH_GOOGLE_CLIENT_ID",
		"oauth.google.client_secret_env":  "VB_OAUTH_GOOGLE_CLIENT_SECRET",
		"oauth.google.scopes":             []string{"openid", "email", "profile"},
		"oauth.google.enabled":            false,
		"web.addr":                        ":8080",
		"web.rate_limit_per_second":       10,
		"log.level":                       "info",
		"log.format":                      "json",
		"otel.endpoint":                   "localhost:4317",
		"otel.sample_ratio":               1.0,
	}
}

func formatHostPort(host string, port int) string {
	if port == 0 {
		return host
	}
	return host + ":" + strconv.Itoa(port)
}

// LoadViper builds and loads the Viper instance using libs/config, with the
// Auth Service defaults baked in. Returns the *viper.Viper ready to Unmarshal.
// env selects the YAML overlay (local/dev/prod). Caller resolves secrets from
// env after Unmarshal (Viper does not load them from YAML).
func LoadViper(env vbconfig.Env, extraDirs ...string) (*viper.Viper, error) {
	loader := vbconfig.New(vbconfig.Options{
		Service:    "auth-service",
		Env:        env,
		ConfigDirs: extraDirs,
		Defaults:   Defaults("dev"),
	})
	v, err := loader.Load()
	if err != nil {
		return nil, fmt.Errorf("auth config: %w", err)
	}
	return v, nil
}
