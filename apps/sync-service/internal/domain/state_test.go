package domain

import (
	"testing"
	"time"
)

func TestSyncStateAdvanceMediaTime(t *testing.T) {
	t.Parallel()
	s := SyncState{
		MediaTimeMs:  1000,
		WallTimeMs:   5000,
		PlaybackRate: 1.0,
		Status:       StatusPlaying,
	}
	// 2000ms elapsed: 1000 + 2000 * 1.0 = 3000
	if got := s.AdvanceMediaTime(7000); got != 3000 {
		t.Errorf("AdvanceMediaTime = %d, want 3000", got)
	}
}

func TestSyncStateAdvanceMediaTimePaused(t *testing.T) {
	t.Parallel()
	s := SyncState{
		MediaTimeMs:  1000,
		WallTimeMs:   5000,
		PlaybackRate: 0,
		Status:       StatusPaused,
	}
	// Paused: position stays constant.
	if got := s.AdvanceMediaTime(9999); got != 1000 {
		t.Errorf("AdvanceMediaTime paused = %d, want 1000", got)
	}
}

func TestSyncStateAdvanceMediaTimeHalfRate(t *testing.T) {
	t.Parallel()
	s := SyncState{
		MediaTimeMs:  1000,
		WallTimeMs:   5000,
		PlaybackRate: 0.5,
		Status:       StatusPlaying,
	}
	// 2000ms elapsed at 0.5x: 1000 + 2000 * 0.5 = 2000
	if got := s.AdvanceMediaTime(7000); got != 2000 {
		t.Errorf("AdvanceMediaTime half-rate = %d, want 2000", got)
	}
}

func TestSyncStateAdvanceMediaTimeClockSkew(t *testing.T) {
	t.Parallel()
	s := SyncState{
		MediaTimeMs:  1000,
		WallTimeMs:   5000,
		PlaybackRate: 1.0,
		Status:       StatusPlaying,
	}
	// now < wall_time: clock went backwards → elapsed clamped to 0, position unchanged.
	if got := s.AdvanceMediaTime(3000); got != 1000 {
		t.Errorf("AdvanceMediaTime with clock skew = %d, want 1000", got)
	}
}

func TestSyncStateApplyCommandPlay(t *testing.T) {
	t.Parallel()
	now := time.Now()
	s := SyncState{
		MediaTimeMs:  5000,
		WallTimeMs:   now.UnixMilli() - 10000,
		PlaybackRate: 0,
		Status:       StatusPaused,
		Epoch:        5,
	}
	s.ApplyCommand(CmdPlay, nil, nil, "", now.UnixMilli(), now)
	if s.Status != StatusPlaying {
		t.Errorf("status should be Playing; got %v", s.Status)
	}
	if s.PlaybackRate != 1.0 {
		t.Errorf("rate should be 1.0; got %f", s.PlaybackRate)
	}
	if s.Epoch != 6 {
		t.Errorf("epoch should bump to 6; got %d", s.Epoch)
	}
}

func TestSyncStateApplyCommandPauseRetainsPosition(t *testing.T) {
	t.Parallel()
	now := time.Now()
	s := SyncState{
		MediaTimeMs:  0,
		WallTimeMs:   now.UnixMilli() - 5000,
		PlaybackRate: 1.0,
		Status:       StatusPlaying,
		Epoch:        3,
	}
	// Before pausing, advance the media time.
	s.ApplyCommand(CmdPause, nil, nil, "", now.UnixMilli(), now)
	if s.Status != StatusPaused {
		t.Errorf("status should be Paused; got %v", s.Status)
	}
	if s.PlaybackRate != 0 {
		t.Errorf("rate should be 0 when paused; got %f", s.PlaybackRate)
	}
	// Media time should have advanced ~5000ms (5s at 1.0x).
	if s.MediaTimeMs < 4000 || s.MediaTimeMs > 6000 {
		t.Errorf("media time should be ~5000; got %d", s.MediaTimeMs)
	}
}

func TestSyncStateApplyCommandSeek(t *testing.T) {
	t.Parallel()
	now := time.Now()
	s := SyncState{Epoch: 1}
	target := int64(42000)
	s.ApplyCommand(CmdSeek, &target, nil, "", now.UnixMilli(), now)
	if s.MediaTimeMs != 42000 {
		t.Errorf("media time should be 42000; got %d", s.MediaTimeMs)
	}
	if s.Epoch != 2 {
		t.Errorf("epoch should bump; got %d", s.Epoch)
	}
}

func TestSyncStateApplyCommandSetRate(t *testing.T) {
	t.Parallel()
	now := time.Now()
	s := SyncState{Epoch: 1, Status: StatusPaused, PlaybackRate: 0}
	rate := 2.0
	s.ApplyCommand(CmdSetRate, nil, &rate, "", now.UnixMilli(), now)
	if s.PlaybackRate != 2.0 {
		t.Errorf("rate should be 2.0; got %f", s.PlaybackRate)
	}
	if s.Status != StatusPlaying {
		t.Errorf("non-zero rate should set status to Playing; got %v", s.Status)
	}
}

func TestSyncStateApplyCommandSetRateOutOfRange(t *testing.T) {
	t.Parallel()
	now := time.Now()
	s := SyncState{PlaybackRate: 1.0}
	rate := 5.0 // above max of 4.0
	s.ApplyCommand(CmdSetRate, nil, &rate, "", now.UnixMilli(), now)
	if s.PlaybackRate == 5.0 {
		t.Error("rate above 4.0 should be rejected")
	}
}

func TestSyncStateApplyCommandLoadMedia(t *testing.T) {
	t.Parallel()
	now := time.Now()
	s := SyncState{MediaTimeMs: 30000, Status: StatusPlaying, PlaybackRate: 1.0, Epoch: 5}
	s.ApplyCommand(CmdLoadMedia, nil, nil, "media_123", now.UnixMilli(), now)
	if s.MediaID != "media_123" {
		t.Errorf("media_id should be set; got %q", s.MediaID)
	}
	if s.MediaTimeMs != 0 {
		t.Errorf("media time should reset to 0; got %d", s.MediaTimeMs)
	}
	if s.Status != StatusPaused {
		t.Errorf("load_media should pause; got %v", s.Status)
	}
}
