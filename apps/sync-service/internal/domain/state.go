package domain

import "time"

// PlaybackStatus mirrors the proto enum but as a domain type (pure Go, no
// generated-code dependency in the domain).
type PlaybackStatus int8

const (
	// StatusUnspecified is the zero value (invalid for persisted state).
	StatusUnspecified PlaybackStatus = 0
	// StatusPaused means playback is halted; media_time is constant.
	StatusPaused PlaybackStatus = 1
	// StatusPlaying means playback is active at playback_rate.
	StatusPlaying PlaybackStatus = 2
	// StatusBuffering means the media source is loading.
	StatusBuffering PlaybackStatus = 3
	// StatusEnded means the media item has finished.
	StatusEnded PlaybackStatus = 4
)

// SyncState is the authoritative playback state for a room. Mirrors the proto
// message but as a plain Go struct so the domain stays free of generated-code
// dependencies.
type SyncState struct {
	RoomID       string
	MediaID      string
	Status       PlaybackStatus
	MediaTimeMs  int64
	WallTimeMs   int64
	PlaybackRate float64
	Epoch        uint64
	HostID       string
	FencingToken uint64
	EpochStarted time.Time
}

// AdvanceMediaTime computes the current media position given the elapsed wall
// time since the last sample. This is the formula from the algorithm spec:
//
//	media_time(t_now) = state.media_time_ms + (t_now - state.wall_time_ms) * playback_rate
//
// When playback_rate == 0 (paused), the position is constant.
func (s *SyncState) AdvanceMediaTime(nowMs int64) int64 {
	if s.PlaybackRate == 0 {
		return s.MediaTimeMs
	}
	elapsed := nowMs - s.WallTimeMs
	if elapsed < 0 {
		elapsed = 0 // clock skew; don't rewind
	}
	return s.MediaTimeMs + int64(float64(elapsed)*s.PlaybackRate)
}

// CommandKind enumerates the authoritative commands a host/moderator can issue.
type CommandKind int8

const (
	// CmdUnspecified is the zero value (invalid).
	CmdUnspecified CommandKind = 0
	// CmdPlay resumes playback at the current position.
	CmdPlay CommandKind = 1
	// CmdPause halts playback, retaining position.
	CmdPause CommandKind = 2
	// CmdSeek jumps to a specific media position.
	CmdSeek CommandKind = 3
	// CmdSetRate changes the playback rate (0.25..4.0).
	CmdSetRate CommandKind = 4
	// CmdLoadMedia loads a new media item.
	CmdLoadMedia CommandKind = 5
	// CmdNext advances to the next queue item.
	CmdNext CommandKind = 6
	// CmdPrevious returns to the previous queue item.
	CmdPrevious CommandKind = 7
)

// ApplyCommand applies a command to the state, bumping the epoch and re-sampling
// the wall time. Returns the new epoch. The caller validates authorization
// (host role + fencing token) BEFORE calling this.
func (s *SyncState) ApplyCommand(kind CommandKind, seekToMs *int64, rate *float64, mediaID string, nowMs int64, now time.Time) {
	// First, compute the current media time at the moment of the command (so
	// a pause after play retains the correct position).
	if s.Status == StatusPlaying {
		s.MediaTimeMs = s.AdvanceMediaTime(nowMs)
	}
	s.WallTimeMs = nowMs

	switch kind {
	case CmdPlay:
		s.Status = StatusPlaying
		if s.PlaybackRate == 0 {
			s.PlaybackRate = 1.0
		}
	case CmdPause:
		s.Status = StatusPaused
		s.PlaybackRate = 0
	case CmdSeek:
		if seekToMs != nil {
			s.MediaTimeMs = *seekToMs
		}
	case CmdSetRate:
		if rate != nil && *rate >= 0.25 && *rate <= 4.0 {
			s.PlaybackRate = *rate
			if *rate > 0 {
				s.Status = StatusPlaying
			}
		}
	case CmdLoadMedia:
		s.MediaID = mediaID
		s.MediaTimeMs = 0
		s.Status = StatusPaused
		s.PlaybackRate = 0
	case CmdNext, CmdPrevious:
		// Queue management is Phase 9 (Media Service); for now these are
		// treated as load-next/prev, which requires the queue. Phase 7
		// accepts them but they're no-ops without a queue.
	}
	s.Epoch++
	s.EpochStarted = now
}
