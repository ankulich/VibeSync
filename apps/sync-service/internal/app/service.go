// Package app implements the Sync Service use cases. The Service type satisfies
// syncv1connect.SyncServiceHandler. The core is RoomSync — a per-room goroutine
// that owns the authoritative clock, heartbeat tracking, drift controller,
// and subscriber broadcast.
package app

import (
	"context"
	"net/http"
	"sync"

	"vibesync/apps/sync-service/internal/config"
	"vibesync/apps/sync-service/internal/domain"
	"vibesync/apps/sync-service/internal/ports"
	commonv1 "vibesync/gen/go/vibesync/common/v1"
	vboutbox "vibesync/libs/outbox"
	vbweb "vibesync/libs/web"
)

// Service is the Sync Service use-case facade.
type Service struct {
	cfg      config.Config
	pool     ports.Pool
	states   ports.SyncStateRepo
	outbox   ports.OutboxWriter
	presence ports.Presence
	perms    ports.RoomPermissions
	clock    ports.Clock
	idgen    ports.IDGen
	logger   Logger

	// manager owns the per-room RoomSync goroutines.
	manager *RoomManager
}

// Logger is the minimal logging interface the app layer needs.
type Logger interface {
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)
}

// Deps bundles all mandatory port implementations.
type Deps struct {
	Cfg      config.Config
	Pool     ports.Pool
	States   ports.SyncStateRepo
	Outbox   ports.OutboxWriter
	Presence ports.Presence
	Perms    ports.RoomPermissions
	Clock    ports.Clock
	IDGen    ports.IDGen
	Logger   Logger
}

// New constructs the Service and starts the RoomManager.
func New(d Deps) *Service {
	if d.Pool == nil || d.States == nil || d.Outbox == nil || d.Clock == nil || d.IDGen == nil {
		panic("sync/app: all core deps are required")
	}
	if d.Logger == nil {
		panic("sync/app: logger is required")
	}
	if d.Perms == nil {
		panic("sync/app: room permissions port is required")
	}
	s := &Service{
		cfg: d.Cfg, pool: d.Pool, states: d.States, outbox: d.Outbox,
		presence: d.Presence, perms: d.Perms, clock: d.Clock, idgen: d.IDGen, logger: d.Logger,
	}
	s.manager = NewRoomManager(d.Cfg, d.Pool, d.States, d.Outbox, d.Presence, d.Clock, d.IDGen, d.Logger)
	return s
}

// Manager exposes the RoomManager for the consumer handler wiring in main.go.
func (s *Service) Manager() *RoomManager { return s.manager }

// ctxDone returns ctx.Err() if cancelled.
func ctxDone(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

// requestSubject carries the caller's identity from Connect headers.
type requestSubject struct {
	UserID     string
	SystemRole commonv1.SystemRole
}

func subjectFromHeader(h http.Header) requestSubject {
	return requestSubject{
		UserID:     h.Get("X-Vibesync-User-Id"),
		SystemRole: vbweb.ParseSystemRole(h.Get("X-Vibesync-System-Role")),
	}
}

// RoomManager owns the per-room RoomSync goroutines.
type RoomManager struct {
	cfg      config.Config
	pool     ports.Pool
	states   ports.SyncStateRepo
	outbox   ports.OutboxWriter
	presence ports.Presence
	clock    ports.Clock
	idgen    ports.IDGen
	logger   Logger

	mu    sync.RWMutex
	rooms map[string]*RoomSync
}

// NewRoomManager constructs a RoomManager.
func NewRoomManager(cfg config.Config, pool ports.Pool, states ports.SyncStateRepo, outbox ports.OutboxWriter, presence ports.Presence, clock ports.Clock, idgen ports.IDGen, logger Logger) *RoomManager {
	return &RoomManager{
		cfg: cfg, pool: pool, states: states, outbox: outbox, presence: presence,
		clock: clock, idgen: idgen, logger: logger, rooms: make(map[string]*RoomSync),
	}
}

// GetOrCreate returns the RoomSync for roomID, creating one if needed.
// On first creation it loads persisted state from the DB (restart recovery).
func (m *RoomManager) GetOrCreate(ctx context.Context, roomID string) (*RoomSync, error) {
	m.mu.RLock()
	rs, ok := m.rooms[roomID]
	m.mu.RUnlock()
	if ok {
		return rs, nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	// Double-check after acquiring write lock.
	if rs, ok := m.rooms[roomID]; ok {
		return rs, nil
	}

	// Load persisted state (if any).
	var state domain.SyncState
	err := func() error {
		tx, err := m.pool.BeginTx(ctx)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback(ctx) }()
		state, err = m.states.Get(ctx, tx, roomID)
		return err
	}()
	if err != nil {
		// No persisted state → initialize fresh.
		state = domain.SyncState{
			RoomID:       roomID,
			Status:       domain.StatusPaused,
			PlaybackRate: 1.0,
			Epoch:        0,
		}
	}

	rs = NewRoomSync(m.cfg, roomID, state,
		m.makePersister(), m.makeStager(),
		m.presence, m.clock, m.idgen, m.logger)
	m.rooms[roomID] = rs
	// The room loop must outlive the request that materialized the room:
	// tying it to the request ctx kills the loop for every future subscriber
	// as soon as that one client disconnects (Start is a no-op once started).
	rs.Start(context.Background())
	return rs, nil
}

// Init creates a room from a room.created.v1 event (called by the consumer).
// Idempotent: if the room already exists, this is a no-op — except that a
// hostless room (materialized by a subscriber before this event arrived)
// adopts the owner as its host, and the owner is always recorded.
func (m *RoomManager) Init(ctx context.Context, roomID, ownerID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if rs, ok := m.rooms[roomID]; ok {
		if rs.AdoptOwnerIfUnset(ownerID) {
			m.logger.Info("hostless room adopted owner from room.created.v1",
				"room_id", roomID, "host_id", ownerID)
		}
		return nil // already initialized
	}
	state := domain.SyncState{
		RoomID:       roomID,
		Status:       domain.StatusPaused,
		PlaybackRate: 1.0,
		Epoch:        0,
		HostID:       ownerID,
		OwnerID:      ownerID,
	}
	rs := NewRoomSync(m.cfg, roomID, state,
		m.makePersister(), m.makeStager(),
		m.presence, m.clock, m.idgen, m.logger)
	m.rooms[roomID] = rs
	rs.Start(context.Background())
	m.logger.Info("room initialized from room.created.v1", "room_id", roomID, "host_id", ownerID)
	return nil
}

// Shutdown stops all room goroutines.
func (m *RoomManager) Shutdown() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, rs := range m.rooms {
		rs.Shutdown()
	}
}

// makePersister returns a closure that persists a SyncState in its own tx.
func (m *RoomManager) makePersister() func(context.Context, domain.SyncState) error {
	return func(ctx context.Context, s domain.SyncState) error {
		tx, err := m.pool.BeginTx(ctx)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if err := m.states.Upsert(ctx, tx, s); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
}

// makeStager returns a closure that stages an outbox event in its own tx.
func (m *RoomManager) makeStager() ports.OutboxStager {
	return func(ctx context.Context, event vboutbox.Event) error {
		tx, err := m.pool.BeginTx(ctx)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if err := m.outbox.Append(ctx, tx, event); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
}
