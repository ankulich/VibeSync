// Package redis contains the Redis client adapter for the Provider Service.
// Backs the Spotify client-credentials token cache.
package redis

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Client wraps *redis.Client for simple string caching.
type Client struct {
	rdb *redis.Client
}

// NewClient dials Redis, pings it, and returns a ready Client.
func NewClient(addr, password string, db int) (*Client, error) {
	rdb := redis.NewClient(&redis.Options{Addr: addr, Password: password, DB: db})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		_ = rdb.Close()
		return nil, fmt.Errorf("redis: ping: %w", err)
	}
	return &Client{rdb: rdb}, nil
}

// Raw returns the underlying *redis.Client for callers that need direct access.
func (c *Client) Raw() *redis.Client { return c.rdb }

// Close releases the underlying Redis connection.
func (c *Client) Close() error {
	if c.rdb == nil {
		return nil
	}
	return c.rdb.Close()
}

// Get returns the string stored at key, or "" when the key is absent (cache
// miss), satisfying the spotify.Cache interface.
func (c *Client) Get(ctx context.Context, key string) (string, error) {
	v, err := c.rdb.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("redis: get %q: %w", key, err)
	}
	return v, nil
}

// Set stores value under key with a TTL, satisfying the spotify.Cache interface.
func (c *Client) Set(ctx context.Context, key, value string, ttl time.Duration) error {
	if err := c.rdb.Set(ctx, key, value, ttl).Err(); err != nil {
		return fmt.Errorf("redis: set %q: %w", key, err)
	}
	return nil
}
