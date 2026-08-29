// Package redis contains the Redis client + Presence implementation for the
// Sync Service. The Sync Service ships the repo's first concrete Presence
// adapter (ADR Phase 7), backed by Redis sorted sets scored on heartbeat time.
package redis

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Client wraps *redis.Client.
type Client struct {
	rdb *redis.Client
}

// NewClient connects to Redis.
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

// Raw exposes the underlying client for the relay's leader election.
func (c *Client) Raw() *redis.Client { return c.rdb }

// Close releases the connection pool.
func (c *Client) Close() error {
	if c.rdb == nil {
		return nil
	}
	return c.rdb.Close()
}

// Presence implements ports.Presence via Redis sorted sets. Each room has a
// sorted set keyed `sync:presence:{roomID}` scored on the heartbeat timestamp
// (Unix milliseconds). Active members are those with scores within the
// liveness window. Expired entries are swept lazily by Active().
type Presence struct {
	rdb *redis.Client
}

// NewPresence constructs a Presence adapter.
func NewPresence(rdb *redis.Client) *Presence { return &Presence{rdb: rdb} }

func presenceKey(roomID string) string { return "sync:presence:" + roomID }

// Join marks a user as present in a room.
func (p *Presence) Join(ctx context.Context, roomID, userID string) error {
	now := time.Now().UnixMilli()
	return p.rdb.ZAdd(ctx, presenceKey(roomID), redis.Z{Score: float64(now), Member: userID}).Err()
}

// Heartbeat refreshes the user's liveness score.
func (p *Presence) Heartbeat(ctx context.Context, roomID, userID string) error {
	now := time.Now().UnixMilli()
	return p.rdb.ZAdd(ctx, presenceKey(roomID), redis.Z{Score: float64(now), Member: userID}).Err()
}

// Leave removes the user from the room.
func (p *Presence) Leave(ctx context.Context, roomID, userID string) error {
	return p.rdb.ZRem(ctx, presenceKey(roomID), userID).Err()
}

// Active returns user IDs present in the room within the liveness window.
// Expired entries (scores older than now-within) are swept opportunistically.
func (p *Presence) Active(ctx context.Context, roomID string, within time.Duration) ([]string, error) {
	key := presenceKey(roomID)
	now := time.Now().UnixMilli()
	cutoff := now - within.Milliseconds()

	// Sweep expired entries (lazy cleanup).
	p.rdb.ZRemRangeByScore(ctx, key, "-inf", fmt.Sprintf("%d", cutoff-1))

	// Fetch active members using ZRangeArgs (the non-deprecated API as of
	// Redis 6.2+). ByScore + Rev orders by score descending.
	members, err := p.rdb.ZRangeArgs(ctx, redis.ZRangeArgs{
		Key: key, Start: cutoff, Stop: -1, ByScore: true,
	}).Result()
	if err != nil {
		return nil, fmt.Errorf("presence: active: %w", err)
	}
	return members, nil
}
