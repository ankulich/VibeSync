package redis

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrRateLimited is returned by RateLimiter.Allow when the caller has exceeded
// its quota. The web middleware translates this into a 429 response.
var ErrRateLimited = errors.New("rate limit exceeded")

// RateLimiter is a fixed-window counter per key, backed by Redis so the limit
// is correct across multiple Auth instances. Fixed-window (rather than token
// bucket) keeps the Redis op count to one INCR per request and is sufficient
// for our "rough per-IP throttle" goal — sophisticated abuse handling is a
// separate concern (future: WAF + per-user limits).
type RateLimiter struct {
	client *Client
	limit  int64 // max requests per window per key
	window time.Duration
}

// NewRateLimiter constructs a RateLimiter. window is typically time.Second;
// limit is the maximum requests in that window.
func NewRateLimiter(client *Client, limit int, window time.Duration) *RateLimiter {
	return &RateLimiter{client: client, limit: int64(limit), window: window}
}

// Allow reports whether the caller identified by key may proceed. Returns
// ErrRateLimited if over quota. The key is caller-supplied (typically a hashed
// client IP) so the limiter is reusable for any granularity.
func (r *RateLimiter) Allow(ctx context.Context, key string) error {
	// Bucket by truncated window so all requests in the same window share one
	// Redis key. The TTL on IncrWithTTL is set to the window so the key
	// expires at the boundary.
	now := time.Now().UTC()
	bucket := now.Truncate(r.window).Unix()
	redisKey := fmt.Sprintf("rl:%s:%d", key, bucket)
	count, err := r.client.IncrWithTTL(ctx, redisKey, r.window+time.Second)
	if err != nil {
		// Fail open: a Redis outage must not lock Auth out of its own API.
		// Deliberately returning nil on error so the request proceeds without
		// a rate-limit check rather than blocking every request.
		//nolint:nilerr // intentional fail-open behavior
		return nil
	}
	if count > r.limit {
		return ErrRateLimited
	}
	return nil
}
