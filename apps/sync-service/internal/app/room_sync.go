package app

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"vibesync/apps/sync-service/internal/config"
	"vibesync/apps/sync-service/internal/domain"
	"vibesync/apps/sync-service/internal/ports"
	commonv1 "vibesync/gen/go/vibesync/common/v1"
	syncv1 "vibesync/gen/go/vibesync/sync/v1"
	vboutbox "vibesync/libs/outbox"
)

// RoomSync owns the authoritative playback state for one room. It runs a
// background goroutine (1 Hz) that:
//   - applies the P+I drift controller
//   - checks host liveness and triggers migration if needed
//   - periodically broadcasts snapshots to subscribers
//   - periodically stages sync.updated.v1 to the outbox
//
// All state mutations go through methods on this struct, guarded by a mutex.
type RoomSync struct {
	cfg        config.SyncConfig
	roomID     string
	state      domain.SyncState
	heartbeats map[string]*domain.ClientHeartbeat
	controller *domain.DriftController
	ringBuffer []frameEntry

	persist  func(context.Context, domain.SyncState) error
	stager   ports.OutboxStager
	presence ports.Presence
	clock    ports.Clock
	idgen    ports.IDGen
	logger   Logger

	mu          sync.Mutex
	subscribers map[chan *syncv1.SubscribeResponse]struct{}
	cancel      context.CancelFunc
	started     bool
}

// frameEntry is one entry in the ring buffer, tagged with its epoch.
type frameEntry struct {
	epoch uint64
	frame *syncv1.SubscribeResponse
}

// NewRoomSync constructs a RoomSync. persist and stager are function closures
// that handle the tx lifecycle externally (RoomSync doesn't hold a pool).
// Call Start to launch the background loop.
func NewRoomSync(
	cfg config.Config,
	roomID string,
	initial domain.SyncState,
	persist func(context.Context, domain.SyncState) error,
	stager ports.OutboxStager,
	presence ports.Presence,
	clock ports.Clock,
	idgen ports.IDGen,
	logger Logger,
) *RoomSync {
	return &RoomSync{
		cfg:         cfg.Sync,
		roomID:      roomID,
		state:       initial,
		heartbeats:  make(map[string]*domain.ClientHeartbeat),
		controller:  makeController(cfg.Sync),
		persist:     persist,
		stager:      stager,
		presence:    presence,
		clock:       clock,
		idgen:       idgen,
		logger:      logger,
		subscribers: make(map[chan *syncv1.SubscribeResponse]struct{}),
	}
}

func makeController(cfg config.SyncConfig) *domain.DriftController {
	dc := domain.NewDriftController()
	if cfg.ControllerKp > 0 {
		dc.Kp = cfg.ControllerKp
	}
	if cfg.ControllerKi > 0 {
		dc.Ki = cfg.ControllerKi
	}
	if cfg.ControllerIntegralClampMs > 0 {
		dc.IntegralClampMs = cfg.ControllerIntegralClampMs
	}
	if cfg.DiscontinuityThresholdMs > 0 {
		dc.DiscontinuityThresholdMs = cfg.DiscontinuityThresholdMs
	}
	return dc
}

// Start launches the 1 Hz background loop. Safe to call once.
func (rs *RoomSync) Start(ctx context.Context) {
	rs.mu.Lock()
	if rs.started {
		rs.mu.Unlock()
		return
	}
	rs.started = true
	loopCtx, cancel := context.WithCancel(ctx)
	rs.cancel = cancel
	rs.mu.Unlock()

	go rs.runLoop(loopCtx)
}

// Shutdown stops the background loop.
func (rs *RoomSync) Shutdown() {
	rs.mu.Lock()
	if rs.cancel != nil {
		rs.cancel()
	}
	rs.mu.Unlock()
}

// runLoop is the 1 Hz background tick. It applies the drift controller,
// checks host liveness, and periodically broadcasts snapshots + stages outbox.
func (rs *RoomSync) runLoop(ctx context.Context) {
	ticker := time.NewTicker(rs.heartbeatInterval())
	defer ticker.Stop()
	snapshotTimer := time.NewTicker(rs.snapshotInterval())
	defer snapshotTimer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			rs.tick()
		case <-snapshotTimer.C:
			rs.broadcastSnapshot()
			rs.stageOutbox(ctx)
			rs.persistState(ctx)
		}
	}
}

func (rs *RoomSync) heartbeatInterval() time.Duration {
	if rs.cfg.HeartbeatInterval > 0 {
		return rs.cfg.HeartbeatInterval
	}
	return time.Second
}

func (rs *RoomSync) snapshotInterval() time.Duration {
	if rs.cfg.SnapshotInterval > 0 {
		return rs.cfg.SnapshotInterval
	}
	return 5 * time.Second
}

// tick applies the drift controller and checks host liveness.
func (rs *RoomSync) tick() {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	now := rs.clock.Now()
	nowMs := rs.clock.NowMs()

	// Compute active peer count + collect drifts.
	var drifts []float64
	var rtts []float64
	activeCount := 0
	for _, hb := range rs.heartbeats {
		if hb.IsAlive(now, rs.hostTimeout()) {
			activeCount++
			if hb.SmoothedRTT > 0 {
				drifts = append(drifts, hb.DriftMs)
				rtts = append(rtts, hb.SmoothedRTT)
			}
		}
	}

	// Compute confidence (simplified: based on active peer count and RTT
	// consistency). More peers + lower RTT spread → higher confidence.
	confidence := computeConfidence(activeCount, rtts)

	// Apply the drift controller.
	medianDrift := domain.MedianFloats(drifts)
	result := rs.controller.Correct(medianDrift, rs.heartbeatInterval(), activeCount, confidence)

	if result.ForceSnapshot {
		rs.controller.Reset()
		rs.broadcastUpdateLocked()
		return
	}

	if result.CorrectionMs != 0 {
		rs.state.MediaTimeMs -= int64(result.CorrectionMs)
		rs.state.WallTimeMs = nowMs
		rs.state.Epoch++
		rs.broadcastUpdateLocked()
	}

	// Check host liveness.
	rs.checkHostMigrationLocked(now)
}

func (rs *RoomSync) hostTimeout() time.Duration {
	if rs.cfg.HostTimeout > 0 {
		return rs.cfg.HostTimeout
	}
	return 5 * time.Second
}

// checkHostMigrationLocked detects a dead host and selects a successor.
// Must be called with rs.mu held.
func (rs *RoomSync) checkHostMigrationLocked(now time.Time) {
	if rs.state.HostID == "" {
		return // no host yet
	}
	hb, ok := rs.heartbeats[rs.state.HostID]
	if ok && hb.IsAlive(now, rs.hostTimeout()) {
		return // host is alive
	}

	// Host is dead. Select a successor.
	// Prefer the longest-present active peer. If presence is available, use
	// it; otherwise pick the peer with the oldest LastSeen among active.
	var successor string
	bestTime := now
	for userID, h := range rs.heartbeats {
		if userID == rs.state.HostID {
			continue
		}
		if h.IsAlive(now, rs.hostTimeout()) && h.LastSeen.Before(bestTime) {
			bestTime = h.LastSeen
			successor = userID
		}
	}

	if successor == "" {
		// No successor: pause the room.
		if rs.state.Status == domain.StatusPlaying {
			rs.state.Status = domain.StatusPaused
			rs.state.PlaybackRate = 0
			rs.state.Epoch++
			rs.broadcastUpdateLocked()
		}
		rs.logger.Warn("host departed, no successor found; room paused",
			"room_id", rs.roomID, "previous_host", rs.state.HostID)
		return
	}

	// Migrate.
	prevHost := rs.state.HostID
	rs.state.HostID = successor
	rs.state.Epoch++
	rs.state.FencingToken++
	rs.state.EpochStarted = now
	rs.logger.Info("host migrated",
		"room_id", rs.roomID, "previous", prevHost, "new", successor,
		"epoch", rs.state.Epoch, "fencing_token", rs.state.FencingToken)

	// Broadcast the migration frame.
	frame := &syncv1.SubscribeResponse{
		SentAt: timestamppb.New(now),
		Payload: &syncv1.SubscribeResponse_HostMigration{
			HostMigration: &syncv1.HostMigration{
				RoomId:          &commonv1.Id{Value: rs.roomID},
				PreviousHostId:  &commonv1.Id{Value: prevHost},
				NewHostId:       &commonv1.Id{Value: successor},
				NewEpoch:        rs.state.Epoch,
				NewFencingToken: rs.state.FencingToken,
				At:              timestamppb.New(now),
			},
		},
	}
	rs.broadcastLocked(frame)
}

func computeConfidence(activePeers int, rtts []float64) uint32 {
	if activePeers == 0 {
		return 0
	}
	// Base confidence on peer count (more peers → more confidence), capped.
	c := uint32(activePeers * 20)
	if c > 100 {
		c = 100
	}
	// Penalize high RTT spread.
	if len(rtts) > 1 {
		var sum, sumSq float64
		for _, r := range rtts {
			sum += r
			sumSq += r * r
		}
		mean := sum / float64(len(rtts))
		variance := sumSq/float64(len(rtts)) - mean*mean
		if variance > 1000 { // high RTT jitter
			c /= 2
		}
	}
	return c
}

// --- RPC methods ---

// ProcessHeartbeat handles a heartbeat RPC.
func (rs *RoomSync) ProcessHeartbeat(
	roomID string, userID string,
	clientEpoch uint64, clientMediaTimeMs int64, clientWallTimeMs int64, lastServerWallTimeMs int64,
) (serverWallTimeMs int64, serverMediaTimeMs int64, epoch uint64, clientDriftMs int32, smoothedRttMs int32) {
	rs.mu.Lock()
	defer rs.mu.Unlock()

	t2 := rs.clock.NowMs()
	now := rs.clock.Now()

	hb, ok := rs.heartbeats[userID]
	if !ok {
		hb = &domain.ClientHeartbeat{}
		rs.heartbeats[userID] = hb
	}

	// Update the heartbeat with the four-timestamp exchange.
	// t1 = clientWallTimeMs, t2 = server receive time, t3 = server response time.
	t3 := rs.clock.NowMs()
	hb.Update(clientWallTimeMs, t2, t3, now)

	// Compute drift: client's media position (adjusted for clock offset) vs
	// authoritative position at server-now.
	serverMediaTime := rs.state.AdvanceMediaTime(t3)
	clientAdjusted := hb.ClientMediaTimeAt(clientMediaTimeMs)
	hb.DriftMs = clientAdjusted - float64(serverMediaTime)

	// Update presence.
	if rs.presence != nil {
		go func() { _ = rs.presence.Heartbeat(context.Background(), roomID, userID) }()
	}

	return t3, serverMediaTime, rs.state.Epoch, int32(hb.DriftMs), int32(hb.SmoothedRTT)
}

// ProcessCommand handles a Command RPC.
func (rs *RoomSync) ProcessCommand(userID string, kind domain.CommandKind, seekToMs *int64, rate *float64, mediaID string, fencingToken uint64) (epoch uint64, accepted bool, reason string) {
	rs.mu.Lock()
	defer rs.mu.Unlock()

	// Fencing token check: reject if stale.
	if fencingToken < rs.state.FencingToken {
		return rs.state.Epoch, false, "STALE_FENCING_TOKEN"
	}

	// Authorization: originator must be the host.
	if userID != rs.state.HostID {
		return rs.state.Epoch, false, "NOT_HOST"
	}

	nowMs := rs.clock.NowMs()
	now := rs.clock.Now()
	rs.state.ApplyCommand(kind, seekToMs, rate, mediaID, nowMs, now)
	rs.broadcastUpdateLocked()
	return rs.state.Epoch, true, ""
}

// Recover replays buffered frames or returns a full snapshot.
func (rs *RoomSync) Recover(sinceEpoch uint64) (snapshot *syncv1.SyncSnapshot, frames []*syncv1.SubscribeResponse) {
	rs.mu.Lock()
	defer rs.mu.Unlock()

	ringSize := rs.cfg.RecoverRingBufferSize
	if ringSize <= 0 {
		ringSize = 32
	}

	if rs.state.Epoch-sinceEpoch <= uint64(ringSize) && sinceEpoch > 0 {
		// Incremental replay.
		for _, fe := range rs.ringBuffer {
			if fe.epoch > sinceEpoch {
				frames = append(frames, fe.frame)
			}
		}
		return nil, frames
	}

	// Full snapshot.
	return rs.buildSnapshotLocked(), nil
}

// --- subscriber management ---

// RegisterSubscriber creates a channel for receiving broadcast frames.
func (rs *RoomSync) RegisterSubscriber() chan *syncv1.SubscribeResponse {
	ch := make(chan *syncv1.SubscribeResponse, 16)
	rs.mu.Lock()
	rs.subscribers[ch] = struct{}{}
	rs.mu.Unlock()
	return ch
}

// UnregisterSubscriber removes a channel.
func (rs *RoomSync) UnregisterSubscriber(ch chan *syncv1.SubscribeResponse) {
	rs.mu.Lock()
	delete(rs.subscribers, ch)
	rs.mu.Unlock()
	close(ch)
}

// --- broadcasting (must be called with rs.mu held) ---

func (rs *RoomSync) broadcastUpdateLocked() {
	now := rs.clock.Now()
	frame := &syncv1.SubscribeResponse{
		SentAt: timestamppb.New(now),
		Payload: &syncv1.SubscribeResponse_Update{
			Update: rs.stateToProtoLocked(),
		},
	}
	rs.addToRingLocked(frame)
	rs.broadcastLocked(frame)
}

func (rs *RoomSync) broadcastSnapshot() {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	snap := rs.buildSnapshotLocked()
	frame := &syncv1.SubscribeResponse{
		SentAt:  timestamppb.New(rs.clock.Now()),
		Payload: &syncv1.SubscribeResponse_Snapshot{Snapshot: snap},
	}
	rs.addToRingLocked(frame)
	rs.broadcastLocked(frame)
}

func (rs *RoomSync) buildSnapshotLocked() *syncv1.SyncSnapshot {
	now := rs.clock.Now()
	var drifts, rtts []float64
	activeCount := 0
	for _, hb := range rs.heartbeats {
		if hb.IsAlive(now, rs.hostTimeout()) {
			activeCount++
			if hb.SmoothedRTT > 0 {
				drifts = append(drifts, hb.DriftMs)
				rtts = append(rtts, hb.SmoothedRTT)
			}
		}
	}
	return &syncv1.SyncSnapshot{
		State:           rs.stateToProtoLocked(),
		DriftEstimateMs: int32(domain.MaxAbsFloats(drifts)),
		Confidence:      computeConfidence(activeCount, rtts),
		ActivePeers:     uint32(activeCount),
		MedianRttMs:     int32(domain.MedianFloats(rtts)),
		CapturedAt:      timestamppb.New(now),
	}
}

func (rs *RoomSync) stateToProtoLocked() *syncv1.SyncState {
	s := &syncv1.SyncState{
		RoomId:       &commonv1.Id{Value: rs.roomID},
		Status:       syncv1.PlaybackStatus(rs.state.Status),
		MediaTimeMs:  rs.state.MediaTimeMs,
		WallTimeMs:   rs.state.WallTimeMs,
		PlaybackRate: rs.state.PlaybackRate,
		Epoch:        rs.state.Epoch,
		FencingToken: rs.state.FencingToken,
	}
	if rs.state.MediaID != "" {
		s.MediaId = &commonv1.Id{Value: rs.state.MediaID}
	}
	if rs.state.HostID != "" {
		s.HostId = &commonv1.Id{Value: rs.state.HostID}
	}
	s.EpochStartedAt = timestamppb.New(rs.state.EpochStarted)
	return s
}

func (rs *RoomSync) broadcastLocked(frame *syncv1.SubscribeResponse) {
	for ch := range rs.subscribers {
		select {
		case ch <- frame:
		default:
			// Subscriber's buffer is full; drop the frame. The client will
			// get the next snapshot on the periodic tick.
		}
	}
}

func (rs *RoomSync) addToRingLocked(frame *syncv1.SubscribeResponse) {
	ringSize := rs.cfg.RecoverRingBufferSize
	if ringSize <= 0 {
		ringSize = 32
	}
	epoch := rs.state.Epoch
	rs.ringBuffer = append(rs.ringBuffer, frameEntry{epoch: epoch, frame: frame})
	if len(rs.ringBuffer) > ringSize {
		rs.ringBuffer = rs.ringBuffer[len(rs.ringBuffer)-ringSize:]
	}
}

// stageOutbox persists a sync.updated.v1 event to the outbox for Kafka.
func (rs *RoomSync) stageOutbox(ctx context.Context) {
	rs.mu.Lock()
	snapshot := rs.buildSnapshotLocked()
	rs.mu.Unlock()

	payload, _ := json.Marshal(map[string]any{
		"room_id":    rs.roomID,
		"epoch":      snapshot.State.Epoch,
		"status":     int(snapshot.State.Status),
		"media_time": snapshot.State.MediaTimeMs,
		"wall_time":  snapshot.State.WallTimeMs,
		"rate":       snapshot.State.PlaybackRate,
		"host_id":    snapshot.State.HostId.GetValue(),
		"drift_ms":   snapshot.DriftEstimateMs,
		"peers":      snapshot.ActivePeers,
	})
	event := vboutbox.Event{
		ID: rs.idgen.New(), AggregateID: rs.roomID, Topic: "sync.updated.v1",
		Key: rs.roomID, Payload: payload, OccurredAt: rs.clock.Now(), Version: "v1",
	}
	if err := rs.stager(ctx, event); err != nil {
		rs.logger.Error("failed to stage sync.updated.v1", "err", err, "room_id", rs.roomID)
	}
}

// persistState checkpoints the authoritative state to Postgres for restart
// recovery.
func (rs *RoomSync) persistState(ctx context.Context) {
	rs.mu.Lock()
	state := rs.state
	rs.mu.Unlock()
	if rs.persist == nil {
		return
	}
	if err := rs.persist(ctx, state); err != nil {
		rs.logger.Warn("failed to persist sync state", "err", err, "room_id", rs.roomID)
	}
}

// slogAdapter wraps *slog.Logger to satisfy the Logger interface.
type slogAdapter struct{ l *slog.Logger }

// NewSlogAdapter wraps a *slog.Logger to satisfy the Logger interface.
func NewSlogAdapter(l *slog.Logger) Logger { return slogAdapter{l: l} }

// Info logs at info level.
func (s slogAdapter) Info(msg string, args ...any) { s.l.Info(msg, args...) }

// Warn logs at warn level.
func (s slogAdapter) Warn(msg string, args ...any) { s.l.Warn(msg, args...) }

// Error logs at error level.
func (s slogAdapter) Error(msg string, args ...any) { s.l.Error(msg, args...) }
