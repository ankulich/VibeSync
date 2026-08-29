// Package app implements the Playback Service use cases. The Service satisfies
// playbackv1connect.PlaybackServiceHandler. The core is RoomCache — an
// in-memory map of roomID → PlaybackRoom guarded by a mutex, updated by the
// sync.updated.v1 consumer and served by the RPC handlers.
package app

import (
	"context"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"vibesync/apps/playback-service/internal/config"
	"vibesync/apps/playback-service/internal/domain"
	"vibesync/apps/playback-service/internal/ports"
)

// Service is the Playback Service use-case facade.
type Service struct {
	cfg   config.Config
	pool  ports.Pool
	repo  ports.PlaybackRepo
	cache *RoomCache
	clock ports.Clock
}

// Deps bundles all mandatory port implementations.
type Deps struct {
	Cfg   config.Config
	Pool  ports.Pool
	Repo  ports.PlaybackRepo
	Clock ports.Clock
}

// New constructs the Service.
func New(d Deps) *Service {
	if d.Pool == nil || d.Repo == nil || d.Clock == nil {
		panic("playback/app: all core deps are required")
	}
	s := &Service{
		cfg: d.Cfg, pool: d.Pool, repo: d.Repo, clock: d.Clock,
		cache: NewRoomCache(),
	}
	return s
}

// Cache exposes the room cache for the consumer handler.
func (s *Service) Cache() *RoomCache { return s.cache }

// withTx runs fn inside a Postgres transaction.
func (s *Service) withTx(ctx context.Context, fn func(ctx context.Context, tx pgx.Tx) error) (err error) {
	tx, err := s.pool.BeginTx(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(ctx)
			panic(p)
		}
		if err != nil {
			_ = tx.Rollback(ctx)
			return
		}
		err = tx.Commit(ctx)
	}()
	return fn(ctx, tx)
}

// ctxDone returns ctx.Err() if cancelled.
func ctxDone(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

// RoomCache is the in-memory roomID → PlaybackRoom cache. Thread-safe.
type RoomCache struct {
	mu    sync.RWMutex
	rooms map[string]*domain.PlaybackRoom
}

// NewRoomCache constructs a RoomCache.
func NewRoomCache() *RoomCache {
	return &RoomCache{rooms: make(map[string]*domain.PlaybackRoom)}
}

// ApplyFromEvent updates the cache from a sync.updated.v1 event.
func (c *RoomCache) ApplyFromEvent(e domain.SyncUpdatedV1, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	room, ok := c.rooms[e.RoomID]
	if !ok {
		room = &domain.PlaybackRoom{RoomID: e.RoomID}
		c.rooms[e.RoomID] = room
	}
	room.ApplyFromEvent(e, now)
}

// Get returns a copy of the cached room state.
func (c *RoomCache) Get(roomID string) (domain.PlaybackRoom, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	room, ok := c.rooms[roomID]
	if !ok {
		return domain.PlaybackRoom{}, false
	}
	return *room, true
}

// ApplySyncCommand applies an authoritative state with fencing enforcement.
// Returns true if applied, false if rejected (stale/redundant).
func (c *RoomCache) ApplySyncCommand(
	roomID string, mediaID string, status int16,
	mediaTimeMs int64, wallTimeMs int64, playbackRate float64,
	epoch uint64, hostID string, fencingToken uint64, now time.Time,
) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	room, ok := c.rooms[roomID]
	if !ok {
		room = &domain.PlaybackRoom{RoomID: roomID}
		c.rooms[roomID] = room
	}
	return room.ApplySyncCommand(mediaID, status, mediaTimeMs, wallTimeMs,
		playbackRate, epoch, hostID, fencingToken, now)
}

// LoadMedia sets the media_id for a room in the cache.
func (c *RoomCache) LoadMedia(roomID, mediaID string, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	room, ok := c.rooms[roomID]
	if !ok {
		room = &domain.PlaybackRoom{RoomID: roomID}
		c.rooms[roomID] = room
	}
	room.LoadMedia(mediaID, now)
}

// TxRunner runs a function inside a Postgres tx.
type TxRunner func(ctx context.Context, fn func(ctx context.Context, tx pgx.Tx) error) error
