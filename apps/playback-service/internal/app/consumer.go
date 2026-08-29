package app

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	vbkafka "vibesync/libs/kafka"

	"vibesync/apps/playback-service/internal/domain"
)

// SyncUpdatedHandler consumes sync.updated.v1 events and updates the room
// cache with the latest authoritative state from the Sync Service.
type SyncUpdatedHandler struct {
	cache  *RoomCache
	logger *slog.Logger
}

// NewSyncUpdatedHandler constructs the consumer handler.
func NewSyncUpdatedHandler(cache *RoomCache, logger *slog.Logger) *SyncUpdatedHandler {
	return &SyncUpdatedHandler{cache: cache, logger: logger}
}

// Handle deserializes the event and updates the cache.
func (h *SyncUpdatedHandler) Handle(_ context.Context, msg vbkafka.Message) error {
	var event domain.SyncUpdatedV1
	if err := json.Unmarshal(msg.Value, &event); err != nil {
		h.logger.Error("consumer: malformed sync.updated.v1 payload, skipping",
			"err", err, "topic", msg.Topic, "offset", msg.Offset)
		return nil // skip poison messages
	}
	if event.RoomID == "" {
		h.logger.Error("consumer: invalid sync.updated.v1 event, skipping",
			"room_id", event.RoomID)
		return nil
	}
	h.cache.ApplyFromEvent(event, time.Now().UTC())
	h.logger.Debug("consumer: updated playback cache",
		"room_id", event.RoomID, "epoch", event.Epoch)
	return nil
}

// Compile-time check.
var _ vbkafka.Handler = (*SyncUpdatedHandler)(nil)
