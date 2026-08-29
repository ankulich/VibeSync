// Package redis contains the Redis-backed adapters for Auth: the
// redislib.Client implementation (go-redis), a JWKS cache, and a token-bucket
// rate limiter.
//
// Auth uses Redis for three concerns:
//   - Rate limiting (per-IP token bucket, distributed via Redis for correctness
//     across instances).
//   - JWKS caching (the public key set is hot; cache it with a short TTL).
//   - Idempotency (future: refresh-token family revocation echoes to Redis so
//     a distributed verifier rejects instantly).
package redis

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Client wraps *redis.Client and provides the KV primitives Auth needs. It
// does not implement the full redislib.Client interface today (Auth doesn't
// need distributed locks yet); the events relay adds that when it ships.
type Client struct {
	rdb *redis.Client
}

// NewClient connects to Redis at addr with the given password and logical DB.
// The connection is pooled internally by go-redis.
func NewClient(addr, password string, db int) (*Client, error) {
	rdb := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       db,
	})
	// Fail fast on a bad address rather than letting the first request pay.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		_ = rdb.Close()
		return nil, fmt.Errorf("redis: ping: %w", err)
	}
	return &Client{rdb: rdb}, nil
}

// Get returns the value at key. An empty/missing key returns ErrNil.
func (c *Client) Get(ctx context.Context, key string) ([]byte, error) {
	v, err := c.rdb.Get(ctx, key).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, ErrNil
		}
		return nil, fmt.Errorf("redis: get: %w", err)
	}
	return v, nil
}

// Set writes value at key with ttl.
func (c *Client) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if err := c.rdb.Set(ctx, key, value, ttl).Err(); err != nil {
		return fmt.Errorf("redis: set: %w", err)
	}
	return nil
}

// Del removes keys. Returns the count deleted.
func (c *Client) Del(ctx context.Context, keys ...string) (int64, error) {
	n, err := c.rdb.Del(ctx, keys...).Result()
	if err != nil {
		return 0, fmt.Errorf("redis: del: %w", err)
	}
	return n, nil
}

// Exists returns the count of keys that exist.
func (c *Client) Exists(ctx context.Context, keys ...string) (int64, error) {
	n, err := c.rdb.Exists(ctx, keys...).Result()
	if err != nil {
		return 0, fmt.Errorf("redis: exists: %w", err)
	}
	return n, nil
}

// IncrWithTTL atomically increments key and sets the TTL on the first
// increment. Used by the token-bucket rate limiter: a per-second bucket
// increments on every request, and the bucket expires at the end of the
// window so the key namespace stays small.
func (c *Client) IncrWithTTL(ctx context.Context, key string, ttl time.Duration) (int64, error) {
	// Pipeline: INCR then EXPIRE (only the first INCR creates the key, so
	// EXPIRE applies the TTL to the new key). Race-free within Redis single
	// threaded execution.
	pipe := c.rdb.TxPipeline()
	incr := pipe.Incr(ctx, key)
	pipe.Expire(ctx, key, ttl)
	if _, err := pipe.Exec(ctx); err != nil {
		return 0, fmt.Errorf("redis: incr_with_ttl: %w", err)
	}
	return incr.Val(), nil
}

// Raw exposes the underlying *redis.Client for adapters that need commands
// not surfaced above (e.g. SCAN, EVAL). Use sparingly; the surface above is
// the preferred API.
func (c *Client) Raw() *redis.Client { return c.rdb }

// Close releases the connection pool.
func (c *Client) Close() error { return c.rdb.Close() }

// ErrNil is the canonical "key not present" sentinel for Auth Redis callers.
var ErrNil = errors.New("redis: nil")
