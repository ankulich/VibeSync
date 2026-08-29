// Package redis contains the Redis client adapter for the User Service. Used
// by the Kafka idempotency middleware (libs/kafka.IdempotencyMiddleware) to
// dedupe events by ID. The adapter implements the narrow kafka.KV interface
// (SetNX only) rather than the full redislib.Client — the User Service has no
// other Redis needs today.
package redis

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Client wraps *redis.Client and exposes SetNX for the idempotency middleware.
type Client struct {
	rdb *redis.Client
}

// NewClient connects to Redis at addr with the given password and logical DB.
func NewClient(addr, password string, db int) (*Client, error) {
	rdb := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       db,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		_ = rdb.Close()
		return nil, fmt.Errorf("redis: ping: %w", err)
	}
	return &Client{rdb: rdb}, nil
}

// SetNX sets key=value with ttl, but only if key does not already exist.
// Returns true if the key was set (first sighting), false if it already
// existed. Implements the kafka.KV interface.
func (c *Client) SetNX(ctx context.Context, key, value string, ttl time.Duration) (bool, error) {
	ok, err := c.rdb.SetNX(ctx, key, value, ttl).Result()
	if err != nil {
		return false, fmt.Errorf("redis: setnx: %w", err)
	}
	return ok, nil
}

// Close releases the connection pool.
func (c *Client) Close() error {
	if c.rdb == nil {
		return nil
	}
	return c.rdb.Close()
}

// ErrNil is the canonical "key not present" sentinel (currently unused but
// kept for future KV operations the User Service may need).
var ErrNil = errors.New("redis: nil")
