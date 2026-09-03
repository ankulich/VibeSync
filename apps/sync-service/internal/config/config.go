// Package config defines the typed configuration for the Sync Service.
// Sync is both a consumer (room.created.v1) and a producer (sync.updated.v1),
// plus has sync-specific tuning knobs for the drift controller and host
// migration.
package config

import (
	"fmt"
	"strconv"
	"time"

	"github.com/spf13/viper"

	vbconfig "vibesync/libs/config"
)

// Config is the full Sync Service configuration.
type Config struct {
	DB             DBConfig    `mapstructure:"db"`
	Redis          RedisConfig `mapstructure:"redis"`
	Kafka          KafkaConfig `mapstructure:"kafka"`
	Sync           SyncConfig  `mapstructure:"sync"`
	Web            WebConfig   `mapstructure:"web"`
	Log            LogConfig   `mapstructure:"log"`
	OTel           OTelConfig  `mapstructure:"otel"`
	RoomServiceAddr string     `mapstructure:"room_service_addr"`
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

// KafkaConfig holds consumer + producer settings.
type KafkaConfig struct {
	Brokers         []string `mapstructure:"brokers"`
	ConsumerTopic   string   `mapstructure:"consumer_topic"`
	ConsumerGroupID string   `mapstructure:"consumer_group_id"`
}

// SyncConfig holds the drift controller and host migration tuning knobs.
type SyncConfig struct {
	HeartbeatInterval         time.Duration `mapstructure:"heartbeat_interval"`
	HostTimeout               time.Duration `mapstructure:"host_timeout"`
	SnapshotInterval          time.Duration `mapstructure:"snapshot_interval"`
	ControllerKp              float64       `mapstructure:"controller_kp"`
	ControllerKi              float64       `mapstructure:"controller_ki"`
	ControllerIntegralClampMs float64       `mapstructure:"controller_integral_clamp_ms"`
	RecoverRingBufferSize     int           `mapstructure:"recover_ring_buffer_size"`
	DiscontinuityThresholdMs  float64       `mapstructure:"discontinuity_threshold_ms"`
	// DriftCorrectionEnabled re-enables the P+I heartbeat-drift nudging of
	// the authoritative clock. OFF by default: the room clock is defined by
	// the owner's commands (play/pause/seek/load) — see
	// docs/sync/algorithm.md "Drift correction". Unreliable drift inputs
	// (skewed client clocks, frozen players) made the nudging rewind every
	// viewer by up to a second every second, perceived as the video
	// looping on a fragment.
	DriftCorrectionEnabled bool `mapstructure:"drift_correction_enabled"`
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

// Defaults returns the Viper defaults map.
func Defaults() map[string]any {
	return map[string]any{
		"db.host":                           "localhost",
		"db.port":                           5432,
		"db.user":                           "vibesync",
		"db.database":                       "sync",
		"db.sslmode":                        "disable",
		"db.max_conns":                      10,
		"redis.addr":                        "localhost:6379",
		"redis.db":                          0,
		"kafka.brokers":                     []string{"localhost:9094"},
		"kafka.consumer_topic":              "room.created.v1",
		"kafka.consumer_group_id":           "sync-service",
		"sync.heartbeat_interval":           "1s",
		"sync.host_timeout":                 "5s",
		"sync.snapshot_interval":            "5s",
		"sync.controller_kp":                0.15,
		"sync.controller_ki":                0.02,
		"sync.controller_integral_clamp_ms": 200.0,
		"sync.recover_ring_buffer_size":     32,
		"sync.discontinuity_threshold_ms":   2000.0,
		"sync.drift_correction_enabled":     false,
		"room_service_addr":                 "localhost:8082",
		"web.addr":                          ":8083",
		"web.rate_limit_per_second":         10,
		"log.level":                         "info",
		"log.format":                        "json",
		"otel.endpoint":                     "localhost:4317",
		"otel.sample_ratio":                 1.0,
	}
}

// LoadViper builds and loads the Viper instance.
func LoadViper(env vbconfig.Env, extraDirs ...string) (*viper.Viper, error) {
	loader := vbconfig.New(vbconfig.Options{
		Service:    "sync-service",
		Env:        env,
		ConfigDirs: extraDirs,
		Defaults:   Defaults(),
	})
	v, err := loader.Load()
	if err != nil {
		return nil, fmt.Errorf("sync config: %w", err)
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
