package domain

import (
	"testing"
	"time"
)

func TestApplySyncCommandAcceptsNewerEpoch(t *testing.T) {
	t.Parallel()
	now := time.Now()
	room := PlaybackRoom{RoomID: "r1", Epoch: 5, FencingToken: 3}
	applied := room.ApplySyncCommand("media1", 2, 1000, now.UnixMilli(), 1.0, 6, "host1", 3, now)
	if !applied {
		t.Error("should apply command with newer epoch + same fencing token")
	}
	if room.Epoch != 6 {
		t.Errorf("epoch = %d, want 6", room.Epoch)
	}
}

func TestApplySyncCommandRejectsStaleFencingToken(t *testing.T) {
	t.Parallel()
	now := time.Now()
	room := PlaybackRoom{RoomID: "r1", Epoch: 5, FencingToken: 5}
	// Command with token 3 < current 5 → rejected.
	applied := room.ApplySyncCommand("media1", 2, 1000, now.UnixMilli(), 1.0, 10, "host2", 3, now)
	if applied {
		t.Error("should reject command with stale fencing token")
	}
	if room.FencingToken != 5 {
		t.Errorf("fencing_token should be unchanged; got %d", room.FencingToken)
	}
}

func TestApplySyncCommandAcceptsHigherFencingToken(t *testing.T) {
	t.Parallel()
	now := time.Now()
	room := PlaybackRoom{RoomID: "r1", Epoch: 5, FencingToken: 3}
	// Command with token 5 > current 3 (host migration happened) → accepted.
	applied := room.ApplySyncCommand("media1", 2, 2000, now.UnixMilli(), 1.0, 6, "host2", 5, now)
	if !applied {
		t.Error("should accept command with higher fencing token")
	}
	if room.FencingToken != 5 {
		t.Errorf("fencing_token = %d, want 5", room.FencingToken)
	}
	if room.HostID != "host2" {
		t.Errorf("host_id = %q, want host2", room.HostID)
	}
}

func TestApplySyncCommandRejectsSameEpochSameToken(t *testing.T) {
	t.Parallel()
	now := time.Now()
	room := PlaybackRoom{RoomID: "r1", Epoch: 5, FencingToken: 3}
	// Same epoch + same token → redundant → rejected.
	applied := room.ApplySyncCommand("media1", 2, 1000, now.UnixMilli(), 1.0, 5, "host1", 3, now)
	if applied {
		t.Error("should reject redundant command (same epoch + same token)")
	}
}

func TestApplySyncCommandFirstCommandAccepted(t *testing.T) {
	t.Parallel()
	now := time.Now()
	room := PlaybackRoom{RoomID: "r1", Epoch: 0, FencingToken: 0}
	applied := room.ApplySyncCommand("media1", 2, 0, now.UnixMilli(), 1.0, 1, "host1", 0, now)
	if !applied {
		t.Error("first command should be accepted")
	}
}

func TestApplyFromEventUpdatesState(t *testing.T) {
	t.Parallel()
	now := time.Now()
	room := PlaybackRoom{RoomID: "r1", Epoch: 0, FencingToken: 5}
	event := SyncUpdatedV1{
		RoomID: "r1", Epoch: 3, Status: 2, MediaTime: 5000,
		WallTime: 10000, Rate: 1.5, HostID: "host1",
	}
	advanced := room.ApplyFromEvent(event, now)
	if !advanced {
		t.Error("should report epoch advanced (0 → 3)")
	}
	if room.Epoch != 3 {
		t.Errorf("epoch = %d, want 3", room.Epoch)
	}
	if room.Status != 2 {
		t.Errorf("status = %d, want 2", room.Status)
	}
	if room.PlaybackRate != 1.5 {
		t.Errorf("rate = %f, want 1.5", room.PlaybackRate)
	}
	// Fencing token should NOT be overwritten by events (only by ApplySyncCommand).
	if room.FencingToken != 5 {
		t.Errorf("fencing_token should be preserved; got %d", room.FencingToken)
	}
}

func TestApplyFromEventSameEpochNotAdvanced(t *testing.T) {
	t.Parallel()
	now := time.Now()
	room := PlaybackRoom{RoomID: "r1", Epoch: 5}
	event := SyncUpdatedV1{RoomID: "r1", Epoch: 5}
	advanced := room.ApplyFromEvent(event, now)
	if advanced {
		t.Error("should report not advanced when epoch is same")
	}
}

func TestLoadMedia(t *testing.T) {
	t.Parallel()
	now := time.Now()
	room := PlaybackRoom{RoomID: "r1", MediaID: "old"}
	room.LoadMedia("new_media", now)
	if room.MediaID != "new_media" {
		t.Errorf("media_id = %q, want new_media", room.MediaID)
	}
}
