// Package config defines the typed configuration for the Provider Service.
package config

import (
	"fmt"
	"strconv"

	"github.com/spf13/viper"

	vbconfig "vibesync/libs/config"
)

// Config is the full Provider Service configuration.
type Config struct {
	DB              DBConfig            `mapstructure:"db"`
	Redis           RedisConfig         `mapstructure:"redis"`
	Web             WebConfig           `mapstructure:"web"`
	Log             LogConfig           `mapstructure:"log"`
	OTel            OTelConfig          `mapstructure:"otel"`
	Spotify         ProviderCredsConfig `mapstructure:"spotify"`
	YouTube         ProviderCredsConfig `mapstructure:"youtube"`
	AuthServiceAddr string              `mapstructure:"auth_service_addr"`
}

// DBConfig is the Postgres connection.
type DBConfig struct {
	Host     string `mapstructure:"host"`
	Port     int    `mapstructure:"port"`
	User     string `mapstructure:"user"`
	Password string `mapstructure:"password"`
	Database string `mapstructure:"database"`
	SSLMode  string `mapstructure:"sslmode"`
	MaxConns int32  `mapstructure:"max_conns"`
}

// RedisConfig is the Redis connection.
type RedisConfig struct {
	Addr     string `mapstructure:"addr"`
	Password string `mapstructure:"password"`
	DB       int    `mapstructure:"db"`
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

// ProviderCredsConfig holds the API credentials for one external provider.
// ClientID/ClientSecret are used by Spotify; YouTube is keyless (oEmbed, see
// ADR-0016) and needs no credentials — only the Enabled toggle.
type ProviderCredsConfig struct {
	ClientID     string `mapstructure:"client_id"`
	ClientSecret string `mapstructure:"client_secret"`
	Enabled      bool   `mapstructure:"enabled"`
}

// Defaults returns the Viper defaults map.
func Defaults() map[string]any {
	return map[string]any{
		"db.host":                   "localhost",
		"db.port":                   5432,
		"db.user":                   "vibesync",
		"db.database":               "provider",
		"db.sslmode":                "disable",
		"db.max_conns":              10,
		"redis.addr":                "localhost:6379",
		"redis.db":                  0,
		"web.addr":                  ":8086",
		"web.rate_limit_per_second": 10,
		"log.level":                 "info",
		"log.format":                "json",
		"otel.endpoint":             "localhost:4317",
		"otel.sample_ratio":         1.0,
		"spotify.enabled":           false,
		"youtube.enabled":           true,
		"auth_service_addr":         "localhost:8080",
	}
}

// LoadViper builds and loads the Viper instance.
func LoadViper(env vbconfig.Env, extraDirs ...string) (*viper.Viper, error) {
	loader := vbconfig.New(vbconfig.Options{
		Service: "provider-service", Env: env, ConfigDirs: extraDirs, Defaults: Defaults(),
	})
	v, err := loader.Load()
	if err != nil {
		return nil, fmt.Errorf("provider config: %w", err)
	}
	return v, nil
}

// Addr returns "host:port".
func (c DBConfig) Addr() string {
	if c.Port == 0 {
		return c.Host
	}
	return c.Host + ":" + strconv.Itoa(c.Port)
}
