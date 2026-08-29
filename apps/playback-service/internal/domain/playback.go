// Package domain contains the Playback Service's domain entities. The Playback
// Service is the executor of the Sync Service's authoritative state; its core
// responsibility is fencing-token enforcement (reject stale commands after a
// host migration). See docs/sync/algorithm.md §Host migration step 4.
package domain

import "time"

// PlaybackRoom is the cached authoritative playback state for one room. It
// mirrors the SyncState from sync.v1 but is a plain Go struct.
type PlaybackRoom struct {
	RoomID       string
	MediaID      string
	Status       int16
	MediaTimeMs  int64
	WallTimeMs   int64
	PlaybackRate float64
	Epoch        uint64
	HostID       string
	FencingToken uint64
	UpdatedAt    time.Time
}

// SyncUpdatedV1 is the deserialized payload of a sync.updated.v1 Kafka event.
// Field names match the JSON keys emitted by the Sync Service's stageOutbox.
type SyncUpdatedV1 struct {
	RoomID    string  `json:"room_id"`
	Epoch     uint64  `json:"epoch"`
	Status    int     `json:"status"`
	MediaTime int64   `json:"media_time"`
	WallTime  int64   `json:"wall_time"`
	Rate      float64 `json:"rate"`
	HostID    string  `json:"host_id"`
	DriftMs   int32   `json:"drift_ms"`
	Peers     uint32  `json:"peers"`
}

// ApplyFromEvent updates the room from a sync.updated.v1 event. Always applies
// (the event IS the authoritative state); returns true if the epoch advanced.
func (r *PlaybackRoom) ApplyFromEvent(e SyncUpdatedV1, now time.Time) bool {
	advanced := e.Epoch > r.Epoch
	r.Epoch = e.Epoch
	r.Status = int16(e.Status)
	r.MediaTimeMs = e.MediaTime
	r.WallTimeMs = e.WallTime
	r.PlaybackRate = e.Rate
	r.HostID = e.HostID
	// fencing_token is NOT in the event payload; it's carried in the
	// ApplySyncCommand RPC, not in the periodic snapshot. We keep the last
	// known token (updated via ApplySyncCommand) and don't overwrite it here.
	r.UpdatedAt = now
	return advanced
}

// ApplySyncCommand applies an authoritative state update with fencing-token
// enforcement. Rejects commands whose fencing_token is less than the room's
// current token (stale post-migration commands). Also rejects commands whose
// epoch is not newer (redundant).
//
// Returns true if applied, false if rejected (stale/redundant).
func (r *PlaybackRoom) ApplySyncCommand(
	mediaID string,
	status int16,
	mediaTimeMs int64,
	wallTimeMs int64,
	playbackRate float64,
	epoch uint64,
	hostID string,
	fencingToken uint64,
	now time.Time,
) bool {
	// Fencing: reject commands with a stale token. The token is bumped on
	// host migrations; a partitioned old host may still send commands with
	// the old token. This is the Playback Service's one behavioral
	// responsibility per algorithm.md.
	if fencingToken < r.FencingToken {
		return false
	}

	// Epoch: reject commands that don't advance the epoch (redundant).
	if epoch <= r.Epoch && fencingToken == r.FencingToken {
		return false
	}

	r.MediaID = mediaID
	r.Status = status
	r.MediaTimeMs = mediaTimeMs
	r.WallTimeMs = wallTimeMs
	r.PlaybackRate = playbackRate
	r.Epoch = epoch
	r.HostID = hostID
	r.FencingToken = fencingToken
	r.UpdatedAt = now
	return true
}

// LoadMedia sets the loaded media for the room.
func (r *PlaybackRoom) LoadMedia(mediaID string, now time.Time) {
	r.MediaID = mediaID
	r.UpdatedAt = now
}
