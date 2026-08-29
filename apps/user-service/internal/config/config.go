// Package config defines the typed configuration for the User Service.
//
// Simpler than Auth's: no OAuth/crypto/signing sections. The User Service is
// a consumer + read-model, so its config covers DB, Redis (idempotency),
// Kafka (consumer), web, log, and otel. Secrets via env only (ADR-0007).
package config

import (
	"fmt"
	"strconv"

	"github.com/spf13/viper"

	vbconfig "vibesync/libs/config"
)

// Config is the full User Service configuration.
type Config struct {
	DB    DBConfig    `mapstructure:"db"`
	Redis RedisConfig `mapstructure:"redis"`
	Kafka KafkaConfig `mapstructure:"kafka"`
	Web   WebConfig   `mapstructure:"web"`
	Log   LogConfig   `mapstructure:"log"`
	OTel  OTelConfig  `mapstructure:"otel"`
}

// DBConfig is the Postgres connection. The User Service uses the `user`
// logical database (created by the compose init script).
type DBConfig struct {
	Host     string `mapstructure:"host"`
	Port     int    `mapstructure:"port"`
	User     string `mapstructure:"user"`
	Password string `mapstructure:"password"` // from VB_DB_PASSWORD env
	Database string `mapstructure:"database"` // "user"
	SSLMode  string `mapstructure:"sslmode"`
	MaxConns int32  `mapstructure:"max_conns"`
}

// RedisConfig is the Redis connection (for the idempotency middleware).
type RedisConfig struct {
	Addr     string `mapstructure:"addr"`
	Password string `mapstructure:"password"`
	DB       int    `mapstructure:"db"`
}

// KafkaConfig holds consumer settings.
type KafkaConfig struct {
	Brokers         []string `mapstructure:"brokers"`
	Topic           string   `mapstructure:"topic"`             // consumed topic, e.g. "user.created.v1"
	ConsumerGroupID string   `mapstructure:"consumer_group_id"` // e.g. "user-service"
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

// Defaults returns the Viper defaults map for the User Service.
func Defaults() map[string]any {
	return map[string]any{
		"db.host":                   "localhost",
		"db.port":                   5432,
		"db.user":                   "vibesync",
		"db.database":               "user",
		"db.sslmode":                "disable",
		"db.max_conns":              10,
		"redis.addr":                "localhost:6379",
		"redis.db":                  0,
		"kafka.brokers":             []string{"localhost:9094"},
		"kafka.topic":               "user.created.v1",
		"kafka.consumer_group_id":   "user-service",
		"web.addr":                  ":8081",
		"web.rate_limit_per_second": 10,
		"log.level":                 "info",
		"log.format":                "json",
		"otel.endpoint":             "localhost:4317",
		"otel.sample_ratio":         1.0,
	}
}

// LoadViper builds and loads the Viper instance using libs/config.
func LoadViper(env vbconfig.Env, extraDirs ...string) (*viper.Viper, error) {
	loader := vbconfig.New(vbconfig.Options{
		Service:    "user-service",
		Env:        env,
		ConfigDirs: extraDirs,
		Defaults:   Defaults(),
	})
	v, err := loader.Load()
	if err != nil {
		return nil, fmt.Errorf("user config: %w", err)
	}
	return v, nil
}

// Addr returns "host:port" for connection strings. Kept for parity with Auth.
func (c DBConfig) Addr() string {
	if c.Port == 0 {
		return c.Host
	}
	return c.Host + ":" + strconv.Itoa(c.Port)
}
