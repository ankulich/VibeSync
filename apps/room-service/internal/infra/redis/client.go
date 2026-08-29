// Package redis contains the Redis client adapter for the Room Service. Used
// by the outbox relay for leader election.
package redis

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Client wraps *redis.Client for the relay's leader election.
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
