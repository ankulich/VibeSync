// Package redislib defines VibeSync's Redis port: sessions, presence,
// playback cache, and distributed locks.
//
// The concrete go-redis-backed implementation ships with the first service
// that needs it (Phase 4, Auth, for sessions). Tests use the miniredis-based
// stub from libs/testing.
//
// Two interfaces are defined:
//
//   - KV: the key/value surface for sessions and presence.
//   - Lock: distributed mutex with TTL + fencing token.
//
// The Lock contract enforces the fencing-token pattern: callers MUST persist
// the token alongside any external effect (DB row, Kafka message) so that a
// partitioned lock holder cannot corrupt state after its lease expires.
package redislib

import (
	"context"
	"errors"
	"time"
)

// ErrNotHeld indicates a lock acquire failed or a held lock was lost.
var ErrNotHeld = errors.New("redis: lock not held")

// KV is the key/value surface. Values are []byte so callers can store
// protobuf, JSON, or raw bytes uniformly.
type KV interface {
	Get(ctx context.Context, key string) ([]byte, error)
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
	Del(ctx context.Context, keys ...string) (int64, error)
	// Exists reports how many of keys exist.
	Exists(ctx context.Context, keys ...string) (int64, error)
}

// Lock is a distributed mutex. Acquire returns a fencing token that callers
// MUST use to gate external writes; a stale holder (whose TTL expired) must
// see its writes rejected by downstream services that record the highest
// valid token seen.
type Lock interface {
	// Acquire blocks until the lock is held or ctx is canceled. The returned
	// token is monotonically increasing for this lock key.
	Acquire(ctx context.Context, key string, ttl time.Duration) (token uint64, err error)
	// Release releases the lock if the caller's token is current. Returns
	// ErrNotHeld if the lock was already lost or stolen.
	Release(ctx context.Context, key string, token uint64) error
}

// Client bundles KV + Lock for callers that want a single dependency.
type Client interface {
	KV
	Lock
	Close() error
}

// Presence is the "who is online in room X" abstraction. Backed by a Redis
// sorted set scored on heartbeat timestamp; expirations handled by TTL sweep.
type Presence interface {
	// Join marks user as present in room.
	Join(ctx context.Context, roomID, userID string) error
	// Heartbeat refreshes the user's liveness.
	Heartbeat(ctx context.Context, roomID, userID string) error
	// Leave removes the user.
	Leave(ctx context.Context, roomID, userID string) error
	// Active returns the user IDs present in room within the liveness window.
	Active(ctx context.Context, roomID string, within time.Duration) ([]string, error)
}
